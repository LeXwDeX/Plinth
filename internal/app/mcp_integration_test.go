package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/mcphttp"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestWorkspaceMCPUsesRealProtocolOwnerSessionAndConfigurationAllowlist(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "art-ai.db")
	application, err := New(Config{Environment: "development", Address: "127.0.0.1:0", DatabasePath: database, StorageRoot: filepath.Join(root, "storage"), LegacyGeneratedImagesRoot: filepath.Join(root, "generated"), ProviderMode: "mock", AgentMode: "mock", AuthMode: "development", DevelopmentUser: "owner-a"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer application.generation.Close()
	handler := application.server.Handler
	create := appChatRequest(handler, "POST", "/api/chat/sessions", `{}`)
	if create.Code != 201 {
		t.Fatalf("session: %d", create.Code)
	}
	var session struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(create.Body.Bytes(), &session)
	call := func(owner, scope, name string, arguments map[string]any) *httptest.ResponseRecorder {
		method := "tools/list"
		params := map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/protocolVersion": mcphttp.ProtocolVersion, "io.modelcontextprotocol/clientInfo": map[string]string{"name": "test", "version": "1"}, "io.modelcontextprotocol/clientCapabilities": map[string]any{}}}
		if name != "" {
			method = "tools/call"
			params["name"] = name
			params["arguments"] = arguments
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "test-read", "method": method, "params": params})
		request := httptest.NewRequest(http.MethodPost, application.schema.AgentConfiguration.Routes.MCP+"?session_id="+scope, bytes.NewReader(body))
		request.Header.Set("X-User-ID", owner)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("MCP-Protocol-Version", mcphttp.ProtocolVersion)
		request.Header.Set("Mcp-Method", method)
		request.Header.Set("Mcp-Name", name)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	list := call("owner-a", session.ID, "", nil)
	if list.Code != 200 || !strings.Contains(list.Body.String(), "mcp_list_session_tasks") {
		t.Fatalf("MCP list: %d %s", list.Code, list.Body.String())
	}
	for _, scope := range []struct{ owner, session string }{{"owner-b", session.ID}, {"owner-a", "other-session"}, {"owner-a", ""}} {
		if response := call(scope.owner, scope.session, "", nil); response.Code != 404 {
			t.Fatalf("cross-scope read: %d", response.Code)
		}
	}
	for _, tool := range []string{"mcp_list_offerings", "mcp_list_session_tasks"} {
		response := call("owner-a", session.ID, tool, map[string]any{})
		if response.Code != 200 {
			t.Fatalf("tool %s: %d %s", tool, response.Code, response.Body.String())
		}
		// #356: MCP shares the Agent projection, including the read-only capacity.
		if tool == "mcp_list_offerings" && !strings.Contains(response.Body.String(), "reference_image_max") {
			t.Fatalf("MCP list_offerings lacks reference_image_max: %s", response.Body.String())
		}
	}
	if response := call("owner-a", session.ID, "mcp_list_session_tasks", map[string]any{"owner_id": "owner-b"}); response.Code != 400 {
		t.Fatal("model-controlled owner accepted")
	}
	if response := call("owner-a", session.ID, "mcp_get_task_assets", map[string]any{"task_id": "missing"}); response.Code != 400 {
		t.Fatal("unknown task accepted")
	}
	for _, private := range []string{"connection_ref", "implementation_key", "provider_options", "remote_job_id", "checkpoint"} {
		if strings.Contains(list.Body.String(), private) {
			t.Fatalf("private field in MCP discovery: %s", private)
		}
	}
	repository, err := sqlite.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	skills, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := agentconfiguration.New(repository, product.Application(), skills, product.List(""))
	if err != nil {
		t.Fatal(err)
	}
	current, err := configuration.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	current.Configuration.EnabledMCPPresetIDs = []string{}
	if _, err := configuration.Publish(context.Background(), current.Revision, "test-admin", "publish", current.Configuration); err != nil {
		t.Fatal(err)
	}
	if response := call("owner-a", session.ID, "mcp_list_session_tasks", map[string]any{}); response.Code != 400 {
		t.Fatal("disabled MCP tool called")
	}
	tasks := appChatRequest(handler, "GET", "/api/v1/tasks", "")
	if tasks.Code != 200 || strings.Contains(tasks.Body.String(), `"task_`) {
		t.Fatalf("MCP unexpectedly created Task: %s", tasks.Body.String())
	}
}

func TestExternalMCPPresetIsUnavailableAndNeverDialed(t *testing.T) {
	var dialed atomic.Int32
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		dialed.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer trap.Close()
	data, err := os.ReadFile("../catalog/default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	applicationSchema := document["application"].(map[string]any)
	configuration := applicationSchema["agent_configuration"].(map[string]any)
	presets := configuration["mcp_presets"].([]any)
	encoded, _ := json.Marshal(presets[0])
	var external map[string]any
	_ = json.Unmarshal(encoded, &external)
	sourceID := external["id"].(string)
	sourceTool := external["tools"].([]any)[0].(map[string]any)["name"].(string)
	external["id"], external["transport"], external["connection_ref"] = "external-read", "streamable_http", "external-read"
	external["tools"] = []any{external["tools"].([]any)[0]}
	external["tools"].([]any)[0].(map[string]any)["name"] = "mcp_external_read"
	configuration["mcp_presets"] = append(presets, external)
	// The copied preset keeps its source's English copy (every Chinese field has a translation).
	english := applicationSchema["localization"].(map[string]any)["bundles"].(map[string]any)["en"].(map[string]any)["fields"].(map[string]any)
	sourcePrefix := "agent_configuration.mcp_presets[id=" + sourceID + "]"
	for path, value := range english {
		if strings.HasPrefix(path, sourcePrefix) && !strings.Contains(path, "tools[name=") ||
			strings.HasPrefix(path, sourcePrefix+".tools[name="+sourceTool+"]") {
			copied := strings.Replace(path, sourcePrefix, "agent_configuration.mcp_presets[id=external-read]", 1)
			english[strings.Replace(copied, "tools[name="+sourceTool+"]", "tools[name=mcp_external_read]", 1)] = value
		}
	}
	configuration["mcp_connections"] = map[string]any{"external-read": map[string]any{"endpoint_setting": "llm_base_url", "api_key_setting": "llm_api_key", "allowed_origins": []string{trap.URL}}}
	data, _ = json.Marshal(document)
	root := t.TempDir()
	catalogPath := filepath.Join(root, "catalog.json")
	if err := os.WriteFile(catalogPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	// This is a valid Catalog plus configured allowed origin and credential;
	// absence of an endpoint is not what prevents the remote call.
	_, err = catalog.Load(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	application, err := New(Config{Environment: "development", Address: "127.0.0.1:0", CatalogFile: catalogPath, DatabasePath: filepath.Join(root, "app.db"), StorageRoot: filepath.Join(root, "storage"), LegacyGeneratedImagesRoot: filepath.Join(root, "generated"), ProviderMode: "mock", AgentMode: "mock", AuthMode: "development", DevelopmentUser: "owner-a", RuntimeSettings: map[string]string{"llm_base_url": trap.URL, "llm_api_key": "configured-private-key"}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer application.generation.Close()
	created := appChatRequest(application.server.Handler, "POST", "/api/chat/sessions", `{}`)
	if created.Code != 201 {
		t.Fatal("owned session creation failed")
	}
	var session struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(created.Body.Bytes(), &session)
	for _, name := range []string{"", "mcp_external_read"} {
		method := "tools/list"
		params := map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/protocolVersion": mcphttp.ProtocolVersion, "io.modelcontextprotocol/clientInfo": map[string]string{"name": "test", "version": "1"}, "io.modelcontextprotocol/clientCapabilities": map[string]any{}}}
		if name != "" {
			method = "tools/call"
			params["name"] = name
			params["arguments"] = map[string]any{}
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		request := httptest.NewRequest("POST", application.schema.AgentConfiguration.Routes.MCP+"?session_id="+session.ID, bytes.NewReader(body))
		request.Header.Set("X-User-ID", "owner-a")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("MCP-Protocol-Version", mcphttp.ProtocolVersion)
		request.Header.Set("Mcp-Method", method)
		request.Header.Set("Mcp-Name", name)
		response := httptest.NewRecorder()
		application.server.Handler.ServeHTTP(response, request)
		if name == "" {
			if response.Code != 200 || strings.Contains(response.Body.String(), "mcp_external_read") || !strings.Contains(response.Body.String(), "mcp_list_session_tasks") {
				t.Fatalf("MCP declarations: %d %s", response.Code, response.Body.String())
			}
		} else if response.Code != 400 {
			t.Fatalf("external call: %d", response.Code)
		}
	}
	if dialed.Load() != 0 {
		t.Fatal("external peer dialed")
	}
}
