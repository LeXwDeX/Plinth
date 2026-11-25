package catalog_test

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// multiviewRuleKey is the sentence that defines what a three-view and a
// four-view request produce. Since #548 it lives in the body of the three-view
// and four-view system Skills, one copy each, and the craft brief keeps only
// the routing sentence.
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

// systemSkillBodies returns the three-view and four-view system Skills and
// the shared body of their prompts: everything after the Skill's own first
// line.
func systemSkillBodies(t *testing.T, chat catalog.ChatSchema) (map[string]catalog.SkillSchema, string) {
	t.Helper()
	skills := map[string]catalog.SkillSchema{}
	for _, skill := range chat.Skills.SystemSkills {
		skills[skill.ID] = skill
	}
	bodies := map[string]string{}
	for _, id := range []string{"system-three-view", "system-four-view"} {
		skill, ok := skills[id]
		if !ok {
			t.Fatalf("system Skill %q is missing", id)
		}
		_, body, found := strings.Cut(skill.Prompt, "\n")
		if !found || strings.TrimSpace(body) == "" {
			t.Fatalf("%s has no body after its first line: %q", id, skill.Prompt)
		}
		bodies[id] = body
	}
	// Both Skills carry the same instructions word for word (#548): there is
	// no reference mechanism, so the test keeps the two copies equal.
	if bodies["system-three-view"] != bodies["system-four-view"] {
		t.Fatal("the three-view and four-view Skills no longer share their body word for word")
	}
	return skills, bodies["system-three-view"]
}

// #550, #548: a person who says three-view or four-view gets one picture per
// view by default. The rule now lives in the three-view and four-view Skills;
// the craft brief that is always present keeps only the routing sentence with
// the rule that must hold before the Agent acts.
func TestThreeViewRouteStaysResidentAndTheRuleLivesInTheSkills(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := product.Application().Chat
	var route string
	for _, prompt := range chat.Agent.CraftPrompts {
		if prompt.ID == "three-view" {
			route = prompt.Text
			if len(prompt.WhenToolsAny) != 1 || prompt.WhenToolsAny[0] != "create_three_view" {
				t.Errorf("the three-view route follows %v, want only create_three_view", prompt.WhenToolsAny)
			}
		}
	}
	if route == "" {
		t.Fatal("the three-view routing sentence is missing")
	}
	for _, want := range []string{"三视图或四视图", "每个视角一张独立图", "use_skill", "create_three_view"} {
		if !strings.Contains(route, want) {
			t.Errorf("the routing sentence lacks %q: %q", want, route)
		}
	}
	if length := utf8.RuneCountInString(route); length > 80 {
		t.Errorf("the routing sentence is %d characters, want at most 80", length)
	}
	if strings.Contains(route, "【三视图制作要领】") {
		t.Error("the three-view craft brief is still resident")
	}
	_, body := systemSkillBodies(t, chat)
	key := multiviewRuleKey(t, chat)
	for _, want := range []string{
		key,
		"不开概念图数量卡",             // the count is already known
		"只有用户明确要一张图",           // the composite is an explicit exception
		"只说设定图",                // a plain design-sheet request keeps today's behavior
		"没有源图时先用 refine_image", // a text-only request draws one front source first
		"estimated_credits",    // the total comes from the quote, not from the Agent
		"create_three_view",
		"create_multiview_images",
		"create_model_3d_from_views",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the three-view Skill body lacks %q", want)
		}
	}
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(raw), key); count != 2 {
		t.Errorf("the rule %q appears %d times in the Catalog, want once in each of the two Skills", key, count)
	}
	for _, composite := range []string{"并排画在一张图上", "画在一张图上", "拼在一张图上", "画在同一张图上"} {
		if strings.Contains(string(raw), composite) {
			t.Errorf("the Catalog still offers a composite by default: %q", composite)
		}
	}
	// The usage that used to sit in the resident brief must still be readable
	// without the Skill: each multiview tool says the least about itself.
	for _, tool := range chat.Tools {
		switch tool.Name {
		case "refine_image":
			if !strings.Contains(tool.Description, "create_three_view") {
				t.Error("refine_image does not point several views of one subject to create_three_view")
			}
		case "create_multiview_images":
			if !strings.Contains(tool.Description, "create_three_view") || !strings.Contains(tool.Description, "not a 3D step") {
				t.Errorf("create_multiview_images does not say when it is the wrong tool: %q", tool.Description)
			}
		case "create_model_3d_from_views":
			if !strings.Contains(tool.Description, "three_view_group") || !strings.Contains(tool.Description, "list_offerings") {
				t.Errorf("create_model_3d_from_views does not say how a three-view group becomes 3D: %q", tool.Description)
			}
		}
	}
}

// #550, #548: the three-view and four-view system Skills are both the
// hand-picked entries and the Agent's directory entries. They run the same
// recipe, have a standard name and a description that covers making 3D from
// the views, and their prompts carry the instructions themselves.
func TestThreeAndFourViewSystemSkillsCarryTheInstructions(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	skills, _ := systemSkillBodies(t, product.Application().Chat)
	for id, want := range map[string]struct{ slug, view, asks string }{
		"system-three-view": {"three-view", "背面", "三视图"},
		"system-four-view":  {"four-view", "右侧", "四视图"},
	} {
		skill := skills[id]
		if skill.Tool != "create_three_view" || !skill.RequiresImage || !skill.OneShot || skill.DefaultMessage == "" {
			t.Errorf("%s = %+v", id, skill)
		}
		if skill.Slug != want.slug || !catalog.ValidSkillSlug(skill.Slug) {
			t.Errorf("%s slug = %q, want %q", id, skill.Slug, want.slug)
		}
		if err := catalog.ValidateSkillDescription(skill.Description); err != nil {
			t.Errorf("%s description: %v", id, err)
		}
		for _, phrase := range []string{want.asks, "生成 3D", "时使用"} {
			if !strings.Contains(skill.Description, phrase) {
				t.Errorf("%s description lacks %q: %q", id, phrase, skill.Description)
			}
		}
		first, _, _ := strings.Cut(skill.Prompt, "\n")
		if !strings.Contains(first, want.view) || strings.Contains(skill.Prompt, "【三视图制作要领】") {
			t.Errorf("%s prompt does not name %q or still points at the removed craft brief: %q", id, want.view, first)
		}
	}
}
