package modeldelivery

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// coverage_progress_462_test.go locks the progress the formats panel shows for a
// task that is converting its own formats after success (#462): the projection
// counts the on-demand slots the frozen Offering declares — the kernel leaves
// exactly the requested ones in the resource states — and how many of them
// already landed as a file. A task that converts nothing of its own reports none,
// so the panel says nothing rather than "0/0".

func (f *fixture) mixedProgressTask(id, owner string, states map[string]generation.ResourceState) generation.Task {
	task := f.tripoGlbOnly(id, owner)
	task.Offering.PostSuccess = &generation.PostSuccessPolicy{Observe: true, IntervalSeconds: 5, DeadlineSeconds: 7200}
	task.Offering.Outputs = []generation.AssetRequirement{
		{Role: "model"},
		{Role: "converted_model", Variant: "converted.gltf", Delivery: generation.AssetDeliveryRequired, Minimum: intPtr(1)},
		{Role: "converted_model", Variant: "converted.fbx", Delivery: generation.AssetDeliveryOnDemand, Minimum: intPtr(1),
			RequestedWhen: &generation.ParameterCondition{Parameter: "output_formats", Operator: "contains", Value: []byte(`"FBX"`)}},
		{Role: "converted_model", Variant: "converted.3mf", Delivery: generation.AssetDeliveryOnDemand, Minimum: intPtr(1),
			RequestedWhen: &generation.ParameterCondition{Parameter: "output_formats", Operator: "contains", Value: []byte(`"3MF"`)}},
	}
	task.Resources = states
	f.tasks[task.ID] = task
	return task
}

func intPtr(value int) *int { return &value }

func TestCoverageReportsTheTasksOwnPostSuccessFormatProgress(t *testing.T) {
	f := newConversionFixture(t)
	f.mixedProgressTask("tripo-mixed", "owner", map[string]generation.ResourceState{
		"converted_model|converted.gltf|": {State: generation.ResourceReady},
		"converted_model|converted.fbx|":  {State: generation.ResourcePending},
		"converted_model|converted.3mf|":  {State: generation.ResourcePending},
	})
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "tripo-mixed")
	if err != nil {
		t.Fatal(err)
	}
	progress := coverage.PostSuccessProgress
	if progress == nil || progress.Total != 2 || progress.Done != 0 {
		t.Fatalf("progress = %+v, want 0/2", progress)
	}
	// The fbx row reads as converting: the platform is already converting it.
	if row := formatByID(t, coverage, "fbx"); row.Status != StatusConverting {
		t.Fatalf("fbx row = %+v, want converting", row)
	}

	// One format landed: the progress advances. The slot is READY only on local
	// asset evidence, so the task also has the file.
	landed := f.mixedProgressTask("tripo-mixed-2", "owner", map[string]generation.ResourceState{
		"converted_model|converted.gltf|": {State: generation.ResourceReady},
		"converted_model|converted.fbx|":  {State: generation.ResourcePending},
		"converted_model|converted.3mf|":  {State: generation.ResourcePending},
	})
	landed.Resources["converted_model|converted.fbx|"] = generation.ResourceState{State: generation.ResourceReady}
	landed.Assets = append(landed.Assets, generation.Asset{
		ID: "tripo-mixed-2-fbx", TaskID: landed.ID, Role: "converted_model", Variant: "converted.fbx",
		Ordinal: 1, StorageKey: "key-fbx", MediaType: "application/octet-stream", Size: 1,
	})
	f.tasks[landed.ID] = landed
	coverage, err = f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "tripo-mixed-2")
	if err != nil {
		t.Fatal(err)
	}
	if progress = coverage.PostSuccessProgress; progress == nil || progress.Total != 2 || progress.Done != 1 {
		t.Fatalf("progress = %+v, want 1/2", progress)
	}
}

// A historical per-item slot (variant_from) is not a task converting its own
// formats: it reports no progress either.
func TestCoverageHasNoPostSuccessProgressForAHistoricalPerItemSlot(t *testing.T) {
	f := newConversionFixture(t)
	task := f.tripoGlbOnly("tripo-legacy-item", "owner")
	task.Offering.PostSuccess = &generation.PostSuccessPolicy{Observe: true, IntervalSeconds: 60, DeadlineSeconds: 86400}
	task.Offering.Outputs = []generation.AssetRequirement{
		{Role: "model"},
		{Role: "converted_model", Variant: "converted.fbx", Delivery: generation.AssetDeliveryOnDemand, Minimum: intPtr(1),
			VariantPrefix: "converted.", VariantFrom: "output_formats"},
	}
	task.Resources = map[string]generation.ResourceState{
		"converted_model|converted.fbx|": {State: generation.ResourcePending},
	}
	f.tasks[task.ID] = task
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "tripo-legacy-item")
	if err != nil {
		t.Fatal(err)
	}
	if coverage.PostSuccessProgress != nil {
		t.Fatalf("progress = %+v, want none", coverage.PostSuccessProgress)
	}
}

// A task that converts no format of its own has no progress: the panel shows
// nothing rather than an empty count.
func TestCoverageHasNoPostSuccessProgressWithoutRequestedFormats(t *testing.T) {
	f := newConversionFixture(t)
	f.tripoGlbOnly("tripo-plain", "owner")
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "tripo-plain")
	if err != nil {
		t.Fatal(err)
	}
	if coverage.PostSuccessProgress != nil {
		t.Fatalf("progress = %+v, want none", coverage.PostSuccessProgress)
	}
}
