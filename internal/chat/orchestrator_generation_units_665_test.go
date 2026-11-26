package chat_test

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

// unitsToolExecutor reports the units each call counted, as the agent tools
// do, and records the prior count the orchestrator handed to each call (by
// prompt: the orchestrator names the calls itself).
type unitsToolExecutor struct {
	mu    sync.Mutex
	units map[string]int
	prior map[string]int
}

func (executor *unitsToolExecutor) Execute(_ context.Context, command chat.ToolCommand,
	_ func(chat.ToolObservation) error) (chat.ToolResult, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	var arguments struct {
		Prompt string `json:"prompt"`
	}
	_ = json.Unmarshal(command.Call.Arguments, &arguments)
	executor.prior[arguments.Prompt] = command.PriorGenerationCalls
	return chat.ToolResult{AgentContent: `{}`, NoSubmission: true, GenerationUnits: executor.units[arguments.Prompt]}, nil
}

// #665 third review: a call without a Task (a processing plan of five steps,
// an applied revision) still counts in a later round: the orchestrator rebuilds
// the prior count from the units persisted on the turn checkpoint, not from
// task-bound calls only, so an executor that lost its in-memory count (restart
// or eviction) starts where the live one stood.
func TestOrchestratorHandsPersistedGenerationUnitsToLaterCalls665(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	schema := configuredChatSchema(t)
	tool := schema.Tools[0].Name
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{budgetCall("plan", tool, "plan"), budgetCall("refused", tool, "refused")}},
		{ToolCalls: []agents.ToolCall{budgetCall("revise", tool, "revise")}},
		{ToolCalls: []agents.ToolCall{budgetCall("next", tool, "next")}},
		{Content: "configured done"},
	}}
	tools := &unitsToolExecutor{units: map[string]int{"plan": 5, "revise": 1}, prior: map[string]int{}}
	runBudgetTurn(t, service, schema, agent, tools, nil)
	want := map[string]int{"plan": 0, "refused": 5, "revise": 5, "next": 6}
	for id, prior := range want {
		if tools.prior[id] != prior {
			t.Fatalf("prior of %s = %d, want %d (all: %v)", id, tools.prior[id], prior, tools.prior)
		}
	}
}

// suspendingUnitsExecutor counts five units for the call and suspends it, as a
// processing plan of five steps whose card waits for the person.
type suspendingUnitsExecutor struct{}

func (suspendingUnitsExecutor) Execute(context.Context, chat.ToolCommand, func(chat.ToolObservation) error) (chat.ToolResult, error) {
	return chat.ToolResult{Suspend: true, NoSubmission: true, GenerationUnits: 5}, nil
}

// #665 wave 4: a call that suspends the turn for approval persists the units
// it counted on the turn checkpoint, even without a call in flight beside it,
// so a process that restarts during the wait rebuilds the turn's count.
func TestSuspendedCallPersistsItsGenerationUnits665(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	schema := configuredChatSchema(t)
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{budgetCall("plan", schema.Tools[0].Name, "plan")}},
	}}
	assistant := runBudgetTurn(t, service, schema, agent, suspendingUnitsExecutor{}, nil)
	turn, err := service.GetTurn(context.Background(), "owner-a", assistant.SessionID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The orchestrator names the calls itself: one call, five units.
	units := turn.Checkpoint.GenerationUnits
	if len(units) != 1 {
		t.Fatalf("checkpoint units = %v, want the suspended call's five persisted", units)
	}
	for _, n := range units {
		if n != 5 {
			t.Fatalf("checkpoint units = %v, want the suspended call's five persisted", units)
		}
	}
}

// denyOnReplayExecutor suspends the "plan" call with five units first, as a
// processing plan whose card waits; its replay after the person denied the
// card is refused before anything was submitted and takes the units back. It
// records the prior count handed to every other call.
type denyOnReplayExecutor struct {
	mu    sync.Mutex
	plans int
	prior map[string]int
}

