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
type turnRegistryContextKey struct{}

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
		registry.bindOfferingSources(o.registry.offeringAllowed, o.registry.offerings)
		return o.harnessRegistry(ctx, registry), nil
	}
	return o.harnessRegistry(ctx, o.registry), nil
}

// registryForRequest returns the registry one Agent request is served from:
// the same frozen-configuration and harness view registryFor builds, forked so
// its Offering lookups and tool lists are memoized for the request (#413).
// Nothing is written back to the Orchestrator's registry, so no cache crosses
// requests or sessions. A request whose turn already forked its registry
// (Orchestrator.Process attaches one) reuses it, so the request and its tool
// execution share the same lookups.
func (o *Orchestrator) registryForRequest(ctx context.Context) (*toolRegistry, error) {
	if turn, ok := ctx.Value(turnRegistryContextKey{}).(*toolRegistry); ok {
		return turn, nil
	}
	registry, err := o.registryFor(ctx)
	if err != nil {
		return nil, err
	}
	return forkTurnRegistry(registry), nil
}

// forkTurnRegistry returns a per-request copy of registry with an empty cache.
// The tool declarations are shared read-only: a turn never edits ordered or
// byName (the harness view copies them before it edits).
func forkTurnRegistry(registry *toolRegistry) *toolRegistry {
	fork := *registry
	fork.cache = newRegistryCache()
	return &fork
}
