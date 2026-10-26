package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentConfigurationRoutesEnforcePermissionRevisionAndSafeProjection(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application()
	skills, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repo, err := OpenMigratedTestRepository(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	store := &configurationHistorySpy{Store: repo}
	service, err := agentconfiguration.New(store, schema, skills, product.List(""))
	if err != nil {
		t.Fatal(err)
	}
	audit, err := administration.NewAuditService(administration.AuditServiceConfig{Repository: repo, DefaultPage: 1, DefaultPageSize: 20, MaximumPageSize: 100, IDPrefix: "audit_", WriteMaximumAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := access.NewPolicy(access.PolicyConfig{DefaultRole: "user", AdminRole: "admin", Roles: []access.RoleDefinition{{Key: "user"}, {Key: "admin", Permissions: []access.Permission{access.Permission(schema.AgentConfiguration.Permission)}}}})
	if err != nil {
		t.Fatal(err)
	}
	identity := DevelopmentIdentity{DefaultClaims: access.Claims{ID: "owner-config", Roles: []string{"user"}}, Policy: policy}
	server := &Server{identity: identity, application: schema, agentConfiguration: service, administrationAudit: audit, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), mux: http.NewServeMux(), ownsAgentSession: func(_ context.Context, owner, session string) bool {
		return owner == "owner-config" && session == "mine"
	}}
	server.registerAgentConfigurationContracts()
	server.mux.HandleFunc("GET "+schema.Runtime.HTTP.Routes.ChatSkills, server.skillCatalogSchema)
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, httptest.NewRequest(method, path, bytes.NewReader(data)))
		return response
	}
	if response := request("GET", schema.AgentConfiguration.Routes.Admin, nil); response.Code != 403 {
		t.Fatalf("ordinary user administration: %d", response.Code)
	}
	effective := request("GET", schema.AgentConfiguration.Routes.Effective+"?session_id=mine", nil)
	if effective.Code != 200 {
		t.Fatalf("effective: %d %s", effective.Code, effective.Body.String())
	}
	if store.reads != 0 {
		t.Fatal("ordinary effective read decoded management history")
	}
	if strings.Contains(effective.Body.String(), "mcp_list_offerings") {
		t.Fatal("nil MCP runtime advertised executable tools")
	}
	for _, secret := range []string{"system_prompt", "domain_context", "implementation_key", "connection_ref", "provider_options", "api_key"} {
		if strings.Contains(effective.Body.String(), secret) {
			t.Fatalf("effective projection leaked %s", secret)
		}
	}
	if response := request("GET", schema.AgentConfiguration.Routes.Effective+"?session_id=other", nil); response.Code != 404 {
		t.Fatalf("cross-session projection: %d", response.Code)
	}
	identity.DefaultClaims.Roles = []string{"admin"}
	server.identity = identity
	current, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	current.Configuration.SystemPrompt = "managed private prompt"
	body := map[string]any{"expected_revision": current.Revision, "configuration": current.Configuration}
	response := request("POST", schema.AgentConfiguration.Routes.Publish, body)
	if response.Code != 200 {
		t.Fatalf("publish: %d %s", response.Code, response.Body.String())
	}
	for _, private := range []string{"implementation_key", "connection_ref", "remote_name", "effects"} {
		if strings.Contains(response.Body.String(), "\""+private+"\":") {
			t.Fatalf("management leaked fixed execution policy: %s", private)
		}
	}
	if !strings.Contains(response.Body.String(), "tool_names") || !strings.Contains(response.Body.String(), "mcp_list_offerings") {
		t.Fatal("management lacks declared MCP tool names")
	}
	var managed struct {
		MCPPresets []struct {
			ID        string `json:"id"`
			Available bool   `json:"available"`
		} `json:"mcp_presets"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &managed); err != nil {
		t.Fatal(err)
	}
	if len(managed.MCPPresets) != 1 || managed.MCPPresets[0].Available {
		t.Fatal("nil runtime marked preset available")
	}
	if response := request("POST", schema.AgentConfiguration.Routes.Publish, body); response.Code != 409 {
		t.Fatalf("stale publish: %d %s", response.Code, response.Body.String())
	}
	publicSkills := request("GET", schema.Runtime.HTTP.Routes.ChatSkills, nil)
	if publicSkills.Code != 200 || strings.Contains(publicSkills.Body.String(), "raw.githubusercontent.com") {
		t.Fatal("effective Skill catalog did not localize source image")
	}
	events, total, err := repo.QueryAdministrationAuditEvents(context.Background(), administration.AuditQuery{Page: 1, PageSize: 10, Action: schema.AgentConfiguration.AuditAction})
	if err != nil || total != 1 || events[0].ActorID != "owner-config" {
		t.Fatalf("missing publish audit: %d %#v %v", total, events, err)
	}
}

type configurationHistorySpy struct {
	agentconfiguration.Store
	reads int
}

func (s *configurationHistorySpy) AgentConfigurationHistory(ctx context.Context, limit int) ([]agentconfiguration.Version, error) {
	s.reads++
	return s.Store.AgentConfigurationHistory(ctx, limit)
}
