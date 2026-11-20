package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #376: the marks of a Rodin request are a hidden platform parameter, so a
// task's public input keeps them out next to the pictures they mark, while the
// settings the person can see stay.
func TestPublicTaskInputWithholdsRodinImageLabels(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get("hyper3d/rodin-gen-2.5/image-to-3d")
	if !ok {
		t.Fatal("missing the Rodin image Offering")
	}
	task := generation.Task{
		Input:    json.RawMessage(`{"prompt":"a robot","reference_assets":["asset-secret-1","asset-secret-2"],"reference_image_labels":["F","L"],"mesh_mode":"Raw","seed":3}`),
		Offering: offering,
	}
	got := publicTaskInput(task)
	if got == nil || got.Prompt != "a robot" || string(got.Parameters["mesh_mode"]) != `"Raw"` || string(got.Parameters["seed"]) != `3` {
		t.Fatalf("input = %#v", got)
	}
	encoded, _ := json.Marshal(got)
	for _, leaked := range []string{"reference_image_labels", "image_label", "asset-secret", `"F"`, `"L"`} {
		if containsString(string(encoded), leaked) {
			t.Fatalf("%q leaked in %s", leaked, encoded)
		}
	}
}
