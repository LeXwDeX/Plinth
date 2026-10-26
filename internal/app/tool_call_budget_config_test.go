package app

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #275: AGENT_MAX_TOOL_CALLS_PER_REPLY overrides the Schema's per-reply tool
// call budget; unset keeps the Schema value (20) and an invalid value stops
// startup instead of silently running with another budget.
func TestAgentToolCallBudgetComesFromSchemaOrEnvironment(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	load := func(value string) (Config, error) {
		return loadConfigFromSchema(application, func(name string) string {
			if name == "AGENT_MAX_TOOL_CALLS_PER_REPLY" {
				return value
			}
			return ""
		})
	}

	defaults, err := load("")
	if err != nil {
		t.Fatal(err)
	}
	schema := withToolCallBudget(application.Chat, defaults.AgentMaxToolCallsPerReply)
	if defaults.AgentMaxToolCallsPerReply != 0 || schema.Agent.MaxToolCallsPerReply != 20 {
		t.Fatalf("default budget = %d (override %d)", schema.Agent.MaxToolCallsPerReply, defaults.AgentMaxToolCallsPerReply)
	}

	overridden, err := load(" 35 ")
	if err != nil {
		t.Fatal(err)
	}
	schema = withToolCallBudget(application.Chat, overridden.AgentMaxToolCallsPerReply)
	if schema.Agent.MaxToolCallsPerReply != 35 {
		t.Fatalf("overridden budget = %d", schema.Agent.MaxToolCallsPerReply)
	}
	if application.Chat.Agent.MaxToolCallsPerReply != 20 {
		t.Fatal("override mutated the shared Schema")
	}

	for _, invalid := range []string{"0", "-3", "abc", "1.5", "101"} {
		if _, err := load(invalid); err == nil ||
			!strings.Contains(err.Error(), "agent_max_tool_calls_per_reply") {
			t.Fatalf("AGENT_MAX_TOOL_CALLS_PER_REPLY=%q: error = %v", invalid, err)
		}
	}
}
