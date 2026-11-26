package sqlite

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
)

// #665: only a group the auto mode started drops its right view when the mode
// changes. In the strict mode the session policy approves the cheap views as
// policy:manual; a four-view request there finishes its required views as a
// plain three-view group instead of pausing on a right-view card that was
// never raised.
func TestManualPolicyApprovedGroupDoesNotWaitOnTheRightView665(t *testing.T) {
	f := newContinuationFixtureWithMode(t, false, "manual")
	ctx := context.Background()
	var last string
	for i, key := range []string{"front", "left", "back"} {
		if _, _, err := f.runtime.PrepareStep(ctx, "owner-a", f.run.ID, key); err != nil {
			t.Fatal(err)
		}
		_, grant, err := f.runtime.RequestAgentGrant(ctx, "owner-a", f.run.ID, key, "turn-a")
		if err != nil || grant.Status != executiongrant.Approved || grant.DecidedBy != executiongrant.PolicyActorPrefix+"manual" {
			t.Fatalf("%s grant = %+v err=%v", key, grant, err)
		}
		_, accepted, err := f.runtime.SubmitStep(ctx, "owner-a", f.run.ID, key)
		if err != nil {
			t.Fatal(err)
		}
		last = string(accepted.TaskID)
		f.finish(t, last, "asset-"+key)
		if i < 2 {
			if _, err := f.views.ContinueWith(ctx, "owner-a", "session-a", f.run.ID, last, "turn-a", true); err != nil {
				t.Fatal(err)
			}
		}
	}
	result, err := f.views.ContinueWith(ctx, "owner-a", "session-a", f.run.ID, last, "turn-a", true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Paused || len(result.Completed) != 3 {
		t.Fatalf("strict-mode group = %+v, want the three required views complete", result)
	}
	if right := f.step(t, "right"); right.TaskID != "" || right.GrantRequestID != "" {
		t.Fatalf("the strict mode touched the right view: %+v", right)
	}
}
