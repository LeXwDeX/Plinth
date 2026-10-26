package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

// The shared MIME/HTTP boundary uses a real GLB body and CAS. Format-specific
// FBX/3MF claim projection is covered by Meshy's scalar declaration tests.
func TestMeshyScalarMediaTypeMaterializationChecksHTTPAndActualBytes(t *testing.T) {
	for _, tc := range []struct {
		name, responseType string
		body               []byte
		want               generation.TaskState
	}{
		{"generic CDN", "application/octet-stream", validGLB("glb"), generation.TaskSucceeded},
		{"concrete wrong MIME", "text/html", validGLB("glb"), generation.TaskFailed},
		{"invalid bytes", "application/octet-stream", []byte("not a model"), generation.TaskFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			var posts, downloads atomic.Int64
			var upstream *httptest.Server
			upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/openapi/v1/motion":
					if r.Method != http.MethodPost {
						t.Errorf("unexpected %s", r.Method)
					}
					posts.Add(1)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"result":"private-job"}`))
				case "/openapi/v1/motion/private-job":
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"status": "SUCCEEDED", "progress": 100, "result_url": upstream.URL + "/artifact.bin"})
				case "/artifact.bin":
					downloads.Add(1)
					w.Header().Set("Content-Type", tc.responseType)
					_, _ = w.Write(tc.body)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()
			host, _ := url.Parse(upstream.URL)
			one := 1
			offering := generation.Offering{ID: "test/scalar-media", Revision: "1", ProviderID: "meshy", ModelID: "test", Capability: "text-to-motion", Domain: generation.DomainModel3D, AdapterKey: "scalar-test", Enabled: true, Timeout: time.Minute, PollInterval: time.Second, MaterializeAttempts: 1, Retry: generation.RetryPolicy{MaxAttempts: 1}, AssetHosts: []string{host.Hostname()}, Outputs: []generation.AssetRequirement{{Role: "clip", Variant: "glb", Minimum: &one, MediaTypes: []string{"model/gltf-binary"}, MaxBytes: 1 << 20}}, ProviderOptions: json.RawMessage(`{"endpoint":"motion","api_version":"v1","result_assets":[{"path":"result_url","role":"clip","variant":"glb","mode":"scalar","media_type":"model/gltf-binary"}]}`)}
			if err := meshy.ValidateOffering(offering); err != nil {
				t.Fatal(err)
			}
			adapter, err := meshy.New(meshy.Config{BaseURL: upstream.URL, APIKey: "test", HTTPClient: upstream.Client(), AllowHTTPAssets: true, StatusMapping: &generation.StatusMapping{UpstreamStatus: map[string]generation.StatusRule{"succeeded": {Phase: "completed"}}}})
			if err != nil {
				t.Fatal(err)
			}
			registry := providers.NewRegistry()
			if err := registry.Register(offering.AdapterKey, adapter); err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			repository, err := sqlite.Open(filepath.Join(root, "tasks.db"))
			if err != nil {
				t.Fatal(err)
			}
			store, err := local.New(filepath.Join(root, "cas"))
			if err != nil {
				t.Fatal(err)
			}
			clock := &fakeClock{now: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)}
			repository.SetClock(clock.Now)
			module, err := generation.New(generation.Dependencies{Repository: repository, Catalog: printReportCatalog{offering}, Providers: registry, Assets: store, Clock: clock, SchemaVersion: "scalar-test"})
			if err != nil {
				t.Fatal(err)
			}
			defer module.Close()
			state := submitFixtureState{}
			accepted, err := state.submitApproved(t, ctx, module, repository, clock, generation.SubmitCommand{OwnerID: "owner", Capability: offering.Capability, OfferingID: offering.ID, Input: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			var task generation.Task
			for step := 0; step < 8; step++ {
				if _, err := module.ReconcileOnce(ctx, generation.CoordinatorConfig{WorkerID: "scalar-worker", LeaseFor: time.Minute, BatchSize: 1}); err != nil {
					t.Fatal(err)
				}
				clock.Advance(31 * time.Second)
				task, err = module.Get(ctx, "owner", accepted.TaskID)
				if err != nil {
					t.Fatal(err)
				}
				if task.State.Terminal() {
					break
				}
			}
			if task.State != tc.want || posts.Load() != 1 || downloads.Load() != 1 {
				t.Fatalf("state=%s failure=%+v posts=%d downloads=%d", task.State, task.Failure, posts.Load(), downloads.Load())
			}
			if tc.want == generation.TaskFailed {
				if len(task.Assets) != 0 {
					t.Fatal("rejected asset was published")
				}
				return
			}
			if len(task.Assets) != 1 || task.Assets[0].MediaType != "model/gltf-binary" || task.Assets[0].Role != "clip" {
				t.Fatalf("wrong stored contract: %+v", task.Assets)
			}
			body, err := store.Open(ctx, task.Assets[0])
			if err != nil {
				t.Fatal(err)
			}
			defer body.Close()
			actual, err := io.ReadAll(body)
			if err != nil || !bytes.Equal(actual, tc.body) {
				t.Fatalf("changed delivery bytes: %v", err)
			}
		})
	}
}
