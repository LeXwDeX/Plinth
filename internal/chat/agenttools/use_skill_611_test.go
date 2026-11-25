package agenttools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func pickedUseSkill(executor *Executor, configuration *chat.AgentConfigurationSnapshot, picked, name string) chat.ToolResult {
	arguments, _ := json.Marshal(map[string]string{"name": name})
	return executor.useSkill(chat.ToolCommand{
		Call:          agents.ToolCall{ID: "call-1", Name: ToolUseSkill, Arguments: arguments},
		Configuration: configuration,
		Options:       chat.TurnOptions{SkillID: picked},
	})
}

// #611: with four-view picked by hand, use_skill("three-view") neither
// returns the three-view body nor marks the reply "三视图": the answer points
// back at the picked Skill. Reading the picked Skill itself returns its body
// and records it as the person's. A Skill for other work stays readable.
func TestUseSkillKeepsTheHandPickedToolSkill(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	skills := product.Application().Chat.Skills
	executor := &Executor{skills: skills}
	configuration := &chat.AgentConfigurationSnapshot{Skills: append(append([]catalog.SkillSchema{}, skills.SystemSkills...),
		catalog.SkillSchema{ID: "personal-00000000000000000000000000000001", Name: "做法", Slug: "my-howto",
			Apply: catalog.SkillApplyInstruction, Prompt: "先列清单。"})}

	sibling := pickedUseSkill(executor, configuration, "system-four-view", "three-view")
	if !sibling.NoSubmission || sibling.Error != "" || len(sibling.Summary) != 0 {
		t.Fatalf("sibling read = %+v", sibling)
	}
	if strings.Contains(sibling.AgentContent, "正面、左侧、背面各一张") || !strings.Contains(sibling.AgentContent, "四视图") ||
		!strings.Contains(sibling.AgentContent, "three-view") {
		t.Fatalf("sibling answer = %q", sibling.AgentContent)
	}
	if used := chat.UsedSkills([]chat.ToolCall{{ID: "call-1", Name: ToolUseSkill, Summary: sibling.Summary}}); len(used) != 0 {
		t.Fatalf("the refused sibling was marked used: %+v", used)
	}

	own := pickedUseSkill(executor, configuration, "system-four-view", "four-view")
	var content map[string]string
	if err := json.Unmarshal([]byte(own.AgentContent), &content); err != nil || content["skill_id"] != "system-four-view" {
		t.Fatalf("own read = %+v", own)
	}
	used := chat.UsedSkills([]chat.ToolCall{{ID: "call-1", Name: ToolUseSkill, Summary: own.Summary}})
	if len(used) != 1 || used[0].ID != "system-four-view" || used[0].Source != "user" || !used[0].Applied {
		t.Fatalf("own read marked %+v", used)
	}

	other := pickedUseSkill(executor, configuration, "system-four-view", "my-howto")
	if err := json.Unmarshal([]byte(other.AgentContent), &content); err != nil || content["name"] != "my-howto" {
		t.Fatalf("other read = %+v", other)
	}
}

// #611: the call of the picked Skill's tool records that Skill as the
// person's, so the reply is marked with what the person chose even when the
// Agent read nothing. A refused call, another tool or no pick records nothing.
func TestThePickedSkillsToolCallRecordsThePick(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	skills := product.Application().Chat.Skills
	executor := &Executor{skills: skills}
	configuration := &chat.AgentConfigurationSnapshot{Skills: skills.SystemSkills}
	command := func(tool, picked string) chat.ToolCommand {
		return chat.ToolCommand{Call: agents.ToolCall{ID: "call-1", Name: tool}, Configuration: configuration,
			Options: chat.TurnOptions{SkillID: picked}}
	}
	result := chat.ToolResult{AgentContent: "ok"}
	executor.recordPickedSkill(command(ToolCreateThreeView, "system-four-view"), &result)
	used := chat.UsedSkills([]chat.ToolCall{{ID: "call-1", Name: ToolCreateThreeView, Summary: result.Summary}})
	if len(used) != 1 || used[0].ID != "system-four-view" || used[0].DisplayName != "四视图" || used[0].Source != "user" || !used[0].Applied {
		t.Fatalf("used = %+v", used)
	}
	for name, cmd := range map[string]chat.ToolCommand{
		"no pick":    command(ToolCreateThreeView, ""),
		"other tool": command(ToolInspectImage, "system-four-view"),
	} {
		result := chat.ToolResult{AgentContent: "ok"}
		executor.recordPickedSkill(cmd, &result)
		if len(result.Summary) != 0 {
			t.Errorf("%s recorded %s", name, result.Summary)
		}
	}
	failed := chat.ToolResult{Error: "three-view skill unavailable"}
	executor.recordPickedSkill(command(ToolCreateThreeView, "system-four-view"), &failed)
	if len(failed.Summary) != 0 {
		t.Errorf("a failed call recorded %s", failed.Summary)
	}
}
