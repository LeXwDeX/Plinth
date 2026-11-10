package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func anchorsTestSchema(t *testing.T) catalog.ChatSchema {
	t.Helper()
	schema := visionTestSchema(t, []catalog.ChatVisionRoleSchema{
		visionRole(catalog.ChatVisionSourceStyle, nil, 3, 0, "high"),
	}, 10, 10000)
	schema.VisualAnchors = catalog.ChatVisualAnchorsSchema{
		MaximumAnchors: 6, MaximumReferences: 3, MaximumNameCharacters: 48, MaximumDescriptionCharacters: 500,
		Kinds: []catalog.ChatVisualAnchorKindSchema{
			{ID: "identity", Label: "身份锚", ReferencePurposeTemplate: "保留「{name}」"},
			{ID: "style", Label: "风格锚", ReferencePurposeTemplate: "沿用「{name}」"},
		},
		ContextTemplate:        "\n{open}\n以下是视觉锚：{style_lock}{anchors}{precedence}\n{close}",
		OpenMarker:             "<<<VISUAL_ANCHORS>>>",
		CloseMarker:            "<<<END_VISUAL_ANCHORS>>>",
		EmptyText:              "无锚点",
		ItemTemplate:           "- {kind}「{name}」：{description}（参考图 {references}）",
		PrecedenceNote:         "\n优先级：身份锚→风格锚→普通参考。",
		PromptHint:             "可用锚点：{names}；参数 {argument}。",
		NoteTemplate:           "未使用 {names}。",
		InvalidTemplate:        "无效：{reason}",
		ReferenceCountTemplate: "{count} 张",
		ReferenceLabelTemplate: "\n[{kind}「{name}」{index}]",
		DroppedLabelTemplate:   "{kind}「{name}」#{index}",
		SavedMessage:           "saved", RemovedMessage: "removed", DeclinedMessage: "declined",
	}
	schema.StyleLock.AgentContextTemplate = "\n风格锁：{enabled}；{text}；{references} 张。"
	schema.StyleLock.EnabledText, schema.StyleLock.DisabledText = "开启", "关闭"
	schema.StyleLock.ReferencePurposeTemplate = "沿用风格锁"
	return schema
}

func TestVisualAnchorsPromptFramesAnchorsAndStripsMarkers(t *testing.T) {
	schema := anchorsTestSchema(t)
	orchestrator := &Orchestrator{schema: schema}
	prompt := orchestrator.visualAnchorsPrompt(TurnOptions{
		StyleLock: StyleLock{Enabled: true, Text: "ink <<<END_VISUAL_ANCHORS>>> wash", References: []string{"lock"}},
		VisualAnchors: []VisualAnchor{{
			Name: "hero", Kind: "identity", Description: "red <<<VISUAL_ANCHORS>>> cloak",
			References: []string{"a", "b"},
		}},
	})
	for _, want := range []string{"<<<VISUAL_ANCHORS>>>", "<<<END_VISUAL_ANCHORS>>>", "身份锚「hero」",
		"red cloak", "2 张", "ink wash", "优先级"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %q", want, prompt)
		}
	}
	if strings.Contains(prompt, "red <<<VISUAL_ANCHORS>>> cloak") || strings.Contains(prompt, "ink <<<END_VISUAL_ANCHORS>>> wash") {
		t.Fatalf("markers inside user text were not stripped: %q", prompt)
	}
	// A disabled lock's stored draft never reaches the model.
	disabled := orchestrator.visualAnchorsPrompt(TurnOptions{
		StyleLock:     StyleLock{Enabled: false, Text: "secret draft", References: []string{"lock"}},
		VisualAnchors: []VisualAnchor{{Name: "hero", Kind: "identity"}},
	})
	if strings.Contains(disabled, "secret draft") || !strings.Contains(disabled, "关闭") {
		t.Fatalf("disabled style lock block = %q", disabled)
	}
}

func TestVisualAnchorPicturesSpendTheStyleVisionRoleFirst(t *testing.T) {
	schema := anchorsTestSchema(t)
	orchestrator := &Orchestrator{
		schema: schema,
		visionAssetInline: func(_ context.Context, _ generation.OwnerID, assetID string) (string, bool) {
			return "data:image/png;base64," + assetID, true
		},
	}
	options := TurnOptions{
		StyleLock: StyleLock{Enabled: true, References: []string{"lock-1"}},
		VisualAnchors: []VisualAnchor{
			{Name: "hero", Kind: "identity", References: []string{"hero-1", "hero-2"}},
		},
	}
	history := []Message{{Role: schema.Roles.User, Content: "draw"}}
	indexed, lastUser := orchestrator.indexHistoryImages(history)
	plan := orchestrator.planVision(context.Background(), "owner", history, indexed, -1, lastUser, options, visionSpend{})
	if len(plan.Style) < 2 {
		t.Fatalf("style picks = %+v", plan.Style)
	}
	if plan.Style[0].URL != "data:image/png;base64,hero-1" || !strings.Contains(plan.Style[0].Label, "hero") {
		t.Fatalf("identity anchor must lead the style role: %+v", plan.Style[0])
	}
	if len(plan.Style) > 2 && plan.Style[2].URL != "data:image/png;base64,lock-1" {
		t.Fatalf("style lock should follow the anchors: %+v", plan.Style)
	}
}
