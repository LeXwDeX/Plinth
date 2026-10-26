package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// VerifyTaskGrantReplay preserves legacy Task replay and verifies the original
// immutable grant identity for admitted Tasks, independent of today's selection.
func (r *Repository) VerifyTaskGrantReplay(ctx context.Context, task generation.Task, admission *generation.ExecutionAdmission) error {
	return verifyTaskGrantReplayOn(ctx, r.db, task, admission)
}

// The Create race branch uses the same connection holding BEGIN IMMEDIATE.
// Using the public reader here would release atomicity or deadlock one-conn DBs.
func verifyTaskGrantReplayOn(ctx context.Context, db harnessReader, task generation.Task, admission *generation.ExecutionAdmission) error {
	g, err := scanGrant(db.QueryRowContext(ctx, grantSelect+` WHERE task_id=? AND owner_id=? AND status='bound'`, task.ID, task.OwnerID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	} // historical Task with no grant
	if err != nil {
		return err
	}
	task.Admission = admission
	requestID, identity, err := taskGrantIdentity(task)
	if err != nil {
		return err
	}
	if g.RequestID != requestID || g.Status != executiongrant.Bound || g.TaskID != string(task.ID) || g.Identity != identity {
		return generation.RepositoryAuthorizationRequired
	}
	return nil
}
