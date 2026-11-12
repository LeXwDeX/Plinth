package chat

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// briefPlatformUsage is the purpose test Schema with a target platform field and
// two platform variants of the game character (#275).
func briefPlatformUsage() catalog.ChatModel3DUsageSchema {
	schema := briefTestSchema()
	schema.Fields = append(schema.Fields, catalog.ChatProductionBriefField{
		Name: "target_platform", Type: "select", Label: "目标平台",
		Options: []catalog.ChatProductionBriefOption{
			{ID: "mobile", Label: "手机"}, {ID: "pc_console", Label: "PC / 主机"},
		},
	})
	usage := briefTestUsage(schema)
	usage.PlatformField = "target_platform"
	mobile, console := 20000, 100000
	usage.Profiles[1].Platforms = []catalog.ChatModel3DUsagePlatform{
		{IDs: []string{"mobile"}, Budget: catalog.ChatModel3DUsagePlatformBudget{MaxTriangles: &mobile}},
		{IDs: []string{"pc_console"}, Budget: catalog.ChatModel3DUsagePlatformBudget{MaxTriangles: &console}},
	}
	return usage
}

// #275: the purpose renders the budget of the brief's target platform, so the
// Agent plans against the same numbers inspect_model and the plan judge by; a
// brief without a platform (or with a stale free-text one) renders the single
// budget as before, and the brief triangle override still replaces the maximum.
func TestBriefPromptRendersThePlatformBudget(t *testing.T) {
	orchestrator := &Orchestrator{schema: catalog.ChatSchema{Model3DUsage: briefPlatformUsage()}}
	render := func(values map[string]any) string {
		values["purpose"] = "game_character"
		return orchestrator.briefPrompt(TurnOptions{ProductionBrief: briefRaw(t, values)})
	}
	tests := []struct {
		name   string
		values map[string]any
		want   string
	}{
		{"no platform", map[string]any{},
			"制作简报：\n用途：游戏/实时·角色（三角面 5000–60000，要求UV）"},
		{"mobile", map[string]any{"target_platform": "mobile"},
			"制作简报：\n用途：游戏/实时·角色（三角面 5000–20000，要求UV）；目标平台：手机"},
		{"pc and console", map[string]any{"target_platform": "pc_console"},
			"制作简报：\n用途：游戏/实时·角色（三角面 5000–100000，要求UV）；目标平台：PC / 主机"},
		{"a stale free-text platform", map[string]any{"target_platform": "手机 + Switch"},
			"制作简报：\n用途：游戏/实时·角色（三角面 5000–60000，要求UV）；目标平台：手机 + Switch"},
		{"the triangle override replaces the platform maximum",
			map[string]any{"target_platform": "pc_console", "triangle_budget": 40000},
			"制作简报：\n用途：游戏/实时·角色（三角面预算 40000，要求UV）；目标平台：PC / 主机"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := render(test.values); got != test.want {
				t.Fatalf("brief prompt = %q, want %q", got, test.want)
			}
		})
	}
}
