package chat

import (
	"strconv"
	"strings"
)

// conceptDirectionsPrompt adds the diverge-then-converge rules (#242) while the
// sketch tool is really offered to this turn. A 3D-scope turn hides the tool,
// so the block disappears there instead of teaching an unusable flow.
func (orchestrator *Orchestrator) conceptDirectionsPrompt(registry *toolRegistry, scope string) string {
	concept := orchestrator.schema.ConceptDirections
	if !concept.Enabled || strings.TrimSpace(concept.ContextTemplate) == "" {
		return ""
	}
	if !registryOffers(registry, scope, concept.SketchTool) {
		return ""
	}
	return strings.NewReplacer(
		"{minimum}", strconv.Itoa(concept.MinCount),
		"{maximum}", strconv.Itoa(concept.MaxCount),
		"{sketch_tool}", concept.SketchTool,
	).Replace(concept.ContextTemplate)
}

// candidatesPrompt adds the parallel-candidate rules (#242) while the plan tool
// is offered to this turn.
func (orchestrator *Orchestrator) candidatesPrompt(registry *toolRegistry, scope string) string {
	candidates := orchestrator.schema.Candidates
	if !candidates.Enabled || strings.TrimSpace(candidates.ContextTemplate) == "" {
		return ""
	}
	if !registryOffers(registry, scope, orchestrator.planProposalTool()) {
		return ""
	}
	return strings.NewReplacer(
		"{maximum}", strconv.Itoa(candidates.MaxCount),
		"{sources}", strings.Join(candidates.Sources, ", "),
	).Replace(candidates.ContextTemplate)
}

// planProposalTool is the declared Agent tool that proposes a production plan.
func (orchestrator *Orchestrator) planProposalTool() string {
	for _, tool := range orchestrator.schema.AgentTools {
		if tool.Execution.ImplementationKey == "plan.propose" {
			return tool.Name
		}
	}
	return ""
}

func registryOffers(registry *toolRegistry, scope, name string) bool {
	if registry == nil || name == "" {
		return false
	}
	for _, tool := range registry.describe(scope) {
		if tool.Name == name {
			return true
		}
	}
	return false
}
