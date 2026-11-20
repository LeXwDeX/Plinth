package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #407: the shipped system prompt states the product situation, who the person
// is, what a good result means and who decides what. It does not hand the model
// a persona: a role sentence says nothing the model cannot infer and pushes the
// rules that follow out of the way.
func TestShippedSystemPromptStatesTheSituationInsteadOfAPersona(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	prompt := product.Application().Chat.Agent.SystemPrompt
	for _, forbidden := range []string{"你是 Plinth", "美术总监", "你的角色", "扮演"} {
		if strings.Contains(prompt, forbidden) {
			t.Errorf("the system prompt still sets a persona with %q", forbidden)
		}
	}
	for _, want := range []string{
		"Plinth 是多模型创作平台",
		"用户多是美术、设计或独立开发者",
		"做得好不好只看一件事",
		"决定权：",
		"提问：",
		"自检后交付：",
		"事实与边界：",
		"表达：",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the system prompt lacks %q", want)
		}
	}
	// Each rule lives once (#407): the removed duplicates stay out.
	for _, moved := range []string{
		"propose_production_plan", "kind=next_step", "switchable_operations",
		"topology.inconsistent_winding_edges", "skin_bind_pose",
		"【生产账本】", "设定摘要", "update_session_notes",
		"final_prompt", "prompt_note", "确认制作简报",
	} {
		if strings.Contains(prompt, moved) {
			t.Errorf("the system prompt repeats %q, which lives in a tool description or an injected block", moved)
		}
	}
}
