package processing

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
)

type switchableMode struct{ mode string }

func (m *switchableMode) SessionApprovalMode(context.Context, string, string) (string, error) {
	return m.mode, nil
}

// #665: the approval mode decides a plan step once. A step left to a person
// in the strict mode is not approved by the auto mode when the plan comes back
// to the same frozen step after the person switched modes; a person decides it.
func TestPlanStepLeftToAPersonIsNotReapprovedAfterAModeSwitch665(t *testing.T) {
	ctx := context.Background()
	service, gateway, grants, plan := policyPlanFixture(t, "retexture", "manual")
	mode := &switchableMode{mode: "manual"}
	policy := approvalpolicy.New("manual", []string{"auto", "manual"},
		[]string{"text-to-3d", "image-to-3d", "retexture"},
		[]string{"text-to-3d", "image-to-3d", "retexture", "remesh"})
	service.WithGrants(&executiongrant.Service{Grants: grants, Prepared: grants, Policy: policy, Modes: mode})
	if err := service.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	paused, err := service.Get(ctx, "owner", plan.ID)
	if err != nil || paused.State != Paused || paused.Failure != ReasonConsentRequired || paused.Steps[0].PreparedInputID == "" {
		t.Fatalf("strict mode plan = %+v err=%v", paused, err)
	}
	mode.mode = "auto"
	// The plan returns to the same frozen step (a reconcile that keeps the
	// frozen input, a crash replay): the pending grant is the same one.
	store := service.store.(*memoryStore)
	again := store.plans[plan.ID]
	again.State, again.Failure = Pending, ""
	again.Steps[0].State, again.Steps[0].Failure, again.Steps[0].Resumable = Pending, "", false
	store.plans[plan.ID] = again
	if err := service.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(ctx, "owner", plan.ID)
	if err != nil || got.State != Paused || got.Failure != ReasonConsentRequired {
		t.Fatalf("plan after the switch = %+v err=%v", got, err)
	}
	if grants.approved != 0 || len(gateway.commands) != 0 {
		t.Fatalf("the auto mode re-decided a step left to a person: approvals=%d submissions=%d",
			grants.approved, len(gateway.commands))
	}
}
