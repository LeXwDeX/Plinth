package chat

import "strings"

// craftPrompt is the domain-brief block of the system context. Every Schema
// craft prompt whose WhenToolsAny names at least one tool offered this turn is
// appended in Schema order, so the Agent receives the 3D craft only on a turn
// that can actually model in 3D. The offered list is the same describe() list
// the model gets, so a tool withheld by the session's Agent scope, a disabled
// Offering or a bound harness never triggers its brief.
func (orchestrator *Orchestrator) craftPrompt(registry *toolRegistry, scope string) string {
	prompts := orchestrator.schema.Agent.CraftPrompts
	if len(prompts) == 0 || registry == nil {
		return ""
	}
	offered := make(map[string]struct{}, len(registry.ordered))
	for _, tool := range registry.describe(scope) {
		offered[tool.Name] = struct{}{}
	}
	var block strings.Builder
	for _, prompt := range prompts {
		if strings.TrimSpace(prompt.Text) == "" {
			continue
		}
		matched := false
		for _, name := range prompt.WhenToolsAny {
			if _, ok := offered[name]; ok {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		block.WriteString("\n\n")
		block.WriteString(prompt.Text)
	}
	return block.String()
}
