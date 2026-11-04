package chat_test

import (
	"context"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// A turn command can reach the queue twice: Run's startup recovery and the
// waiting scanner both read queued turns, and the in-flight dedup only holds
// while the first copy runs. A copy dequeued after the turn finished must
// not reopen it or call the Agent again (#90).
func TestOrchestratorWorkerSkipsStaleCommandForFinishedTurn(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.Agent.WorkerCount = 1
	schema.Agent.QueueSize = 4
	// The finished turn ran a tool round: its persisted checkpoint is what a
	// replay resumes from, executing the tool again and streaming a summary.
	agent := &scriptedAgent{responses: []agents.Completion{
		{
			Content: "configured plan",
			ToolCalls: []agents.ToolCall{{
				ID:        "configured-call",
				Name:      schema.Tools[0].Name,
				Arguments: []byte(`{"prompt":"paint"}`),
			}},
		},
		{Content: " configured summary"},
		{Content: "second reply"},
	}}
	tools := &recordingToolExecutor{commands: make(chan chat.ToolCommand, 4)}
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
	ctx := context.Background()
	startTurn := func(content string) chat.ProcessTurn {
		t.Helper()
		session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
		if err != nil {
			t.Fatal(err)
		}
		assistant, err := service.StartTurn(ctx, chat.StartTurn{
			OwnerID: "owner-a", SessionID: session.ID, Content: content,
		})
		if err != nil {
			t.Fatal(err)
		}
		return chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}
	}
	finished := startTurn("first")
	if err := orchestrator.Process(ctx, finished); err != nil {
		t.Fatal(err)
	}
	finishedTurn, err := service.GetTurn(ctx, finished.OwnerID, finished.SessionID, finished.AssistantMessageID)
	if err != nil {
		t.Fatal(err)
	}
	if finishedTurn.State == service.TurnQueuedState() || finishedTurn.State == service.TurnRunningState() {
		t.Fatalf("first turn state = %q, want terminal", finishedTurn.State)
	}
	// The stale copy goes ahead of a fresh turn, so the single worker meets
	// it first.
	fresh := startTurn("second")
	if err := orchestrator.Enqueue(finished); err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Enqueue(fresh); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- orchestrator.Run(runCtx) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		pending, err := service.RecoverTurns(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("turns remained recoverable: %#v", pending)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	agent.mu.Lock()
	calls := len(agent.requests)
	agent.mu.Unlock()
	if calls != 3 {
		t.Fatalf("agent calls = %d, want 3 (stale command must not replay the finished turn)", calls)
	}
	if executed := len(tools.commands); executed != 1 {
		t.Fatalf("tool executions = %d, want 1", executed)
	}
	after, err := service.GetTurn(ctx, finished.OwnerID, finished.SessionID, finished.AssistantMessageID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != finishedTurn.State {
		t.Fatalf("finished turn state = %q, want %q", after.State, finishedTurn.State)
	}
	messages, err := service.ListMessages(ctx, fresh.OwnerID, fresh.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got := messages[len(messages)-1].Content; got != "second reply" {
		t.Fatalf("fresh turn reply = %q, want %q", got, "second reply")
	}
}
