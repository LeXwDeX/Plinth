package chat

import (
	"context"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// skillToolDirective is the system-context block for a turn that chose a
// tool-binding Skill (a Product Schema system Skill). The Skill text and the
// directive template come from the Schema; the block is added only while the
// bound tool is really offered this turn (scope and availability), so the
// Agent is never told to call a tool it cannot use. The block names the pick
// by its display name and standard name (#611), the same names the directory
// and the used-Skill mark show.
func (orchestrator *Orchestrator) skillToolDirective(ctx context.Context, registry *toolRegistry, options TurnOptions) string {
	if options.SkillID == "" {
		return ""
	}
	snapshot := configurationFromContext(ctx)
	if snapshot == nil {
		return ""
	}
	for _, skill := range snapshot.Skills {
		if skill.ID != options.SkillID || skill.Tool == "" {
			continue
		}
		offered := false
		for _, tool := range registry.describe(orchestrator.agentScope.Normalize(options.AgentScope)) {
			if tool.Name == skill.Tool {
				offered = true
				break
			}
		}
		if !offered {
			return ""
		}
		return strings.NewReplacer(
			"{name}", catalog.SkillDisplayName(skill),
			"{slug}", catalog.SkillSlug(skill),
			"{prompt}", skill.Prompt,
			"{tool}", skill.Tool,
		).Replace(orchestrator.schema.Skills.ToolDirectiveTemplate)
	}
	return ""
}

// styleSkillDirective names the style Skill the turn chose (#564). A style
// Skill is applied to the generation prompt at submission, so the Agent needs
// its name and gist to keep the prompt it writes from adding a second style.
// The block is left out for a tool-binding Skill (skillToolDirective speaks for
// it), for no Skill, and when the Schema declares no template.
func (orchestrator *Orchestrator) styleSkillDirective(ctx context.Context, options TurnOptions) string {
	template := orchestrator.schema.Skills.StyleDirectiveTemplate
	if options.SkillID == "" || strings.TrimSpace(template) == "" {
		return ""
	}
	snapshot := configurationFromContext(ctx)
	if snapshot == nil {
		return ""
	}
	for _, skill := range snapshot.Skills {
		if skill.ID != options.SkillID {
			continue
		}
		if skill.Kind() != catalog.SkillKindStyle {
			return ""
		}
		name := skill.NameZH
		if strings.TrimSpace(name) == "" {
			name = skill.Name
		}
		return strings.NewReplacer(
			"{name}", name,
			"{summary}", skillSummary(skill.Prompt, orchestrator.schema.Skills.StyleSummaryMaxCharacters),
		).Replace(template)
	}
	return ""
}

// skillSummary is the first sentence of a Skill prompt, at most maximum
// characters: enough to know the style without repeating the prompt the
// server applies itself.
func skillSummary(prompt string, maximum int) string {
	return catalog.SkillSummary(prompt, maximum)
}
