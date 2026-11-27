package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agenttools"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
	"github.com/LeXwDeX/Plinth/internal/threeview"
)

// startByPerson starts one view the way the card does while the series runs.
func (f *continuationFixture) startByPerson(t *testing.T, key string) string {
	t.Helper()
	ctx := context.Background()
	if _, _, err := f.runtime.PrepareStep(ctx, "owner-a", f.run.ID, key); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.runtime.RequestAgentGrantWithBudget(ctx, "owner-a", f.run.ID, key, "turn-a"); err != nil {
		t.Fatal(err)
	}
	_, accepted, err := f.runtime.SubmitStep(ctx, "owner-a", f.run.ID, key)
	if err != nil || accepted.TaskID == "" {
		t.Fatalf("person start %s: %+v err=%v", key, accepted, err)
	}
	return string(accepted.TaskID)
}

// #689, #693: the person starts the back view on the card while the series
// still makes the left view. The continuation does not call it a failure: it
// waits for the running view, submits nothing for it, and then finishes the
// group.
func TestContinuationWaitsForAViewThePersonStarted(t *testing.T) {
	f := newContinuationFixtureWithMode(t, false, "auto")
	ctx := context.Background()
	frontTask := f.startFrontByPolicy(t)
	f.finish(t, frontTask, "asset-front")
	result, err := f.views.ContinueWith(ctx, "owner-a", "session-a", f.run.ID, frontTask, "turn-a", false)
	if err != nil || result.View != "left" || result.TaskID == "" {
		t.Fatalf("left submit: %+v err=%v", result, err)
	}
	leftTask := result.TaskID
	backTask := f.startByPerson(t, "back")
	f.finish(t, leftTask, "asset-left")
	submits := f.gateway.submits
	result, err = f.views.ContinueWith(ctx, "owner-a", "session-a", f.run.ID, leftTask, "turn-a", false)
	if err != nil || result.Paused || !result.Awaiting || result.TaskID != backTask || result.View != "back" {
		t.Fatalf("running back view: %+v err=%v", result, err)
	}
	if f.gateway.submits != submits {
		t.Fatalf("the running view was submitted again: %d -> %d", submits, f.gateway.submits)
	}
	f.finish(t, backTask, "asset-back")
	result, err = f.views.ContinueWith(ctx, "owner-a", "session-a", f.run.ID, backTask, "turn-a", false)
	if err != nil || result.Paused || len(result.Completed) != 3 {
		t.Fatalf("after the person's view: %+v err=%v", result, err)
	}
}

