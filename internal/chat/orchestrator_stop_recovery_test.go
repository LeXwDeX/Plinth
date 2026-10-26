package chat_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
)

type acceptedWithoutBindingExecutor struct {
	accepted chan struct{}
	called   atomic.Int32
	result   error
}

func (executor *acceptedWithoutBindingExecutor) Execute(
	ctx context.Context, _ chat.ToolCommand, _ func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	executor.called.Add(1)
	close(executor.accepted)
	<-ctx.Done()
	return chat.ToolResult{}, executor.result
}

func TestStopRecoversAcceptedTaskWithoutBindingAfterLookupRetry(t *testing.T) {
	for _, tc := range []struct {
		name             string
		executeError     string
		failFirstLookups bool
	}{
		{name: "unknown submit response", executeError: "submit result unknown"},
		{name: "consent accept failed and lookup transiently fails", executeError: "consent accept failed", failFirstLookups: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			repository := newMemoryChatRepository()
			service := newChatService(t, repository)
			schema := configuredChatSchema(t)
			schema.Presentation.Messages.TurnStopped = "stopped"
			agent := &scriptedStreamFuncs{streams: []func(context.Context, func(agents.Delta) error) (agents.Completion, error){
				func(context.Context, func(agents.Delta) error) (agents.Completion, error) {
					return agents.Completion{ToolCalls: []agents.ToolCall{{Name: schema.Tools[0].Name,
						Arguments: []byte(`{"prompt":"paint"}`)}}}, nil
				},
			}}
			executor := &acceptedWithoutBindingExecutor{accepted: make(chan struct{}), result: errors.New(tc.executeError)}
			var failLookup atomic.Bool
			failLookup.Store(tc.failFirstLookups)
			var canceled atomic.Int32
			session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
			if err != nil {
				t.Fatal(err)
			}
			acceptedTask := generation.Task{ID: "accepted-task", OwnerID: "owner-a", SessionID: session.ID}
			lookup := func(_ context.Context, ownerID, key string) (generation.Task, error) {
				if ownerID != "owner-a" || key == "" {
					t.Errorf("lookup scope = %q, key = %q", ownerID, key)
				}
				if failLookup.Load() {
					return generation.Task{}, errors.New("temporary lookup failure")
				}
				return acceptedTask, nil
			}
			get := func(_ context.Context, ownerID, taskID string) (generation.Task, error) {
				if ownerID != "owner-a" || taskID != "accepted-task" {
					t.Errorf("read scope = %q, task = %q", ownerID, taskID)
				}
				return acceptedTask, nil
			}
			cancelTask := func(_ context.Context, ownerID, taskID string) error {
				if ownerID != "owner-a" || taskID != "accepted-task" {
					t.Errorf("cancel scope = %q, task = %q", ownerID, taskID)
				}
				canceled.Add(1)
				return nil
			}
			makeOrchestrator := func() *chat.Orchestrator {
				orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
					Service: service, Schema: schema, Model: "model", Agent: agent, Tools: executor,
					TaskLookup: lookup, TaskGet: get, TaskCanceler: cancelTask,
				})
				if err != nil {
					t.Fatal(err)
				}
				return orchestrator
			}
			orchestrator := makeOrchestrator()
			assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
			if err != nil {
				t.Fatal(err)
			}
			command := chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}
			processDone := make(chan error, 1)
			go func() { processDone <- orchestrator.Process(ctx, command) }()
			select {
			case <-executor.accepted:
			case <-time.After(2 * time.Second):
				t.Fatal("paid submission was not accepted")
			}
			messages, err := service.ListMessages(ctx, "owner-a", session.ID)
			if err != nil || len(messages[1].ToolCalls) != 1 || messages[1].ToolCalls[0].TaskID != "" || messages[1].ToolCalls[0].IdempotencyKey == "" {
				t.Fatalf("frozen unbound call = %#v, err=%v", messages, err)
			}
			if active, err := orchestrator.CancelTurn(ctx, "owner-b", session.ID); err != nil || active {
				t.Fatalf("foreign owner cancel = %v, %v", active, err)
			}
			if active, err := orchestrator.CancelTurn(ctx, "owner-a", "another-session"); err != nil || active {
				t.Fatalf("foreign session cancel = %v, %v", active, err)
			}
			if active, err := orchestrator.CancelTurn(ctx, "owner-a", session.ID); err != nil || !active {
				t.Fatalf("stop accepted turn = %v, %v", active, err)
			}
			processErr := <-processDone
			if tc.failFirstLookups {
				if processErr == nil {
					t.Fatal("lookup failure must keep turn recoverable")
				}
				turns, err := service.RecoverTurns(ctx)
				if err != nil || len(turns) != 1 || !turns[0].StopRequested || canceled.Load() != 0 {
					t.Fatalf("durable stop after lookup failure: turns=%#v canceled=%d err=%v", turns, canceled.Load(), err)
				}
				failLookup.Store(false)
				if err := makeOrchestrator().Process(ctx, command); err != nil {
					t.Fatalf("restart reconciliation: %v", err)
				}
			} else if processErr != nil {
				t.Fatalf("stop reconciliation: %v", processErr)
			}
			if canceled.Load() == 0 || executor.called.Load() != 1 {
				t.Fatalf("cancel count=%d paid submit count=%d", canceled.Load(), executor.called.Load())
			}
			if turns, err := service.RecoverTurns(ctx); err != nil || len(turns) != 0 {
				t.Fatalf("stopped turn remained active: turns=%#v err=%v", turns, err)
			}
		})
	}
}

