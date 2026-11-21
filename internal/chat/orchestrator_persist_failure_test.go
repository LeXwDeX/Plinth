package chat_test

// #429: the tool phase and the background ending had their own way of parking
// a turn forever. Every failed checkpoint write of executeTools was reported
// as ErrTurnCheckpointConflict, so a store that refused the write or a value
// that could not be encoded looked like "another writer won" and left the turn
// running, the stream open and the session busy until a restart. The background
// ending of #137 ended the same way. These tests pin the endings now: only a
// real lost compare-and-swap leaves the turn to its other writer; a write that
// fails for any other reason, and a background ending that keeps failing, end
// the turn as failed. A paid Task the round already submitted keeps its
// binding, keeps running in the kernel and is never submitted again.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// toolPhaseFault fails the checkpoint write once the round's calls reached the
// tool loop: a receipt or a waiting entry is only in the checkpoint after a
// call ran. The round's own plan (no receipt, no waiting entry) is written
// before that and always succeeds, so the turn reaches the tool phase.
func toolPhaseFault(fault error) func(chat.TurnCheckpoint) error {
	return func(checkpoint chat.TurnCheckpoint) error {
		if len(checkpoint.Completed) > 0 || len(checkpoint.WaitingCalls()) > 0 {
			return fault
		}
		return nil
	}
}

// paidToolName is a Schema tool whose execution declares the paid effect, so
// the orchestrator treats a submission of it as billable.
func paidToolName(t *testing.T, schema catalog.ChatSchema) string {
	t.Helper()
	for _, tool := range schema.Tools {
		for _, effect := range tool.Execution.Effects {
			if effect == "PAID_TASK" {
				return tool.Name
			}
		}
	}
	for _, tool := range schema.AgentTools {
		for _, effect := range tool.Execution.Effects {
			if effect == "PAID_TASK" {
				return tool.Name
			}
		}
	}
	t.Fatal("the test Schema declares no paid tool")
	return ""
}

// paidTaskExecutor stands in for a generation tool: the first call submits one
// paid Task and hands back its identity, a call with the binding only observes
// it. It counts both, so a test can prove a failed turn never pays twice.
type paidTaskExecutor struct {
	mu           sync.Mutex
	submissions  int
	observations int
}

func (executor *paidTaskExecutor) Execute(_ context.Context, command chat.ToolCommand,
	observe func(chat.ToolObservation) error) (chat.ToolResult, error) {
	executor.mu.Lock()
	if command.BoundTaskID != "" {
		executor.observations++
		executor.mu.Unlock()
		return chat.ToolResult{TaskID: command.BoundTaskID, AgentContent: "{}"}, nil
	}
	executor.submissions++
	taskID := "task-" + command.SessionID
	executor.mu.Unlock()
	if err := observe(chat.ToolObservation{Registered: true, TaskID: taskID}); err != nil {
		return chat.ToolResult{}, err
	}
	// The Task is accepted and still runs: the reply waits for its receipt.
	return chat.ToolResult{TaskID: taskID, AwaitTask: true, WaitUntil: time.Now().Add(time.Hour)}, nil
}

func (executor *paidTaskExecutor) counts() (submissions, observations int) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return executor.submissions, executor.observations
}

