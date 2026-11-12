package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #239: the 2D purpose field renders the purpose label and its delivery
// requirements into the system context instead of disappearing from the brief.
func TestBriefPromptRendersTheImagePurposeAndRequirements(t *testing.T) {
	orchestrator := &Orchestrator{schema: catalog.ChatSchema{
		Model3DUsage: catalog.ChatModel3DUsageSchema{Brief: catalog.ChatProductionBriefSchema{
			Fields: []catalog.ChatProductionBriefField{
				{Name: "use", Type: "profile", Label: "用途"},
				{Name: "image_use", Type: "image_profile", Label: "2D 用途"},
				{Name: "image_transparent", Type: "boolean", Label: "透明背景"},
				{Name: "look", Type: "text", Label: "风格"},
			},
			ContextTemplate:           "\n\n【制作简报】{brief}",
			EmptyText:                 "none",
			FieldTemplate:             "{label}：{value}",
			FlagTemplate:              "{label}",
			Separator:                 "；",
			PurposeTemplate:           "{label}（{budget}）",
			BudgetTemplate:            "{minimum}–{maximum}{requirements}",
			OpenBudgetTemplate:        "≤{maximum}{requirements}",
			OverrideBudgetTemplate:    "{value}{requirements}",
			RequirementsTemplate:      "，需 {items}",
			RequirementSeparator:      "、",
			ImagePurposeTemplate:      "{label}（{requirements}）",
			ImageRequirementsTemplate: "要求 {items}",
			ImageRequirementSeparator: "、",
		}},
		Image2DUsage: catalog.ChatImage2DUsageSchema{
			DefaultProfile: "illustration",
			Profiles: []catalog.ChatImage2DUsageProfile{{
				ID: "illustration", Label: "角色立绘", Reason: "立绘需要竖画幅。",
				Requirements: catalog.ChatImage2DRequirements{
					AspectRatio: "2:3", MinimumShortEdge: 1536,
					Background: "solid_or_transparent", Subjects: "single", Pose: "a-pose",
				},
			}},
			RequirementsNote: catalog.ChatImage2DRequirementsNote{
				Template: "{items}", Separator: "；",
				AspectRatioTemplate: "画幅 {value}", MinimumShortEdgeTemplate: "短边至少 {value} 像素",
				BackgroundTemplate: "背景{value}", SubjectsTemplate: "主体{value}", PoseTemplate: "姿势 {value}",
				BackgroundTexts: map[string]string{
					"any": "不限", "solid": "纯色", "transparent": "透明", "solid_or_transparent": "纯色或透明",
				},
				SubjectsTexts: map[string]string{"single": "单个", "multiple": "可多个"},
				PoseTexts:     map[string]string{"a-pose": "A-pose", "t-pose": "T-pose"},
			},
		},
	}}
	brief := ProductionBrief{
		"image_use":         json.RawMessage(`"illustration"`),
		"image_transparent": json.RawMessage(`true`),
		"look":              json.RawMessage(`"watercolor"`),
	}
	prompt := orchestrator.briefPrompt(TurnOptions{ProductionBrief: brief})
	for _, want := range []string{
		"2D 用途：角色立绘（要求 画幅 2:3、短边至少 1536 像素、背景纯色或透明、主体单个、姿势 A-pose）",
		"透明背景",
		"风格：watercolor",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("brief prompt omits %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "illustration") {
		t.Fatalf("brief prompt leaks the purpose id:\n%s", prompt)
	}
}

// #286: a select field renders its option's label, not the stored id, and a
// stored value that is no option (free text written before the field became a
// select) is rendered as it was written instead of being dropped.
func TestBriefPromptRendersSelectLabelsAndKeepsFreeText(t *testing.T) {
	schema := briefTestSchema()
	schema.Fields = []catalog.ChatProductionBriefField{
		{Name: "purpose", Type: "profile", Label: "用途"},
		{Name: "target_engine", Type: "select", Label: "目标引擎", Options: []catalog.ChatProductionBriefOption{
			{ID: "unity", Label: "Unity"}, {ID: "web", Label: "Web / three.js"},
		}},
		{Name: "target_platform", Type: "select", Label: "目标平台", Options: []catalog.ChatProductionBriefOption{
			{ID: "mobile", Label: "手机"}, {ID: "web", Label: "网页"},
		}},
		{Name: "triangle_budget", Type: "integer", Label: "三角面预算"},
	}
	orchestrator := &Orchestrator{schema: catalog.ChatSchema{Model3DUsage: briefTestUsage(schema)}}

	prompt := orchestrator.briefPrompt(TurnOptions{ProductionBrief: briefRaw(t, map[string]any{
		"target_engine": "web", "target_platform": "web",
	})})
	if want := "制作简报：\n目标引擎：Web / three.js；目标平台：网页"; prompt != want {
		t.Fatalf("select prompt = %q, want %q", prompt, want)
	}
	if strings.Contains(prompt, "unity") {
		t.Fatalf("the unset engine option leaked into the prompt: %q", prompt)
	}

	legacy := orchestrator.briefPrompt(TurnOptions{ProductionBrief: briefRaw(t, map[string]any{
		"target_engine": "Unity", "target_platform": "手机 + Switch",
	})})
	if want := "制作简报：\n目标引擎：Unity；目标平台：手机 + Switch"; legacy != want {
		t.Fatalf("free-text prompt = %q, want %q", legacy, want)
	}
}
