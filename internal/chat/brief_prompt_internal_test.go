package chat

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func briefTestSchema() catalog.ChatProductionBriefSchema {
	return catalog.ChatProductionBriefSchema{
		Fields: []catalog.ChatProductionBriefField{
			{Name: "purpose", Type: "profile", Label: "用途"},
			{Name: "style", Type: "text", Label: "风格"},
			{Name: "needs_rig", Type: "boolean", Label: "需要绑骨"},
			{Name: "triangle_budget", Type: "integer", Label: "三角面预算"},
		},
		ContextTemplate:        "制作简报：\n{brief}",
		EmptyText:              "尚未建立制作简报",
		FieldTemplate:          "{label}：{value}",
		FlagTemplate:           "{label}：是",
		Separator:              "；",
		PurposeTemplate:        "{label}（{budget}）",
		BudgetTemplate:         "三角面 {minimum}–{maximum}{requirements}",
		OpenBudgetTemplate:     "三角面不超过 {maximum}{requirements}",
		OverrideBudgetTemplate: "三角面预算 {value}{requirements}",
		RequirementsTemplate:   "，要求{items}",
		RequirementSeparator:   "、",
		UVText:                 "UV",
		WatertightText:         "水密",
		SkinText:               "蒙皮",
	}
}

func briefTestUsage(schema catalog.ChatProductionBriefSchema) catalog.ChatModel3DUsageSchema {
	minimum := 5000
	maximum := 60000
	return catalog.ChatModel3DUsageSchema{
		DefaultProfile: "render",
		Profiles: []catalog.ChatModel3DUsageProfile{
			{ID: "render", Label: "展示/渲染",
				Budget: catalog.ChatModel3DUsageBudget{MaxTriangles: 2000000}},
			{ID: "game_character", Label: "游戏/实时·角色",
				Budget: catalog.ChatModel3DUsageBudget{
					MinTriangles: &minimum, MaxTriangles: maximum, RequireUV: true,
				}},
		},
		Brief: schema,
	}
}

func briefRaw(t *testing.T, values map[string]any) ProductionBrief {
	t.Helper()
	brief := ProductionBrief{}
	for name, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		brief[name] = encoded
	}
	return brief
}

// #103: every turn carries the session's production brief, or says that there
// is none. The purpose's budget renders once, with a session triangle override
// replacing the purpose's maximum.
func TestBriefPromptRendersTheSessionBriefOrSaysThereIsNone(t *testing.T) {
	schema := briefTestSchema()
	orchestrator := &Orchestrator{schema: catalog.ChatSchema{Model3DUsage: briefTestUsage(schema)}}

	empty := orchestrator.briefPrompt(TurnOptions{})
	if empty != "制作简报：\n尚未建立制作简报" {
		t.Fatalf("empty prompt = %q", empty)
	}

	full := orchestrator.briefPrompt(TurnOptions{ProductionBrief: briefRaw(t, map[string]any{
		"purpose":         "game_character",
		"style":           "写实",
		"needs_rig":       true,
		"triangle_budget": 40000,
	})})
	want := "制作简报：\n用途：游戏/实时·角色（三角面预算 40000，要求UV）；风格：写实；需要绑骨：是"
	if full != want {
		t.Fatalf("brief prompt = %q, want %q", full, want)
	}

	purposeBudget := orchestrator.briefPrompt(TurnOptions{ProductionBrief: briefRaw(t, map[string]any{
		"purpose":   "game_character",
		"needs_rig": false,
	})})
	want = "制作简报：\n用途：游戏/实时·角色（三角面 5000–60000，要求UV）"
	if purposeBudget != want {
		t.Fatalf("purpose budget prompt = %q, want %q", purposeBudget, want)
	}
}

func TestBriefPromptStaysOutWhenTheSchemaDeclaresNoContext(t *testing.T) {
	schema := briefTestSchema()
	schema.ContextTemplate = " "
	orchestrator := &Orchestrator{schema: catalog.ChatSchema{Model3DUsage: briefTestUsage(schema)}}
	if prompt := orchestrator.briefPrompt(TurnOptions{ProductionBrief: briefRaw(t, map[string]any{
		"purpose": "render",
	})}); prompt != "" {
		t.Fatalf("prompt without a context template = %q", prompt)
	}
}
