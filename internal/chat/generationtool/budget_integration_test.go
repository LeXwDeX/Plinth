package generationtool_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func createActiveBudgetPlan(t *testing.T, repo *sqlite.Repository, id string, limit int, capabilities ...string) budget.ProductionBudget {
	t.Helper()
	ctx := context.Background()
	steps := make([]budget.PlanStep, 0, len(capabilities))
	for _, capability := range capabilities {
		steps = append(steps, budget.PlanStep{Capability: capability, Purpose: capability, EstimatedCredits: 7})
	}
	now := time.Now().UTC()
	stored, err := repo.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: id, OwnerID: "owner-a", SessionID: "session-a",
		Plan: budget.Plan{Title: "制作计划", Steps: steps}, LimitCredits: limit,
		State: budget.StatePending, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	active, err := repo.DecideProductionBudget(ctx, "owner-a", stored.ID, "human-a", true, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return active
}

// budgetHarness wires the plan store into the Agent's grant service. The plan
// itself is created after the harness returns, through the same Repository.
func budgetHarness(t *testing.T, toolName, capability string) (*generationtool.Executor, chat.ToolCommand, *grantGateway, *sqlite.Repository) {
	t.Helper()
	return policyHarnessConfigured(t, sessionMode{mode: "budget"}, toolName, capability,
		func(service *executiongrant.Service) {
			service.Budgets = service.Grants.(executiongrant.ProductionBudgetStore)
		})
}

func executeBudgetCall(t *testing.T, executor *generationtool.Executor, command chat.ToolCommand, callID string) chat.ToolResult {
	t.Helper()
	command.Call.ID = callID
	command.ConsentWaitDeadline = time.Time{}
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil {
		t.Fatalf("execute %s: %v", callID, err)
	}
	return result
}

func budgetContent(t *testing.T, result chat.ToolResult) map[string]any {
	t.Helper()
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatalf("agent content %q: %v", result.AgentContent, err)
	}
	return content
}

func budgetNumbers(t *testing.T, content map[string]any) (remaining, reserved float64) {
	t.Helper()
	value, ok := content["budget"].(map[string]any)
	if !ok {
		t.Fatalf("tool result carries no budget: %v", content)
	}
	return value["remaining"].(float64), value["reserved"].(float64)
}

// Two planned Agent steps run one after the other inside the approved plan
// without an approval card, and each says what the plan still holds.
func TestBudgetModeRunsPlannedStepsInsideTheLimitWithoutCards(t *testing.T) {
	executor, command, gateway, repo := budgetHarness(t, "refine_image", "text-to-image")
	createActiveBudgetPlan(t, repo, "pb-run", 15, "text-to-image")

	first := executeBudgetCall(t, executor, command, "call-a")
	if first.Suspend || first.NoSubmission || gateway.submitCount != 1 {
		t.Fatalf("first planned step waited: %+v submits=%d", first, gateway.submitCount)
	}
	content := budgetContent(t, first)
	remaining, reserved := budgetNumbers(t, content)
	if remaining != 8 || reserved != 7 {
		t.Fatalf("first step budget = remaining %v reserved %v", remaining, reserved)
	}

	second := executeBudgetCall(t, executor, command, "call-b")
	if second.Suspend || second.NoSubmission || gateway.submitCount != 2 {
		t.Fatalf("second planned step waited: %+v submits=%d", second, gateway.submitCount)
	}
	content = budgetContent(t, second)
	remaining, reserved = budgetNumbers(t, content)
	if remaining != 1 || reserved != 14 {
		t.Fatalf("second step budget = remaining %v reserved %v", remaining, reserved)
	}
	ops := []string{"call-a", "call-b"}
	for _, callID := range ops {
		op, err := repo.LoadAgentOperation(context.Background(), "owner-a", "session-a", "turn-a", callID)
		if err != nil {
			t.Fatal(err)
		}
		grant, err := repo.GetExecutionGrant(context.Background(), "owner-a", op.GrantRequestID)
		if err != nil {
			t.Fatal(err)
		}
		if grant.DecidedBy != executiongrant.BudgetActorPrefix+"pb-run" || !grant.BudgetEligible {
			t.Fatalf("%s grant = %+v", callID, grant)
		}
	}
}

