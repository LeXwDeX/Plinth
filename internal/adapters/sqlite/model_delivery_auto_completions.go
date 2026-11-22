package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modeldelivery"
)

var _ modeldelivery.AutoCompletionStore = (*Repository)(nil)

// The automatic delivery completion's durable record (#461): one row per Task
// plus the one-row scan watermark. The policy that reads and writes it lives in
// internal/modeldelivery; this file only knows the tables.

// AutoCompletionWatermark reads the scan position. False means no scan has ever
// run: the first scan then writes "now" and never looks at a historical Task.
func (r *Repository) AutoCompletionWatermark(ctx context.Context) (modeldelivery.AutoCompletionCursor, bool, error) {
	var updatedAt, taskID string
	err := r.db.QueryRowContext(ctx, `SELECT updated_at, task_id FROM model_delivery_auto_completion_watermark WHERE id = 1`).
		Scan(&updatedAt, &taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return modeldelivery.AutoCompletionCursor{}, false, nil
	}
	if err != nil {
		return modeldelivery.AutoCompletionCursor{}, false, err
	}
	parsed, err := parseTime(updatedAt)
	if err != nil {
		return modeldelivery.AutoCompletionCursor{}, false, err
	}
	return modeldelivery.AutoCompletionCursor{UpdatedAt: parsed, TaskID: generation.TaskID(taskID)}, true, nil
}

