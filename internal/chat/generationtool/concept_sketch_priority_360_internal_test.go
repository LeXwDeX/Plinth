package generationtool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

func sketchOffering(id string, parameters ...schemacontract.Parameter) generation.Offering {
	return generation.Offering{ID: generation.OfferingID(id), Parameters: parameters}
}

func countParameter(policy string) schemacontract.Parameter {
	return schemacontract.Parameter{
		Name: "n", Type: "number", Integer: true, Minimum: pointerFloat(1), Maximum: pointerFloat(10),
		Presentation: &schemacontract.ParameterPresentation{Surface: schemacontract.SurfaceProvider, Control: schemacontract.ControlNumber},
		Default:      json.RawMessage(`1`), AdjustmentPolicy: policy,
	}
}

func pointerFloat(value float64) *float64 { return &value }

func buildInputValues(t *testing.T, executor *Executor, command chat.ToolCommand, offering generation.Offering) map[string]any {
	t.Helper()
	raw, _, err := executor.buildInput(context.Background(), command, offering,
		map[string]json.RawMessage{}, "draw", nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatal(err)
	}
	return values
}

// #360: a concept sketch fixes one image per task even when the person's panel
// or the Agent's remembered setting ask for four. A non-sketch call keeps the
// ordinary layer order.
func TestConceptSketchOverridesBeatThePanelAndAgentLayers(t *testing.T) {
	executor := &Executor{chat: catalog.ChatSchema{ConceptDirections: catalog.ChatConceptDirectionsSchema{
		Enabled: true, SketchTool: "create_concept_sketch", MaxCount: 4,
		ParameterOverrides: map[string]map[string]json.RawMessage{
			"openai/gpt-image-2/text-to-image": {"n": json.RawMessage(`1`)},
		},
	}}}
	offering := sketchOffering("openai/gpt-image-2/text-to-image", countParameter("user"))
	command := chat.ToolCommand{
		Tool: catalog.ChatToolSchema{Name: "create_concept_sketch"},
		Options: chat.TurnOptions{Parameters: map[string]json.RawMessage{
			"n": json.RawMessage(`4`),
		}},
		ExplicitParameters: map[string]json.RawMessage{"n": json.RawMessage(`4`)},
	}
	// Three chosen directions, three paid images in total: each sketch is one
	// image however the panel was set.
	total := 0.0
	for index := 0; index < 3; index++ {
		values := buildInputValues(t, executor, command, offering)
		if values["n"] != float64(1) {
			t.Fatalf("sketch %d n = %v, want 1", index, values["n"])
		}
		total += values["n"].(float64)
	}
	if total != 3 {
		t.Fatalf("three directions billed %v images, want 3", total)
	}

	refine := command
	refine.Tool.Name = "refine_image"
	if values := buildInputValues(t, executor, refine, offering); values["n"] != float64(4) {
		t.Fatalf("refine n = %v, want the panel's 4", values["n"])
	}
}

