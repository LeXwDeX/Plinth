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
	input, err := stepInput(spec, generation.AssetID("image-a"), "")
	if err != nil || !MatchesPreparedInput(spec, "image-a", "", input) {
		t.Fatalf("recipe input: %s %v", input, err)
	}
	for _, changed := range []json.RawMessage{
		json.RawMessage(`{"prompt":"different","reference_assets":["image-a"],"quality":"high"}`),
		json.RawMessage(`{"prompt":"right side, preserve identity","reference_assets":["image-b"],"quality":"high"}`),
		json.RawMessage(`{"prompt":"right side, preserve identity","reference_assets":["image-a","image-b"],"quality":"high"}`),
		json.RawMessage(`{"prompt":"right side, preserve identity","reference_assets":["image-a"],"quality":"low"}`),
	} {
		if MatchesPreparedInput(spec, "image-a", "", changed) {
			t.Fatalf("changed recipe input accepted: %s", changed)
		}
	}
	spec.StaticInput["prompt"] = json.RawMessage(`"override"`)
	if _, err := stepInput(spec, "image-a", ""); err == nil {
		t.Fatal("static input overwrote recipe prompt")
	}
}

// A step whose recipe asks for the original picture beside the source view
// freezes both references in order, and a changed pair is refused (#409). The
// single-source recipe above is unchanged.
func TestRecipeInputFreezesTheSourcePictureAsASecondReference(t *testing.T) {
	spec := StepSpec{
		PromptTemplate: "left view, keep the pose", PromptParameter: "prompt",
		SourceParameter: "reference_assets", IncludeSourceAsset: true,
	}
	input, err := stepInput(spec, "front-view", "original")
	if err != nil || !MatchesPreparedInput(spec, "front-view", "original", input) {
		t.Fatalf("two-source input: %s %v", input, err)
	}
	var frozen map[string]json.RawMessage
	if err := json.Unmarshal(input, &frozen); err != nil {
		t.Fatal(err)
	}
	var assets []string
	if err := json.Unmarshal(frozen["reference_assets"], &assets); err != nil {
		t.Fatal(err)
	}
	if len(assets) != 2 || assets[0] != "front-view" || assets[1] != "original" {
		t.Fatalf("reference order = %v", assets)
	}
	for _, changed := range [][]string{
		{"original", "front-view"},
		{"front-view"},
		{"front-view", "other"},
	} {
		encoded, _ := json.Marshal(map[string]any{"prompt": spec.PromptTemplate, "reference_assets": changed})
		if MatchesPreparedInput(spec, "front-view", "original", encoded) {
			t.Fatalf("changed reference list accepted: %v", changed)
		}
	}
	// A recipe that names the same picture twice cannot freeze an input.
	if _, err := stepInput(spec, "same", "same"); err == nil {
		t.Fatal("a step cannot take the same picture twice")
	}
}
