package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/capture"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type CaptureRepositoryConfig struct {
	Bytes     capture.AssetBytes
	MaxBytes  int64
	MaxPixels int64
}

type CaptureRepository struct {
	db  *sql.DB
	cfg CaptureRepositoryConfig
}

func (r *Repository) CaptureRepository(cfg CaptureRepositoryConfig) (*CaptureRepository, error) {
	if cfg.Bytes == nil || cfg.MaxBytes < 1 || cfg.MaxPixels < 1 {
		return nil, fmt.Errorf("%w: missing capture byte reader or limits", capture.ErrInvalid)
	}
	return &CaptureRepository{db: r.db, cfg: cfg}, nil
}

func validCaptureToken(value string) bool {
	return value != "" && len(value) <= 200 && strings.TrimSpace(value) == value
}

func validCaptureView(view capture.View) bool {
	for _, text := range []string{view.CameraOrbit, view.CameraTarget, view.FieldOfView,
		view.Environment, view.RendererVersion} {
		if text == "" || len(text) > 300 || strings.TrimSpace(text) != text {
			return false
		}
	}
	return view.AnimationTimeMS >= 0 && len(view.AnimationName) <= 300
}

func captureCommandHash(cmd capture.Command) (string, error) {
	value := struct {
		OwnerID           string       `json:"owner_id"`
		SessionID         string       `json:"session_id"`
		SourceTaskID      string       `json:"source_task_id"`
		ModelAssetID      string       `json:"model_asset_id"`
		ScreenshotAssetID string       `json:"screenshot_asset_id"`
		View              capture.View `json:"view"`
	}{cmd.OwnerID, cmd.SessionID, cmd.SourceTaskID, cmd.ModelAssetID, cmd.ScreenshotAssetID, cmd.View}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func readCapture(ctx context.Context, q *sql.Conn, owner, key string, byID bool) (capture.Record, string, error) {
	field := "id"
	if !byID {
		field = "idempotency_key"
	}
	var record capture.Record
	var viewJSON, createdAt, commandHash string
	err := q.QueryRowContext(ctx, `SELECT id,owner_id,session_id,source_task_id,model_asset_id,
		screenshot_asset_id,idempotency_key,command_hash,view_json,pixel_width,pixel_height,
		size_bytes,sha256,created_at FROM model_captures WHERE owner_id=? AND `+field+`=?`,
		owner, key).Scan(&record.ID, &record.OwnerID, &record.SessionID, &record.SourceTaskID,
		&record.ModelAssetID, &record.ScreenshotAssetID, &record.IdempotencyKey,
		&commandHash, &viewJSON, &record.PixelWidth, &record.PixelHeight,
		&record.SizeBytes, &record.SHA256, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return capture.Record{}, "", capture.ErrNotFound
	}
	if err != nil {
		return capture.Record{}, "", err
	}
	if err = json.Unmarshal([]byte(viewJSON), &record.View); err != nil {
		return capture.Record{}, "", err
	}
	if record.CreatedAt, err = parseTime(createdAt); err != nil {
		return capture.Record{}, "", err
	}
	return record, commandHash, nil
}

func (r *CaptureRepository) readReplay(ctx context.Context, owner, key string) (capture.Record, string, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return capture.Record{}, "", err
	}
	defer conn.Close()
	return readCapture(ctx, conn, owner, key, false)
}

