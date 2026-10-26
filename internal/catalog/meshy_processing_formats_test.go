package catalog_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestMeshyProcessingFormatContracts(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id      generation.OfferingID
		formats []string
		// optional formats are requested but not guaranteed by Meshy (or are
		// opaque containers): GLB stays required, these never fail the task.
		optional map[string]bool
		texture  bool
	}{
		{"meshy/processing/remesh", []string{"glb", "fbx", "obj", "usdz", "stl", "3mf"}, map[string]bool{}, false},
		{"meshy/processing/retexture", []string{"glb", "obj", "fbx", "stl", "usdz", "3mf"}, map[string]bool{"stl": true, "3mf": true}, true},
	} {
		t.Run(string(tc.id), func(t *testing.T) {
			offering, found := c.Get(tc.id)
			if !found {
				t.Fatal("missing offering")
			}
			formatParameterFound := false
			for _, parameter := range offering.Parameters {
				if parameter.Name != "target_formats" {
					continue
				}
				formatParameterFound = true
				if !parameter.Required || parameter.MinItems != len(tc.formats) ||
					parameter.MaxItems != len(tc.formats) {
					t.Fatalf("format bounds = %#v", parameter)
				}
				var defaults []string
				if err := json.Unmarshal(parameter.Default, &defaults); err != nil {
					t.Fatal(err)
				}
				if len(defaults) != len(tc.formats) {
					t.Fatalf("format default = %#v", defaults)
				}
				for index, format := range tc.formats {
					if defaults[index] != format {
						t.Fatalf("format default = %#v, want %#v", defaults, tc.formats)
					}
				}
			}
			if !formatParameterFound {
				t.Fatal("missing target_formats parameter")
			}
			for _, format := range tc.formats {
				found := false
				for _, output := range offering.Outputs {
					if output.Role != "model" || output.Variant != format || output.Minimum == nil {
						continue
					}
					if tc.optional[format] {
						found = *output.Minimum == 0 && output.Delivery == "background"
					} else {
						found = *output.Minimum == 1 && output.Delivery == ""
					}
				}
				if !found {
					t.Fatalf("missing required output %s", format)
				}
			}
			if tc.texture {
				found := false
				for _, output := range offering.Outputs {
					if output.Role == "texture" && output.VariantPrefix == "texture." &&
						output.Delivery == "required" && output.Minimum != nil && *output.Minimum == 0 {
						found = true
					}
				}
				if !found {
					t.Fatal("retexture has no required-if-present texture contract")
				}
			}
		})
	}
}