func (executor *denyOnReplayExecutor) Execute(_ context.Context, command chat.ToolCommand,
	_ func(chat.ToolObservation) error) (chat.ToolResult, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	var arguments struct {
		Prompt string `json:"prompt"`
	}
	_ = json.Unmarshal(command.Call.Arguments, &arguments)
	if arguments.Prompt != "plan" {
		executor.prior[arguments.Prompt] = command.PriorGenerationCalls
		return chat.ToolResult{AgentContent: `{}`, NoSubmission: true, GenerationUnits: 1}, nil
	}
	executor.plans++
	if executor.plans == 1 {
		return chat.ToolResult{Suspend: true, NoSubmission: true, GenerationUnits: 5}, nil
	}
	return chat.ToolResult{Error: "denied", NoSubmission: true, GenerationUnitsTakenBack: true}, nil
}

// #665 final review: a suspended call persisted its five units; once its
// approval is denied, the replay takes them back and the checkpoint drops
// them, so a later call of the turn does not see them as used.
func TestDeniedSuspendedCallLeavesNoUnitsForLaterCalls665(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	schema := configuredChatSchema(t)
	tool := schema.Tools[0].Name
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{budgetCall("plan", tool, "plan")}},
		{ToolCalls: []agents.ToolCall{budgetCall("next", tool, "next")}},
		{Content: "configured done"},
	}}
	tools := &denyOnReplayExecutor{prior: map[string]int{}}
	assistant := runBudgetTurn(t, service, schema, agent, tools, nil)
	suspended, err := service.GetTurn(ctx, "owner-a", assistant.SessionID, assistant.ID)
	if err != nil || len(suspended.Checkpoint.GenerationUnits) != 1 {
		t.Fatalf("suspended checkpoint units = %v err=%v, want the plan's five", suspended.Checkpoint.GenerationUnits, err)
	}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{Service: service, Schema: schema,
		Model: "configured-model", Agent: agent, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: assistant.SessionID,
		AssistantMessageID: assistant.ID}); err != nil {
		t.Fatal(err)
	}
	if tools.plans != 2 {
		t.Fatalf("plan executions = %d, want the replay after the denial", tools.plans)
	}
	if prior, ran := tools.prior["next"]; !ran || prior != 0 {
		t.Fatalf("prior of the next call = %d (ran %v), want 0: the denied plan's units were not taken back", prior, ran)
	}
	ended, err := service.GetTurn(ctx, "owner-a", assistant.SessionID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	for id, units := range ended.Checkpoint.GenerationUnits {
		if units == 5 {
			t.Fatalf("checkpoint still holds the denied call's units: %s=%d", id, units)
		}
	}
}

// unitsSaveFaultRepository fails the first checkpoint write that carries
// generation units, as a store error while the suspended call persists them.
type unitsSaveFaultRepository struct {
	*memoryChatRepository
	mu     sync.Mutex
	failed bool
}

func (r *unitsSaveFaultRepository) CompareAndSwapTurnCheckpoint(ctx context.Context, ownerID, sessionID, assistantID string,
	version int64, oldState, newState string, checkpoint chat.TurnCheckpoint, allowStopped bool, updatedAt time.Time,
) (int64, error) {
	r.mu.Lock()
	fail := !r.failed && len(checkpoint.GenerationUnits) > 0
	r.failed = r.failed || fail
	r.mu.Unlock()
	if fail {
		return 0, errors.New("store down")
	}
	return r.memoryChatRepository.CompareAndSwapTurnCheckpoint(ctx, ownerID, sessionID, assistantID,
		version, oldState, newState, checkpoint, allowStopped, updatedAt)
}

// #665 final review: the suspend path writes the checkpoint only to keep the
// suspended call's units; a failed write there is logged and the turn still
// suspends on its card instead of failing (the units then rebuild by the
// earlier rule after a restart).
func TestSuspendedCallSuspendsWhenItsUnitsCannotBeSaved665(t *testing.T) {
	repository := &unitsSaveFaultRepository{memoryChatRepository: newMemoryChatRepository()}
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{budgetCall("plan", schema.Tools[0].Name, "plan")}},
	}}
	assistant := runBudgetTurn(t, service, schema, agent, suspendingUnitsExecutor{}, nil)
	repository.mu.Lock()
	failed := repository.failed
	repository.mu.Unlock()
	if !failed {
		t.Fatal("the units write was never attempted")
	}
	turn, err := service.GetTurn(context.Background(), "owner-a", assistant.SessionID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turn.State != service.TurnRunningState() {
		t.Fatalf("turn state = %q, want it still suspended (%q)", turn.State, service.TurnRunningState())
	}
}
