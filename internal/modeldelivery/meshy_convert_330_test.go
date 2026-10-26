package modeldelivery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

// #330: 补齐格式 for a Meshy (or any Convert-compatible) source runs Meshy
// Convert. Plans made before, with Remesh and convert_format_only, are still
// recognized through the retired conversion: what they delivered stays
// available, a running one still locks its formats, and Remesh is never
// planned again.

func convertResult(f *fixture, id string, variants ...string) generation.Task {
	var specs []assetSpec
	for _, variant := range variants {
		specs = append(specs, assetSpec{role: "model", variant: variant})
	}
	task := f.addTask(id, "owner", "meshy", generation.FormatConversion, specs...)
	task.Offering.ID = meshyConvert
	f.tasks[task.ID] = task
	return task
}

func legacyRemeshPlan(f *fixture, t *testing.T, state string, result generation.TaskID) {
	t.Helper()
	plan := processing.Plan{ID: "legacy-remesh-" + state, OwnerID: "owner", SessionID: "session-owner",
		SourceTaskID: "meshy-task", SourceAssetID: "meshy-task-a0", State: state,
		Steps: []processing.Step{{OfferingID: remeshOffering, Revision: "2026-10-22.329.1", State: state, TaskID: result,
			Input: json.RawMessage(`{"convert_format_only":true,"source_task":"meshy-task"}`)}}}
	if err := f.store.CreateProcessingPlan(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
}

func TestMeshy330CompletionNeverPlansTheRetiredRemeshRoute(t *testing.T) {
	f := newPaidCompletionFixture(t)
	f.meshyGlbOnly("meshy-task", "owner")
	// Even with Convert unavailable, Remesh is not a fallback.
	f.unavailable[meshyConvert] = true
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range coverage.Formats {
		if row.Conversion != nil && row.Conversion.OfferingID == remeshOffering {
			t.Fatalf("%s offers the retired Remesh route: %#v", row.ID, row)
		}
	}
	if fbx := formatByID(t, coverage, "fbx"); fbx.Status != StatusUnavailable || fbx.Reason != reasonOfferingUnavailable {
		t.Fatalf("fbx = %#v", fbx)
	}
	if len(coverage.Steps) != 0 || coverage.CanComplete {
		t.Fatalf("steps = %#v", coverage.Steps)
	}
	// Available again: one Convert run fills the five missing Meshy formats
	// for 1 credit.
	delete(f.unavailable, meshyConvert)
	coverage, _ = f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if len(coverage.Steps) != 1 || coverage.Steps[0].OfferingID != meshyConvert || coverage.CompletionCredits != 1 ||
		strings.Join(coverage.Steps[0].Formats, ",") != "fbx,obj,stl,usdz,3mf" {
		t.Fatalf("steps = %#v credits = %d", coverage.Steps, coverage.CompletionCredits)
	}
	if fbx := formatByID(t, coverage, "fbx"); fbx.Conversion == nil || fbx.Conversion.EstimatedCredits != 1 || !fbx.Conversion.CreditsMetered {
		t.Fatalf("fbx = %#v", fbx)
	}
}

func TestMeshy330FinishedLegacyRemeshPlanStillDeliversItsFiles(t *testing.T) {
	f := newPaidCompletionFixture(t)
	f.meshyGlbOnly("meshy-task", "owner")
	result := f.addTask("legacy-remesh-task", "owner", "meshy", generation.Remesh, func() []assetSpec {
		var specs []assetSpec
		for _, variant := range []string{"glb", "fbx", "obj", "usdz", "stl", "3mf"} {
			specs = append(specs, assetSpec{role: "model", variant: variant})
		}
		return specs
	}()...)
	legacyRemeshPlan(f, t, processing.Succeeded, result.ID)
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"fbx", "obj", "usdz", "stl", "3mf"} {
		if row := formatByID(t, coverage, id); row.Status != StatusAvailable || row.TaskID != "legacy-remesh-task" {
			t.Fatalf("%s = %#v: a file paid for before #330 is lost", id, row)
		}
	}
	if coverage.CanComplete {
		t.Fatalf("nothing is missing, yet completion is offered: %#v", coverage.Steps)
	}
	if _, err := f.service.Complete(context.Background(), "owner", "meshy-task"); !errors.Is(err, ErrNothingToComplete) {
		t.Fatalf("complete = %v", err)
	}
}

