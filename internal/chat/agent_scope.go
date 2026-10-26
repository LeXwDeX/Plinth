package chat

import (
	"strings"
)

// scopePrompt is the Agent-scope block of the system context, rebuilt every
// turn from the Schema text, the session's scope and the tools this turn
// really offers. An unrestricted configuration (no scope declared) adds
// nothing.
func (orchestrator *Orchestrator) scopePrompt(registry *toolRegistry, scope string) string {
	if !orchestrator.agentScope.Enabled() {
		return ""
	}
	mode, ok := orchestrator.agentScope.Mode(scope)
	if !ok {
		return ""
	}
	empty := orchestrator.schema.AgentScope.EmptyListText
	list := func(names []string) string {
		if len(names) == 0 {
			return empty
		}
		return strings.Join(names, ", ")
	}
	allowed := make([]string, 0, len(registry.ordered))
	for _, tool := range registry.describe(scope) {
		allowed = append(allowed, tool.Name)
	}
	return strings.NewReplacer(
		"{mode}", mode.Label,
		"{description}", mode.Description,
		"{allowed_tools}", list(allowed),
		"{blocked_tools}", list(registry.withheld(scope)),
	).Replace(orchestrator.schema.AgentScope.ContextTemplate)
}

// scopeRefuses reports whether the turn's scope refuses a call of a known
// tool. The caller has already resolved the tool, so a name the registry does
// not know never reaches here.
func (orchestrator *Orchestrator) scopeRefuses(registry *toolRegistry, name, scope string, bound bool) bool {
	tool, known := registry.byName[name]
	return known && registry.scopeBlocks(tool, orchestrator.agentScope.Normalize(scope), bound)
}

// scopeRejection is the tool result of a call the scope refuses. It names the
// mode so the Agent can explain it, and says what to suggest.
func (orchestrator *Orchestrator) scopeRejection(name, scope string) string {
	label := scope
	if mode, ok := orchestrator.agentScope.Mode(orchestrator.agentScope.Normalize(scope)); ok {
		label = mode.Label
	}
	return strings.NewReplacer("{tool}", name, "{mode}", label).Replace(orchestrator.schema.AgentScope.RejectionTemplate)
}
