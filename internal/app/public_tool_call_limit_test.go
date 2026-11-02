package app

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// #275: the public Schema exposes the per-reply tool call budget in force, so
// the composer's counter shows the limit the Agent is held to. The Schema
// default is 20; AGENT_MAX_TOOL_CALLS_PER_REPLY overrides it for the
// orchestrator and the public projection alike.
func TestPublicSchemaCarriesEffectiveToolCallLimit(t *testing.T) {
	for name, run := range map[string]struct {
		override int
		want     int
	}{
		"schema default":   {override: 0, want: 20},
		"runtime override": {override: 35, want: 35},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			application, err := New(Config{
				Environment: "development", Address: "127.0.0.1:0",
				DatabasePath: filepath.Join(root, "art-ai.db"), StorageRoot: filepath.Join(root, "storage"),
				LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
				ProviderMode:              "mock", AgentMode: "mock",
				AuthMode: "development", DevelopmentUser: "owner-a", DevelopmentRoles: []string{"admin"},
				AgentMaxToolCallsPerReply: run.override,
			}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = application.generation.Close() })
			response := httptest.NewRecorder()
			application.server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, application.schema.Runtime.HTTP.Routes.ProductSchema, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("schema status=%d body=%s", response.Code, response.Body.String())
			}
			var body struct {
				Schema struct {
					Chat struct {
						AgentLimits *struct {
							MaxToolCallsPerReply int `json:"max_tool_calls_per_reply"`
						} `json:"agent_limits"`
					} `json:"chat"`
				} `json:"schema"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Schema.Chat.AgentLimits == nil || body.Schema.Chat.AgentLimits.MaxToolCallsPerReply != run.want {
				t.Fatalf("public chat.agent_limits = %+v, want %d", body.Schema.Chat.AgentLimits, run.want)
			}
		})
	}
}
