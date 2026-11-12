package mock

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #278: a Tripo task whose purpose chose the GLB and FBX only is delivered by the
// mock the way the upstream delivers it. The kernel pins the unselected
// converted-model slots to minimum 0 at submission; the mock reads the pinned
// Offering and leaves them out.
func TestMockDeliversOnlyTheSelectedTripoFormats(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	const id = "tripo/tripo-v3.1-full/text-to-3d"
	offering, ok := loaded.Get(generation.OfferingID(id))
	if !ok {
		t.Fatalf("%s missing", id)
	}
	for name, formats := range map[string][]string{
		"game":    {"GLTF", "FBX"},
		"print":   {"GLTF", "STL", "3MF"},
		"display": {"GLTF", "USDZ", "FBX", "OBJ", "STL", "3MF"},
	} {
		t.Run(name, func(t *testing.T) {
			_, frozen, err := loaded.Normalize(generation.TextTo3D, id, json.RawMessage(`{"prompt":"a crate","authorize_extra_credits":true,"output_formats":`+mustJSON(t, formats)+`}`))
			if err != nil {
				t.Fatal(err)
			}
			var values map[string]any
			if err := json.Unmarshal(frozen, &values); err != nil {
				t.Fatal(err)
			}
			pinned := offering
			pinned.Outputs = append([]generation.AssetRequirement(nil), offering.Outputs...)
			for index, output := range pinned.Outputs {
				if output.RequiredWhen != nil && !schema.ConditionMatches(*output.RequiredWhen, values) {
					zero := 0
					pinned.Outputs[index].Minimum = &zero
				}
			}
			var delivered []string
			for _, file := range complete(t, New(), pinned, "formats-"+name, values) {
				if file.claim.Role == "converted_model" {
					delivered = append(delivered, file.claim.Variant)
				}
			}
			want := make([]string, 0, len(formats))
			for _, format := range formats {
				want = append(want, "converted."+strings.ToLower(format))
			}
			slices.Sort(delivered)
			slices.Sort(want)
			if !slices.Equal(delivered, want) {
				t.Fatalf("delivered converted models = %v, want %v", delivered, want)
			}
		})
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
