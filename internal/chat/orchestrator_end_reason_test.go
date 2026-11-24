package chat_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #328: a finished turn stores why it ended. The strings are the Schema's
// (here the "configured-end-*" values of newChatService, which no code
// hardcodes); the turn state stays what it was before reasons existed: error
// for a failed turn, done for every other end.

func startedEndReasonTurn(t *testing.T, service *chat.Service) chat.ProcessTurn {
	t.Helper()
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	return chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}
}

func storedEndReasonTurn(t *testing.T, service *chat.Service, command chat.ProcessTurn) chat.Turn {
	t.Helper()
	turn, err := service.GetTurn(context.Background(), command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}

func wantTurnEnd(t *testing.T, turn chat.Turn, state, reason string) {
	t.Helper()
	if turn.State != state || turn.EndReason != reason {
		t.Fatalf("turn ended as state=%q reason=%q, want state=%q reason=%q", turn.State, turn.EndReason, state, reason)
	}
}

func endReasonOrchestrator(
	t *testing.T, service *chat.Service, schema catalog.ChatSchema, agent agents.Adapter, tools chat.ToolExecutor, logger *slog.Logger,
) *chat.Orchestrator {
	t.Helper()
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "configured-model", Agent: agent, Tools: tools, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	return orchestrator
}

func TestTurnEndReasonCompletedForANormalReply(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	orchestrator := endReasonOrchestrator(t, service, configuredChatSchema(t),
		&scriptedAgent{responses: []agents.Completion{{Content: "all done"}}}, noToolExecutor{}, nil)
	command := startedEndReasonTurn(t, service)
	if turn := storedEndReasonTurn(t, service, command); turn.EndReason != "" {
		t.Fatalf("a queued turn already has the reason %q", turn.EndReason)
	}
	if err := orchestrator.Process(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	wantTurnEnd(t, storedEndReasonTurn(t, service, command), "configured-turn-done", configuredEndReasons.Completed)
}

func TestTurnEndReasonCompletedAfterToolCalls(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	schema := configuredChatSchema(t)
	tool := schema.Tools[0].Name
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{budgetCall("g1", tool, "a"), budgetCall("g2", tool, "b")}},
		{Content: "configured summary"},
	}}
	tools := &recordingToolExecutor{commands: make(chan chat.ToolCommand, 8)}
	stored := runBudgetTurn(t, service, schema, agent, tools, nil)
	turn, err := service.GetTurn(context.Background(), "owner-a", stored.SessionID, stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantTurnEnd(t, turn, "configured-turn-done", configuredEndReasons.Completed)
}