func (r *CaptureRepository) Record(ctx context.Context, cmd capture.Command) (capture.Record, error) {
	if !validCaptureToken(cmd.ID) || !validCaptureToken(cmd.OwnerID) ||
		!validCaptureToken(cmd.SessionID) || !validCaptureToken(cmd.SourceTaskID) ||
		!validCaptureToken(cmd.ModelAssetID) || !validCaptureToken(cmd.ScreenshotAssetID) ||
		!validCaptureToken(cmd.IdempotencyKey) || !validCaptureView(cmd.View) {
		return capture.Record{}, capture.ErrInvalid
	}
	commandHash, err := captureCommandHash(cmd)
	if err != nil {
		return capture.Record{}, err
	}
	// A previously committed capture is the first authority on replay. The
	// source Offering or asset may no longer be available in today's catalog.
	if existing, digest, e := r.readReplay(ctx, cmd.OwnerID, cmd.IdempotencyKey); e == nil {
		if digest != commandHash {
			return capture.Record{}, capture.ErrConflict
		}
		return existing, nil
	} else if !errors.Is(e, capture.ErrNotFound) {
		return capture.Record{}, e
	}

	body, err := r.cfg.Bytes.OpenOwned(ctx, cmd.OwnerID, cmd.ScreenshotAssetID)
	if err != nil {
		return capture.Record{}, fmt.Errorf("%w: screenshot unreadable", capture.ErrNotFound)
	}
	data, readErr := io.ReadAll(io.LimitReader(body, r.cfg.MaxBytes+1))
	closeErr := body.Close()
	if readErr != nil || closeErr != nil {
		return capture.Record{}, errors.Join(readErr, closeErr)
	}
	if int64(len(data)) > r.cfg.MaxBytes || len(data) == 0 {
		return capture.Record{}, capture.ErrInvalid
	}
	width, height, err := capturePNGDimensions(data, r.cfg.MaxPixels)
	if err != nil {
		return capture.Record{}, err
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])

	// OpenOwned may query the same SQLite database. Release the replay read
	// connection before opening bytes: production intentionally uses one DB
	// connection, so holding it here would self-deadlock every new capture.
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return capture.Record{}, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return capture.Record{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if existing, savedHash, e := readCapture(ctx, conn, cmd.OwnerID, cmd.IdempotencyKey, false); e == nil {
		if savedHash != commandHash {
			return capture.Record{}, capture.ErrConflict
		}
		return existing, nil
	} else if !errors.Is(e, capture.ErrNotFound) {
		return capture.Record{}, e
	}
	var owned int
	if err = conn.QueryRowContext(ctx, `SELECT 1 FROM go_chat_sessions WHERE id=? AND owner_id=?`,
		cmd.SessionID, cmd.OwnerID).Scan(&owned); err != nil {
		return capture.Record{}, capture.ErrNotFound
	}
	var modelMediaType, modelRole, modelVariant, offeringJSON string
	err = conn.QueryRowContext(ctx, `SELECT a.media_type,a.role,a.variant,t.offering_json FROM generation_assets a
		JOIN generation_tasks t ON t.id=a.task_id
		WHERE a.id=? AND a.task_id=? AND a.storage_key<>'' AND a.sha256<>''
		AND t.owner_id=? AND t.session_id=? AND t.state='SUCCEEDED'`, cmd.ModelAssetID,
		cmd.SourceTaskID, cmd.OwnerID, cmd.SessionID).Scan(&modelMediaType, &modelRole, &modelVariant, &offeringJSON)
	if err != nil || (modelMediaType != "model/gltf-binary" && modelMediaType != "model/gltf+json" && modelMediaType != "application/octet-stream") {
		return capture.Record{}, capture.ErrNotFound
	}
	var sourceOffering generation.Offering
	if json.Unmarshal([]byte(offeringJSON), &sourceOffering) != nil ||
		!generation.AssetMatchesOutput(sourceOffering.Outputs, modelRole, modelVariant, modelMediaType) {
		return capture.Record{}, capture.ErrNotFound
	}
	var size int64
	var storedDigest, mediaType string
	err = conn.QueryRowContext(ctx, `SELECT size_bytes,sha256,media_type FROM generation_input_assets
		WHERE id=? AND owner_id=? AND storage_key<>''`, cmd.ScreenshotAssetID,
		cmd.OwnerID).Scan(&size, &storedDigest, &mediaType)
	if err != nil {
		return capture.Record{}, capture.ErrNotFound
	}
	if mediaType != "image/png" || size != int64(len(data)) || !strings.EqualFold(storedDigest, digest) {
		return capture.Record{}, capture.ErrMismatch
	}
	viewJSON, err := json.Marshal(cmd.View)
	if err != nil {
		return capture.Record{}, err
	}
	createdAt := time.Now().UTC()
	_, err = conn.ExecContext(ctx, `INSERT INTO model_captures
		(id,owner_id,session_id,source_task_id,model_asset_id,screenshot_asset_id,
		idempotency_key,command_hash,view_json,pixel_width,pixel_height,size_bytes,sha256,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, cmd.ID, cmd.OwnerID, cmd.SessionID,
		cmd.SourceTaskID, cmd.ModelAssetID, cmd.ScreenshotAssetID, cmd.IdempotencyKey,
		commandHash, string(viewJSON), width, height, size, digest, formatTime(createdAt))
	if err != nil {
		return capture.Record{}, fmt.Errorf("%w: %v", capture.ErrConflict, err)
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return capture.Record{}, err
	}
	committed = true
	return capture.Record{Command: cmd, PixelWidth: width, PixelHeight: height,
		SizeBytes: size, SHA256: digest, CreatedAt: createdAt}, nil
}

func capturePNGDimensions(data []byte, maxPixels int64) (int, int, error) {
	configuration, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || configuration.Width < 1 || configuration.Height < 1 ||
		configuration.Width > 8192 || configuration.Height > 8192 ||
		int64(configuration.Width)*int64(configuration.Height) > maxPixels {
		return 0, 0, capture.ErrInvalid
	}
	if _, err = png.Decode(bytes.NewReader(data)); err != nil {
		return 0, 0, capture.ErrInvalid
	}
	return configuration.Width, configuration.Height, nil
}

func (r *CaptureRepository) Get(ctx context.Context, owner, id string) (capture.Record, error) {
	if !validCaptureToken(owner) || !validCaptureToken(id) {
		return capture.Record{}, capture.ErrInvalid
	}
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return capture.Record{}, err
	}
	defer conn.Close()
	record, _, err := readCapture(ctx, conn, owner, id, true)
	return record, err
}

// maximumSessionCaptures bounds one session listing; a session holds far fewer
// captures than this, and a longer one is read newest-first and cut.
const maximumSessionCaptures = 500

// ListSession returns the owner's captures of one session, oldest first. A
// foreign or unknown session yields an empty list, never another owner's rows.
func (r *CaptureRepository) ListSession(ctx context.Context, owner, session string) ([]capture.Record, error) {
	if !validCaptureToken(owner) || !validCaptureToken(session) {
		return nil, capture.ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,owner_id,session_id,source_task_id,model_asset_id,
		screenshot_asset_id,idempotency_key,view_json,pixel_width,pixel_height,size_bytes,sha256,created_at
		FROM model_captures WHERE owner_id=? AND session_id=? ORDER BY created_at DESC, id DESC LIMIT ?`,
		owner, session, maximumSessionCaptures)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []capture.Record
	for rows.Next() {
		var record capture.Record
		var viewJSON, createdAt string
		if err = rows.Scan(&record.ID, &record.OwnerID, &record.SessionID, &record.SourceTaskID,
			&record.ModelAssetID, &record.ScreenshotAssetID, &record.IdempotencyKey, &viewJSON,
			&record.PixelWidth, &record.PixelHeight, &record.SizeBytes, &record.SHA256, &createdAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(viewJSON), &record.View); err != nil {
			return nil, err
		}
		if record.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(records)-1; left < right; left, right = left+1, right-1 {
		records[left], records[right] = records[right], records[left]
	}
	return records, nil
}
