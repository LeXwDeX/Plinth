package app

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func newAgentStatusTestApplication(t *testing.T, agentMode string, settings map[string]string, logs io.Writer) *Application {
	t.Helper()
	root := t.TempDir()
	application, err := New(Config{
		Environment: "development", Address: "127.0.0.1:0",
		DatabasePath: filepath.Join(root, "art-ai.db"), StorageRoot: filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		ProviderMode:              "mock", AgentMode: agentMode, RuntimeSettings: settings,
		AuthMode: "development", DevelopmentUser: "owner-a", DevelopmentRoles: []string{"admin"},
	}, slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatalf("startup with agent mode %s: %v", agentMode, err)
	}
	t.Cleanup(func() { _ = application.generation.Close() })
	return application
}

func publicAgentAvailable(t *testing.T, application *Application) bool {
	t.Helper()
	response := httptest.NewRecorder()
	application.server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, application.schema.Runtime.HTTP.Routes.ProductSchema, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("schema status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Schema struct {
			Chat struct {
				AgentStatus *struct {
					Available bool `json:"available"`
				} `json:"agent_status"`
			} `json:"chat"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Schema.Chat.AgentStatus == nil {
		t.Fatalf("public schema has no chat.agent_status: %s", response.Body.String())
	}
	return body.Schema.Chat.AgentStatus.Available
}

// #253: a live Agent without an API key (the formal stack ships none) still
// starts; the log names the missing setting, the public Schema reports the
// Agent unavailable and prompt enhancement answers with the Schema message.
func TestLiveAgentWithoutAPIKeyStartsAndReportsNotConfigured(t *testing.T) {
	var logs bytes.Buffer
	application := newAgentStatusTestApplication(t, "live", map[string]string{
		"llm_base_url": "http://127.0.0.1:1/v1",
	}, &logs)
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "missing_settings=LLM_API_KEY") {
		t.Fatalf("startup warning does not name the missing setting: %s", logs.String())
	}
	if publicAgentAvailable(t, application) {
		t.Fatal("public schema reports an Agent without API key as available")
	}
	contract := application.schema.Chat.API.PromptEnhance
	payload, _ := json.Marshal(map[string]string{contract.PromptField: "a red fox"})
	response := httptest.NewRecorder()
	application.server.Handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodPost,
		application.schema.Runtime.HTTP.LegacyRoutes.PromptEnhance,
		bytes.NewReader(payload),
	))
	var body map[string]string
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != http.StatusServiceUnavailable ||
		body[contract.ErrorField] != application.schema.Chat.Presentation.Messages.AgentNotConfigured {
		t.Fatalf("prompt enhance status=%d body=%s", response.Code, response.Body.String())
	}
}

// Mock mode and a configured live Agent stay available.
func TestConfiguredAgentStaysAvailable(t *testing.T) {
	for _, run := range []struct {
		mode     string
		settings map[string]string
	}{
		{mode: "mock"},
		{mode: "live", settings: map[string]string{"llm_base_url": "http://127.0.0.1:1/v1", "llm_api_key": "configured-test-key"}},
	} {
		var logs bytes.Buffer
		application := newAgentStatusTestApplication(t, run.mode, run.settings, &logs)
		if !publicAgentAvailable(t, application) {
			t.Fatalf("%s: configured Agent reported unavailable", run.mode)
		}
		if strings.Contains(logs.String(), "missing_settings") || strings.Contains(logs.String(), "configured-test-key") {
			t.Fatalf("%s: unexpected startup log: %s", run.mode, logs.String())
		}
	}
}

// An empty model (a Schema default left empty) is accepted by validation and
// makes the live Agent the not-configured stand-in, naming both settings.
func TestLiveAgentWithoutModelIsNotConfigured(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	setting := application.Runtime.Configuration.Settings["llm_model"]
	if setting.Required {
		t.Fatal("llm_model must not be required: an empty model means the Agent is not configured")
	}
	setting.Default = ""
	application.Runtime.Configuration.Settings["llm_model"] = setting
	adapter, err := buildChatAgent(Config{AgentMode: "live", RuntimeSettings: map[string]string{
		"llm_base_url": "http://127.0.0.1:1/v1",
	}}, application)
	if err != nil {
		t.Fatal(err)
	}
	standIn, ok := adapter.(agents.NotConfigured)
	if !ok || !slices.Equal(standIn.Missing, []string{"LLM_API_KEY", "LLM_MODEL"}) {
		t.Fatalf("adapter = %#v", adapter)
	}
	adapter, err = buildChatAgent(Config{AgentMode: "live", RuntimeSettings: map[string]string{
		"llm_base_url": "http://127.0.0.1:1/v1", "llm_api_key": "configured-test-key",
	}}, application)
	if err != nil {
		t.Fatal(err)
	}
	if standIn, ok := adapter.(agents.NotConfigured); !ok || !slices.Equal(standIn.Missing, []string{"LLM_MODEL"}) {
		t.Fatalf("adapter without model = %#v", adapter)
	}
}

// #251: a self-deployed container has no gateway on 127.0.0.1:8000, so the
// Agent base URL has no default; without LLM_BASE_URL the live Agent is the
// not-configured stand-in instead of an Agent that shows as ready and fails.
func TestLiveAgentWithoutBaseURLIsNotConfigured(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	setting := application.Runtime.Configuration.Settings[application.Chat.Agent.BaseURLSetting]
	if setting.Default != "" || setting.Required {
		t.Fatalf("Agent base URL setting = %#v; it must default to empty and not be required", setting)
	}
	adapter, err := buildChatAgent(Config{AgentMode: "live", RuntimeSettings: map[string]string{
		"llm_api_key": "configured-test-key",
	}}, application)
	if err != nil {
		t.Fatal(err)
	}
	if standIn, ok := adapter.(agents.NotConfigured); !ok || !slices.Equal(standIn.Missing, []string{"LLM_BASE_URL"}) {
		t.Fatalf("adapter without base URL = %#v", adapter)
	}
	var logs bytes.Buffer
	started := newAgentStatusTestApplication(t, "live", map[string]string{"llm_api_key": "configured-test-key"}, &logs)
	if publicAgentAvailable(t, started) || !strings.Contains(logs.String(), "missing_settings=LLM_BASE_URL") {
		t.Fatalf("live Agent without base URL reported available; log: %s", logs.String())
	}
}
