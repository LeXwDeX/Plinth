package skillrun

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

type fixedRecipe struct{ recipe Recipe }

func (f fixedRecipe) ResolveRecipe(context.Context, string, generation.OfferingID, string) (Recipe, bool, error) {
	return f.recipe, true, nil
}

// createdRunStore records the run Create hands to the store; the other store
// methods are never reached.
type createdRunStore struct {
	Store
	created Run
}

func (s *createdRunStore) CreateSkillRun(_ context.Context, run Run) (Run, error) {
	s.created = run
	return run, nil
}

// #283: the creator of a run may freeze extra static input (the three-view
// frame) into every step, but never replace what the recipe owns.
func TestCreateFreezesExtraStaticInputIntoEveryStep(t *testing.T) {
	declared := map[string]json.RawMessage{"quality": json.RawMessage(`"high"`)}
	recipe := Recipe{ID: "three-view", Revision: "r1", Steps: []StepSpec{
		{Key: "front", Label: "Front", Capability: "image-to-image", OfferingID: "offering", OfferingRevision: "r2",
			PromptTemplate: "front", PromptParameter: "prompt", SourceParameter: "reference_assets",
			StaticInput: declared, OutputRole: "image"},
		{Key: "left", Label: "Left", Capability: "image-to-image", OfferingID: "offering", OfferingRevision: "r2",
			PromptTemplate: "left", PromptParameter: "prompt", SourceParameter: "reference_assets",
			OutputRole: "image", SourceStepKey: "front"},
	}}
	command := func(extra map[string]json.RawMessage) CreateCommand {
		return CreateCommand{OwnerID: "owner", SessionID: "session", RecipeID: "three-view", ExpectedRevision: "r1",
			SelectedOfferingID: "offering", SourceAssetID: "source", IdempotencyKey: "key", StaticInput: extra}
	}
	store := &createdRunStore{}
	service, err := NewService(store, fixedRecipe{recipe})
	if err != nil {
		t.Fatal(err)
	}
	extra := map[string]json.RawMessage{"aspect_ratio": json.RawMessage(`"3:4"`)}
	run, err := service.Create(context.Background(), command(extra))
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range run.Steps {
		if string(step.Spec.StaticInput["aspect_ratio"]) != `"3:4"` {
			t.Fatalf("step %s lost the frame: %v", step.Spec.Key, step.Spec.StaticInput)
		}
	}
	if string(run.Steps[0].Spec.StaticInput["quality"]) != `"high"` {
		t.Fatalf("the recipe's own static input was lost: %v", run.Steps[0].Spec.StaticInput)
	}
	if len(declared) != 1 {
		t.Fatalf("the resolver's map was written to: %v", declared)
	}
	// The frame is part of the run's identity: another frame is another request.
	other, err := service.Create(context.Background(),
		command(map[string]json.RawMessage{"aspect_ratio": json.RawMessage(`"1:1"`)}))
	if err != nil || other.RecipeHash == run.RecipeHash || other.RequestHash == run.RequestHash {
		t.Fatalf("another frame must change the run's hashes: %v", err)
	}
	// What the recipe owns cannot be replaced.
	for name, key := range map[string]string{"declared static input": "quality", "prompt": "prompt", "source": "reference_assets"} {
		_, err := service.Create(context.Background(), command(map[string]json.RawMessage{key: json.RawMessage(`"x"`)}))
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s was replaced: %v", name, err)
		}
	}
}