// A stopped turn is finalized as done with the stop text; its reason says the
// person stopped it.
func TestTurnEndReasonStoppedKeepsTheDoneState(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	schema := configuredChatSchema(t)
	schema.Presentation.Messages.TurnStopped = "configured stopped"
	agent := &scriptedAgent{}
	orchestrator := endReasonOrchestrator(t, service, schema, agent, noToolExecutor{}, nil)
	command := startedEndReasonTurn(t, service)
	if _, active, err := service.RequestTurnStop(context.Background(), command.OwnerID, command.SessionID); err != nil || !active {
		t.Fatalf("request stop = %v, %v", active, err)
	}
	if err := orchestrator.Process(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	wantTurnEnd(t, storedEndReasonTurn(t, service, command), "configured-turn-done", configuredEndReasons.Stopped)
	if len(agent.requests) != 0 {
		t.Fatalf("a stopped turn still called the Agent %d times", len(agent.requests))
	}
}

// #137: the person stopped waiting and let the Task run on. The turn ends as
// done and says so; while it is parked on the Task it has no reason yet.
func TestTurnEndReasonBackgroundTurn(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	executor := newWaitingExecutor()
	facts := newBackgroundFacts()
	o := backgroundOrchestrator(t, service, backgroundSchema(t), executor, facts, nil)
	command, taskID := waitingTurn(t, service, o, "end reason")
	wantTurnEnd(t, storedEndReasonTurn(t, service, command), service.TurnWaitingTaskState(), "")
	facts.mu.Lock()
	facts.queued[taskID] = true
	facts.mu.Unlock()
	if err := o.ContinueTurnInBackground(ctx, "owner-a", command.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := o.Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	wantTurnEnd(t, storedEndReasonTurn(t, service, command), "configured-turn-done", configuredEndReasons.Background)
}

// A failed turn keeps the error state and says error; so does a turn the
// orchestrator refused for a full queue.
func TestTurnEndReasonErrorKeepsTheErrorState(t *testing.T) {
	ctx := context.Background()
	t.Run("the Agent request failed", func(t *testing.T) {
		service := newChatService(t, newMemoryChatRepository())
		orchestrator := endReasonOrchestrator(t, service, configuredChatSchema(t),
			failingAgent{err: errors.New("upstream refused")}, noToolExecutor{}, nil)
		command := startedEndReasonTurn(t, service)
		if err := orchestrator.Process(ctx, command); err != nil {
			t.Fatal(err)
		}
		wantTurnEnd(t, storedEndReasonTurn(t, service, command), "configured-turn-error", configuredEndReasons.Error)
	})
	t.Run("the queue was full", func(t *testing.T) {
		service := newChatService(t, newMemoryChatRepository())
		schema := configuredChatSchema(t)
		schema.Agent.WorkerCount, schema.Agent.QueueSize = 1, 1
		orchestrator := endReasonOrchestrator(t, service, schema, &scriptedAgent{}, noToolExecutor{}, nil)
		first, second := startedEndReasonTurn(t, service), startedEndReasonTurn(t, service)
		if err := orchestrator.Schedule(ctx, first); err != nil {
			t.Fatal(err)
		}
		if err := orchestrator.Schedule(ctx, second); !errors.Is(err, chat.ErrOrchestratorQueueFull) {
			t.Fatalf("second schedule = %v", err)
		}
		wantTurnEnd(t, storedEndReasonTurn(t, service, second), "configured-turn-error", configuredEndReasons.Error)
		if turn := storedEndReasonTurn(t, service, first); turn.EndReason != "" {
			t.Fatalf("the queued turn has the reason %q", turn.EndReason)
		}
	})
}

// approvalCardExecutor plays a generation executor whose paid step left an
// approval card: the call returns at once and the executor reports it as
// detached from the turn. detached and readErr are what the persisted
// operation says when the orchestrator asks at the end of the turn.
type approvalCardExecutor struct {
	mu       sync.Mutex
	detached bool
	readErr  error
	asked    []chat.ToolCommand
}

func (executor *approvalCardExecutor) Execute(
	context.Context, chat.ToolCommand, func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	return chat.ToolResult{AgentContent: `{"result":"configured waiting"}`, NoSubmission: true}, nil
}

func (executor *approvalCardExecutor) DetachedOperationForCall(_ context.Context, command chat.ToolCommand) (bool, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.asked = append(executor.asked, command)
	return executor.detached, executor.readErr
}

func (executor *approvalCardExecutor) questions() []chat.ToolCommand {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return append([]chat.ToolCommand(nil), executor.asked...)
}

func approvalCardTurn(t *testing.T, executor chat.ToolExecutor, logger *slog.Logger) (*chat.Service, chat.ProcessTurn, catalog.ChatSchema) {
	t.Helper()
	service := newChatService(t, newMemoryChatRepository())
	schema := configuredChatSchema(t)
	tool := schema.Tools[0].Name
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{budgetCall("g1", tool, "paint")}},
		{Content: "waiting for your decision"},
	}}
	orchestrator := endReasonOrchestrator(t, service, schema, agent, executor, logger)
	command := startedEndReasonTurn(t, service)
	if err := orchestrator.Process(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	return service, command, schema
}

