package catalog_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #621: the Agent answers in the person's language even when what it reads is
// English, calls a Skill it read itself "used this turn" rather than the
// person's pick, and reads "背包图标" as an inventory item icon.
func TestAgentReplyWordingRules(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := product.Application()
	chat := app.Chat
	system := chat.Agent.SystemPrompt
	// One language rule, and it says English tool results (use_skill bodies,
	// offering knowledge, refusals) do not change the reply language.
	if strings.Count(system, "不要中英混杂") != 1 {
		t.Fatalf("the language rule is stated %d times", strings.Count(system, "不要中英混杂"))
	}
	if rule := languageRule(system); !strings.Contains(rule, "工具结果是英文也不改") {
		t.Errorf("the language rule does not cover English tool results: %q", rule)
	}

	// The Skill copy the Agent reads in the default locale is Chinese: no
	// English sentence leaks into a turn whose prompts are written in it.
	directory := chat.Skills.Directory
	english := regexp.MustCompile(`[A-Za-z]+[ ,.]+[A-Za-z]+[ ,.]+[A-Za-z]+`)
	placeholder := regexp.MustCompile(`\{[a-z_]+\}`)
	for field, text := range map[string]string{
		"template": directory.Template, "style_instruction_template": directory.StyleInstructionTemplate,
		"style_models_template": directory.StyleModelsTemplate, "instruction_preamble": directory.InstructionPreamble,
		"messages.unknown": directory.Messages.Unknown, "messages.not_style": directory.Messages.NotStyle,
		"messages.unsupported": directory.Messages.Unsupported, "messages.user_choice_kept": directory.Messages.UserChoiceKept,
		"messages.picked": directory.Messages.Picked, "messages.no_style_model": directory.Messages.NoStyleModel,
		"messages.agent_read":     directory.Messages.AgentRead,
		"tool_directive_template": chat.Skills.ToolDirectiveTemplate, "style_directive_template": chat.Skills.StyleDirectiveTemplate,
	} {
		if match := english.FindString(placeholder.ReplaceAllString(text, "")); match != "" {
			t.Errorf("skills %s carries English %q", field, match)
		}
	}

	// "你选的" is only for the person's own pick: a Skill the Agent read is
	// "本轮使用", and the copy it reads never calls it the person's.
	if !strings.Contains(directory.Messages.AgentRead, "本轮使用") || !strings.Contains(directory.Messages.AgentRead, "你选的") {
		t.Fatalf("messages.agent_read = %q", directory.Messages.AgentRead)
	}
	for field, text := range map[string]string{
		"style_instruction_template": directory.StyleInstructionTemplate, "instruction_preamble": directory.InstructionPreamble,
		"style_models_template": directory.StyleModelsTemplate, "messages.no_style_model": directory.Messages.NoStyleModel,
		"messages.unsupported": directory.Messages.Unsupported, "template": directory.Template,
	} {
		if strings.Contains(text, "你选的") {
			t.Errorf("Agent-read copy %s calls the Skill the person's pick: %q", field, text)
		}
	}
	useSkill := ""
	briefImageUse := ""
	for _, tool := range chat.AgentTools {
		switch tool.Name {
		case "use_skill":
			useSkill = tool.Description
		case "update_production_brief":
			briefImageUse = string(tool.Parameters)
		}
	}
	if !strings.Contains(useSkill, "used this turn") {
		t.Errorf("use_skill does not say a Skill the Agent read is used this turn: %q", useSkill)
	}

	// "背包图标" is a game inventory item icon: the subject is the item.
	if !strings.Contains(briefImageUse, "inventory") || !strings.Contains(briefImageUse, "背包图标") {
		t.Errorf("update_production_brief image_use does not explain inventory icons")
	}
	for _, profile := range chat.Image2DUsage.Profiles {
		if profile.ID == "icon_prop" && !strings.Contains(profile.Description, "背包") {
			t.Errorf("icon_prop does not cover inventory icons: %q", profile.Description)
		}
	}
}

// languageRule is the bullet of the system prompt that states the reply
// language.
func languageRule(system string) string {
	for _, line := range strings.Split(system, "\n") {
		if strings.Contains(line, "不要中英混杂") {
			return line
		}
	}
	return ""
}
