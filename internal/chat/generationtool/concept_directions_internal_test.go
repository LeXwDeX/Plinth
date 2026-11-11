package generationtool

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

func conceptExecutor() *Executor {
	return &Executor{chat: catalog.ChatSchema{ConceptDirections: catalog.ChatConceptDirectionsSchema{
		Enabled: true, MinCount: 3, MaxCount: 3, SketchTool: "create_concept_sketch",
		LimitReached: "最多 {maximum} 个方向",
		ParameterOverrides: map[string]map[string]json.RawMessage{
			"image-cheap": {"size": json.RawMessage(`"1024x1024"`), "fixed_quality": json.RawMessage(`"low"`)},
		},
	}}}
}

func conceptCommand() chat.ToolCommand {
	return chat.ToolCommand{
		Call: agents.ToolCall{ID: "call-1", Name: "create_concept_sketch"},
		Tool: catalog.ChatToolSchema{Name: "create_concept_sketch"},
	}
}

func TestConceptSketchLimitCountsTheReplysEarlierCalls(t *testing.T) {
	executor := conceptExecutor()
	command := conceptCommand()
	if refusal := executor.conceptSketchLimit(command); refusal != "" {
		t.Fatalf("first sketch refused: %q", refusal)
	}
	command.EarlierCalls = []chat.ToolCall{{Name: "create_concept_sketch"}, {Name: "create_concept_sketch"}}
	if refusal := executor.conceptSketchLimit(command); refusal != "" {
		t.Fatalf("third sketch refused: %q", refusal)
	}
	command.EarlierCalls = append(command.EarlierCalls, chat.ToolCall{Name: "create_concept_sketch"})
	if refusal := executor.conceptSketchLimit(command); refusal != "最多 3 个方向" {
		t.Fatalf("fourth sketch refusal = %q", refusal)
	}
	other := conceptCommand()
	other.Tool.Name = "refine_image"
	other.EarlierCalls = command.EarlierCalls
	if refusal := executor.conceptSketchLimit(other); refusal != "" {
		t.Fatalf("another tool refused: %q", refusal)
	}
}

func TestConceptSketchParameterValuesOnlyApplyDeclaredPublicParameters(t *testing.T) {
	executor := conceptExecutor()
	offering := generation.Offering{ID: "image-cheap", Parameters: []schemacontract.Parameter{
		{Name: "size", Type: "select"},
		{Name: "fixed_quality", Type: "select", Fixed: true},
	}}
	values := executor.conceptSketchParameterValues(conceptCommand(), offering)
	if string(values["size"]) != `"1024x1024"` {
		t.Fatalf("values = %v", values)
	}
	if _, present := values["fixed_quality"]; present {
		t.Fatalf("a fixed parameter was applied: %v", values)
	}
	// An Offering the table does not name is used as is.
	if values := executor.conceptSketchParameterValues(conceptCommand(), generation.Offering{ID: "other"}); len(values) != 0 {
		t.Fatalf("unlisted offering values = %v", values)
	}
}
