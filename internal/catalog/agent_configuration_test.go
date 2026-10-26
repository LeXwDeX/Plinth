package catalog_test

import (
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/catalog"
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
			s.AgentConfiguration.MCPPresets[0].Tools[0].Execution.Consent = "human"
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
	if err := catalog.ValidateApplicationSchema(c.Application()); err != nil {
		t.Fatalf("caller mutated catalog: %v", err)
	}
}
