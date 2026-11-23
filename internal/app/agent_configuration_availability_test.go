package app

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
)

// #445: the shipped workspace-read preset repeats built-in reads, so a fresh
// configuration starts without it: the default effective projection has one
// tool per read query. The administrator can still enable the preset
// explicitly, and then the published configuration decides.
func TestDefaultAppProjectsAvailableLocalMCPPreset(t *testing.T) {
	root := t.TempDir()
	application, err := New(Config{Environment: "development", Address: "127.0.0.1:0", DatabasePath: filepath.Join(root, "art-ai.db"), StorageRoot: filepath.Join(root, "storage"), LegacyGeneratedImagesRoot: filepath.Join(root, "generated"), ProviderMode: "mock", AgentMode: "mock", AuthMode: "development", DevelopmentUser: "owner-a", DevelopmentRoles: []string{"admin"}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer application.generation.Close()
	routes := application.schema.AgentConfiguration.Routes
	response := httptest.NewRecorder()
	application.server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, routes.Admin, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("management status=%d body=%s", response.Code, response.Body.String())
	}
	var managed struct {
		Revision      string          `json:"revision"`
		Configuration json.RawMessage `json:"configuration"`
		MCPPresets    []struct {
			ID             string `json:"id"`
			Available      bool   `json:"available"`
			DefaultEnabled bool   `json:"default_enabled"`
		} `json:"mcp_presets"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &managed); err != nil {
		t.Fatal(err)
	}
	if len(managed.MCPPresets) != 1 || managed.MCPPresets[0].ID != "workspace-read" || !managed.MCPPresets[0].Available {
		t.Fatalf("default local connection unavailable: %#v", managed.MCPPresets)
	}
	if managed.MCPPresets[0].DefaultEnabled {
		t.Fatal("a preset that repeats built-in reads is enabled by default")
	}
	var configuration agentconfiguration.Configuration
	if err := json.Unmarshal(managed.Configuration, &configuration); err != nil {
		t.Fatal(err)
	}
	if len(configuration.EnabledMCPPresetIDs) != 0 {
		t.Fatalf("default configuration enables MCP presets: %v", configuration.EnabledMCPPresetIDs)
	}
	effective := func() string {
		response = httptest.NewRecorder()
		application.server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, routes.Effective, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("effective status=%d body=%s", response.Code, response.Body.String())
		}
		return response.Body.String()
	}
	first := effective()
	for _, builtIn := range []string{`"list_offerings"`, `"get_task"`} {
		if !strings.Contains(first, builtIn) {
			t.Fatalf("default projection lost built-in read %s: %s", builtIn, first)
		}
	}
	for _, duplicate := range []string{"mcp_list_offerings", "mcp_list_session_tasks", "mcp_get_task_assets"} {
		if strings.Contains(first, duplicate) {
			t.Fatalf("default projection still offers %s next to its built-in read: %s", duplicate, first)
		}
	}
	// The administrator's explicit choice still works: publish the preset on.
	configuration.EnabledMCPPresetIDs = []string{"workspace-read"}
	body, err := json.Marshal(map[string]any{"expected_revision": managed.Revision, "configuration": configuration})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, routes.Publish, bytes.NewReader(body))
	request.Header.Set("X-User-ID", "owner-a")
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	application.server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("publish status=%d body=%s", response.Code, response.Body.String())
	}
	if published := effective(); !strings.Contains(published, "mcp_list_offerings") {
		t.Fatalf("explicitly enabled preset absent from effective projection: %s", published)
	}
}

// #115: the Creative Lab Offerings are disabled, so neither the management nor
// the effective projection lists their tools or teaches their prompt section.
func TestDefaultAppAgentConfigurationHidesCapabilitiesWithoutEnabledOfferings(t *testing.T) {
	root := t.TempDir()
	application, err := New(Config{Environment: "development", Address: "127.0.0.1:0", DatabasePath: filepath.Join(root, "art-ai.db"), StorageRoot: filepath.Join(root, "storage"), LegacyGeneratedImagesRoot: filepath.Join(root, "generated"), ProviderMode: "mock", AgentMode: "mock", AuthMode: "development", DevelopmentUser: "owner-a", DevelopmentRoles: []string{"admin"}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer application.generation.Close()
	routes := application.schema.AgentConfiguration.Routes
	for _, path := range []string{routes.Admin, routes.Effective + "?creative_domain=model-3d", routes.Effective + "?creative_domain=image"} {
		response := httptest.NewRecorder()
		application.server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		body := response.Body.String()
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, response.Code, body)
		}
		for _, leaked := range []string{"creative_lab", "Creative Lab"} {
			if strings.Contains(body, leaked) {
				t.Fatalf("%s still exposes %q", path, leaked)
			}
		}
		if !strings.Contains(body, "create_model_3d") && !strings.Contains(path, "image") {
			t.Fatalf("%s lost unrelated tools", path)
		}
	}
}
