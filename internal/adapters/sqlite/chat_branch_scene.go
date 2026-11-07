package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/capture"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// branchCaptureCopy copies the source session's model captures into the new
// branch with fresh IDs and idempotency keys, and returns old→new IDs so the
// copied message attachments can point at the branch's own capture rows.
//
// A capture is immutable evidence. Copying it is a normal audit fact: the same
// asset/task is named by two sessions, each with its own record. Nothing is
// re-rendered, re-charged or re-validated against an upstream job.
func branchCaptureCopy(ctx context.Context, tx *sql.Tx, request chat.BranchRequest) (map[string]string, int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id,source_task_id,model_asset_id,screenshot_asset_id,
		       idempotency_key,view_json,pixel_width,pixel_height,size_bytes,sha256,created_at
		FROM model_captures
		WHERE owner_id = ? AND session_id = ?
		ORDER BY created_at, id`, request.OwnerID, request.SessionID)
	if err != nil {
		return nil, 0, err
	}
	type sourceCapture struct {
		id, sourceTask, modelAsset, screenshotAsset, idempotencyKey string
		viewJSON                                                    string
		pixelWidth, pixelHeight                                     int
		sizeBytes                                                   int64
		sha256, createdAt                                           string
	}
	var sources []sourceCapture
	for rows.Next() {
		var item sourceCapture
		if err := rows.Scan(&item.id, &item.sourceTask, &item.modelAsset, &item.screenshotAsset,
			&item.idempotencyKey, &item.viewJSON, &item.pixelWidth, &item.pixelHeight,
			&item.sizeBytes, &item.sha256, &item.createdAt); err != nil {
			rows.Close()
			return nil, 0, err
		}
		sources = append(sources, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, 0, err
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	if len(sources) == 0 {
		return map[string]string{}, 0, nil
	}
	remap := make(map[string]string, len(sources))
	for _, source := range sources {
		var view capture.View
		if err := json.Unmarshal([]byte(source.viewJSON), &view); err != nil {
			return nil, 0, fmt.Errorf("%w: capture view", chat.ErrBranchScene)
		}
		// Fail closed: the copied capture must still name a Task this source
		// session may read and assets it owns. A bypassed row cannot smuggle
		// another owner's evidence into the branch.
		owned, err := taskInSession(ctx, tx, request.OwnerID, request.SessionID, source.sourceTask)
		if err != nil {
			return nil, 0, err
		}
		if !owned {
			return nil, 0, fmt.Errorf("%w: capture source task", chat.ErrBranchScene)
		}
		modelOwned, err := captureModelAssetOwned(ctx, tx, request.OwnerID, source.sourceTask, source.modelAsset)
		if err != nil {
			return nil, 0, err
		}
		screenshotOwned, err := captureScreenshotOwned(ctx, tx, request.OwnerID, source.screenshotAsset)
		if err != nil {
			return nil, 0, err
		}
		if !modelOwned || !screenshotOwned {
			return nil, 0, fmt.Errorf("%w: capture evidence", chat.ErrBranchScene)
		}
		newID := request.NewID("capture")
		if newID == "" || newID == source.id {
			return nil, 0, fmt.Errorf("%w: capture id", chat.ErrBranchScene)
		}
		command := capture.Command{
			ID: newID, OwnerID: request.OwnerID, SessionID: request.NewSessionID,
			SourceTaskID: source.sourceTask, ModelAssetID: source.modelAsset,
			ScreenshotAssetID: source.screenshotAsset,
			IdempotencyKey:    branchCaptureKey(request.NewSessionID, source.idempotencyKey),
			View:              view,
		}
		commandHash, err := captureCommandHash(command)
		if err != nil {
			return nil, 0, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO model_captures
				(id,owner_id,session_id,source_task_id,model_asset_id,screenshot_asset_id,
				 idempotency_key,command_hash,view_json,pixel_width,pixel_height,size_bytes,sha256,created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			newID, request.OwnerID, request.NewSessionID, source.sourceTask, source.modelAsset,
			source.screenshotAsset, command.IdempotencyKey, commandHash, source.viewJSON,
			source.pixelWidth, source.pixelHeight, source.sizeBytes, source.sha256, source.createdAt,
		); err != nil {
			return nil, 0, fmt.Errorf("copy branch capture: %w", err)
		}
		remap[source.id] = newID
	}
	return remap, len(sources), nil
}

// branchCaptureKey makes the copied capture's idempotency key unique in the
// owner scope without exposing the source session's key.
func branchCaptureKey(newSession, sourceKey string) string {
	sum := sha256.Sum256([]byte("branch-capture\x00" + newSession + "\x00" + sourceKey))
	return hex.EncodeToString(sum[:])
}

func captureModelAssetOwned(ctx context.Context, q queryRower, owner, task, asset string) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, `
		SELECT 1 FROM generation_assets AS a
		JOIN generation_tasks AS t ON t.id = a.task_id
		WHERE a.id = ? AND a.task_id = ? AND t.owner_id = ?
		  AND t.state = 'SUCCEEDED' AND a.storage_key <> '' AND a.sha256 <> ''`,
		asset, task, owner).Scan(&one)
	return sceneRowFound(one, err)
}

func captureScreenshotOwned(ctx context.Context, q queryRower, owner, asset string) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, `
		SELECT 1 FROM generation_input_assets
		WHERE id = ? AND owner_id = ? AND storage_key <> '' AND sha256 <> ''`,
		asset, owner).Scan(&one)
	return sceneRowFound(one, err)
}

func sceneRowFound(_ int, err error) (bool, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// branchSceneCopy copies the current stage: every object not deleted at branch
// time, all of its immutable representations, and the object's active
// representation. IDs are new; source_representation_id is remapped inside the
// copied object. The parent session is never changed.
//
// This is deliberately the current state, not a point-in-time snapshot of the
// selected message: rect/deleted are updated in place and no scene history
// exists. The Product Schema text says so.
func branchSceneCopy(ctx context.Context, tx *sql.Tx, request chat.BranchRequest) (bool, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id,revision,active_representation_id,
		       rect_x,rect_y,rect_width,rect_height,anchor_x,anchor_y,created_at,updated_at
		FROM scene_objects
		WHERE owner_id = ? AND session_id = ? AND deleted_at IS NULL
		ORDER BY created_at, id`, request.OwnerID, request.SessionID)
	if err != nil {
		return false, err
	}
	type sourceObject struct {
		id, activeRepresentation                      string
		revision                                      int64
		rectX, rectY, width, height, anchorX, anchorY float64
		createdAt, updatedAt                          string
	}
	var objects []sourceObject
	for rows.Next() {
		var object sourceObject
		if err := rows.Scan(&object.id, &object.revision, &object.activeRepresentation,
			&object.rectX, &object.rectY, &object.width, &object.height,
			&object.anchorX, &object.anchorY, &object.createdAt, &object.updatedAt); err != nil {
			rows.Close()
			return false, err
		}
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	if len(objects) == 0 {
		return false, nil
	}
	var revision int64
	var revisionAt string
	err = tx.QueryRowContext(ctx, `
		SELECT revision, updated_at FROM scene_revisions
		WHERE owner_id = ? AND session_id = ?`, request.OwnerID, request.SessionID,
	).Scan(&revision, &revisionAt)
	// The scene revision row exists for a live scene (Create/RegisterCandidate
	// bump it). A scene with objects but no revision is corrupt; do not guess.
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("%w: scene revision", chat.ErrBranchScene)
	}
	if err != nil {
		return false, err
	}
	if revision < 1 {
		return false, fmt.Errorf("%w: scene revision", chat.ErrBranchScene)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO scene_revisions (session_id,owner_id,revision,updated_at)
		VALUES (?,?,?,?)`, request.NewSessionID, request.OwnerID, revision, revisionAt,
	); err != nil {
		return false, fmt.Errorf("copy branch scene revision: %w", err)
	}
	for _, object := range objects {
		if err := copyBranchSceneObject(ctx, tx, request, object.id, object.activeRepresentation,
			object.revision, object.rectX, object.rectY, object.width, object.height,
			object.anchorX, object.anchorY, object.createdAt, object.updatedAt); err != nil {
			return false, err
		}
	}
	return true, nil
}

func copyBranchSceneObject(
	ctx context.Context,
	tx *sql.Tx,
	request chat.BranchRequest,
	sourceObjectID, sourceActiveRepresentation string,
	revision int64,
	rectX, rectY, width, height, anchorX, anchorY float64,
	createdAt, updatedAt string,
) error {
	type sourceRepresentation struct {
		id, kind, assetID, sourceTask, sourceRepresentation, preparedInput, metadataRevision, createdAt string
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id,kind,asset_id,COALESCE(source_task_id,''),COALESCE(source_representation_id,''),
		       COALESCE(prepared_input_id,''),metadata_schema_revision,created_at
		FROM scene_representations
		WHERE object_id = ?
		ORDER BY created_at, id`, sourceObjectID)
	if err != nil {
		return err
	}
	var representations []sourceRepresentation
	byID := map[string]sourceRepresentation{}
	for rows.Next() {
		var rep sourceRepresentation
		if err := rows.Scan(&rep.id, &rep.kind, &rep.assetID, &rep.sourceTask,
			&rep.sourceRepresentation, &rep.preparedInput, &rep.metadataRevision, &rep.createdAt); err != nil {
			rows.Close()
			return err
		}
		representations = append(representations, rep)
		byID[rep.id] = rep
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(representations) == 0 || byID[sourceActiveRepresentation].id == "" {
		return fmt.Errorf("%w: active representation", chat.ErrBranchScene)
	}
	newIDs := make(map[string]string, len(representations))
	known := make(map[string]bool, len(representations))
	for _, rep := range representations {
		known[rep.id] = true
	}
	for _, rep := range representations {
		id := request.NewID("scene_representation")
		if id == "" || newIDs[rep.id] != "" {
			return fmt.Errorf("%w: representation id", chat.ErrBranchScene)
		}
		newIDs[rep.id] = id
		if err := checkBranchRepresentation(ctx, tx, request, rep.kind, rep.assetID,
			rep.sourceTask, rep.sourceRepresentation, rep.preparedInput, rep.metadataRevision, known); err != nil {
			return err
		}
	}
	newObjectID := request.NewID("scene_object")
	if newObjectID == "" {
		return fmt.Errorf("%w: object id", chat.ErrBranchScene)
	}
	// The object row must exist before its representations (their object_id is
	// a RESTRICT foreign key). active_representation_id itself has no foreign
	// key, so it can name the new representation before that row is inserted.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO scene_objects
			(id,owner_id,session_id,revision,active_representation_id,
			 rect_x,rect_y,rect_width,rect_height,anchor_x,anchor_y,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		newObjectID, request.OwnerID, request.NewSessionID, revision,
		newIDs[sourceActiveRepresentation], rectX, rectY, width, height, anchorX, anchorY,
		createdAt, updatedAt,
	); err != nil {
		return fmt.Errorf("copy branch scene object: %w", err)
	}
	// Insert representations in dependency order: an immutable row cannot be
	// updated later, and the self-referencing FK must exist at insert time.
	inserted := map[string]bool{}
	remaining := append([]sourceRepresentation(nil), representations...)
	for len(remaining) > 0 {
		progress := false
		next := remaining[:0]
		for _, rep := range remaining {
			if rep.sourceRepresentation != "" && !inserted[rep.sourceRepresentation] {
				next = append(next, rep)
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO scene_representations
					(id,object_id,kind,asset_id,source_task_id,source_representation_id,
					 prepared_input_id,metadata_schema_revision,created_at)
				VALUES (?,?,?,?,?,?,?,?,?)`,
				newIDs[rep.id], newObjectID, rep.kind, rep.assetID,
				nullSceneString(rep.sourceTask), nullSceneString(newIDs[rep.sourceRepresentation]),
				nullSceneString(rep.preparedInput), rep.metadataRevision, rep.createdAt,
			); err != nil {
				return fmt.Errorf("copy branch scene representation: %w", err)
			}
			inserted[rep.id] = true
			progress = true
		}
		if !progress {
			return fmt.Errorf("%w: representation cycle", chat.ErrBranchScene)
		}
		remaining = next
	}
	return nil
}

// checkBranchRepresentation is the fail-closed admission of one copied
// representation. Every referenced asset, Task and prepared input must belong
// to the owner and to the source session or one of its ancestors; otherwise
// the branch would carry evidence it is not allowed to read.
func checkBranchRepresentation(
	ctx context.Context,
	tx *sql.Tx,
	request chat.BranchRequest,
	kind, assetID, sourceTask, sourceRepresentation, preparedInput, metadataRevision string,
	knownRepresentations map[string]bool,
) error {
	if metadataRevision == "" {
		return fmt.Errorf("%w: representation metadata", chat.ErrBranchScene)
	}
	if sourceRepresentation != "" {
		if !knownRepresentations[sourceRepresentation] {
			return fmt.Errorf("%w: representation source", chat.ErrBranchScene)
		}
	}
	if sourceTask != "" {
		owned, err := taskInSession(ctx, tx, request.OwnerID, request.SessionID, sourceTask)
		if err != nil {
			return err
		}
		if !owned {
			return fmt.Errorf("%w: representation task", chat.ErrBranchScene)
		}
		var succeeded int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM generation_tasks WHERE id=? AND owner_id=? AND state='SUCCEEDED'`,
			sourceTask, request.OwnerID).Scan(&succeeded); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: representation task state", chat.ErrBranchScene)
			}
			return err
		}
	}
	if preparedInput != "" {
		owned, err := branchPreparedInputOwned(ctx, tx, request.OwnerID, request.SessionID, preparedInput)
		if err != nil {
			return err
		}
		if !owned {
			return fmt.Errorf("%w: representation prepared input", chat.ErrBranchScene)
		}
	}
	ready, err := branchRepresentationAssetReady(ctx, tx, request.OwnerID, kind, sourceTask, assetID)
	if err != nil {
		return err
	}
	if !ready {
		return fmt.Errorf("%w: representation asset", chat.ErrBranchScene)
	}
	return nil
}

