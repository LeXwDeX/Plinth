package processing

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestPreparationFailureResumesBeforeSeparateConsent(t *testing.T) {
	for _, failure := range []struct {
		name string
		err  error
	}{
		{"unavailable", &generation.Error{Code: generation.ErrUnavailable}},
		{"internal", &generation.Error{Code: generation.ErrInternal}},
		{"storage", errors.New("temporary storage failure")},
	} {
		t.Run(failure.name, func(t *testing.T) {
			ctx := context.Background()
			service, _, gateway, _, plan := singleRemeshFixture(t)
			gateway.prepareError = failure.err
			if err := service.advanceAll(ctx); err != nil {
				t.Fatal(err)
			}
			paused, _ := service.Get(ctx, "owner", plan.ID)
			step := paused.Steps[0]
			if paused.State != Paused || paused.Failure != ReasonPrepareUnavailable || !step.Resumable || step.SubmissionUncertain || step.PreparedInputID != "" || len(gateway.commands) != 0 {
				t.Fatalf("preparation failure = %#v, submits=%d", paused, len(gateway.commands))
			}
			gateway.prepareError = nil
			// A restored dependency alone must not resume the plan in the background.
			if err := service.advanceAll(ctx); err != nil {
				t.Fatal(err)
			}
			stillPaused, _ := service.Get(ctx, "owner", plan.ID)
			if stillPaused.Version != paused.Version || len(gateway.prepared) != 0 {
				t.Fatalf("paused plan advanced without explicit resume: %#v", stillPaused)
			}
			resumed, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: plan.ID,
				ExpectedPlanVersion: paused.Version, ExpectedOfferingRevision: "v1"})
			if err != nil || resumed.State != Pending || resumed.Steps[0].Retry != 0 {
				t.Fatalf("resume = %#v, %v", resumed, err)
			}
			if err := service.advanceAll(ctx); err != nil {
				t.Fatal(err)
			}
			awaitingConsent, _ := service.Get(ctx, "owner", plan.ID)
			step = awaitingConsent.Steps[0]
			wantKey := "processing:" + plan.ID + ":0:0"
			if awaitingConsent.Failure != ReasonConsentRequired || step.PreparedInputID == "" || step.AuthorizationRef != "" || step.IdempotencyKey != wantKey || len(gateway.commands) != 0 {
				t.Fatalf("recovery must require a separate decision: %#v", awaitingConsent)
			}
			if _, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: plan.ID,
				ExpectedPlanVersion: awaitingConsent.Version, ExpectedOfferingRevision: "v1",
				AuthorizationRef: "test-human-approved"}); err != nil {
				t.Fatal(err)
			}
			if err := service.advanceAll(ctx); err != nil {
				t.Fatal(err)
			}
			if len(gateway.commands) != 1 || gateway.commands[0].IdempotencyKey != wantKey {
				t.Fatalf("approved recovery submissions = %#v", gateway.commands)
			}
		})
	}
}

func TestDeterministicPreparationFailureRemainsTerminal(t *testing.T) {
	for _, code := range []generation.ErrorCode{generation.ErrInvalidInput, generation.ErrForbidden, generation.ErrNotFound, generation.ErrConflict} {
		t.Run(string(code), func(t *testing.T) {
			ctx := context.Background()
			service, _, gateway, _, plan := singleRemeshFixture(t)
			gateway.prepareError = &generation.Error{Code: code, Operation: "prepare_input"}
			if err := service.advanceAll(ctx); err != nil {
				t.Fatal(err)
			}
			failed, _ := service.Get(ctx, "owner", plan.ID)
			if failed.State != Failed || failed.Failure != ReasonSubmitRejected || failed.Steps[0].Resumable || failed.Steps[0].Retryable || len(gateway.commands) != 0 {
				t.Fatalf("deterministic rejection = %#v", failed)
			}
			if _, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: plan.ID,
				ExpectedPlanVersion: failed.Version, ExpectedOfferingRevision: "v1"}); !errors.Is(err, ErrConflict) {
				t.Fatalf("terminal rejection resumed: %v", err)
			}
		})
	}
}
