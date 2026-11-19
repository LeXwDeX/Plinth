package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

type choicePromptRepository struct{ stubRepository }

func (choicePromptRepository) OwnsSession(context.Context, string, string) (bool, error) {
	return true, nil
}
func (choicePromptRepository) Session(_ context.Context, owner, id string) (Session, bool, error) {
	return Session{ID: id, OwnerID: owner, ApprovalMode: "manual"}, true, nil
}
func (choicePromptRepository) SessionNotes(context.Context, string, string) (string, bool, error) {
	return "", true, nil
}

type fixedChoices struct {
	ChoiceStore
	choices []Choice
}

func (store fixedChoices) ListChoices(context.Context, string, string, int) ([]Choice, error) {
	return store.choices, nil
}

// #321: the Agent reads the exact next call of a card answered on the card
// after that user message, and the open card's options in its system context.
func TestAgentRequestCarriesChoiceCards(t *testing.T) {
	schema := suspendTestSchema()
	registry, err := compileToolRegistry(schema, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	service := newChatServiceForInternal(t)
	service.repository = choicePromptRepository{}
	options := []ChoiceOption{
		{ID: "first", ProviderName: "P", ModelName: "One", Arguments: json.RawMessage(`{"offering_id":"first"}`)},
		{ID: "second", ProviderName: "P", ModelName: "Two", Arguments: json.RawMessage(`{"offering_id":"second"}`)},
	}
	answered := Choice{ID: "choice_a", ToolName: "process_model", Capability: "mesh-segmentation", State: ChoiceAnswered,
		Options: options, AnswerOptionID: "second", AnswerSource: ChoiceAnsweredOnCard, AnswerMessageID: "user-2"}
	open := Choice{ID: "choice_b", ToolName: "retexture_model", Capability: "retexture", State: ChoicePending,
		Options: options, DefaultOptionID: "first", AllowDefault: true, DefaultArguments: json.RawMessage(`{"use_default_offering":true}`)}
	service.SetChoices(fixedChoices{choices: []Choice{open, answered}}, 20)
	o := &Orchestrator{schema: schema, registry: registry, agentScope: agentscope.FromSchema(schema.AgentScope), service: service}
	history := []Message{
		{ID: "user-1", Role: schema.Roles.User, Content: "做模型分割"},
		{ID: "assistant-1", Role: schema.Roles.Assistant, Content: "请在卡片上选择"},
		{ID: "user-2", Role: schema.Roles.User, Content: "选择：P · Two"},
	}
	request := o.agentRequest(context.Background(), history, TurnOptions{AgentScope: "auto"}, "owner", "session")
	last := request.Messages[len(request.Messages)-1]
	if !strings.HasPrefix(last.Content, "选择：P · Two") || !strings.Contains(last.Content, `"chosen":{"id":"second"`) ||
		!strings.Contains(last.Content, `"tool":"process_model"`) {
		t.Fatalf("answer message = %q", last.Content)
	}
	if first := request.Messages[1].Content; strings.Contains(first, "choice_id") {
		t.Fatalf("an earlier user message carries a card: %q", first)
	}
	system := request.Messages[0].Content
	if !strings.Contains(system, `"choice_id":"choice_b"`) || !strings.Contains(system, `"use_default_offering":true`) ||
		strings.Contains(system, `"choice_id":"choice_a"`) {
		t.Fatalf("system context does not carry exactly the open card: %q", system)
	}
}

// #374: the answer to a card that names no target tool reaches the Agent as
// the chosen direction (with its action), not as a call to repeat, and the open
// card lists its options for a typed pick. A card of a kind the Schema no
// longer declares says nothing.
func TestAgentRequestCarriesNextStepCards(t *testing.T) {
	schema := suspendTestSchema()
	registry, err := compileToolRegistry(schema, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	service := newChatServiceForInternal(t)
	service.repository = choicePromptRepository{}
	options := []ChoiceOption{
		{ID: "clear_and_draw", Label: "清空功能选择，先出三视图图片", Description: "回到自由模式",
			Arguments: json.RawMessage(`{}`), Action: &ChoiceAction{Type: "clear_operation"}},
		{ID: "switch_retexture", Label: "改为换贴图", Arguments: json.RawMessage(`{}`),
			Action: &ChoiceAction{Type: "switch_operation", OperationID: "retexture"}},
		{ID: "keep", Label: "保持当前功能", Arguments: json.RawMessage(`{}`)},
	}
	answered := Choice{ID: "choice_n1", Kind: "next_step", Capability: "next_step", State: ChoiceAnswered,
		Options: options, AnswerOptionID: "clear_and_draw", AnswerSource: ChoiceAnsweredOnCard, AnswerMessageID: "user-2"}
	open := Choice{ID: "choice_n2", Kind: "next_step", Capability: "next_step", State: ChoicePending, Options: options}
	stale := Choice{ID: "choice_gone", Kind: "removed_kind", Capability: "removed_kind", State: ChoiceAnswered,
		Options: options, AnswerOptionID: "keep", AnswerSource: ChoiceAnsweredOnCard, AnswerMessageID: "user-3"}
	history := []Message{
		{ID: "user-1", Role: schema.Roles.User, Content: "把这三张图生成 3D"},
		{ID: "assistant-1", Role: schema.Roles.Assistant, Content: "请在卡片上选择"},
		{ID: "user-2", Role: schema.Roles.User, Content: "选择：清空功能选择，先出三视图图片"},
	}
	build := func(cards ...Choice) agents.Request {
		service.SetChoices(fixedChoices{choices: cards}, 20)
		o := &Orchestrator{schema: schema, registry: registry, agentScope: agentscope.FromSchema(schema.AgentScope), service: service}
		return o.agentRequest(context.Background(), history, TurnOptions{AgentScope: "auto"}, "owner", "session")
	}

	request := build(open, answered)
	last := request.Messages[len(request.Messages)-1].Content
	for _, want := range []string{
		"[Direction card answer]",
		`"chosen":{"id":"clear_and_draw","title":"清空功能选择，先出三视图图片","description":"回到自由模式","action":{"type":"clear_operation"}}`,
		`"kind":"next_step"`,
	} {
		if !strings.Contains(last, want) {
			t.Fatalf("answer message lacks %q: %q", want, last)
		}
	}
	if strings.Contains(last, `"tool"`) || strings.Contains(last, "call the tool") || strings.Contains(last, "switch_retexture") {
		t.Fatalf("a direction answer reads like a tool call or lists the other options: %q", last)
	}
	system := request.Messages[0].Content
	for _, want := range []string{"Open direction card", `"choice_id":"choice_n2"`, `"id":"switch_retexture"`,
		`"action":{"type":"switch_operation","operation_id":"retexture"}`} {
		if !strings.Contains(system, want) {
			t.Fatalf("system context lacks %q: %q", want, system)
		}
	}
	if strings.Contains(system, `"choice_id":"choice_n1"`) || strings.Contains(system, "tool named") {
		t.Fatalf("system context carries the answered card or a call template: %q", system)
	}

	history[2].ID = "user-3"
	request = build(stale)
	if last := request.Messages[len(request.Messages)-1].Content; strings.Contains(last, "choice_gone") || strings.Contains(last, "Direction card") {
		t.Fatalf("a card of an undeclared kind reached the Agent: %q", last)
	}
}

// #374: a kind that may switch functions needs the harness operations the
// service checks the switch against; NewService fails at assembly without them
// instead of refusing every switch at the first card (the application tests
// start the real assembly with the shipped Catalog, which exercises the wiring
// in app.go).
func TestChoiceActionWiringNeedsHarnessOperationsForSwitchKinds(t *testing.T) {
	switching := catalog.ChatChoiceKindSchema{ID: "next_step", Actions: []string{catalog.ChoiceActionSwitchOperation, catalog.ChoiceActionClearOperation}}
	clearing := catalog.ChatChoiceKindSchema{ID: "clear_only", Actions: []string{catalog.ChoiceActionClearOperation}}
	operations := []catalog.HarnessOperationSchema{{PublicHarnessOperation: catalog.PublicHarnessOperation{ID: "retexture"}, ToolName: "retexture_model"}}
	if err := checkChoiceActionWiring([]catalog.ChatChoiceKindSchema{switching}, nil); err == nil || !strings.Contains(err.Error(), "next_step") {
		t.Fatalf("a switch kind without operations = %v", err)
	}
	for name, test := range map[string]struct {
		kinds      []catalog.ChatChoiceKindSchema
		operations []catalog.HarnessOperationSchema
	}{
		"switch kind with operations": {[]catalog.ChatChoiceKindSchema{switching}, operations},
		"clear-only kind":             {[]catalog.ChatChoiceKindSchema{clearing}, nil},
		"no kinds":                    {nil, nil},
	} {
		if err := checkChoiceActionWiring(test.kinds, test.operations); err != nil {
			t.Errorf("%s refused: %v", name, err)
		}
	}
}
