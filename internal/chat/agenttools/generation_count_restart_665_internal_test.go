package agenttools

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

// #665 third review: only a revision that was applied counts against the
// turn, and every counted call reports its units so the orchestrator can
// persist them.
func TestRefusedRevisionsDoNotUseUpTheTurn665(t *testing.T) {
	refuse := true
	executor := &Executor{maxGeneration: 1, usage: map[string]generationUsage{}, limitContent: "limit reached",
		reviseProcessingPlan: func(context.Context, chat.ToolCommand) (chat.ToolResult, error) {
			if refuse {
				return chat.ToolResult{AgentContent: "refused", Error: "outside the plan", NoSubmission: true}, nil
			}
			return chat.ToolResult{AgentContent: "revised"}, nil
		}}
	revise := func(id string) chat.ToolResult {
		result, err := executor.Execute(context.Background(), chat.ToolCommand{OwnerID: "o", SessionID: "s",
			AssistantMessageID: "turn", Call: agents.ToolCall{ID: id, Name: ToolReviseProcessingPlan,
				Arguments: []byte(`{"operation":"retry"}`)}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, id := range []string{"bad-1", "bad-2", "bad-3"} {
		if got := revise(id); got.AgentContent != "refused" || got.GenerationUnits != 0 {
			t.Fatalf("%s: refused revision = %+v", id, got)
		}
	}
	refuse = false
	if got := revise("good"); got.AgentContent != "revised" || got.GenerationUnits != 1 {
		t.Fatalf("applied revision after refusals = %+v", got)
	}
	if got := revise("good"); got.AgentContent != "revised" || got.GenerationUnits != 1 {
		t.Fatalf("the same applied call was counted again: %+v", got)
	}
	if got := revise("next"); got.AgentContent != "limit reached" || got.GenerationUnits != 0 {
		t.Fatalf("a revision past the limit = %+v", got)
	}
}

// #665 third review: a process that lost the turn's counts (restart or
// eviction) and starts from the units the orchestrator persisted refuses and
// accepts exactly where the live process does.
func TestRebuiltGenerationCountEqualsTheLiveCount665(t *testing.T) {
	live := &Executor{maxGeneration: 6, usage: map[string]generationUsage{}}
	persisted := map[string]int{}
	count := func(executor *Executor, id string, prior, weight int) bool {
		reached := executor.generationLimitReachedForCallWeighted("s", "turn", id, prior, weight, executor.maxGeneration)
		if units := executor.countedGenerationUnits("s", "turn", id); units > 0 {
			persisted[id] = units
		}
		return reached
	}
	prior := func(except string) int {
		total := 0
		for id, units := range persisted {
			if id != except {
				total += units
			}
		}
		return total
	}
	if count(live, "plan", 0, 5) {
		t.Fatal("a five-step plan did not fit six")
	}
	if persisted["plan"] != 5 {
		t.Fatalf("persisted units = %v, want the plan's five steps", persisted)
	}
	liveRefusesTwo := live.generationLimitWouldBeReached("s", "turn", "two", prior("two"), 2, live.maxGeneration)
	restarted := &Executor{maxGeneration: 6, usage: map[string]generationUsage{}}
	if refused := restarted.generationLimitWouldBeReached("s", "turn", "two", prior("two"), 2, restarted.maxGeneration); refused != liveRefusesTwo || !refused {
		t.Fatalf("rebuilt refusal of two units = %v, live = %v, want both refused", refused, liveRefusesTwo)
	}
	if count(restarted, "one", prior("one"), 1) {
		t.Fatal("the rebuilt count refused the last unit")
	}
	if !count(restarted, "another", prior("another"), 1) {
		t.Fatal("the rebuilt count let the turn go past its limit")
	}
}

// #665 third review: a create_three_view the duplicate guard refused starts no
// run and uses no generation of the turn.
func TestThreeViewDuplicateRefusalIsNotCounted665(t *testing.T) {
	creates := 0
	executor, command := duplicateGuardExecutor(t,
		[]skillrun.Step{{Phase: skillrun.GrantPending, GrantRequestID: "grant-earlier"}},
		executiongrant.Grant{RequestID: "grant-earlier", TriggerTurnID: "turn-earlier", Status: executiongrant.Pending,
			Source: executiongrant.AgentPending}, false, false, &creates)
	executor.maxGeneration, executor.usage = 1, map[string]generationUsage{}
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil || result.AgentContent != "already running: run-earlier" || result.GenerationUnits != 0 {
		t.Fatalf("duplicate = %+v err=%v", result, err)
	}
	if executor.generationLimitWouldBeReached(executor.scopeKey(command), command.AssistantMessageID, "next", 0, 1, executor.maxGeneration) {
		t.Fatal("the refused duplicate used up the turn's only generation")
	}
}
