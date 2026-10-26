package tripo_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

// The upstream refetch path (#295): after the Task ended, the kernel reads the
// accepted Tripo task (GET tasks/{id}) once more with paid dispatch denied; the
// fresh output URLs are downloaded. No create (POST) is ever sent.
func TestTripoRefetchReadsTheAcceptedTaskWithoutCreate(t *testing.T) {
	var posts atomic.Int32
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			posts.Add(1)
			http.Error(w, "create must not be called", http.StatusTeapot)
			return
		}
		switch r.URL.Path {
		case "/tasks/tripo-42":
			writeEnvelope(w, map[string]any{
				"task_id": "tripo-42", "status": "success", "progress": 100,
				"output": map[string]string{"pbr_model_url": upstream.URL + "/model.glb"},
			})
		case "/model.glb":
			w.Header().Set("Content-Type", "model/gltf-binary")
			_, _ = w.Write([]byte("glTF tripo"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	errorMapping, statusMapping := declaredTripoMappings(t)
	adapter, err := tripo.New(tripo.Config{BaseURL: upstream.URL, APIKey: "tripo-key", HTTPClient: upstream.Client(),
		AllowHTTPAssets: true, ErrorMapping: errorMapping, StatusMapping: statusMapping})
	if err != nil {
		t.Fatal(err)
	}
	offering := generation.Offering{
		ProviderID: "tripo", Capability: generation.TextTo3D, PollInterval: time.Second,
		AssetHosts:      []string{hostname(t, upstream.URL)},
		ProviderOptions: []byte(`{"model":"v-test","endpoint":"generation/text-to-model","observe_endpoint":"tasks/{task_id}","upload_endpoint":"files"}`),
	}
	ctx := generation.WithDeniedDispatchCheckpointer(context.Background())
	observed, err := adapter.Observe(ctx, generation.ObserveRequest{
		TaskID: "failed-task", AttemptID: "failed-attempt", Offering: offering,
		Job: generation.UpstreamJob{ProviderID: "tripo", RemoteID: "tripo-42"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Phase != generation.UpstreamCompleted || len(observed.Artifacts) == 0 || observed.Artifacts[0].Role != "model" {
		t.Fatalf("refetch observation = %#v", observed)
	}
	body, _, err := adapter.OpenAsset(ctx, generation.OpenAssetRequest{Offering: offering, Claim: observed.Artifacts[0]})
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	if content, _ := io.ReadAll(body); string(content) != "glTF tripo" || posts.Load() != 0 {
		t.Fatalf("content = %q posts=%d", content, posts.Load())
	}
}
