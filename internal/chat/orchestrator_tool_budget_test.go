package chat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #275: the budget counts executed tool calls in one Agent reply, not LLM
// rounds. A batch that overflows the remaining budget runs up to the limit;
// the rest is listed for the person after the tool-less summary round, which
// still sees the executed results.
func TestOrchestratorToolBudgetCountsCallsNotRounds(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	schema := budgetSchema(t, 3)
	tool := schema.Tools[0].Name
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{budgetCall("g1", tool, "a"), budgetCall("g2", tool, "b")}},
		{ToolCalls: []agents.ToolCall{budgetCall("g3", tool, "c"), budgetCall("g4", tool, "d")}},
		{Content: "configured summary"},
	}}
	tools := &recordingToolExecutor{commands: make(chan chat.ToolCommand, 8)}
	stored := runBudgetTurn(t, service, schema, agent, tools, nil)
	if executed := drainPrompts(tools); executed != "a,b,c" {
		t.Fatalf("executed = %q", executed)
	}
	if len(stored.ToolCalls) != 3 {
		t.Fatalf("stored calls = %#v", stored.ToolCalls)
	}
	for _, want := range []string{"configured summary", "limit 3", "pending:\n* " + tool + " -> d"} {
		if !strings.Contains(stored.Content, want) {
			t.Fatalf("content = %q, missing %q", stored.Content, want)
		}
	}
	if len(agent.requests) != 3 {
		t.Fatalf("agent requests = %d", len(agent.requests))
	}
	summary := agent.requests[2]
	last := summary.Messages[len(summary.Messages)-1]
	if len(summary.Tools) != 0 || last.Role != schema.Roles.Tool {
		t.Fatalf("summary request tools = %d, last message = %#v", len(summary.Tools), last)
	}
}

// The budget starts again at 0 for every new Agent reply.
func TestOrchestratorToolBudgetResetsForEachReply(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	schema := budgetSchema(t, 1)
	tool := schema.Tools[0].Name
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{budgetCall("g1", tool, "first")}},
		{ToolCalls: []agents.ToolCall{budgetCall("g2", tool, "again")}},
		{Content: "first summary"},
		{ToolCalls: []agents.ToolCall{budgetCall("g3", tool, "second")}},
		{Content: "second done"},
	}}
	tools := &recordingToolExecutor{commands: make(chan chat.ToolCommand, 8)}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	first := runBudgetTurn(t, service, schema, agent, tools, &session)
	second := runBudgetTurn(t, service, schema, agent, tools, &session)
	if executed := drainPrompts(tools); executed != "first,second" {
		t.Fatalf("executed = %q", executed)
	}
	if !strings.Contains(first.Content, "limit 1") ||
		strings.Contains(second.Content, "limit 1") ||
		!strings.Contains(second.Content, "second done") {
		t.Fatalf("first = %q, second = %q", first.Content, second.Content)
	}
}

// Resuming a persisted plan (e.g. after consent) replays its executed calls;
// they stay in the same reply's budget instead of starting a new one.
func TestOrchestratorToolBudgetCountsResumedCalls(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	schema := budgetSchema(t, 2)
	tool := schema.Tools[0].Name
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{budgetCall("g3", tool, "third")}},
		{Content: "resumed summary"},
	}}
	tools := &recordingToolExecutor{commands: make(chan chat.ToolCommand, 8)}
	stored := runBudgetTurn(t, service, schema, agent, tools, nil, func(message *chat.Message) {
		message.Content = "configured persisted plan"
		message.ToolCalls = []chat.ToolCall{
			{ID: message.ID + "_tool_0", Name: tool, Arguments: []byte(`{"prompt":"one"}`)},
			{ID: message.ID + "_r1_tool_0", Name: tool, Arguments: []byte(`{"prompt":"two"}`)},
		}
	})
	if executed := drainPrompts(tools); executed != "one,two" {
		t.Fatalf("executed = %q", executed)
	}
	if len(stored.ToolCalls) != 2 ||
		!strings.Contains(stored.Content, "limit 2") ||
		!strings.Contains(stored.Content, "* "+tool+" -> third") {
		t.Fatalf("stored = %#v", stored)
	}
}

func budgetSchema(t *testing.T, budget int) catalog.ChatSchema {
	t.Helper()
	schema := configuredChatSchema(t)
	schema.Agent.MaxToolCallsPerReply = budget
	messages := &schema.Presentation.Messages
	messages.ToolRoundLimit = "limit {limit}"
	messages.ToolRoundLimitPending = "pending:"
	messages.ToolRoundLimitPendingItem = "* {tool} -> {intent}"
	messages.ToolRoundLimitPendingTool = "* {tool}"
	return schema
}

func budgetCall(id, tool, prompt string) agents.ToolCall {
	return agents.ToolCall{ID: id, Name: tool, Arguments: []byte(`{"prompt":"` + prompt + `"}`)}
}

func runBudgetTurn(
	t *testing.T,
	service *chat.Service,
	schema catalog.ChatSchema,
	agent agents.Adapter,
	tools chat.ToolExecutor,
	session *chat.Session,
	persist ...func(*chat.Message),
) chat.Message {
	t.Helper()
	ctx := context.Background()
	if session == nil {
		created, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
		if err != nil {
			t.Fatal(err)
		}
		session = &created
	}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "draw",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range persist {
		change(&assistant)
		if err := service.UpdateMessage(ctx, "owner-a", assistant); err != nil {
			t.Fatal(err)
		}
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID:            "owner-a",
		SessionID:          session.ID,
		AssistantMessageID: assistant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	return messages[len(messages)-1]
}

func drainPrompts(tools *recordingToolExecutor) string {
	var prompts []string
	for {
		select {
		case command := <-tools.commands:
			prompts = append(prompts, strings.TrimSuffix(strings.TrimPrefix(
				string(command.Call.Arguments), `{"prompt":"`), `"}`))
		default:
			return strings.Join(prompts, ",")
		}
	}
}
