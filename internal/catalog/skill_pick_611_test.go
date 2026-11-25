package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #611: the Schema text behind a hand-picked view Skill. The two directory
// descriptions tell the counts apart and send a four-view request to
// four-view; the turn block names the pick and forbids reading another Skill
// for its tool; with no picture and only a vague subject the Skill asks first
// instead of inventing a look; use_skill has an answer for a sibling of the
// pick. Each text has its English counterpart.
func TestHandPickedViewSkillTextsKeepTheChoice(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	skills := application.Chat.Skills
	english := application.Localization.Bundles["en"].Fields
	byID := map[string]catalog.SkillSchema{}
	for _, skill := range skills.SystemSkills {
		byID[skill.ID] = skill
	}
	three, four := byID["system-three-view"], byID["system-four-view"]
	if !strings.Contains(three.Description, "无右侧") || !strings.Contains(three.Description, "四视图") ||
		!strings.Contains(three.Description, "four-view") || strings.Contains(three.Description, "转面图") {
		t.Errorf("three-view description does not send four views away: %q", three.Description)
	}
	if !strings.Contains(four.Description, "右侧") || !strings.Contains(four.Description, "四个视角") {
		t.Errorf("four-view description = %q", four.Description)
	}
	if en := english["chat.skills.system_skills[id=system-three-view].description"]; !strings.Contains(en, "four-view") ||
		!strings.Contains(en, "no right view") {
		t.Errorf("English three-view description = %q", en)
	}

	directive := skills.ToolDirectiveTemplate
	for _, want := range []string{"{name}", "{slug}", "{prompt}", "{tool}", "以它的做法为准", "不要再用 use_skill"} {
		if !strings.Contains(directive, want) {
			t.Errorf("the turn block lacks %q: %q", want, directive)
		}
	}
	if en := english["chat.skills.tool_directive_template"]; !strings.Contains(en, "{slug}") || !strings.Contains(en, "Do not read another Skill") {
		t.Errorf("English turn block = %q", en)
	}

	for _, id := range []string{"system-three-view", "system-four-view"} {
		first, _, _ := strings.Cut(byID[id].Prompt, "\n")
		for _, want := range []string{"笼统的主体", "先简短问一句", "不要自己设定"} {
			if !strings.Contains(first, want) {
				t.Errorf("%s first line lacks %q: %q", id, want, first)
			}
		}
		en, _, _ := strings.Cut(english["chat.skills.system_skills[id="+id+"].prompt"], "\n")
		if !strings.Contains(en, "vague subject") || !strings.Contains(en, "ask one short question") {
			t.Errorf("%s English first line lacks the ask-first rule: %q", id, en)
		}
	}

	picked := skills.Directory.Messages.Picked
	if !strings.Contains(picked, "{name}") || !strings.Contains(picked, "{slug}") {
		t.Errorf("messages.picked = %q", picked)
	}
	if en := english["chat.skills.directory.messages.picked"]; !strings.Contains(en, "{name}") || !strings.Contains(en, "{slug}") {
		t.Errorf("English messages.picked = %q", en)
	}
	app := product.Application()
	app.Localization = nil
	app.Chat.Skills.Directory.Messages.Picked = ""
	if catalog.ValidateApplicationSchema(app) == nil {
		t.Error("a Schema without messages.picked was accepted")
	}
}
