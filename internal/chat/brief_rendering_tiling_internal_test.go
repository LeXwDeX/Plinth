package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #287: a tileable purpose renders its tiling requirement, and the prompt
// guidance of a purpose that has any reaches the Agent in the same line; a
// purpose with neither renders exactly what it did before.
func TestBriefPromptRendersTilingAndPromptGuidance(t *testing.T) {
	brief := catalog.ChatProductionBriefSchema{
		Fields: []catalog.ChatProductionBriefField{
			{Name: "use", Type: "profile", Label: "用途"},
			{Name: "image_use", Type: "image_profile", Label: "2D 用途"},
		},
		ContextTemplate: "\n\n【制作简报】{brief}", EmptyText: "none", FieldTemplate: "{label}：{value}",
		FlagTemplate: "{label}", Separator: "；", PurposeTemplate: "{label}（{budget}）",
		ImagePurposeTemplate: "{label}（{requirements}）", ImageRequirementsTemplate: "要求 {items}",
		ImageRequirementSeparator: "、",
	}
	note := catalog.ChatImage2DRequirementsNote{
		Template: "{items}", Separator: "；",
		AspectRatioTemplate: "画幅 {value}", MinimumShortEdgeTemplate: "短边至少 {value} 像素",
		BackgroundTemplate: "背景{value}", SubjectsTemplate: "主体{value}", PoseTemplate: "姿势 {value}",
		BackgroundTexts: map[string]string{"any": "不限"},
		SubjectsTexts:   map[string]string{"single": "单个"},
		TileableText:    "必须可无缝平铺", GuidanceTemplate: "提示词要点：{value}",
	}
	orchestrator := &Orchestrator{schema: catalog.ChatSchema{
		Model3DUsage: catalog.ChatModel3DUsageSchema{Brief: brief},
		Image2DUsage: catalog.ChatImage2DUsageSchema{
			DefaultProfile: "texture_asset",
			Profiles: []catalog.ChatImage2DUsageProfile{
				{
					ID: "texture_asset", Label: "贴图素材", Reason: "贴图。",
					PromptGuidance: "正面平视，均匀漫射光",
					Requirements: catalog.ChatImage2DRequirements{
						AspectRatio: "1:1", MinimumShortEdge: 1024, Background: "any", Subjects: "single", Tileable: true,
					},
				},
				{
					ID: "concept_art", Label: "概念设定图", Reason: "概念。",
					Requirements: catalog.ChatImage2DRequirements{
						AspectRatio: "16:9", MinimumShortEdge: 1024, Background: "any", Subjects: "single",
					},
				},
			},
			RequirementsNote: note,
		},
	}}
	texture := orchestrator.briefPrompt(TurnOptions{ProductionBrief: ProductionBrief{"image_use": json.RawMessage(`"texture_asset"`)}})
	want := "2D 用途：贴图素材（要求 画幅 1:1、短边至少 1024 像素、背景不限、主体单个、必须可无缝平铺、提示词要点：正面平视，均匀漫射光）"
	if !strings.Contains(texture, want) {
		t.Fatalf("texture brief omits %q:\n%s", want, texture)
	}
	concept := orchestrator.briefPrompt(TurnOptions{ProductionBrief: ProductionBrief{"image_use": json.RawMessage(`"concept_art"`)}})
	if strings.Contains(concept, "平铺") || strings.Contains(concept, "提示词要点") {
		t.Fatalf("a purpose that is not tileable rendered a tiling item or guidance:\n%s", concept)
	}
}