func branchRepresentationAssetReady(ctx context.Context, q *sql.Tx, owner, kind, sourceTask, assetID string) (bool, error) {
	if assetID == "" {
		return false, nil
	}
	inputOwned, err := captureScreenshotOwned(ctx, q, owner, assetID)
	if err != nil {
		return false, err
	}
	// A capture's screenshot is an input asset even though the record names a
	// source model Task. Other kinds with a source Task must use that Task's
	// own output file.
	if kind == "capture" {
		return inputOwned, nil
	}
	if sourceTask != "" {
		return captureModelAssetOwned(ctx, q, owner, sourceTask, assetID)
	}
	return inputOwned, nil
}

func branchPreparedInputOwned(ctx context.Context, q queryRower, owner, session, prepared string) (bool, error) {
	var preparedSession string
	err := q.QueryRowContext(ctx, `
		SELECT session_id FROM generation_prepared_inputs
		WHERE id = ? AND owner_id = ?`, prepared, owner).Scan(&preparedSession)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return sessionInAncestry(ctx, q, owner, session, preparedSession)
}

// remapCaptureIDs rewrites the capture_id members of one copied message's
// attachments. The attachment array is a flat list of objects, so raw members
// are kept verbatim; only messages that actually name a copied capture are
// decoded and re-encoded.
func remapCaptureIDs(raw string, remap map[string]string) (string, error) {
	if raw == "" || len(remap) == 0 {
		return raw, nil
	}
	var attachments []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &attachments); err != nil {
		return "", fmt.Errorf("branch message attachments: %w", err)
	}
	changed := false
	for _, attachment := range attachments {
		value, ok := attachment["capture_id"]
		if !ok {
			continue
		}
		var id string
		if json.Unmarshal(value, &id) != nil {
			continue
		}
		if replacement, found := remap[id]; found {
			encoded, err := json.Marshal(replacement)
			if err != nil {
				return "", err
			}
			attachment["capture_id"] = encoded
			changed = true
		}
	}
	if !changed {
		return raw, nil
	}
	encoded, err := json.Marshal(attachments)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