// A paid step whose approval card was detached leaves the turn waiting for the
// person: it ends as done, with the awaiting-approval reason. The question to
// the executor names the call, its frozen key and the turn, like the stop
// reconciliation's.
func TestTurnEndReasonAwaitingApprovalFromADetachedCard(t *testing.T) {
	executor := &approvalCardExecutor{detached: true}
	service, command, _ := approvalCardTurn(t, executor, nil)
	wantTurnEnd(t, storedEndReasonTurn(t, service, command), "configured-turn-done", configuredEndReasons.AwaitingApproval)
	asked := executor.questions()
	if len(asked) != 1 {
		t.Fatalf("the executor was asked %d times: %#v", len(asked), asked)
	}
	messages, err := service.ListMessages(context.Background(), command.OwnerID, command.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	stored := messages[len(messages)-1]
	if len(stored.ToolCalls) != 1 {
		t.Fatalf("stored calls = %#v", stored.ToolCalls)
	}
	question := asked[0]
	if question.OwnerID != command.OwnerID || question.SessionID != command.SessionID ||
		question.AssistantMessageID != command.AssistantMessageID ||
		question.Call.ID != stored.ToolCalls[0].ID || question.Call.Name != stored.ToolCalls[0].Name ||
		question.FrozenIdempotencyKey == "" || question.FrozenIdempotencyKey != stored.ToolCalls[0].IdempotencyKey {
		t.Fatalf("question = %#v, stored call = %#v", question, stored.ToolCalls[0])
	}
}

// A call whose card did not leave the turn is an ordinary completed turn.
func TestTurnEndReasonCompletedWhenNoCardWasDetached(t *testing.T) {
	executor := &approvalCardExecutor{detached: false}
	service, command, _ := approvalCardTurn(t, executor, nil)
	wantTurnEnd(t, storedEndReasonTurn(t, service, command), "configured-turn-done", configuredEndReasons.Completed)
	if asked := executor.questions(); len(asked) != 1 {
		t.Fatalf("the executor was asked %d times", len(asked))
	}
}

// The reason is bookkeeping: when the persisted operation cannot be read, the
// turn still finishes, the failure is logged, and the next reason applies.
func TestTurnEndReasonFallsBackWhenTheCardCannotBeRead(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	executor := &approvalCardExecutor{detached: true, readErr: errors.New("operation store unavailable")}
	service, command, _ := approvalCardTurn(t, executor, logger)
	wantTurnEnd(t, storedEndReasonTurn(t, service, command), "configured-turn-done", configuredEndReasons.Completed)
	if !strings.Contains(logs.String(), "读取轮次的批准卡失败") || !strings.Contains(logs.String(), "operation store unavailable") {
		t.Fatalf("the read failure was not logged: %q", logs.String())
	}
}

// A turn that failed is an error however many cards it left behind.
func TestTurnEndReasonErrorBeatsAwaitingApproval(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	schema := configuredChatSchema(t)
	executor := &approvalCardExecutor{detached: true}
	agent := &quotaAfterToolAgent{tool: schema.Tools[0].Name, err: errors.New("follow-up refused")}
	orchestrator := endReasonOrchestrator(t, service, schema, agent, executor, nil)
	command := startedEndReasonTurn(t, service)
	if err := orchestrator.Process(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if agent.streams() < 2 {
		t.Fatalf("the follow-up did not run: %d streams", agent.streams())
	}
	wantTurnEnd(t, storedEndReasonTurn(t, service, command), "configured-turn-error", configuredEndReasons.Error)
}

// pendingPlanStore is a production plan store whose pending plans the test
// sets; it answers every other question like promptBudgetStore.
type pendingPlanStore struct {
	promptBudgetStore
	mu      sync.Mutex
	pending []budget.ProductionBudget
	err     error
	reads   int
}

func (store *pendingPlanStore) PendingBudgets(context.Context, string, string) ([]budget.ProductionBudget, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.reads++
	return append([]budget.ProductionBudget(nil), store.pending...), store.err
}

func (store *pendingPlanStore) set(pending []budget.ProductionBudget, err error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.pending, store.err = pending, err
}

func (store *pendingPlanStore) readCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.reads
}

// A production plan the turn proposed and the person has not decided is an
// approval card too; a plan of another turn, an unreadable store and a session
// without production plans are not.
func TestTurnEndReasonPendingProductionPlanOfTheTurn(t *testing.T) {
	for _, test := range []struct {
		name  string
		plans bool
		store func(turn string) ([]budget.ProductionBudget, error)
		want  string
	}{
		{"this turn proposed it", true, func(turn string) ([]budget.ProductionBudget, error) {
			return []budget.ProductionBudget{{ID: "pb-other", MessageID: "other-turn"}, {ID: "pb-1", MessageID: turn}}, nil
		}, configuredEndReasons.AwaitingApproval},
		{"another turn proposed it", true, func(string) ([]budget.ProductionBudget, error) {
			return []budget.ProductionBudget{{ID: "pb-other", MessageID: "other-turn"}}, nil
		}, configuredEndReasons.Completed},
		{"no plan is pending", true, func(string) ([]budget.ProductionBudget, error) { return nil, nil }, configuredEndReasons.Completed},
		{"the plans cannot be read", true, func(turn string) ([]budget.ProductionBudget, error) {
			return []budget.ProductionBudget{{ID: "pb-1", MessageID: turn}}, errors.New("budget store unavailable")
		}, configuredEndReasons.Completed},
		{"production plans are not declared", false, func(turn string) ([]budget.ProductionBudget, error) {
			return []budget.ProductionBudget{{ID: "pb-1", MessageID: turn}}, nil
		}, configuredEndReasons.Completed},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newChatService(t, newMemoryChatRepository())
			schema := configuredChatSchema(t)
			store := &pendingPlanStore{}
			if test.plans {
				service.SetProductionPlans(chat.ProductionPlanPolicy{
					Budget:    schema.ApprovalPolicy.Budget,
					Offerings: func() []generation.Offering { return nil },
					Store:     store,
				})
			}
			orchestrator := endReasonOrchestrator(t, service, schema,
				&scriptedAgent{responses: []agents.Completion{{Content: "here is my plan"}}}, noToolExecutor{}, nil)
			command := startedEndReasonTurn(t, service)
			pending, err := test.store(command.AssistantMessageID)
			store.set(pending, err)
			if err := orchestrator.Process(context.Background(), command); err != nil {
				t.Fatal(err)
			}
			wantTurnEnd(t, storedEndReasonTurn(t, service, command), "configured-turn-done", test.want)
		})
	}
}

