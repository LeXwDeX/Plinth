package chat_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #402: the plan's acceptance criteria are the 3D purpose's delivery standard
// (triangles, texture size, UV, pivot...). A plan whose steps all make pictures
// has no 3D delivery, so its card must not list them. The decision follows the
// steps' Offerings: a plan is picture-only when every step's Offering is in the
// image domain. Anything else, including an Offering that declares no domain,
// keeps the 3D criteria exactly as before.

func acceptanceOfferings402() []generation.Offering {
	return []generation.Offering{
		{ID: "image-default", Revision: "v1", Capability: "text-to-image", Domain: generation.DomainImage, Enabled: true, Default: true, EstimatedCredits: 5},
		{ID: "image-edit", Revision: "v1", Capability: "image-to-image", Domain: generation.DomainImage, Enabled: true, Default: true, EstimatedCredits: 6},
		{ID: "three-default", Revision: "v1", Capability: "image-to-3d", Domain: generation.DomainModel3D, Enabled: true, Default: true, EstimatedCredits: 7},
		{ID: "remesh-default", Revision: "v1", Capability: "remesh", Domain: generation.DomainProcessing, Enabled: true, Default: true, EstimatedCredits: 6},
		// A historical Offering that declares no domain is not known to be a picture.
		{ID: "undeclared-default", Revision: "v1", Capability: "undeclared-image", Enabled: true, Default: true, EstimatedCredits: 4},
	}
}

func acceptanceService402(t *testing.T, store chat.ProductionPlanStore) *chat.Service {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	service := &chat.Service{}
	service.SetProductionPlans(chat.ProductionPlanPolicy{
		Budget:      application.Chat.ApprovalPolicy.Budget,
		Offerings:   acceptanceOfferings402,
		Store:       store,
		Checkpoints: []string{"self_check"},
		Usage:       application.Chat.Model3DUsage,
		Production:  application.Chat.ModelReview.Inspect.Production,
		Candidates:  application.Chat.Candidates,
	})
	return service
}

// acceptanceOf proposes one plan and returns what the server froze and what the
// Agent was told about its acceptance.
func acceptanceOf(t *testing.T, steps string, brief chat.ProductionBrief) ([]budget.AcceptanceCriterion, any) {
	t.Helper()
	store := &recordingProductionPlans{}
	service := acceptanceService402(t, store)
	command := planCommandWithBrief(`{"title":"计划","steps":`+steps+`,"budgets":[{"unit":"legacy.credits","amount":60}]}`, brief)
	plan, content := propose(t, service, store, command)
	if content["success"] == false {
		t.Fatalf("plan refused: %v", content)
	}
	return plan.Acceptance, content["acceptance"]
}

func criteriaText402(criteria []budget.AcceptanceCriterion) string {
	parts := make([]string, 0, len(criteria))
	for _, criterion := range criteria {
		parts = append(parts, criterion.ID+"="+criterion.Statement)
	}
	return strings.Join(parts, " | ")
}

func TestAPlanOfPicturesOnlyFreezesNoThreeDAcceptance_402(t *testing.T) {
	steps := `[{"capability":"text-to-image","purpose":"概念图"},{"capability":"image-to-image","purpose":"改背景"},{"capability":"text-to-image","purpose":"第二张"},{"capability":"text-to-image","purpose":"第三张"}]`
	for name, brief := range map[string]chat.ProductionBrief{
		// The 3D purpose the Issue's session carried, a 2D purpose, and no brief
		// (the Schema's default 3D profile): none of them puts 3D criteria on a
		// plan that never makes a model.
		"a 3D purpose in the brief": {"purpose": json.RawMessage(`"game_character"`)},
		"a 2D purpose in the brief": {"image_use": json.RawMessage(`"illustration"`)},
		"no brief":                  nil,
	} {
		t.Run(name, func(t *testing.T) {
			criteria, told := acceptanceOf(t, steps, brief)
			if len(criteria) != 0 {
				t.Fatalf("a pictures-only plan froze 3D acceptance: %s", criteriaText402(criteria))
			}
			if list, ok := told.([]any); told != nil && (!ok || len(list) != 0) {
				t.Fatalf("the Agent was told acceptance %v for a pictures-only plan", told)
			}
		})
	}
}

func TestAPlanWithAThreeDStepKeepsItsAcceptance_402(t *testing.T) {
	brief := chat.ProductionBrief{"purpose": json.RawMessage(`"game_character"`)}
	// The reference: a plan of 3D steps only, which was never changed.
	threeD, _ := acceptanceOf(t, `[{"capability":"image-to-3d","purpose":"生成角色"}]`, brief)
	if len(threeD) == 0 {
		t.Fatal("a 3D plan froze no acceptance")
	}
	text := criteriaText402(threeD)
	for _, wanted := range []string{"purpose=", "游戏/实时·角色", "triangles=", "60000", "uv="} {
		if !strings.Contains(text, wanted) {
			t.Fatalf("3D acceptance lost %q: %s", wanted, text)
		}
	}

	for name, steps := range map[string]string{
		"pictures then a model":       `[{"capability":"text-to-image","purpose":"概念图"},{"capability":"image-to-3d","purpose":"生成角色"}]`,
		"a model then processing":     `[{"capability":"image-to-3d","purpose":"生成角色"},{"capability":"remesh","purpose":"减面"}]`,
		"pictures then processing":    `[{"capability":"text-to-image","purpose":"概念图"},{"capability":"remesh","purpose":"减面"}]`,
		"processing only":             `[{"capability":"remesh","purpose":"减面"}]`,
		"a step of an unknown domain": `[{"capability":"text-to-image","purpose":"概念图"},{"capability":"undeclared-image","purpose":"历史服务"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			criteria, told := acceptanceOf(t, steps, brief)
			// Byte for byte what the 3D-only plan froze: the criteria come from
			// the purpose and the brief, not from the steps.
			if !reflect.DeepEqual(criteria, threeD) {
				t.Fatalf("acceptance changed:\n got  %s\n want %s", criteriaText402(criteria), text)
			}
			if told == nil {
				t.Fatal("the Agent was told no acceptance")
			}
		})
	}
}
