package chat_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

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
