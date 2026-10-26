package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/rendermanifest"
)

type RenderManifestReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// RenderManifestEvidence reads authoritative records through the current write
// transaction. All three checks fail closed when a record is absent or stale.
type RenderManifestEvidence interface {
	SceneRevision(context.Context, RenderManifestReader, string, string) (int64, error)
	ObjectInput(context.Context, RenderManifestReader, string, string, rendermanifest.Input) (bool, error)
	ReadyAsset(context.Context, RenderManifestReader, string, string, string) (int64, string, bool, error)
}

type SQLiteRenderManifestEvidence struct{}

func (SQLiteRenderManifestEvidence) SceneRevision(ctx context.Context, q RenderManifestReader, owner, session string) (int64, error) {
	var rev int64
	err := q.QueryRowContext(ctx, `SELECT revision FROM scene_revisions WHERE owner_id=? AND session_id=?`, owner, session).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, rendermanifest.ErrNotFound
	}
	return rev, err
}
func (SQLiteRenderManifestEvidence) ObjectInput(ctx context.Context, q RenderManifestReader, owner, session string, in rendermanifest.Input) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM scene_objects o JOIN scene_representations r
		ON r.id=o.active_representation_id AND r.object_id=o.id
		WHERE o.id=? AND o.owner_id=? AND o.session_id=? AND o.revision=? AND o.deleted_at IS NULL
		AND r.id=? AND r.asset_id=?`, in.ObjectID, owner, session, in.ObjectRevision, in.RepresentationID, in.AssetID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
func (SQLiteRenderManifestEvidence) ReadyAsset(ctx context.Context, q RenderManifestReader, owner, session, id string) (int64, string, bool, error) {
	var size int64
	var digest string
	err := q.QueryRowContext(ctx, `SELECT size_bytes,sha256 FROM generation_input_assets
		WHERE id=? AND owner_id=? AND storage_key<>'' AND size_bytes>0 AND sha256<>''
		UNION ALL SELECT a.size_bytes,a.sha256 FROM generation_assets a JOIN generation_tasks t ON t.id=a.task_id
		WHERE a.id=? AND t.owner_id=? AND t.session_id=? AND t.state='SUCCEEDED'
		AND a.storage_key<>'' AND a.size_bytes>0 AND a.sha256<>''`, id, owner, id, owner, session).Scan(&size, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	return size, digest, true, nil
}

type RenderManifestRepositoryConfig struct {
	Bytes          rendermanifest.AssetBytes
	Verifier       rendermanifest.RenderVerifier
	Evidence       RenderManifestEvidence
	MaxOutputBytes int64
}
type RenderManifestRepository struct {
	db  *sql.DB
	cfg RenderManifestRepositoryConfig
}

func (r *Repository) RenderManifestRepository(cfg RenderManifestRepositoryConfig) (*RenderManifestRepository, error) {
	if cfg.Bytes == nil || cfg.Verifier == nil || cfg.Evidence == nil || cfg.MaxOutputBytes <= 0 {
		return nil, fmt.Errorf("%w: missing render proof dependency", rendermanifest.ErrInvalid)
	}
	return &RenderManifestRepository{db: r.db, cfg: cfg}, nil
}

func validManifestID(s string) bool { return s != "" && strings.TrimSpace(s) == s }
func validDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			if c < 'a' || c > 'f' {
				return false
			}
		}
	}
	return true
}
func near(a, b float64) bool { return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b)) }
func validFrame(f rendermanifest.Frame) bool {
	for _, v := range []float64{f.X, f.Y, f.Width, f.Height, f.Matrix[0], f.Matrix[1], f.Matrix[2], f.Matrix[3], f.Matrix[4], f.Matrix[5]} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	if f.Width <= 0 || f.Height <= 0 || f.PixelWidth <= 0 || f.PixelHeight <= 0 {
		return false
	}
	sx, sy := float64(f.PixelWidth)/f.Width, float64(f.PixelHeight)/f.Height
	return near(f.Matrix[0], sx) && near(f.Matrix[1], 0) && near(f.Matrix[2], 0) && near(f.Matrix[3], sy) && near(f.Matrix[4], -f.X*sx) && near(f.Matrix[5], -f.Y*sy)
}
func validManifest(m rendermanifest.Manifest, maxBytes int64) bool {
	if !validManifestID(m.ID) || !validManifestID(m.OwnerID) || !validManifestID(m.SessionID) || !validManifestID(m.RendererVersion) || m.SceneRevision < 1 || !validFrame(m.Frame) || len(m.Inputs) == 0 || len(m.Inputs) > 50 {
		return false
	}
	if !validManifestID(m.Output.AssetID) || m.Output.SizeBytes <= 0 || m.Output.SizeBytes > maxBytes || !validDigest(m.Output.SHA256) {
		return false
	}
	previous := ""
	for _, in := range m.Inputs {
		if !validManifestID(in.ObjectID) || in.ObjectID <= previous || in.ObjectRevision < 1 || !validManifestID(in.RepresentationID) || !validManifestID(in.AssetID) || !validDigest(in.AssetSHA256) {
			return false
		}
		previous = in.ObjectID
	}
	return true
}

// Create hashes the already-uploaded output bytes and obtains trusted render
// proof before opening the write transaction. Admission then checks all
// scene/object revisions and asset records in one SQLite snapshot. Reading
// bytes outside the transaction lets an AssetBytes adapter use the same DB.
func (r *RenderManifestRepository) Create(ctx context.Context, m rendermanifest.Manifest) (rendermanifest.Manifest, error) {
	if !validManifest(m, r.cfg.MaxOutputBytes) {
		return rendermanifest.Manifest{}, rendermanifest.ErrInvalid
	}
	body, err := r.cfg.Bytes.OpenOwned(ctx, m.OwnerID, m.Output.AssetID)
	if err != nil {
		return rendermanifest.Manifest{}, fmt.Errorf("%w: %v", rendermanifest.ErrNotFound, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(body, m.Output.SizeBytes+1))
	closeErr := body.Close()
	if readErr != nil {
		return rendermanifest.Manifest{}, readErr
	}
	if closeErr != nil {
		return rendermanifest.Manifest{}, closeErr
	}
	hash := sha256.Sum256(data)
	if int64(len(data)) != m.Output.SizeBytes || hex.EncodeToString(hash[:]) != m.Output.SHA256 {
		return rendermanifest.Manifest{}, rendermanifest.ErrMismatch
	}
	if err = r.cfg.Verifier.Verify(ctx, m, data); err != nil {
		return rendermanifest.Manifest{}, fmt.Errorf("%w: %v", rendermanifest.ErrMismatch, err)
	}
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return rendermanifest.Manifest{}, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return rendermanifest.Manifest{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	var exists int
	err = conn.QueryRowContext(ctx, `SELECT 1 FROM render_manifests WHERE id=?`, m.ID).Scan(&exists)
	if err == nil {
		return rendermanifest.Manifest{}, rendermanifest.ErrConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return rendermanifest.Manifest{}, err
	}
	rev, err := r.cfg.Evidence.SceneRevision(ctx, conn, m.OwnerID, m.SessionID)
	if err != nil {
		return rendermanifest.Manifest{}, err
	}
	if rev != m.SceneRevision {
		return rendermanifest.Manifest{}, rendermanifest.ErrDrift
	}
	for _, in := range m.Inputs {
		ok, e := r.cfg.Evidence.ObjectInput(ctx, conn, m.OwnerID, m.SessionID, in)
		if e != nil {
			return rendermanifest.Manifest{}, e
		}
		if !ok {
			return rendermanifest.Manifest{}, rendermanifest.ErrDrift
		}
		_, digest, ready, e := r.cfg.Evidence.ReadyAsset(ctx, conn, m.OwnerID, m.SessionID, in.AssetID)
		if e != nil {
			return rendermanifest.Manifest{}, e
		}
		if !ready {
			return rendermanifest.Manifest{}, rendermanifest.ErrNotFound
		}
		if digest != in.AssetSHA256 {
			return rendermanifest.Manifest{}, rendermanifest.ErrMismatch
		}
	}
	size, digest, ready, err := r.cfg.Evidence.ReadyAsset(ctx, conn, m.OwnerID, m.SessionID, m.Output.AssetID)
	if err != nil {
		return rendermanifest.Manifest{}, err
	}
	if !ready {
		return rendermanifest.Manifest{}, rendermanifest.ErrNotFound
	}
	if size != m.Output.SizeBytes || digest != m.Output.SHA256 {
		return rendermanifest.Manifest{}, rendermanifest.ErrMismatch
	}
	inputsJSON, err := json.Marshal(m.Inputs)
	if err != nil {
		return rendermanifest.Manifest{}, err
	}
	matrixJSON, err := json.Marshal(m.Frame.Matrix)
	if err != nil {
		return rendermanifest.Manifest{}, err
	}
	m.CreatedAt = time.Now().UTC()
	_, err = conn.ExecContext(ctx, `INSERT INTO render_manifests (id,owner_id,session_id,scene_revision,frame_x,frame_y,frame_width,frame_height,pixel_width,pixel_height,matrix_json,inputs_json,renderer_version,output_asset_id,output_size_bytes,output_sha256,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, m.ID, m.OwnerID, m.SessionID, m.SceneRevision, m.Frame.X, m.Frame.Y, m.Frame.Width, m.Frame.Height, m.Frame.PixelWidth, m.Frame.PixelHeight, string(matrixJSON), string(inputsJSON), m.RendererVersion, m.Output.AssetID, m.Output.SizeBytes, m.Output.SHA256, formatTime(m.CreatedAt))
	if err != nil {
		return rendermanifest.Manifest{}, err
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return rendermanifest.Manifest{}, err
	}
	committed = true
	return m, nil
}