// With no declared reasons nothing is stored and nothing is read to tell them
// apart: the turn ends exactly as before.
func TestTurnEndReasonUndeclaredStoresNoneAndReadsNothing(t *testing.T) {
	service := newChatServiceWith(t, newMemoryChatRepository(), func(config *chat.ServiceConfig) {
		config.TurnEndReasons = catalog.ChatTurnEndReasonsSchema{}
	})
	if service.RecordsTurnEndReasons() {
		t.Fatal("a service without declared reasons reports that it records them")
	}
	schema := configuredChatSchema(t)
	store := &pendingPlanStore{}
	service.SetProductionPlans(chat.ProductionPlanPolicy{
		Budget:    schema.ApprovalPolicy.Budget,
		Offerings: func() []generation.Offering { return nil },
		Store:     store,
	})
	executor := &approvalCardExecutor{detached: true}
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{budgetCall("g1", schema.Tools[0].Name, "paint")}},
		{Content: "waiting for your decision"},
	}}
	orchestrator := endReasonOrchestrator(t, service, schema, agent, executor, nil)
	command := startedEndReasonTurn(t, service)
	if err := orchestrator.Process(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	wantTurnEnd(t, storedEndReasonTurn(t, service, command), "configured-turn-done", "")
	if asked := executor.questions(); len(asked) != 0 || store.readCount() != 0 {
		t.Fatalf("approval cards were read without declared reasons: %d questions, %d plan reads", len(asked), store.readCount())
	}
}

