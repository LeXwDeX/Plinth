package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

// #665: a view whose submit the kernel refused for good is not left approved
// forever. A grant the auto mode approved holds no plan reservation, yet it is
// denied all the same, so the three-view duplicate guard stops counting the
// group and the person can start the view again.
func TestThreeViewPermanentSubmitRefusalDeniesAnyApprovedGrant665(t *testing.T) {
	ctx := context.Background()
	f := newContinuationFixtureWithMode(t, false, "auto")
	if _, _, err := f.runtime.PrepareStep(ctx, "owner-a", f.run.ID, "front"); err != nil {
		t.Fatal(err)
	}
	_, grant, err := f.runtime.RequestAgentGrantWithBudget(ctx, "owner-a", f.run.ID, "front", "turn-a")
	if err != nil || grant.Status != executiongrant.Approved || grant.ApprovedByBudget() {
		t.Fatalf("auto front grant = %+v err=%v", grant, err)
	}
	f.gateway.refuse = generation.ErrNotSubmitted
	if _, _, err := f.runtime.SubmitStep(ctx, "owner-a", f.run.ID, "front"); err == nil {
		t.Fatal("the refused submit reported success")
	}
	after, err := f.repo.GetExecutionGrant(ctx, "owner-a", grant.RequestID)
	if err != nil || after.Status != executiongrant.Denied || after.DecidedBy != executiongrant.AbandonedActor {
		t.Fatalf("grant after a permanent refusal = %+v err=%v, want denied by %s", after, err, executiongrant.AbandonedActor)
	}
	step := f.step(t, "front")
	if step.Phase != skillrun.GrantPending {
		t.Fatalf("front phase = %s", step.Phase)
	}
	if _, err := f.runtime.RestartStep(ctx, "owner-a", f.run.ID, "front", step.Attempt); err != nil {
		t.Fatalf("restart after a permanent refusal: %v", err)
	}
	// An uncertain failure (a lost response) keeps the approval for the replay.
	f2 := newContinuationFixtureWithMode(t, false, "auto")
	if _, _, err := f2.runtime.PrepareStep(ctx, "owner-a", f2.run.ID, "front"); err != nil {
		t.Fatal(err)
	}
	_, kept, err := f2.runtime.RequestAgentGrantWithBudget(ctx, "owner-a", f2.run.ID, "front", "turn-a")
	if err != nil || kept.Status != executiongrant.Approved {
		t.Fatalf("second front grant = %+v err=%v", kept, err)
	}
	f2.gateway.refuse = errors.New("response lost")
	_, _, _ = f2.runtime.SubmitStep(ctx, "owner-a", f2.run.ID, "front")
	if still, err := f2.repo.GetExecutionGrant(ctx, "owner-a", kept.RequestID); err != nil || still.Status != executiongrant.Approved {
		t.Fatalf("an uncertain refusal denied the grant: %+v err=%v", still, err)
	}
}
