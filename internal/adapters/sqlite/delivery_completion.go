package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

var _ generation.AttemptReader = (*Repository)(nil)
var _ generation.TaskGrantReader = (*Repository)(nil)
var _ interface {
	CommitDeliveryCompletion(context.Context, generation.ValidatedDeliveryCompletion) error
} = (*Repository)(nil)

// LoadAttemptForTask reads one historical Attempt of the owner's Task. The
// refetch source must keep resolving after the source Task itself becomes
// SUCCEEDED through a local delivery completion, and after its local Task row
// is tombstoned while an already-started refetch still needs the provider job
// (#330 third follow-ups). The Task row must exist and belong to the owner.
func (r *Repository) LoadAttemptForTask(
	ctx context.Context,
	owner generation.OwnerID,
	taskID generation.TaskID,
	attemptID generation.AttemptID,
) (generation.Attempt, error) {
	if owner == "" || taskID == "" || attemptID == "" {
		return generation.Attempt{}, generation.RepositoryNotFound
	}
	var exists int
	// A tombstoned source may still be needed by its already-started refetch
	// (the provider job and bytes do not disappear with the local tombstone).
	err := r.db.QueryRowContext(ctx, `
		SELECT 1 FROM generation_tasks
		WHERE id = ? AND owner_id = ?`, taskID, owner).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return generation.Attempt{}, generation.RepositoryNotFound
	}
	if err != nil {
		return generation.Attempt{}, err
	}
	attempt, err := loadAttempt(ctx, r.db, attemptID)
	if errors.Is(err, sql.ErrNoRows) {
		return generation.Attempt{}, generation.RepositoryNotFound
	}
	if err != nil {
		return generation.Attempt{}, err
	}
	if attempt.TaskID != taskID {
		return generation.Attempt{}, generation.RepositoryNotFound
	}
	return attempt, nil
}

// BoundGrantForTask reads the immutable bound grant of one Task. It returns
// false for a Task without a bound grant (legacy or free refetch).
func (r *Repository) BoundGrantForTask(
	ctx context.Context,
	owner generation.OwnerID,
	taskID generation.TaskID,
) (generation.TaskGrant, bool, error) {
	if owner == "" || taskID == "" {
		return generation.TaskGrant{}, false, nil
	}
	var grant generation.TaskGrant
	err := r.db.QueryRowContext(ctx, `SELECT request_id, operation_id, plan_step_id,
		prepared_input_id, prepared_input_hash, idempotency_key, offering_id,
		offering_revision, status, task_id FROM execution_grants
		WHERE owner_id = ? AND task_id = ? AND status = 'bound' LIMIT 1`, owner, taskID).Scan(
		&grant.RequestID, &grant.OperationID, &grant.PlanStepID,
		&grant.PreparedInputID, &grant.PreparedInputHash, &grant.IdempotencyKey,
		&grant.OfferingID, &grant.OfferingRevision, &grant.Status, &grant.TaskID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return generation.TaskGrant{}, false, nil
	}
	if err != nil {
		return generation.TaskGrant{}, false, err
	}
	return grant, true, nil
}
