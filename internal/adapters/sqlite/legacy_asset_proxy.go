package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/adapters/legacyassetproxy"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func (r *Repository) GetLegacyAssetTask(
	ctx context.Context,
	taskID string,
) (legacyassetproxy.LegacyAssetTask, bool, error) {
	exists, err := r.tableExists(ctx, "task_cache")
	if err != nil {
		return legacyassetproxy.LegacyAssetTask{}, false, err
	}
	if exists {
		record, found, err := r.getLegacyAssetTaskFromCache(
			ctx,
			taskID,
		)
		if err != nil || found {
			return record, found, err
		}
	}
	return r.getLegacyAssetTaskFromNative(ctx, taskID)
}

func (r *Repository) getLegacyAssetTaskFromCache(
	ctx context.Context,
	taskID string,
) (legacyassetproxy.LegacyAssetTask, bool, error) {
	var record legacyassetproxy.LegacyAssetTask
	err := r.db.QueryRowContext(ctx, `
		SELECT taskId, taskType, status, providerId, data
		FROM task_cache
		WHERE taskId = ?`,
		taskID,
	).Scan(
		&record.TaskID,
		&record.TaskType,
		&record.Status,
		&record.ProviderID,
		&record.Data,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return legacyassetproxy.LegacyAssetTask{}, false, nil
	}
	if err != nil {
		return legacyassetproxy.LegacyAssetTask{}, false, err
	}
	return record, true, nil
}

func (r *Repository) getLegacyAssetTaskFromNative(
	ctx context.Context,
	taskID string,
) (legacyassetproxy.LegacyAssetTask, bool, error) {
	var record legacyassetproxy.LegacyAssetTask
	var offeringJSON string
	var opaqueJSON sql.NullString
	var upstreamProvider sql.NullString
	var upstreamJob sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT task.id, task.capability, task.state, task.version,
		       attempt.id, attempt.offering_json, attempt.opaque_json,
		       attempt.upstream_provider_id, attempt.upstream_job_id
		FROM generation_tasks AS task
		JOIN generation_attempts AS attempt
		  ON attempt.id = task.current_attempt_id
		WHERE task.id = ?`,
		taskID,
	).Scan(
		&record.TaskID,
		&record.TaskType,
		&record.Status,
		&record.Version,
		&record.AttemptID,
		&offeringJSON,
		&opaqueJSON,
		&upstreamProvider,
		&upstreamJob,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return legacyassetproxy.LegacyAssetTask{}, false, nil
	}
	if err != nil {
		return legacyassetproxy.LegacyAssetTask{}, false, err
	}
	if err := json.Unmarshal(
		[]byte(offeringJSON),
		&record.Offering,
	); err != nil {
		return legacyassetproxy.LegacyAssetTask{}, false, fmt.Errorf(
			"decode native offering for %q: %w",
			taskID,
			err,
		)
	}
	record.ProviderID = record.Offering.ProviderID
	if record.ProviderID == "" && upstreamProvider.Valid {
		record.ProviderID = upstreamProvider.String
	}
	record.Data = "{}"
	if opaqueJSON.Valid &&
		json.Valid([]byte(opaqueJSON.String)) {
		record.Data = opaqueJSON.String
	}
	if upstreamProvider.Valid && upstreamJob.Valid {
		record.UpstreamJob = &generation.UpstreamJob{
			ProviderID: upstreamProvider.String,
			RemoteID:   upstreamJob.String,
		}
	}
	return record, true, nil
}

// UpdateLegacyAssetTaskData rebases only signed URLs on an explicitly read snapshot.
func (r *Repository) UpdateLegacyAssetTaskData(ctx context.Context, expected legacyassetproxy.LegacyAssetTask, data string) error {
	if data == expected.Data {
		return nil
	}
	if expected.AttemptID == "" {
		result, err := r.db.ExecContext(ctx, `UPDATE task_cache SET data = ?, updatedAt = CURRENT_TIMESTAMP WHERE taskId = ? AND data = ?`, data, expected.TaskID, expected.Data)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return generation.RepositoryConflict
		}
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Advance the aggregate version in the same transaction as the opaque update.
	result, err := tx.ExecContext(ctx, `UPDATE generation_tasks SET version = version + 1 WHERE id = ? AND version = ? AND current_attempt_id = ? AND NOT EXISTS (SELECT 1 FROM generation_task_tombstones WHERE task_id = generation_tasks.id)`, expected.TaskID, expected.Version, expected.AttemptID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return generation.RepositoryConflict
	}
	result, err = tx.ExecContext(ctx, `UPDATE generation_attempts SET opaque_json = ? WHERE id = ? AND COALESCE(opaque_json, '{}') = ?`, data, expected.AttemptID, expected.Data)
	if err != nil {
		return err
	}
	n, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return generation.RepositoryConflict
	}
	return tx.Commit()
}
