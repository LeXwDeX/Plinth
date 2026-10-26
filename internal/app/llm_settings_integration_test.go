package app

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// #322 Scopes 2 and 5 end to end on the mock Agent: two connections from the
// environment, the administrator's settings, a session that picks a model,
// the fallback when that model goes offline, and no connection value in any
// response the browser can read.
func TestLLMSettingsAndSessionModelEndToEnd(t *testing.T) {
	const (
		defaultKey = "sk-default-never-shown"
		glmKey     = "sk-glm-never-shown"
		gateway    = "https://gateway.secret-host.test/v1"
	)
	root := t.TempDir()
	var logs strings.Builder
	application, err := New(Config{
		Environment: "development", Address: "127.0.0.1:0",
		DatabasePath: filepath.Join(root, "art-ai.db"), StorageRoot: filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		ProviderMode:              "mock", AgentMode: "mock",
		RuntimeSettings: map[string]string{"llm_base_url": gateway, "llm_api_key": defaultKey, "llm_model": "deepseek"},
		ExtraChatConnections: []ChatConnectionConfig{
			{ID: "GLM", BaseURL: gateway, APIKey: glmKey, Model: "glm-5.3", WindowTokens: 200000},
		},
		AuthMode: "development", DevelopmentUser: "owner-a", DevelopmentRoles: []string{"admin"},
	}, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.generation.Close() })
	handler := application.server.Handler
	var bodies []string
	call := func(method, target, body string, want int) map[string]any {
		t.Helper()
		response := appChatRequest(handler, method, target, body)
		if response.Code != want {
			t.Fatalf("%s %s: %d %s", method, target, response.Code, response.Body.String())
		}
		bodies = append(bodies, response.Body.String())
		decoded := map[string]any{}
		_ = json.Unmarshal(response.Body.Bytes(), &decoded)
		return decoded
	}
	routes := application.schema.LLMSettings.Routes
	managed := call(http.MethodGet, routes.Admin, "", http.StatusOK)
	connections, _ := managed["connections"].([]any)
	if len(connections) != 2 || connections[1].(map[string]any)["id"] != "GLM" || connections[1].(map[string]any)["configured"] != true {
		t.Fatalf("management = %v", managed)
	}

	created := call(http.MethodPost, "/api/chat/sessions", `{"title":"models"}`, http.StatusCreated)
	sessionID, _ := created["id"].(string)
	modelRoute := strings.ReplaceAll(application.schema.Runtime.HTTP.Routes.ChatSessionModel, "{sessionID}", sessionID)
	reasoningRoute := strings.ReplaceAll(application.schema.Runtime.HTTP.Routes.ChatSessionReasoning, "{sessionID}", sessionID)
	contextRoute := strings.ReplaceAll(application.schema.Runtime.HTTP.Routes.ChatSessionContext, "{sessionID}", sessionID)
	view := call(http.MethodGet, modelRoute, "", http.StatusOK)
	if view["chatModel"] != "LLM" || len(view["chatModels"].([]any)) != 2 {
		t.Fatalf("session model = %v", view)
	}
	// The administrator narrows GLM to two levels with high as default.
	publish := `{"expected_revision":"0","settings":{"default_model":"LLM","models":{` +
		`"LLM":{"enabled":true,"effort_levels":["low","high","max"],"default_effort":"low"},` +
		`"GLM":{"enabled":true,"effort_levels":["high","max"],"default_effort":"high"}}}}`
	call(http.MethodPost, routes.Publish, publish, http.StatusOK)

	view = call(http.MethodPut, modelRoute, `{"chatModel":"GLM"}`, http.StatusOK)
	if view["chatModel"] != "GLM" || view["chatModelChosen"] != "GLM" {
		t.Fatalf("pick GLM = %v", view)
	}
	reasoning := call(http.MethodGet, reasoningRoute, "", http.StatusOK)
	if reasoning["reasoningEffort"] != "high" || len(reasoning["reasoningLevels"].([]any)) != 2 {
		t.Fatalf("GLM levels = %v", reasoning)
	}
	if usage := call(http.MethodGet, contextRoute, "", http.StatusOK); usage["contextWindow"] != float64(200000) {
		t.Fatalf("GLM window = %v", usage)
	}
	call(http.MethodPost, routes.Test, `{"connection_id":"GLM"}`, http.StatusOK)

	// GLM goes offline: the session falls back to the default model.
	offline := `{"expected_revision":"1","settings":{"default_model":"LLM","models":{` +
		`"LLM":{"enabled":true,"effort_levels":["low","high","max"],"default_effort":"low"},` +
		`"GLM":{"enabled":false,"effort_levels":["high","max"],"default_effort":"high"}}}}`
	call(http.MethodPost, routes.Publish, offline, http.StatusOK)
	view = call(http.MethodGet, modelRoute, "", http.StatusOK)
	if view["chatModel"] != "LLM" || view["chatModelChosen"] != "GLM" || view["chatModelFallback"] != true || len(view["chatModels"].([]any)) != 1 {
		t.Fatalf("offline fallback = %v", view)
	}
	if usage := call(http.MethodGet, contextRoute, "", http.StatusOK); usage["contextWindow"] != float64(1000000) {
		t.Fatalf("fallback window = %v", usage)
	}
	call(http.MethodPut, modelRoute, `{"chatModel":"GLM"}`, http.StatusBadRequest)

	schemaResponse := appChatRequest(handler, http.MethodGet, application.schema.Runtime.HTTP.Routes.ProductSchema, "")
	audit := appChatRequest(handler, http.MethodGet, application.schema.Runtime.HTTP.LegacyRoutes.AdminAuditLogs, "")
	if audit.Code != http.StatusOK || !strings.Contains(audit.Body.String(), "llm_settings.test") {
		t.Fatalf("audit log: %d %.300s", audit.Code, audit.Body.String())
	}
	bodies = append(bodies, schemaResponse.Body.String(), audit.Body.String(), logs.String())
	for _, body := range bodies {
		for _, secret := range []string{defaultKey, glmKey, "secret-host"} {
			if strings.Contains(body, secret) {
				t.Fatalf("a response or log carried %q: %.400s", secret, body)
			}
		}
	}
}

