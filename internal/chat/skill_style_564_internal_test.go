package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #564: a style Skill only reaches the prompt at submission, so the Agent was
// told by the craft brief to add a style "when neither the person nor a Skill
// names one" without knowing which Skill was chosen. The turn's system context
// now names the selected style Skill and a one-sentence summary of it.
func TestSelectedStyleSkillIsNamedInTheSystemContext(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := loaded.Application().Chat
	if strings.TrimSpace(schema.Skills.StyleDirectiveTemplate) == "" {
		t.Fatal("默认 Catalog 未声明 chat.skills.style_directive_template")
	}
	orchestrator := &Orchestrator{schema: schema}
	snapshot := &AgentConfigurationSnapshot{Skills: []catalog.SkillSchema{
		{ID: "watercolor", Name: "Watercolor", NameZH: "水彩",
			Prompt: "柔和的水彩笔触，纸张纹理明显。边缘晕染自然，色彩通透，适合童书插画与概念氛围图，整体低饱和。"},
		{ID: "system-three-view", Name: "Three-view", Prompt: "出三视图。", Tool: "create_three_view"},
	}}
	ctx := context.WithValue(context.Background(), configurationContextKey{}, snapshot)

	block := orchestrator.styleSkillDirective(ctx, TurnOptions{SkillID: "watercolor"})
	if !strings.Contains(block, "水彩") || !strings.Contains(block, "柔和的水彩笔触，纸张纹理明显。") {
		t.Fatalf("the block does not name the Skill and its first sentence: %q", block)
	}
	if strings.Contains(block, "整体低饱和") {
		t.Fatalf("the block carries the whole Skill prompt instead of a summary: %q", block)
	}
	if strings.Contains(block, "{") {
		t.Fatalf("the block kept a placeholder: %q", block)
	}
	// A tool-binding Skill has its own directive; no Skill, nothing.
	if block := orchestrator.styleSkillDirective(ctx, TurnOptions{SkillID: "system-three-view"}); block != "" {
		t.Fatalf("a tool-binding Skill rendered a style block: %q", block)
	}
	if block := orchestrator.styleSkillDirective(ctx, TurnOptions{}); block != "" {
		t.Fatalf("a turn without a Skill rendered a style block: %q", block)
	}
}
