package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/scene"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// SceneReader is scoped to the same SQLite write transaction as the mutation.
type SceneReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// SceneEvidence is an injected read-only admission port. Implementations must
// use reader, so ownership and readiness are checked in the mutation snapshot.
type SceneEvidence interface {
	SessionOwned(context.Context, SceneReader, string, string) (bool, error)
	AssetReady(context.Context, SceneReader, string, string, string) (bool, error)
	TaskSource(context.Context, SceneReader, string, string, string, string) (bool, error)
	PreparedInputOwned(context.Context, SceneReader, string, string, string, string) (bool, error)
}

type SceneRepository struct {
	db                 *sql.DB
	evidence           SceneEvidence
	reconcileMu        sync.Mutex
	reconcileUpdatedAt string
	reconcileTaskID    string
}

func (r *Repository) SceneRepository(evidence SceneEvidence) (*SceneRepository, error) {
	if evidence == nil {
		return nil, fmt.Errorf("%w: scene evidence required", scene.ErrInvalid)
	}
	return &SceneRepository{db: r.db, evidence: evidence}, nil
}

// SQLiteSceneEvidence validates existing platform facts without changing them.
type SQLiteSceneEvidence struct{}

func (SQLiteSceneEvidence) SessionOwned(ctx context.Context, q SceneReader, owner, session string) (bool, error) {
	return sceneExists(ctx, q, `SELECT 1 FROM go_chat_sessions WHERE id=? AND owner_id=?`, session, owner)
}
func (SQLiteSceneEvidence) AssetReady(ctx context.Context, q SceneReader, owner, session, asset string) (bool, error) {
	// Input assets have already been materialized when their immutable storage
	// identity and digest exist. Output assets additionally require a terminal
	// task in the same session; a remote claim or URL is not a READY asset.
	// Gaussian Splat is a separate preview-only representation. It cannot be
	// admitted under any existing Scene kind, even with a forged kind label.
	return sceneExists(ctx, q, `SELECT 1 FROM generation_input_assets WHERE id=? AND owner_id=? AND storage_key<>'' AND sha256<>'' AND media_type<>?
		UNION ALL SELECT 1 FROM generation_assets a JOIN generation_tasks t ON t.id=a.task_id
		WHERE a.id=? AND t.owner_id=? AND t.session_id=? AND t.state='SUCCEEDED'
		AND a.storage_key<>'' AND a.sha256<>'' AND a.delivery_profile<>? AND a.role<>? AND a.media_type<>?`,
		asset, owner, schema.GaussianSplatMediaType, asset, owner, session,
		schema.DeliveryProfileGaussianSplat32V1, schema.GaussianSplatRole, schema.GaussianSplatMediaType)
}
func (SQLiteSceneEvidence) TaskSource(ctx context.Context, q SceneReader, owner, session, task, asset string) (bool, error) {
	return sceneExists(ctx, q, `SELECT 1 FROM generation_tasks t JOIN generation_assets a ON a.task_id=t.id
		WHERE t.id=? AND t.owner_id=? AND t.session_id=? AND t.state='SUCCEEDED' AND a.id=?`, task, owner, session, asset)
}
func (SQLiteSceneEvidence) PreparedInputOwned(ctx context.Context, q SceneReader, owner, session, prepared, task string) (bool, error) {
	if task == "" {
		return sceneExists(ctx, q, `SELECT 1 FROM generation_prepared_inputs WHERE id=? AND owner_id=? AND session_id=?`, prepared, owner, session)
	}
	return sceneExists(ctx, q, `SELECT 1 FROM generation_prepared_inputs p JOIN execution_grants g ON g.prepared_input_id=p.id
		JOIN generation_tasks t ON t.id=g.task_id WHERE p.id=? AND p.owner_id=? AND p.session_id=?
		AND g.status='bound' AND g.prepared_input_hash=p.input_hash AND t.id=? AND t.owner_id=? AND t.session_id=?`, prepared, owner, session, task, owner, session)
}
func sceneExists(ctx context.Context, q SceneReader, query string, args ...any) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, query, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func validSceneID(s string) bool { return strings.TrimSpace(s) == s && s != "" }
func validGeometry(rect scene.Rect, anchor scene.Anchor) bool {
	for _, n := range []float64{rect.X, rect.Y, rect.Width, rect.Height, anchor.X, anchor.Y} {
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return false
		}
	}
	return rect.Width > 0 && rect.Height > 0
}
func validRepresentation(rep scene.Representation) bool {
	if !validSceneID(rep.ID) || !validSceneID(rep.AssetID) || !validSceneID(rep.MetadataSchemaRevision) {
		return false
	}
	switch rep.Kind {
	case scene.Bitmap, scene.Vector, scene.Model3D, scene.Capture:
		return true
	}
	return false
}

