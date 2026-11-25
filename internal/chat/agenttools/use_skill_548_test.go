package agenttools

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func useSkillCall(t *testing.T, executor *Executor, configuration *chat.AgentConfigurationSnapshot, name string) (chat.ToolResult, map[string]string) {
	t.Helper()
	arguments, _ := json.Marshal(map[string]string{"name": name})
	result := executor.useSkill(chat.ToolCommand{
		Call:          agents.ToolCall{ID: "call-1", Name: ToolUseSkill, Arguments: arguments},
		Configuration: configuration,
	})
	var content map[string]string
	if result.AgentContent != "" {
		if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
			t.Fatal(err)
		}
	}
	return result, content
}

// #548: use_skill reads one Skill of the turn's frozen table by its standard
// name. It returns the body behind the Schema's fixed preamble, records the
// Skill as used by the Agent, and never submits anything.
func TestUseSkillReturnsTheFrozenBodyBehindThePreamble(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	skills := product.Application().Chat.Skills
	directory := skills.Directory
	executor := &Executor{skills: skills}
	long := strings.Repeat("步", directory.MaxInstructionCharacters+50)
	configuration := &chat.AgentConfigurationSnapshot{Skills: append(append([]catalog.SkillSchema{}, skills.SystemSkills...),
		catalog.SkillSchema{ID: "personal-0123456789abcdef0123456789abcdef", Name: "水彩", Prompt: "柔和的水彩笔触。纸纹。"},
		catalog.SkillSchema{ID: "personal-00000000000000000000000000000001", Name: "长做法", Slug: "long-howto", Apply: catalog.SkillApplyInstruction, Prompt: long},
		catalog.SkillSchema{ID: "shared-style", Name: "共享", Slug: "shared-style", Prompt: "共享画风。"},
	)}

	result, content := useSkillCall(t, executor, configuration, "three-view")
	if !result.NoSubmission || result.Error != "" {
		t.Fatalf("result = %+v", result)
	}
	if content["skill_id"] != "system-three-view" || content["name"] != "three-view" {
		t.Fatalf("content = %v", content)
	}
	if !strings.HasPrefix(content["instructions"], directory.InstructionPreamble+"\n\n") ||
		!strings.HasSuffix(content["instructions"], skills.SystemSkills[0].Prompt) {
		t.Fatalf("instructions = %q", content["instructions"])
	}
	used := chat.UsedSkills([]chat.ToolCall{{ID: "call-1", Name: ToolUseSkill, Summary: result.Summary}})
	if len(used) != 1 || used[0].ID != "system-three-view" || used[0].DisplayName != "三视图" || used[0].Source != "agent" || !used[0].Applied {
		t.Fatalf("used skills = %+v", used)
	}

	// An instruction body is cut to the Schema bound.
	_, content = useSkillCall(t, executor, configuration, "long-howto")
	body := strings.TrimPrefix(content["instructions"], directory.InstructionPreamble+"\n\n")
	if utf8.RuneCountInString(body) != directory.MaxInstructionCharacters {
		t.Fatalf("instruction body has %d characters, want %d", utf8.RuneCountInString(body), directory.MaxInstructionCharacters)
	}

	// A style Skill (a legacy row: derived slug) returns how to apply it.
	slug := catalog.SkillSlug(configuration.Skills[len(configuration.Skills)-3])
	_, content = useSkillCall(t, executor, configuration, slug)
	if !strings.Contains(content["instructions"], directory.GenerationArgument+"="+slug) || !strings.Contains(content["instructions"], "水彩") {
		t.Fatalf("style instructions = %q", content["instructions"])
	}

	// Unknown names and published (shared) Skills are not readable.
	for _, name := range []string{"no-such-skill", "shared-style"} {
		result, _ := useSkillCall(t, executor, configuration, name)
		if result.Error != strings.ReplaceAll(directory.Messages.Unknown, "{name}", name) || len(result.Summary) != 0 {
			t.Errorf("%s: result = %+v", name, result)
		}
	}
}
