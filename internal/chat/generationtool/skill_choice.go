package generationtool

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

const refusalSkill = "skill_unavailable"

// callSkill is the style Skill one generation call applies (#548) and how it
// came to be: the person's pick for the turn, or the Agent's skill argument.
type callSkill struct {
	// Used is the record shown in the conversation; nil when the call uses no
	// Skill.
	Used *chat.UsedSkill
	// Note tells the Agent that the person's own style Skill replaced the one
	// its call named.
	Note string
	// Refusal is the readable answer to a skill argument that cannot apply:
	// nothing is prepared or submitted.
	Refusal string
}

// resolveCallSkill decides the one style Skill a generation call applies:
//
//	effective = the person's style Skill for the turn ?? the call's skill argument
//
// A tool-binding or instruction Skill the person picked styles nothing, so it
// is cleared here. The person's pick always wins; a different Skill the Agent named is
// ignored and the result says so. An Agent Skill is applied to this call only
// and is written into command.Options.SkillID, so the Grant fingerprint names
// a stable Skill ID next to the argument itself. It reads only the turn's
// frozen configuration and the call's own arguments, so the first submission
// and a resume after an approval resolve the same command and the same
// fingerprint. Nothing here applies the Skill's recommended size.
func (executor *Executor) resolveCallSkill(command chat.ToolCommand) (chat.ToolCommand, callSkill) {
	skills, _ := executor.promptSkills(command)
	if bound, exists := skills[command.Options.SkillID]; exists && bound.Kind() != catalog.SkillKindStyle {
		command.Options.SkillID = ""
	}
	directory := executor.chat.Skills.Directory
	slug := ""
	if argument := directory.GenerationArgument; argument != "" && declaresToolArgument(command.Tool, argument) {
		var arguments map[string]json.RawMessage
		if json.Unmarshal(command.Call.Arguments, &arguments) == nil {
			slug = strings.TrimSpace(rawString(arguments[argument]))
		}
	}
	var frozen []catalog.SkillSchema
	if command.Configuration != nil {
		frozen = command.Configuration.Skills
	}
	system := make(map[string]bool, len(executor.chat.Skills.SystemSkills))
	for _, skill := range executor.chat.Skills.SystemSkills {
		system[skill.ID] = true
	}
	if hand := command.Options.SkillID; hand != "" {
		var result callSkill
		skill, exists := skills[hand]
		if exists {
			result.Used = &chat.UsedSkill{ID: hand, DisplayName: catalog.SkillDisplayName(skill), Source: "user", Applied: true}
		}
		if slug != "" {
			if named, found := chat.DirectorySkill(frozen, system, slug); !found || named.ID != hand {
				name := hand
				if exists {
					name = catalog.SkillDisplayName(skill)
				}
				result.Note = strings.NewReplacer("{name}", name, "{slug}", slug).Replace(directory.Messages.UserChoiceKept)
			}
		}
		return command, result
	}
	if slug == "" {
		return command, callSkill{}
	}
	skill, found := chat.DirectorySkill(frozen, system, slug)
	if !found {
		return command, callSkill{Refusal: strings.ReplaceAll(directory.Messages.Unknown, "{name}", slug)}
	}
	if skill.Kind() != catalog.SkillKindStyle {
		return command, callSkill{Refusal: strings.ReplaceAll(directory.Messages.NotStyle, "{name}", slug)}
	}
	command.Options.SkillID = skill.ID
	return command, callSkill{Used: &chat.UsedSkill{
		ID: skill.ID, DisplayName: catalog.SkillDisplayName(skill), Source: "agent", Applied: true,
	}}
}

type callSkillKey struct{}

// withCallSkill carries the call's resolved Skill to the steps of one Execute:
// the refusal check, the approval evidence and the unsupported-model answer.
func withCallSkill(ctx context.Context, skill *callSkill) context.Context {
	return context.WithValue(ctx, callSkillKey{}, skill)
}

// callSkillFrom is the call's resolved Skill; an Execute that resolved none
// (a processing tool, a direct internal call) reads an empty one.
func callSkillFrom(ctx context.Context) *callSkill {
	if skill, ok := ctx.Value(callSkillKey{}).(*callSkill); ok && skill != nil {
		return skill
	}
	return &callSkill{}
}

// skillUnsupported is the readable refusal of an Agent style Skill on an
// Offering without the Skill feature. The person's own pick keeps its
// existing answer.
func (executor *Executor) skillUnsupported(skill callSkill) string {
	if skill.Used == nil || skill.Used.Source != "agent" {
		return ""
	}
	return strings.ReplaceAll(executor.chat.Skills.Directory.Messages.Unsupported, "{name}", skill.Used.DisplayName)
}

// attachCallSkill records the call's Skill on its result: the note for the
// Agent under the tool's skill_note key, and the used Skill in the call's
// summary, which the conversation reads back after a reload. A refused call
// (nothing submitted because of its Skill) records nothing.
func (executor *Executor) attachCallSkill(command chat.ToolCommand, result *chat.ToolResult, skill callSkill) {
	if skill.Refusal != "" {
		return
	}
	if skill.Note != "" && command.Tool.ResultFields.SkillNote != "" && result.AgentContent != "" {
		var content map[string]any
		if json.Unmarshal([]byte(result.AgentContent), &content) == nil && content != nil {
			content[command.Tool.ResultFields.SkillNote] = skill.Note
			if encoded, err := json.Marshal(content); err == nil {
				result.AgentContent = string(encoded)
			}
		}
	}
	if skill.Used != nil && len(result.Summary) == 0 {
		result.Summary = chat.UsedSkillSummary(*skill.Used)
	}
}

// skillChoiceEvidence is the approval card's line for the call's style Skill.
func skillChoiceEvidence(skill callSkill) *generation.SkillChoice {
	if skill.Used == nil || !skill.Used.Applied {
		return nil
	}
	return &generation.SkillChoice{ID: skill.Used.ID, Name: skill.Used.DisplayName, Source: skill.Used.Source}
}

// declaresToolArgument reports whether a tool's parameters declare a property.
func declaresToolArgument(tool catalog.ChatToolSchema, name string) bool {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(tool.Parameters, &schema) != nil {
		return false
	}
	_, declared := schema.Properties[name]
	return declared
}
