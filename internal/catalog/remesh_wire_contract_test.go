package catalog_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

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
