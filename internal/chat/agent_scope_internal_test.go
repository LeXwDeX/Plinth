package chat

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func scopeTestOrchestrator(t *testing.T) (*Orchestrator, catalog.ChatSchema) {
	t.Helper()
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := loaded.Application().Chat
	registry, err := compileToolRegistry(schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &Orchestrator{schema: schema, registry: registry, agentScope: registry.scope}, schema
}

func TestAnAcceptedPaidTaskStaysObservableAfterTheScopeChanges(t *testing.T) {
	orchestrator, _ := scopeTestOrchestrator(t)
	registry := orchestrator.registry
	// A new 3D call is refused in 2D mode ...
	if !orchestrator.scopeRefuses(registry, "create_model_3d", "2d", false) {
		t.Fatal("a new 3D generation must be refused in 2d")
	}
	// ... but the Task it already accepted is only observed, never revoked.
	if orchestrator.scopeRefuses(registry, "create_model_3d", "2d", true) {
		t.Fatal("an accepted paid task must remain observable after a scope switch")
	}
	// The exception is for paid tools: a read tool of the wrong class is refused.
	if !orchestrator.scopeRefuses(registry, "list_session_models", "2d", true) {
		t.Fatal("a bound call must not unlock a non-paid out-of-scope tool")
	}
	if orchestrator.scopeRefuses(registry, "unknown_tool", "2d", false) {
		t.Fatal("an unknown name is the unavailable-tool path, not a scope refusal")
	}
}

func TestAFrozenConfigurationCannotWidenTheScope(t *testing.T) {
	orchestrator, schema := scopeTestOrchestrator(t)
	// A snapshot taken before scopes existed declares no class, and an edited
	// one could declare a wider one. Neither may change what a scope exposes.
	snapshot := AgentConfigurationSnapshot{
		Tools:      append([]catalog.ChatToolSchema(nil), schema.Tools...),
		AgentTools: append([]catalog.ChatAgentToolSchema(nil), schema.AgentTools...),
	}
	for index := range snapshot.Tools {
		snapshot.Tools[index].Execution.AgentScope = ""
		if snapshot.Tools[index].Name == "create_model_3d" {
			snapshot.Tools[index].Execution.AgentScope = "utility"
		}
	}
	for index := range snapshot.AgentTools {
		snapshot.AgentTools[index].Execution.AgentScope = "utility"
	}
	ctx := context.WithValue(context.Background(), configurationContextKey{}, &snapshot)
	registry, err := orchestrator.registryFor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		"create_model_3d":     true,
		"refine_image":        false,
		"list_session_models": true,
		"get_task":            false,
	} {
		if got := orchestrator.scopeRefuses(registry, name, "2d", false); got != want {
			t.Errorf("2d refuses %s = %v, want %v", name, got, want)
		}
	}
}

func TestAToolWithoutADeclaredClassIsOnlyOfferedInAuto(t *testing.T) {
	orchestrator, _ := scopeTestOrchestrator(t)
	tool := orchestrator.registry.byName["get_task"]
	tool.execution.AgentScope = ""
	for scope, blocked := range map[string]bool{"2d": true, "3d": true, "auto": false} {
		if got := orchestrator.registry.scopeBlocks(tool, scope, false); got != blocked {
			t.Errorf("undeclared class in %s blocked = %v, want %v", scope, got, blocked)
		}
	}
}