// The service maps each kind to the Schema's string, ends the turn as error
// only for the error kind, and clears the reason when the turn runs again.
func TestTurnEndReasonServiceMapsKindsToTheSchemaStrings(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	if !service.RecordsTurnEndReasons() {
		t.Fatal("the configured service does not report its declared reasons")
	}
	for _, test := range []struct {
		kind   chat.TurnEndReason
		state  string
		reason string
	}{
		{chat.TurnEndCompleted, "configured-turn-done", "configured-end-completed"},
		{chat.TurnEndAwaitingApproval, "configured-turn-done", "configured-end-awaiting-approval"},
		{chat.TurnEndStopped, "configured-turn-done", "configured-end-stopped"},
		{chat.TurnEndBackground, "configured-turn-done", "configured-end-background"},
		{chat.TurnEndError, "configured-turn-error", "configured-end-error"},
	} {
		command := startedEndReasonTurn(t, service)
		if err := service.FinishTurnWithReason(ctx, command.AssistantMessageID, test.kind); err != nil {
			t.Fatal(err)
		}
		wantTurnEnd(t, storedEndReasonTurn(t, service, command), test.state, test.reason)
		if err := service.MarkTurnRunning(ctx, command.AssistantMessageID); err != nil {
			t.Fatal(err)
		}
		wantTurnEnd(t, storedEndReasonTurn(t, service, command), "configured-turn-running", "")
	}
	for _, test := range []struct {
		failed bool
		state  string
		reason string
	}{
		{false, "configured-turn-done", "configured-end-completed"},
		{true, "configured-turn-error", "configured-end-error"},
	} {
		command := startedEndReasonTurn(t, service)
		if err := service.FinishTurn(ctx, command.AssistantMessageID, test.failed); err != nil {
			t.Fatal(err)
		}
		wantTurnEnd(t, storedEndReasonTurn(t, service, command), test.state, test.reason)
	}
	command := startedEndReasonTurn(t, service)
	if err := service.FinishTurnWithReason(ctx, command.AssistantMessageID, chat.TurnEndReason(99)); err == nil {
		t.Fatal("an unknown turn end reason finished the turn")
	}
	if turn := storedEndReasonTurn(t, service, command); turn.State != "configured-turn-queued" || turn.EndReason != "" {
		t.Fatalf("a refused finish changed the turn: %#v", turn)
	}
}

// Declared reasons are all there and all different, or the service refuses to
// start: a reader could not tell the reasons apart otherwise.
func TestTurnEndReasonServiceRefusesPartialOrDuplicateReasons(t *testing.T) {
	newService := func(reasons catalog.ChatTurnEndReasonsSchema) error {
		_, err := chat.NewService(chat.ServiceConfig{
			Repository: newMemoryChatRepository(), DefaultTitle: "title", UserRole: "user", AssistantRole: "assistant",
			PendingContent: "pending", StreamActive: "active", StreamDone: "done", StreamError: "error",
			TurnQueued: "queued", TurnRunning: "running", TurnWaitingTask: "waiting", TurnDone: "done", TurnError: "error",
			TurnEndReasons:       reasons,
			PlaceholderHealing:   chat.PlaceholderHealingPolicy{StaleAfter: 1, PromptArgument: "prompt", PromptPreviewCharacters: 5, InterruptedMessage: "interrupted", InterruptedPromptTemplate: "{prompt}"},
			StreamCleanupTTL:     1,
			SubscriberBufferSize: 1,
			EventFields:          chat.EventFieldPolicy{Type: "type", MessageID: "message"},
			EventTypes:           chat.EventTypePolicy{MessageCreated: "created"},
		})
		return err
	}
	if err := newService(catalog.ChatTurnEndReasonsSchema{}); err != nil {
		t.Fatalf("undeclared reasons refused: %v", err)
	}
	if err := newService(configuredEndReasons); err != nil {
		t.Fatalf("declared reasons refused: %v", err)
	}
	partial := configuredEndReasons
	partial.Stopped = ""
	if err := newService(partial); err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("partial reasons accepted: %v", err)
	}
	duplicate := configuredEndReasons
	duplicate.Background = duplicate.Completed
	if err := newService(duplicate); err == nil || !strings.Contains(err.Error(), "background") {
		t.Fatalf("duplicate reasons accepted: %v", err)
	}
}
