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

// #407 review: the 2D craft brief must not offer an image edit for removing a
// background. The remove_background tool says why: an image edit changes the
// picture and does not reliably produce real transparency, so the two rules
// would contradict each other.
func TestShippedImageCraftKeepsBackgroundRemovalOnItsTool(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := product.Application()
	var production string
	for _, prompt := range app.Chat.Agent.CraftPrompts {
		if prompt.ID == "image2d_production" {
			production = prompt.Text
		}
	}
	if production == "" {
		t.Fatal("the image2d_production craft brief is missing")
	}
	for _, contradiction := range []string{"去背景、局部修改用图生图", "去背景用图生图", "图生图或 remove_background"} {
		if strings.Contains(production, contradiction) {
			t.Errorf("the 2D craft brief offers an image edit for background removal: %q", contradiction)
		}
	}
	if !strings.Contains(production, "要真正去掉背景时用 remove_background") {
		t.Error("the 2D craft brief does not send background removal to remove_background")
	}
	var described bool
	for _, tool := range app.Chat.Tools {
		if tool.Name == "remove_background" {
			described = described || strings.Contains(tool.Description, "real transparency")
		}
	}
	if !described {
		t.Error("the remove_background description no longer says an image edit gives no real transparency")
	}
	prompt := app.Chat.Agent.SystemPrompt
	if !strings.Contains(prompt, "除停用后换用继任模型外，用户锁定的模型不能被换成别的") {
		t.Error("the system prompt does not reconcile the lock with the retired-model successor")
	}
	if !strings.Contains(prompt, "自检以真实调用为据") {
		t.Error("the system prompt states self-check records for 2D too")
	}
}
