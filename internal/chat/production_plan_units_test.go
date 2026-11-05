package chat_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// unitOfferings price a Tripo model and a Hyper3D model in their own units and
// a retexture in historical credits.
func unitOfferings() []generation.Offering {
	return []generation.Offering{
		{ID: "tripo-3d", Revision: "v1", Capability: "text-to-3d", Enabled: true, Default: true, CreditsMetered: true,
			NativePricing: &schema.NativePricing{Version: 1, Unit: "tripo.credit", UnitLabel: "Tripo credits", BaseAmount: "30"}},
		{ID: "rodin-split", Revision: "v1", Capability: "mesh-segmentation", Enabled: true, Default: true, CreditsMetered: true,
			NativePricing: &schema.NativePricing{Version: 1, Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", BaseAmount: "0.5"}},
		{ID: "retexture-default", Revision: "v1", Capability: "retexture", Enabled: true, Default: true, EstimatedCredits: 4},
	}
}

func unitProposalService(t *testing.T, store chat.ProductionPlanStore) *chat.Service {
	t.Helper()
	service := &chat.Service{}
	service.SetProductionPlans(chat.ProductionPlanPolicy{
		Budget: shippedBudgetSchema(t), Offerings: unitOfferings, Store: store,
	})
	return service
}

const unitPlanSteps = `"steps":[
	{"capability":"text-to-3d","purpose":"主体"},
	{"capability":"mesh-segmentation","purpose":"拆件"},
	{"capability":"retexture","purpose":"贴图"}]`

// #120: the plan quotes every step in its own unit and stores one limit per
// unit; the Agent's amounts of different units never make up for each other.
func TestProposeProductionPlanBudgetsEachUnitSeparately(t *testing.T) {
	store := &recordingProductionPlans{}
	service := unitProposalService(t, store)
	result, err := service.ProposeProductionPlan(context.Background(), planCommand(`{"title":"分单位",`+unitPlanSteps+`,
		"budgets":[{"unit":"tripo.credit","amount":40},{"unit":"hyper3d.credit","amount":1.5},{"unit":"legacy.credits","amount":4}]}`))
	if err != nil || result.Error != "" || len(store.created) != 1 {
		t.Fatalf("proposal = %+v err=%v", result, err)
	}
	tripo := budget.Amount{Unit: "tripo.credit", UnitLabel: "Tripo credits", Micros: 40000000}
	hyper3D := budget.Amount{Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", Micros: 1500000}
	if want := []budget.Amount{hyper3D, budget.Legacy(4), tripo}; !reflect.DeepEqual(store.limits[0], want) {
		t.Fatalf("stored limits = %+v, want %+v", store.limits[0], want)
	}
	steps := store.plans[0].Steps
	if steps[0].Estimate != (budget.Amount{Unit: "tripo.credit", UnitLabel: "Tripo credits", Micros: 30000000}) ||
		steps[1].Estimate != (budget.Amount{Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", Micros: 500000}) ||
		steps[2].Estimate != budget.Legacy(4) {
		t.Fatalf("step quotes = %+v", steps)
	}
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatal(err)
	}
	budgets, _ := content["budgets"].([]any)
	if len(budgets) != 3 || budgets[0].(map[string]any)["amount"] != "1.5" || budgets[0].(map[string]any)["unit"] != "hyper3d.credit" {
		t.Fatalf("tool result budgets = %v", content["budgets"])
	}

	for name, testCase := range map[string]struct {
		budgets string
		want    string
	}{
		"a quoted unit without a budget": {
			`[{"unit":"tripo.credit","amount":100},{"unit":"legacy.credits","amount":100}]`,
			"budgets must give every unit the steps are quoted in at least the server estimate of that unit (0.5 hyper3d.credit, 4 legacy.credits, 30 tripo.credit); missing or too low: 0.5 hyper3d.credit. Units are never added or converted",
		},
		"one unit below its estimate": {
			`[{"unit":"tripo.credit","amount":29},{"unit":"hyper3d.credit","amount":5},{"unit":"legacy.credits","amount":100}]`,
			"budgets must give every unit the steps are quoted in at least the server estimate of that unit (0.5 hyper3d.credit, 4 legacy.credits, 30 tripo.credit); missing or too low: 30 tripo.credit. Units are never added or converted",
		},
		"an unknown unit": {
			`[{"unit":"gold","amount":1}]`,
			"budget unit gold is unknown; use the unit of a step quote (hyper3d.credit, legacy.credits, tripo.credit)",
		},
		"a unit twice": {
			`[{"unit":"tripo.credit","amount":30},{"unit":"tripo.credit","amount":30}]`,
			"budget unit tripo.credit is listed twice",
		},
		"fractional legacy credits": {
			`[{"unit":"legacy.credits","amount":4.5}]`,
			"budget for unit legacy.credits must be a positive decimal amount in whole credits",
		},
		"a zero amount": {
			`[{"unit":"tripo.credit","amount":0}]`,
			"budget for unit tripo.credit must be a positive decimal amount",
		},
	} {
		t.Run(name, func(t *testing.T) {
			store := &recordingProductionPlans{}
			service := unitProposalService(t, store)
			result, err := service.ProposeProductionPlan(context.Background(), planCommand(`{"title":"分单位",`+unitPlanSteps+`,"budgets":`+testCase.budgets+`}`))
			if err != nil || result.Error != testCase.want || len(store.created) != 0 {
				t.Fatalf("refusal = %q err=%v stored=%d, want %q", result.Error, err, len(store.created), testCase.want)
			}
		})
	}
}
