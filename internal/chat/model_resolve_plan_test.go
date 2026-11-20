package chat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #380 step 1: the plan quotes a step with chat.ResolveModel, the rule the
// generation tools choose their model with. These tests fix the plan's side of
// the contract: the layers, the conflicts and the #417 family substitution.

func resolvePlanService(t *testing.T, store chat.ProductionPlanStore, offerings []generation.Offering,
	usage catalog.ChatModel3DUsageSchema) *chat.Service {
	t.Helper()
	service := &chat.Service{}
	service.SetProductionPlans(chat.ProductionPlanPolicy{
		Budget: shippedBudgetSchema(t),
		Offerings: func() []generation.Offering {
			return offerings
		},
		Store: store,
		Usage: usage,
	})
	return service
}

// familyPlanOfferings are one model family's shapes of image-to-3d, priced
// apart as Meshy's are (the multiview shape charges more).
func familyPlanOfferings() []generation.Offering {
	return []generation.Offering{
		{ID: "meshy/meshy-7.1/image-to-3d", Revision: "v1", ProviderID: "meshy", ModelID: "meshy-7.1",
			ModelFamily: "meshy-7.1", Capability: generation.ImageTo3D, Domain: generation.DomainModel3D,
			Enabled: true, Default: true, EstimatedCredits: 20},
		{ID: "meshy/meshy-7.1-multiview/image-to-3d", Revision: "v1", ProviderID: "meshy", ModelID: "meshy-7.1-multiview",
			ModelFamily: "meshy-7.1", Capability: generation.ImageTo3D, Domain: generation.DomainModel3D,
			Enabled: true, EstimatedCredits: 40},
		{ID: "tripo/tripo-v3.1-multiview/image-to-3d", Revision: "v1", ProviderID: "tripo", ModelID: "tripo-v3.1-multiview",
			ModelFamily: "tripo-v3.1", Capability: generation.ImageTo3D, Domain: generation.DomainModel3D,
			Enabled: true, EstimatedCredits: 10},
	}
}

func lockPlanCommand(arguments, model string) chat.ToolCommand {
	command := planCommand(arguments)
	command.Options = chat.TurnOptions{ExplicitModel3D: model, CreativeDomain: string(generation.DomainModel3D)}
	return command
}

// #417: the person locked Meshy 7.1 and the plan's step names Meshy 7.1
// multiview. That is the same model in the shape the step runs, so the plan
// quotes and pins the multiview Offering instead of refusing the step.
func TestProductionPlanRunsTheLockFamilysShapeForAMultiviewStep(t *testing.T) {
	store := &recordingProductionPlans{}
	service := resolvePlanService(t, store, familyPlanOfferings(), catalog.ChatModel3DUsageSchema{})
	command := lockPlanCommand(`{"title":"多视图","steps":[{"capability":"image-to-3d","model":"meshy-7.1-multiview","purpose":"主体"}],
		"budgets":[{"unit":"legacy.credits","amount":40}]}`, "meshy-7.1")
	result, err := service.ProposeProductionPlan(context.Background(), command)
	if err != nil || result.Error != "" || len(store.created) != 1 {
		t.Fatalf("proposal = %+v err=%v stored=%d", result, err, len(store.created))
	}
	step := store.plans[0].Steps[0]
	if step.OfferingID != "meshy/meshy-7.1-multiview/image-to-3d" || step.Estimate != budget.Legacy(40) {
		t.Fatalf("step = %+v, want the multiview Offering quoted at the plan's price", step)
	}
	if !store.plans[0].Allows("image-to-3d", "meshy/meshy-7.1-multiview/image-to-3d") {
		t.Fatal("the plan must allow the Offering it quoted")
	}
	if store.plans[0].Allows("image-to-3d", "meshy/meshy-7.1/image-to-3d") {
		t.Fatal("the plan pinned the multiview shape; the single-picture shape is a different quote")
	}
}

// A step naming another model family than the person's lock stays refused: the
// lock is the person's decision and the plan may not quote around it.
func TestProductionPlanRefusesAStepOutsideTheLockFamily(t *testing.T) {
	store := &recordingProductionPlans{}
	service := resolvePlanService(t, store, familyPlanOfferings(), catalog.ChatModel3DUsageSchema{})
	command := lockPlanCommand(`{"title":"多视图","steps":[{"capability":"image-to-3d","model":"tripo-v3.1-multiview","purpose":"主体"}],
		"budgets":[{"unit":"legacy.credits","amount":10}]}`, "meshy-7.1")
	result, err := service.ProposeProductionPlan(context.Background(), command)
	want := "the person locked the 3D model to meshy-7.1; step image-to-3d must use it, not tripo-v3.1-multiview"
	if err != nil || result.Error != want || len(store.created) != 0 {
		t.Fatalf("proposal = %q err=%v stored=%d, want %q", result.Error, err, len(store.created), want)
	}
}

