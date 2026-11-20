package generationtool_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
)

// #406: a session that kept the Schema's default approval mode. The tests read
// the mode from the shipped Schema, the way the session service gives it to a
// new session, and fail loudly when the default stops being auto: what they
// check (no card, no plan, no waiting person) is what auto means.

func defaultApprovalMode406(t *testing.T) string {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	mode := product.Application().Chat.ApprovalPolicy.DefaultMode
	if mode != "auto" {
		t.Fatalf("the shipped Schema's default approval mode = %q, want auto", mode)
	}
	return mode
}

// The reported request: four pictures in one go. Under the default mode every
// step is approved by the session policy and submitted inside the turn; no
// approval card is created and no production plan is waiting for the person.
func TestDefaultModeRunsAFourPictureRequestWithoutCardsOrAPlan_406(t *testing.T) {
	executor, command, gateway, repo := policyHarness(t, sessionMode{mode: defaultApprovalMode406(t)}, "refine_image", "text-to-image")
	ctx := context.Background()
	for index, prompt := range []string{"front view", "side view", "back view", "top view"} {
		callID := fmt.Sprintf("call-%d", index)
		var observed []string
		result, err := executor.Execute(ctx, withCall(command, callID, `{"prompt":"`+prompt+`"}`), func(o chat.ToolObservation) error {
			observed = append(observed, o.ApprovalSource)
			return nil
		})
		if err != nil || result.Suspend || result.NoSubmission {
			t.Fatalf("picture %d waited or was not submitted: %+v err=%v", index+1, result, err)
		}
		if len(observed) == 0 || observed[0] != "policy" {
			t.Fatalf("picture %d approval source = %v, want policy", index+1, observed)
		}
		op, err := repo.LoadAgentOperation(ctx, "owner-a", "session-a", "turn-a", callID)
		if err != nil || op.Detached {
			t.Fatalf("picture %d left a detached card behind: %+v err=%v", index+1, op, err)
		}
		grant, err := repo.GetExecutionGrant(ctx, "owner-a", op.GrantRequestID)
		if err != nil || grant.DecidedBy != "policy:auto" || !grant.ApprovedByPolicy() {
			t.Fatalf("picture %d grant = %+v err=%v, want approved by policy:auto", index+1, grant, err)
		}
	}
	if gateway.submitCount != 4 {
		t.Fatalf("submitted %d pictures, want 4", gateway.submitCount)
	}
	if cards := pendingCards(t, repo, "session-a", time.Now().Add(-time.Hour)); cards != 0 {
		t.Fatalf("%d approval cards were created", cards)
	}
	plans, err := repo.PendingProductionBudgets(ctx, "owner-a", "session-a")
	if err != nil || len(plans) != 0 {
		t.Fatalf("a production plan is waiting for the person: %+v err=%v", plans, err)
	}
	if _, found, err := repo.CurrentProductionBudget(ctx, "owner-a", "session-a"); err != nil || found {
		t.Fatalf("a production plan exists: found=%v err=%v", found, err)
	}
}

// What the default now approves that strict mode asks a person about. The list
// is the shipped Schema's manual_approval_capabilities; docs/product.md §6.1
// states it. Switching the default to auto moved no paid step into a person's
// hands, and moved these into nobody's.
func TestDefaultModeApprovesTheThreeDStepsStrictModeAsksAbout_406(t *testing.T) {
	mode := defaultApprovalMode406(t)
	for name, c := range map[string]struct{ tool, capability string }{
		"text to 3D":  {"create_model_3d", "text-to-3d"},
		"image to 3D": {"create_model_3d_from_image", "image-to-3d"},
	} {
		strict, _, strictObserved := runPolicyCase(t, "manual", c.tool, c.capability)
		if !strict.NoSubmission || len(strictObserved) != 0 {
			t.Fatalf("%s: strict mode did not ask a person: %+v", name, strict)
		}
		result, gateway, observed := runPolicyCase(t, mode, c.tool, c.capability)
		if result.Suspend || result.NoSubmission || gateway.submitCount != 1 {
			t.Fatalf("%s: the default mode waited: %+v submits=%d", name, result, gateway.submitCount)
		}
		if len(observed) == 0 || observed[0].ApprovalSource != "policy" {
			t.Fatalf("%s: observation = %+v", name, observed)
		}
	}
}

// A step that spends extra credits (extra export formats) is a card in strict
// mode and is granted with the rest of the step in the default mode.
func TestDefaultModeGrantsExtraCreditsWithoutACard_406(t *testing.T) {
	h := newProcessingHarness(t, defaultApprovalMode406(t), meshyCatalogTasks)
	result := h.run3D("call-1", []string{"FBX", "USDZ"}, nil)
	if result.Suspend || result.NoSubmission || h.gateway.submitCount != 1 {
		t.Fatalf("extra formats under the default mode waited or were not submitted: %+v submits=%d", result, h.gateway.submitCount)
	}
	op, err := h.gateway.repo.LoadAgentOperation(context.Background(), "owner-a", "session-a", "turn-a", "call-1")
	if err != nil || op.Detached {
		t.Fatalf("a card was created: %+v err=%v", op, err)
	}
	grant, err := h.gateway.repo.GetExecutionGrant(context.Background(), "owner-a", op.GrantRequestID)
	if err != nil || grant.Status == executiongrant.Pending || grant.DecidedBy != "policy:auto" {
		t.Fatalf("grant = %+v err=%v, want decided by policy:auto", grant, err)
	}
}