func TestMeshy330RunningLegacyRemeshPlanStillLocksItsFormats(t *testing.T) {
	f := newPaidCompletionFixture(t)
	f.meshyGlbOnly("meshy-task", "owner")
	legacyRemeshPlan(f, t, processing.Paused, "")
	coverage, _ := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if usdz := formatByID(t, coverage, "usdz"); usdz.Status != StatusConverting || usdz.Conversion == nil || usdz.Conversion.OfferingID != remeshOffering {
		t.Fatalf("usdz = %#v", usdz)
	}
	if coverage.CanComplete {
		t.Fatalf("Convert would pay again for what the paused plan converts: %#v", coverage.Steps)
	}
	if _, err := f.service.Complete(context.Background(), "owner", "meshy-task"); !errors.Is(err, ErrNothingToComplete) {
		t.Fatalf("complete = %v", err)
	}
	if len(f.store.plans) != 1 {
		t.Fatalf("plans = %d", len(f.store.plans))
	}
}

// Meshy Convert returns model files only: an OBJ without a material library
// or textures gets the README note, as a Tripo conversion's does.
func TestMeshy330ReadmeExplainsAConvertObjWithoutMaterial(t *testing.T) {
	f := newFixture(t)
	convertResult(f, "convert-task", "glb", "fbx", "obj", "usdz", "stl", "3mf")
	readme := readmeOf(t, f, "convert-task")
	if !strings.Contains(readme, objNote(f, "model.obj")) {
		t.Fatalf("README lacks the converted OBJ note:\n%s", readme)
	}
	// Meshy's own generation OBJ is not a conversion (TestReadmeLeavesOtherObjFilesAlone).
}

// #330 A (dev-test note 99526): the real Convert OBJ is a Blender export that
// names "mtllib temp.mtl" and uses that material, with complete UVs, while
// Convert delivers no MTL and no texture. The README must explain exactly
// that, instead of listing temp.mtl as a file the task failed to save. The
// OBJ bytes stay as delivered (no rewrite of the dangling reference).
func TestMeshy330ReadmeExplainsTheDanglingMaterialLibraryOfAConvertObj(t *testing.T) {
	f := newFixture(t)
	const object = "# Blender 4.4.0\nmtllib temp.mtl\no Mesh\nv 0 0 0\nv 1 0 0\nv 0 1 0\nvt 0 0\nvt 1 0\nvt 0 1\nusemtl Material\nf 1/1 2/2 3/3\n"
	var specs []assetSpec
	for _, variant := range []string{"glb", "fbx", "obj", "usdz", "stl", "3mf"} {
		spec := assetSpec{role: "model", variant: variant}
		if variant == "obj" {
			spec.body = object
		}
		specs = append(specs, spec)
	}
	task := f.addTask("convert-task", "owner", "meshy", generation.FormatConversion, specs...)
	task.Offering.ID = meshyConvert
	f.tasks[task.ID] = task

	readme := readmeOf(t, f, "convert-task")
	note := strings.NewReplacer("{path}", "model.obj", "{references}", "temp.mtl").Replace(f.service.schema.Archive.Readme.ConvertedObjReferenceNote)
	if f.service.schema.Archive.Readme.ConvertedObjReferenceNote == "" || !strings.Contains(readme, note) {
		t.Fatalf("README lacks the Convert reference note:\n%s", readme)
	}
	missing := strings.ReplaceAll(f.service.schema.Archive.Readme.DependencyMissingLine, "{path}", "temp.mtl")
	if strings.Contains(readme, missing) || strings.Contains(readme, objNote(f, "model.obj")) {
		t.Fatalf("README still blames the task for temp.mtl or claims a reference-free OBJ:\n%s", readme)
	}
	files := func() map[string]string {
		archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), "convert-task")
		if err != nil {
			t.Fatal(err)
		}
		defer archive.Close()
		return readZip(t, archive)
	}()
	if files["model.obj"] != object {
		t.Fatalf("the delivered OBJ bytes changed:\n%q", files["model.obj"])
	}
}
