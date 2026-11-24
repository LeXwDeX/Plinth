package chat_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #553: a reply has no tool call limit. Well past the old limit of 100 calls
// every call runs, the model keeps its tools, no limit text is appended and the
// turn ends as completed once the model stops calling tools.
func TestOrchestratorRunsToolCallsBeyondTheOldLimit(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	schema := configuredChatSchema(t)
	tool := schema.Tools[0].Name
	const rounds, perRound = 3, 50
	var responses []agents.Completion
	var want []string
	for round := range rounds {
		var calls []agents.ToolCall
		for index := range perRound {
			prompt := fmt.Sprintf("r%dc%d", round, index)
			calls = append(calls, budgetCall(prompt, tool, prompt))
			want = append(want, prompt)
		}
		responses = append(responses, agents.Completion{ToolCalls: calls})
	}
	responses = append(responses, agents.Completion{Content: "configured done"})
	agent := &scriptedAgent{responses: responses}
	tools := &recordingToolExecutor{commands: make(chan chat.ToolCommand, rounds*perRound)}
	stored := runBudgetTurn(t, service, schema, agent, tools, nil)
	if executed := drainPrompts(tools); executed != strings.Join(want, ",") {
		t.Fatalf("executed %d calls, want %d", len(strings.Split(executed, ",")), len(want))
	}
	if len(stored.ToolCalls) != rounds*perRound || !strings.Contains(stored.Content, "configured done") {
		t.Fatalf("stored %d calls, content %q", len(stored.ToolCalls), stored.Content)
	}
	if len(agent.requests) != rounds+1 {
		t.Fatalf("agent requests = %d", len(agent.requests))
	}
	for index, request := range agent.requests {
		if len(request.Tools) == 0 {
			t.Fatalf("request %d was sent without tools", index)
		}
	}
	turn, err := service.GetTurn(context.Background(), "owner-a", stored.SessionID, stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantTurnEnd(t, turn, "configured-turn-done", configuredEndReasons.Completed)
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
