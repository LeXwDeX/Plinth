package chat_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #417: a plan step names no parameters, so its quote is the quote it freezes:
// the native base amount plus the components its defaults already pay for.
// Meshy 7.1 multiview quotes 20 + 10 (texture) + 5 (8K) + 5 (2K Ultra geometry)
// = 40, not the bare base amount of 20 the plan card used to show.
func TestProposeProductionPlanQuotesMeshyMultiviewAtItsDefaultQuote(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	offering, ok := product.Get("meshy/meshy-7.1-multiview/image-to-3d")
	if !ok {
		t.Fatal("missing Meshy multiview Offering")
	}
	service := func(store chat.ProductionPlanStore) *chat.Service {
		created := &chat.Service{}
		created.SetProductionPlans(chat.ProductionPlanPolicy{
			Budget:      application.Chat.ApprovalPolicy.Budget,
			Offerings:   func() []generation.Offering { return []generation.Offering{offering} },
			Store:       store,
			Checkpoints: []string{"self_check"},
			Usage:       application.Chat.Model3DUsage,
			Production:  application.Chat.ModelReview.Inspect.Production,
			Candidates:  application.Chat.Candidates,
		})
		return created
	}
	steps := `"steps":[{"capability":"image-to-3d","model":"meshy/meshy-7.1-multiview/image-to-3d","purpose":"主体"}]`

	store := &recordingProductionPlans{}
	result, err := service(store).ProposeProductionPlan(context.Background(),
		planCommand(`{"title":"Meshy 多视图",`+steps+`,"budgets":[{"unit":"meshy.credit","amount":40}]}`))
	if err != nil || result.Error != "" || len(store.plans) != 1 {
		t.Fatalf("proposal = %+v err=%v stored=%d", result, err, len(store.plans))
	}
	want := budget.Amount{Unit: "meshy.credit", UnitLabel: "Meshy credits", Micros: 40000000}
	planned := store.plans[0].Steps
	if len(planned) != 1 || planned[0].OfferingID != "meshy/meshy-7.1-multiview/image-to-3d" || planned[0].Estimate != want {
		t.Fatalf("planned step = %+v, want the Meshy multiview quote %+v", planned, want)
	}
	if totals := store.plans[0].EstimatedTotals(); len(totals) != 1 || totals[0] != want {
		t.Fatalf("plan totals = %+v, want %+v", totals, want)
	}
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatal(err)
	}
	// The plan card and the tool receipt read the same quote.
	totals, _ := content["estimated_totals"].([]any)
	if len(totals) != 1 || totals[0].(map[string]any)["unit"] != "meshy.credit" ||
		totals[0].(map[string]any)["amount"] != "40" {
		t.Fatalf("tool receipt estimated_totals = %v", content["estimated_totals"])
	}
	receipt := content["steps"].([]any)
	if receipt[0].(map[string]any)["estimate"].(map[string]any)["amount"] != "40" {
		t.Fatalf("tool receipt step = %v", receipt[0])
	}

	// A budget below that quote is refused and names the server's estimate.
	store = &recordingProductionPlans{}
	result, err = service(store).ProposeProductionPlan(context.Background(),
		planCommand(`{"title":"Meshy 多视图",`+steps+`,"budgets":[{"unit":"meshy.credit","amount":39}]}`))
	wantRefusal := "budgets must give every unit the steps are quoted in at least the server estimate of that unit (40 meshy.credit); missing or too low: 40 meshy.credit. Units are never added or converted"
	if err != nil || result.Error != wantRefusal || len(store.created) != 0 {
		t.Fatalf("refusal = %q err=%v stored=%d, want %q", result.Error, err, len(store.created), wantRefusal)
	}
}
