package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #643: after the same problem survives two fixes the Agent changes the
// approach (another tool, such as the free crop or a local edit, another model
// or smaller steps) or asks, instead of paying for the same attempt again. An
// image edit takes the source picture's ratio when the person gave no board or
// size, instead of the purpose's suggested frame. Each rule lives in one place.
func TestShippedPromptStopsRepeatingTheSameFixAndKeepsTheSourceRatio_643(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	agent := product.Application().Chat.Agent
	repeat := "同一种问题连续两次没修好，不再用相近做法付费重试"
	ratio := "图生图先按源图比例用 set_params 设 aspect_ratio"
	if strings.Count(agent.SystemPrompt, repeat) != 1 {
		t.Errorf("the system prompt does not state the repeated-failure rule once: %q", repeat)
	}
	for _, want := range []string{"免费裁切", "换模型或拆步", "停下问用户"} {
		if !strings.Contains(agent.SystemPrompt, want) {
			t.Errorf("the repeated-failure rule lacks %q", want)
		}
	}
	if strings.Contains(agent.SystemPrompt, ratio) {
		t.Errorf("the system prompt repeats the source-ratio rule of the 2D craft brief")
	}
	found := 0
	for _, prompt := range agent.CraftPrompts {
		if strings.Contains(prompt.Text, repeat) {
			t.Errorf("craft %q repeats the repeated-failure rule of the system prompt", prompt.ID)
		}
		if strings.Contains(prompt.Text, ratio) {
			if prompt.ID != "image2d_production" {
				t.Errorf("craft %q states the source-ratio rule outside the 2D production brief", prompt.ID)
			}
			found++
		}
	}
	if found != 1 {
		t.Errorf("the source-ratio rule appears in %d craft briefs, want 1", found)
	}
}
