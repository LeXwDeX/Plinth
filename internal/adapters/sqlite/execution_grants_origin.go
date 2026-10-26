package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/taskretry"
)

func (r *Repository) AdmittedPreparedInput(ctx context.Context, owner generation.OwnerID, task generation.TaskID) (generation.PreparedInputID, error) {
	var id generation.PreparedInputID
	err := r.db.QueryRowContext(ctx, `SELECT prepared_input_id FROM execution_grants
		WHERE owner_id = ? AND task_id = ? AND status = 'bound' LIMIT 1`, owner, task).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// PreparedInputOf returns the admission of a task of this owner: the frozen
// input it was admitted with and the plan step it belonged to, both "" when
// the task has no bound grant (an older task). A retry uses the first to keep
// the original scene and render-manifest context and the second to refuse a
// plan or skill-run step.
func (r *Repository) PreparedInputOf(ctx context.Context, owner, task string) (taskretry.Origin, error) {
	var origin taskretry.Origin
	err := r.db.QueryRowContext(ctx, `SELECT prepared_input_id, plan_step_id FROM execution_grants
		WHERE owner_id = ? AND task_id = ? AND status = 'bound' LIMIT 1`, owner, task).Scan(&origin.PreparedInputID, &origin.PlanStepID)
	if errors.Is(err, sql.ErrNoRows) {
		return taskretry.Origin{}, nil
	}
	return origin, err
}

// GrantByKey returns the owner's grant carrying an idempotency key. Keys are
// unique per owner, so another owner's key never matches.
func (r *Repository) GrantByKey(ctx context.Context, owner, key string) (executiongrant.Grant, bool, error) {
	grant, err := scanGrant(r.db.QueryRowContext(ctx, grantSelect+` WHERE owner_id = ? AND idempotency_key = ?`, owner, key))
	if errors.Is(err, sql.ErrNoRows) {
		return executiongrant.Grant{}, false, nil
	}
	if err != nil {
		return executiongrant.Grant{}, false, err
	}
	return grant, true, nil
}
