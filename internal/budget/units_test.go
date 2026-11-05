package budget_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

var (
	tripo30   = budget.Amount{Unit: "tripo.credit", UnitLabel: "Tripo credits", Micros: 30000000}
	hyperHalf = budget.Amount{Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", Micros: 500000}
)

// #120: a plan's quotes are totalled per unit; amounts of different units are
// never added, and a limit only covers its own unit.
func TestPlanTotalsAndCoverageStayPerUnit(t *testing.T) {
	plan := budget.Plan{Title: "p", Steps: []budget.PlanStep{
		{Capability: "text-to-3d", Estimate: tripo30},
		{Capability: "mesh-segmentation", Estimate: hyperHalf},
		{Capability: "mesh-segmentation", Estimate: hyperHalf},
		{Capability: "retexture", Estimate: budget.Legacy(4)},
		{Capability: "format-conversion", Estimate: budget.Legacy(0)},
	}}
	wantTotals := []budget.Amount{{Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", Micros: 1000000}, budget.Legacy(4), tripo30}
	if totals := plan.EstimatedTotals(); !reflect.DeepEqual(totals, wantTotals) {
		t.Fatalf("totals = %+v", totals)
	}
	// 100 legacy credits do not cover 30 Tripo credits.
	uncovered := plan.Uncovered([]budget.Amount{budget.Legacy(100), {Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", Micros: 1000000}})
	if len(uncovered) != 1 || uncovered[0] != tripo30 {
		t.Fatalf("uncovered = %+v", uncovered)
	}
	if missing := plan.Uncovered(wantTotals); len(missing) != 0 {
		t.Fatalf("exact limits left %+v uncovered", missing)
	}
}

// Plan JSON stored before #120 has an integer estimated_credits without a
// unit: it reads as legacy credits with the same number.
func TestPlanStepReadsALegacyEstimateIntoTheLegacyBucket(t *testing.T) {
	var plan budget.Plan
	if err := json.Unmarshal([]byte(`{"title":"旧","steps":[{"capability":"text-to-3d","estimated_credits":25},
		{"capability":"retexture","estimate":{"unit":"hyper3d.credit","unit_label":"Hyper3D credits","amount":"0.5"}}]}`), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Steps[0].Estimate != budget.Legacy(25) || plan.Steps[1].Estimate != hyperHalf {
		t.Fatalf("steps = %+v", plan.Steps)
	}
	encoded, err := json.Marshal(plan.Steps[1])
	if err != nil || string(encoded) != `{"capability":"retexture","estimate":{"unit":"hyper3d.credit","unit_label":"Hyper3D credits","amount":"0.5"}}` {
		t.Fatalf("encoded step = %s err=%v", encoded, err)
	}
}

func TestAmountsKeepExactDecimalsAndWholeLegacyCredits(t *testing.T) {
	amount, err := budget.ParseAmount("hyper3d.credit", "Hyper3D credits", "2.5")
	if err != nil || amount.Micros != 2500000 || amount.Decimal() != "2.5" {
		t.Fatalf("native amount = %+v err=%v", amount, err)
	}
	if _, err := budget.ParseAmount(budget.LegacyUnit, budget.LegacyUnit, "2.5"); !errors.Is(err, budget.ErrAmount) {
		t.Fatalf("fractional legacy credits: %v", err)
	}
	for _, limits := range [][]budget.Amount{
		nil,
		{tripo30, tripo30},
		{{Unit: "tripo.credit", UnitLabel: "Tripo credits"}},
	} {
		if _, err := budget.NormalizeLimits(limits); !errors.Is(err, budget.ErrAmount) {
			t.Fatalf("limits %+v: %v", limits, err)
		}
	}
	format := budget.AmountFormat{LegacyLabel: "历史积分估算", Separator: " · "}
	if text := format.List([]budget.Amount{hyperHalf, budget.Legacy(40), tripo30}); text != "0.5 Hyper3D credits · 40 历史积分估算 · 30 Tripo credits" {
		t.Fatalf("formatted = %q", text)
	}
}

// A native Offering is quoted exactly in its own unit for plans; the prepared
// input keeps its frozen whole-credit estimate for the input hash.
func TestEstimateOfferingKeepsTheNativeUnitAndAmount(t *testing.T) {
	offering := schema.Offering{NativePricing: &schema.NativePricing{Version: 1, Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", BaseAmount: "0.5"}}
	if got := budget.EstimateOffering(offering); got != hyperHalf {
		t.Fatalf("plan quote = %+v", got)
	}
	if got := budget.EstimateOfferingCredits(offering); got != 1 {
		t.Fatalf("prepared whole-credit estimate = %d", got)
	}
	quote := &schema.NativeQuote{Version: 1, Unit: "tripo.credit", UnitLabel: "Tripo credits", BaseAmount: "20", ExtraAmount: "10", TotalAmount: "30"}
	if got := budget.QuoteAmount(quote, 30); got != tripo30 {
		t.Fatalf("frozen native quote = %+v", got)
	}
	if got := budget.QuoteAmount(nil, 7); got != budget.Legacy(7) {
		t.Fatalf("frozen legacy quote = %+v", got)
	}
}

// A plan is exhausted only when every unit charged its whole limit.
func TestProductionBudgetIsExhaustedOnlyWhenEveryUnitIs(t *testing.T) {
	production := budget.ProductionBudget{Limits: []budget.Amount{hyperHalf, tripo30}}
	partly := budget.Totals{"tripo.credit": {Spent: 30000000}}
	if production.ExhaustedBy(partly) {
		t.Fatal("one used-up unit exhausted the plan")
	}
	partly["hyper3d.credit"] = budget.UnitTotals{Spent: 500000}
	if !production.ExhaustedBy(partly) {
		t.Fatal("a plan with every unit used up is not exhausted")
	}
	summary := budget.SummaryOf(production, budget.Totals{"tripo.credit": {Spent: 10000000, Reserved: 25000000}})
	tripo, _ := summary.Unit("tripo.credit")
	if len(summary.Units) != 2 || tripo.Remaining != 0 || tripo.Spent != 10000000 {
		t.Fatalf("summary = %+v", summary.Units)
	}
}
