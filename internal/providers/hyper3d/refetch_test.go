package hyper3d_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The upstream refetch path (#295): after the Task ended, the kernel reads the
// accepted job once more with paid dispatch denied. Rodin answers from its
// status and download calls; the create endpoint is never touched.
func TestRodinRefetchReadsTheAcceptedJobWithoutCreate(t *testing.T) {
	var creates, downloads atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/rodin":
			creates.Add(1)
			http.Error(w, "create must not be called", http.StatusTeapot)
		case "/api/v2/status":
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"jobs":[{"status":"Done"}]}`)
		case "/api/v2/download":
			downloads.Add(1)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"list": []map[string]string{{"name": "asset.glb", "url": server.URL + "/files/asset.glb"}}})
		case "/files/asset.glb":
			w.Header().Set("Content-Type", "model/gltf-binary")
			_, _ = w.Write(testGLB())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	port := adapter(t, server.URL+"/api/v2", server.Client(), nil)
	step := offering(generation.TextTo3D)
	step.AssetHosts = []string{"127.0.0.1"}
	opaque := json.RawMessage(`{"task_uuid":"task-uuid","subscription_key":"status-secret"}`)
	ctx := generation.WithDeniedDispatchCheckpointer(context.Background())
	observed, err := port.Observe(ctx, generation.ObserveRequest{
		TaskID: "failed-task", AttemptID: "failed-attempt", Offering: step,
		Job: generation.UpstreamJob{ProviderID: "hyper3d", RemoteID: "task-uuid"}, Opaque: opaque,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Phase != generation.UpstreamCompleted || len(observed.Artifacts) != 1 || downloads.Load() != 1 {
		t.Fatalf("refetch observation = %#v downloads=%d", observed, downloads.Load())
	}
	body, _, err := port.OpenAsset(ctx, generation.OpenAssetRequest{Offering: step, Claim: observed.Artifacts[0]})
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	payload, _ := io.ReadAll(body)
	if !bytes.Equal(payload, testGLB()) || creates.Load() != 0 {
		t.Fatalf("payload=%q creates=%d", payload, creates.Load())
	}
}
