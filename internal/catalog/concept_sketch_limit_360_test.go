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

// #586: the per-task image count is the Agent's call from what the person
// says, not a panel control. Each count parameter is an advised parameter:
// shown read-only, set by the Agent with a reason within the Offering's own
// declared range, and no longer written from the panel.
func TestShippedTaskImageCountsAreTheAgentsAdvisedCall(t *testing.T) {
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
		assertAgentAdvisedCount(t, loaded, id, "n", json.RawMessage(`4`), 10)
	}
	for _, id := range []string{
		"jimeng/seedream-4-5/text-to-image",
		"jimeng/seedream-4-5/image-to-image",
	} {
		assertAgentAdvisedCount(t, loaded, id, "sequential_image_generation", json.RawMessage(`"auto"`), 0)
		assertAgentAdvisedCount(t, loaded, id, "max_images", json.RawMessage(`4`), 15)
	}
}

func assertAgentAdvisedCount(t *testing.T, loaded *Catalog, id, name string, value json.RawMessage, maximum float64) {
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
	if parameter.AdjustmentPolicy != "advised" || parameter.Presentation == nil ||
		parameter.Presentation.Surface != schemacontract.SurfaceReadOnly {
		t.Fatalf("%s.%s adjustment_policy = %q, presentation = %+v", id, name, parameter.AdjustmentPolicy, parameter.Presentation)
	}
	if !offering.AgentParameterAllowed(parameter) || offering.UserParameterAllowed(parameter) {
		t.Fatalf("%s.%s authority user=%v agent=%v", id, name,
			offering.UserParameterAllowed(parameter), offering.AgentParameterAllowed(parameter))
	}
	if maximum > 0 && (parameter.Minimum == nil || *parameter.Minimum != 1 || parameter.Maximum == nil || *parameter.Maximum != maximum) {
		t.Fatalf("%s.%s range = %v..%v, want 1..%v", id, name, parameter.Minimum, parameter.Maximum, maximum)
	}
	// The Agent's write path takes the value with a reason and refuses it
	// without one; the panel's path refuses it.
	if _, err := ValidateParameterPatch(offering, map[string]json.RawMessage{name: value}, true,
		map[string]string{name: "用户要同一提示词的 4 个变体"}); err != nil {
		t.Fatalf("%s.%s refused the Agent's explained patch: %v", id, name, err)
	}
	if _, err := ValidateParameterPatch(offering, map[string]json.RawMessage{name: value}, true, nil); err == nil {
		t.Fatalf("%s.%s accepted an Agent patch without a reason", id, name)
	}
	if _, err := ValidateParameterPatch(offering, map[string]json.RawMessage{name: value}, false, nil); err == nil {
		t.Fatalf("%s.%s accepted a panel patch", id, name)
	}
}
