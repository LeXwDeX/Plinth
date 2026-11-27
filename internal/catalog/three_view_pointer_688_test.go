package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #688: the three- and four-view Skills, and the selected-Skill directive,
// tell the Agent what to do when the person points at "this character" but
// gave no picture: look in the history and project assets, then ask for the
// picture or the canvas instead of asking for a description. Both locales say
// so.
func TestThreeViewSkillsHandleAPointerWithoutPicture(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := product.Application()
	skills := app.Chat.Skills
	en := app.Localization.Bundles["en"].Fields
	check := func(name, zh, english string) {
		t.Helper()
		for _, want := range []string{"find_history_images", "list_project_assets", "以画板为主", "@画布", "不要先让用户描述"} {
			if !strings.Contains(zh, want) {
				t.Fatalf("%s (zh) lacks %q", name, want)
			}
		}
		for _, want := range []string{"find_history_images", "list_project_assets", "Canvas first", "do not ask them to describe"} {
			if !strings.Contains(english, want) {
				t.Fatalf("%s (en) lacks %q", name, want)
			}
		}
	}
	for _, id := range []string{"system-three-view", "system-four-view"} {
		found := false
		for _, skill := range skills.SystemSkills {
			if skill.ID == id {
				found = true
				check(id, skill.Prompt, en["chat.skills.system_skills[id="+id+"].prompt"])
			}
		}
		if !found {
			t.Fatalf("%s is not declared", id)
		}
	}
	directive, englishDirective := skills.ToolDirectiveTemplate, en["chat.skills.tool_directive_template"]
	for _, want := range []string{"以画板为主", "不要先让用户描述"} {
		if !strings.Contains(directive, want) {
			t.Fatalf("tool directive (zh) lacks %q", want)
		}
	}
	for _, want := range []string{"Canvas first", "instead of asking them to describe"} {
		if !strings.Contains(englishDirective, want) {
			t.Fatalf("tool directive (en) lacks %q", want)
		}
	}
}
