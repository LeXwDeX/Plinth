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
// their bodies: everything after the Skill's own first line. Since #665 each
// Skill has its own body: the views it makes, the four-view switch and how the
// approval modes continue differ, while the shared rules appear in both.
func systemSkillBodies(t *testing.T, chat catalog.ChatSchema) (map[string]catalog.SkillSchema, map[string]string) {
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
	return skills, bodies
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
	_, bodies := systemSkillBodies(t, chat)
	key := multiviewRuleKey(t, chat)
	for id, body := range bodies {
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
				t.Errorf("the %s Skill body lacks %q", id, want)
			}
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
		if skill.Tool != "create_three_view" || skill.RequiresImage || !skill.OneShot || skill.DefaultMessage == "" {
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

// #593: the three-view and four-view Skills are free: the person's picture
// (attached, selected on the stage or referenced with @) is the source; with
// none the Agent first makes one with 2D text-to-image and confirms which to
// use. The rule leads each prompt, in both languages, and no Skill asks the
// composer for a picture before sending.
func TestThreeAndFourViewSkillsUseTheGivenPictureOrDrawOneFirst(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	skills, bodies := systemSkillBodies(t, application.Chat)
	english := application.Localization.Bundles["en"].Fields
	for id, view := range map[string]string{"system-three-view": "出三视图", "system-four-view": "出四视图"} {
		skill := skills[id]
		first, _, _ := strings.Cut(skill.Prompt, "\n")
		for _, phrase := range []string{"用户已给图", "@ 引用", "就用用户的图", "没给图就先用 2D 文生图出图", "和用户确认用哪张", "再用 create_three_view " + view} {
			if !strings.Contains(first, phrase) {
				t.Errorf("%s first line lacks %q: %q", id, phrase, first)
			}
		}
		if !strings.HasPrefix(first, "先定源图") {
			t.Errorf("%s does not lead with the source rule: %q", id, first)
		}
		if skill.RequiresImage {
			t.Errorf("%s still requires a picture before sending", id)
		}
		en := english["chat.skills.system_skills[id="+id+"].prompt"]
		if !strings.HasPrefix(en, "Settle the source picture first") || !strings.Contains(en, "2D text-to-image") ||
			!strings.Contains(en, "confirm with them which one to use") {
			t.Errorf("%s English prompt lacks the source rule: %q", id, en)
		}
	}
	// The per-view rule is unchanged: one separate picture per view.
	for id, body := range bodies {
		if !strings.Contains(body, "每个视角一张独立图片") || !strings.Contains(body, "用户确认用哪张后再以它为源调用") {
			t.Errorf("%s body lost a rule: %q", id, body)
		}
	}
	// The directive names the tool as the goal, not as the only allowed tool.
	directive := application.Chat.Skills.ToolDirectiveTemplate
	if strings.Contains(directive, "不要用别的工具代替") || strings.Contains(directive, "必须用工具") || !strings.Contains(directive, "没给图时") {
		t.Errorf("the Skill directive still forces the bound tool: %q", directive)
	}
	if en := english["chat.skills.tool_directive_template"]; strings.Contains(en, "Do not substitute another tool") || !strings.Contains(en, "when they gave none") {
		t.Errorf("the English Skill directive still forces the bound tool: %q", en)
	}
}

// #665: each Skill tells the truth about its own views. The three-view Skill
// makes no right view and passes no include_right unless the person asks for
// four views; the four-view Skill passes include_right; both say how each
// approval mode continues after the front view, and how a budget plan covers
// the views (no pinned model; the right view stays on the card).
func TestThreeAndFourViewSkillsMatchTheirOwnViews(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	skills, bodies := systemSkillBodies(t, application.Chat)
	three, four := bodies["system-three-view"], bodies["system-four-view"]
	if three == four {
		t.Fatal("the three-view and four-view Skills still share one body")
	}
	if strings.Contains(three, "依次出左、背、右") || strings.Contains(three, "依次出左侧、背面和右侧") {
		t.Error("the three-view Skill still says it makes the right view")
	}
	for id, want := range map[string][]string{
		"system-three-view": {"不传 include_right", "要四视图", "include_right=true", "严格（逐步批准）", "审批：", "自动："},
		"system-four-view":  {"传 include_right=true", "只要三视图", "右侧", "严格（逐步批准）", "审批："},
	} {
		for _, phrase := range want {
			if !strings.Contains(skills[id].Prompt, phrase) {
				t.Errorf("%s prompt lacks %q", id, phrase)
			}
		}
	}
	for _, body := range bodies {
		if strings.Contains(body, "自动或预算批准方式下服务端在同一回合里接着出完其余视角") {
			t.Error("a Skill still says the budget mode always finishes the views")
		}
	}
	english := application.Localization.Bundles["en"].Fields
	for _, id := range []string{"system-three-view", "system-four-view"} {
		en := english["chat.skills.system_skills[id="+id+"].prompt"]
		if !strings.Contains(en, "include_right") || !strings.Contains(en, "Strict (approve each step)") {
			t.Errorf("%s English prompt lacks the mode rules: %q", id, en)
		}
	}
	// A picked Skill is not read again, and the person's words win over the chip.
	directive := application.Chat.Skills.ToolDirectiveTemplate
	if !strings.Contains(directive, "不要再用 use_skill 读它") || !strings.Contains(directive, "以用户的话为准") {
		t.Errorf("the directive does not resolve a request that differs from the pick: %q", directive)
	}
	if picked := application.Chat.Skills.Directory.Messages.Picked; !strings.Contains(picked, "以用户的话为准") {
		t.Errorf("the picked message does not let the person's words win: %q", picked)
	}
}
