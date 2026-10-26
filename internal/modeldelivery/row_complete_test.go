package modeldelivery

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #323: a single obtainable row can be converted on its own. The request
// starts only the step that delivers that format; it keeps the plan key, so
// the row action and 补齐格式 never start the same paid run twice.
func TestCompleteStartsOnlyTheStepOfTheRequestedFormat(t *testing.T) {
	f := newFixture(t)
	f.meshyGlbOnly("meshy-task", "owner")
	ctx := context.Background()

	coverage, err := f.service.Complete(ctx, "owner", "meshy-task", "gltf")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.store.plans) != 1 {
		t.Fatalf("plans = %#v", f.store.plans)
	}
	for _, plan := range f.store.plans {
		if plan.Steps[0].OfferingID != generatedGltf {
			t.Fatalf("started %s for gltf", plan.Steps[0].OfferingID)
		}
	}
	if gltf := formatByID(t, coverage, "gltf"); gltf.Status != StatusConverting {
		t.Fatalf("gltf after its row action = %#v", gltf)
	}
	if fbx := formatByID(t, coverage, "fbx"); fbx.Status != StatusObtainable {
		t.Fatalf("fbx must stay obtainable = %#v", fbx)
	}
	if !coverage.CanComplete {
		t.Fatal("the remaining paid step must still be offered by 补齐格式")
	}

	// The same row again: nothing new to start.
	if _, err := f.service.Complete(ctx, "owner", "meshy-task", "gltf"); !errors.Is(err, ErrNothingToComplete) {
		t.Fatalf("repeat row action = %v", err)
	}
	// A format no step delivers starts nothing either.
	if _, err := f.service.Complete(ctx, "owner", "meshy-task", "glb"); !errors.Is(err, ErrNothingToComplete) {
		t.Fatalf("available format = %v", err)
	}
	if _, err := f.service.Complete(ctx, "owner", "meshy-task", "nope"); !errors.Is(err, ErrNothingToComplete) {
		t.Fatalf("unknown format = %v", err)
	}
	if len(f.store.plans) != 1 {
		t.Fatalf("plans after refused requests = %d", len(f.store.plans))
	}

	// 补齐格式 afterwards starts only what is left (the Meshy step).
	if _, err := f.service.Complete(ctx, "owner", "meshy-task"); err != nil {
		t.Fatal(err)
	}
	if len(f.store.plans) != 2 || f.submitted != 0 {
		t.Fatalf("plans=%d submitted=%d", len(f.store.plans), f.submitted)
	}
}

// A step that converts several formats in one run is started whole from any
// of its rows; the coverage names every format it converts.
func TestCompleteOfOneFormatStartsItsWholeStep(t *testing.T) {
	f := newPaidCompletionFixture(t)
	f.meshyGlbOnly("meshy-task", "owner")
	coverage, err := f.service.Complete(context.Background(), "owner", "meshy-task", "obj")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.store.plans) != 1 {
		t.Fatalf("plans = %#v", f.store.plans)
	}
	for _, id := range []string{"fbx", "obj", "usdz", "stl", "3mf"} {
		if row := formatByID(t, coverage, id); row.Status != StatusConverting {
			t.Fatalf("%s = %#v", id, row)
		}
	}
}

// The row names who converts: the coverage carries the Offering's short
// provider label beside its provider name.
func TestCoverageCarriesTheProviderShortLabel(t *testing.T) {
	f := newFixture(t)
	f.meshyGlbOnly("meshy-task", "owner")
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	gltf := formatByID(t, coverage, "gltf")
	if gltf.Conversion == nil || gltf.Conversion.ProviderID != "local" || gltf.Conversion.ProviderShortLabel != "本地" || gltf.Conversion.CreditsMetered {
		t.Fatalf("gltf conversion = %#v", gltf.Conversion)
	}
	fbx := formatByID(t, coverage, "fbx")
	if fbx.Conversion == nil || fbx.Conversion.ProviderShortLabel != "MESHY" || fbx.Conversion.ProviderName != "Meshy AI" {
		t.Fatalf("fbx conversion = %#v", fbx.Conversion)
	}
	for _, step := range coverage.Steps {
		if step.ProviderShortLabel == "" {
			t.Fatalf("step without short label: %#v", step)
		}
	}
}
