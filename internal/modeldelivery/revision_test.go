package modeldelivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

// The completion plan key contains the Offering revision. These tests change
// the revision while a plan is still going and prove that the same format is
// then "converting", never obtainable again, and that no second paid plan is
// created (the HTTP layer maps ErrNothingToComplete to 409).

const (
	// meshyConvert is the Meshy completion route since #330; remeshOffering
	// ran it before, with convert_format_only, and is kept only so plans
	// made then are still recognized (a retired conversion).
	meshyConvert   = generation.OfferingID("meshy/processing/convert")
	remeshOffering = generation.OfferingID("meshy/processing/remesh")
	tripoConvert   = generation.OfferingID("tripo/tripo-v3.1/format-conversion")
)

func setPlanState(f *fixture, state string) {
	for id, plan := range f.store.plans {
		plan.State = state
		for index := range plan.Steps {
			plan.Steps[index].State = state
		}
		f.store.plans[id] = plan
	}
}

func TestRevisionChangeWhileAPlanIsActiveKeepsTheFormatConverting(t *testing.T) {
	for _, state := range []string{processing.Pending, processing.Running, processing.Paused} {
		t.Run(state, func(t *testing.T) {
			f := newPaidCompletionFixture(t)
			f.meshyGlbOnly("meshy-task", "owner")
			if _, err := f.service.Complete(context.Background(), "owner", "meshy-task"); err != nil {
				t.Fatal(err)
			}
			setPlanState(f, state)

			f.revisions[meshyConvert] = "revision-after-publish"

			coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
			if err != nil {
				t.Fatal(err)
			}
			usdz := formatByID(t, coverage, "usdz")
			if usdz.Status != StatusConverting || usdz.Reason != "converting" || usdz.Conversion == nil ||
				usdz.Conversion.OfferingID != meshyConvert {
				t.Fatalf("usdz after the revision changed = %#v", usdz)
			}
			if coverage.CanComplete || coverage.CompletionCredits != 0 || len(coverage.Steps) != 1 ||
				coverage.Steps[0].State != state || coverage.Steps[0].PlanID == "" {
				t.Fatalf("the active plan must still be the step: %#v credits=%d can=%v",
					coverage.Steps, coverage.CompletionCredits, coverage.CanComplete)
			}
			// A second click must not start a second paid plan.
			if _, err := f.service.Complete(context.Background(), "owner", "meshy-task"); !errors.Is(err, ErrNothingToComplete) {
				t.Fatalf("second complete after a revision change = %v", err)
			}
			if len(f.store.plans) != 1 {
				t.Fatalf("a second plan was created: %d plans", len(f.store.plans))
			}
		})
	}
}

func TestRevisionChangeLocksEveryPerFormatPlanAndOnlyTheirFormats(t *testing.T) {
	f := newConversionFixture(t)
	f.addTask("old-tripo", "owner", "tripo", "text-to-3d",
		assetSpec{role: "model", variant: "", mediaType: "model/gltf-binary"},
		assetSpec{role: "converted_model", variant: "converted.gltf", mediaType: "model/gltf-binary"})
	if _, err := f.service.Complete(context.Background(), "owner", "old-tripo"); err != nil {
		t.Fatal(err)
	}
	if len(f.store.plans) != 6 {
		t.Fatalf("plans = %d", len(f.store.plans))
	}
	// Both Offerings are republished while all six plans are still running.
	f.revisions[tripoConvert] = "tripo-revision-2"
	f.revisions[meshyConvert] = "meshy-revision-2"
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "old-tripo")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"gltf", "fbx", "obj", "stl", "usdz", "3mf"} {
		if row := formatByID(t, coverage, id); row.Status != StatusConverting {
			t.Fatalf("%s = %#v", id, row)
		}
	}
	if coverage.CanComplete {
		t.Fatalf("nothing may be offered while the plans run: %#v", coverage.Steps)
	}
	if _, err := f.service.Complete(context.Background(), "owner", "old-tripo"); !errors.Is(err, ErrNothingToComplete) {
		t.Fatalf("complete = %v", err)
	}
	if len(f.store.plans) != 6 {
		t.Fatalf("a duplicate plan was created: %d", len(f.store.plans))
	}
}