type unexpectedStopExecutor struct{ calls atomic.Int32 }

func (executor *unexpectedStopExecutor) Execute(
	context.Context, chat.ToolCommand, func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	executor.calls.Add(1)
	return chat.ToolResult{}, errors.New("stopped turn executed a tool")
}

func TestStoppedTurnLookupNeverSubmitsOrCancelsAnotherSession(t *testing.T) {
	for _, tc := range []struct {
		name       string
		task       generation.Task
		found      bool
		missingKey bool
	}{
		{name: "no task"},
		{name: "legacy unbound generation", missingKey: true},
		{name: "foreign owner", found: true, task: generation.Task{ID: "foreign", OwnerID: "owner-b"}},
		{name: "foreign session", found: true, task: generation.Task{ID: "foreign", OwnerID: "owner-a", SessionID: "other-session"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			service := newChatService(t, newMemoryChatRepository())
			schema := configuredChatSchema(t)
			session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
			if err != nil {
				t.Fatal(err)
			}
			assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
			if err != nil {
				t.Fatal(err)
			}
			assistant.Content = "planned"
			assistant.ToolCalls = []chat.ToolCall{{ID: "call-1", Name: schema.Tools[0].Name,
				IdempotencyKey: "chat:" + session.ID + ":call-1"}}
			if tc.missingKey {
				assistant.ToolCalls[0].IdempotencyKey = ""
			}
			if err := service.UpdateMessage(ctx, "owner-a", assistant); err != nil {
				t.Fatal(err)
			}
			if _, active, err := service.RequestTurnStop(ctx, "owner-a", session.ID); err != nil || !active {
				t.Fatalf("request stop = %v, %v", active, err)
			}
			executor := &unexpectedStopExecutor{}
			var canceled atomic.Int32
			orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
				Service: service, Schema: schema, Model: "model", Agent: &scriptedStreamFuncs{}, Tools: executor,
				TaskLookup: func(_ context.Context, _, _ string) (generation.Task, error) {
					if !tc.found {
						return generation.Task{}, &generation.Error{Code: generation.ErrNotFound, Operation: "lookup"}
					}
					return tc.task, nil
				},
				TaskCanceler: func(context.Context, string, string) error {
					canceled.Add(1)
					return nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			err = orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID})
			unresolved := tc.found || tc.missingKey
			if unresolved && err == nil || !unresolved && err != nil {
				t.Fatalf("reconciliation error = %v", err)
			}
			if executor.calls.Load() != 0 || canceled.Load() != 0 {
				t.Fatalf("stopped turn executed %d tools and canceled %d foreign tasks", executor.calls.Load(), canceled.Load())
			}
			turns, err := service.RecoverTurns(ctx)
			if err != nil || len(turns) != btoi(unresolved) {
				t.Fatalf("remaining turns = %#v, err=%v", turns, err)
			}
		})
	}
}

