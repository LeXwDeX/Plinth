package generationtool_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #549: in a turn that answers a direction card on the card, the generation
// tools run only the chosen option's step with the capability it resolves to.
// The orchestrator already refused other tools; this is the capability check
// it cannot make (process_model runs remesh and resize alike).

func choiceScope549(tool, capability string) *chat.ChoiceScope {
	return &chat.ChoiceScope{ChoiceID: "choice-1", OptionID: "balanced", OptionLabel: "保轮廓",
		Step:    &chat.ChoiceStep{Tool: tool, Capability: capability},
		Refusal: "没有提交 {tool}：用户只选了「{option}」。请先汇报当前结果，再用新的 ask_choice 卡问下一步。"}
}

func TestChoiceScopeRefusesAnotherCapabilityOfTheChosenTool_549(t *testing.T) {
	executor, command, gateway, _ := policyHarness(t, sessionMode{mode: "auto"}, "refine_image", "text-to-image")
	command.ChoiceScope = choiceScope549("refine_image", "image-to-image")
	result, err := executor.Execute(context.Background(), withCall(command, "call-a", `{"prompt":"wood"}`), nil)
	if err != nil || !result.NoSubmission || !strings.Contains(result.AgentContent, "保轮廓") ||
		!strings.Contains(result.AgentContent, "refine_image") {
		t.Fatalf("unchosen capability = %+v err=%v", result, err)
	}
	if gateway.submitCount != 0 {
		t.Fatalf("submitted %d steps outside the chosen option", gateway.submitCount)
	}
}

func TestChoiceScopeRunsTheChosenStep_549(t *testing.T) {
	executor, command, gateway, _ := policyHarness(t, sessionMode{mode: "auto"}, "refine_image", "text-to-image")
	command.ChoiceScope = choiceScope549("refine_image", "text-to-image")
	result, err := executor.Execute(context.Background(), withCall(command, "call-a", `{"prompt":"wood"}`), nil)
	if err != nil || result.NoSubmission || gateway.submitCount != 1 {
		t.Fatalf("chosen step = %+v err=%v submits=%d", result, err, gateway.submitCount)
	}
}

// An option without a step authorizes none.
func TestChoiceScopeWithoutAStepRefusesEveryStep_549(t *testing.T) {
	executor, command, gateway, _ := policyHarness(t, sessionMode{mode: "auto"}, "refine_image", "text-to-image")
	scope := choiceScope549("", "")
	scope.Step = nil
	command.ChoiceScope = scope
	result, err := executor.Execute(context.Background(), withCall(command, "call-a", `{"prompt":"wood"}`), nil)
	if err != nil || !result.NoSubmission || gateway.submitCount != 0 {
		t.Fatalf("step without a chosen one = %+v err=%v submits=%d", result, err, gateway.submitCount)
	}
}

// A step the approved budget plan allows runs in the card-answer turn: the
// person approved the plan's steps as a whole.
func TestChoiceScopeLeavesAPlannedStepToThePlan_549(t *testing.T) {
	executor, command, gateway, repo := budgetHarness(t, "refine_image", "text-to-image")
	createActiveBudgetPlan(t, repo, "pb-549", 15, "text-to-image")
	command.ChoiceScope = choiceScope549("process_model", "remesh")
	result := executeBudgetCall(t, executor, command, "call-a")
	if result.NoSubmission || gateway.submitCount != 1 {
		t.Fatalf("planned step in a card-answer turn = %+v submits=%d", result, gateway.submitCount)
	}
}

// The chosen step is a step once, also when it stopped on an approval card:
// a second call of the same reply with other arguments is the second run and
// raises no second card, while a replay of the first call stays idempotent.
func TestChoiceScopeCountsAnApprovalCardAsTheChosenRun_549(t *testing.T) {
	ctx := context.Background()
	executor, command, gateway, repo := policyHarness(t, sessionMode{mode: "manual"}, "create_model_3d_from_image", "image-to-3d")
	command.ChoiceScope = choiceScope549("create_model_3d_from_image", "image-to-3d")
	first, err := executor.Execute(ctx, withCall(command, "call-1", `{"prompt":"wood"}`), nil)
	if err != nil || !first.NoSubmission || first.AgentContent == "" {
		t.Fatalf("first call = %+v err=%v", first, err)
	}
	second, err := executor.Execute(ctx, withCall(command, "call-2", `{"prompt":"marble"}`), nil)
	if err != nil || !second.NoSubmission || !strings.Contains(second.AgentContent, "保轮廓") {
		t.Fatalf("second run = %+v err=%v", second, err)
	}
	if got := pendingCards(t, repo, "session-a", time.Time{}); got != 1 {
		t.Fatalf("pending cards = %d, want the chosen run's only", got)
	}
	replay, err := executor.Execute(ctx, withCall(command, "call-1", `{"prompt":"wood"}`), nil)
	if err != nil || replay.AgentContent == "" || strings.Contains(replay.AgentContent, "保轮廓") {
		t.Fatalf("replay of the first call = %+v err=%v", replay, err)
	}
	if gateway.submitCount != 0 {
		t.Fatalf("submits = %d", gateway.submitCount)
	}
}
