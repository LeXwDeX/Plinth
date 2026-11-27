package generationtool_test

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
)

// #702: in auto mode the step after every 100 approved steps of the session
// asks a person once. The policy decides a recorded request only once: when
// other approvals later move the session's count past the checkpoint, the
// waiting request is still the person's, and only a new call is decided
// against the new count.
func TestAutoModeCheckpointCardsTheStepAfterAHundredApprovals(t *testing.T) {
	approved := 100
	executor, command, gateway, repo := policyHarnessConfigured(t, sessionMode{mode: "auto"}, "refine_image",
		"text-to-image", func(service *executiongrant.Service) {
			service.Policy = service.Policy.WithCheckpoints(map[string]int{"auto": 100, "budget": 100})
			service.SessionPaidSteps = func(context.Context, string, string) (int, error) { return approved, nil }
		})
	ctx := context.Background()
	first, err := executor.Execute(ctx, command, nil)
	if err != nil || first.Suspend || !first.NoSubmission || gateway.submitCount != 0 {
		t.Fatalf("the 101st step must wait for a person: %+v submits=%d err=%v", first, gateway.submitCount, err)
	}
	approved = 101
	resumed, err := executor.Execute(ctx, command, nil)
	if err != nil || !resumed.NoSubmission || gateway.submitCount != 0 {
		t.Fatalf("a later count approved the waiting request: %+v submits=%d err=%v", resumed, gateway.submitCount, err)
	}
	op, err := repo.LoadAgentOperation(ctx, "owner-a", "session-a", "turn-a", "call-a")
	if err != nil || !op.PolicyEvaluated {
		t.Fatalf("operation = %+v err=%v", op, err)
	}
	if grant, err := repo.GetExecutionGrant(ctx, "owner-a", op.GrantRequestID); err != nil || grant.Status != executiongrant.Pending {
		t.Fatalf("grant = %+v err=%v", grant, err)
	}
	next := command
	next.Call.ID = "call-b"
	if got, err := executor.Execute(ctx, next, nil); err != nil || got.NoSubmission || gateway.submitCount != 1 {
		t.Fatalf("the step after the approval should run: %+v submits=%d err=%v", got, gateway.submitCount, err)
	}
}