// A lock of another domain or capability is not the step's lock: a plan lists
// remesh and processing steps the lock was never about, and refusing them would
// make the whole plan unproposable.
func TestProductionPlanIgnoresALockThatIsNotTheSteps(t *testing.T) {
	offerings := append(familyPlanOfferings(), generation.Offering{
		ID: "meshy/processing/remesh", Revision: "v1", ProviderID: "meshy", ModelID: "meshy-remesh",
		Capability: generation.Remesh, Domain: generation.DomainProcessing, Enabled: true, Default: true,
		EstimatedCredits: 5,
	})
	store := &recordingProductionPlans{}
	service := resolvePlanService(t, store, offerings, catalog.ChatModel3DUsageSchema{})
	command := lockPlanCommand(`{"title":"加工","steps":[{"capability":"image-to-3d","purpose":"主体"},{"capability":"remesh","purpose":"减面"}],
		"budgets":[{"unit":"legacy.credits","amount":25}]}`, "meshy-7.1")
	result, err := service.ProposeProductionPlan(context.Background(), command)
	if err != nil || result.Error != "" || len(store.created) != 1 {
		t.Fatalf("proposal = %+v err=%v stored=%d", result, err, len(store.created))
	}
	steps := store.plans[0].Steps
	// The 3-D step follows the lock; the remesh step has its own default and no
	// family or lock of its own to follow.
	if steps[0].OfferingID != "meshy/meshy-7.1/image-to-3d" || steps[1].OfferingID != "" ||
		steps[1].Estimate != budget.Legacy(5) {
		t.Fatalf("steps = %+v", steps)
	}
}

// The purpose recommendation is a layer of the plan too, so a plan without a
// lock or a named model quotes the model the generation tool will run.
func TestProductionPlanQuotesThePurposeRecommendation(t *testing.T) {
	offerings := familyPlanOfferings()
	usage := catalog.ChatModel3DUsageSchema{
		DefaultProfile: "render",
		Profiles: []catalog.ChatModel3DUsageProfile{{
			ID: "render", Label: "展示", Description: "展示用。", Reason: "展示用途优先完整细节。",
			RecommendedOfferings: map[string][]string{
				chat.ModelShapeImageTo3D: {"meshy/meshy-7.1-multiview/image-to-3d"},
			},
		}},
		Brief: catalog.ChatProductionBriefSchema{Fields: []catalog.ChatProductionBriefField{
			{Name: "purpose", Type: "profile", Label: "用途"},
		}},
	}
	store := &recordingProductionPlans{}
	service := resolvePlanService(t, store, offerings, usage)
	command := planCommand(`{"title":"推荐","steps":[{"capability":"image-to-3d","purpose":"主体"}],
		"budgets":[{"unit":"legacy.credits","amount":40}]}`)
	command.Options = chat.TurnOptions{
		CreativeDomain:  string(generation.DomainModel3D),
		ProductionBrief: chat.ProductionBrief{"purpose": []byte(`"render"`)},
	}
	result, err := service.ProposeProductionPlan(context.Background(), command)
	if err != nil || result.Error != "" || len(store.created) != 1 {
		t.Fatalf("proposal = %+v err=%v stored=%d", result, err, len(store.created))
	}
	step := store.plans[0].Steps[0]
	if step.OfferingID != "meshy/meshy-7.1-multiview/image-to-3d" || step.Estimate != budget.Legacy(40) {
		t.Fatalf("step = %+v, want the purpose's recommendation", step)
	}
}

// A lock the Catalog no longer serves is refused instead of quoted around: the
// plan must not budget a step the turn will refuse.
func TestProductionPlanRefusesARetiredLockInsteadOfQuotingAnotherModel(t *testing.T) {
	offerings := familyPlanOfferings()
	offerings[0].Enabled = false // the locked model is no longer available
	store := &recordingProductionPlans{}
	service := resolvePlanService(t, store, offerings, catalog.ChatModel3DUsageSchema{})
	command := lockPlanCommand(`{"title":"退役","steps":[{"capability":"image-to-3d","purpose":"主体"}],
		"budgets":[{"unit":"legacy.credits","amount":40}]}`, "meshy-7.1")
	result, err := service.ProposeProductionPlan(context.Background(), command)
	if err != nil || result.Error == "" || len(store.created) != 0 ||
		!strings.Contains(result.Error, "the person locked the 3D model to meshy-7.1, which has no available offering") {
		t.Fatalf("proposal = %q err=%v stored=%d", result.Error, err, len(store.created))
	}
}
