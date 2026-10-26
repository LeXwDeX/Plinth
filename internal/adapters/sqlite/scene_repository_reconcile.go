package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/scene"
)

// ReconcileTaskCandidates replays durable, succeeded image-to-3d facts. It is
// safe after restart and never applies a candidate to the active object.
// The source representation is frozen in PreparedInput; object movement after
// Task acceptance does not prevent the result from being recorded.
func (r *SceneRepository) ReconcileTaskCandidates(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, scene.ErrInvalid
	}
	// A malformed or deleted historical source must not pin every future
	// reconciliation behind the same first page. Advance a keyset cursor even
	// when a row fails, then wrap after reaching the end to retry repaired rows.
	r.reconcileMu.Lock()
	defer r.reconcileMu.Unlock()
	rows, err := r.db.QueryContext(ctx, `SELECT t.owner_id,t.session_id,t.id,a.id,p.id,p.payload_json,t.updated_at
		FROM generation_tasks t JOIN execution_grants g ON g.task_id=t.id AND g.status='bound'
		JOIN generation_prepared_inputs p ON p.id=g.prepared_input_id AND p.input_hash=g.prepared_input_hash AND p.owner_id=t.owner_id AND p.session_id=t.session_id
		JOIN generation_assets a ON a.task_id=t.id AND a.role='model' AND a.ordinal=0
		WHERE t.state='SUCCEEDED' AND t.capability=?
		AND json_extract(p.payload_json,'$.source_scene_object_id') IS NOT NULL
		AND json_extract(p.payload_json,'$.source_scene_representation_id') IS NOT NULL
		AND NOT EXISTS (SELECT 1 FROM scene_representations r WHERE r.source_task_id=t.id
			AND r.asset_id=a.id AND r.prepared_input_id=p.id)
		AND (t.updated_at>? OR (t.updated_at=? AND t.id>?))
		ORDER BY t.updated_at,t.id LIMIT ?`, string(generation.ImageTo3D),
		r.reconcileUpdatedAt, r.reconcileUpdatedAt, r.reconcileTaskID, limit)
	if err != nil {
		return 0, err
	}
	type pending struct{ owner, session, task, asset, prepared, payload, updatedAt string }
	var items []pending
	for rows.Next() {
		var item pending
		if err = rows.Scan(&item.owner, &item.session, &item.task, &item.asset, &item.prepared, &item.payload, &item.updatedAt); err != nil {
			break
		}
		items = append(items, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	if len(items) == 0 {
		r.reconcileUpdatedAt, r.reconcileTaskID = "", ""
		return 0, nil
	}
	last := items[len(items)-1]
	r.reconcileUpdatedAt, r.reconcileTaskID = last.updatedAt, last.task
	count := 0
	var problems error
	for _, item := range items {
		var prepared generation.PreparedInput
		if e := json.Unmarshal([]byte(item.payload), &prepared); e != nil {
			problems = errors.Join(problems, fmt.Errorf("task %s: invalid prepared input: %w", item.task, e))
			continue
		}
		if prepared.OwnerID != generation.OwnerID(item.owner) || prepared.SessionID != item.session ||
			prepared.ID != generation.PreparedInputID(item.prepared) || prepared.SourceSceneObjectID == "" ||
			prepared.SourceSceneRepresentationID == "" || prepared.SourceSceneRevision < 1 || prepared.SchemaVersion == "" {
			problems = errors.Join(problems, fmt.Errorf("task %s: %w", item.task, scene.ErrConflict))
			continue
		}
		_, e := r.RegisterCandidate(ctx, scene.Candidate{OwnerID: item.owner, SessionID: item.session, ObjectID: prepared.SourceSceneObjectID,
			FrozenSourceRevision: prepared.SourceSceneRevision, FrozenSourceRepresentationID: prepared.SourceSceneRepresentationID,
			Idempotent: true, Representation: scene.Representation{ID: scene.TaskCandidateID(item.task, item.asset), ObjectID: prepared.SourceSceneObjectID,
				Kind: scene.Model3D, AssetID: item.asset, SourceTaskID: item.task, SourceRepresentationID: prepared.SourceSceneRepresentationID,
				PreparedInputID: item.prepared, MetadataSchemaRevision: prepared.SchemaVersion}})
		if e != nil {
			problems = errors.Join(problems, fmt.Errorf("task %s: %w", item.task, e))
			continue
		}
		count++
	}
	return count, problems
}
