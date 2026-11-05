package catalog_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #130: Meshy Remesh rebuilds geometry only. Its description tells the Agent
// that textures are not kept and the size may change, and points to the baked
// decimation, Retexture and Resize. The size claim holds because every field
// that could keep the size on Remesh is a deprecated wire field, so no Schema
// mapping can send one; the bake claim holds because Tripo decimation fixes
// bake and smart UV on.
func TestRemeshDescriptionStatesWhatItDoesNotKeep(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	remesh, ok := c.Get("meshy/processing/remesh")
	if !ok {
		t.Fatal("missing remesh offering")
	}
	for _, want := range []string{"只重建网格几何", "不保留贴图", "尺寸", "带烘焙的减面", "换贴图", "精确尺寸"} {
		if !strings.Contains(remesh.Description, want) {
			t.Errorf("remesh description omits %q: %q", want, remesh.Description)
		}
	}
	var options map[string]any
	if err := json.Unmarshal(remesh.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	endpoint, _ := options["endpoint"].(string)
	deprecated := map[string]bool{}
	for _, entry := range c.Application().ProviderDeprecations {
		if entry.ProviderID != remesh.ProviderID {
			continue
		}
		for _, field := range entry.Parameters {
			if len(field.Endpoints) == 0 || slices.Contains(field.Endpoints, endpoint) {
				deprecated[field.Name] = true
			}
		}
	}
	for _, sizeField := range []string{"resize_height", "resize_longest_side", "auto_size", "origin_at"} {
		if !deprecated[sizeField] {
			t.Errorf("%s is not a deprecated remesh field; recheck the size note", sizeField)
		}
		for _, parameter := range remesh.Parameters {
			if parameter.Name == sizeField || parameter.WireName == sizeField {
				t.Errorf("remesh declares %s", sizeField)
			}
		}
	}
	for _, step := range []generation.OfferingID{"meshy/processing/retexture", "meshy/processing/resize"} {
		next, ok := c.Get(step)
		if !ok || !next.Enabled {
			t.Fatalf("%s is not available to follow a remesh", step)
		}
		takesRemesh := false
		for _, parameter := range next.Parameters {
			takesRemesh = takesRemesh || slices.Contains(parameter.TaskOfferings, remesh.ID)
		}
		if !takesRemesh {
			t.Errorf("%s does not take a remesh task as its source", step)
		}
	}

	decimate, ok := c.Get("tripo/geometry/decimate-triangle")
	if !ok || !decimate.Enabled || decimate.Capability != generation.Remesh {
		t.Fatalf("baked decimation unavailable: %+v", decimate)
	}
	for _, want := range []string{"带烘焙的减面", "保留贴图"} {
		if !strings.Contains(decimate.Description, want) {
			t.Errorf("decimation description omits %q: %q", want, decimate.Description)
		}
	}
	for _, name := range []string{"bake", "smart_uv"} {
		fixedOn := false
		for _, parameter := range decimate.Parameters {
			if parameter.Name == name {
				fixedOn = parameter.Fixed && string(parameter.Default) == "true"
			}
		}
		if !fixedOn {
			t.Errorf("decimation %s is not fixed on; its description promises baked textures", name)
		}
	}
}

func TestRemeshModelIdentityIsNotAWireParameter(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := c.Get("meshy/processing/remesh")
	if !ok {
		t.Fatal("missing remesh offering")
	}
	if offering.ModelID != "meshy-processing" || offering.ProviderID != "meshy" || offering.Capability != generation.Remesh {
		t.Fatalf("remesh identity changed: %#v", offering)
	}
	if offering.Revision == "2026-10-09.1" {
		t.Fatal("wire contract change must advance the offering revision")
	}
	if offering.EstimatedCredits != 5 || !offering.CreditsMetered {
		t.Fatalf("remesh cost changed: %v, metered=%v", offering.EstimatedCredits, offering.CreditsMetered)
	}
	var options map[string]any
	if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	if _, exists := options["model_parameter"]; exists {
		t.Fatal("remesh must not declare an undocumented model parameter")
	}
	if options["api_version"] != "v1" || options["endpoint"] != "remesh" {
		t.Fatalf("remesh endpoint changed: %#v", options)
	}
	for _, parameter := range offering.Parameters {
		if parameter.Name == "ai_model" || parameter.WireName == "ai_model" {
			t.Fatal("remesh must not expose ai_model as an input")
		}
	}
	// Discovery compatibility has a distinct declaration. Removing the upstream
	// field must not remove the model identity consumed by legacy discovery.
	found := false
	for _, model := range c.Application().ProviderDiscovery.LegacyModels {
		if model.ModelID != "meshy-processing" {
			continue
		}
		found = true
		if model.ProviderKey != "meshy-processing" || model.Name != "Meshy Remesh" {
			t.Fatalf("discovery identity changed: %#v", model)
		}
		if _, ok := model.ParameterProjections[generation.Remesh]; !ok {
			t.Fatal("remesh discovery projection missing")
		}
	}
	if !found {
		t.Fatal("processing discovery model missing")
	}
	for _, id := range []generation.OfferingID{"meshy/meshy-7/text-to-3d", "meshy/meshy-7/image-to-3d"} {
		generationOffering, ok := c.Get(id)
		if !ok {
			t.Fatalf("missing generation offering %s", id)
		}
		var generationOptions map[string]any
		if err := json.Unmarshal(generationOffering.ProviderOptions, &generationOptions); err != nil {
			t.Fatal(err)
		}
		if generationOptions["model_parameter"] != "ai_model" || generationOffering.ModelID != "meshy-7" {
			t.Fatalf("generation model contract changed for %s", id)
		}
	}
}
