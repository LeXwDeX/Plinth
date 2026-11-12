package catalog_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestTripoFullFormatSelectionRequiresExplicitCostConsent(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		capability generation.Capability
		input      string
	}{
		{generation.TextTo3D, `{"prompt":"castle"}`},
		{generation.ImageTo3D, `{"reference_assets":["asset-1"]}`},
	}
	for _, tc := range cases {
		t.Run(string(tc.capability), func(t *testing.T) {
			id := generation.OfferingID("tripo/tripo-v3.1-full/" + string(tc.capability))
			if _, _, err := taskCatalog.Normalize(tc.capability, id, json.RawMessage(tc.input)); err == nil {
				t.Fatal("full format selection accepted without explicit consent")
			}
			var input map[string]any
			if err := json.Unmarshal([]byte(tc.input), &input); err != nil {
				t.Fatal(err)
			}
			input["authorize_extra_credits"] = true
			encoded, _ := json.Marshal(input)
			offering, normalized, err := taskCatalog.Normalize(tc.capability, id, encoded)
			if err != nil {
				t.Fatal(err)
			}
			var values struct {
				Formats []string `json:"output_formats"`
			}
			if err := json.Unmarshal(normalized, &values); err != nil {
				t.Fatal(err)
			}
			if len(values.Formats) != 6 {
				t.Fatalf("formats=%v", values.Formats)
			}
			for _, format := range []string{"GLTF", "USDZ", "FBX", "OBJ", "STL", "3MF"} {
				found := false
				for _, candidate := range values.Formats {
					if candidate == format {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing %s in %v", format, values.Formats)
				}
			}
			// The format set is the purpose's to choose (#278), but advised cannot
			// break the contract: the GLTF conversion delivers the model-role GLB, so
			// a selection without it, an empty one, repeats and unknown formats are
			// refused.
			for _, probe := range [][]string{{"FBX"}, {}, {"GLTF", "FBX", "FBX"}, {"GLTF", "XYZ"}, {"STL", "3MF"}} {
				input["output_formats"] = probe
				encoded, _ := json.Marshal(input)
				if _, _, err := taskCatalog.Normalize(tc.capability, id, encoded); err == nil {
					t.Fatalf("accepted incomplete/duplicate formats: %v", probe)
				}
			}
			input["output_formats"] = []string{"GLTF", "FBX"}
			encoded, _ = json.Marshal(input)
			_, frozen, err := taskCatalog.Normalize(tc.capability, id, encoded)
			if err != nil {
				t.Fatalf("the game format set was refused: %v", err)
			}
			var chosen struct {
				Formats []string `json:"output_formats"`
			}
			if err := json.Unmarshal(frozen, &chosen); err != nil || len(chosen.Formats) != 2 ||
				chosen.Formats[0] != "GLTF" || chosen.Formats[1] != "FBX" {
				t.Fatalf("the chosen set was not frozen as given: %s (%v)", frozen, err)
			}
			delete(input, "authorize_extra_credits")
			encoded, _ = json.Marshal(input)
			if _, _, err := taskCatalog.Normalize(tc.capability, id, encoded); err == nil {
				t.Fatal("paid formats accepted without explicit consent")
			}
			// A conversion costs 5 credits, the basic price; it becomes the advanced
			// conversion (10 credits) only while a texture format, texture size or
			// pivot option is off its upstream default (Tripo pricing page, read
			// 2026-11-12), which the selection's upgrade declares.
			selection := (*schema.NativePriceSelection)(nil)
			if offering.NativePricing != nil {
				selection = offering.NativePricing.Selection
			}
			if selection == nil || selection.Amount != "5" || selection.Upgrade == nil ||
				selection.Upgrade.Amount != "10" || len(selection.Upgrade.WhenAny) != 3 {
				t.Fatalf("native conversion price=%#v", offering.NativePricing)
			}
		})
	}
}
