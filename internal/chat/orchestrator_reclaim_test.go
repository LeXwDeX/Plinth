package chat_test

// #516: a turn a worker marked running stays running when Process returns
// before installing its ending (here: the turn read fails) and nothing in the
// live process looked at running turns until the next restart. The reclaim
// pass enqueues such a turn again; a turn suspended on a wait of this process
// is held and is left alone.

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// strandingRepository fails reads of a running turn and writes that mark a
// turn running, each a set number of times.
type strandingRepository struct {
	*recoverySnapshotRepository
	running string

	mu               sync.Mutex
	failRunningReads int
	failMarkRunning  int
}

func (r *strandingRepository) GetTurn(ctx context.Context, ownerID, sessionID, assistantID string) (chat.Turn, error) {
	turn, err := r.recoverySnapshotRepository.GetTurn(ctx, ownerID, sessionID, assistantID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil && turn.State == r.running && r.failRunningReads > 0 {
		r.failRunningReads--
		return chat.Turn{}, errors.New("injected turn read failure")
	}
	return turn, err
}

func (r *strandingRepository) UpdateTurnState(ctx context.Context, assistantMessageID, state, reason string, now time.Time) error {
	r.mu.Lock()
	if state == r.running && r.failMarkRunning > 0 {
		r.failMarkRunning--
		r.mu.Unlock()
		return errors.New("injected mark running failure")
	}
	r.mu.Unlock()
	return r.recoverySnapshotRepository.UpdateTurnState(ctx, assistantMessageID, state, reason, now)
}

func (r *strandingRepository) pending() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failRunningReads + r.failMarkRunning
}

func runReclaimHarness(t *testing.T, repository *strandingRepository, agent agents.Adapter,
	tools chat.ToolExecutor) (*chat.Service, *chat.Orchestrator, context.Context) {
	t.Helper()
	service := newChatService(t, repository)
	repository.running = service.TurnRunningState()
	schema := suspendSchema(t)
	// The scanner's own reclaim pass waits one consent wait; the test drives
	// the passes itself.
	schema.Agent.ConsentWaitTimeoutSeconds = 600
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{Service: service, Schema: schema,
		Model: "configured-model", Agent: agent, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- orchestrator.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-runErr:
			if err != nil {
				t.Errorf("orchestrator.Run: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("orchestrator.Run did not stop")
		}
	})
	select {
	case <-repository.recovered:
	case <-time.After(2 * time.Second):
		t.Fatal("orchestrator.Run never took its recovery snapshot")
	}
	return service, orchestrator, ctx
}

func newStrandingRepository() *strandingRepository {
	return &strandingRepository{recoverySnapshotRepository: &recoverySnapshotRepository{
		memoryChatRepository: newMemoryChatRepository(), recovered: make(chan struct{})}}
}

func TestRunningTurnWhoseProcessFailedEarlyIsReclaimed516(t *testing.T) {
	repository := newStrandingRepository()
	repository.failRunningReads = 1
	agent := &scriptedSuspendAgent{initial: agents.Completion{Content: "reclaimed answer"}}
	service, orchestrator, ctx := runReclaimHarness(t, repository, agent, &suspendConsentToolExecutor{})
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID,
		AssistantMessageID: startTurnFor(t, service, "owner-a", session.ID, "hello")}
	if err := orchestrator.Schedule(ctx, command); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return repository.pending() == 0 })
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != service.TurnRunningState() || agent.streamsCount() != 0 {
		t.Fatalf("the failed Process did not leave the turn running: %+v streams=%d %v", turn, agent.streamsCount(), err)
	}
	waitFor(t, func() bool {
		if err := orchestrator.ReclaimStrandedTurns(ctx); err != nil {
			t.Fatal(err)
		}
		turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
		return err == nil && turn.State == "configured-turn-done"
	})
	if agent.streamsCount() != 1 {
		t.Fatalf("agent streams = %d, want one run after the reclaim", agent.streamsCount())
	}
	if message := lastAssistant(t, service, "owner-a", session.ID); message.Content != "reclaimed answer" {
		t.Fatalf("reclaimed turn answer = %q", message.Content)
	}
}

// A failed write of the running mark leaves the turn queued, which the waiting
// scanner already offers again.
func TestTurnWhoseRunningMarkFailedIsRetried516(t *testing.T) {
	repository := newStrandingRepository()
	repository.failMarkRunning = 1
	agent := &scriptedSuspendAgent{initial: agents.Completion{Content: "retried answer"}}
	service, orchestrator, ctx := runReclaimHarness(t, repository, agent, &suspendConsentToolExecutor{})
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID,
		AssistantMessageID: startTurnFor(t, service, "owner-a", session.ID, "hello")}
	if err := orchestrator.Schedule(ctx, command); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
		return err == nil && turn.State == "configured-turn-done"
	})
	if repository.pending() != 0 || agent.streamsCount() != 1 {
		t.Fatalf("pending failures=%d streams=%d", repository.pending(), agent.streamsCount())
	}
}

// A turn suspended on a consent wait of this process is held: the reclaim
// passes leave it running and do not replay its call.
func TestConsentSuspendedTurnIsNotReclaimed516(t *testing.T) {
	repository := newStrandingRepository()
	agent := &scriptedSuspendAgent{initial: agents.Completion{ToolCalls: []agents.ToolCall{{
		ID: "call-consent", Name: "wait_for_approval", Arguments: json.RawMessage(`{}`),
	}}}}
	tools := &suspendConsentToolExecutor{}
	service, orchestrator, ctx := runReclaimHarness(t, repository, agent, tools)
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID,
		AssistantMessageID: startTurnFor(t, service, "owner-a", session.ID, "wait")}
	t.Cleanup(func() { orchestrator.ResumeConsentWait("owner-a", session.ID) })
	if err := orchestrator.Schedule(ctx, command); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return suspendedCount(orchestrator) == 1 })
	for range 3 {
		if err := orchestrator.ReclaimStrandedTurns(ctx); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	tools.mu.Lock()
	consents := tools.consents
	tools.mu.Unlock()
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || consents != 1 || turn.State != service.TurnRunningState() || suspendedCount(orchestrator) != 1 {
		t.Fatalf("a suspended turn was reclaimed: consents=%d turn=%+v %v", consents, turn, err)
	}
}
