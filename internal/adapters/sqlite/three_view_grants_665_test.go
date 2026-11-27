package sqlite

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/threeview"
)

// switchModes is a session approval mode the person changes mid-run.
type switchModes struct{ mode string }

func (m *switchModes) SessionApprovalMode(context.Context, string, string) (string, error) {
	return m.mode, nil
}

// #665: a three-view card left to a person in the strict mode is not approved
// by a later switch to the auto mode when the call replays: the policy decided
// the grant once, before the step recorded it, like the generation tools'
// PolicyEvaluated rule. Only the person approves it after that.
func TestThreeViewPendingCardIsNotApprovedByALaterModeSwitch(t *testing.T) {
	ctx := context.Background()
	f := newContinuationFixtureWithMode(t, false, "manual")
	modes := &switchModes{mode: "manual"}
	f.runtime.Grants.Modes = modes
	f.runtime.Grants.Policy = approvalpolicy.New("manual", []string{"auto", "manual", "budget"},
		[]string{"image-to-image"}, []string{"image-to-image"})
	if _, _, err := f.runtime.PrepareStep(ctx, "owner-a", f.run.ID, "front"); err != nil {
		t.Fatal(err)
	}
	_, grant, err := f.runtime.RequestAgentGrant(ctx, "owner-a", f.run.ID, "front", "turn-a")
	if err != nil || grant.Status != executiongrant.Pending {
		t.Fatalf("strict front grant = %+v err=%v, want pending", grant, err)
	}
	modes.mode = "auto"
	_, replayed, err := f.runtime.RequestAgentGrant(ctx, "owner-a", f.run.ID, "front", "turn-a")
	if err != nil || replayed.Status != executiongrant.Pending || replayed.RequestID != grant.RequestID {
		t.Fatalf("replay after the switch = %+v err=%v, want the same pending grant", replayed, err)
	}
	approved, err := f.runtime.Grants.Decide(ctx, executiongrant.Principal{OwnerID: "owner-a", ActorID: "owner-a",
		Source: executiongrant.HumanBrowser}, grant.RequestID, executiongrant.Approve)
	if err != nil || approved.Status != executiongrant.Approved {
		t.Fatalf("the person's approval = %+v err=%v", approved, err)
	}
}

// #665 (#294): in the auto mode a later view that would repeat a charged but
// unstored attempt is not submitted by the continuation: its grant waits for a
// person and the group pauses.
func TestThreeViewContinuationLeavesARechargeToAPerson(t *testing.T) {
	ctx := context.Background()
	f := newContinuationFixtureWithMode(t, false, "auto")
	if _, _, err := f.runtime.PrepareStep(ctx, "owner-a", f.run.ID, "front"); err != nil {
		t.Fatal(err)
	}
	_, grant, err := f.runtime.RequestAgentGrantWithBudget(ctx, "owner-a", f.run.ID, "front", "turn-a")
	if err != nil || grant.Status != executiongrant.Approved || !grant.ApprovedByPolicy() {
		t.Fatalf("auto front grant = %+v err=%v", grant, err)
	}
	_, accepted, err := f.runtime.SubmitStep(ctx, "owner-a", f.run.ID, "front")
	if err != nil {
		t.Fatal(err)
	}
	f.finish(t, string(accepted.TaskID), "asset-front-665")
	f.runtime.Grants.Recharge = func(_ context.Context, owner, session, capability string) (string, error) {
		if owner != "owner-a" || session != "session-a" || capability != string(generation.ImageToImage) {
			t.Errorf("guard asked for %s/%s/%s", owner, session, capability)
		}
		return "task-charged-unstored", nil
	}
	submits := f.gateway.submits
	result, err := f.views.Continue(ctx, "owner-a", "session-a", f.run.ID, string(accepted.TaskID), "turn-a")
	if err != nil {
		t.Fatal(err)
	}
	// #702: a grant left to a person waits on its card; it is not a budget gap.
	if !result.Paused || result.Reason != threeview.ReasonViewAwaitingApproval || result.TaskID != "" || f.gateway.submits != submits {
		t.Fatalf("continuation = %+v submits %d->%d, want paused without a submit", result, submits, f.gateway.submits)
	}
	left := f.step(t, "left")
	if left.GrantRequestID == "" {
		t.Fatalf("left view has no grant for the person: %+v", left)
	}
	if got, err := f.repo.GetExecutionGrant(ctx, "owner-a", left.GrantRequestID); err != nil || got.Status != executiongrant.Pending {
		t.Fatalf("left grant = %+v err=%v, want pending", got, err)
	}
}

