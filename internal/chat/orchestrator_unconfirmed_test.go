package chat_test

// #516: a paid call whose submission can be neither confirmed nor ruled out
// suspends the turn so a replay can find its Task. The replays are bounded by
// the Schema's Agent request timeout from the first suspension: past it, the
// turn ends failed instead of replaying the call every consent wait forever.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

type alwaysUnknownTool struct {
	mu       sync.Mutex
	calls    int
	acceptAt int
}

func (tool *alwaysUnknownTool) Execute(_ context.Context, _ chat.ToolCommand,
	observe func(chat.ToolObservation) error) (chat.ToolResult, error) {
	tool.mu.Lock()
	tool.calls++
	calls, acceptAt := tool.calls, tool.acceptAt
	tool.mu.Unlock()
	if acceptAt == 0 || calls < acceptAt {
		return chat.ToolResult{}, errors.New("submit result unknown")
	}
	if err := observe(chat.ToolObservation{Registered: true, TaskID: "accepted-task"}); err != nil {
		return chat.ToolResult{}, err
	}
	return chat.ToolResult{TaskID: "accepted-task", AgentContent: `{"result":"accepted"}`}, nil
}

func (tool *alwaysUnknownTool) count() int {
	tool.mu.Lock()
	defer tool.mu.Unlock()
	return tool.calls
}

func unconfirmedHarness(t *testing.T, tool *alwaysUnknownTool) (*chat.Service, func() *chat.Orchestrator, chat.ProcessTurn, string) {
	t.Helper()
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	schema := suspendSchema(t)
	schema.Agent.ConsentWaitTimeoutSeconds = 600
	schema.Agent.RequestTimeoutSeconds = 1
	unconfirmed := schema.Presentation.Messages.SubmissionUnconfirmed
	if unconfirmed == "" {
		t.Fatal("the Catalog declares no submission_unconfirmed text")
	}
	toolName := schema.Tools[0].Name
	agent := &scriptedSuspendAgent{initial: agents.Completion{ToolCalls: []agents.ToolCall{{
		ID: "call-a", Name: toolName, Arguments: json.RawMessage(`{"prompt":"paint"}`),
	}}}, resume: []agents.Completion{{Content: "recovered task"}}}
	makeOrchestrator := func() *chat.Orchestrator {
		orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
			Service: service, Schema: schema, Model: "configured-model", Agent: agent, Tools: tool,
		})
		if err != nil {
			t.Fatal(err)
		}
		return orchestrator
	}
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistantID := startTurnFor(t, service, "owner-a", session.ID, "draw")
	return service, makeOrchestrator,
		chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistantID}, unconfirmed
}

func TestUnconfirmedPaidCallEndsFailedPastTheRequestTimeout516(t *testing.T) {
	ctx := context.Background()
	tool := &alwaysUnknownTool{}
	service, makeOrchestrator, command, unconfirmed := unconfirmedHarness(t, tool)

	first := makeOrchestrator()
	if err := first.Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	if first.PendingConsentWaits() != 1 {
		t.Fatal("an unknown submission was not suspended for a replay")
	}
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || len(turn.Checkpoint.Unconfirmed) != 1 || turn.Checkpoint.Unconfirmed[0].CallID == "" {
		t.Fatalf("the first unknown suspension was not stored: %+v %v", turn.Checkpoint.Unconfirmed, err)
	}
	since := turn.Checkpoint.Unconfirmed[0].Since

	// A replay within the timeout (here after a restart) still suspends and
	// keeps the first suspension time.
	if err := makeOrchestrator().Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	turn, err = service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State == "configured-turn-error" || len(turn.Checkpoint.Unconfirmed) != 1 ||
		!turn.Checkpoint.Unconfirmed[0].Since.Equal(since) {
		t.Fatalf("a replay within the timeout changed the turn: %+v %v", turn, err)
	}

	time.Sleep(time.Until(since.Add(1100 * time.Millisecond)))
	if err := makeOrchestrator().Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	turn, err = service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != "configured-turn-error" {
		t.Fatalf("the turn kept replaying past the request timeout: %+v %v", turn, err)
	}
	if tool.count() != 3 {
		t.Fatalf("tool calls = %d, want 3 (no fresh submission)", tool.count())
	}
	if message := lastAssistant(t, service, "owner-a", command.SessionID); !strings.Contains(message.Content, unconfirmed) {
		t.Fatalf("the person was not told the submission is unconfirmed: %q", message.Content)
	}
	if turns, err := service.RecoverTurns(ctx); err != nil || len(turns) != 0 {
		t.Fatalf("a failed turn is still recoverable: %d %v", len(turns), err)
	}
}

// A replay that finds the Task clears the entry and finishes as before.
func TestUnconfirmedPaidCallThatIsFoundFinishes516(t *testing.T) {
	ctx := context.Background()
	tool := &alwaysUnknownTool{acceptAt: 2}
	service, makeOrchestrator, command, _ := unconfirmedHarness(t, tool)
	if err := makeOrchestrator().Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	if err := makeOrchestrator().Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != "configured-turn-done" || len(turn.Checkpoint.Unconfirmed) != 0 {
		t.Fatalf("found Task did not finish the turn: %+v %v", turn, err)
	}
	messages, err := service.ListMessages(ctx, command.OwnerID, command.SessionID)
	if err != nil || messages[len(messages)-1].ToolCalls[0].TaskID != "accepted-task" {
		t.Fatalf("the accepted Task binding was lost: %+v %v", messages, err)
	}
}
