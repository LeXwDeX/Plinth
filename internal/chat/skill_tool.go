package chat

import (
	"context"
	"strings"
)

// skillToolDirective is the system-context block for a turn that chose a
// tool-binding Skill (a Product Schema system Skill). The Skill text and the
// directive template come from the Schema; the block is added only while the
// bound tool is really offered this turn (scope and availability), so the
// Agent is never told to call a tool it cannot use.
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
			"{name}", skill.Name,
			"{prompt}", skill.Prompt,
			"{tool}", skill.Tool,
		).Replace(orchestrator.schema.Skills.ToolDirectiveTemplate)
	}
	return ""
}