// #702: in the auto mode the approval window can fall between two views. A
// three-view group is a bounded 2D series the person already let run, and the
// continuation's view grant has no card a person could answer, so the window
// does not stop it: the view is submitted, and its grant still counts toward
// the window.
func TestThreeViewContinuationRunsThroughTheApprovalWindow(t *testing.T) {
	ctx := context.Background()
	f := newContinuationFixtureWithMode(t, false, "auto")
	task := f.startFrontByPolicy(t)
	f.finish(t, task, "asset-front-702")
	f.runtime.Grants.Policy = f.runtime.Grants.Policy.WithCheckpoints(map[string]int{"auto": 100})
	// 99 earlier approvals plus the front view: the left view is the 101st.
	f.runtime.Grants.SessionPaidSteps = func(ctx context.Context, owner, session string) (int, error) {
		n, err := f.repo.SessionApprovedGrants(ctx, owner, session)
		return n + 99, err
	}
	if n, _ := f.runtime.Grants.SessionPaidSteps(ctx, "owner-a", "session-a"); n != 100 {
		t.Fatalf("approved before the left view = %d, want 100", n)
	}
	submits := f.gateway.submits
	result, err := f.views.Continue(ctx, "owner-a", "session-a", f.run.ID, task, "turn-a")
	if err != nil {
		t.Fatal(err)
	}
	if result.Paused || result.View != "left" || result.TaskID == "" || f.gateway.submits != submits+1 {
		t.Fatalf("continuation = %+v submits %d->%d, want the left view submitted", result, submits, f.gateway.submits)
	}
	left := f.step(t, "left")
	if got, err := f.repo.GetExecutionGrant(ctx, "owner-a", left.GrantRequestID); err != nil ||
		got.Status == executiongrant.Pending || !got.ApprovedByPolicy() {
		t.Fatalf("left grant = %+v err=%v, want approved by the policy", got, err)
	}
	if n, _ := f.runtime.Grants.SessionPaidSteps(ctx, "owner-a", "session-a"); n != 101 {
		t.Fatalf("approved after the left view = %d, want 101 (the view still counts)", n)
	}
}

// refusingGateway refuses every submission for good before writing a Task.
type refusingGateway struct{ *continuationGateway }

func (g refusingGateway) SubmitPrepared(context.Context, generation.SubmitPreparedCommand) (generation.Accepted, error) {
	return generation.Accepted{}, generation.ErrNotSubmitted
}

// #665: a view whose approved, plan-reserved grant is refused for good before
// any Task was written (a stale frozen input, #516) gives the reservation back,
// as the generation tools do; it does not stay reserved until a new plan.
func TestThreeViewPermanentSubmitRefusalReleasesThePlanReservation(t *testing.T) {
	ctx := context.Background()
	f := newContinuationFixture(t, true)
	if _, _, err := f.runtime.PrepareStep(ctx, "owner-a", f.run.ID, "front"); err != nil {
		t.Fatal(err)
	}
	_, grant, err := f.runtime.RequestAgentGrantWithBudget(ctx, "owner-a", f.run.ID, "front", "turn-a")
	if err != nil || !grant.ApprovedByBudget() {
		t.Fatalf("front grant = %+v err=%v", grant, err)
	}
	reserved := func() int {
		var n int
		if err := f.repo.db.QueryRow(`SELECT count(*) FROM budget_reservations WHERE grant_request_id=? AND state='reserved'`,
			grant.RequestID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if reserved() != 1 {
		t.Fatalf("front grant holds no reservation")
	}
	f.runtime.Generation = refusingGateway{f.gateway}
	if _, _, err := f.runtime.SubmitStep(ctx, "owner-a", f.run.ID, "front"); err == nil {
		t.Fatal("submit was not refused")
	}
	if n := reserved(); n != 0 {
		t.Fatalf("reservation still held after a permanent refusal: %d", n)
	}
}

// #665: a four-view request started in the auto mode whose session switches to
// another mode mid-run no longer makes the right view by itself. The group must
// not then report itself complete: it pauses at the right view so the Agent
// tells the person it waits for them on the card.
func TestFourViewRequestPausesAtTheRightViewAfterLeavingTheAutoMode(t *testing.T) {
	ctx := context.Background()
	f := newContinuationFixtureWithMode(t, false, "auto")
	modes := &switchModes{mode: "auto"}
	f.runtime.Grants.Modes = modes
	task := f.startFrontByPolicy(t)
	for i, want := range []string{"left", "back"} {
		f.finish(t, task, "asset-switch-"+want)
		result, err := f.views.ContinueWith(ctx, "owner-a", "session-a", f.run.ID, task, "turn-a", true)
		if err != nil || result.TaskID == "" || result.View != want {
			t.Fatalf("continue %d = %+v err=%v, want %s submitted", i, result, err, want)
		}
		task = result.TaskID
	}
	modes.mode = "budget"
	f.finish(t, task, "asset-switch-done")
	submits := f.gateway.submits
	result, err := f.views.ContinueWith(ctx, "owner-a", "session-a", f.run.ID, task, "turn-a", true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Paused || result.View != "right" || result.Reason != threeview.ReasonBudgetNotCovered ||
		result.TaskID != "" || f.gateway.submits != submits {
		t.Fatalf("result = %+v, want paused at the right view without a submit", result)
	}
}
