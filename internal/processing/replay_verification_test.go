package processing

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestRecoveredStepRequiresFrozenInputAndGrantInBothPaths(t *testing.T) {
	for _, mode := range []string{"resume", "advance"} {
		for _, mismatch := range []string{"input", "grant", "legacy", "none"} {
			t.Run(mode+"/"+mismatch, func(t *testing.T) {
				ctx := context.Background()
				service, store, gateway, _, plan := singleRemeshFixture(t)
				gateway.submitError = errors.New("lost response")
				if err := advanceApproved(ctx, service); err != nil {
					t.Fatal(err)
				}
				paused, _ := service.Get(ctx, "owner", plan.ID)
				step := &paused.Steps[0]
				key := "processing:" + plan.ID + ":0:0"
				landed := generation.Task{ID: "landed", OwnerID: "owner", SessionID: "session", Capability: "remesh",
					Offering: generation.Offering{ID: "remesh", Revision: "v1"}, State: generation.TaskRunning,
					Input: gateway.prepared[step.PreparedInputID].NormalizedInput}
				switch mismatch {
				case "input":
					landed.Input = []byte(`{"source_task":"different-source"}`)
				case "grant":
					step.AuthorizationRef = "different-grant"
				case "legacy":
					step.PreparedInputID = ""
				}
				gateway.byKey[key], gateway.tasks[landed.ID] = landed.ID, landed
				if mode == "advance" {
					paused.State, step.State = Pending, Pending
				}
				store.plans[plan.ID] = paused
				if mode == "resume" {
					if _, err := service.Resume(ctx, ResumeCommand{OwnerID: "owner", PlanID: plan.ID,
						ExpectedPlanVersion: paused.Version, ExpectedOfferingRevision: "v1"}); err != nil {
						t.Fatal(err)
					}
				} else if err := service.advanceAll(ctx); err != nil {
					t.Fatal(err)
				}
				result, _ := service.Get(ctx, "owner", plan.ID)
				if mismatch == "none" {
					if result.State != Running || result.Steps[0].TaskID != landed.ID {
						t.Fatalf("verified task not adopted: %#v", result)
					}
				} else if result.State != Paused || result.Failure != ReasonSubmitUnreconciled || result.Steps[0].Resumable || result.Steps[0].TaskID != "" {
					t.Fatalf("unverified task adopted or retry permitted: %#v", result)
				}
				if len(gateway.commands) != 0 {
					t.Fatalf("reconciliation submitted %d new tasks", len(gateway.commands))
				}
			})
		}
	}
}