// A checkpoint write that fails in the tool phase ends the turn as failed:
// the failure text with the error ID is stored, the error and done events
// close the stream, and the session takes the next message. The round may
// already have submitted a paid Task, so the reply keeps its binding; the
// failed turn is not replayable, which is what keeps the Task from being paid
// for twice.
func TestToolPhaseCheckpointFailureEndsTheTurnAsFailed(t *testing.T) {
	_, encodeErr := json.Marshal(struct{ Raw json.RawMessage }{Raw: json.RawMessage(`{`)})
	if encodeErr == nil {
		t.Fatal("the encoding fault did not fail")
	}
	for name, fault := range map[string]error{
		"the checkpoint cannot be encoded": encodeErr,
		"the store refuses the write":      errors.New("database is locked"),
	} {
		t.Run(name, func(t *testing.T) {
			h := newHungTurn(t, configuredRejectionTexts)
			tool := paidToolName(t, h.schema)
			h.repo.setFault(toolPhaseFault(fault))
			agent := &scriptedAgent{responses: []agents.Completion{
				{ToolCalls: []agents.ToolCall{budgetCall("raw-1", tool, "one")}},
			}}
			tools := &paidTaskExecutor{}
			command, err := h.run(agent, tools)
			if err != nil {
				t.Fatalf("Process = %v; the failure is handled, not left for the worker", err)
			}
			if submissions, observations := tools.counts(); submissions != 1 || observations != 0 {
				t.Fatalf("submissions=%d observations=%d; a failed turn must not pay again", submissions, observations)
			}
			wantTurnEnd(t, h.turn(command), "configured-turn-error", configuredEndReasons.Error)
			if status, closed := h.stream(command); status != "configured-error" || !closed {
				t.Fatalf("stream status = %q, terminal event = %v", status, closed)
			}
			stored := h.assistant(command)
			if !strings.Contains(stored.Content, h.schema.Presentation.Messages.UnknownError) ||
				!strings.Contains(stored.Content, command.AssistantMessageID) {
				t.Fatalf("stored reply = %#v", stored)
			}
			// The Task this round already paid for keeps its binding on the
			// reply, so the task tray and a stop still find it.
			if len(stored.ToolCalls) != 1 || stored.ToolCalls[0].TaskID != "task-"+h.session.ID {
				t.Fatalf("paid Task binding was lost: %#v", stored.ToolCalls)
			}
			// Nothing replays the failed turn into another paid submission.
			if pending, err := h.service.RecoverTurns(context.Background()); err != nil || len(pending) != 0 {
				t.Fatalf("the failed turn stayed recoverable: %+v %v", pending, err)
			}
			for _, want := range []string{"level=ERROR", "session_id=" + h.session.ID,
				"assistant_message_id=" + command.AssistantMessageID, fault.Error()} {
				if !strings.Contains(h.logs.String(), want) {
					t.Fatalf("log lacks %q: %s", want, h.logs.String())
				}
			}
			h.wantSessionOpen()
		})
	}
}

// A write that really lost the compare-and-swap is the other writer's turn
// now: it stays running, the stream stays open, and nothing here pays again.
func TestToolPhaseCheckpointConflictLeavesTheTurnToItsOtherWriter(t *testing.T) {
	h := newHungTurn(t, configuredRejectionTexts)
	tool := paidToolName(t, h.schema)
	h.repo.setFault(toolPhaseFault(chat.ErrTurnCheckpointConflict))
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{budgetCall("raw-1", tool, "one")}},
	}}
	tools := &paidTaskExecutor{}
	command, err := h.run(agent, tools)
	if !errors.Is(err, chat.ErrTurnCheckpointConflict) {
		t.Fatalf("Process = %v", err)
	}
	if state := h.turn(command).State; state != "configured-turn-running" {
		t.Fatalf("turn state = %q", state)
	}
	if status, closed := h.stream(command); status != "configured-active" || closed {
		t.Fatalf("stream status = %q, terminal event = %v", status, closed)
	}
	if submissions, _ := tools.counts(); submissions != 1 {
		t.Fatalf("submissions = %d", submissions)
	}
}

