package modeldelivery

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

// #330 B (dev-test note 99526): Meshy Convert finished and charged, then the
// platform refused its files, so the completion plan failed. The format list
// says what was charged and offers the existing free upstream refetch (#295)
// before a paid retry; once a refetch delivered, its files fill the formats.

type refetchFixture struct {
	*fixture
	eligibility map[generation.TaskID]generation.RefetchEligibility
	reads       int
}

func (f *refetchFixture) RefetchEligibility(_ context.Context, owner generation.OwnerID, id generation.TaskID) (generation.RefetchEligibility, error) {
	f.reads++
	task, exists := f.tasks[id]
	if !exists || task.OwnerID != owner {
		return generation.RefetchEligibility{}, &generation.Error{Code: generation.ErrNotFound}
	}
	if view, ok := f.eligibility[id]; ok {
		return view, nil
	}
	return generation.RefetchEligibility{SourceTaskID: id, Reason: generation.RefetchNotApplicable}, nil
}

// failedConvert330 completes the Meshy GLB-only task and fails the Convert
// plan with a failed step Task, as on dev-test.
func failedConvert330(t *testing.T) *refetchFixture {
	t.Helper()
	f := &refetchFixture{fixture: newPaidCompletionFixture(t), eligibility: map[generation.TaskID]generation.RefetchEligibility{}}
	service, err := New(f.catalog.Application().ModelDelivery, f, f.plans, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.service = service
	f.meshyGlbOnly("meshy-task", "owner")
	if _, err := f.service.Complete(context.Background(), "owner", "meshy-task"); err != nil {
		t.Fatal(err)
	}
	failed := f.addTask("convert-failed", "owner", "meshy", generation.FormatConversion)
	failed.State, failed.Assets = generation.TaskFailed, nil
	failed.Failure = &generation.Failure{Class: generation.FailureIntegrity, Retryable: true}
	f.tasks[failed.ID] = failed
	for id, plan := range f.store.plans {
		plan.State, plan.Failure = processing.Failed, "plan_task_failed"
		plan.Steps[0].State, plan.Steps[0].TaskID = processing.Failed, failed.ID
		f.store.plans[id] = plan
	}
	return f
}

func convertStep330(t *testing.T, coverage Coverage) Step {
	t.Helper()
	for _, step := range coverage.Steps {
		if step.OfferingID == meshyConvert {
			return step
		}
	}
	t.Fatalf("no Convert step in %#v", coverage.Steps)
	return Step{}
}

func TestFailedChargedConvertOffersTheFreeRefetchFirst330(t *testing.T) {
	f := failedConvert330(t)
	f.eligibility["convert-failed"] = generation.RefetchEligibility{SourceTaskID: "convert-failed", Eligible: true, ChargedCredits: 1}
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	step := convertStep330(t, coverage)
	if step.State != processing.Failed || step.ChargedCredits != 1 || step.Refetch == nil || !step.Refetch.Eligible || step.Refetch.TaskID != "" {
		t.Fatalf("step = %#v refetch = %#v", step, step.Refetch)
	}
	if fbx := formatByID(t, coverage, "fbx"); fbx.Status != StatusObtainable {
		t.Fatalf("fbx = %#v", fbx)
	}
	// After a refetch failed and the source closed, only the paid retry stays,
	// still saying what was charged.
	f.eligibility["convert-failed"] = generation.RefetchEligibility{SourceTaskID: "convert-failed", Reason: generation.RefetchRepeatedFailure, ChargedCredits: 1, FailedRefetches: 1}
	coverage, _ = f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	step = convertStep330(t, coverage)
	if step.ChargedCredits != 1 || step.Refetch == nil || step.Refetch.Eligible || step.Refetch.Reason != string(generation.RefetchRepeatedFailure) || step.Refetch.Failed != 1 {
		t.Fatalf("step = %#v refetch = %#v", step, step.Refetch)
	}
}

func TestUnchargedFailureHasNoRefetchOrChargeNote330(t *testing.T) {
	f := failedConvert330(t)
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	if step := convertStep330(t, coverage); step.ChargedCredits != 0 || step.Refetch != nil {
		t.Fatalf("a failure before the provider finished must not claim a charge: %#v", step)
	}
}

func TestRunningRefetchKeepsTheFormatsConverting330(t *testing.T) {
	f := failedConvert330(t)
	refetch := f.addTask("convert-refetch", "owner", "meshy", generation.FormatConversion)
	refetch.State = generation.TaskRunning
	f.tasks[refetch.ID] = refetch
	f.eligibility["convert-failed"] = generation.RefetchEligibility{SourceTaskID: "convert-failed", Eligible: true, ChargedCredits: 1, RefetchTaskID: refetch.ID, RefetchState: generation.TaskRunning}
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"fbx", "obj", "usdz", "stl", "3mf"} {
		if row := formatByID(t, coverage, id); row.Status != StatusConverting {
			t.Fatalf("%s = %#v", id, row)
		}
	}
	if coverage.CanComplete {
		t.Fatal("completion offered while the refetch runs")
	}
	if _, err := f.service.Complete(context.Background(), "owner", "meshy-task"); !errors.Is(err, ErrNothingToComplete) {
		t.Fatalf("complete = %v", err)
	}
}

func TestSucceededRefetchDeliversTheFormats330(t *testing.T) {
	f := failedConvert330(t)
	var specs []assetSpec
	for _, variant := range []string{"glb", "fbx", "obj", "usdz", "stl", "3mf"} {
		specs = append(specs, assetSpec{role: "model", variant: variant})
	}
	refetch := f.addTask("convert-refetch", "owner", "meshy", generation.FormatConversion, specs...)
	refetch.Offering.ID = meshyConvert
	f.tasks[refetch.ID] = refetch
	f.eligibility["convert-failed"] = generation.RefetchEligibility{SourceTaskID: "convert-failed", Eligible: true, ChargedCredits: 1, RefetchTaskID: refetch.ID, RefetchState: generation.TaskSucceeded}
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"fbx", "obj", "usdz", "stl", "3mf"} {
		if row := formatByID(t, coverage, id); row.Status != StatusAvailable || row.TaskID != refetch.ID {
			t.Fatalf("%s = %#v", id, row)
		}
	}
	if glb := formatByID(t, coverage, "glb"); glb.TaskID != "meshy-task" {
		t.Fatalf("the source's own GLB must stay: %#v", glb)
	}
	// Another owner's refetch Task is never used.
	other := refetch
	other.OwnerID = "intruder"
	f.tasks[refetch.ID] = other
	coverage, _ = f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if fbx := formatByID(t, coverage, "fbx"); fbx.Status == StatusAvailable {
		t.Fatalf("foreign refetch delivered: %#v", fbx)
	}
}