func TestRevisionChangeDoesNotBlockAnotherFormatOrAnotherTask(t *testing.T) {
	f := newConversionFixture(t)
	f.addTask("tripo-a", "owner", "tripo", "text-to-3d",
		assetSpec{role: "model", variant: "", mediaType: "model/gltf-binary"},
		assetSpec{role: "converted_model", variant: "converted.gltf", mediaType: "model/gltf-binary"})
	f.addTask("tripo-b", "owner", "tripo", "text-to-3d",
		assetSpec{role: "model", variant: "", mediaType: "model/gltf-binary"},
		assetSpec{role: "converted_model", variant: "converted.gltf", mediaType: "model/gltf-binary"})
	// Task A converts only usdz so far: start the six plans, then drop all
	// but the usdz plan to model "one format is running, the rest are not".
	if _, err := f.service.Complete(context.Background(), "owner", "tripo-a"); err != nil {
		t.Fatal(err)
	}
	for id, plan := range f.store.plans {
		var input map[string]any
		_ = json.Unmarshal(plan.Steps[0].Input, &input)
		if input["format"] != "USDZ" {
			delete(f.store.plans, id)
		}
	}
	f.revisions[tripoConvert] = "tripo-revision-2"
	coverage, _ := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "tripo-a")
	if usdz := formatByID(t, coverage, "usdz"); usdz.Status != StatusConverting {
		t.Fatalf("usdz = %#v", usdz)
	}
	if fbx := formatByID(t, coverage, "fbx"); fbx.Status != StatusObtainable {
		t.Fatalf("a format nobody is converting stays obtainable: %#v", fbx)
	}
	before := len(f.store.plans)
	if _, err := f.service.Complete(context.Background(), "owner", "tripo-a"); err != nil {
		t.Fatal(err)
	}
	created := 0
	for _, plan := range f.store.plans {
		var input map[string]any
		_ = json.Unmarshal(plan.Steps[0].Input, &input)
		if input["format"] == "USDZ" {
			created++
		}
	}
	if created != 1 || len(f.store.plans) != before+5 {
		t.Fatalf("usdz plans=%d total=%d (was %d): only the unconverted formats may start", created, len(f.store.plans), before)
	}
	// The other task is unaffected by A's plan.
	other, _ := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "tripo-b")
	if usdz := formatByID(t, other, "usdz"); usdz.Status != StatusObtainable || !other.CanComplete {
		t.Fatalf("another task was blocked: %#v", usdz)
	}
}

func TestRevisionChangeAfterAFinishedPlanStillDeliversTheFileAndNeverReconverts(t *testing.T) {
	f := newPaidCompletionFixture(t)
	f.meshyGlbOnly("meshy-task", "owner")
	if _, err := f.service.Complete(context.Background(), "owner", "meshy-task"); err != nil {
		t.Fatal(err)
	}
	var specs []assetSpec
	for _, variant := range []string{"glb", "fbx", "obj", "usdz", "stl", "3mf"} {
		specs = append(specs, assetSpec{role: "model", variant: variant})
	}
	result := f.addTask("convert-task", "owner", "meshy", "format-conversion", specs...)
	for id, plan := range f.store.plans {
		plan.State, plan.Steps[0].State, plan.Steps[0].TaskID = processing.Succeeded, processing.Succeeded, result.ID
		f.store.plans[id] = plan
	}
	f.revisions[meshyConvert] = "revision-after-publish"
	coverage, _ := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if usdz := formatByID(t, coverage, "usdz"); usdz.Status != StatusAvailable || usdz.TaskID != "convert-task" {
		t.Fatalf("a file paid for under the old revision is lost: %#v", usdz)
	}
	if _, err := f.service.Complete(context.Background(), "owner", "meshy-task"); !errors.Is(err, ErrNothingToComplete) {
		t.Fatalf("complete = %v", err)
	}
}