// #693: a bounded continuation (auto mode) keeps redrawing an inconsistent
// view past consistency.max_attempts_per_view and stops only at the turn's
// generation checkpoint, before it prepares or submits anything.
func TestBoundedContinuationRedrawsUntilTheCheckpoint(t *testing.T) {
	f := newContinuationFixtureWithMode(t, false, "auto")
	ctx := context.Background()
	task := f.startFrontByPolicy(t)
	f.finish(t, task, "asset-front")
	result, err := f.views.ContinueWithin(ctx, "owner-a", "session-a", f.run.ID, task, "turn-a", false, 10)
	if err != nil || result.View != "left" || result.TaskID == "" {
		t.Fatalf("left: %+v err=%v", result, err)
	}
	f.finish(t, result.TaskID, "asset-left")
	result, err = f.views.ContinueWithin(ctx, "owner-a", "session-a", f.run.ID, result.TaskID, "turn-a", false, 10)
	if err != nil || result.View != "back" || result.TaskID == "" {
		t.Fatalf("back: %+v err=%v", result, err)
	}
	// Three inconsistent back views in a row: the unbounded continuation
	// pauses after the second (TestRejectedViewIsRedrawnAloneWithinTheAttemptLimit).
	for attempt := 1; attempt <= 3; attempt++ {
		asset := "asset-back-" + string(rune('0'+attempt))
		f.finish(t, result.TaskID, asset)
		f.assets.bad[generation.AssetID(asset)] = continuationPicture(t, 64, 64, 28, 20, 8)
		result, err = f.views.ContinueWithin(ctx, "owner-a", "session-a", f.run.ID, result.TaskID, "turn-a", false, 10)
		if err != nil || result.Paused || !result.Retried || result.Attempt != attempt || result.TaskID == "" ||
			result.MaximumAttempts != 0 || result.FailedReasons == "" {
			t.Fatalf("redraw %d: %+v err=%v", attempt, result, err)
		}
	}
	f.finish(t, result.TaskID, "asset-back-9")
	f.assets.bad["asset-back-9"] = continuationPicture(t, 64, 64, 28, 20, 8)
	prepares, submits := f.gateway.prepares, f.gateway.submits
	result, err = f.views.ContinueWithin(ctx, "owner-a", "session-a", f.run.ID, result.TaskID, "turn-a", false, 0)
	if err != nil || !result.Paused || result.Reason != threeview.ReasonGenerationCheckpoint || result.TaskID != "" {
		t.Fatalf("at the checkpoint: %+v err=%v", result, err)
	}
	if f.gateway.prepares != prepares || f.gateway.submits != submits {
		t.Fatalf("the checkpoint prepared or submitted: prepares %d->%d submits %d->%d",
			prepares, f.gateway.prepares, submits, f.gateway.submits)
	}
	if back := f.step(t, "back"); back.Review != skillrun.Rejected || back.Attempt != 3 {
		t.Fatalf("back at the checkpoint = %+v", back)
	}
}

// #693, #694: the continuation counts every view it submits on the call that
// started the group, and the turn stops at the auto mode's checkpoint instead
// of submitting past it, also after a restart lost the in-process count.
func TestThreeViewContinuationCountsTowardTheCheckpoint(t *testing.T) {
	f := newContinuationFixtureWithMode(t, false, "auto")
	f.toolConfig = func(config *agenttools.Config) {
		config.MaxGenerationCallsPerTurn = 20
		config.GenerationCheckpoint = map[string]int{"auto": 2}
		config.ConsentLimitContent = "checkpoint reached"
	}
	executor, command := f.toolFor(t, `{"subject_type":"humanoid"}`)
	command.ApprovalMode = "auto"
	ctx := context.Background()
	observe := func(chat.ToolObservation) error { return nil }
	result, err := executor.Execute(ctx, command, observe)
	if err != nil || !result.AwaitTask || result.GenerationUnits != 1 {
		t.Fatalf("front: %+v err=%v", result, err)
	}
	command.BoundTaskID = result.TaskID
	f.finish(t, command.BoundTaskID, "asset-count-0")
	result, err = executor.Execute(ctx, command, observe)
	if err != nil || !result.AwaitTask || result.GenerationUnits != 2 {
		t.Fatalf("left: %+v err=%v", result, err)
	}
	command.BoundTaskID = result.TaskID
	f.finish(t, command.BoundTaskID, "asset-count-1")
	submits := f.gateway.submits
	result, err = executor.Execute(ctx, command, observe)
	if err != nil || result.AwaitTask || result.GenerationCheckpoint != 2 || !result.NoSubmission ||
		result.AgentContent != "checkpoint reached" || f.gateway.submits != submits {
		t.Fatalf("back past the checkpoint: %+v err=%v submits %d->%d", result, err, submits, f.gateway.submits)
	}
	// A restarted process rebuilds the call's own units from the checkpoint.
	restarted, _ := f.toolFor(t, `{"subject_type":"humanoid"}`)
	command.OwnGenerationUnits = 2
	result, err = restarted.Execute(ctx, command, observe)
	if err != nil || result.GenerationCheckpoint != 2 || f.gateway.submits != submits {
		t.Fatalf("after restart: %+v err=%v submits %d->%d", result, err, submits, f.gateway.submits)
	}
}

