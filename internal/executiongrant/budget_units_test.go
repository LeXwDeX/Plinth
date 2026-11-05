package executiongrant

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// recordingPlanStore keeps the plan CreateProductionPlan stored.
type recordingPlanStore struct {
	fakeProductionBudgets
	created *budget.ProductionBudget
}

func (store *recordingPlanStore) CreateProductionBudget(_ context.Context, value budget.ProductionBudget) (budget.ProductionBudget, error) {
	store.created = &value
	return value, nil
}

func hyper3DQuote(total string) *schema.NativeQuote {
	return &schema.NativeQuote{Version: 1, Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits",
		BaseAmount: total, ExtraAmount: "0", TotalAmount: total}
}

// #120: a native-priced step reserves its exact quote in its own unit, never
// the rounded legacy estimate the prepared input also freezes.
func TestBudgetModeReservesANativeStepInItsOwnUnit(t *testing.T) {
	store, budgets := budgetStep("text-to-image")
	store.snapshot.NativeQuote = hyper3DQuote("0.5")
	store.snapshot.EstimatedCredits = 1
	store.snapshot.CreditsMetered = true
	budgets.found = true
	budgets.summary = budget.Summary{ID: "pb_1", State: budget.StateActive,
		Units: []budget.UnitBudget{budget.NewUnitBudget(budget.Amount{Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", Micros: 3000000}, 0, 0)},
		Plan: budget.Plan{Title: "分单位", Steps: []budget.PlanStep{
			{Capability: "text-to-image", Estimate: budget.Amount{Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", Micros: 500000}},
		}}}
	result, err := runBudgetPolicy(t, "text-to-image", budgets, store)
	if err != nil || result.Outcome != PolicyOutcomeApproved {
		t.Fatalf("native step = %+v err=%v", result, err)
	}
	want := budget.Amount{Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", Micros: 500000}
	if budgets.approve != 1 || budgets.amount != want {
		t.Fatalf("reserved = %+v (approvals %d), want %+v", budgets.amount, budgets.approve, want)
	}

	// A metered native step whose quote is zero cannot be reserved.
	store, budgets = budgetStep("text-to-image")
	store.snapshot.NativeQuote = hyper3DQuote("0")
	store.snapshot.EstimatedCredits = 0
	store.snapshot.CreditsMetered = true
	budgets.found = true
	budgets.summary = budget.Summary{ID: "pb_1", State: budget.StateActive, Units: legacyUnits(100, 0, 0),
		Plan: budget.Plan{Title: "分单位", Steps: []budget.PlanStep{{Capability: "text-to-image", Estimate: budget.Legacy(5)}}}}
	result, err = runBudgetPolicy(t, "text-to-image", budgets, store)
	if !errors.Is(err, ErrHumanRequired) || result.Reason != BudgetReasonUnpriced || budgets.approve != 0 {
		t.Fatalf("unpriced native step = %+v err=%v approvals=%d", result, err, budgets.approve)
	}
}

// A plan is stored only when every unit its steps are quoted in has its own
// limit covering them; amounts of different units never make up for each
// other.
func TestCreateProductionPlanNeedsALimitPerQuotedUnit(t *testing.T) {
	tripo := func(whole int) budget.Amount {
		return budget.Amount{Unit: "tripo.credit", UnitLabel: "Tripo credits", Micros: credits(whole)}
	}
	plan := budget.Plan{Title: "分单位", Steps: []budget.PlanStep{
		{Capability: "text-to-3d", Purpose: "主体", Estimate: tripo(30)},
		{Capability: "retexture", Purpose: "贴图", Estimate: budget.Legacy(10)},
	}}
	for name, limits := range map[string][]budget.Amount{
		"missing unit":              {budget.Legacy(100)},
		"too low in one unit":       {tripo(29), budget.Legacy(100)},
		"duplicate unit":            {tripo(30), tripo(30), budget.Legacy(10)},
		"zero limit":                {tripo(30), budget.Legacy(10), {Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits"}},
		"fractional legacy credits": {tripo(30), {Unit: budget.LegacyUnit, UnitLabel: budget.LegacyUnit, Micros: 10500000}},
	} {
		t.Run(name, func(t *testing.T) {
			store := &recordingPlanStore{}
			service := Service{Budgets: store}
			if _, err := service.CreateProductionPlan(context.Background(), "owner-a", "session-a", "key", "message", "call", plan, limits); !errors.Is(err, ErrInvalid) {
				t.Fatalf("limits %+v: err=%v", limits, err)
			}
			if store.created != nil {
				t.Fatalf("a refused plan was stored: %+v", store.created)
			}
		})
	}
	store := &recordingPlanStore{}
	service := Service{Budgets: store}
	stored, err := service.CreateProductionPlan(context.Background(), "owner-a", "session-a", "key", "message", "call",
		plan, []budget.Amount{tripo(40), budget.Legacy(10)})
	if err != nil || len(stored.Limits) != 2 || stored.Limits[0] != budget.Legacy(10) || stored.Limits[1] != tripo(40) {
		t.Fatalf("stored plan = %+v err=%v", stored, err)
	}
}
