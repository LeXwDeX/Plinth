package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// Upstream result refetch bindings (#295, migration 0060).

var _ generation.UpstreamRefetchStore = (*Repository)(nil)

func (r *Repository) CreateUpstreamRefetch(ctx context.Context, task generation.Task, event generation.Event, binding generation.UpstreamRefetch, maxInflight int) (generation.Task, bool, error) {
	if binding.SourceTaskID == "" || binding.SourceAttemptID == "" || binding.Ordinal < 1 || binding.RequestedBy == "" {
		return generation.Task{}, false, generation.RepositoryAuthorizationRequired
	}
	return r.createTask(ctx, task, event, maxInflight, &binding, nil)
}

func insertUpstreamRefetch(ctx context.Context, conn *sql.Conn, binding generation.UpstreamRefetch) error {
	_, err := conn.ExecContext(ctx, `
		INSERT INTO generation_upstream_refetches (
			task_id, owner_id, session_id, source_task_id, source_attempt_id, ordinal, requested_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		binding.TaskID, binding.OwnerID, binding.SessionID, binding.SourceTaskID,
		binding.SourceAttemptID, binding.Ordinal, string(binding.RequestedBy), formatTime(binding.CreatedAt))
	return err
}

const upstreamRefetchSelect = `SELECT task_id, owner_id, session_id, source_task_id, source_attempt_id, ordinal, requested_by, created_at FROM generation_upstream_refetches`

func scanUpstreamRefetch(row interface{ Scan(...any) error }) (generation.UpstreamRefetch, error) {
	var binding generation.UpstreamRefetch
	var requestedBy, createdAt string
	if err := row.Scan(&binding.TaskID, &binding.OwnerID, &binding.SessionID, &binding.SourceTaskID,
		&binding.SourceAttemptID, &binding.Ordinal, &requestedBy, &createdAt); err != nil {
		return binding, err
	}
	binding.RequestedBy = generation.RefetchRequester(requestedBy)
	created, err := parseTime(createdAt)
	if err != nil {
		return binding, err
	}
	binding.CreatedAt = created
	return binding, nil
}

func (r *Repository) UpstreamRefetchOf(ctx context.Context, id generation.TaskID) (generation.UpstreamRefetch, error) {
	binding, err := scanUpstreamRefetch(r.db.QueryRowContext(ctx, upstreamRefetchSelect+` WHERE task_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return generation.UpstreamRefetch{}, generation.RepositoryNotFound
	}
	return binding, err
}

func (r *Repository) listUpstreamRefetches(ctx context.Context, where string, args ...any) ([]generation.UpstreamRefetch, error) {
	rows, err := r.db.QueryContext(ctx, upstreamRefetchSelect+` WHERE `+where+` ORDER BY ordinal, created_at, task_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []generation.UpstreamRefetch
	for rows.Next() {
		binding, err := scanUpstreamRefetch(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, binding)
	}
	return result, rows.Err()
}

func (r *Repository) UpstreamRefetchesOf(ctx context.Context, owner generation.OwnerID, source generation.TaskID) ([]generation.UpstreamRefetch, error) {
	return r.listUpstreamRefetches(ctx, `owner_id = ? AND source_task_id = ?`, owner, source)
}

func (r *Repository) SessionUpstreamRefetches(ctx context.Context, owner generation.OwnerID, session string) ([]generation.UpstreamRefetch, error) {
	return r.listUpstreamRefetches(ctx, `owner_id = ? AND session_id = ?`, owner, session)
}
