package chat_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// gatedCancelAgent blocks a turn's request until its context is canceled and
// then holds the cancellation open until the test releases it, so a test can
// observe what the deleting caller does while the turn winds down.
type gatedCancelAgent struct {
	once     sync.Once
	entered  chan struct{}
	canceled chan struct{}
	release  chan struct{}
}

func newGatedCancelAgent() *gatedCancelAgent {
	return &gatedCancelAgent{
		entered:  make(chan struct{}),
		canceled: make(chan struct{}),
		release:  make(chan struct{}),
	}
}

func (agent *gatedCancelAgent) Stream(
	ctx context.Context,
	_ agents.Request,
	_ func(agents.Delta) error,
) (agents.Completion, error) {
	agent.once.Do(func() { close(agent.entered) })
	<-ctx.Done()
	select {
	case <-agent.canceled:
	default:
		close(agent.canceled)
	}
	<-agent.release
	return agents.Completion{}, ctx.Err()
}

func (*gatedCancelAgent) Complete(context.Context, agents.Request) (agents.Completion, error) {
	return agents.Completion{}, context.Canceled
}

// A deleted session must take its running turn with it: the turn is
// interrupted, DeleteSession returns only after the turn left, and nothing
// the turn would have written afterwards is an error (#384). The log of the
// self-hosted instance showed the turn running on for up to two minutes after
// the delete and then failing on rows that no longer existed.
func TestDeleteSessionStopsTheRunningTurnBeforeItsRowsGo(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	logs := &countingLogHandler{levels: map[slog.Level]int{}}
	agent := newGatedCancelAgent()
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  configuredChatSchema(t),
		Model:   "configured-model",
		Agent:   agent,
		Tools:   scriptedToolExecutor{},
		Logger:  slog.New(logs),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID: "owner-a", SessionID: session.ID, Content: "draw slowly",
	})
	if err != nil {
		t.Fatal(err)
	}
	processDone := make(chan error, 1)
	go func() {
		processDone <- orchestrator.Process(ctx, chat.ProcessTurn{
			OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
		})
	}()
	<-agent.entered

	deleteDone := make(chan error, 1)
	go func() { deleteDone <- service.DeleteSession(ctx, "owner-a", session.ID) }()
	select {
	case <-agent.canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("deleting the session did not interrupt its running turn")
	}
	select {
	case err := <-deleteDone:
		t.Fatalf("DeleteSession returned (%v) while the turn was still winding down", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(agent.release)
	select {
	case err := <-deleteDone:
		if err != nil {
			t.Fatalf("DeleteSession: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DeleteSession did not return once the turn left")
	}
	select {
	case err := <-processDone:
		if err != nil {
			t.Fatalf("the interrupted turn reported %v; a deleted session is not an error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the interrupted turn did not return")
	}
	if got := logs.count(slog.LevelError); got != 0 {
		t.Fatalf("%d error log records for a turn whose session was deleted", got)
	}
	if _, err := service.ListMessages(ctx, "owner-a", session.ID); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("messages of the deleted session = %v, want ErrSessionNotFound", err)
	}
	if active, err := service.HasActiveTurn(ctx, "owner-a", session.ID); err != nil || active {
		t.Fatalf("HasActiveTurn after delete = %v, %v", active, err)
	}
}

// deletingRepository holds DeleteSession until the test lets it go.
type deletingRepository struct {
	*memoryChatRepository
	entered chan struct{}
	release chan struct{}
}

func (r *deletingRepository) DeleteSession(ctx context.Context, ownerID, sessionID string) (bool, error) {
	close(r.entered)
	<-r.release
	return r.memoryChatRepository.DeleteSession(ctx, ownerID, sessionID)
}

// While the rows of a session are being deleted a new turn must not start on
// it: it would write rows the deletion is about to remove.
func TestStartTurnRefusesASessionBeingDeleted(t *testing.T) {
	repository := &deletingRepository{
		memoryChatRepository: newMemoryChatRepository(),
		entered:              make(chan struct{}),
		release:              make(chan struct{}),
	}
	service := newChatService(t, repository)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	deleteDone := make(chan error, 1)
	go func() { deleteDone <- service.DeleteSession(ctx, "owner-a", session.ID) }()
	<-repository.entered
	_, err = service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "late"})
	if !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("StartTurn on a session being deleted = %v, want ErrSessionNotFound", err)
	}
	close(repository.release)
	if err := <-deleteDone; err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
}

