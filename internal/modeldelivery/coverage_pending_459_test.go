package modeldelivery

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #459: a finished 3D task can still owe formats as post-success resources
// (#457's mixed post_success shape). The formats projection must read those
// slots through the Schema's format matches: a pending slot reads as
// converting, a slot that ended without a file reads as a failed conversion,
// and neither may be planned or offered as a new paid run.

// pendingResourceTask is a succeeded Tripo task that saved only its main GLB
// and carries resource slots for the other formats, the shape the kernel
// writes after the main model landed.
func (f *fixture) pendingResourceTask(id, owner string) generation.Task {
	task := f.tripoGlbOnly(id, owner)
	task.Resources = map[string]generation.ResourceState{
		"converted_model|converted.fbx|":  {State: generation.ResourcePending},
		"converted_model|converted.obj|":  {State: generation.ResourcePending},
		"converted_model|converted.stl|":  {State: generation.ResourceFailed, Reason: "post_success.conversion_failed"},
		"converted_model|converted.usdz|": {State: generation.ResourceIncomplete, Reason: generation.ResourceReasonMessageKey},
		"converted_model|converted.3mf|":  {State: generation.ResourceAbandoned},
	}
	f.tasks[task.ID] = task
	return task
}

func TestCoverageOfPendingPostSuccessResourcesReadsConverting459(t *testing.T) {
	f := newConversionFixture(t)
	f.pendingResourceTask("tripo-pending", "owner")
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "tripo-pending")
	if err != nil {
		t.Fatal(err)
	}
	if !coverage.Applicable {
		t.Fatalf("coverage = %#v", coverage)
	}
	for _, id := range []string{"fbx", "obj"} {
		row := formatByID(t, coverage, id)
		if row.Status != StatusConverting || row.Reason != reasonConverting {
			t.Fatalf("%s = %#v", id, row)
		}
		if row.Conversion != nil {
			t.Fatalf("%s names a conversion the platform did not offer: %#v", id, row.Conversion)
		}
	}
	// A slot that ended without a file is missing again, and it reads as a
	// conversion that ran and failed rather than one that never ran.
	for _, id := range []string{"stl", "usdz", "3mf"} {
		row := formatByID(t, coverage, id)
		if row.Status != StatusObtainable || row.Reason != reasonConversionFailed {
			t.Fatalf("%s = %#v", id, row)
		}
		if row.Conversion == nil {
			t.Fatalf("%s offers no route to try again: %#v", id, row)
		}
	}
	// A ready slot needs no entry: the file it names is classified as an asset.
	glb := formatByID(t, coverage, "glb")
	if glb.Status != StatusAvailable || glb.AssetID == "" {
		t.Fatalf("glb = %#v", glb)
	}
}

func TestCompleteSkipsFormatsTheTaskStillOwes459(t *testing.T) {
	f := newConversionFixture(t)
	f.pendingResourceTask("tripo-pending", "owner")
	coverage, err := f.service.Complete(context.Background(), generation.OwnerID("owner"), "tripo-pending")
	if err != nil {
		t.Fatal(err)
	}
	planned := map[string]bool{}
	for _, step := range coverage.Steps {
		for _, format := range step.Formats {
			planned[format] = true
		}
	}
	for _, id := range []string{"fbx", "obj"} {
		if planned[id] {
			t.Fatalf("a plan was started for %s while the task still owes it: %#v", id, coverage.Steps)
		}
		if row := formatByID(t, coverage, id); row.Status != StatusConverting {
			t.Fatalf("%s = %#v", id, row)
		}
	}
	// The other missing formats are still completed as before.
	if !planned["gltf"] {
		t.Fatalf("steps = %#v", coverage.Steps)
	}
}