// The plans here are the pre-#330 kind (Remesh with convert_format_only): the
// retired conversion still recognizes them, so an active one still locks.
func TestOnlyActiveCompletionPlansOfTheSameTaskAndOwnerLockAFormat(t *testing.T) {
	f := newPaidCompletionFixture(t)
	f.meshyGlbOnly("meshy-task", "owner")
	f.meshyGlbOnly("other-task", "owner")
	f.meshyGlbOnly("foreign-task", "intruder")
	ctx := context.Background()
	convertOnly := json.RawMessage(`{"convert_format_only":true}`)
	plan := func(id, owner, session, task string, asset string, state string, input json.RawMessage) processing.Plan {
		return processing.Plan{ID: id, OwnerID: generation.OwnerID(owner), SessionID: session,
			SourceTaskID: generation.TaskID(task), SourceAssetID: generation.AssetID(asset), State: state,
			Steps: []processing.Step{{OfferingID: remeshOffering, Input: input, Revision: "old", State: state}}}
	}
	for _, p := range []processing.Plan{
		// A plan for another task of the same owner and session.
		plan("p-other", "owner", "session-owner", "other-task", "other-task-a0", processing.Running, convertOnly),
		// A plan of someone else for a task with the same session name.
		plan("p-foreign", "intruder", "session-owner", "meshy-task", "meshy-task-a0", processing.Running, convertOnly),
		// The user's own remesh (not a format conversion) of the same task.
		plan("p-remesh", "owner", "session-owner", "meshy-task", "meshy-task-a0", processing.Running, json.RawMessage(`{"target_polycount":3000}`)),
		// Finished without a result, and failed: neither is active.
		plan("p-failed", "owner", "session-owner", "meshy-task", "meshy-task-a0", processing.Failed, convertOnly),
	} {
		if err := f.store.CreateProcessingPlan(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	coverage, _ := f.service.Coverage(ctx, generation.OwnTasks("owner"), "meshy-task")
	if usdz := formatByID(t, coverage, "usdz"); usdz.Status != StatusObtainable || !coverage.CanComplete {
		t.Fatalf("usdz = %#v can=%v: unrelated plans must not lock the format", usdz, coverage.CanComplete)
	}
	// An active conversion plan of this very task does.
	if err := f.store.CreateProcessingPlan(ctx, plan("p-own", "owner", "session-owner", "meshy-task", "meshy-task-a0", processing.Pending, convertOnly)); err != nil {
		t.Fatal(err)
	}
	coverage, _ = f.service.Coverage(ctx, generation.OwnTasks("owner"), "meshy-task")
	if usdz := formatByID(t, coverage, "usdz"); usdz.Status != StatusConverting || coverage.CanComplete {
		t.Fatalf("usdz = %#v can=%v", usdz, coverage.CanComplete)
	}
	if _, err := f.service.Complete(ctx, "owner", "meshy-task"); !errors.Is(err, ErrNothingToComplete) {
		t.Fatalf("complete = %v", err)
	}
}

// A busy session can hold more plans than the session listing returns. The
// active plan of this task must be found however many newer plans for other
// tasks exist, or a revision change would let the same format be paid twice.
func TestOldActivePlanBehindManyNewerPlansStillLocksTheFormat(t *testing.T) {
	f := newPaidCompletionFixture(t)
	f.meshyGlbOnly("meshy-task", "owner")
	if _, err := f.service.Complete(context.Background(), "owner", "meshy-task"); err != nil {
		t.Fatal(err)
	}
	setPlanState(f, processing.Paused)
	var original processing.Plan
	for _, plan := range f.store.plans {
		original = plan
	}
	for index := 0; index < 150; index++ {
		filler := original
		filler.ID = fmt.Sprintf("filler-%03d", index)
		filler.SourceTaskID = generation.TaskID(fmt.Sprintf("other-task-%03d", index))
		filler.State = processing.Succeeded
		filler.CreatedAt = original.CreatedAt.Add(time.Duration(index+1) * time.Minute)
		f.store.plans[filler.ID] = filler
	}
	f.revisions[meshyConvert] = "revision-after-publish"

	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	if usdz := formatByID(t, coverage, "usdz"); usdz.Status != StatusConverting {
		t.Fatalf("usdz with the active plan outside the session page = %#v", usdz)
	}
	if coverage.CanComplete {
		t.Fatalf("nothing may be offered while the old plan is paused: %#v", coverage.Steps)
	}
	if _, err := f.service.Complete(context.Background(), "owner", "meshy-task"); !errors.Is(err, ErrNothingToComplete) {
		t.Fatalf("complete with the old plan outside the session page = %v", err)
	}
	if len(f.store.plans) != 151 {
		t.Fatalf("a second paid plan was created: %d plans", len(f.store.plans))
	}
}
