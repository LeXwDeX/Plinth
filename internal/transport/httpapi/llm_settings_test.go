package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/llmsettings"
)

// #322 Scope 2: the LLM settings routes follow the Agent configuration's
// rules: the settings permission, an audit record per change and per test,
// expected_revision on every write, and no connection value anywhere.
func TestLLMSettingsRoutesEnforcePermissionRevisionAuditAndSecrecy(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application()
	repo, err := OpenMigratedTestRepository(filepath.Join(t.TempDir(), "llm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	const upstreamDetail = "upstream said: invalid key sk-should-never-show"
	service, err := llmsettings.New(llmsettings.Config{Store: repo, Thinking: schema.Chat.Agent.Thinking, HistoryLimit: 10,
		Connections: []llmsettings.Connection{
			{ID: "LLM", Model: "deepseek", BaseURLSet: true, APIKeySet: true, CredentialsRequired: true,
				Environment: [4]string{"LLM_BASE_URL", "LLM_API_KEY", "LLM_MODEL", "LLM_CONTEXT_WINDOW_TOKENS"}},
			{ID: "GLM", Model: "glm-5.3", BaseURLSet: true, APIKeySet: false, CredentialsRequired: true,
				Environment: [4]string{"GLM_BASE_URL", "GLM_API_KEY", "GLM_MODEL", "GLM_CONTEXT_WINDOW_TOKENS"}},
		},
		// The prober only ever returns a classification; the upstream text
		// stays in it.
		Prober: func(context.Context, llmsettings.Connection) llmsettings.ProbeResult {
			_ = upstreamDetail
			return llmsettings.ProbeResult{Vision: llmsettings.VisionUnknown, Outcome: llmsettings.OutcomeUnauthorized}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	audit, err := administration.NewAuditService(administration.AuditServiceConfig{Repository: repo, DefaultPage: 1, DefaultPageSize: 20, MaximumPageSize: 100, IDPrefix: "audit_", WriteMaximumAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := access.NewPolicy(access.PolicyConfig{DefaultRole: "user", AdminRole: "admin", Roles: []access.RoleDefinition{
		{Key: "user"}, {Key: "admin", Permissions: []access.Permission{access.Permission(schema.LLMSettings.Permission)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	identity := DevelopmentIdentity{DefaultClaims: access.Claims{ID: "admin-llm", Roles: []string{"user"}}, Policy: policy}
	server := &Server{identity: identity, application: schema, llmSettings: service, administrationAudit: audit,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)), mux: http.NewServeMux()}
	server.registerLLMSettingsContracts()
	routes := schema.LLMSettings.Routes
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, httptest.NewRequest(method, path, bytes.NewReader(data)))
		return response
	}
	for _, call := range []struct {
		method, path string
		body         any
	}{
		{"GET", routes.Admin, nil},
		{"POST", routes.Publish, map[string]any{"expected_revision": "0"}},
		{"POST", routes.Reset, map[string]any{"expected_revision": "0"}},
		{"POST", routes.Test, map[string]any{"connection_id": "LLM"}},
	} {
		if response := request(call.method, call.path, call.body); response.Code != http.StatusForbidden {
			t.Fatalf("ordinary user %s %s: %d", call.method, call.path, response.Code)
		}
	}
	identity.DefaultClaims.Roles = []string{"admin"}
	server.identity = identity

	read := request("GET", routes.Admin, nil)
	if read.Code != http.StatusOK || read.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("read: %d %s", read.Code, read.Body.String())
	}
	var managed llmsettings.Management
	if err := json.Unmarshal(read.Body.Bytes(), &managed); err != nil {
		t.Fatal(err)
	}
	if managed.Revision != "0" || len(managed.Connections) != 2 || !managed.Connections[0].Configured || managed.Connections[1].Configured ||
		managed.Connections[1].APIKeySet || managed.Connections[1].Environment[1] != "GLM_API_KEY" {
		t.Fatalf("management = %+v", managed)
	}
	settings := managed.Settings
	settings.Models["LLM"] = llmsettings.ModelSettings{Enabled: true, EffortLevels: []string{"low", "high"}, DefaultEffort: "high"}
	published := request("POST", routes.Publish, map[string]any{"expected_revision": "0", "settings": settings})
	if published.Code != http.StatusOK || !strings.Contains(published.Body.String(), `"revision":"1"`) {
		t.Fatalf("publish: %d %s", published.Code, published.Body.String())
	}
	if stale := request("POST", routes.Publish, map[string]any{"expected_revision": "0", "settings": settings}); stale.Code != http.StatusConflict {
		t.Fatalf("stale publish: %d %s", stale.Code, stale.Body.String())
	}
	invalid := settings
	invalid.Models = map[string]llmsettings.ModelSettings{"LLM": {Enabled: true, EffortLevels: []string{"xhigh"}, DefaultEffort: "xhigh"}, "GLM": settings.Models["GLM"]}
	if response := request("POST", routes.Publish, map[string]any{"expected_revision": "1", "settings": invalid}); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid publish: %d %s", response.Code, response.Body.String())
	}
	if response := request("POST", routes.Publish, map[string]any{"expected_revision": "1", "settings": settings, "base_url": "https://x"}); response.Code != http.StatusBadRequest {
		t.Fatalf("unknown member accepted: %d", response.Code)
	}
	if response := request("POST", routes.Restore, map[string]any{"expected_revision": "1", "target_revision": "9"}); response.Code != http.StatusNotFound {
		t.Fatalf("restore missing version: %d", response.Code)
	}
	if response := request("POST", routes.Reset, map[string]any{"expected_revision": "1"}); response.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", response.Code, response.Body.String())
	}
	tested := request("POST", routes.Test, map[string]any{"connection_id": "LLM"})
	if tested.Code != http.StatusOK || !strings.Contains(tested.Body.String(), `"outcome":"unauthorized"`) {
		t.Fatalf("test: %d %s", tested.Code, tested.Body.String())
	}
	if response := request("POST", routes.Test, map[string]any{"connection_id": "NOPE"}); response.Code != http.StatusNotFound {
		t.Fatalf("test unknown connection: %d", response.Code)
	}
	events, total, err := repo.QueryAdministrationAuditEvents(context.Background(), administration.AuditQuery{Page: 1, PageSize: 10, Action: schema.LLMSettings.AuditAction})
	if err != nil || total != 2 || events[0].ActorID != "admin-llm" {
		t.Fatalf("settings audit: %d %#v %v", total, events, err)
	}
	tests, testTotal, err := repo.QueryAdministrationAuditEvents(context.Background(), administration.AuditQuery{Page: 1, PageSize: 10, Action: schema.LLMSettings.TestAuditAction})
	if err != nil || testTotal != 1 || tests[0].ResourceID != "LLM" {
		t.Fatalf("test audit: %d %#v %v", testTotal, tests, err)
	}
	for _, event := range append(events, tests...) {
		encoded, _ := json.Marshal(event)
		if strings.Contains(string(encoded), "sk-should-never-show") {
			t.Fatalf("audit leaked upstream text: %s", encoded)
		}
	}
	for _, body := range []string{read.Body.String(), published.Body.String(), tested.Body.String()} {
		if strings.Contains(body, "sk-should-never-show") || strings.Contains(body, "https://") {
			t.Fatalf("a response carried a connection value: %s", body)
		}
	}
	// Unauthenticated requests are refused before anything else.
	server.identity = DevelopmentIdentity{Policy: policy}
	if response := request("GET", routes.Admin, nil); response.Code != http.StatusUnauthorized && response.Code != http.StatusForbidden {
		t.Fatalf("anonymous read: %d", response.Code)
	}
}
