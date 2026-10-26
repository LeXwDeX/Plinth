package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/administration"
)

func (r *Repository) ListPendingLegacyUsage(
	ctx context.Context,
	status string,
) ([]administration.LegacyPendingUsage, error) {
	exists, err := r.tableExists(ctx, "usage_ledger")
	if err != nil {
		return nil, err
	}
	if !exists {
		return []administration.LegacyPendingUsage{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, taskId, providerId, taskStatus
		FROM usage_ledger
		WHERE taskStatus = ?
		ORDER BY createdAt ASC, id ASC`,
		status,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]administration.LegacyPendingUsage, 0)
	for rows.Next() {
		var record administration.LegacyPendingUsage
		if err := rows.Scan(
			&record.ID,
			&record.TaskID,
			&record.ProviderID,
			&record.Status,
		); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Repository) GetLegacyTaskCache(
	ctx context.Context,
	taskID string,
) (administration.LegacyTaskCache, bool, error) {
	exists, err := r.tableExists(ctx, "task_cache")
	if err != nil {
		return administration.LegacyTaskCache{}, false, err
	}
	if !exists {
		return administration.LegacyTaskCache{}, false, nil
	}
	var cache administration.LegacyTaskCache
	err = r.db.QueryRowContext(ctx, `
		SELECT status, data
		FROM task_cache
		WHERE taskId = ?`,
		taskID,
	).Scan(&cache.Status, &cache.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return administration.LegacyTaskCache{}, false, nil
	}
	if err != nil {
		return administration.LegacyTaskCache{}, false, err
	}
	return cache, true, nil
}

func (r *Repository) UpdateLegacyUsageStatus(
	ctx context.Context,
	update administration.LegacyUsageStatusUpdate,
) error {
	var (
		result sql.Result
		err    error
	)
	if update.WriteResultURLs {
		resultURLs, marshalErr := json.Marshal(update.ResultURLs)
		if marshalErr != nil {
			return marshalErr
		}
		result, err = r.db.ExecContext(ctx, `
			UPDATE usage_ledger
			SET taskStatus = ?, finishedAt = ?, resultUrls = ?
			WHERE id = ?`,
			update.Status,
			update.FinishedAt.UTC().UnixMilli(),
			string(resultURLs),
			update.ID,
		)
	} else {
		result, err = r.db.ExecContext(ctx, `
			UPDATE usage_ledger
			SET taskStatus = ?, finishedAt = ?
			WHERE id = ?`,
			update.Status,
			update.FinishedAt.UTC().UnixMilli(),
			update.ID,
		)
	}
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf(
			"legacy usage status update affected %d rows for %q",
			affected,
			update.ID,
		)
	}
	return nil
}
