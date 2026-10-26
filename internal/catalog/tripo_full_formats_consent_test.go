package catalog_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
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
			// advised 不能突破必需格式合同：子集、空集和重复取值拒绝。
			for _, probe := range [][]string{{"FBX"}, {}, {"FBX", "FBX"}} {
				input["output_formats"] = probe
				encoded, _ := json.Marshal(input)
				if _, _, err := taskCatalog.Normalize(tc.capability, id, encoded); err == nil {
					t.Fatalf("accepted incomplete/duplicate formats: %v", probe)
				}
			}
			input["output_formats"] = []string{"FBX"}
			delete(input, "authorize_extra_credits")
			encoded, _ = json.Marshal(input)
			if _, _, err := taskCatalog.Normalize(tc.capability, id, encoded); err == nil {
				t.Fatal("paid formats accepted without explicit consent")
			}
			if offering.NativePricing == nil || offering.NativePricing.Selection == nil || offering.NativePricing.Selection.Amount != "5" {
				t.Fatalf("native conversion price=%#v", offering.NativePricing)
			}
		})
	}
}
