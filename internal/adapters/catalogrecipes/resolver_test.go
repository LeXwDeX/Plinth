package catalogrecipes_test

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/catalogrecipes"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestThreeViewRecipeResolvesSelectedImageOffering(t *testing.T) {
	source, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	recipe, ok, err := catalogrecipes.New(source).ResolveRecipe(context.Background(), "image-three-view",
		"openai/gpt-image-2/image-to-image", "")
	if err != nil || !ok {
		t.Fatalf("resolve: %+v %v", recipe, err)
	}
	if recipe.Revision != "2026-11-10.243.1" || len(recipe.Steps) != 4 {
		t.Fatalf("unexpected versioned recipe: %+v", recipe)
	}
	// The views are the multiview view keys; only the right view is optional
	// (#65), the first uses the original source and every later view uses the
	// front view (#243).
	for index, key := range []string{"front", "left", "back", "right"} {
		step := recipe.Steps[index]
		if step.Optional != (key == "right") {
			t.Fatalf("%s optional = %v", key, step.Optional)
		}
		if step.Key != key || step.OfferingID != "openai/gpt-image-2/image-to-image" ||
			step.PromptParameter != "prompt" || step.SourceParameter != "reference_assets" ||
			step.OfferingRevision == "" || step.PromptTemplate == "" {
			t.Fatalf("unresolved %s step: %+v", key, step)
		}
		wantSource := "front"
		if index == 0 {
			wantSource = ""
		}
		if step.SourceStepKey != wantSource {
			t.Fatalf("%s source_step_key = %q, want %q", key, step.SourceStepKey, wantSource)
		}
	}
	if _, ok, err := catalogrecipes.New(source).ResolveRecipe(context.Background(), "image-three-view",
		"openai/gpt-image-2/text-to-image", ""); err != nil || ok {
		t.Fatalf("text offering accepted: ok=%v err=%v", ok, err)
	}
}

func TestThreeViewRecipeAppliesTheSelectedSubjectType(t *testing.T) {
	source, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(subject string) (string, string, bool, error) {
		recipe, ok, err := catalogrecipes.New(source).ResolveRecipe(context.Background(), "image-three-view",
			"openai/gpt-image-2/image-to-image", subject)
		if !ok {
			return "", "", ok, err
		}
		return recipe.Steps[0].PromptTemplate, recipe.Steps[1].PromptTemplate, ok, err
	}
	defFront, defView, ok, err := resolve("")
	if !ok || err != nil {
		t.Fatalf("default resolve: %v %v", ok, err)
	}
	if !strings.Contains(defFront, "one complete object") || strings.Contains(defFront, "{subject_") ||
		!strings.Contains(defView, "orthographic") || strings.Contains(defView, "{subject_") {
		t.Fatalf("default generic template not applied: front=%q view=%q", defFront, defView)
	}
	humanFront, humanView, ok, err := resolve("humanoid")
	if !ok || err != nil {
		t.Fatalf("humanoid resolve: %v %v", ok, err)
	}
	if !strings.Contains(humanFront, "A-pose") || !strings.Contains(humanView, "A-pose") {
		t.Fatalf("humanoid template not applied: front=%q view=%q", humanFront, humanView)
	}
	if _, _, ok, err := resolve("no-such-subject"); ok || err != nil {
		t.Fatalf("unknown subject type accepted: ok=%v err=%v", ok, err)
	}
}
