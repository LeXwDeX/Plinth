package catalog_test

import (
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"slices"
	"strings"
	"testing"
)

func TestAgentConfigurationPresetPolicyAndPrivateProjection(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := c.Application()
	public, err := json.Marshal(schema.Public().AgentConfiguration)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"connection_ref", "mcp_connections", "execution", "api_key"} {
		if strings.Contains(string(public), private) {
			t.Fatalf("private config exposed: %s", private)
		}
	}
	if strings.Contains(string(public), schema.Chat.Agent.SystemPrompt) {
		t.Fatal("private prompt exposed")
	}
	for _, mutation := range []func(*catalog.ApplicationSchema){
		func(s *catalog.ApplicationSchema) {
			s.AgentConfiguration.MCPPresets[0].Tools[0].Execution.Effects = []string{"PAID_TASK"}
		},
		func(s *catalog.ApplicationSchema) {
			s.AgentConfiguration.MCPPresets[0].Tools[0].Execution.Scope = "owner"
		},
		func(s *catalog.ApplicationSchema) { s.AgentConfiguration.Limits.MaxPromptCharacters = 0 },
		func(s *catalog.ApplicationSchema) { s.AgentConfiguration.Routes.MCP = "https://external.test/mcp" },
		func(s *catalog.ApplicationSchema) {
			s.AgentConfiguration.Routes.MCP = s.AgentConfiguration.Routes.Admin
		},
		func(s *catalog.ApplicationSchema) {
			s.AgentConfiguration.Routes.Publish = s.AgentConfiguration.Routes.Validate
		},
	} {
		changed := c.Application()
		mutation(&changed)
		if err := catalog.ValidateApplicationSchema(changed); err == nil {
			t.Fatal("unsafe managed policy accepted")
		}
	}
}

func TestAgentConfigurationDeclarationsAreDefensiveCopies(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	changed := c.Application()
	changed.AgentConfiguration.UI["title"] = "changed"
	changed.AgentConfiguration.MCPPresets[0].Tools[0].Execution.Effects[0] = "PAID_TASK"
	changed.AgentConfiguration.MCPPresets[0].Tools[0].Parameters[0] = 'x'
	*changed.AgentConfiguration.MCPPresets[0].DefaultEnabled = true
	if err := catalog.ValidateApplicationSchema(c.Application()); err != nil {
		t.Fatalf("caller mutated catalog: %v", err)
	}
	if c.Application().AgentConfiguration.MCPPresets[0].EnabledByDefault() {
		t.Fatal("the default selection is shared between callers")
	}
}

// #445: a preset whose tools repeat built-in reads declares both the
// equivalence and that a fresh configuration leaves it out; Catalog validation
// makes the pair a load-time rule instead of a data preference.
func TestAgentConfigurationRejectsDefaultEnabledDuplicateMCPReads(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	preset := c.Application().AgentConfiguration.MCPPresets[0]
	if preset.ID != "workspace-read" || preset.EnabledByDefault() {
		t.Fatalf("shipped preset = %q default enabled = %v", preset.ID, preset.EnabledByDefault())
	}
	equivalences := map[string]string{}
	for _, tool := range preset.Tools {
		equivalences[tool.Name] = tool.Duplicates
	}
	for tool, builtIn := range map[string]string{
		"mcp_list_offerings":     "list_offerings",
		"mcp_get_task_assets":    "get_task",
		"mcp_list_session_tasks": "",
	} {
		if equivalences[tool] != builtIn {
			t.Fatalf("equivalence for %s = %q, want %q", tool, equivalences[tool], builtIn)
		}
	}

	enabled := c.Application()
	value := true
	enabled.AgentConfiguration.MCPPresets[0].DefaultEnabled = &value
	if err := catalog.ValidateApplicationSchema(enabled); err == nil || !strings.Contains(err.Error(), "must not be enabled by default") {
		t.Fatalf("default-enabled duplicate preset accepted: %v", err)
	}

	unknown := c.Application()
	unknown.AgentConfiguration.MCPPresets[0].Tools[0].Duplicates = "no_such_tool"
	if err := catalog.ValidateApplicationSchema(unknown); err == nil || !strings.Contains(err.Error(), "duplicates unknown built-in tool") {
		t.Fatalf("unknown duplicate target accepted: %v", err)
	}
}

// #517: the workspace-read preset's mcp_get_task_assets needs a task, as its
// runtime does; the Schema says so, so the model is not left to guess.
func TestWorkspaceReadPresetRequiresTheTaskOfGetTaskAssets(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range c.Application().AgentConfiguration.MCPPresets[0].Tools {
		if tool.Name != "mcp_get_task_assets" {
			continue
		}
		var parameters struct {
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(tool.Parameters, &parameters); err != nil || !slices.Contains(parameters.Required, "task_id") {
			t.Fatalf("mcp_get_task_assets parameters = %s (%v)", tool.Parameters, err)
		}
		return
	}
	t.Fatal("the workspace-read preset has no mcp_get_task_assets")
}
