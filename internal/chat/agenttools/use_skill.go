package agenttools

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// ToolUseSkill reads one Skill of the turn's directory (#548).
const ToolUseSkill = "use_skill"

// useSkill returns the body of one Skill of the turn's frozen configuration.
// An instruction or tool Skill returns its prompt, cut to the Schema bound; a
// style Skill returns its gist and how to apply it to a generation call. The
// body always follows the Schema's fixed preamble: it is reference text and
// changes no approval rule, tool permission or system rule. Read-only.
func (e *Executor) useSkill(command chat.ToolCommand) chat.ToolResult {
	directory := e.skills.Directory
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(command.Call.Arguments, &args); err != nil || !directory.Enabled() {
		return chat.ToolResult{Error: "invalid use_skill arguments", NoSubmission: true}
	}
	frozen := e.skills.SystemSkills
	if command.Configuration != nil {
		frozen = command.Configuration.Skills
	}
	system := make(map[string]bool, len(e.skills.SystemSkills))
	for _, skill := range e.skills.SystemSkills {
		system[skill.ID] = true
	}
	skill, found := chat.DirectorySkill(frozen, system, args.Name)
	if !found {
		return chat.ToolResult{Error: strings.ReplaceAll(directory.Messages.Unknown, "{name}", strings.TrimSpace(args.Name)), NoSubmission: true}
	}
	slug := catalog.SkillSlug(skill)
	// The person's tool-binding pick decides how its tool is used (#611): a
	// sibling bound to the same tool is not read and not marked used, and the
	// pick read again is recorded as the person's.
	source := "agent"
	if picked, ok := chat.PickedToolSkill(frozen, command.Options.SkillID); ok && skill.Tool == picked.Tool {
		if skill.ID != picked.ID {
			return chat.ToolResult{AgentContent: strings.NewReplacer(
				"{name}", catalog.SkillDisplayName(picked), "{slug}", slug,
			).Replace(directory.Messages.Picked), NoSubmission: true}
		}
		source = "user"
	}
	body := skill.Prompt
	if skill.Kind() == catalog.SkillKindStyle {
		body = strings.NewReplacer(
			"{name}", catalog.SkillDisplayName(skill),
			"{summary}", catalog.SkillSummary(skill.Prompt, e.skills.StyleSummaryMaxCharacters),
			"{argument}", directory.GenerationArgument,
			"{slug}", slug,
		).Replace(directory.StyleInstructionTemplate)
		// #620: which models can apply it, so the call names one of them the
		// first time (or says none can) instead of trying model after model.
		var models []string
		if e.styleSkillModels != nil {
			models = e.styleSkillModels()
		}
		if line := directory.StyleModelsText(models); line != "" {
			body += "\n" + line
		}
	} else if utf8.RuneCountInString(body) > directory.MaxInstructionCharacters {
		body = string([]rune(body)[:directory.MaxInstructionCharacters])
	}
	content, err := json.Marshal(map[string]string{
		"skill_id":     skill.ID,
		"name":         slug,
		"instructions": directory.InstructionPreamble + "\n\n" + body,
	})
	if err != nil {
		return chat.ToolResult{Error: "use_skill result unavailable", NoSubmission: true}
	}
	return chat.ToolResult{
		AgentContent: string(content),
		NoSubmission: true,
		Summary: chat.UsedSkillSummary(chat.UsedSkill{
			ID: skill.ID, DisplayName: catalog.SkillDisplayName(skill), Source: source, Applied: true,
		}),
	}
}

// recordPickedSkill marks a call of the tool the person's picked Skill binds
// with that Skill as the person's (#611), so the reply shows the Skill the
// person chose even when the Agent read none. A failed call, a call that
// already carries a summary, another tool or no pick records nothing.
func (e *Executor) recordPickedSkill(command chat.ToolCommand, result *chat.ToolResult) {
	if result == nil || result.Error != "" || len(result.Summary) != 0 {
		return
	}
	frozen := e.skills.SystemSkills
	if command.Configuration != nil {
		frozen = command.Configuration.Skills
	}
	picked, ok := chat.PickedToolSkill(frozen, command.Options.SkillID)
	if !ok || picked.Tool != command.Call.Name {
		return
	}
	result.Summary = chat.UsedSkillSummary(chat.UsedSkill{
		ID: picked.ID, DisplayName: catalog.SkillDisplayName(picked), Source: "user", Applied: true,
	})
}
