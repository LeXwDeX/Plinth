package chat_test

import (
	"context"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #403: a turn that has not ended is reported by the message reads themselves.
// Before, only the placeholder text made an assistant message "pending", and the
// Orchestrator stores the real text and tool calls after the first round, so a
// reader of a turn in its second round saw a reply that looked finished.

// activeTurnFixture starts a turn whose assistant message already holds the
// real text and a tool call of a finished first round (what the Orchestrator
// stores between rounds) and returns the pieces a read test needs.
func activeTurnFixture(t *testing.T) (*chat.Service, *memoryChatRepository, chat.Session, chat.Message) {
	t.Helper()
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "draw twice",
	})
	if err != nil {
		t.Fatal(err)
	}
	assistant.Content = "first round narration"
	assistant.ToolCalls = []chat.ToolCall{{
		ID:        "call-a",
		Name:      "tool-a",
		Arguments: []byte(`{"configured-prompt":"paint a castle"}`),
	}}
	if err := service.UpdateMessage(ctx, "owner-a", assistant); err != nil {
		t.Fatal(err)
	}
	return service, repository, session, assistant
}

func TestListMessagesMarksTheAssistantOfAnUnfinishedTurn403(t *testing.T) {
	for name, test := range map[string]struct {
		state   string
		running bool
	}{
		"queued":       {"configured-turn-queued", true},
		"running":      {"configured-turn-running", true},
		"waiting task": {"configured-turn-waiting-task", true},
		"done":         {"configured-turn-done", false},
		"error":        {"configured-turn-error", false},
	} {
		t.Run(name, func(t *testing.T) {
			service, repository, session, assistant := activeTurnFixture(t)
			ctx := context.Background()
			if err := repository.UpdateTurnState(ctx, assistant.ID, test.state, "", time.Now()); err != nil {
				t.Fatal(err)
			}

			messages, err := service.ListMessages(ctx, "owner-a", session.ID)
			if err != nil {
				t.Fatal(err)
			}
			page, err := service.ListMessagesPage(ctx, "owner-a", session.ID, chat.MessageListQuery{Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			for source, list := range map[string][]chat.Message{"list": messages, "page": page.Messages} {
				if len(list) != 2 {
					t.Fatalf("%s = %#v", source, list)
				}
				if list[0].TurnActive {
					t.Fatalf("%s: the user message is never a running reply: %#v", source, list[0])
				}
				if list[1].TurnActive != test.running {
					t.Fatalf("%s: TurnActive = %v for a %s turn, want %v", source, list[1].TurnActive, name, test.running)
				}
				// The mark is a projection: the stored text and calls are what they were.
				if list[1].Content != "first round narration" || len(list[1].ToolCalls) != 1 {
					t.Fatalf("%s: the reply changed: %#v", source, list[1])
				}
			}
		})
	}
}

func TestTheUnfinishedTurnMarkStaysInsideItsOwnerAndSession403(t *testing.T) {
	service, repository, running, assistant := activeTurnFixture(t)
	ctx := context.Background()
	if err := repository.UpdateTurnState(ctx, assistant.ID, "configured-turn-running", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	// Another session of the same owner, whose turn has ended.
	other, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	finished, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: other.ID, Content: "done already"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateTurnState(ctx, finished.ID, "configured-turn-done", "", time.Now()); err != nil {
		t.Fatal(err)
	}

	messages, err := service.ListMessages(ctx, "owner-a", other.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if message.TurnActive {
			t.Fatalf("a running turn of another session marked this one: %#v", message)
		}
	}

	// A foreign owner reads nothing of the running session, so nothing of its state leaks.
	if _, err := service.ListMessages(ctx, "owner-b", running.ID); err == nil {
		t.Fatal("a foreign owner read a running session")
	}
	if _, err := service.ListMessagesPage(ctx, "owner-b", running.ID, chat.MessageListQuery{Limit: 10}); err == nil {
		t.Fatal("a foreign owner read a page of a running session")
	}
}

// A user-visible reply is read while the Agent works on its second round: the
// first round's text and tool call are stored, the placeholder is gone, and the
// reply must still be reported as running until the turn ends.
func TestAMultiRoundTurnIsRunningWhileItsSecondRoundRuns403(t *testing.T) {
	schema := configuredChatSchema(t)
	secondRoundEntered := make(chan struct{})
	releaseSecondRound := make(chan struct{})
	streams := []func(ctx context.Context, emit func(agents.Delta) error) (agents.Completion, error){
		func(_ context.Context, _ func(agents.Delta) error) (agents.Completion, error) {
			return agents.Completion{
				Content: "let me draw it",
				ToolCalls: []agents.ToolCall{{
					Name:      schema.Tools[0].Name,
					Arguments: []byte(`{"prompt":"paint"}`),
				}},
			}, nil
		},
		func(_ context.Context, _ func(agents.Delta) error) (agents.Completion, error) {
			close(secondRoundEntered)
			<-releaseSecondRound
			return agents.Completion{Content: " and here it is"}, nil
		},
	}
	orchestrator, service, _ := newScriptedStreamOrchestrator(t, schema, streams, scriptedToolExecutor{}, nil)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	processDone := make(chan error, 1)
	go func() {
		processDone <- orchestrator.Process(ctx, chat.ProcessTurn{
			OwnerID:            "owner-a",
			SessionID:          session.ID,
			AssistantMessageID: assistant.ID,
		})
	}()
	select {
	case <-secondRoundEntered:
	case err := <-processDone:
		t.Fatalf("the turn ended before its second round: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the second round never started")
	}

	midway, err := service.ListMessagesPage(ctx, "owner-a", session.ID, chat.MessageListQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(midway.Messages) != 2 {
		t.Fatalf("messages = %#v", midway.Messages)
	}
	reply := midway.Messages[1]
	if reply.Content == "configured-pending" || reply.Content == "" || len(reply.ToolCalls) == 0 {
		t.Fatalf("the first round was not stored before the second one: %#v", reply)
	}
	if !reply.TurnActive {
		t.Fatalf("a reply whose second round is running was not reported as running: %#v", reply)
	}

	close(releaseSecondRound)
	if err := <-processDone; err != nil {
		t.Fatal(err)
	}
	finished, err := service.ListMessagesPage(ctx, "owner-a", session.ID, chat.MessageListQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if finished.Messages[1].TurnActive {
		t.Fatalf("a finished turn is still reported as running: %#v", finished.Messages[1])
	}
	if finished.Messages[1].Content == "" {
		t.Fatalf("the finished reply lost its text: %#v", finished.Messages[1])
	}
}

func TestTheSemanticProjectionReportsAnUnfinishedTurnAsPendingWithItsText403(t *testing.T) {
	policy := chat.ProjectionPolicy{PendingContent: "configured-pending"}
	running := chat.ProjectMessage(policy, chat.Message{
		ID:         "reply",
		Role:       "configured-assistant",
		Content:    "first round narration",
		TurnActive: true,
		CreatedAt:  time.Unix(1, 0).UTC(),
	})
	if !running.IsPending || running.Content != "first round narration" {
		t.Fatalf("a running reply keeps its text and is pending: %#v", running)
	}
	finished := chat.ProjectMessage(policy, chat.Message{
		ID:        "reply",
		Role:      "configured-assistant",
		Content:   "first round narration",
		CreatedAt: time.Unix(1, 0).UTC(),
	})
	if finished.IsPending {
		t.Fatalf("a finished reply is not pending: %#v", finished)
	}
}