// streamStatus is what a client connecting now sees: the stream status and
// whether its replay holds the event that closes the handler.
func streamStatus(t *testing.T, service *chat.Service, schema catalog.ChatSchema,
	command chat.ProcessTurn) (status string, closed bool) {
	t.Helper()
	subscription, err := service.Subscribe(context.Background(), command.OwnerID, command.SessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	for _, event := range subscription.Replay {
		if event.Data[schema.Stream.EventFields.Type] == schema.Stream.EventTypes.Done {
			closed = true
		}
	}
	return subscription.Status, closed
}

// A background ending that fails once is retried: every step is idempotent,
// so the second attempt settles the consent claim, writes the note and closes
// the stream as done. The Task keeps running and is not submitted again.
func TestBackgroundEndingRetriesATransientFailure(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	executor := newWaitingExecutor()
	facts := newBackgroundFacts()
	logs := &hungTurnLogs{}
	facts.logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	schema := backgroundSchema(t)
	o := backgroundOrchestrator(t, service, schema, executor, facts, nil)
	command, taskID := waitingTurn(t, service, o, "background retry")
	facts.mu.Lock()
	facts.queued[taskID] = true
	facts.mu.Unlock()
	if err := o.ContinueTurnInBackground(ctx, "owner-a", command.SessionID); err != nil {
		t.Fatal(err)
	}
	facts.mu.Lock()
	facts.consentFailures = 1
	facts.mu.Unlock()
	if err := o.Process(ctx, command); err != nil {
		t.Fatalf("Process = %v", err)
	}
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil {
		t.Fatal(err)
	}
	wantTurnEnd(t, turn, "configured-turn-done", configuredEndReasons.Background)
	assistant := assistantOf(t, service, command)
	if !strings.Contains(assistant.Content, schema.Agent.BackgroundWait.TurnContent) {
		t.Fatalf("content = %q", assistant.Content)
	}
	if settled := facts.settlements(); len(settled) != 1 || !strings.HasSuffix(settled[0], "|"+taskID) {
		t.Fatalf("consent settlements = %v", settled)
	}
	if canceled := facts.cancellations(); len(canceled) != 0 {
		t.Fatalf("background ending cancelled %v", canceled)
	}
	executor.mu.Lock()
	state := executor.states[taskID]
	executor.mu.Unlock()
	if state != generation.TaskRunning {
		t.Fatalf("task state = %s", state)
	}
	if _, submissions := executor.counts(command.SessionID); submissions != 1 {
		t.Fatalf("submissions = %d", submissions)
	}
	if status, closed := streamStatus(t, service, schema, command); status != "configured-done" || !closed {
		t.Fatalf("stream status = %q, terminal event = %v", status, closed)
	}
	if !strings.Contains(logs.String(), "准备重试") || !strings.Contains(logs.String(), "consent store unavailable") {
		t.Fatalf("the failed attempt was not logged: %s", logs.String())
	}
}

// A background ending whose bounded retries all fail ends the turn as failed
// instead of parking it until a restart: the person reads the failure with its
// error ID, the stream closes, and the Tasks the person chose to keep running
// are neither cancelled nor submitted again.
func TestBackgroundEndingFailureEndsTheTurnAsFailed(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	executor := newWaitingExecutor()
	facts := newBackgroundFacts()
	logs := &hungTurnLogs{}
	facts.logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	schema := backgroundSchema(t)
	o := backgroundOrchestrator(t, service, schema, executor, facts, nil)
	command, taskID := waitingTurn(t, service, o, "background failure")
	facts.mu.Lock()
	facts.queued[taskID] = true
	facts.mu.Unlock()
	if err := o.ContinueTurnInBackground(ctx, "owner-a", command.SessionID); err != nil {
		t.Fatal(err)
	}
	facts.mu.Lock()
	facts.consentFailures = 10
	facts.mu.Unlock()
	if err := o.Process(ctx, command); err != nil {
		t.Fatalf("Process = %v; the failure is handled, not left for the worker", err)
	}
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil {
		t.Fatal(err)
	}
	// The failed ending wins over the background reason: the turn is an
	// error, not a turn that ended normally.
	wantTurnEnd(t, turn, "configured-turn-error", configuredEndReasons.Error)
	if status, closed := streamStatus(t, service, schema, command); status != "configured-error" || !closed {
		t.Fatalf("stream status = %q, terminal event = %v", status, closed)
	}
	stored := assistantOf(t, service, command)
	if !strings.Contains(stored.Content, schema.Presentation.Messages.UnknownError) ||
		!strings.Contains(stored.Content, command.AssistantMessageID) ||
		strings.Contains(stored.Content, schema.Agent.BackgroundWait.TurnContent) {
		t.Fatalf("stored reply = %#v", stored)
	}
	// The person's background choice stands: nothing is cancelled, the Task
	// keeps running and only the one submission exists.
	if canceled := facts.cancellations(); len(canceled) != 0 {
		t.Fatalf("the failed ending cancelled %v", canceled)
	}
	executor.mu.Lock()
	state := executor.states[taskID]
	executor.mu.Unlock()
	if state != generation.TaskRunning {
		t.Fatalf("task state = %s", state)
	}
	if _, submissions := executor.counts(command.SessionID); submissions != 1 {
		t.Fatalf("submissions = %d", submissions)
	}
	var paid chat.ToolCall
	for _, call := range stored.ToolCalls {
		if call.TaskID == taskID {
			paid = call
		}
	}
	if paid.ID == "" || !paid.Background {
		t.Fatalf("the Task binding is not the reply's: %#v", stored.ToolCalls)
	}
	// A failed turn is not replayed by the restart recovery.
	if pending, err := service.RecoverTurns(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("the failed turn stayed recoverable: %+v %v", pending, err)
	}
	for _, want := range []string{"level=ERROR", "session_id=" + command.SessionID,
		"assistant_message_id=" + command.AssistantMessageID, "consent store unavailable"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("log lacks %q: %s", want, logs.String())
		}
	}
}
