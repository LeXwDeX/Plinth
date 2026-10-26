package skillrun

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestRecipeInputFreezesExactPromptAndSource(t *testing.T) {
	spec := StepSpec{
		PromptTemplate:  "right side, preserve identity",
		PromptParameter: "prompt", SourceParameter: "reference_assets",
		StaticInput: map[string]json.RawMessage{"quality": json.RawMessage(`"high"`)},
	}
	input, err := stepInput(spec, generation.AssetID("image-a"))
	if err != nil || !MatchesPreparedInput(spec, "image-a", input) {
		t.Fatalf("recipe input: %s %v", input, err)
	}
	for _, changed := range []json.RawMessage{
		json.RawMessage(`{"prompt":"different","reference_assets":["image-a"],"quality":"high"}`),
		json.RawMessage(`{"prompt":"right side, preserve identity","reference_assets":["image-b"],"quality":"high"}`),
		json.RawMessage(`{"prompt":"right side, preserve identity","reference_assets":["image-a","image-b"],"quality":"high"}`),
		json.RawMessage(`{"prompt":"right side, preserve identity","reference_assets":["image-a"],"quality":"low"}`),
	} {
		if MatchesPreparedInput(spec, "image-a", changed) {
			t.Fatalf("changed recipe input accepted: %s", changed)
		}
	}
	spec.StaticInput["prompt"] = json.RawMessage(`"override"`)
	if _, err := stepInput(spec, "image-a"); err == nil {
		t.Fatal("static input overwrote recipe prompt")
	}
}
