package catalog

import (
	"encoding/json"
	"strings"
	"testing"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// #360: a concept sketch is exactly one image per task. The shipped Sketch
// parameters must say so for every Offering the flow can sketch with, whatever
// the panel or the Agent asks for.
func TestShippedConceptSketchesFixOneImagePerTask(t *testing.T) {
	loaded, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	overrides := loaded.Application().Chat.ConceptDirections.ParameterOverrides
	for id, parameters := range map[string]map[string]string{
		"openai/gpt-image-2/text-to-image":             {"n": `1`},
		"openai/gpt-image-2/image-to-image":            {"n": `1`},
		"openai/gpt-image-2.5-sunburst/text-to-image":  {"n": `1`},
		"openai/gpt-image-2.5-sunburst/image-to-image": {"n": `1`},
		"jimeng/seedream-4-5/text-to-image": {
			"sequential_image_generation": `"disabled"`,
			"max_images":                  `null`,
		},
		"jimeng/seedream-4-5/image-to-image": {
			"sequential_image_generation": `"disabled"`,
			"max_images":                  `null`,
		},
	} {
		for name, want := range parameters {
			got, exists := overrides[id][name]
			if !exists {
				t.Fatalf("%s 草图未固定 %s", id, name)
			}
			if trimmed := strings.TrimSpace(string(got)); trimmed != want {
				t.Fatalf("%s 草图 %s = %s，want %s", id, name, trimmed, want)
			}
		}
	}
}

// #360: the per-task image count belongs to the person alone. The three count
// parameters are declared with the existing adjustment_policy mechanism, so
// the Agent can neither set_params them nor receive them at execution time.
func TestShippedTaskImageCountsAreUserOnly(t *testing.T) {
	loaded, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{
		"openai/gpt-image-2/text-to-image",
		"openai/gpt-image-2/image-to-image",
		"openai/gpt-image-2.5-sunburst/text-to-image",
		"openai/gpt-image-2.5-sunburst/image-to-image",
	} {
		assertUserOnlyParameter(t, loaded, id, "n", json.RawMessage(`2`))
	}
	for _, id := range []string{
		"jimeng/seedream-4-5/text-to-image",
		"jimeng/seedream-4-5/image-to-image",
	} {
		assertUserOnlyParameter(t, loaded, id, "sequential_image_generation", json.RawMessage(`"auto"`))
		assertUserOnlyParameter(t, loaded, id, "max_images", json.RawMessage(`2`))
	}
}

func assertUserOnlyParameter(t *testing.T, loaded *Catalog, id, name string, value json.RawMessage) {
	t.Helper()
	offering, found := loaded.Get(schemacontract.OfferingID(id))
	if !found {
		t.Fatalf("Offering %q 不存在", id)
	}
	var parameter schemacontract.Parameter
	found = false
	for _, candidate := range offering.Parameters {
		if candidate.Name == name {
			parameter, found = candidate, true
			break
		}
	}
	if !found {
		t.Fatalf("Offering %q 未声明 %q", id, name)
	}
	if parameter.AdjustmentPolicy != "user" {
		t.Fatalf("%s.%s adjustment_policy = %q", id, name, parameter.AdjustmentPolicy)
	}
	if offering.AgentParameterAllowed(parameter) || !offering.UserParameterAllowed(parameter) {
		t.Fatalf("%s.%s authority user=%v agent=%v", id, name,
			offering.UserParameterAllowed(parameter), offering.AgentParameterAllowed(parameter))
	}
	// The existing patch validation is the Agent's write path: it refuses
	// the value, while the person's path still normalizes it.
	if _, err := ValidateParameterPatch(offering, map[string]json.RawMessage{name: value}, true, nil); err == nil {
		t.Fatalf("%s.%s accepted an Agent patch", id, name)
	}
	if _, err := ValidateParameterPatch(offering, map[string]json.RawMessage{name: value}, false, nil); err != nil {
		t.Fatalf("%s.%s refused the person's patch: %v", id, name, err)
	}
}
