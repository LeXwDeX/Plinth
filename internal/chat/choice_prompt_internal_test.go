package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agentscope"
)

type choicePromptRepository struct{ stubRepository }

func (choicePromptRepository) OwnsSession(context.Context, string, string) (bool, error) {
	return true, nil
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
