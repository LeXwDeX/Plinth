package catalog_test

import (
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// multiviewRuleKey is the sentence that defines what a three-view and a
// four-view request produce. It lives in the three-view craft brief only
// (#550, one place per rule as #407 asks).
func multiviewRuleKey(t *testing.T, chat catalog.ChatSchema) string {
	t.Helper()
	var recipe *catalog.ChatSkillRecipeSchema
	for index := range chat.Skills.Recipes {
		if chat.Skills.Recipes[index].ID == chat.ThreeView.RecipeID {
			recipe = &chat.Skills.Recipes[index]
		}
	}
	if recipe == nil || len(recipe.Steps) != 4 || !recipe.Steps[3].Optional {
		t.Fatalf("three-view recipe = %+v", recipe)
	}
	labels := make([]string, 0, len(recipe.Steps))
	for _, step := range recipe.Steps {
		labels = append(labels, step.Label)
	}
	// The views and their order come from the recipe the three-view runtime runs.
	return "三视图是" + strings.Join(labels[:3], "、") + "，四视图再加" + labels[3]
}

// #550: a person who says three-view or four-view gets one picture per view by
// default. The rule is stated once, in the three-view craft brief, and names the
// views in the recipe's order.
func TestThreeViewCraftDefaultsToOnePicturePerView(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := product.Application().Chat
	var craft string
	for _, prompt := range chat.Agent.CraftPrompts {
		if prompt.ID == "three-view" {
			craft = prompt.Text
		}
	}
	if craft == "" {
		t.Fatal("the three-view craft brief is missing")
	}
	key := multiviewRuleKey(t, chat)
	for _, want := range []string{
		key,
		"不开概念图数量卡",             // the count is already known
		"只有用户明确要一张图",           // the composite is an explicit exception
		"只说设定图",                // a plain design-sheet request keeps today's behavior
		"没有源图时先用 refine_image", // a text-only request draws one front source first
		"estimated_credits",    // the total comes from the quote, not from the Agent
		"create_three_view",
	} {
		if !strings.Contains(craft, want) {
			t.Errorf("the three-view craft brief lacks %q", want)
		}
	}
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(raw), key); count != 1 {
		t.Errorf("the rule %q appears %d times in the Catalog, want exactly once", key, count)
	}
	for _, composite := range []string{"并排画在一张图上", "画在一张图上", "拼在一张图上", "画在同一张图上"} {
		if strings.Contains(string(raw), composite) {
			t.Errorf("the Catalog still offers a composite by default: %q", composite)
		}
	}
	for _, tool := range chat.Tools {
		if tool.Name == "refine_image" && !strings.Contains(tool.Description, "create_three_view") {
			t.Error("refine_image does not point several views of one subject to create_three_view")
		}
	}
}

// #550: the three-view and four-view system Skills are the hand-picked entries
// to the same rule. Their prompts point at the craft brief instead of restating
// it, and both run the same three-view recipe.
func TestThreeAndFourViewSystemSkillsPointAtTheCraftRule(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	skills := map[string]catalog.SkillSchema{}
	for _, skill := range product.Application().Chat.Skills.SystemSkills {
		skills[skill.ID] = skill
	}
	for id, view := range map[string]string{"system-three-view": "背面", "system-four-view": "右侧"} {
		skill, ok := skills[id]
		if !ok {
			t.Fatalf("system Skill %q is missing", id)
		}
		if skill.Tool != "create_three_view" || !skill.RequiresImage || !skill.OneShot || skill.DefaultMessage == "" {
			t.Errorf("%s = %+v", id, skill)
		}
		if !strings.Contains(skill.Prompt, "三视图制作要领") || !strings.Contains(skill.Prompt, view) {
			t.Errorf("%s prompt does not point at the craft rule or name %q: %q", id, view, skill.Prompt)
		}
	}
}
