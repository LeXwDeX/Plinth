package agenttools

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
)

// countingGenerate is a generation tool that reports whether the call's
// operation is already prepared and returns a fixed result.
type countingGenerate struct {
	prepared bool
	result   chat.ToolResult
	err      error
}

func (g *countingGenerate) ExistingOperation(context.Context, chat.ToolCommand) (bool, error) {
	return g.prepared, nil
}

func (g *countingGenerate) Execute(context.Context, chat.ToolCommand, func(chat.ToolObservation) error) (chat.ToolResult, error) {
	return g.result, g.err
}

func planCommand(callID string, prior int) chat.ToolCommand {
	return chat.ToolCommand{OwnerID: "o", SessionID: "s", AssistantMessageID: "turn", PriorGenerationCalls: prior,
		Call: agents.ToolCall{ID: callID, Name: "plan_model_processing", Arguments: []byte(`{"steps":[{},{},{}]}`)},
		Tool: catalog.ChatToolSchema{Processing: &catalog.ChatToolProcessingSchema{
			Plan: &catalog.ChatToolProcessingPlanSchema{StepsArgument: "steps"}}}}
}

// #665 wave 4: a processing plan of three paid steps that waits for approval
// is prepared; a process that restarts during that wait replays the call with
// the operation already prepared. It counts the plan's three steps again (not
// zero, and not one once its Task is bound), and the limit does not refuse it.
func TestPreparedPlanReplayCountsItsStepsAfterARestart665(t *testing.T) {
	generate := &countingGenerate{result: chat.ToolResult{Suspend: true, NoSubmission: true}}
	executor := &Executor{maxGeneration: 4, usage: map[string]generationUsage{}, params: agentparams.NewStore(),
		generate: generate, limitContent: "limit reached"}
	first, err := executor.Execute(context.Background(), planCommand("plan", 0), nil)
	if err != nil || !first.Suspend || first.GenerationUnits != 3 {
		t.Fatalf("first execution = %+v err=%v, want suspended with 3 units", first, err)
	}
	generate.prepared = true
	restarted := &Executor{maxGeneration: 4, usage: map[string]generationUsage{}, params: agentparams.NewStore(),
		generate: generate, limitContent: "limit reached"}
	// Another call of the turn already used three, so a fresh plan of three
	// would not fit; the prepared one is not refused.
	replay, err := restarted.Execute(context.Background(), planCommand("plan", 3), nil)
	if err != nil || !replay.Suspend || replay.GenerationUnits != 3 {
		t.Fatalf("replay after restart = %+v err=%v, want suspended with 3 units", replay, err)
	}
	if !restarted.generationLimitWouldBeReached(restarted.scopeKey(planCommand("next", 0)), "turn", "next", 0, 1) {
		t.Fatal("the rebuilt count left room past the limit")
	}
}

// #665 wave 4: a call the generation tool refused itself (invalid arguments,
// a refused input, a store error before submitting) after it was counted uses
// none of the turn's generations and reports no units; a queued admission
// retry and an approval card keep theirs.
func TestRefusedGenerationIsNotCounted665(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result chat.ToolResult
		err    error
		units  int
	}{
		{"refused with an error text", chat.ToolResult{Error: "invalid arguments", NoSubmission: true}, nil, 0},
		{"refused with an error", chat.ToolResult{NoSubmission: true}, errors.New("store failed"), 0},
		{"admission queue", chat.ToolResult{NoSubmission: true, RetryAfter: 1}, errors.New("full"), 3},
		{"approval card", chat.ToolResult{Suspend: true, NoSubmission: true}, nil, 3},
		{"plan created", chat.ToolResult{AgentContent: "{}", NoSubmission: true}, nil, 3},
		{"unknown submit result", chat.ToolResult{}, errors.New("response lost"), 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := &Executor{maxGeneration: 3, usage: map[string]generationUsage{}, params: agentparams.NewStore(),
				generate: &countingGenerate{result: tc.result, err: tc.err}, limitContent: "limit reached"}
			result, _ := executor.Execute(context.Background(), planCommand("plan", 0), nil)
			if result.GenerationUnits != tc.units {
				t.Fatalf("units = %d, want %d", result.GenerationUnits, tc.units)
			}
			if fits := !executor.generationLimitWouldBeReached(executor.scopeKey(planCommand("next", 0)), "turn", "next", 0, 1); fits != (tc.units == 0) {
				t.Fatalf("room for one more = %v, want %v", fits, tc.units == 0)
			}
		})
	}
}