func TestCompleteAfterTheResourcesReachTerminalState459(t *testing.T) {
	f := newConversionFixture(t)
	// The settled slot has its file, the failed one has none: the first is
	// available and the second is a missing format to plan again.
	task := f.addTask("tripo-pending", "owner", "tripo", "text-to-3d",
		assetSpec{role: modelRole, variant: "", mediaType: "model/gltf-binary"},
		assetSpec{role: convertedRole, variant: "converted.fbx", mediaType: "application/octet-stream"},
	)
	task.Resources = map[string]generation.ResourceState{
		"converted_model|converted.fbx|": {State: generation.ResourceReady},
		"converted_model|converted.obj|": {State: generation.ResourceFailed, Reason: "post_success.conversion_failed"},
	}
	f.tasks[task.ID] = task
	coverage, err := f.service.Complete(context.Background(), generation.OwnerID("owner"), "tripo-pending")
	if err != nil {
		t.Fatal(err)
	}
	planned := map[string]bool{}
	for _, step := range coverage.Steps {
		for _, format := range step.Formats {
			planned[format] = true
		}
	}
	if !planned["obj"] {
		t.Fatalf("the failed slot's format was not planned: %#v", coverage.Steps)
	}
	if planned["fbx"] {
		t.Fatalf("the settled slot's format was planned again: %#v", coverage.Steps)
	}
	if fbx := formatByID(t, coverage, "fbx"); fbx.Status != StatusAvailable {
		t.Fatalf("the settled slot's format = %#v", fbx)
	}
}

func TestPendingResourceFormatsUsesTheSchemaMatches459(t *testing.T) {
	f := newConversionFixture(t)
	task := f.pendingResourceTask("tripo-pending", "owner")
	formats := PendingResourceFormats(f.catalog.Application().ModelDelivery, task)
	want := map[string]bool{"fbx": true, "obj": true}
	if len(formats) != len(want) {
		t.Fatalf("pending formats = %v", formats)
	}
	for _, format := range formats {
		if !want[format] {
			t.Fatalf("pending formats = %v", formats)
		}
	}
	// A slot of no format (an aggregate prefix slot) is not named: it stands
	// for several formats at once.
	task.Resources["converted_model|converted.|"] = generation.ResourceState{State: generation.ResourcePending}
	if got := PendingResourceFormats(f.catalog.Application().ModelDelivery, task); len(got) != len(formats) {
		t.Fatalf("aggregate slot added a format: %v", got)
	}
}

// #459: the pending mapping is conservative because a slot that has not
// delivered has no media type. Tripo's converted.gltf slot fills two target
// formats (a self-contained GLB and the glTF JSON), so both count as pending
// until the file lands and FormatOf can name the one it is. Naming only the
// first format in presentation order would leave glTF unguarded, and a person
// or the future reconciler would start a second paid run for it.
func TestPendingGltFSlotReadsAsBothContainers459(t *testing.T) {
	f := newConversionFixture(t)
	task := f.tripoGlbOnly("tripo-gltf-slot", "owner")
	task.Resources = map[string]generation.ResourceState{
		"converted_model|converted.gltf|": {State: generation.ResourcePending},
	}
	f.tasks[task.ID] = task

	pending := PendingResourceFormats(f.catalog.Application().ModelDelivery, task)
	if len(pending) != 2 || pending[0] != "glb" || pending[1] != "gltf" {
		t.Fatalf("pending formats = %v; want [glb gltf]", pending)
	}

	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "tripo-gltf-slot")
	if err != nil {
		t.Fatal(err)
	}
	// The main model is already saved as GLB, so that row stays available; the
	// glTF container is the one this slot still owes and it reads as converting.
	if row := formatByID(t, coverage, "glb"); row.Status != StatusAvailable {
		t.Fatalf("glb = %#v; want available from the saved main model", row)
	}
	if row := formatByID(t, coverage, "gltf"); row.Status != StatusConverting || row.Reason != reasonConverting {
		t.Fatalf("gltf = %#v; want converting", row)
	}

	planned := map[string]bool{}
	complete, err := f.service.Complete(context.Background(), generation.OwnerID("owner"), "tripo-gltf-slot")
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range complete.Steps {
		for _, format := range step.Formats {
			planned[format] = true
		}
	}
	if planned["gltf"] {
		t.Fatalf("a plan was started for gltf while the task still owes it: %#v", complete.Steps)
	}
}
