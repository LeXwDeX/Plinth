package generation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

// Exercise real preview/refine HTTP, durable checkpoints, materialization,
// preparation, grant binding, and downstream HTTP with the default Catalog.
func TestDeliveredMeshyDependencyFullKernelWire(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	posts := map[string][]map[string]any{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			format := strings.TrimPrefix(r.URL.Path, "/assets/")
			body, media, err := fixtureModelPayload(format, "model/gltf-binary")
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			w.Header().Set("Content-Type", media)
			_, _ = w.Write(body)
			return
		}
		if r.Method == http.MethodPost {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			mu.Lock()
			posts[r.URL.Path] = append(posts[r.URL.Path], body)
			mu.Unlock()
			id := "downstream-" + strings.TrimPrefix(r.URL.Path, "/openapi/v1/")
			if r.URL.Path == "/openapi/v2/text-to-3d" {
				id = "preview"
				if body["mode"] == "refine" {
					id = "refine"
					if body["preview_task_id"] != "preview" {
						t.Errorf("refine body=%#v", body)
					}
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"result": id})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/openapi/v2/text-to-3d/") {
			urls := map[string]string{}
			for _, format := range []string{"glb", "fbx", "obj", "stl", "usdz", "3mf"} {
				urls[format] = server.URL + "/assets/" + format
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": strings.TrimPrefix(r.URL.Path, "/openapi/v2/text-to-3d/"), "status": "SUCCEEDED", "progress": 100, "model_urls": urls})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "IN_PROGRESS", "progress": 10})
	}))
	defer server.Close()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	config := meshy.Config{BaseURL: server.URL, APIKey: "test", HTTPClient: server.Client(), AllowHTTPAssets: true}
	for _, connection := range c.Application().Runtime.ProviderConnections {
		if connection.AdapterKey == "meshy.v2" {
			config.StatusMapping = connection.StatusMapping
			config.ErrorMapping = connection.ErrorMapping
		}
	}
	adapter, err := meshy.New(config)
	if err != nil {
		t.Fatal(err)
	}
	h := newConfiguredHarness(t, adapter, nil, func(d *generation.Dependencies) {
		registry := providers.NewRegistry()
		if err := registry.Register("meshy.v2", adapter); err != nil {
			t.Fatal(err)
		}
		d.Providers = registry // No lifecycle or resolver fixture wrapper.
		d.Catalog = dependencyLocalCatalog{d.Catalog}
	})
	source, err := h.submitApproved(t, ctx, generation.SubmitCommand{OwnerID: "owner-a", Capability: generation.TextTo3D, OfferingID: "meshy/meshy-7.1/text-to-3d", Input: json.RawMessage(`{"prompt":"a humanoid"}`)})
	if err != nil {
		t.Fatal(err)
	}
	delivered := h.runUntilTerminal(t, "owner-a", source.TaskID)
	if delivered.State != generation.TaskSucceeded {
		t.Fatalf("source state=%s failure=%#v", delivered.State, delivered.Failure)
	}
	before, err := h.repository.Load(ctx, source.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if before.CurrentAttempt.UpstreamJob.RemoteID != "preview" || !strings.Contains(string(before.CurrentAttempt.Opaque), `"refine_task_id":"refine"`) {
		t.Fatalf("source attempt=%#v", before.CurrentAttempt)
	}
	for _, tc := range []struct {
		cap             generation.Capability
		endpoint, extra string
	}{
		{generation.Remesh, "remesh", ""}, {generation.Retexture, "retexture", `,"prompt":"gold"`}, {generation.Rigging, "rigging", ""},
	} {
		t.Run(tc.endpoint, func(t *testing.T) {
			prepared, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: "owner-a", SessionID: "generation-test-session:owner-a", Capability: tc.cap, OfferingID: generation.OfferingID("meshy/processing/" + tc.endpoint), Input: json.RawMessage(fmt.Sprintf(`{"source_task":%q%s}`, source.TaskID, tc.extra))})
			if err != nil {
				t.Fatal(err)
			}
			if len(prepared.Dependencies) != 1 || prepared.Dependencies[0].RemoteID != "refine" {
				t.Fatalf("dependencies=%#v", prepared.Dependencies)
			}
			cmd := authorizePrepared(t, ctx, h.repository, prepared, "wire-"+tc.endpoint)
			if _, err = h.module.SubmitPrepared(ctx, cmd); err != nil {
				t.Fatal(err)
			}
			h.reconcile(t)
			mu.Lock()
			bodies := posts["/openapi/v1/"+tc.endpoint]
			mu.Unlock()
			if len(bodies) != 1 || bodies[0]["input_task_id"] != "refine" {
				t.Fatalf("wire bodies=%#v", bodies)
			}
		})
	}
	after, err := h.repository.Load(ctx, source.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Task.State != generation.TaskSucceeded || after.CurrentAttempt.UpstreamJob.RemoteID != "preview" || string(after.CurrentAttempt.Opaque) != string(before.CurrentAttempt.Opaque) {
		t.Fatal("source terminal/checkpoint mutated")
	}
}

// Only the asset host is changed for the isolated HTTP test server.
type dependencyLocalCatalog struct{ generation.Catalog }

func (c dependencyLocalCatalog) Get(id generation.OfferingID) (generation.Offering, bool) {
	o, ok := c.Catalog.Get(id)
	o.AssetHosts = []string{"127.0.0.1"}
	return o, ok
}
func (c dependencyLocalCatalog) Normalize(cap generation.Capability, id generation.OfferingID, input json.RawMessage) (generation.Offering, json.RawMessage, error) {
	o, p, err := c.Catalog.Normalize(cap, id, input)
	o.AssetHosts = []string{"127.0.0.1"}
	return o, p, err
}
