package processing

import (
	"context"
	"fmt"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

type ReconcileCommand struct {
	OwnerID             generation.OwnerID `json:"-"`
	PlanID              string             `json:"-"`
	ExpectedPlanVersion int64              `json:"expected_plan_version"`
}

func stepIdempotencyKey(plan Plan, index int) string {
	if key := plan.Steps[index].IdempotencyKey; key != "" {
		return key
	}
	// Historical plans did not persist the key. Recover its exact original
	// identity from the plan and attempt, never allocate a replacement key.
	return fmt.Sprintf("processing:%s:%d:%d", plan.ID, index, plan.Steps[index].Retry)
}

// Reconcile only reads the kernel and updates the plan's reference. It never
// prepares, authorizes, submits, changes a Task, or increments the paid attempt.
func (s *Service) Reconcile(ctx context.Context, command ReconcileCommand) (Plan, error) {
	if command.OwnerID == "" || command.PlanID == "" || command.ExpectedPlanVersion <= 0 {
		return Plan{}, ErrInvalid
	}
	plan, err := s.Get(ctx, command.OwnerID, command.PlanID)
	if err != nil {
		return Plan{}, err
	}
	if plan.Version != command.ExpectedPlanVersion {
		return Plan{}, ErrConflict
	}
	for index := range plan.Steps {
		step := &plan.Steps[index]
		if step.State != Paused || step.Failure != ReasonSubmitUnreconciled {
			continue
		}
		if plan.State != Paused || step.TaskID != "" {
			return Plan{}, ErrConflict
		}
		key := stepIdempotencyKey(plan, index)
		task, lookupErr := s.gateway.LookupByIdempotency(ctx, plan.OwnerID, key)
		switch {
		case generation.IsCode(lookupErr, generation.ErrNotFound):
			// SQLite's lookup returns NotFound only for an unreserved key. A
			// tombstoned Task leaves its key reserved and returns Conflict instead.
			// Keep any frozen input/grant: execution_grants reserves the key for
			// that exact identity even before a Task exists. Resume requires a
			// fresh user action at today's cost; changed frozen costs fail closed.
			// A legacy step without a frozen input prepares/authorizes normally.
			// advance always rechecks this SAME key before any submission.
			step.IdempotencyKey, step.SubmissionUncertain, step.Retryable = key, false, false
			if err := s.stop(ctx, &plan, step, Paused, ReasonReconciledAbsent); err != nil {
				return Plan{}, err
			}
			return plan, nil
		case lookupErr != nil:
			return Plan{}, ErrReconcileUndetermined
		}
		// No cross-owner/session or unrelated offering may be linked. Even a
		// malformed gateway result must fail closed without exposing the Task.
		if task.ID == "" || task.OwnerID != plan.OwnerID || task.SessionID != plan.SessionID ||
			task.Offering.Domain != generation.DomainProcessing || task.Capability != step.Capability || task.Offering.ID != step.OfferingID {
			return Plan{}, ErrReconcileUndetermined
		}
		verifyErr := s.verifyRecoveredStep(ctx, task, plan, step, key)
		if verifyErr != nil && !replayRejected(verifyErr) {
			return Plan{}, ErrReconcileUndetermined
		}
		step.TaskID, step.IdempotencyKey, step.State = task.ID, key, Running
		step.Failure, step.SubmissionUncertain, step.Resumable, step.Retryable = "", false, false, false
		plan.State, plan.Failure = Running, ""
		if verifyErr != nil {
			step.Failure, plan.Failure = ReasonReconciledMismatch, ReasonReconciledMismatch
		}
		plan.UpdatedAt = time.Now().UTC()
		if err := s.savePlan(ctx, &plan); err != nil {
			return Plan{}, err
		}
		return plan, nil
	}
	// Repeating with the current version is a read-only no-op. An old version
	// still conflicts, so a delayed click cannot resolve a different attempt.
	for _, step := range plan.Steps {
		if step.TaskID != "" || step.Failure == ReasonReconciledAbsent {
			return plan, nil
		}
	}
	return Plan{}, ErrConflict
}
