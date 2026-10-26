package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
)

func TestAgentRequestUsesFrozenHarnessOperation(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := loaded.Application().Chat
	registry, err := compileToolRegistry(schema, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{schema: schema, registry: registry, agentScope: agentscope.FromSchema(schema.AgentScope)}
	snapshot := &resolve.Snapshot{Selection: harness.Selection{OperationID: "remesh", OfferingID: "chosen-offering", OfferingRevision: "v1", FixedParameters: map[string]json.RawMessage{"face_count": json.RawMessage(`1000`)}, FillableParameters: []string{}}, OperationID: "remesh", ToolName: "process_model", ToolParameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`), Status: resolve.StatusReady, Input: map[string]json.RawMessage{}}
	request := o.agentRequest(context.Background(), nil, TurnOptions{AgentScope: "auto", Harness: snapshot}, "owner", "")
	if !strings.Contains(request.Messages[0].Content, `"operation_id":"remesh"`) || !strings.Contains(request.Messages[0].Content, `"offering_id":"chosen-offering"`) {
		t.Fatal("actual model request does not carry the selected operation")
	}
	allowed := map[string]bool{"process_model": true}
	for _, name := range schema.Harness.BaseTools {
		allowed[name] = true
	}
	seen := map[string]bool{}
	for _, tool := range request.Tools {
		if !allowed[tool.Name] {
			t.Errorf("unselected executable tool %q leaked", tool.Name)
		}
		seen[tool.Name] = true
	}
	if !seen["process_model"] {
		t.Fatal("selected executable tool absent")
	}
	snapshot.Status = resolve.StatusNeedsInput
	request = o.agentRequest(context.Background(), nil, TurnOptions{AgentScope: "auto", Harness: snapshot}, "owner", "")
	for _, tool := range request.Tools {
		if tool.Name == "process_model" {
			t.Fatal("missing source still exposes execution")
		}
	}
}

// #187 D: a turn that only lacks a parameter keeps its target tool, so a call
// gets the missing input named; the model also learns which inputs come from
// pictures in the conversation.
func TestAgentRequestKeepsToolWhenOnlyParametersAreMissing(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := loaded.Application().Chat
	registry, err := compileToolRegistry(schema, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{schema: schema, registry: registry, agentScope: agentscope.FromSchema(schema.AgentScope)}
	snapshot := &resolve.Snapshot{Selection: harness.Selection{OperationID: "retexture", OfferingID: "hyper3d/texture/local-model", OfferingRevision: "v1", FixedParameters: map[string]json.RawMessage{}, FillableParameters: []string{}},
		OperationID: "retexture", ToolName: "retexture_model", ToolParameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Status: resolve.StatusNeedsInput, CallableWithMissing: true, MissingParameters: []string{"prompt"}, ConversationParameters: []string{"reference_image"}, Input: map[string]json.RawMessage{}}
	request := o.agentRequest(context.Background(), nil, TurnOptions{AgentScope: "auto", Harness: snapshot}, "owner", "")
	found := false
	for _, tool := range request.Tools {
		found = found || tool.Name == "retexture_model"
	}
	if !found {
		t.Fatal("a parameter gap hid the selected tool")
	}
	if !strings.Contains(request.Messages[0].Content, `"conversation_parameters":["reference_image"]`) {
		t.Fatalf("model context lacks the conversation inputs: %s", request.Messages[0].Content)
	}
}
