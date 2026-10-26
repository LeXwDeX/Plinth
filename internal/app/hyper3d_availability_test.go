package app

import (
	"bytes"
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

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestHyper3DRuntimeAvailabilityThroughApplication(t *testing.T) {
	for _, tc := range []struct {
		name          string
		key           string
		localAddress  bool
		removeDefault bool
		available     bool
	}{
		{name: "missing_key_with_schema_default"},
		{name: "missing_key_with_configured_address", localAddress: true},
		{name: "synthetic_key_with_local_address", key: "synthetic-hyper3d-availability-key", localAddress: true, available: true},
		{name: "key_without_any_address", key: "synthetic-hyper3d-availability-key", removeDefault: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			catalogFile, source := hyper3DAvailabilityCatalog(t, root, tc.removeDefault)
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				t.Errorf("availability check contacted upstream: %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			t.Cleanup(upstream.Close)
			settings := map[string]string{
				"llm_model": "availability-test-model",
				// A different 3D Provider is configured, so an explicit Rodin
				// selection must not silently prepare a Meshy task instead.
				"meshy_api_key":   "synthetic-meshy-availability-key",
				"meshy_base_url":  upstream.URL,
				"hyper3d_api_key": tc.key,
			}
			if tc.localAddress {
				settings["hyper3d_base_url"] = upstream.URL + "/api/v2"
			}
			config := Config{
				RuntimeSettings: settings, CatalogFile: catalogFile,
				Environment: "development", Address: "127.0.0.1:0",
				DatabasePath: filepath.Join(root, "art-ai.db"), StorageRoot: filepath.Join(root, "storage"),
				LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
				ProviderMode:              "live", AgentMode: "mock", AuthMode: "development",
				DevelopmentUser: "hyper3d-availability-owner", DevelopmentRoles: []string{"user"},
			}
			application, err := New(config, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = application.generation.Close() })
			request := func(method, path, body string) *httptest.ResponseRecorder {
				t.Helper()
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Origin", "http://example.com")
				response := httptest.NewRecorder()
				application.server.Handler.ServeHTTP(response, r)
				return response
			}

			// Runtime availability is an Offering projection, not an
			// Application Schema field or an upstream credential probe.
			response := request(http.MethodGet, "/api/v1/offerings", "")
			var projected struct {
				Offerings []struct {
					ID        generation.OfferingID `json:"id"`
					Revision  string                `json:"revision"`
					Available *bool                 `json:"available"`
				} `json:"offerings"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &projected); err != nil || response.Code != http.StatusOK {
				t.Fatalf("offerings=%d %s err=%v", response.Code, response.Body.String(), err)
			}
			for id, want := range map[generation.OfferingID]bool{
				"hyper3d/rodin-gen-2.5/text-to-3d":  tc.available,
				"hyper3d/rodin-gen-2.5/image-to-3d": tc.available,
				"meshy/meshy-7.1/text-to-3d":        true,
			} {
				declared, ok := source.Get(id)
				if !ok || !declared.Enabled {
					t.Fatalf("test requires the enabled current Offering %s", id)
				}
				found := false
				for _, offering := range projected.Offerings {
					if offering.ID != id {
						continue
					}
					found = true
					if offering.Available == nil || *offering.Available != want || offering.Revision != declared.Revision ||
						application.generation.OfferingAvailable(id) != want {
						t.Fatalf("Offering %s projection=%+v want available=%t revision=%s", id, offering, want, declared.Revision)
					}
				}
				if !found {
					t.Fatalf("Offering %s disappeared instead of reporting its availability", id)
				}
			}

			publicSchema := request(http.MethodGet, "/api/v1/schema", "")
			var public struct {
				Schema struct {
					Runtime map[string]json.RawMessage `json:"runtime"`
				} `json:"schema"`
			}
			if err := json.Unmarshal(publicSchema.Body.Bytes(), &public); err != nil || publicSchema.Code != http.StatusOK || len(public.Schema.Runtime) == 0 {
				t.Fatalf("public schema status=%d err=%v", publicSchema.Code, err)
			}
			for _, field := range []string{"configuration", "provider_connections", "provider_accounts"} {
				if _, leaked := public.Schema.Runtime[field]; leaked {
					t.Fatalf("public Schema leaked runtime.%s", field)
				}
			}

			// Rodin's native price model is not registered in legacy discovery.
			// Its absence there is the same with or without a key and is not
			// evidence of runtime unavailability; /offerings above is that evidence.
			discovery := request(http.MethodGet, "/api/providers?capability=text-to-3d&projection=extended", "")
			var discovered struct {
				Providers []struct {
					ID          string `json:"id"`
					ProviderKey string `json:"providerKey"`
				} `json:"providers"`
			}
			if err := json.Unmarshal(discovery.Body.Bytes(), &discovered); err != nil || discovery.Code != http.StatusOK || len(discovered.Providers) == 0 {
				t.Fatalf("discovery=%d %s err=%v", discovery.Code, discovery.Body.String(), err)
			}
			for _, provider := range discovered.Providers {
				if provider.ProviderKey == "hyper3d" || provider.ID == "rodin-gen-2.5" {
					t.Fatalf("legacy discovery unexpectedly advertised Rodin: %+v", provider)
				}
			}
			for _, body := range [][]byte{publicSchema.Body.Bytes(), response.Body.Bytes(), discovery.Body.Bytes()} {
				for _, secret := range []string{"synthetic-hyper3d-availability-key", "synthetic-meshy-availability-key", "hyper3d_api_key", "hyper3d_base_url", upstream.URL} {
					if bytes.Contains(body, []byte(secret)) {
						t.Fatalf("public projection leaked server configuration %q", secret)
					}
				}
			}

			session := request(http.MethodPost, "/api/chat/sessions", `{"title":"Hyper3D configuration availability"}`)
			var created struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(session.Body.Bytes(), &created); err != nil || session.Code != http.StatusCreated || created.ID == "" {
				t.Fatalf("session=%d %s err=%v", session.Code, session.Body.String(), err)
			}
			prepare := request(http.MethodPost, "/api/v1/tasks/prepare",
				`{"session_id":"`+created.ID+`","capability":"text-to-3d","offering_id":"hyper3d/rodin-gen-2.5/text-to-3d","input":{"prompt":"a carved oak chair","authorize_extra_credits":true}}`)
			if tc.available {
				var prepared struct {
					ID         string `json:"prepared_input_id"`
					OfferingID string `json:"offering_id"`
				}
				if err := json.Unmarshal(prepare.Body.Bytes(), &prepared); err != nil || prepare.Code != http.StatusCreated || prepared.ID == "" || prepared.OfferingID != "hyper3d/rodin-gen-2.5/text-to-3d" {
					t.Fatalf("configured Rodin prepare=%d %s err=%v", prepare.Code, prepare.Body.String(), err)
				}
			} else {
				var problem struct {
					Code string `json:"code"`
				}
				if err := json.Unmarshal(prepare.Body.Bytes(), &problem); err != nil || prepare.Code != http.StatusServiceUnavailable || problem.Code != string(generation.ErrUnavailable) {
					t.Fatalf("unconfigured Rodin must not fall back: %d %s err=%v", prepare.Code, prepare.Body.String(), err)
				}
			}
			if calls.Load() != 0 {
				t.Fatalf("availability/Prepare made %d upstream requests", calls.Load())
			}
		})
	}
}

func hyper3DAvailabilityCatalog(t *testing.T, root string, removeDefault bool) (string, *catalog.Catalog) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "catalog", "default_catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	settings := document["application"].(map[string]any)["runtime"].(map[string]any)["configuration"].(map[string]any)["settings"].(map[string]any)
	baseURL := settings["hyper3d_base_url"].(map[string]any)
	if baseURL["default"] != "https://api.hyper3d.com/api/v2" {
		t.Fatalf("recheck the Hyper3D default address contract: %v", baseURL["default"])
	}
	if removeDefault {
		// An absent environment value normally uses the declared default.
		// Only this private fixture removes it; no account or Offering is changed.
		baseURL["default"] = ""
		delete(baseURL, "default_by_environment")
	}
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	source, err := catalog.Load(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "catalog.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, source
}
