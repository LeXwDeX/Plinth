package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestSemanticTextInputCanUseCurrentSchemaForLegacyTaskSnapshot(t *testing.T) {
	t.Parallel()
	task := generation.Task{
		Input: json.RawMessage(`{"legacy_field":"preserved prompt"}`),
		Offering: generation.Offering{
			Parameters: []generation.Parameter{{
				Name: "legacy_field",
				Type: "text",
			}},
		},
	}
	currentParameters := []generation.Parameter{{
		Name:     "legacy_field",
		Type:     "text",
		Semantic: generation.SemanticPrompt,
	}}

	if prompt := semanticTextInput(
		task.Input,
		currentParameters,
		generation.SemanticPrompt,
	); prompt != "preserved prompt" {
		t.Fatalf("prompt = %q", prompt)
	}
}
