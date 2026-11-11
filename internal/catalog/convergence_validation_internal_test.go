package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

// shippedConvergenceSchema is the default Catalog's chat block. The shipped
// Catalog loads, so every bound below is the product's real declaration.
func shippedConvergenceSchema(t *testing.T) ChatSchema {
	t.Helper()
	loaded, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return loaded.Application().Chat
}

func TestConvergenceValidationAcceptsTheShippedBlocks(t *testing.T) {
	chat := shippedConvergenceSchema(t)
	if err := validateConvergence(chat); err != nil {
		t.Fatalf("the shipped #242 blocks are invalid: %v", err)
	}
}

// #242: the sketch tool is a 2D image step. A tool that declares an image
// domain but is scoped to 3D would put paid sketches into a 3D session.
func TestConceptDirectionsRequireATwoDimensionalSketchTool(t *testing.T) {
	chat := shippedConvergenceSchema(t)
	found := false
	for index := range chat.Tools {
		if chat.Tools[index].Name != chat.ConceptDirections.SketchTool {
			continue
		}
		found = true
		chat.Tools[index].Execution.AgentScope = "3d"
	}
	if !found {
		t.Fatalf("sketch tool %q is not declared", chat.ConceptDirections.SketchTool)
	}
	err := validateConceptDirections(chat)
	if err == nil || !strings.Contains(err.Error(), "2D") {
		t.Fatalf("err = %v, want a 2D scope refusal", err)
	}
}

// #242: the expanded candidates are plan steps; a candidate count above the
// plan's own max_steps could never be approved as one plan.
func TestCandidatesFitWithinThePlanStepBound(t *testing.T) {
	chat := shippedConvergenceSchema(t)
	chat.ApprovalPolicy.Budget.MaxSteps = chat.Candidates.MaxCount - 1
	err := validateCandidates(chat)
	if err == nil || !strings.Contains(err.Error(), "budget.max_steps") {
		t.Fatalf("err = %v, want the max_steps refusal", err)
	}
	chat.ApprovalPolicy.Budget.MaxSteps = chat.Candidates.MaxCount
	if err := validateCandidates(chat); err != nil {
		t.Fatalf("max_count == max_steps must be allowed: %v", err)
	}
}

// #242: a seed is replaced with a number. Text and enum parameters that happen
// to share its name must not be declared as seeds.
func TestRerollSeedsMustBeDeclaredIntegerParameters(t *testing.T) {
	chat := shippedConvergenceSchema(t)
	for _, name := range chat.Reroll.SeedParameters {
		if !declaredIntegerParameter(chat, name) {
			t.Fatalf("shipped seed %q is not an integer parameter", name)
		}
	}
	chat.Reroll.SeedParameters = []string{"prompt"}
	err := validateReroll(chat)
	if err == nil || !strings.Contains(err.Error(), "整数参数") {
		t.Fatalf("err = %v, want the integer refusal", err)
	}
	chat.Reroll.SeedParameters = []string{"model_seed"}
	if err := validateReroll(chat); err != nil {
		t.Fatalf("a declared integer seed must pass: %v", err)
	}
}

// #242: concept sketches are cheap drafts. Every override must be one the
// Offering accepts and must lower the shipped default, never raise it.
func TestConceptDirectionOverridesAreCheapDefaults(t *testing.T) {
	chat := shippedConvergenceSchema(t)
	overrides := chat.ConceptDirections.ParameterOverrides
	if len(overrides) == 0 {
		t.Fatal("concept_directions.parameter_overrides is empty; drafts would run at full price")
	}
	openai := overrides["openai/gpt-image-2/text-to-image"]
	if string(openai["quality"]) != `"low"` || string(openai["size"]) != `"1024x1024"` {
		t.Fatalf("openai draft override = %v, want quality=low size=1024x1024", openai)
	}
	gemini := overrides["gemini/gemini-3-pro-image/text-to-image"]
	if string(gemini["image_size"]) != `"1K"` {
		t.Fatalf("gemini draft override = %v, want image_size=1K", gemini)
	}
	// Validate exactly like a real submission so a rename or enum change is
	// caught here rather than at the person's first paid sketch.
	if err := validateConceptDirectionOfferings(chat.ConceptDirections, nil); err == nil {
		t.Fatal("an empty Offering map must refuse declared overrides")
	}
	loaded, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateConceptDirectionOfferings(chat.ConceptDirections, loaded.byID); err != nil {
		t.Fatalf("shipped draft overrides do not simulate: %v", err)
	}
}

// The JSON shape is part of the Schema contract: overrides are raw values.
func TestConceptDirectionOverrideDocumentShape(t *testing.T) {
	chat := shippedConvergenceSchema(t)
	encoded, err := json.Marshal(chat.ConceptDirections)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		ParameterOverrides map[string]map[string]json.RawMessage `json:"parameter_overrides"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.ParameterOverrides) != len(chat.ConceptDirections.ParameterOverrides) {
		t.Fatalf("round trip lost overrides: %v", decoded.ParameterOverrides)
	}
}