// In live mode each connection's requests go to its own address with its own
// key; nothing is shared between connections (#322 Scope 2).
func TestLiveConnectionsUseOnlyTheirOwnAddressAndKey(t *testing.T) {
	type seen struct{ authorization, model string }
	upstream := func(log *[]seen) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			var body struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(request.Body).Decode(&body)
			*log = append(*log, seen{request.Header.Get("Authorization"), body.Model})
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`))
		}))
	}
	var defaultSeen, glmSeen []seen
	defaultServer, glmServer := upstream(&defaultSeen), upstream(&glmSeen)
	t.Cleanup(defaultServer.Close)
	t.Cleanup(glmServer.Close)
	root := t.TempDir()
	application, err := New(Config{
		Environment: "development", Address: "127.0.0.1:0",
		DatabasePath: filepath.Join(root, "art-ai.db"), StorageRoot: filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		ProviderMode:              "mock", AgentMode: "live",
		RuntimeSettings: map[string]string{"llm_base_url": defaultServer.URL + "/v1", "llm_api_key": "default-key", "llm_model": "deepseek"},
		ExtraChatConnections: []ChatConnectionConfig{
			{ID: "GLM", BaseURL: glmServer.URL + "/v1", APIKey: "glm-key", Model: "glm-5.3"},
			{ID: "QWEN", Model: "qwen3.8-plus"},
		},
		AuthMode: "development", DevelopmentUser: "owner-a", DevelopmentRoles: []string{"admin"},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.generation.Close() })
	test := application.schema.LLMSettings.Routes.Test
	for _, id := range []string{"GLM", "LLM", "QWEN"} {
		if response := appChatRequest(application.server.Handler, http.MethodPost, test, `{"connection_id":"`+id+`"}`); response.Code != http.StatusOK {
			t.Fatalf("test %s: %d %s", id, response.Code, response.Body.String())
		}
	}
	if len(glmSeen) != 1 || glmSeen[0] != (seen{"Bearer glm-key", "glm-5.3"}) {
		t.Fatalf("GLM upstream saw %+v", glmSeen)
	}
	if len(defaultSeen) != 1 || defaultSeen[0] != (seen{"Bearer default-key", "deepseek"}) {
		t.Fatalf("default upstream saw %+v", defaultSeen)
	}
	// QWEN has no address or key of its own and is not called at all.
	response := appChatRequest(application.server.Handler, http.MethodGet, application.schema.LLMSettings.Routes.Admin, "")
	var managed struct {
		Connections []struct {
			ID         string `json:"id"`
			Configured bool   `json:"configured"`
			Check      *struct {
				Outcome string `json:"outcome"`
			} `json:"check"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &managed); err != nil || len(managed.Connections) != 3 ||
		managed.Connections[2].Configured || managed.Connections[2].Check == nil || managed.Connections[2].Check.Outcome != "not_configured" {
		t.Fatalf("management = %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "glm-key") || strings.Contains(response.Body.String(), "127.0.0.1") {
		t.Fatalf("management carried a connection value: %s", response.Body.String())
	}
}
