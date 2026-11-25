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
	body := skill.Prompt
	if skill.Kind() == catalog.SkillKindStyle {
		body = strings.NewReplacer(
			"{name}", catalog.SkillDisplayName(skill),
			"{summary}", catalog.SkillSummary(skill.Prompt, e.skills.StyleSummaryMaxCharacters),
			"{argument}", directory.GenerationArgument,
			"{slug}", slug,
		).Replace(directory.StyleInstructionTemplate)
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
			ID: skill.ID, DisplayName: catalog.SkillDisplayName(skill), Source: "agent", Applied: true,
		}),
	}
}
