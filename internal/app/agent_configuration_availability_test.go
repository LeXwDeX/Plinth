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

func TestDefaultAppProjectsAvailableLocalMCPPreset(t *testing.T) {
	root := t.TempDir()
	application, err := New(Config{Environment: "development", Address: "127.0.0.1:0", DatabasePath: filepath.Join(root, "art-ai.db"), StorageRoot: filepath.Join(root, "storage"), LegacyGeneratedImagesRoot: filepath.Join(root, "generated"), ProviderMode: "mock", AgentMode: "mock", AuthMode: "development", DevelopmentUser: "owner-a", DevelopmentRoles: []string{"admin"}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer application.generation.Close()
	response := httptest.NewRecorder()
	application.server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, application.schema.AgentConfiguration.Routes.Admin, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("management status=%d body=%s", response.Code, response.Body.String())
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
	if len(managed.MCPPresets) != 1 || managed.MCPPresets[0].ID != "workspace-read" || !managed.MCPPresets[0].Available {
		t.Fatalf("default local connection unavailable: %#v", managed)
	}
	response = httptest.NewRecorder()
	application.server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, application.schema.AgentConfiguration.Routes.Effective, nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "mcp_list_offerings") {
		t.Fatalf("available local tools absent from effective projection: %d %s", response.Code, response.Body.String())
	}
}
