package generationtool

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

func countOffering(id, parameter string, policy string) generation.Offering {
	count := countParameter(policy)
	count.Name = parameter
	return generation.Offering{
		ID:                            generation.OfferingID(id),
		EstimatedCreditsUnitParameter: parameter,
		Parameters:                    []schemacontract.Parameter{count},
	}
}

// #486: the person's image count is one value for the whole session, so the
// Offering's declared count parameter runs with it even when another Offering
// kept a value of its own. A concept sketch still fixes one image per task.
func TestSessionImageCountRunsTheOfferingsCountParameter(t *testing.T) {
	executor := &Executor{
		chat: catalog.ChatSchema{ConceptDirections: catalog.ChatConceptDirectionsSchema{
			Enabled: true, SketchTool: "create_concept_sketch", MaxCount: 4,
			ParameterOverrides: map[string]map[string]json.RawMessage{
				"openai/gpt-image-2/text-to-image": {"n": json.RawMessage(`1`)},
			},
		}},
		imageCount: func(ownerID, sessionID string) (int, bool) {
			return 4, sessionID == "session-1"
		},
		offeringParameters: func(ownerID, sessionID, offeringID string) map[string]json.RawMessage {
			return map[string]json.RawMessage{"n": json.RawMessage(`1`)}
		},
		offeringParameterStates: func(ownerID, sessionID, offeringID string) map[string]RememberedParameter {
			return map[string]RememberedParameter{"n": {Value: json.RawMessage(`1`), Source: "user"}}
		},
	}
	command := chat.ToolCommand{
		Tool: catalog.ChatToolSchema{Name: "refine_image"}, SessionID: "session-1",
	}
	gpt := countOffering("openai/gpt-image-2/text-to-image", "n", "user")
	if values := buildInputValues(t, executor, command, gpt); values["n"] != float64(4) {
		t.Fatalf("n = %v, want the session's 4 over the Offering's 1", values["n"])
	}

	// The Seedream Offering prices by a parameter of its own name; the same
	// session value reaches it.
	seedream := countOffering("jimeng/seedream-4-5/text-to-image", "max_images", "user")
	seedreamCommand := command
	if values := buildInputValues(t, executor, seedreamCommand, seedream); values["max_images"] != float64(4) {
		t.Fatalf("max_images = %v, want the session's 4", values["max_images"])
	}

	// A sketch stays one image per task, however the person's count is set.
	sketch := command
	sketch.Tool.Name = "create_concept_sketch"
	if values := buildInputValues(t, executor, sketch, gpt); values["n"] != float64(1) {
		t.Fatalf("sketch n = %v, want 1", values["n"])
	}

	// A session that set no count keeps the Offering's remembered value.
	other := command
	other.SessionID = "session-2"
	if values := buildInputValues(t, executor, other, gpt); values["n"] != float64(1) {
		t.Fatalf("n = %v, want the Offering's remembered 1", values["n"])
	}
}

// A fixed count parameter is the Offering's own backend value: the session's
// number must not overwrite it.
func TestSessionImageCountLeavesAFixedCountAlone(t *testing.T) {
	executor := &Executor{
		imageCount: func(ownerID, sessionID string) (int, bool) { return 4, true },
	}
	fixed := countParameter("user")
	fixed.Fixed = true
	offering := generation.Offering{
		ID:                            generation.OfferingID("openai/gpt-image-2/text-to-image"),
		EstimatedCreditsUnitParameter: "n",
		Parameters:                    []schemacontract.Parameter{fixed},
	}
	command := chat.ToolCommand{Tool: catalog.ChatToolSchema{Name: "refine_image"}}
	if values := buildInputValues(t, executor, command, offering); values["n"] != nil {
		t.Fatalf("n = %v, want the Offering's fixed value to stay out of the input", values["n"])
	}
}
