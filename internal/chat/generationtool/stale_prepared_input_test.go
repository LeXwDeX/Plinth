package generationtool_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #516: SubmitPrepared refused the frozen input before writing any Task (the
// Product Schema changed, the adapter is gone, an input changed). The step is a
// readable tool result, not a possible submission: the turn does not replay
// the same authorization, the Agent is told to prepare it again, and the
// budget reservation goes back to the plan.
func TestStalePreparedInputIsAReadableResult516(t *testing.T) {
	executor, command, gateway, repo := budgetHarness(t, "refine_image", "text-to-image")
	createActiveBudgetPlan(t, repo, "pb-stale", 15, "text-to-image")
	gateway.submitErr = &generation.Error{Code: generation.ErrConflict, Operation: "submit_prepared",
		Detail: "Product Schema 已变化，请重新准备输入", Cause: generation.ErrNotSubmitted}

	command.Call.ID = "call-stale"
	command.ConsentWaitDeadline = time.Time{}
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil || !result.NoSubmission || result.Suspend || result.TaskID != "" || gateway.submitCount != 1 {
		t.Fatalf("stale input: result=%+v err=%v submits=%d", result, err, gateway.submitCount)
	}
	if !strings.Contains(result.AgentContent, "没有提交任务") {
		t.Fatalf("the Agent was not told the step was not submitted: %q", result.AgentContent)
	}
	summary, found, err := repo.ProductionBudgetSummary(context.Background(), "owner-a", "session-a")
	if err != nil || !found {
		t.Fatalf("plan summary: found=%v err=%v", found, err)
	}
	if legacy, _ := summary.Unit(budget.LegacyUnit); legacy.Reserved != 0 {
		t.Fatalf("the stale step kept its reservation: %+v", summary.Units)
	}
}

// A submission whose outcome is unknown keeps the old answer: an error without
// NoSubmission, so the turn keeps the call for a replay that finds the Task.
func TestUnknownPreparedSubmissionStaysUnknown516(t *testing.T) {
	executor, command, gateway, repo := budgetHarness(t, "refine_image", "text-to-image")
	createActiveBudgetPlan(t, repo, "pb-unknown", 15, "text-to-image")
	gateway.submitErr = &generation.Error{Code: generation.ErrConflict, Operation: "submit_prepared",
		Cause: errors.New("repository write lost")}

	command.Call.ID = "call-unknown"
	command.ConsentWaitDeadline = time.Time{}
	result, err := executor.Execute(context.Background(), command, nil)
	if err == nil || result.NoSubmission || result.AgentContent != "" {
		t.Fatalf("unknown submission: result=%+v err=%v", result, err)
	}
}
