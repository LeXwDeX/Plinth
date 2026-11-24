package agenttools

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

// #563: create_three_view is a paid generation of the turn like any other
// generation tool; max_generation_calls_per_turn bounds it too. A new run past
// the bound is refused before anything is created; the same call coming back
// to its own run (recovery, observation) is not counted again.
func TestThreeViewCountsAgainstTheTurnGenerationLimit(t *testing.T) {
	ctx := context.Background()
	limited := func() (*Executor, *harnessThreeGateway, chat.ToolCommand) {
		executor, gateway, _, command, _ := threeViewHarnessFixture(t)
		executor.maxGeneration = 1
		executor.usage = map[string]generationUsage{}
		executor.limitContent = "configured: generation limit of this turn reached"
		return executor, gateway, command
	}

	// Earlier calls of the turn already used the bound: no run is created.
	executor, gateway, command := limited()
	command.PriorGenerationCalls = 1
	result, err := executor.Execute(ctx, command, nil)
	if err != nil || !result.NoSubmission || result.Suspend || result.AgentContent != executor.limitContent {
		t.Fatalf("a three-view past the limit = %+v %v", result, err)
	}
	if _, _, _, err := executor.existingThreeView(ctx, command); !errors.Is(err, skillrun.ErrMissing) {
		t.Fatalf("a run was created past the limit: %v", err)
	}
	if gateway.submits != 0 {
		t.Fatalf("submits = %d", gateway.submits)
	}

	// Within the bound the run is prepared; the same call again is its own
	// run, not a second generation.
	executor, gateway, command = limited()
	result, err = executor.Execute(ctx, command, nil)
	if err != nil || !result.Suspend {
		t.Fatalf("the first three-view of the turn = %+v %v", result, err)
	}
	result, err = executor.Execute(ctx, command, nil)
	if err != nil || result.AgentContent == executor.limitContent {
		t.Fatalf("the same call was counted twice: %+v %v", result, err)
	}

	// A second new three-view in the same turn reaches the bound.
	second := command
	second.Call.ID = "call-second"
	result, err = executor.Execute(ctx, second, nil)
	if err != nil || !result.NoSubmission || result.AgentContent != executor.limitContent {
		t.Fatalf("a second three-view in the turn = %+v %v", result, err)
	}
	if _, _, _, err := executor.existingThreeView(ctx, second); !errors.Is(err, skillrun.ErrMissing) {
		t.Fatalf("a run was created past the limit: %v", err)
	}
	if gateway.submits != 0 {
		t.Fatalf("submits = %d", gateway.submits)
	}
}
