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

// #129: a planned step whose values would be over the session purpose's
// budget carries the note on the stored plan (the plan card reads it) and in
// the Agent's plan receipt; a step that fits carries none.
func TestProposeProductionPlanCarriesThePurposeBudgetNoteOfEachStep(t *testing.T) {
	store := &recordingProductionPlans{}
	service := unitProposalService(t, store)
	var checked []string
	service.SetProductionPlanPurposeBudget(func(command chat.ToolCommand, offering generation.Offering) string {
		checked = append(checked, string(offering.ID)+"@"+command.SessionID)
		if offering.ID == "tripo-3d" {
			return "Tripo 的面数上限超过用途预算"
		}
		return ""
	})
	result, err := service.ProposeProductionPlan(context.Background(), planCommand(`{"title":"分单位",`+unitPlanSteps+`,
		"budgets":[{"unit":"tripo.credit","amount":40},{"unit":"hyper3d.credit","amount":1.5},{"unit":"legacy.credits","amount":4}]}`))
	if err != nil || result.Error != "" || len(store.created) != 1 {
		t.Fatalf("proposal = %+v err=%v", result, err)
	}
	if len(checked) != 3 || checked[0] != "tripo-3d@session-a" {
		t.Fatalf("checked steps = %v", checked)
	}
	steps := store.plans[0].Steps
	if steps[0].PurposeBudgetNote != "Tripo 的面数上限超过用途预算" || steps[1].PurposeBudgetNote != "" {
		t.Fatalf("stored notes = %+v", steps)
	}
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatal(err)
	}
	receipt := content["steps"].([]any)
	if receipt[0].(map[string]any)["purpose_budget_note"] != "Tripo 的面数上限超过用途预算" {
		t.Fatalf("receipt step = %v", receipt[0])
	}
	if _, noted := receipt[1].(map[string]any)["purpose_budget_note"]; noted {
		t.Fatalf("a fitting step was noted: %v", receipt[1])
	}
}

// lockedOfferings price the default text-to-3d model in historical credits and
// the Tripo model the person can lock in Tripo credits, as the Catalog does.
func lockedOfferings() []generation.Offering {
	return []generation.Offering{
		{ID: "meshy/meshy-7.1/text-to-3d", Revision: "v1", Capability: "text-to-3d", ProviderID: "meshy", ModelID: "meshy-7.1",
			Enabled: true, Default: true, EstimatedCredits: 40},
		{ID: "tripo/tripo-v3.1-full/text-to-3d", Revision: "v1", Capability: "text-to-3d", ProviderID: "tripo", ModelID: "tripo-v3.1-full",
			Enabled: true, CreditsMetered: true,
			NativePricing: &schema.NativePricing{Version: 1, Unit: "tripo.credit", UnitLabel: "Tripo credits", BaseAmount: "10"}},
		{ID: "retexture-default", Revision: "v1", Capability: "retexture", Enabled: true, Default: true, EstimatedCredits: 15},
	}
}

func lockedPlanCommand(arguments string) chat.ToolCommand {
	command := planCommand(arguments)
	command.Options.ExplicitModel3D = "tripo-v3.1-full"
	return command
}

// #144: the person's explicit 3D model runs the 3D step (it outranks the
// default and the Agent's model), so the plan quotes that model and pins the
// step to it. Quoted at the default instead, the plan budgeted another unit
// and the approved plan could not cover the step that actually ran.
func TestProposeProductionPlanQuotesTheLockedModel3D(t *testing.T) {
	proposal := func(command chat.ToolCommand) (*recordingProductionPlans, chat.ToolResult) {
		t.Helper()
		store := &recordingProductionPlans{}
		service := &chat.Service{}
		service.SetProductionPlans(chat.ProductionPlanPolicy{Budget: shippedBudgetSchema(t), Offerings: lockedOfferings, Store: store})
		result, err := service.ProposeProductionPlan(context.Background(), command)
		if err != nil {
			t.Fatal(err)
		}
		return store, result
	}
	steps := `"steps":[{"capability":"text-to-3d","purpose":"主体"},{"capability":"retexture","purpose":"贴图"}]`

	store, result := proposal(lockedPlanCommand(`{"title":"锁定模型",` + steps + `,
		"budgets":[{"unit":"tripo.credit","amount":20},{"unit":"legacy.credits","amount":15}]}`))
	if result.Error != "" || len(store.created) != 1 {
		t.Fatalf("proposal = %+v", result)
	}
	planned := store.plans[0].Steps
	tripo := budget.Amount{Unit: "tripo.credit", UnitLabel: "Tripo credits", Micros: 10000000}
	if planned[0].OfferingID != "tripo/tripo-v3.1-full/text-to-3d" || planned[0].Estimate != tripo {
		t.Fatalf("locked step = %+v, want the Tripo Offering quoted at %+v", planned[0], tripo)
	}
	if planned[1].OfferingID != "" || planned[1].Estimate != budget.Legacy(15) {
		t.Fatalf("retexture step = %+v, want the unlocked default", planned[1])
	}
	if !store.plans[0].Allows("text-to-3d", "tripo/tripo-v3.1-full/text-to-3d") {
		t.Fatal("the approved plan must allow the locked model's step")
	}

	store, result = proposal(lockedPlanCommand(`{"title":"锁定模型",` + steps + `,"budgets":[{"unit":"legacy.credits","amount":80}]}`))
	if want := "budgets must give every unit the steps are quoted in at least the server estimate of that unit (15 legacy.credits, 10 tripo.credit); missing or too low: 10 tripo.credit. Units are never added or converted"; result.Error != want || len(store.created) != 0 {
		t.Fatalf("legacy-only budget: refusal = %q stored=%d, want %q", result.Error, len(store.created), want)
	}

	store, result = proposal(lockedPlanCommand(`{"title":"锁定模型","steps":[{"capability":"text-to-3d","model":"meshy-7.1","purpose":"主体"}],
		"budgets":[{"unit":"legacy.credits","amount":40}]}`))
	if want := "the person locked the 3D model to tripo-v3.1-full; step text-to-3d must use it, not meshy-7.1"; result.Error != want || len(store.created) != 0 {
		t.Fatalf("another model: refusal = %q stored=%d, want %q", result.Error, len(store.created), want)
	}

	store, result = proposal(lockedPlanCommand(`{"title":"锁定模型","steps":[{"capability":"text-to-3d","model":"tripo/tripo-v3.1-full","purpose":"主体"}],
		"budgets":[{"unit":"tripo.credit","amount":10}]}`))
	if result.Error != "" || store.plans[0].Steps[0].OfferingID != "tripo/tripo-v3.1-full/text-to-3d" {
		t.Fatalf("the locked model by its provider/model name: %+v %+v", result, store.plans)
	}

	store, result = proposal(planCommand(`{"title":"未锁定",` + steps + `,"budgets":[{"unit":"legacy.credits","amount":55}]}`))
	if result.Error != "" || store.plans[0].Steps[0].OfferingID != "" || store.plans[0].Steps[0].Estimate != budget.Legacy(40) {
		t.Fatalf("without a lock the default still quotes the step: %+v %+v", result, store.plans)
	}
}