func (r *RenderManifestRepository) Get(ctx context.Context, owner, session, id string) (rendermanifest.Manifest, error) {
	if !validManifestID(owner) || !validManifestID(session) || !validManifestID(id) {
		return rendermanifest.Manifest{}, rendermanifest.ErrInvalid
	}
	var m rendermanifest.Manifest
	var inputsJSON, matrixJSON, created string
	err := r.db.QueryRowContext(ctx, `SELECT id,owner_id,session_id,scene_revision,frame_x,frame_y,frame_width,frame_height,pixel_width,pixel_height,matrix_json,inputs_json,renderer_version,output_asset_id,output_size_bytes,output_sha256,created_at FROM render_manifests WHERE id=? AND owner_id=? AND session_id=?`, id, owner, session).Scan(&m.ID, &m.OwnerID, &m.SessionID, &m.SceneRevision, &m.Frame.X, &m.Frame.Y, &m.Frame.Width, &m.Frame.Height, &m.Frame.PixelWidth, &m.Frame.PixelHeight, &matrixJSON, &inputsJSON, &m.RendererVersion, &m.Output.AssetID, &m.Output.SizeBytes, &m.Output.SHA256, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return rendermanifest.Manifest{}, rendermanifest.ErrNotFound
	}
	if err != nil {
		return rendermanifest.Manifest{}, err
	}
	if err = json.Unmarshal([]byte(matrixJSON), &m.Frame.Matrix); err != nil {
		return rendermanifest.Manifest{}, err
	}
	if err = json.Unmarshal([]byte(inputsJSON), &m.Inputs); err != nil {
		return rendermanifest.Manifest{}, err
	}
	m.CreatedAt, err = parseTime(created)
	return m, err
}