// The deletion of a session that is not there is the usual not-found, and it
// touches nothing of another owner's turn under the same session ID.
func TestDeleteSessionOfAnotherOwnerLeavesTheTurnRunning(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	agent := newGatedCancelAgent()
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  configuredChatSchema(t),
		Model:   "configured-model",
		Agent:   agent,
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
	processDone := make(chan error, 1)
	go func() {
		processDone <- orchestrator.Process(ctx, chat.ProcessTurn{
			OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
		})
	}()
	<-agent.entered
	if err := service.DeleteSession(ctx, "owner-b", session.ID); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("another owner's delete = %v, want ErrSessionNotFound", err)
	}
	select {
	case <-agent.canceled:
		t.Fatal("another owner's delete interrupted the turn")
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := orchestrator.CancelTurn(ctx, "owner-a", session.ID); err != nil {
		t.Fatal(err)
	}
	close(agent.release)
	if err := <-processDone; err != nil {
		t.Fatalf("Process: %v", err)
	}
}

// A turn row whose session is gone (a database that once ran without foreign
// key enforcement keeps such orphans) must not be resumed by the startup
// recovery: no model call, no error, and the turn is finished so it is not
// offered again by the next restart (#384: "chat: session not found" 110 ms
// after the start).
func TestRunSkipsTurnsOfDeletedSessions(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.Agent.WorkerCount = 1
	schema.Agent.QueueSize = 2
	logs := &countingLogHandler{levels: map[slog.Level]int{}}
	agent := &scriptedAgent{responses: []agents.Completion{{Content: "must not be asked"}}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   scriptedToolExecutor{},
		Logger:  slog.New(logs),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	repository.dropSessionKeepingTurns(session.ID)

	runCtx, cancel := context.WithCancel(ctx)
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
			t.Fatalf("the turn of a deleted session stays recoverable: %#v", pending)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	agent.mu.Lock()
	requests := len(agent.requests)
	agent.mu.Unlock()
	if requests != 0 {
		t.Fatalf("the recovery called the model %d times for a deleted session", requests)
	}
	if got := logs.count(slog.LevelError); got != 0 {
		t.Fatalf("%d error log records for a turn of a deleted session", got)
	}
}

// A queued command that reaches a worker after its session was deleted ends
// quietly: nothing to run, nothing to report.
func TestProcessOfADeletedSessionIsNotAnError(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	agent := &scriptedAgent{responses: []agents.Completion{{Content: "must not be asked"}}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  configuredChatSchema(t),
		Model:   "configured-model",
		Agent:   agent,
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
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteSession(ctx, "owner-a", session.ID); err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
	}); err != nil {
		t.Fatalf("Process of a deleted session = %v, want nil", err)
	}
	agent.mu.Lock()
	requests := len(agent.requests)
	agent.mu.Unlock()
	if requests != 0 {
		t.Fatalf("the model was called %d times for a deleted session", requests)
	}
}

// lateWriteAgent holds the turn's request until the test has deleted the
// session behind its back, then streams text, as a model that was already
// answering would.
type lateWriteAgent struct {
	entered chan struct{}
	proceed chan struct{}
}

func (agent *lateWriteAgent) Stream(
	_ context.Context,
	_ agents.Request,
	emit func(agents.Delta) error,
) (agents.Completion, error) {
	close(agent.entered)
	<-agent.proceed
	if err := emit(agents.Delta{Content: "late text"}); err != nil {
		return agents.Completion{}, err
	}
	return agents.Completion{Content: "late text"}, nil
}

func (*lateWriteAgent) Complete(context.Context, agents.Request) (agents.Completion, error) {
	return agents.Completion{}, context.Canceled
}

