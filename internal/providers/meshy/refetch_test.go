package meshy_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

// The upstream refetch path (#295): after the Task ended, the kernel reads the
// accepted Meshy task (GET) once more with paid dispatch denied, then downloads
// the signed result URL. No create (POST) is ever sent. With refine after
// preview the checkpointed refine task is read, never a new refine dispatch.
func TestMeshyRefetchReadsTheAcceptedTaskWithoutCreate(t *testing.T) {
	var posts atomic.Int32
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			posts.Add(1)
			http.Error(w, "create must not be called", http.StatusTeapot)
			return
		}
		switch r.URL.Path {
		case "/openapi/v2/text-to-3d/remote-42", "/openapi/v2/text-to-3d/refine-7":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": r.URL.Path, "status": "SUCCEEDED", "progress": 100,
				"model_urls": map[string]string{"glb": upstream.URL + "/asset.glb"},
			})
		case "/asset.glb":
			w.Header().Set("Content-Type", "model/gltf-binary")
			_, _ = w.Write([]byte("glTF asset"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	parsed, _ := url.Parse(upstream.URL)
	adapter := newMeshyAdapter(t, meshy.Config{BaseURL: upstream.URL, APIKey: "meshy-key", HTTPClient: upstream.Client(), AllowHTTPAssets: true})
	single := generation.Offering{
		ProviderID: "meshy", ModelID: "meshy-test", Capability: generation.TextTo3D, PollInterval: 3 * time.Second,
		AssetHosts:      []string{parsed.Hostname()},
		ProviderOptions: json.RawMessage(`{"endpoint":"text-to-3d","api_version":"v2","model_parameter":"ai_model","result_assets":[{"path":"model_urls","role":"model","mode":"map"}]}`),
	}
	refine := single
	refine.ProviderOptions = json.RawMessage(`{"endpoint":"text-to-3d","api_version":"v2","model_parameter":"ai_model","refine_after_preview":{"set":{"mode":"refine"},"source_parameter":"preview_task_id"},"result_assets":[{"path":"model_urls","role":"model","mode":"map"}]}`)
	ctx := generation.WithDeniedDispatchCheckpointer(context.Background())
	for name, request := range map[string]generation.ObserveRequest{
		"single stage":         {TaskID: "failed-task", AttemptID: "failed-attempt", Offering: single, Job: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "remote-42"}},
		"refine after preview": {TaskID: "failed-task", AttemptID: "failed-attempt", Offering: refine, Input: json.RawMessage(`{"prompt":"castle","mode":"refine"}`), Job: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "remote-42"}, Opaque: json.RawMessage(`{"refine_task_id":"refine-7"}`)},
	} {
		t.Run(name, func(t *testing.T) {
			observed, err := adapter.Observe(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if observed.Phase != generation.UpstreamCompleted || len(observed.Artifacts) != 1 {
				t.Fatalf("refetch observation = %#v", observed)
			}
			body, _, err := adapter.OpenAsset(ctx, generation.OpenAssetRequest{Offering: request.Offering, Claim: observed.Artifacts[0]})
			if err != nil {
				t.Fatal(err)
			}
			defer body.Close()
			if content, _ := io.ReadAll(body); string(content) != "glTF asset" {
				t.Fatalf("content = %q", content)
			}
		})
	}
	if posts.Load() != 0 {
		t.Fatalf("refetch sent %d non-GET requests", posts.Load())
	}
}