// #360: the Seedream sketch turns image-set generation off and carries no
// max_images, whatever the panel or the Agent set.
func TestSeedreamConceptSketchCarriesNoImageSetCount(t *testing.T) {
	executor := &Executor{chat: catalog.ChatSchema{ConceptDirections: catalog.ChatConceptDirectionsSchema{
		Enabled: true, SketchTool: "create_concept_sketch", MaxCount: 4,
		ParameterOverrides: map[string]map[string]json.RawMessage{
			"jimeng/seedream-4-5/text-to-image": {
				"sequential_image_generation": json.RawMessage(`"disabled"`),
				"max_images":                  json.RawMessage(`null`),
			},
		},
	}}}
	offering := sketchOffering("jimeng/seedream-4-5/text-to-image",
		schemacontract.Parameter{Name: "sequential_image_generation", Type: "select", Default: json.RawMessage(`"disabled"`),
			Options:          []schemacontract.ParameterOption{{Value: "disabled"}, {Value: "auto"}},
			Presentation:     &schemacontract.ParameterPresentation{Surface: schemacontract.SurfaceProvider, Control: schemacontract.ControlSelect},
			AdjustmentPolicy: "user"},
		schemacontract.Parameter{Name: "max_images", Type: "number", Integer: true,
			Minimum: pointerFloat(1), Maximum: pointerFloat(15), Default: json.RawMessage(`1`),
			Presentation:     &schemacontract.ParameterPresentation{Surface: schemacontract.SurfaceProvider, Control: schemacontract.ControlNumber},
			AdjustmentPolicy: "user"},
	)
	command := chat.ToolCommand{
		Tool: catalog.ChatToolSchema{Name: "create_concept_sketch"},
		Options: chat.TurnOptions{Parameters: map[string]json.RawMessage{
			"sequential_image_generation": json.RawMessage(`"auto"`),
			"max_images":                  json.RawMessage(`5`),
		}},
		ExplicitParameters: map[string]json.RawMessage{
			"sequential_image_generation": json.RawMessage(`"auto"`),
			"max_images":                  json.RawMessage(`5`),
		},
	}
	values := buildInputValues(t, executor, command, offering)
	if values["sequential_image_generation"] != "disabled" {
		t.Fatalf("sequential_image_generation = %v", values["sequential_image_generation"])
	}
	if _, exists := values["max_images"]; exists {
		t.Fatalf("max_images leaked into the sketch: %v", values["max_images"])
	}
}

// #360: the remembered store keeps both layers. A person's user-only value
// applies; a stale Agent value for the same parameter does not.
func TestUserOnlyTaskImageCountsFollowThePersonNotTheAgent(t *testing.T) {
	panel := map[string]json.RawMessage{"n": json.RawMessage(`2`)}
	executor := &Executor{
		offeringParameters: func(_, _, _ string) map[string]json.RawMessage {
			return map[string]json.RawMessage{"n": json.RawMessage(`5`)}
		},
		offeringParameterStates: func(_, _, _ string) map[string]RememberedParameter {
			return map[string]RememberedParameter{"n": {Value: json.RawMessage(`5`), Source: "agent"}}
		},
	}
	offering := sketchOffering("openai/gpt-image-2/text-to-image", countParameter("user"))
	command := chat.ToolCommand{
		Tool:               catalog.ChatToolSchema{Name: "refine_image"},
		Options:            chat.TurnOptions{Parameters: panel},
		ExplicitParameters: panel,
	}
	if values := buildInputValues(t, executor, command, offering); values["n"] != float64(2) {
		t.Fatalf("agent value leaked into n = %v", values["n"])
	}

	// The person's remembered value is applied when the panel left it unset.
	executor.offeringParameterStates = func(_, _, _ string) map[string]RememberedParameter {
		return map[string]RememberedParameter{"n": {Value: json.RawMessage(`5`), Source: "user"}}
	}
	unset := chat.ToolCommand{
		Tool:               catalog.ChatToolSchema{Name: "refine_image"},
		ExplicitParameters: map[string]json.RawMessage{},
	}
	if values := buildInputValues(t, executor, unset, offering); values["n"] != float64(5) {
		t.Fatalf("the person's remembered n = %v, want 5", values["n"])
	}
}

// #360: set_params without an offering id merges Agent values into the turn
// options; a user-only parameter must not take them.
func TestAgentMergedOptionsCannotRaiseTheTaskImageCount(t *testing.T) {
	executor := &Executor{}
	offering := sketchOffering("openai/gpt-image-2/text-to-image", countParameter("user"))
	command := chat.ToolCommand{
		Tool:               catalog.ChatToolSchema{Name: "refine_image"},
		Options:            chat.TurnOptions{Parameters: map[string]json.RawMessage{"n": json.RawMessage(`5`)}},
		ExplicitParameters: map[string]json.RawMessage{},
	}
	if values := buildInputValues(t, executor, command, offering); values["n"] != nil {
		t.Fatalf("agent-only n reached the input: %v", values["n"])
	}
}
