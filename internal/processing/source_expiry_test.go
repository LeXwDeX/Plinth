package processing

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #325: a step whose source's original Provider task is gone fails with that
// reason and is not offered as a paid retry: the same frozen input would be
// refused again. A new plan binds the saved model file instead.
func TestAnExpiredSourceTaskFailureIsNotRetryable(t *testing.T) {
	ctx := context.Background()
	service, _, gateway, _, plan := singleRemeshFixture(t)
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	task := gateway.tasks["step-1"]
	task.State = generation.TaskFailed
	task.Failure = &generation.Failure{Class: generation.FailureSourceExpired}
	gateway.tasks[task.ID] = task
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	failed, _ := service.Get(ctx, "owner", plan.ID)
	if failed.State != Failed || failed.Failure != ReasonSourceExpired || failed.Steps[0].Failure != ReasonSourceExpired || failed.Steps[0].Retryable {
		t.Fatalf("failed = %#v", failed)
	}
	if _, err := service.Retry(ctx, RetryCommand{OwnerID: "owner", PlanID: plan.ID, ExpectedPlanVersion: failed.Version, ExpectedOfferingRevision: "v1"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("retry of an expired source = %v; want ErrConflict", err)
	}
	if len(gateway.commands) != 1 {
		t.Fatalf("submitted %d tasks", len(gateway.commands))
	}
}

func TestAnExpiredSourceRefusedAtSubmissionNamesTheReason(t *testing.T) {
	ctx := context.Background()
	service, _, gateway, _, plan := singleRemeshFixture(t)
	gateway.submitError = &generation.Error{Code: generation.ErrInvalidInput, Operation: "resolve_task_dependencies", Cause: generation.ErrSourceExpired}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	failed, _ := service.Get(ctx, "owner", plan.ID)
	if failed.State != Failed || failed.Failure != ReasonSourceExpired || failed.Steps[0].Retryable {
		t.Fatalf("rejected = %#v", failed)
	}
}
