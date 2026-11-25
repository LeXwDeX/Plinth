package agenttools

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #621: a Skill the Agent read on its own carries the Schema note that it is
// "used this turn", not the person's pick; reading the person's own pick of
// the turn carries no such note.
func TestUseSkillNotesASkillTheAgentChoseItself(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	skills := product.Application().Chat.Skills
	note := skills.Directory.Messages.AgentRead
	executor := &Executor{skills: skills}
	style := catalog.SkillSchema{ID: "personal-0123456789abcdef0123456789abcdef", Name: "骑砍图标", Slug: "mb-icon-style", Prompt: "骑砍风格的物品图标。"}
	configuration := &chat.AgentConfigurationSnapshot{Skills: append(append([]catalog.SkillSchema{}, skills.SystemSkills...), style)}
	read := func(picked, name string) map[string]string {
		var content map[string]string
		if err := json.Unmarshal([]byte(pickedUseSkill(executor, configuration, picked, name).AgentContent), &content); err != nil {
			t.Fatal(err)
		}
		return content
	}
	for _, name := range []string{"mb-icon-style", "three-view"} {
		if content := read("", name); note == "" || content["note"] != note {
			t.Errorf("%s read by the Agent: note %q, want %q", name, content["note"], note)
		}
	}
	if content := read("system-four-view", "four-view"); content["note"] != "" {
		t.Errorf("the person's tool pick carries the Agent note %q", content["note"])
	}
	if content := read(style.ID, "mb-icon-style"); content["note"] != "" {
		t.Errorf("the person's style pick carries the Agent note %q", content["note"])
	}
}
