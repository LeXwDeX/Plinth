package chat

import (
	"context"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// AgentConfigurationSnapshot freezes private policy declarations and editable
// content before a new round talks to the model. It is persisted with the turn.
type AgentConfigurationSnapshot struct {
	SkillExecution catalog.ChatSkillExecutionSchema `json:"skill_execution"`
	Revision       string                           `json:"revision"`
	SystemPrompt   string                           `json:"system_prompt"`
	DomainContext  map[string]string                `json:"domain_context"`
	Skills         []catalog.SkillSchema            `json:"skills"`
	Tools          []catalog.ChatToolSchema         `json:"tools"`
	AgentTools     []catalog.ChatAgentToolSchema    `json:"agent_tools"`
}
type configurationContextKey struct{}

func configurationFromContext(ctx context.Context) *AgentConfigurationSnapshot {
	value, _ := ctx.Value(configurationContextKey{}).(*AgentConfigurationSnapshot)
	return value
}
func (o *Orchestrator) registryFor(ctx context.Context) (*toolRegistry, error) {
	if snapshot := configurationFromContext(ctx); snapshot != nil {
		schema := o.schema
		schema.Tools = append([]catalog.ChatToolSchema(nil), snapshot.Tools...)
		schema.AgentTools = append([]catalog.ChatAgentToolSchema(nil), snapshot.AgentTools...)
		// The scope class of a tool is the server's current declaration, never a
		// frozen one: a snapshot taken before scopes existed has none, and
		// editable content must not widen what a scope exposes.
		for index := range schema.Tools {
			tool := &schema.Tools[index]
			if current, known := o.registry.byName[tool.Name]; known && current.execution.ImplementationKey == tool.Execution.ImplementationKey {
				tool.Execution.AgentScope = current.execution.AgentScope
			}
		}
		for index := range schema.AgentTools {
			tool := &schema.AgentTools[index]
			current, known := o.registry.byName[tool.Name]
			if known && current.execution.ImplementationKey == tool.Execution.ImplementationKey {
				tool.Execution.AgentScope = current.execution.AgentScope
			}
			if known && current.execution.ImplementationKey == tool.Execution.ImplementationKey && paidTool(current.execution) {
				// A historical declaration may have understated a paid effect.
				// Apply the server's cost/cancellation invariant to recovery while
				// retaining the frozen tool selection and editable content.
				tool.Execution.Effects = append([]string(nil), current.execution.Effects...)
				tool.Execution.Cancellation = current.execution.Cancellation
			}
		}
		registry, err := compileToolRegistry(schema, o.registry.allowed)
		if err != nil {
			return nil, err
		}
		registry.offeringAllowed = o.registry.offeringAllowed
		return o.harnessRegistry(ctx, registry), nil
	}
	return o.harnessRegistry(ctx, o.registry), nil
}