// A turn whose rows were deleted under it by some other path (the repository
// is called directly here) still ends quietly: the write that fails on the
// missing session, the rows it cannot update and the stream it cannot finish
// are the deletion's doing, not errors of the turn. The log showed exactly
// these: a foreign key failure on the continuation's write, then "message not
// found" twice from the finishing writes (#384).
func TestTurnEndsQuietlyWhenItsRowsAreDeletedUnderIt(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	logs := &countingLogHandler{levels: map[slog.Level]int{}}
	agent := &lateWriteAgent{entered: make(chan struct{}), proceed: make(chan struct{})}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  configuredChatSchema(t),
		Model:   "configured-model",
		Agent:   agent,
		Tools:   scriptedToolExecutor{},
		Logger:  slog.New(logs),
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
	processDone := make(chan error, 1)
	go func() {
		processDone <- orchestrator.Process(ctx, chat.ProcessTurn{
			OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
		})
	}()
	<-agent.entered
	if deleted, err := repository.DeleteSession(ctx, "owner-a", session.ID); err != nil || !deleted {
		t.Fatalf("DeleteSession = %v, %v", deleted, err)
	}
	close(agent.proceed)
	select {
	case err := <-processDone:
		if err != nil {
			t.Fatalf("a turn whose session was deleted reported %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the turn did not return")
	}
	if got := logs.count(slog.LevelError); got != 0 {
		t.Fatalf("%d error log records for a turn whose session was deleted", got)
	}
}

// blockedRepository refuses to delete any session, as a repository does for a
// session that has records it must keep.
type blockedRepository struct{ *memoryChatRepository }

func (blockedRepository) PreflightSessionDeletion(context.Context, string, string) error {
	return chat.ErrSessionDeletionBlocked
}

// A deletion that is bound to be refused must not stop the running turn: the
// session stays, and so must its turn.
func TestDeleteSessionRefusedLeavesTheRunningTurnAlone(t *testing.T) {
	repository := blockedRepository{newMemoryChatRepository()}
	service := newChatService(t, repository)
	agent := newGatedCancelAgent()
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  configuredChatSchema(t),
		Model:   "configured-model",
		Agent:   agent,
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
	processDone := make(chan error, 1)
	go func() {
		processDone <- orchestrator.Process(ctx, chat.ProcessTurn{
			OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
		})
	}()
	<-agent.entered
	if err := service.DeleteSession(ctx, "owner-a", session.ID); !errors.Is(err, chat.ErrSessionDeletionBlocked) {
		t.Fatalf("DeleteSession = %v, want ErrSessionDeletionBlocked", err)
	}
	select {
	case <-agent.canceled:
		t.Fatal("a refused deletion interrupted the running turn")
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := orchestrator.CancelTurn(ctx, "owner-a", session.ID); err != nil {
		t.Fatal(err)
	}
	close(agent.release)
	if err := <-processDone; err != nil {
		t.Fatalf("Process: %v", err)
	}
}

// resumingAgent blocks its first request until the turn is canceled and answers
// the next ones, as a model does when its turn is run again from the persisted
// state.
type resumingAgent struct {
	mu      sync.Mutex
	calls   int
	entered chan struct{}
}

func (agent *resumingAgent) Stream(
	ctx context.Context,
	_ agents.Request,
	emit func(agents.Delta) error,
) (agents.Completion, error) {
	agent.mu.Lock()
	agent.calls++
	call := agent.calls
	agent.mu.Unlock()
	if call == 1 {
		close(agent.entered)
		<-ctx.Done()
		return agents.Completion{}, ctx.Err()
	}
	if emit != nil {
		if err := emit(agents.Delta{Content: "configured response"}); err != nil {
			return agents.Completion{}, err
		}
	}
	return agents.Completion{Content: "configured response"}, nil
}

func (*resumingAgent) Complete(context.Context, agents.Request) (agents.Completion, error) {
	return agents.Completion{}, context.Canceled
}

// RunSessionDeletion is the protocol DeleteSession and the canvas project
// deletion share: mark, stop the turn, delete, clear. When the rows were not
// deleted (a storage failure, or a project that was already gone) the session
// stays, so the turn it interrupted must go on from its persisted state and
// finish, not stay stopped until the next restart.
func TestRunSessionDeletionResumesTheInterruptedTurnWhenNothingWasDeleted(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.Agent.WorkerCount = 1
	schema.Agent.QueueSize = 2
	agent := &resumingAgent{entered: make(chan struct{})}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   scriptedToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stopRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- orchestrator.Run(runCtx) }()
	defer func() {
		stopRun()
		<-runDone
	}()
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Enqueue(chat.ProcessTurn{
		OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	<-agent.entered

	deleted, err := service.RunSessionDeletion(ctx, "owner-a", session.ID, func(context.Context) (bool, error) {
		return false, nil
	})
	if err != nil || deleted {
		t.Fatalf("RunSessionDeletion = %v, %v, want false and no error", deleted, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		messages, err := service.ListMessages(ctx, "owner-a", session.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) == 2 && messages[1].Content == "configured response" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the interrupted turn was not resumed: messages = %#v", messages)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The final transaction can refuse a deletion the preflight allowed: a paid
// submission accepted after the preflight advanced a SkillRun step while the
// turn was being stopped. The session stays, and the turn the deletion
// interrupted goes on from its checkpoint like the other "nothing deleted"
// paths (#430).
func TestRunSessionDeletionResumesTheTurnWhenTheFinalCheckRefuses(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.Agent.WorkerCount = 1
	schema.Agent.QueueSize = 2
	agent := &resumingAgent{entered: make(chan struct{})}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   agent,
		Tools:   scriptedToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stopRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- orchestrator.Run(runCtx) }()
	defer func() {
		stopRun()
		<-runDone
	}()
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Enqueue(chat.ProcessTurn{
		OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	<-agent.entered

	deleted, err := service.RunSessionDeletion(ctx, "owner-a", session.ID, func(context.Context) (bool, error) {
		return false, chat.ErrSessionDeletionBlocked
	})
	if deleted || !errors.Is(err, chat.ErrSessionDeletionBlocked) {
		t.Fatalf("RunSessionDeletion = %v, %v, want the final check's refusal", deleted, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		messages, err := service.ListMessages(ctx, "owner-a", session.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) == 2 && messages[1].Content == "configured response" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the interrupted turn was not resumed: messages = %#v", messages)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// What the deletion function reports reaches the caller: the canvas project
// deletion answers "project not found" from it.
func TestRunSessionDeletionReportsWhatTheDeletionDid(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("storage failure")
	if deleted, err := service.RunSessionDeletion(ctx, "owner-a", session.ID, func(context.Context) (bool, error) {
		return false, failure
	}); deleted || !errors.Is(err, failure) {
		t.Fatalf("a failing deletion = %v, %v", deleted, err)
	}
	if owns, err := service.OwnsSession(ctx, "owner-a", session.ID); err != nil || !owns {
		t.Fatalf("the session after a failed deletion owns=%v err=%v", owns, err)
	}
	if deleted, err := service.RunSessionDeletion(ctx, "owner-a", session.ID, func(ctx context.Context) (bool, error) {
		return repository.DeleteSession(ctx, "owner-a", session.ID)
	}); err != nil || !deleted {
		t.Fatalf("a deletion that removed the rows = %v, %v", deleted, err)
	}
}

// preflightWindowRepository runs a hook inside PreflightSessionDeletion, the
// window between the owner check and the deletion mark, and then refuses the
// deletion as a session with a SkillRun in flight does.
type preflightWindowRepository struct {
	*memoryChatRepository
	inside  func()
	verdict error
}

func (r *preflightWindowRepository) PreflightSessionDeletion(context.Context, string, string) error {
	if r.inside != nil {
		r.inside()
	}
	return r.verdict
}

// A turn that finishes while the preflight is deciding must still finish: the
// deletion mark is set only after the preflight passed, so a refusal does not
// suppress the turn's finishing writes and leave it running forever (#430).
func TestDeleteSessionRefusedInsideThePreflightWindowLeavesAFinishedTurnFinished(t *testing.T) {
	repository := &preflightWindowRepository{
		memoryChatRepository: newMemoryChatRepository(),
		verdict:              chat.ErrSessionDeletionBlocked,
	}
	service := newChatService(t, repository)
	agent := &lateWriteAgent{entered: make(chan struct{}), proceed: make(chan struct{})}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  configuredChatSchema(t),
		Model:   "configured-model",
		Agent:   agent,
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
	processDone := make(chan error, 1)
	go func() {
		processDone <- orchestrator.Process(ctx, chat.ProcessTurn{
			OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
		})
	}()
	<-agent.entered
	repository.inside = func() {
		close(agent.proceed)
		select {
		case err := <-processDone:
			if err != nil {
				t.Errorf("the turn finishing in the preflight window reported %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("the turn did not finish in the preflight window")
		}
	}
	if err := service.DeleteSession(ctx, "owner-a", session.ID); !errors.Is(err, chat.ErrSessionDeletionBlocked) {
		t.Fatalf("DeleteSession = %v, want ErrSessionDeletionBlocked", err)
	}
	if active, err := service.HasActiveTurn(ctx, "owner-a", session.ID); err != nil || active {
		t.Fatalf("the surviving session is busy (active=%v err=%v): the finished turn was left running", active, err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1].Content != "late text" {
		t.Fatalf("messages after the refused deletion = %#v, want the finished reply", messages)
	}
}

// A resumed turn must not wait for a restart when the queue is full: the
// running row is returned to the queued state the waiting scanner reads, so
// the next sweep enqueues it (#430).
func TestResumeSessionReturnsAFullQueueTurnToTheScanner(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.Agent.WorkerCount = 1
	schema.Agent.QueueSize = 1
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service,
		Schema:  schema,
		Model:   "configured-model",
		Agent:   &scriptedAgent{},
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
	if err := service.MarkTurnRunning(ctx, assistant.ID); err != nil {
		t.Fatal(err)
	}
	// No worker runs in this test: the single queue slot stays occupied.
	if err := orchestrator.Enqueue(chat.ProcessTurn{AssistantMessageID: "queue-filler"}); err != nil {
		t.Fatal(err)
	}
	deleted, err := service.RunSessionDeletion(ctx, "owner-a", session.ID, func(context.Context) (bool, error) {
		return false, nil
	})
	if err != nil || deleted {
		t.Fatalf("RunSessionDeletion = %v, %v, want the session to stay", deleted, err)
	}
	turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turn.State != service.TurnQueuedState() {
		t.Fatalf("the running turn left by a full queue is %q, want %q", turn.State, service.TurnQueuedState())
	}
	candidates, err := service.ListWakeCandidates(ctx, chat.TurnPageCursor{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].AssistantMessageID != assistant.ID {
		t.Fatalf("the waiting scanner sees %#v, want the resumed turn", candidates)
	}
}
