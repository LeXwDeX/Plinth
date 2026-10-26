package catalogrecipes_test

import (
	"context"
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
		"openai/gpt-image-2/image-to-image")
	if err != nil || !ok {
		t.Fatalf("resolve: %+v %v", recipe, err)
	}
	if recipe.Revision != "2026-10-13.1" || len(recipe.Steps) != 3 {
		t.Fatalf("unexpected versioned recipe: %+v", recipe)
	}
	for index, key := range []string{"front", "side", "back"} {
		step := recipe.Steps[index]
		if step.Key != key || step.OfferingID != "openai/gpt-image-2/image-to-image" ||
			step.PromptParameter != "prompt" || step.SourceParameter != "reference_assets" ||
			step.OfferingRevision == "" || step.PromptTemplate == "" {
			t.Fatalf("unresolved %s step: %+v", key, step)
		}
	}
	if _, ok, err := catalogrecipes.New(source).ResolveRecipe(context.Background(), "image-three-view",
		"openai/gpt-image-2/text-to-image"); err != nil || ok {
		t.Fatalf("text offering accepted: ok=%v err=%v", ok, err)
	}
}
