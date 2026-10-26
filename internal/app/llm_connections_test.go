package app

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func llmConnectionSchema(t *testing.T) catalog.ApplicationSchema {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return taskCatalog.Application()
}

// #322 Scope 2: LLM_CONNECTIONS lists extra chat connections; each reads
// only its own <ID>_* variables, never LLM_* or another connection's.
func TestExtraChatConnectionsReadOnlyTheirOwnVariables(t *testing.T) {
	schema := llmConnectionSchema(t)
	environment := map[string]string{
		"LLM_BASE_URL":              "https://gateway.test/v1",
		"LLM_API_KEY":               "default-key-value",
		"LLM_MODEL":                 "deepseek",
		"LLM_CONNECTIONS":           " GLM , QWEN ",
		"GLM_BASE_URL":              "https://gateway.test/v1",
		"GLM_API_KEY":               "glm-key-value",
		"GLM_MODEL":                 "glm-5.3",
		"GLM_CONTEXT_WINDOW_TOKENS": "200000",
		"QWEN_MODEL":                "qwen3.8-plus",
	}
	config, err := loadConfigFromSchema(schema, func(name string) string { return environment[name] })
	if err != nil {
		t.Fatal(err)
	}
	connections := chatConnections(config, schema)
	if len(connections) != 3 {
		t.Fatalf("connections = %+v", connections)
	}
	defaultConnection, glm, qwen := connections[0], connections[1], connections[2]
	if defaultConnection.ID != "LLM" || defaultConnection.Model != "deepseek" || defaultConnection.APIKey != "default-key-value" ||
		defaultConnection.Environment != [4]string{"LLM_BASE_URL", "LLM_API_KEY", "LLM_MODEL", "LLM_CONTEXT_WINDOW_TOKENS"} {
		t.Fatalf("default = %+v", defaultConnection)
	}
	if glm.ID != "GLM" || glm.BaseURL != "https://gateway.test/v1" || glm.APIKey != "glm-key-value" || glm.Model != "glm-5.3" || glm.WindowTokens != 200000 {
		t.Fatalf("glm = %+v", glm)
	}
	// QWEN has no address or key of its own: nothing falls back to LLM_*.
	if qwen.ID != "QWEN" || qwen.BaseURL != "" || qwen.APIKey != "" || qwen.Model != "qwen3.8-plus" {
		t.Fatalf("qwen = %+v", qwen)
	}
	live := llmSettingsConnections(connections, true)
	if !live[1].Configured() || live[2].Configured() || live[2].BaseURLSet || live[2].APIKeySet {
		t.Fatalf("live connections = %+v", live)
	}
	mock := llmSettingsConnections(connections, false)
	if !mock[2].Configured() {
		t.Fatalf("a mock connection needs only its model: %+v", mock[2])
	}
}

func TestExtraChatConnectionsRejectInvalidDeclarationsWithoutValues(t *testing.T) {
	schema := llmConnectionSchema(t)
	for name, environment := range map[string]map[string]string{
		"lower-case id":      {"LLM_CONNECTIONS": "qwen"},
		"duplicate id":       {"LLM_CONNECTIONS": "GLM,GLM"},
		"default id again":   {"LLM_CONNECTIONS": "LLM"},
		"another's variable": {"LLM_CONNECTIONS": "OPENAI"},
		"bad window":         {"LLM_CONNECTIONS": "GLM", "GLM_CONTEXT_WINDOW_TOKENS": "lots", "GLM_API_KEY": "secret-never-shown"},
		"too many":           {"LLM_CONNECTIONS": "A1,A2,A3,A4,A5,A6,A7,A8,A9,B1,B2,B3,B4,B5,B6,B7,B8"},
	} {
		_, err := loadConfigFromSchema(schema, func(variable string) string { return environment[variable] })
		if err == nil {
			t.Fatalf("%s: accepted", name)
		}
		if strings.Contains(err.Error(), "secret-never-shown") {
			t.Fatalf("%s: error shows a key: %v", name, err)
		}
	}
	// Empty entries are ignored.
	if _, err := loadConfigFromSchema(schema, func(variable string) string {
		return map[string]string{"LLM_CONNECTIONS": " , "}[variable]
	}); err != nil {
		t.Fatalf("empty list: %v", err)
	}
}