// withSceneWrite reserves SQLite's writer lock before evidence reads. This
// serializes admission and CAS even across separate Repository instances.
func (r *SceneRepository) withSceneWrite(ctx context.Context, fn func(*sql.Conn) error) error {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if err = fn(conn); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

func (r *SceneRepository) Create(ctx context.Context, input scene.Create) (scene.Object, error) {
	obj, rep := input.Object, input.Initial
	if !validSceneID(obj.ID) || !validSceneID(obj.OwnerID) || !validSceneID(obj.SessionID) || !validGeometry(obj.Rect, obj.Anchor) || !validRepresentation(rep) || rep.ObjectID != obj.ID || rep.SourceRepresentationID != "" {
		return scene.Object{}, scene.ErrInvalid
	}
	// The first representation may originate from a task, but never from a
	// guessed predecessor representation. Legacy tasks without a session are
	// rejected by the evidence port rather than assigned one retroactively.
	err := r.withSceneWrite(ctx, func(conn *sql.Conn) error {
		ok, e := r.evidence.SessionOwned(ctx, conn, obj.OwnerID, obj.SessionID)
		if e != nil {
			return e
		}
		if !ok {
			return scene.ErrNotFound
		}
		if e = r.checkRepresentation(ctx, conn, obj.OwnerID, obj.SessionID, rep); e != nil {
			return e
		}
		now := time.Now().UTC()
		obj.Revision = 1
		obj.ActiveRepresentationID = rep.ID
		obj.DeletedAt = nil
		obj.CreatedAt = now
		obj.UpdatedAt = now
		rep.CreatedAt = now
		_, e = conn.ExecContext(ctx, `INSERT INTO scene_objects (id,owner_id,session_id,revision,active_representation_id,rect_x,rect_y,rect_width,rect_height,anchor_x,anchor_y,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, obj.ID, obj.OwnerID, obj.SessionID, 1, rep.ID, obj.Rect.X, obj.Rect.Y, obj.Rect.Width, obj.Rect.Height, obj.Anchor.X, obj.Anchor.Y, formatTime(now), formatTime(now))
		if e != nil {
			return e
		}
		if e = insertSceneRepresentation(ctx, conn, rep); e != nil {
			return e
		}
		return bumpSceneRevision(ctx, conn, obj.OwnerID, obj.SessionID, true)
	})
	return obj, err
}

func (r *SceneRepository) checkRepresentation(ctx context.Context, q SceneReader, owner, session string, rep scene.Representation) error {
	if !validRepresentation(rep) {
		return scene.ErrInvalid
	}
	ok, err := r.evidence.AssetReady(ctx, q, owner, session, rep.AssetID)
	if err != nil {
		return err
	}
	if !ok {
		return scene.ErrNotFound
	}
	if rep.SourceTaskID != "" {
		ok, err = r.evidence.TaskSource(ctx, q, owner, session, rep.SourceTaskID, rep.AssetID)
		if err != nil {
			return err
		}
		if !ok {
			return scene.ErrNotFound
		}
	}
	if rep.PreparedInputID != "" {
		ok, err = r.evidence.PreparedInputOwned(ctx, q, owner, session, rep.PreparedInputID, rep.SourceTaskID)
		if err != nil {
			return err
		}
		if !ok {
			return scene.ErrNotFound
		}
	}
	return nil
}

func insertSceneRepresentation(ctx context.Context, conn *sql.Conn, rep scene.Representation) error {
	_, err := conn.ExecContext(ctx, `INSERT INTO scene_representations (id,object_id,kind,asset_id,source_task_id,source_representation_id,prepared_input_id,metadata_schema_revision,created_at) VALUES (?,?,?,?,?,?,?,?,?)`, rep.ID, rep.ObjectID, rep.Kind, rep.AssetID, nullSceneString(rep.SourceTaskID), nullSceneString(rep.SourceRepresentationID), nullSceneString(rep.PreparedInputID), rep.MetadataSchemaRevision, formatTime(rep.CreatedAt))
	return err
}
func nullSceneString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (r *SceneRepository) RegisterCandidate(ctx context.Context, input scene.Candidate) (scene.Representation, error) {
	rep := input.Representation
	if !validSceneID(input.OwnerID) || !validSceneID(input.SessionID) || !validSceneID(input.ObjectID) || !validRepresentation(rep) || rep.ObjectID != input.ObjectID {
		return scene.Representation{}, scene.ErrInvalid
	}
	if rep.SourceTaskID != "" && rep.PreparedInputID != "" && input.FrozenSourceRevision == 0 {
		return scene.Representation{}, scene.ErrInvalid
	}
	err := r.withSceneWrite(ctx, func(conn *sql.Conn) error {
		if input.Idempotent {
			existing, e := readSceneRepresentation(ctx, conn, input.OwnerID, input.SessionID, input.ObjectID, rep.ID)
			if e == nil {
				if existing.ObjectID != rep.ObjectID || existing.Kind != rep.Kind || existing.AssetID != rep.AssetID ||
					existing.SourceTaskID != rep.SourceTaskID || existing.SourceRepresentationID != rep.SourceRepresentationID ||
					existing.PreparedInputID != rep.PreparedInputID || existing.MetadataSchemaRevision != rep.MetadataSchemaRevision {
					return scene.ErrConflict
				}
				rep = existing
				return nil
			}
			if !errors.Is(e, scene.ErrNotFound) {
				return e
			}
		}
		object, err := readSceneObject(ctx, conn, input.OwnerID, input.SessionID, input.ObjectID)
		if err != nil {
			return err
		}
		if object.DeletedAt != nil {
			return scene.ErrConflict
		}
		if input.ExpectedObjectRevision != 0 &&
			(object.Revision != input.ExpectedObjectRevision || object.ActiveRepresentationID != input.ExpectedSourceRepresentationID) {
			return scene.ErrConflict
		}
		if input.FrozenSourceRevision != 0 {
			// The task candidate must descend from the exact frozen source asset
			// and the prepared input bound to the accepted image-to-3d Task.
			if rep.Kind != scene.Model3D || rep.SourceTaskID == "" || rep.PreparedInputID == "" ||
				input.FrozenSourceRepresentationID == "" || rep.SourceRepresentationID != input.FrozenSourceRepresentationID {
				return scene.ErrInvalid
			}
			var payload string
			var sourceAsset, sourceKind, capability string
			e := conn.QueryRowContext(ctx, `SELECT p.payload_json,r.asset_id,r.kind,t.capability
				FROM generation_prepared_inputs p JOIN scene_representations r ON r.id=? AND r.object_id=?
				JOIN generation_tasks t ON t.id=? AND t.owner_id=? AND t.session_id=?
				JOIN execution_grants g ON g.task_id=t.id AND g.prepared_input_id=p.id AND g.prepared_input_hash=p.input_hash AND g.status='bound'
				WHERE p.id=? AND p.owner_id=? AND p.session_id=?`,
				input.FrozenSourceRepresentationID, object.ID, rep.SourceTaskID, input.OwnerID, input.SessionID,
				rep.PreparedInputID, input.OwnerID, input.SessionID).Scan(&payload, &sourceAsset, &sourceKind, &capability)
			if errors.Is(e, sql.ErrNoRows) {
				return scene.ErrNotFound
			}
			if e != nil {
				return e
			}
			var prepared generation.PreparedInput
			if json.Unmarshal([]byte(payload), &prepared) != nil || prepared.SourceSceneObjectID != object.ID ||
				prepared.SourceSceneRevision != input.FrozenSourceRevision ||
				prepared.SourceSceneRepresentationID != input.FrozenSourceRepresentationID ||
				sourceKind != string(scene.Bitmap) || capability != string(generation.ImageTo3D) {
				return scene.ErrConflict
			}
			id, e := scene.FrozenSourceAsset(prepared.NormalizedInput)
			if e != nil || id != sourceAsset {
				return scene.ErrConflict
			}
		}
		if err = r.checkRepresentation(ctx, conn, input.OwnerID, input.SessionID, rep); err != nil {
			return err
		}
		if rep.SourceRepresentationID != "" {
			ok, e := sceneExists(ctx, conn, `SELECT 1 FROM scene_representations WHERE id=? AND object_id=?`, rep.SourceRepresentationID, input.ObjectID)
			if e != nil {
				return e
			}
			if !ok {
				return scene.ErrNotFound
			}
		}
		rep.CreatedAt = time.Now().UTC()
		if err = insertSceneRepresentation(ctx, conn, rep); err != nil {
			return err
		}
		return bumpSceneRevision(ctx, conn, input.OwnerID, input.SessionID, false)
	})
	return rep, err
}

func readSceneObject(ctx context.Context, q SceneReader, owner, session, id string) (scene.Object, error) {
	var obj scene.Object
	var deleted sql.NullString
	var created, updated string
	err := q.QueryRowContext(ctx, `SELECT id,owner_id,session_id,revision,active_representation_id,rect_x,rect_y,rect_width,rect_height,anchor_x,anchor_y,deleted_at,created_at,updated_at FROM scene_objects WHERE id=? AND owner_id=? AND session_id=?`, id, owner, session).Scan(&obj.ID, &obj.OwnerID, &obj.SessionID, &obj.Revision, &obj.ActiveRepresentationID, &obj.Rect.X, &obj.Rect.Y, &obj.Rect.Width, &obj.Rect.Height, &obj.Anchor.X, &obj.Anchor.Y, &deleted, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return scene.Object{}, scene.ErrNotFound
	}
	if err != nil {
		return scene.Object{}, err
	}
	obj.CreatedAt, err = parseTime(created)
	if err != nil {
		return scene.Object{}, err
	}
	obj.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return scene.Object{}, err
	}
	if deleted.Valid {
		t, e := parseTime(deleted.String)
		if e != nil {
			return scene.Object{}, e
		}
		obj.DeletedAt = &t
	}
	return obj, nil
}
func (r *SceneRepository) Get(ctx context.Context, owner, session, id string) (scene.Object, error) {
	if !validSceneID(owner) || !validSceneID(session) || !validSceneID(id) {
		return scene.Object{}, scene.ErrInvalid
	}
	return readSceneObject(ctx, r.db, owner, session, id)
}

func (r *SceneRepository) GetRepresentation(ctx context.Context, owner, session, objectID, representationID string) (scene.Representation, error) {
	if !validSceneID(owner) || !validSceneID(session) || !validSceneID(objectID) || !validSceneID(representationID) {
		return scene.Representation{}, scene.ErrInvalid
	}
	return readSceneRepresentation(ctx, r.db, owner, session, objectID, representationID)
}
func readSceneRepresentation(ctx context.Context, q SceneReader, owner, session, objectID, representationID string) (scene.Representation, error) {
	var rep scene.Representation
	var sourceTask, sourceRep, prepared sql.NullString
	var created string
	err := q.QueryRowContext(ctx, `SELECT r.id,r.object_id,r.kind,r.asset_id,r.source_task_id,r.source_representation_id,r.prepared_input_id,r.metadata_schema_revision,r.created_at
		FROM scene_representations r JOIN scene_objects o ON o.id=r.object_id
		WHERE r.id=? AND r.object_id=? AND o.owner_id=? AND o.session_id=?`, representationID, objectID, owner, session).
		Scan(&rep.ID, &rep.ObjectID, &rep.Kind, &rep.AssetID, &sourceTask, &sourceRep, &prepared, &rep.MetadataSchemaRevision, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return scene.Representation{}, scene.ErrNotFound
	}
	if err != nil {
		return scene.Representation{}, err
	}
	rep.SourceTaskID, rep.SourceRepresentationID, rep.PreparedInputID = sourceTask.String, sourceRep.String, prepared.String
	rep.CreatedAt, err = parseTime(created)
	return rep, err
}

func (r *SceneRepository) ListRepresentations(ctx context.Context, owner, session, id string) ([]scene.Representation, error) {
	if _, err := r.Get(ctx, owner, session, id); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,object_id,kind,asset_id,source_task_id,source_representation_id,prepared_input_id,metadata_schema_revision,created_at FROM scene_representations WHERE object_id=? ORDER BY created_at,id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []scene.Representation
	for rows.Next() {
		var v scene.Representation
		var task, source, prepared sql.NullString
		var created string
		if err = rows.Scan(&v.ID, &v.ObjectID, &v.Kind, &v.AssetID, &task, &source, &prepared, &v.MetadataSchemaRevision, &created); err != nil {
			return nil, err
		}
		v.SourceTaskID = task.String
		v.SourceRepresentationID = source.String
		v.PreparedInputID = prepared.String
		v.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	// created_at is variable-width RFC3339Nano text, whose byte order differs
	// from time order (".642662Z" < ".642Z"). Order by the parsed instant so
	// historical rows keep their lineage order; ID breaks exact ties.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Snapshot reads the revision and every active representation in one SQLite
// read transaction. A later manifest admission compares this revision again
// under BEGIN IMMEDIATE, rejecting any intervening scene mutation.
func (r *SceneRepository) Snapshot(ctx context.Context, owner, session string) (scene.Snapshot, error) {
	if !validSceneID(owner) || !validSceneID(session) {
		return scene.Snapshot{}, scene.ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return scene.Snapshot{}, err
	}
	defer tx.Rollback()
	snapshot := scene.Snapshot{OwnerID: owner, SessionID: session}
	err = tx.QueryRowContext(ctx, `SELECT revision FROM scene_revisions WHERE owner_id=? AND session_id=?`, owner, session).Scan(&snapshot.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return scene.Snapshot{}, scene.ErrNotFound
	}
	if err != nil {
		return scene.Snapshot{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT o.id,o.revision,o.active_representation_id,o.rect_x,o.rect_y,o.rect_width,o.rect_height,o.anchor_x,o.anchor_y,o.created_at,o.updated_at,
		r.id,r.kind,r.asset_id,r.source_task_id,r.source_representation_id,r.prepared_input_id,r.metadata_schema_revision,r.created_at
		FROM scene_objects o JOIN scene_representations r ON r.id=o.active_representation_id AND r.object_id=o.id
		WHERE o.owner_id=? AND o.session_id=? AND o.deleted_at IS NULL ORDER BY o.id`, owner, session)
	if err != nil {
		return scene.Snapshot{}, err
	}
	for rows.Next() {
		var item scene.SnapshotItem
		var objectCreated, objectUpdated, repCreated string
		var sourceTask, sourceRep, prepared sql.NullString
		err = rows.Scan(&item.Object.ID, &item.Object.Revision, &item.Object.ActiveRepresentationID,
			&item.Object.Rect.X, &item.Object.Rect.Y, &item.Object.Rect.Width, &item.Object.Rect.Height,
			&item.Object.Anchor.X, &item.Object.Anchor.Y, &objectCreated, &objectUpdated,
			&item.Representation.ID, &item.Representation.Kind, &item.Representation.AssetID,
			&sourceTask, &sourceRep, &prepared, &item.Representation.MetadataSchemaRevision, &repCreated)
		if err != nil {
			break
		}
		item.Object.OwnerID, item.Object.SessionID = owner, session
		item.Representation.ObjectID = item.Object.ID
		item.Representation.SourceTaskID, item.Representation.SourceRepresentationID = sourceTask.String, sourceRep.String
		item.Representation.PreparedInputID = prepared.String
		item.Object.CreatedAt, err = parseTime(objectCreated)
		if err != nil {
			break
		}
		item.Object.UpdatedAt, err = parseTime(objectUpdated)
		if err != nil {
			break
		}
		item.Representation.CreatedAt, err = parseTime(repCreated)
		if err != nil {
			break
		}
		snapshot.Items = append(snapshot.Items, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return scene.Snapshot{}, err
	}
	if err = tx.Commit(); err != nil {
		return scene.Snapshot{}, err
	}
	return snapshot, nil
}

func (r *SceneRepository) ApplyCandidate(ctx context.Context, m scene.Mutation, representationID string) (scene.Object, error) {
	if !validSceneID(representationID) {
		return scene.Object{}, scene.ErrInvalid
	}
	return r.mutate(ctx, m, func(conn *sql.Conn, obj scene.Object) error {
		ok, err := sceneExists(ctx, conn, `SELECT 1 FROM scene_representations WHERE id=? AND object_id=?`, representationID, obj.ID)
		if err != nil {
			return err
		}
		if !ok {
			return scene.ErrNotFound
		}
		_, err = conn.ExecContext(ctx, `UPDATE scene_objects SET active_representation_id=?,revision=revision+1,updated_at=? WHERE id=? AND owner_id=? AND session_id=? AND revision=? AND deleted_at IS NULL`, representationID, formatTime(time.Now().UTC()), obj.ID, obj.OwnerID, obj.SessionID, m.ExpectedRevision)
		return err
	})
}
func (r *SceneRepository) Move(ctx context.Context, m scene.Mutation, rect scene.Rect, anchor scene.Anchor) (scene.Object, error) {
	if !validGeometry(rect, anchor) {
		return scene.Object{}, scene.ErrInvalid
	}
	return r.mutate(ctx, m, func(conn *sql.Conn, obj scene.Object) error {
		_, err := conn.ExecContext(ctx, `UPDATE scene_objects SET rect_x=?,rect_y=?,rect_width=?,rect_height=?,anchor_x=?,anchor_y=?,revision=revision+1,updated_at=? WHERE id=? AND owner_id=? AND session_id=? AND revision=? AND deleted_at IS NULL`, rect.X, rect.Y, rect.Width, rect.Height, anchor.X, anchor.Y, formatTime(time.Now().UTC()), obj.ID, obj.OwnerID, obj.SessionID, m.ExpectedRevision)
		return err
	})
}
func (r *SceneRepository) Delete(ctx context.Context, m scene.Mutation) (scene.Object, error) {
	return r.mutate(ctx, m, func(conn *sql.Conn, obj scene.Object) error {
		now := formatTime(time.Now().UTC())
		_, err := conn.ExecContext(ctx, `UPDATE scene_objects SET deleted_at=?,revision=revision+1,updated_at=? WHERE id=? AND owner_id=? AND session_id=? AND revision=? AND deleted_at IS NULL`, now, now, obj.ID, obj.OwnerID, obj.SessionID, m.ExpectedRevision)
		return err
	})
}
func (r *SceneRepository) mutate(ctx context.Context, m scene.Mutation, fn func(*sql.Conn, scene.Object) error) (scene.Object, error) {
	if !validSceneID(m.OwnerID) || !validSceneID(m.SessionID) || !validSceneID(m.ObjectID) || m.ExpectedRevision < 1 {
		return scene.Object{}, scene.ErrInvalid
	}
	var updated scene.Object
	err := r.withSceneWrite(ctx, func(conn *sql.Conn) error {
		obj, e := readSceneObject(ctx, conn, m.OwnerID, m.SessionID, m.ObjectID)
		if e != nil {
			return e
		}
		if obj.Revision != m.ExpectedRevision || obj.DeletedAt != nil {
			return scene.ErrConflict
		}
		if e = fn(conn, obj); e != nil {
			return e
		}
		updated, e = readSceneObject(ctx, conn, m.OwnerID, m.SessionID, m.ObjectID)
		if e != nil {
			return e
		}
		if updated.Revision != m.ExpectedRevision+1 {
			return scene.ErrConflict
		}
		return bumpSceneRevision(ctx, conn, m.OwnerID, m.SessionID, false)
	})
	return updated, err
}

// bumpSceneRevision is part of the same BEGIN IMMEDIATE transaction as the
// object/representation change. A candidate changes the version history even
// when it does not become active, so consumers cannot freeze a stale list.
func bumpSceneRevision(ctx context.Context, conn *sql.Conn, owner, session string, create bool) error {
	now := formatTime(time.Now().UTC())
	var result sql.Result
	var err error
	if create {
		result, err = conn.ExecContext(ctx, `INSERT INTO scene_revisions(session_id,owner_id,revision,updated_at)
			VALUES (?,?,1,?) ON CONFLICT(session_id) DO UPDATE SET
			revision=scene_revisions.revision+1, updated_at=excluded.updated_at
			WHERE scene_revisions.owner_id=excluded.owner_id`, session, owner, now)
	} else {
		result, err = conn.ExecContext(ctx, `UPDATE scene_revisions SET revision=revision+1,updated_at=?
			WHERE session_id=? AND owner_id=?`, now, session, owner)
	}
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return scene.ErrNotFound
	}
	return nil
}
