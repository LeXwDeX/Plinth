package catalog_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// Fixed parameters require a default and a hidden or explicit read-only surface.
func TestFixedParameterRequiresNonEditableSurfaceAndDefault(t *testing.T) {
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"visible": func(parameter map[string]any) {
			parameter["presentation"] = map[string]any{"surface": "provider", "control": "select"}
		},
		"no default": func(parameter map[string]any) { delete(parameter, "default") },
	} {
		t.Run(name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			for _, item := range doc["offerings"].([]any) {
				offering := item.(map[string]any)
				if offering["id"] != "gemini/gemini-3-pro-image/text-to-image" {
					continue
				}
				for _, entry := range offering["parameters"].([]any) {
					if parameter := entry.(map[string]any); parameter["name"] == "image_size" {
						parameter["fixed"] = true
						delete(parameter, "adjustment_policy")
						delete(parameter, "adjustment_reason")
						mutate(parameter)
					}
				}
			}
			encoded, _ := json.Marshal(doc)
			if _, err := catalog.Load(strings.NewReader(string(encoded))); err == nil ||
				!strings.Contains(err.Error(), "fixed") {
				t.Fatalf("load error = %v, want fixed rejection", err)
			}
		})
	}
}

func TestReadOnlyRequiresFixedAndKeepsServerEnforcement(t *testing.T) {
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, fixed := range []bool{true, false} {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		for _, item := range doc["offerings"].([]any) {
			offering := item.(map[string]any)
			if offering["id"] != "openai/gpt-image-2/text-to-image" {
				continue
			}
			for _, entry := range offering["parameters"].([]any) {
				parameter := entry.(map[string]any)
				if parameter["name"] != "quality" {
					continue
				}
				parameter["fixed"] = fixed
				delete(parameter, "adjustment_policy")
				delete(parameter, "adjustment_reason")
				parameter["label"] = "Fixture quality label"
				parameter["presentation"].(map[string]any)["surface"] = "readonly"
			}
		}
		encoded, _ := json.Marshal(doc)
		product, err := catalog.Load(strings.NewReader(string(encoded)))
		if !fixed {
			if err == nil || !strings.Contains(err.Error(), "readonly") {
				t.Fatalf("non-fixed readonly error = %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		offering, _ := product.Get("openai/gpt-image-2/text-to-image")
		_, input, err := product.Normalize(offering.Capability, offering.ID, json.RawMessage(`{"prompt":"test","quality":"low"}`))
		if err != nil {
			t.Fatal(err)
		}
		var values map[string]any
		if err := json.Unmarshal(input, &values); err != nil {
			t.Fatal(err)
		}
		if values["quality"] != "max" {
			t.Fatalf("readonly quality downgraded: %v", values["quality"])
		}
	}
}

// Meshy rejects 8K textures with quad topology (HTTP 400 on dev-test). The
// constraint lives in the Product Schema as an input rule, so reopening the
// topology and texture choices later (dropping fixed, showing the control)
// still cannot send the invalid combination upstream.
func TestMeshy8KTextureQuadTopologyIsRejectedBySchemaRule(t *testing.T) {
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	targets := map[string]string{
		"meshy/meshy-7.1/text-to-3d":  `{"prompt":"castle","topology":"quad","texture_resolution":"8k"}`,
		"meshy/meshy-7.1/image-to-3d": `{"reference_assets":["asset-1"],"topology":"quad","texture_resolution":"8k"}`,
	}
	for _, item := range doc["offerings"].([]any) {
		offering := item.(map[string]any)
		if _, ok := targets[offering["id"].(string)]; !ok {
			continue
		}
		for _, entry := range offering["parameters"].([]any) {
			parameter := entry.(map[string]any)
			if parameter["name"] == "topology" || parameter["name"] == "texture_resolution" {
				delete(parameter, "fixed")
				delete(parameter, "adjustment_policy")
				delete(parameter, "adjustment_reason")
				parameter["presentation"] = map[string]any{"surface": "provider", "control": "select"}
			}
		}
	}
	encoded, _ := json.Marshal(doc)
	reopened, err := catalog.Load(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	for id, input := range targets {
		offering, _ := reopened.Get(generation.OfferingID(id))
		if _, _, err := reopened.Normalize(offering.Capability, offering.ID, json.RawMessage(input)); err == nil ||
			!strings.Contains(err.Error(), "8K") {
			t.Fatalf("%s: err = %v, want the 8K/quad schema rule", id, err)
		}
		triangle := strings.Replace(input, `"quad"`, `"triangle"`, 1)
		if _, _, err := reopened.Normalize(offering.Capability, offering.ID, json.RawMessage(triangle)); err != nil {
			t.Fatalf("%s: triangle with 8K rejected: %v", id, err)
		}
	}
}

// Seedream size keeps its full option list and pixel bounds; forced_when
// lifts each lower option to the highest size of the same ratio. Dropping
// those forced_when entries restores the original choice range.
func TestSeedreamSizeReopensToDeclaredRange(t *testing.T) {
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, item := range doc["offerings"].([]any) {
		offering := item.(map[string]any)
		if offering["id"] != "jimeng/seedream-4-5/text-to-image" {
			continue
		}
		for _, entry := range offering["parameters"].([]any) {
			if parameter := entry.(map[string]any); parameter["name"] == "size" {
				delete(parameter, "forced_when")
			}
		}
	}
	encoded, _ := json.Marshal(doc)
	reopened, err := catalog.Load(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	offering, _ := reopened.Get("jimeng/seedream-4-5/text-to-image")
	for input, want := range map[string]string{
		// #267: the retired auto-composition tier still names an explicit 1:1 size.
		`{"prompt":"x","size":"2K"}`:        "4096x4096",
		`{"prompt":"x","size":"2048x2048"}`: "2048x2048",
		`{"prompt":"x","size":"1024x1024"}`: "",
	} {
		_, normalized, err := reopened.Normalize(offering.Capability, offering.ID, json.RawMessage(input))
		if want == "" {
			if err == nil {
				t.Fatalf("%s: accepted below min_pixels", input)
			}
			continue
		}
		var values map[string]any
		if err != nil || json.Unmarshal(normalized, &values) != nil || values["size"] != want {
			t.Fatalf("%s: size = %v err = %v, want %s", input, values["size"], err, want)
		}
	}
}

// Legacy discovery keeps the default projections of parameters that are now
// fixed and hidden. They are inert while hidden; dropping fixed and showing
// the control brings back the original legacy defaults with no other edit.
func TestLegacyProjectionsOfFixedParametersArePreserved(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]map[string]string{
		"meshy-7.1": {"image-to-3d": {"target_polycount": "30000"}},
		"tripo-v3.1-full": {
			"text-to-3d":  {"texture_quality": `"standard"`, "geometry_quality": `"standard"`},
			"image-to-3d": {"texture_quality": `"standard"`, "geometry_quality": `"standard"`},
		},
	}
	seen := map[string]bool{}
	for _, model := range product.Application().ProviderDiscovery.LegacyModels {
		seen[model.ModelID] = true
		for capability, fields := range want[model.ModelID] {
			projection := model.ParameterProjections[generation.Capability(capability)]
			for name, value := range fields {
				if got := string(projection.Fields[name].Default); got != value {
					t.Errorf("%s %s.%s legacy default = %s, want %s", model.ModelID, capability, name, got, value)
				}
			}
		}
	}
	for modelID := range want {
		if !seen[modelID] {
			t.Errorf("legacy model %s missing", modelID)
		}
	}
}