// SetAutoCompletionWatermark writes the scan position.
func (r *Repository) SetAutoCompletionWatermark(ctx context.Context, cursor modeldelivery.AutoCompletionCursor) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO model_delivery_auto_completion_watermark (id, updated_at, task_id) VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET updated_at = excluded.updated_at, task_id = excluded.task_id`,
		formatTaskTime(cursor.UpdatedAt), string(cursor.TaskID))
	return err
}

// AutoCompletionCandidates lists the succeeded Tasks of the declared task kinds
// that are newer than the watermark and have no record yet, oldest first, and
// advances the watermark past everything it listed. A Task that is still
// running is not listed; when it succeeds its updated_at is past the watermark
// and the next scan finds it.
func (r *Repository) AutoCompletionCandidates(
	ctx context.Context,
	capabilities []generation.Capability,
	limit int,
) ([]modeldelivery.AutoCompletionCandidate, error) {
	if limit <= 0 || len(capabilities) == 0 {
		return nil, nil
	}
	cursor, known, err := r.AutoCompletionWatermark(ctx)
	if err != nil {
		return nil, err
	}
	if !known {
		// First enable: write the position now, so no historical Task is ever
		// looked at (#461).
		if err := r.SetAutoCompletionWatermark(ctx, modeldelivery.AutoCompletionCursor{UpdatedAt: time.Now().UTC()}); err != nil {
			return nil, err
		}
		return nil, nil
	}
	placeholders := make([]string, 0, len(capabilities))
	for range capabilities {
		placeholders = append(placeholders, "?")
	}
	args := []any{string(generation.TaskSucceeded)}
	for _, capability := range capabilities {
		args = append(args, string(capability))
	}
	args = append(args, formatTaskTime(cursor.UpdatedAt), formatTaskTime(cursor.UpdatedAt), string(cursor.TaskID), limit)
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT t.id, t.owner_id, t.session_id, t.updated_at
		FROM generation_tasks AS t
		LEFT JOIN model_delivery_auto_completions AS c ON c.task_id = t.id
		WHERE t.state = ? AND t.session_id IS NOT NULL AND c.task_id IS NULL
			AND t.capability IN (%s)
			AND (t.updated_at > ? OR (t.updated_at = ? AND t.id > ?))
		ORDER BY t.updated_at, t.id
		LIMIT ?`,
		joinPlaceholders(placeholders)), args...)
	if err != nil {
		return nil, err
	}
	type candidateRow struct {
		id, owner, session, updatedAt string
	}
	var items []candidateRow
	for rows.Next() {
		var item candidateRow
		if err = rows.Scan(&item.id, &item.owner, &item.session, &item.updatedAt); err != nil {
			break
		}
		items = append(items, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := make([]modeldelivery.AutoCompletionCandidate, 0, len(items))
	for _, item := range items {
		result = append(result, modeldelivery.AutoCompletionCandidate{
			TaskID: generation.TaskID(item.id), OwnerID: generation.OwnerID(item.owner),
			SessionID: item.session,
		})
	}
	if len(items) > 0 {
		last := items[len(items)-1]
		parsed, err := parseTime(last.updatedAt)
		if err != nil {
			return nil, err
		}
		if err := r.SetAutoCompletionWatermark(ctx, modeldelivery.AutoCompletionCursor{
			UpdatedAt: parsed, TaskID: generation.TaskID(last.id),
		}); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func joinPlaceholders(placeholders []string) string {
	result := ""
	for index, placeholder := range placeholders {
		if index > 0 {
			result += ", "
		}
		result += placeholder
	}
	return result
}

// AutoCompletionRecord reads one Task's record. False means the Task was never
// looked at.
func (r *Repository) AutoCompletionRecord(ctx context.Context, taskID generation.TaskID) (modeldelivery.AutoCompletionRecord, bool, error) {
	var (
		record                  modeldelivery.AutoCompletionRecord
		policyRevision, state   string
		planID, reason          string
		nextRunAt, updatedAt    string
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT task_id, owner_id, policy_revision, state, attempts, plan_id,
		       paid_credits, paid_runs, reason, next_run_at, updated_at
		FROM model_delivery_auto_completions WHERE task_id = ?`, string(taskID)).
		Scan(&record.TaskID, &record.OwnerID, &policyRevision, &state, &record.Attempts, &planID,
			&record.PaidCredits, &record.PaidRuns, &reason, &nextRunAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return modeldelivery.AutoCompletionRecord{}, false, nil
	}
	if err != nil {
		return modeldelivery.AutoCompletionRecord{}, false, err
	}
	if record.NextRunAt, err = parseTime(nextRunAt); err != nil {
		return modeldelivery.AutoCompletionRecord{}, false, err
	}
	if record.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return modeldelivery.AutoCompletionRecord{}, false, err
	}
	record.OwnerID = generation.OwnerID(record.OwnerID)
	record.PolicyRevision, record.State = policyRevision, state
	record.PlanID, record.Reason = planID, reason
	return record, true, nil
}

// SaveAutoCompletionRecord writes one Task's record.
func (r *Repository) SaveAutoCompletionRecord(ctx context.Context, record modeldelivery.AutoCompletionRecord) error {
	if record.TaskID == "" || record.OwnerID == "" {
		return fmt.Errorf("自动补齐记录缺少任务或用户")
	}
	updated := record.UpdatedAt
	if updated.IsZero() {
		updated = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO model_delivery_auto_completions
			(task_id, owner_id, policy_revision, state, attempts, plan_id, paid_credits, paid_runs, reason, next_run_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(task_id) DO UPDATE SET
			owner_id = excluded.owner_id, policy_revision = excluded.policy_revision,
			state = excluded.state, attempts = excluded.attempts, plan_id = excluded.plan_id,
			paid_credits = excluded.paid_credits, paid_runs = excluded.paid_runs,
			reason = excluded.reason, next_run_at = excluded.next_run_at,
			updated_at = excluded.updated_at`,
		string(record.TaskID), string(record.OwnerID), record.PolicyRevision, record.State,
		record.Attempts, record.PlanID, record.PaidCredits, record.PaidRuns, record.Reason,
		formatTaskTime(record.NextRunAt), formatTaskTime(updated))
	return err
}

// AutoCompletionDue reads the records whose round has come, oldest first.
func (r *Repository) AutoCompletionDue(ctx context.Context, now time.Time, limit int) ([]modeldelivery.AutoCompletionRecord, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT task_id, owner_id, policy_revision, state, attempts, plan_id,
		       paid_credits, paid_runs, reason, next_run_at, updated_at
		FROM model_delivery_auto_completions
		WHERE state <> ? AND next_run_at <= ?
		ORDER BY next_run_at, task_id
		LIMIT ?`, modeldelivery.AutoStateDone, formatTaskTime(now), limit)
	if err != nil {
		return nil, err
	}
	type recordRow struct {
		record                  modeldelivery.AutoCompletionRecord
		policyRevision          string
		state                   string
		planID, reason          string
		nextRunAt, updatedAt    string
	}
	var items []recordRow
	for rows.Next() {
		var item recordRow
		if err = rows.Scan(&item.record.TaskID, &item.record.OwnerID, &item.policyRevision, &item.state,
			&item.record.Attempts, &item.planID, &item.record.PaidCredits, &item.record.PaidRuns,
			&item.reason, &item.nextRunAt, &item.updatedAt); err != nil {
			break
		}
		items = append(items, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := make([]modeldelivery.AutoCompletionRecord, 0, len(items))
	for _, item := range items {
		record := item.record
		if record.NextRunAt, err = parseTime(item.nextRunAt); err != nil {
			return nil, err
		}
		if record.UpdatedAt, err = parseTime(item.updatedAt); err != nil {
			return nil, err
		}
		record.PolicyRevision, record.State = item.policyRevision, item.state
		record.PlanID, record.Reason = item.planID, item.reason
		result = append(result, record)
	}
	return result, nil
}