// #689: a view the person started on the card that still waits for its
// approval pauses the call with the started wording, not the failure one.
func TestAgentToolSaysAStartedViewIsNotAFailure(t *testing.T) {
	f := newContinuationFixtureWithMode(t, false, "auto")
	f.toolSchema = func(schema *catalog.ChatThreeViewSchema) {
		schema.Messages.AgentPausedStarted = "started-not-failed {run} {view} {reason}"
	}
	executor, command := f.toolFor(t, `{"subject_type":"humanoid"}`)
	ctx := context.Background()
	observe := func(chat.ToolObservation) error { return nil }
	result, err := executor.Execute(ctx, command, observe)
	if err != nil || !result.AwaitTask {
		t.Fatalf("front: %+v err=%v", result, err)
	}
	command.BoundTaskID = result.TaskID
	f.finish(t, command.BoundTaskID, "asset-started-0")
	result, err = executor.Execute(ctx, command, observe)
	if err != nil || !result.AwaitTask {
		t.Fatalf("left: %+v err=%v", result, err)
	}
	// The call made its own run; the person prepares its back view.
	runID := ""
	groups, err := f.views.List(ctx, "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if slotOf(group, "left").TaskID == result.TaskID {
			runID = group.RunID
		}
	}
	if _, _, err := f.runtime.PrepareStep(ctx, "owner-a", runID, "back"); err != nil {
		t.Fatal(err)
	}
	command.BoundTaskID = result.TaskID
	f.finish(t, command.BoundTaskID, "asset-started-1")
	result, err = executor.Execute(ctx, command, observe)
	if err != nil || result.AwaitTask || !strings.HasPrefix(result.AgentContent, "started-not-failed") ||
		!strings.Contains(result.AgentContent, "busy") {
		t.Fatalf("prepared back view: %+v err=%v", result, err)
	}
}

// #689: a view whose grant waits (a person started it on the card, or the
// continuation's own grant was left for approval) pauses with
// view_awaiting_approval, not with the started reason that named the card;
// a view only prepared keeps view_already_started.
func TestContinuationSaysAViewWaitsForItsApproval(t *testing.T) {
	f := newContinuationFixtureWithMode(t, false, "auto")
	ctx := context.Background()
	frontTask := f.startFrontByPolicy(t)
	f.finish(t, frontTask, "asset-front")
	result, err := f.views.ContinueWith(ctx, "owner-a", "session-a", f.run.ID, frontTask, "turn-a", false)
	if err != nil || result.View != "left" || result.TaskID == "" {
		t.Fatalf("left submit: %+v err=%v", result, err)
	}
	leftTask := result.TaskID
	if _, _, err := f.runtime.PrepareStep(ctx, "owner-a", f.run.ID, "back"); err != nil {
		t.Fatal(err)
	}
	f.finish(t, leftTask, "asset-left")
	result, err = f.views.ContinueWith(ctx, "owner-a", "session-a", f.run.ID, leftTask, "turn-a", false)
	if err != nil || !result.Paused || result.Reason != threeview.ReasonViewAlreadyStarted || result.View != "back" {
		t.Fatalf("prepared back view: %+v err=%v", result, err)
	}
	if _, _, err := f.runtime.RequestAgentGrantWithBudget(ctx, "owner-a", f.run.ID, "back", "turn-a"); err != nil {
		t.Fatal(err)
	}
	if back := f.step(t, "back"); back.Phase != skillrun.GrantPending {
		t.Fatalf("back = %+v, want a grant", back)
	}
	submits := f.gateway.submits
	result, err = f.views.ContinueWith(ctx, "owner-a", "session-a", f.run.ID, leftTask, "turn-a", false)
	if err != nil || !result.Paused || result.Reason != threeview.ReasonViewAwaitingApproval || result.View != "back" ||
		f.gateway.submits != submits {
		t.Fatalf("back waiting for its grant: %+v err=%v", result, err)
	}
}
