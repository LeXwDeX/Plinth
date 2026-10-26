package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestMeshy71RequiresEveryRequestedModelFormat(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range []generation.Capability{generation.TextTo3D, generation.ImageTo3D} {
		t.Run(string(capability), func(t *testing.T) {
			// advised 允许合法质量值；必需格式交付不变。
			input := `{"prompt":"castle","geometry_resolution":"standard","should_remesh":true}`
			if capability == generation.ImageTo3D {
				input = `{"reference_assets":["asset-1"],"geometry_resolution":"standard","should_remesh":true,"texture_resolution":"2k"}`
			}
			offering, normalized, err := taskCatalog.Normalize(capability, "", json.RawMessage(input))
			if err != nil {
				t.Fatal(err)
			}
			if offering.ModelID != "meshy-7.1" || !offering.Default {
				t.Fatalf("default offering = %s default=%v", offering.ModelID, offering.Default)
			}
			var values struct {
				Formats    []string `json:"target_formats"`
				Resolution string   `json:"geometry_resolution"`
				Remesh     bool     `json:"should_remesh"`
				Texture    string   `json:"texture_resolution"`
			}
			if err := json.Unmarshal(normalized, &values); err != nil {
				t.Fatal(err)
			}
			want := map[string]bool{"glb": true, "obj": true, "fbx": true, "stl": true, "usdz": true, "3mf": true}
			wantTexture := "8k"
			if capability == generation.ImageTo3D {
				wantTexture = "2k"
			}
			if len(values.Formats) != len(want) || values.Resolution != "standard" || !values.Remesh || values.Texture != wantTexture {
				t.Fatalf("normalized formats/resolution/remesh/texture = %v/%s/%v/%s", values.Formats, values.Resolution, values.Remesh, values.Texture)
			}
			for _, format := range values.Formats {
				if !want[format] {
					t.Fatalf("unexpected or repeated format %q in %v", format, values.Formats)
				}
				delete(want, format)
			}
			for _, format := range []string{"glb", "obj", "fbx", "stl", "usdz", "3mf"} {
				found := false
				for _, output := range offering.Outputs {
					if output.Role == "model" && output.Variant == format && output.Minimum != nil && *output.Minimum == 1 {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing required output %s", format)
				}
			}
			for _, companion := range []struct{ role, variant, prefix string }{
				{role: "model", variant: "mtl"},
				{role: "texture", prefix: "texture."},
			} {
				found := false
				for _, output := range offering.Outputs {
					if output.Role == companion.role && output.Variant == companion.variant &&
						output.VariantPrefix == companion.prefix && output.Delivery == "required" &&
						output.Minimum != nil && *output.Minimum == 0 {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing conditional companion contract %#v", companion)
				}
			}
			if _, _, err := taskCatalog.Normalize(capability, offering.ID, json.RawMessage(`{"ultra_mode":true}`)); err == nil {
				t.Fatal("accepted undeclared ultra_mode")
			}
			// 必需格式不能被建议设置移除：子集和重复取值拒绝。
			for _, probe := range []string{
				`{"target_formats":["glb"]}`,
				`{"target_formats":["glb","glb","glb","glb","glb","glb"]}`,
			} {
				raw := json.RawMessage(strings.Replace(input, "{", "{"+probe[1:len(probe)-1]+",", 1))
				if _, _, err := taskCatalog.Normalize(capability, offering.ID, raw); err == nil {
					t.Fatalf("accepted incomplete/duplicate formats: %s", probe)
				}
			}
			if _, _, err := taskCatalog.Normalize(capability, generation.OfferingID("meshy/meshy-7/"+string(capability)), json.RawMessage(input)); err == nil {
				t.Fatal("deprecated Meshy 7 offering accepted new task")
			}
		})
	}
}
