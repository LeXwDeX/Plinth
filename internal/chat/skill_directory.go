package chat

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// skillDirectoryPrompt is the Skill directory of the system context (#548):
// one line per Skill with its standard name, display name and description, so
// the Agent decides from those alone whether to read a body with use_skill.
// Only the turn's frozen Skills are listed (the Schema's system Skills when the
// turn has no frozen configuration). A system Skill bound to a tool is
// listed only while that tool is offered this turn; a personal style Skill only
// while a generation tool that takes the style argument is offered. Personal
// Skills fill their own bounded part newest first, and the rest are counted in
// one closing line. The Skill the person picked for this turn is left out: its
// text is already in the turn. Published (shared) Skills carry no description
// and are not listed.
func (orchestrator *Orchestrator) skillDirectoryPrompt(ctx context.Context, registry *toolRegistry, scope string, options TurnOptions) string {
	directory := orchestrator.schema.Skills.Directory
	if !directory.Enabled() || registry == nil {
		return ""
	}
	// A turn without a frozen configuration still has the Schema's system
	// Skills, as every other Skill reader assumes.
	skills := orchestrator.schema.Skills.SystemSkills
	if snapshot := configurationFromContext(ctx); snapshot != nil {
		skills = snapshot.Skills
	}
	offered := map[string]bool{}
	styleArgument := false
	for _, tool := range registry.describe(scope) {
		offered[tool.Name] = true
		styleArgument = styleArgument || declaresArgument(tool.Parameters, directory.GenerationArgument)
	}
	if !offered[directory.Tool] {
		return ""
	}
	system := make(map[string]bool, len(orchestrator.schema.Skills.SystemSkills))
	for _, skill := range orchestrator.schema.Skills.SystemSkills {
		system[skill.ID] = true
	}
	var systemPart strings.Builder
	systemUsed := 0
	var personal []catalog.SkillSchema
	for _, skill := range skills {
		if skill.ID == options.SkillID {
			continue
		}
		switch {
		case system[skill.ID]:
			if skill.Tool != "" && !offered[skill.Tool] {
				continue
			}
			line := catalog.SkillDirectoryEntry(skill, directory)
			length := utf8.RuneCountInString(line)
			if systemUsed+length > directory.MaxSystemCharacters {
				continue
			}
			systemUsed += length
			systemPart.WriteString(line)
		case strings.HasPrefix(skill.ID, catalog.PersonalSkillIDPrefix):
			if skill.Kind() == catalog.SkillKindStyle && !styleArgument {
				continue
			}
			personal = append(personal, skill)
		}
	}
	entries := systemPart.String() + personalDirectoryPart(personal, directory)
	if entries == "" {
		return ""
	}
	return strings.ReplaceAll(directory.Template, "{entries}", strings.TrimSuffix(entries, "\n"))
}

// personalDirectoryPart lists personal Skills newest first (the store returns
// them oldest first) within MaxPersonalCharacters, the omitted line included.
// It stops at the first Skill that does not fit, so the listed ones are always
// the newest.
func personalDirectoryPart(personal []catalog.SkillSchema, directory catalog.ChatSkillDirectorySchema) string {
	if len(personal) == 0 {
		return ""
	}
	lines := make([]string, 0, len(personal))
	total := 0
	for index := len(personal) - 1; index >= 0; index-- {
		line := catalog.SkillDirectoryEntry(personal[index], directory)
		lines = append(lines, line)
		total += utf8.RuneCountInString(line)
	}
	if total <= directory.MaxPersonalCharacters {
		return strings.Join(lines, "")
	}
	omitted := func(count int) string {
		return strings.ReplaceAll(directory.OmittedTemplate, "{count}", strconv.Itoa(count)) + "\n"
	}
	// Reserve room for the closing line at its longest count.
	budget := directory.MaxPersonalCharacters - utf8.RuneCountInString(omitted(len(personal)))
	var part strings.Builder
	used, listed := 0, 0
	for _, line := range lines {
		length := utf8.RuneCountInString(line)
		if used+length > budget {
			break
		}
		used += length
		listed++
		part.WriteString(line)
	}
	part.WriteString(omitted(len(personal) - listed))
	return part.String()
}

// declaresArgument reports whether a tool's JSON parameters declare a property.
func declaresArgument(parameters json.RawMessage, name string) bool {
	if name == "" || len(parameters) == 0 {
		return false
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(parameters, &schema) != nil {
		return false
	}
	_, declared := schema.Properties[name]
	return declared
}

// UsedSkill is the compact record of a Skill a call used, stored in the call's
// summary under UsedSkillSummaryKey. The conversation shows it as "this turn
// used Skill X" and keeps it across reloads, without a table of its own.
type UsedSkill struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	// Source is "user" (the person picked it for the turn) or "agent".
	Source string `json:"source"`
	// Applied is false when the Skill named by the call did not take effect,
	// such as an Agent style Skill the person's own choice replaced.
	Applied bool `json:"applied"`
}

// UsedSkillSummaryKey is the member of a tool call summary that carries the
// UsedSkill of that call.
const UsedSkillSummaryKey = "used_skill"

// UsedSkillSummary is the call summary that records one used Skill.
func UsedSkillSummary(used UsedSkill) json.RawMessage {
	encoded, _ := json.Marshal(map[string]any{UsedSkillSummaryKey: used})
	return encoded
}

// DirectorySkill finds a Skill of the turn's frozen table by its standard name.
// Only the Skills the directory can list are found: the Schema's system Skills
// and the owner's personal Skills. Published (shared) Skills are not listed and
// are never read here.
func DirectorySkill(skills []catalog.SkillSchema, system map[string]bool, slug string) (catalog.SkillSchema, bool) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return catalog.SkillSchema{}, false
	}
	for _, skill := range skills {
		if !system[skill.ID] && !strings.HasPrefix(skill.ID, catalog.PersonalSkillIDPrefix) {
			continue
		}
		if catalog.SkillSlug(skill) == slug {
			return skill, true
		}
	}
	return catalog.SkillSchema{}, false
}

// UsedSkills lists the Skills an assistant message's persisted tool calls
// used (#548), in call order, once per Skill and source. It reads the
// summaries use_skill and the generation tools stored with their calls, so a
// reload shows the same list without a table of its own.
func UsedSkills(calls []ToolCall) []UsedSkill {
	var used []UsedSkill
	seen := map[[2]string]int{}
	for _, call := range calls {
		if len(call.Summary) == 0 {
			continue
		}
		var summary map[string]json.RawMessage
		if json.Unmarshal(call.Summary, &summary) != nil || len(summary[UsedSkillSummaryKey]) == 0 {
			continue
		}
		var item UsedSkill
		if json.Unmarshal(summary[UsedSkillSummaryKey], &item) != nil || item.ID == "" {
			continue
		}
		key := [2]string{item.ID, item.Source}
		if index, found := seen[key]; found {
			used[index].Applied = used[index].Applied || item.Applied
			continue
		}
		seen[key] = len(used)
		used = append(used, item)
	}
	return used
}
