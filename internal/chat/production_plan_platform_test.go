package chat_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #275: the plan freezes the checkpoint thresholds and the acceptance criteria
// the person approves from the purpose's budget. That budget is the variant for
// the brief's target platform, with the brief's triangle_budget over its
// maximum, the same budget inspect_model judges by.

func platformPlanBrief(values map[string]string, triangleBudget string) chat.ProductionBrief {
	brief := chat.ProductionBrief{}
	for name, value := range values {
		encoded, _ := json.Marshal(value)
		brief[name] = encoded
	}
	if triangleBudget != "" {
		brief["triangle_budget"] = json.RawMessage(triangleBudget)
	}
	return brief
}

func proposeCheckpointPlan(t *testing.T, brief chat.ProductionBrief) budget.Plan {
	t.Helper()
	store := &recordingProductionPlans{}
	service := proposalService(t, store, shippedBudgetSchema(t))
	arguments := `{"title":"角色","steps":[{"capability":"image-to-3d","purpose":"生成角色"},{"capability":"remesh","purpose":"减面"}],` +
		`"budgets":[{"unit":"legacy.credits","amount":20}],` +
		`"checkpoints":[{"after_capability":"remesh","checks":["triangles","texture_size","uv"]}]}`
	plan, content := propose(t, service, store, planCommandWithBrief(arguments, brief))
	if content["success"] == false {
		t.Fatalf("the plan was refused: %v", content)
	}
	return plan
}

func TestPlanCheckpointsFreezeThePlatformBudget(t *testing.T) {
	tests := []struct {
		name    string
		purpose string
		values  map[string]string
		budget  string
		minimum float64
		maximum float64
		texture float64
	}{
		{"no platform keeps the single budget", "game_character", nil, "", 5000, 60000, 2048},
		{"mobile", "game_character", map[string]string{"target_platform": "mobile"}, "", 5000, 20000, 2048},
		{"web", "game_character", map[string]string{"target_platform": "web"}, "", 5000, 20000, 2048},
		{"xr", "game_character", map[string]string{"target_platform": "xr"}, "", 5000, 20000, 2048},
		{"pc and console", "game_character", map[string]string{"target_platform": "pc_console"}, "", 5000, 100000, 4096},
		{"a stale free-text platform is unspecified", "game_character",
			map[string]string{"target_platform": "手机 + Switch"}, "", 5000, 60000, 2048},
		{"the brief triangle budget wins over the platform", "game_character",
			map[string]string{"target_platform": "pc_console"}, "12000", 5000, 12000, 4096},
		{"the brief triangle budget wins without a platform", "game_character", nil, "40000", 5000, 40000, 2048},
		{"a prop on mobile", "game_prop", map[string]string{"target_platform": "mobile"}, "", 300, 8000, 2048},
		{"a prop on pc and console", "game_prop", map[string]string{"target_platform": "pc_console"}, "", 300, 40000, 4096},
		{"a purpose without variants ignores the platform", "render", map[string]string{"target_platform": "mobile"}, "", 0, 2000000, 8192},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := map[string]string{"purpose": test.purpose}
			for name, value := range test.values {
				values[name] = value
			}
			plan := proposeCheckpointPlan(t, platformPlanBrief(values, test.budget))
			if len(plan.Checkpoints) != 1 || len(plan.Checkpoints[0].Checks) != 3 {
				t.Fatalf("checkpoints = %+v", plan.Checkpoints)
			}
			triangles, texture := plan.Checkpoints[0].Checks[0], plan.Checkpoints[0].Checks[1]
			if triangles.Check != "triangles" || triangles.Maximum == nil || *triangles.Maximum != test.maximum {
				t.Fatalf("triangles threshold = %+v, want maximum %v", triangles, test.maximum)
			}
			if test.minimum > 0 {
				if triangles.Minimum == nil || *triangles.Minimum != test.minimum {
					t.Fatalf("triangles threshold = %+v, want minimum %v", triangles, test.minimum)
				}
			} else if triangles.Minimum != nil {
				t.Fatalf("triangles threshold = %+v, want no minimum", triangles)
			}
			if texture.Check != "texture_size" || texture.Maximum == nil || *texture.Maximum != test.texture {
				t.Fatalf("texture threshold = %+v, want maximum %v", texture, test.texture)
			}

			// The approved acceptance criteria state the same standard the
			// checkpoint freezes.
			statements := map[string]string{}
			for _, criterion := range plan.Acceptance {
				statements[criterion.ID] = criterion.Statement
			}
			if !strings.Contains(statements["triangles"], formatPlanNumber(test.maximum)) {
				t.Fatalf("triangle acceptance %q does not state %v", statements["triangles"], test.maximum)
			}
			if !strings.Contains(statements["texture_size"], formatPlanNumber(test.texture)) {
				t.Fatalf("texture acceptance %q does not state %v", statements["texture_size"], test.texture)
			}
		})
	}
}

func formatPlanNumber(value float64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
