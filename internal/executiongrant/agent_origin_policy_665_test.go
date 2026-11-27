package executiongrant

import (
	"context"
	"errors"
	"testing"
)

// #665: a step of an Agent-created processing plan is asked for as a browser
// grant carrying the plan's budget eligibility. It follows the Agent's extra
// spend rule, like the Agent's own tool steps: in manual mode extra credits
// under the cost threshold still wait for a person.
func TestAgentPlanStepExtraSpendNeedsAPersonInManualMode(t *testing.T) {
	store := policyFixture("text-to-image")
	store.grant.BudgetEligible = true // HumanBrowser + eligible: an Agent plan step
	store.snapshot.ExtraCredits = 5
	store.snapshot.EstimatedCredits = 10
	service := policyService(store, &modeReader{mode: "manual"})
	service.Policy = service.Policy.WithCreditsThreshold(20)
	if _, err := service.ApproveByPolicy(context.Background(), "owner-a", "request-a", "manual"); !errors.Is(err, ErrHumanRequired) {
		t.Fatalf("manual Agent plan step with extra credits: %v", err)
	}
	if store.approved != 0 || store.grant.Status != Pending {
		t.Fatalf("grant changed: %+v", store.grant)
	}
	// A browser step the person started is not the Agent's.
	store = policyFixture("text-to-image")
	store.snapshot.ExtraCredits = 5
	store.snapshot.EstimatedCredits = 10
	service = policyService(store, &modeReader{mode: "manual"})
	service.Policy = service.Policy.WithCreditsThreshold(20)
	if got, err := service.ApplyPolicy(context.Background(), store.grant); err != nil || got.Status != Approved {
		t.Fatalf("panel step: %+v err=%v", got, err)
	}
}

// #665: an Agent step of an Offering that bills in credits but has no
// estimate and no native quote cannot be priced; in manual mode with a cost
// threshold it waits for a person instead of passing as free. Auto approves it.
func TestUnpricedMeteredAgentStepIsNotFreeUnderTheManualThreshold(t *testing.T) {
	unpriced := func() *fakeGrantStore {
		store := policyFixture("text-to-image")
		store.grant.Source, store.grant.TriggerTurnID = AgentPending, "turn-a"
		store.snapshot.CreditsMetered = true
		return store
	}
	store := unpriced()
	service := policyService(store, &modeReader{mode: "manual"})
	service.Policy = service.Policy.WithCreditsThreshold(20)
	if _, err := service.ApproveByPolicy(context.Background(), "owner-a", "request-a", "manual"); !errors.Is(err, ErrHumanRequired) {
		t.Fatalf("manual unpriced metered step: %v", err)
	}
	store = unpriced()
	service = policyService(store, &modeReader{mode: "auto"})
	service.Policy = service.Policy.WithCreditsThreshold(20)
	if got, err := service.ApplyPolicy(context.Background(), store.grant); err != nil || got.Status != Approved {
		t.Fatalf("auto unpriced metered step: %+v err=%v", got, err)
	}
	// A priced step under the threshold is still automatic in manual mode.
	store = unpriced()
	store.snapshot.EstimatedCredits = 5
	service = policyService(store, &modeReader{mode: "manual"})
	service.Policy = service.Policy.WithCreditsThreshold(20)
	if got, err := service.ApplyPolicy(context.Background(), store.grant); err != nil || got.Status != Approved {
		t.Fatalf("manual priced step: %+v err=%v", got, err)
	}
}

// #665 (#294): a step the Agent started that would repeat a charged-but-unstored
// attempt is never the policy's to approve, in any mode, while a step the person
// started keeps following the mode.
func TestRechargeGuardKeepsAgentStepsForAPersonInEveryMode(t *testing.T) {
	var asked []string
	guard := func(_ context.Context, owner, session, capability string) (string, error) {
		asked = append(asked, owner+"/"+session+"/"+capability)
		return "task-charged", nil
	}
	for _, source := range []struct {
		name     string
		source   string
		turn     string
		eligible bool
	}{{"agent tool step", AgentPending, "turn-a", false}, {"agent plan step", HumanBrowser, "", true}} {
		store := policyFixture("image-to-image")
		store.grant.Source, store.grant.TriggerTurnID, store.grant.BudgetEligible = source.source, source.turn, source.eligible
		service := policyService(store, &modeReader{mode: "auto"})
		service.Recharge = guard
		got, err := service.ApplyPolicy(context.Background(), store.grant)
		if err != nil || got.Status != Pending || store.approved != 0 {
			t.Fatalf("%s in auto mode: %+v err=%v approvals=%d", source.name, got, err, store.approved)
		}
	}
	if len(asked) != 2 || asked[0] != "owner-a/session-a/image-to-image" {
		t.Fatalf("guard asked %v", asked)
	}
	// The person's own browser step is the decision that clears the guard.
	store := policyFixture("image-to-image")
	service := policyService(store, &modeReader{mode: "auto"})
	service.Recharge = guard
	if got, err := service.ApplyPolicy(context.Background(), store.grant); err != nil || got.Status != Approved {
		t.Fatalf("panel step: %+v err=%v", got, err)
	}
	if len(asked) != 2 {
		t.Fatalf("guard consulted for a person's step: %v", asked)
	}
	// A guard failure fails closed.
	store = policyFixture("image-to-image")
	store.grant.Source, store.grant.TriggerTurnID = AgentPending, "turn-a"
	service = policyService(store, &modeReader{mode: "auto"})
	failure := errors.New("task list failed")
	service.Recharge = func(context.Context, string, string, string) (string, error) { return "", failure }
	if got, err := service.ApplyPolicy(context.Background(), store.grant); !errors.Is(err, failure) || got.Status != Pending {
		t.Fatalf("guard failure: %+v err=%v", got, err)
	}
}
