package chat

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
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

// fixedMultiviewSnapshot is a ready, fixed three-view-to-3D selection whose
// session also enabled retexture, remesh and the (unavailable) printability
// repair.
func fixedMultiviewSnapshot() *resolve.Snapshot {
	return &resolve.Snapshot{
		Selection: harness.Selection{OperationID: "multiview-to-3d", OfferingID: "tripo/multiview", OfferingRevision: "v1",
			EnabledOperations: []string{"multiview-to-3d", "retexture", "remesh", "repair-printability"},
			FixedParameters:   map[string]json.RawMessage{}, FillableParameters: []string{}},
		OperationID: "multiview-to-3d", ToolName: "create_model_3d_from_views",
		ToolParameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Status:         resolve.StatusReady, Input: map[string]json.RawMessage{},
	}
}

// #374: ask_choice is a base tool, so a fixed function can still open the
// next-step card with the page-performed actions; the target tools of every
// other card kind are not base tools, so those cards stay refused. The
// function context names the functions an option may switch to: wired,
// available, enabled in the selection and not the one already fixed.
func TestFixedHarnessStillOffersTheNextStepCardAndNothingElse(t *testing.T) {
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
	snapshot := fixedMultiviewSnapshot()

	fixed := o.harnessRegistry(context.WithValue(context.Background(), harnessContextKey{}, snapshot), registry)
	ask, found := fixed.byName["ask_choice"]
	if !found || !fixed.permits(ask, false) {
		t.Fatal("a fixed function hides ask_choice")
	}
	if !slices.Contains(schema.Harness.BaseTools, "ask_choice") {
		t.Fatal("ask_choice is not a Schema base tool")
	}
	targetKinds := 0
	for _, kind := range schema.Choice.Kinds {
		if kind.ToolName == "" {
			continue
		}
		targetKinds++
		target, declared := fixed.byName[kind.ToolName]
		if !declared || fixed.permits(target, false) {
			t.Errorf("kind %s: target tool %s is available under a fixed function", kind.ID, kind.ToolName)
		}
	}
	if targetKinds == 0 {
		t.Fatal("the shipped Catalog declares no kind with a target tool to compare")
	}

	request := o.agentRequest(context.Background(), nil, TurnOptions{AgentScope: "auto", Harness: snapshot}, "owner", "")
	var tool *agents.Tool
	for index := range request.Tools {
		if request.Tools[index].Name == "ask_choice" {
			tool = &request.Tools[index]
		}
	}
	if tool == nil || !strings.Contains(string(tool.Parameters), `"next_step"`) ||
		!strings.Contains(string(tool.Parameters), `"clear_operation"`) {
		t.Fatalf("the model request lacks the next-step card: %+v", tool)
	}
	data, decoded := harnessSwitchable(t, request.Messages[0].Content)
	listed := []string{}
	for _, operation := range data {
		listed = append(listed, operation.ID)
		if operation.Label == "" {
			t.Errorf("switchable operation %s has no label", operation.ID)
		}
	}
	if !decoded || !slices.Equal(listed, []string{"retexture", "remesh"}) {
		t.Fatalf("switchable operations = %v (decoded %v), want retexture and remesh", listed, decoded)
	}

	// Without the tool in the turn's list there is nothing to switch with.
	schema.Harness.BaseTools = slices.DeleteFunc(slices.Clone(schema.Harness.BaseTools), func(name string) bool { return name == "ask_choice" })
	o.schema = schema
	request = o.agentRequest(context.Background(), nil, TurnOptions{AgentScope: "auto", Harness: snapshot}, "owner", "")
	for _, tool := range request.Tools {
		if tool.Name == "ask_choice" {
			t.Fatal("ask_choice stayed after it left the base tools")
		}
	}
	if without, _ := harnessSwitchable(t, request.Messages[0].Content); len(without) != 0 {
		t.Fatalf("the function context offers switches without a card to carry them: %+v", without)
	}
}

type switchableEntry struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// harnessSwitchable reads the switchable_operations of the selection data in a
// system prompt; decoded is false when the prompt carries no such data.
func harnessSwitchable(t *testing.T, prompt string) (entries []switchableEntry, decoded bool) {
	t.Helper()
	for _, line := range strings.Split(prompt, "\n") {
		if !strings.HasPrefix(line, `{"selection":`) {
			continue
		}
		var data struct {
			Switchable []switchableEntry `json:"switchable_operations"`
		}
		if err := json.Unmarshal([]byte(line), &data); err != nil {
			t.Fatal(err)
		}
		return data.Switchable, true
	}
	return nil, false
}
