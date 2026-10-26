package chat_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

const thinkingSecret = "private reasoning that must stay hidden"

// thinkingAgent plays a reasoning model (#285): each round reports reasoning
// (content-free Thinking deltas) before its text. The first round also
// reasons again after its first visible text and then calls a tool; the
// second round summarizes. Its completions carry reasoning for passback.
type thinkingAgent struct {
	tool  string
	calls int
}

func (agent *thinkingAgent) Stream(
	_ context.Context,
	_ agents.Request,
	emit func(agents.Delta) error,
) (agents.Completion, error) {
	agent.calls++
	var deltas []agents.Delta
	completion := agents.Completion{ReasoningContent: thinkingSecret}
	if agent.calls == 1 {
		deltas = []agents.Delta{
			{Thinking: true}, {Thinking: true},
			{Content: "\n"}, // blank text does not end the status
			{Content: "configured plan"},
			{Thinking: true},
			{Content: " more"},
		}
		completion.Content = "\nconfigured plan more"
		completion.ToolCalls = []agents.ToolCall{{
			ID: "configured-call", Name: agent.tool, Arguments: []byte(`{"prompt":"paint"}`),
		}}
	} else {
		deltas = []agents.Delta{{Thinking: true}, {Content: " configured summary"}}
		completion.Content = " configured summary"
	}
	for _, delta := range deltas {
		if err := emit(delta); err != nil {
			return agents.Completion{}, err
		}
	}
	return completion, nil
}

func (*thinkingAgent) Complete(context.Context, agents.Request) (agents.Completion, error) {
	return agents.Completion{}, nil
}

func runThinkingTurn(t *testing.T, schema catalog.ChatSchema) ([]chat.Event, chat.Message) {
	t.Helper()
	service := newChatService(t, newMemoryChatRepository())
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   &thinkingAgent{tool: schema.Tools[0].Name},
		Tools:   scriptedToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	events, err := service.ListEvents(ctx, "owner-a", session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	return events, messages[1]
}

// TestOrchestratorShowsThinkingStatusPerModelRound pins #285: each model round
// opens with the Schema thinking status, repeated reasoning before text adds
// nothing, reasoning after visible text announces it again, and the round after
// a tool round shows it again after the tool status. No event and no stored
// text carries reasoning.
func TestOrchestratorShowsThinkingStatusPerModelRound(t *testing.T) {
	schema := configuredChatSchema(t)
	schema.Stream.EventTypes.Thinking = "configured-thinking"
	schema.Presentation.Status.Thinking = "configured thinking"
	schema.Stream.EventTypes.Status = "configured-status-event"
	schema.Presentation.Status.Generating = "configured generating"
	events, stored := runThinkingTurn(t, schema)

	var sequence []string
	for _, event := range events {
		kind, _ := event.Data["configured-type"].(string)
		switch kind {
		case "configured-thinking":
			if event.Data["configured-content"] != "configured thinking" {
				t.Fatalf("thinking event content = %#v", event.Data)
			}
			sequence = append(sequence, "thinking")
		case "configured-text":
			if content, _ := event.Data["configured-content"].(string); strings.TrimSpace(content) != "" {
				sequence = append(sequence, "text:"+strings.TrimSpace(content))
			}
		case "configured-status-event":
			sequence = append(sequence, "status")
		case "configured-done":
			sequence = append(sequence, "done")
		}
		encoded, err := json.Marshal(event.Data)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), thinkingSecret) {
			t.Fatalf("event carries reasoning: %s", encoded)
		}
	}
	want := []string{
		"thinking", "text:configured plan", "thinking", "text:more",
		"status", "thinking", "text:configured summary", "done",
	}
	if strings.Join(sequence, "|") != strings.Join(want, "|") {
		t.Fatalf("sequence = %v, want %v", sequence, want)
	}
	if strings.Contains(stored.Content, thinkingSecret) {
		t.Fatalf("stored message carries reasoning: %q", stored.Content)
	}
}

// TestOrchestratorThinkingStatusIsOptional pins that a Schema without the
// thinking event streams as before.
func TestOrchestratorThinkingStatusIsOptional(t *testing.T) {
	schema := configuredChatSchema(t)
	schema.Stream.EventTypes.Thinking = ""
	schema.Presentation.Status.Thinking = ""
	events, _ := runThinkingTurn(t, schema)
	for _, event := range events {
		if event.Data["configured-type"] == "thinking" || event.Data["configured-content"] == "思考中…" {
			t.Fatalf("unexpected thinking event: %#v", event.Data)
		}
	}
}