func TestStoppedTurnReconcilesLaterCallsAfterEarlierLookupFailure(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	schema := configuredChatSchema(t)
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	assistant.Content = "planned"
	assistant.ToolCalls = []chat.ToolCall{
		{ID: "first", Name: schema.Tools[0].Name, IdempotencyKey: "key-first"},
		{ID: "second", Name: schema.Tools[0].Name, IdempotencyKey: "key-second"},
	}
	if err := service.UpdateMessage(ctx, "owner-a", assistant); err != nil {
		t.Fatal(err)
	}
	if _, active, err := service.RequestTurnStop(ctx, "owner-a", session.ID); err != nil || !active {
		t.Fatalf("stop = %v, %v", active, err)
	}
	var firstFailure atomic.Bool
	firstFailure.Store(true)
	var canceled atomic.Int32
	var settled atomic.Int32
	tools := &unexpectedStopExecutor{}
	o, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "model", Agent: &scriptedStreamFuncs{}, Tools: tools,
		TaskLookup: func(_ context.Context, ownerID, key string) (generation.Task, error) {
			if ownerID != "owner-a" {
				t.Errorf("wrong lookup owner %s", ownerID)
			}
			if key == "key-first" {
				if firstFailure.Load() {
					return generation.Task{}, errors.New("temporary lookup failure")
				}
				return generation.Task{}, &generation.Error{Code: generation.ErrConflict}
			}
			return generation.Task{ID: "task-second", OwnerID: "owner-a", SessionID: session.ID}, nil
		},
		TaskGet: func(_ context.Context, ownerID, taskID string) (generation.Task, error) {
			if ownerID != "owner-a" || taskID != "task-second" {
				t.Errorf("wrong task read %s %s", ownerID, taskID)
			}
			return generation.Task{ID: "task-second", OwnerID: "owner-a", SessionID: session.ID}, nil
		},
		TaskCanceler: func(_ context.Context, ownerID, taskID string) error {
			if ownerID != "owner-a" || taskID != "task-second" {
				t.Errorf("wrong cancel %s %s", ownerID, taskID)
			}
			canceled.Add(1)
			return nil
		},
		StopConsent: func(ownerID, sessionID, callID, key, taskID string) error {
			if ownerID != "owner-a" || sessionID != session.ID {
				t.Errorf("wrong consent scope %s %s", ownerID, sessionID)
			}
			if callID == "second" && key == "key-second" && taskID == "task-second" {
				settled.Add(1)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}
	if err := o.Process(ctx, command); err == nil {
		t.Fatal("first lookup failure must keep stop recoverable")
	}
	if canceled.Load() < 1 || settled.Load() < 1 || tools.calls.Load() != 0 {
		t.Fatalf("later call not reconciled: canceled=%d settled=%d executed=%d", canceled.Load(), settled.Load(), tools.calls.Load())
	}
	firstFailure.Store(false)
	if err := o.Process(ctx, command); err != nil {
		t.Fatalf("tombstone recovery: %v", err)
	}
	if turns, err := service.RecoverTurns(ctx); err != nil || len(turns) != 0 {
		t.Fatalf("stopped turn remains: %#v, %v", turns, err)
	}
}

func btoi(value bool) int {
	if value {
		return 1
	}
	return 0
}

// detachedStopExecutor answers the stop reconciliation's question whether a
// generation call's approval card left its turn.
type detachedStopExecutor struct {
	unexpectedStopExecutor
	detached    bool
	wantHarness *harness.Binding
}

func (executor *detachedStopExecutor) DetachedOperationForCall(_ context.Context, command chat.ToolCommand) (bool, error) {
	if executor.wantHarness != nil && (command.Options.Harness == nil || command.Options.Harness.Selection.Binding != *executor.wantHarness) {
		return false, chat.ErrTurnCheckpointConflict
	}
	return executor.detached, nil
}

// A card that was detached before the Stop can still be approved afterwards,
// and the task that approval creates carries the call's frozen idempotency
// key. Stop reconciliation (immediate, at finalize, or at restart recovery)
// must not find and cancel that task; an undetached call is still cancelled.
func TestStoppedTurnLeavesADetachedApprovalsTaskAlone(t *testing.T) {
	for _, tc := range []struct {
		name     string
		detached bool
		canceled int32
	}{
		{name: "detached card", detached: true, canceled: 0},
		{name: "undetached call", detached: false, canceled: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			service := newChatService(t, newMemoryChatRepository())
			schema := configuredChatSchema(t)
			session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
			if err != nil {
				t.Fatal(err)
			}
			frozen := harness.DefaultSelection("owner-a", session.ID)
			service.SetHarnessSnapshotResolver(func(context.Context, string, string) (resolve.Snapshot, error) {
				return resolve.Snapshot{Selection: frozen, Status: resolve.StatusFree}, nil
			})
			assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
			if err != nil {
				t.Fatal(err)
			}
			assistant.Content = "planned"
			assistant.ToolCalls = []chat.ToolCall{{ID: "call-1", Name: schema.Tools[0].Name,
				IdempotencyKey: "chat:" + session.ID + ":call-1"}}
			if err := service.UpdateMessage(ctx, "owner-a", assistant); err != nil {
				t.Fatal(err)
			}
			if _, active, err := service.RequestTurnStop(ctx, "owner-a", session.ID); err != nil || !active {
				t.Fatalf("request stop = %v, %v", active, err)
			}
			executor := &detachedStopExecutor{detached: tc.detached, wantHarness: &frozen.Binding}
			var canceled atomic.Int32
			orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
				Service: service, Schema: schema, Model: "model", Agent: &scriptedStreamFuncs{}, Tools: executor,
				TaskLookup: func(_ context.Context, _, key string) (generation.Task, error) {
					// The approved card's task, under the call's frozen key.
					return generation.Task{ID: "task-approved", OwnerID: "owner-a", SessionID: session.ID}, nil
				},
				TaskGet: func(context.Context, string, string) (generation.Task, error) {
					return generation.Task{ID: "task-approved", OwnerID: "owner-a", SessionID: session.ID}, nil
				},
				TaskCanceler: func(context.Context, string, string) error {
					canceled.Add(1)
					return nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}); err != nil {
				t.Fatalf("stop reconciliation: %v", err)
			}
			if executor.calls.Load() != 0 || canceled.Load() != tc.canceled {
				t.Fatalf("stopped turn executed %d tools and canceled %d tasks, want 0 and %d", executor.calls.Load(), canceled.Load(), tc.canceled)
			}
			if turns, err := service.RecoverTurns(ctx); err != nil || len(turns) != 0 {
				t.Fatalf("stopped turn remained active: turns=%#v err=%v", turns, err)
			}
		})
	}
}
