package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #611: with the four-view Skill picked by hand, the Agent read three-view
// from the directory and the reply was marked "三视图". A hand-picked Skill
// bound to a tool keeps every other Skill bound to the same tool out of the
// directory, and the turn's block names the picked Skill by its display name
// and standard name and tells the Agent not to read another Skill for that
// tool.
func TestHandPickedToolSkillKeepsItsSiblingsOutOfTheTurn(t *testing.T) {
	orchestrator, schema := scopeTestOrchestrator(t)
	snapshot := &AgentConfigurationSnapshot{Skills: append(append([]catalog.SkillSchema{}, schema.Skills.SystemSkills...),
		personalStyle(1, "水彩"))}
	ctx := context.WithValue(context.Background(), configurationContextKey{}, snapshot)
	for _, picked := range []struct{ id, slug, name, sibling string }{
		{"system-four-view", "four-view", "四视图", "three-view"},
		{"system-three-view", "three-view", "三视图", "four-view"},
	} {
		options := TurnOptions{SkillID: picked.id, AgentScope: "2d"}
		directory := orchestrator.skillDirectoryPrompt(ctx, orchestrator.registry, "2d", options)
		for _, slug := range []string{picked.slug, picked.sibling} {
			if strings.Contains(directory, "- "+slug+"（") {
				t.Errorf("%s picked: the directory still lists %s:\n%s", picked.id, slug, directory)
			}
		}
		// Skills for other work stay readable.
		if !strings.Contains(directory, "（水彩）") {
			t.Errorf("%s picked: the personal style Skill left the directory:\n%s", picked.id, directory)
		}
		block := orchestrator.skillToolDirective(ctx, orchestrator.registry, options)
		for _, want := range []string{"「" + picked.name + "」", "（" + picked.slug + "）", "不要再用 use_skill"} {
			if !strings.Contains(block, want) {
				t.Errorf("%s picked: the turn block lacks %q:\n%s", picked.id, want, block)
			}
		}
	}
}

// #611: the hand-picked Skill, not a sibling the Agent read, is the turn's
// Skill: PickedToolSkill finds it only when it binds a tool.
func TestPickedToolSkillFindsOnlyAToolBindingPick(t *testing.T) {
	_, schema := scopeTestOrchestrator(t)
	skills := append(append([]catalog.SkillSchema{}, schema.Skills.SystemSkills...), personalStyle(1, "水彩"))
	if skill, found := PickedToolSkill(skills, "system-four-view"); !found || skill.Slug != "four-view" {
		t.Fatalf("four-view pick = %+v %v", skill, found)
	}
	for _, id := range []string{"", personalStyle(1, "水彩").ID, "no-such-skill"} {
		if _, found := PickedToolSkill(skills, id); found {
			t.Errorf("%q was taken for a tool-binding pick", id)
		}
	}
}
