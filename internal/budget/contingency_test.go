package budget_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/budget"
)

func TestContingenciesCountTowardTheApprovedLimit(t *testing.T) {
	plan := budget.Plan{
		Steps: []budget.PlanStep{
			{Capability: "text-to-3d", Estimate: budget.Legacy(10)},
			{Capability: "remesh", Estimate: budget.Legacy(2)},
		},
		Contingencies: []budget.PlanContingency{{
			ID: "pivot", Trigger: []string{"pivot_bottom_center"},
			Step: budget.PlanStep{Capability: "resize", Estimate: budget.Legacy(3)},
		}},
	}
	if got := plan.StepTotals(); len(got) != 1 || got[0].Micros != budget.Legacy(12).Micros {
		t.Fatalf("step totals = %+v", got)
	}
	if got := plan.ContingencyTotals(); len(got) != 1 || got[0].Micros != budget.Legacy(3).Micros {
		t.Fatalf("contingency totals = %+v", got)
	}
	if got := plan.EstimatedTotals(); len(got) != 1 || got[0].Micros != budget.Legacy(15).Micros {
		t.Fatalf("estimated totals = %+v", got)
	}
	if uncovered := plan.Uncovered([]budget.Amount{budget.Legacy(14)}); len(uncovered) != 1 {
		t.Fatalf("a limit below steps+contingencies must be uncovered: %+v", uncovered)
	}
	if uncovered := plan.Uncovered([]budget.Amount{budget.Legacy(15)}); len(uncovered) != 0 {
		t.Fatalf("a worst-case limit covers the plan: %+v", uncovered)
	}
}

func TestAllowsAcceptsADeclaredContingency(t *testing.T) {
	plan := budget.Plan{
		Steps: []budget.PlanStep{{Capability: "text-to-3d", OfferingID: "meshy-7.1"}},
		Contingencies: []budget.PlanContingency{{
			ID: "pivot", Trigger: []string{"pivot_bottom_center"},
			Step: budget.PlanStep{Capability: "resize", OfferingID: "meshy/resize", Estimate: budget.Legacy(3)},
		}},
	}
	if !plan.Allows("resize", "meshy/resize") {
		t.Fatal("a declared contingency's capability and Offering must be allowed")
	}
	if plan.Allows("resize", "another/resize") {
		t.Fatal("a pinned contingency Offering must not accept another Offering")
	}
	if plan.Allows("retexture", "") {
		t.Fatal("an undeclared capability must stay outside the plan")
	}
}

func TestPromptLinesListAcceptanceCheckpointsAndContingencies(t *testing.T) {
	plan := budget.Plan{
		Acceptance: []budget.AcceptanceCriterion{{ID: "triangles", Statement: "三角面不超过 20000"}},
		Checkpoints: []budget.PlanCheckpoint{{
			AfterCapability: "remesh",
			Checks:          []budget.PlanCheck{{Check: "pivot_bottom_center"}},
		}},
		Contingencies: []budget.PlanContingency{{
			ID: "pivot", Trigger: []string{"pivot_bottom_center"},
			Step: budget.PlanStep{Capability: "resize", Estimate: budget.Legacy(3)},
		}},
	}
	if !strings.Contains(plan.AcceptanceLine(), "三角面不超过 20000") {
		t.Fatalf("acceptance line = %q", plan.AcceptanceLine())
	}
	if !strings.Contains(plan.CheckpointLine(), "remesh: pivot_bottom_center") {
		t.Fatalf("checkpoint line = %q", plan.CheckpointLine())
	}
	line := plan.ContingencyLine(budget.AmountFormat{LegacyLabel: "积分", Separator: " · "})
	if !strings.Contains(line, "pivot") || !strings.Contains(line, "resize") {
		t.Fatalf("contingency line = %q", line)
	}
	if _, found := plan.Contingency("pivot"); !found {
		t.Fatal("Contingency must find the declared id")
	}
	if _, found := plan.CheckpointAfter("remesh"); !found {
		t.Fatal("CheckpointAfter must find the declared capability")
	}
}
