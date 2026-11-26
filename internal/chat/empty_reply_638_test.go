package chat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #638: the follow-up round after the tool results answered with nothing (no
// text, no call; the adapter layer already retried it). The reply keeps what
// the first round said and adds the Schema's text that the model returned
// nothing, with the error reference; the turn ends with the empty_response
// reason in the done state, not as completed.
func TestAnEmptyFollowUpEndsTheTurnWithTheEmptyResponseReason(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	schema := configuredChatSchema(t)
	empty := schema.Presentation.Messages.AgentEmptyResponse
	if empty == "" {
		t.Fatal("the default Schema declares no agent_empty_response text")
	}
	reference := strings.SplitN(schema.Presentation.Messages.ErrorReference, "{id}", 2)[0]
	agent := &scriptedAgent{responses: []agents.Completion{
		{Content: "I split it in three.", ToolCalls: []agents.ToolCall{budgetCall("g1", schema.Tools[0].Name, "a")}},
		{ReasoningContent: "only thought"},
	}}
	tools := &recordingToolExecutor{commands: make(chan chat.ToolCommand, 8)}
	stored := runBudgetTurn(t, service, schema, agent, tools, nil)
	if !strings.HasPrefix(stored.Content, "I split it in three.") || !strings.Contains(stored.Content, empty) ||
		!strings.Contains(stored.Content, reference) {
		t.Fatalf("assistant = %q", stored.Content)
	}
	turn, err := service.GetTurn(context.Background(), "owner-a", stored.SessionID, stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantTurnEnd(t, turn, "configured-turn-done", configuredEndReasons.EmptyResponse)
}

// A first round that answered with nothing says so instead of "no result",
// and ends the same way.
func TestAnEmptyFirstRoundEndsTheTurnWithTheEmptyResponseReason(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	schema := configuredChatSchema(t)
	stored := runBudgetTurn(t, service, schema, &scriptedAgent{responses: []agents.Completion{{}}}, noToolExecutor{}, nil)
	if !strings.HasPrefix(stored.Content, schema.Presentation.Messages.AgentEmptyResponse) {
		t.Fatalf("assistant = %q", stored.Content)
	}
	turn, err := service.GetTurn(context.Background(), "owner-a", stored.SessionID, stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantTurnEnd(t, turn, "configured-turn-done", configuredEndReasons.EmptyResponse)
}

// A follow-up that drew its own picture instead of calling a tool (also after
// its retry) keeps the reply so far and says so, instead of ending without a
// word.
func TestAFollowUpThatDrewItsOwnPictureSaysSo(t *testing.T) {
	schema := configuredChatSchema(t)
	agent := &breaksAfterToolAgent{tool: schema.Tools[0].Name, opening: "Working on it.", err: agents.ErrInlineImageOutput}
	content := runFailingTurnWith(t, schema, agent)
	if !strings.HasPrefix(content, "Working on it.") ||
		!strings.Contains(content, schema.Presentation.Messages.AgentInlineImageOutput) {
		t.Fatalf("assistant = %q", content)
	}
}

// #638: the rounds after tool results think at the level the model family
// declares for tool_followup (gpt-astra: medium) while the first round keeps
// the turn's level (its default high). A level the person chose for the
// session is kept for every round, the default one included.
func TestFollowUpRoundsThinkAtTheDeclaredLevelUnlessThePersonChose(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		chosen          string
		first, followUp string
	}{
		{"", "high", "medium"},
		{"xhigh", "xhigh", "xhigh"},
		{"high", "high", "high"},
	} {
		fixture := newContextFixture(t, chat.ModelPolicy{Model: "gpt-astra"})
		if test.chosen != "" {
			if _, err := fixture.service.SetSessionReasoning(ctx, "owner-a", fixture.session, test.chosen); err != nil {
				t.Fatal(err)
			}
		}
		agent := &scriptedAgent{responses: []agents.Completion{
			{Content: "checking", ToolCalls: []agents.ToolCall{budgetCall("g1", fixture.schema.Tools[0].Name, "a")}},
			{Content: "next", ToolCalls: []agents.ToolCall{budgetCall("g2", fixture.schema.Tools[0].Name, "b")}},
			{Content: "done"},
		}, completions: []agents.Completion{{Content: "title"}}}
		orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
			Service: fixture.service, Schema: fixture.schema, Model: "gpt-astra", Agent: agent,
			Tools: &recordingToolExecutor{commands: make(chan chat.ToolCommand, 8)},
		})
		if err != nil {
			t.Fatal(err)
		}
		assistant, err := fixture.service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: fixture.session, Content: "draw"})
		if err != nil {
			t.Fatal(err)
		}
		if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: fixture.session,
			AssistantMessageID: assistant.ID}); err != nil {
			t.Fatal(err)
		}
		orchestrator.WaitForTitles()
		agent.mu.Lock()
		requests := append([]agents.Request(nil), agent.requests...)
		agent.mu.Unlock()
		var streamed []string
		for _, request := range requests {
			if request.Purpose == "" && len(request.Tools) > 0 {
				streamed = append(streamed, request.ReasoningEffort)
			}
		}
		if len(streamed) != 3 || streamed[0] != test.first || streamed[1] != test.followUp || streamed[2] != test.followUp {
			t.Fatalf("chosen %q: round efforts = %v, want %s then %s", test.chosen, streamed, test.first, test.followUp)
		}
		turns, _ := fixture.repository.ListTurns(ctx, nil)
		for _, turn := range turns {
			if turn.Options.FollowUpReasoningEffort != test.followUp {
				t.Fatalf("chosen %q: frozen follow-up level = %q", test.chosen, turn.Options.FollowUpReasoningEffort)
			}
		}
	}
}
