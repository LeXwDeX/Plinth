package tripo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestPreCompletionRetainsCheckpointOnNonCompletedSource(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := c.Get("tripo/tripo-v3.1-full/text-to-3d")
	if !ok {
		t.Fatal("missing full-format offering")
	}
	checkpoints := []struct {
		name string
		raw  json.RawMessage
	}{
		{"submitted", json.RawMessage(`{"conversions":{"GLTF":{"status":"submitted","remote_id":"synthetic-child"}}}`)},
		{"done", json.RawMessage(`{"conversions":{"GLTF":{"status":"done","remote_id":"synthetic-child","locator":"https://example.invalid/synthetic.glb"}}}`)},
		{"ambiguous", json.RawMessage(`{"stage":"models/convert","details":{"conversions":{"GLTF":{"status":"dispatching"}}}}`)},
	}
	sources := []struct {
		status   string
		progress int
		phase    generation.UpstreamPhase
		failed   bool
	}{
		{"queued", 0, generation.UpstreamAccepted, false},
		{"running", 99, generation.UpstreamRunning, false},
		{"failed", 99, generation.UpstreamFailed, true},
		{"cancelled", 99, generation.UpstreamCanceled, true},
	}
	for _, source := range sources {
		for _, checkpoint := range checkpoints {
			t.Run(source.status+"/"+checkpoint.name, func(t *testing.T) {
				var gets, posts atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet || r.URL.Path != "/tasks/synthetic-base" {
						posts.Add(1)
						http.Error(w, "unexpected private operation", http.StatusBadRequest)
						return
					}
					gets.Add(1)
					writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
						"task_id": "synthetic-base", "status": source.status, "progress": source.progress,
					}})
				}))
				t.Cleanup(upstream.Close)
				request := generation.ObserveRequest{
					AttemptID: "synthetic-attempt", Offering: offering,
					Job:    generation.UpstreamJob{ProviderID: "tripo", RemoteID: "synthetic-base"},
					Input:  json.RawMessage(`{"output_formats":["GLTF","USDZ","FBX","OBJ","STL","3MF"],"authorize_extra_credits":true}`),
					Opaque: checkpoint.raw,
				}
				observation, err := newAdapter(t, upstream).Observe(context.Background(), request)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(observation.Opaque, checkpoint.raw) {
					t.Fatal("source observation erased or changed private conversion checkpoint")
				}
				if observation.Phase != source.phase || observation.Progress != source.progress {
					t.Fatalf("source state changed: phase=%s progress=%d", observation.Phase, observation.Progress)
				}
				if (observation.Failure != nil) != source.failed {
					t.Fatalf("source failure changed: %#v", observation.Failure)
				}
				if len(observation.Artifacts) != 0 || observation.Dispatch != nil || gets.Load() != 1 || posts.Load() != 0 {
					t.Fatal("non-completed source advanced a conversion or emitted assets")
				}
			})
		}
	}
}