// The third planned step that does not fit the limit stops at its card, and the
// Agent is told the reason and the remaining credits.
func TestBudgetModeSendsAStepBeyondTheLimitToItsCard(t *testing.T) {
	executor, command, gateway, repo := budgetHarness(t, "refine_image", "text-to-image")
	createActiveBudgetPlan(t, repo, "pb-over", 15, "text-to-image")
	for _, callID := range []string{"call-a", "call-b"} {
		if result := executeBudgetCall(t, executor, command, callID); result.NoSubmission {
			t.Fatalf("%s should run inside the budget: %+v", callID, result)
		}
	}
	third := executeBudgetCall(t, executor, command, "call-c")
	if !third.NoSubmission || third.Suspend || gateway.submitCount != 2 {
		t.Fatalf("third step ran: %+v submits=%d", third, gateway.submitCount)
	}
	content := budgetContent(t, third)
	if content["status"] != "awaiting_approval" || content["reason"] != budget.ReasonOverBudget {
		t.Fatalf("third step content = %v", content)
	}
	remaining, reserved := budgetNumbers(t, content)
	if remaining != 1 || reserved != 14 {
		t.Fatalf("third step budget = remaining %v reserved %v", remaining, reserved)
	}
}

// A capability outside the approved plan is the person's to decide even when
// the plan still holds enough credits.
func TestBudgetModeSendsAnOffPlanStepToItsCard(t *testing.T) {
	executor, command, gateway, repo := budgetHarness(t, "create_model_3d_from_image", "image-to-3d")
	createActiveBudgetPlan(t, repo, "pb-off-plan", 100, "text-to-image")
	result := executeBudgetCall(t, executor, command, "call-a")
	if !result.NoSubmission || result.Suspend || gateway.submitCount != 0 {
		t.Fatalf("off-plan step ran: %+v submits=%d", result, gateway.submitCount)
	}
	content := budgetContent(t, result)
	if content["reason"] != budget.ReasonOffPlan {
		t.Fatalf("off-plan content = %v", content)
	}
	if _, ok := content["budget"]; !ok {
		t.Fatalf("off-plan content omits the plan: %v", content)
	}
}

// Without an approved plan a manual step still waits, and the reason tells the
// Agent to propose one first.
func TestBudgetModeWithoutAPlanSendsTheStepToItsCardWithNoPlan(t *testing.T) {
	executor, command, gateway, _ := budgetHarness(t, "create_model_3d_from_image", "image-to-3d")
	result := executeBudgetCall(t, executor, command, "call-a")
	if !result.NoSubmission || gateway.submitCount != 0 {
		t.Fatalf("no-plan step ran: %+v submits=%d", result, gateway.submitCount)
	}
	content := budgetContent(t, result)
	if content["reason"] != budget.ReasonNoPlan {
		t.Fatalf("no-plan content = %v", content)
	}
}

// A repeat of a charged-but-unstored attempt is never approved by the plan: it
// still waits on its card, and no reservation is written (#294, #103).
func TestBudgetModeNeverCoversARechargeAttempt(t *testing.T) {
	tasks := &sessionTasks{tasks: []generation.Task{
		unstoredTask("task_old", "text-to-image", time.Now().Add(-time.Hour)),
	}}
	executor, command, gateway, repo := policyHarnessWithTasks(t, sessionMode{mode: "budget"},
		"refine_image", "text-to-image", func(service *executiongrant.Service) {
			service.Budgets = service.Grants.(executiongrant.ProductionBudgetStore)
		}, tasks)
	createActiveBudgetPlan(t, repo, "pb-recharge", 100, "text-to-image")
	result := executeBudgetCall(t, executor, command, "call-a")
	if !result.NoSubmission || result.Suspend || gateway.submitCount != 0 {
		t.Fatalf("a recharge attempt ran: %+v submits=%d", result, gateway.submitCount)
	}
	content := budgetContent(t, result)
	if content["status"] != "awaiting_approval" {
		t.Fatalf("recharge content = %v", content)
	}
	op, err := repo.LoadAgentOperation(context.Background(), "owner-a", "session-a", "turn-a", "call-a")
	if err != nil {
		t.Fatal(err)
	}
	if summary, found, err := repo.BudgetSummaryForGrant(context.Background(), "owner-a", op.GrantRequestID); err != nil || found {
		t.Fatalf("recharge attempt reserved credits: %+v found=%v err=%v", summary, found, err)
	}
}
