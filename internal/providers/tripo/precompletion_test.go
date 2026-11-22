package tripo_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func fullTextFrozenInput(t *testing.T, c *catalog.Catalog) json.RawMessage {
	t.Helper()
	_, input, err := c.Normalize(generation.TextTo3D, "tripo/tripo-v3.1-full/text-to-3d", json.RawMessage(`{"prompt":"castle","output_formats":["GLTF","USDZ","FBX","OBJ","STL","3MF"],"authorize_extra_credits":true}`))
	if err != nil {
		t.Fatal(err)
	}
	return input
}

// The mixed Offering (#462) holds the public task only for the blocking GLTF
// conversion: the task completes with the self-contained GLB as its primary
// model, and the five requested formats are converted after success, one claim
// per round, until every one of them has landed.
func TestMixedOfferingCompletesWithThePrimaryModelThenConvertsTheRest(t *testing.T) {
	var converts []string
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/tasks/base":
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
				"task_id": "base", "status": "success", "progress": 100,
				"output": map[string]any{"model_url": upstream.URL + "/base.glb"},
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/models/convert":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			format, _ := body["format"].(string)
			converts = append(converts, format)
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"task_id": "child-" + strings.ToLower(format)}})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks/child-"):
			format := strings.TrimPrefix(r.URL.Path, "/tasks/child-")
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
				"task_id": "child-" + format, "status": "success", "progress": 100,
				"output": map[string]any{"model_url": upstream.URL + "/" + format + ".glb"},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := c.Get("tripo/tripo-v3.1-full/text-to-3d")
	if !ok {
		t.Fatal("missing full-format offering")
	}
	input := fullTextFrozenInput(t, c)
	adapter := newAdapter(t, upstream)
	request := generation.ObserveRequest{
		AttemptID: "attempt", Offering: offering,
		Job: generation.UpstreamJob{ProviderID: "tripo", RemoteID: "base"}, Input: input,
	}
	// Before success only the blocking conversion runs.
	for round := 0; round < 30; round++ {
		observation, err := adapter.Observe(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if observation.Phase == generation.UpstreamCompleted {
			if !slices.Equal(converts, []string{"GLTF"}) || len(observation.Artifacts) != 2 {
				t.Fatalf("completed early: converts=%v artifacts=%#v", converts, observation.Artifacts)
			}
			primary := 0
			for _, claim := range observation.Artifacts {
				if claim.Role != "model" {
					continue
				}
				primary++
				if claim.Locator != upstream.URL+"/gltf.glb" {
					t.Fatalf("primary model used unconverted source: %#v", claim)
				}
			}
			if primary != 1 {
				t.Fatalf("primary model claims = %d, want 1", primary)
			}
			request.Opaque = observation.Opaque
			break
		}
		if observation.Phase != generation.UpstreamRunning || len(observation.Artifacts) != 0 {
			t.Fatalf("premature result: %#v", observation)
		}
		if len(converts) > 0 && (observation.Progress != 98 || observation.ObservedAt.IsZero()) {
			t.Fatalf("private conversion observation must retain ordered running progress: %#v", observation)
		}
		request.Opaque = observation.Opaque
	}
	// After success the requested formats are converted one per round.
	request.PostSuccess = true
	variants := map[string]bool{}
	for round := 0; round < 60; round++ {
		observation, err := adapter.Observe(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if observation.Phase == generation.UpstreamFailed {
			t.Fatalf("post-success run failed: %#v", observation.Failure)
		}
		for _, claim := range observation.Artifacts {
			if claim.Role == "converted_model" {
				variants[claim.Variant] = true
			}
		}
		request.Opaque = observation.Opaque
		if observation.Phase == generation.UpstreamCompleted && len(observation.Artifacts) == 0 {
			break
		}
	}
	if len(converts) != 6 {
		t.Fatalf("converts = %v, want the blocking GLTF and the five requested formats", converts)
	}
	// converted.gltf was claimed before success; the other five come from the
	// post-success run.
	for _, format := range []string{"usdz", "fbx", "obj", "stl", "3mf"} {
		if !variants["converted."+format] {
			t.Fatalf("missing converted.%s: %v", format, variants)
		}
	}
}

func TestAmbiguousFullFormatDispatchNeverResendsPaidConversion(t *testing.T) {
	converts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			converts++
		}
		writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
			"task_id": "base", "status": "success", "progress": 100,
			"output": map[string]any{"model_url": "https://cdn.tripo3d.ai/base.glb"},
		}})
	}))
	defer upstream.Close()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, _ := c.Get("tripo/tripo-v3.1-full/text-to-3d")
	request := generation.ObserveRequest{
		AttemptID: "attempt", Offering: offering,
		Job:    generation.UpstreamJob{ProviderID: "tripo", RemoteID: "base"},
		Input:  fullTextFrozenInput(t, c),
		Opaque: json.RawMessage(`{"stage":"models/convert","details":{"conversions":{"GLTF":{"status":"dispatching"}}}}`),
	}
	observation, err := newAdapter(t, upstream).Observe(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Phase != generation.UpstreamFailed || converts != 0 || len(observation.Artifacts) != 0 {
		t.Fatalf("ambiguous dispatch retried or completed: %#v; converts=%d", observation, converts)
	}
}

// #460 A1: a failed conversion child is resubmitted for its own format (the
// official billing page: failed tasks are not charged), at most five times with
// 1, 2, 4… second backoff; only when the budget is spent does the public task
// fail, and every attempt is a new private permit.
func TestFullFormatChildFailureRetriesThenFailsPublicTask(t *testing.T) {
	converts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			converts++
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"task_id": "child"}})
			return
		}
		if r.URL.Path == "/tasks/child" {
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"task_id": "child", "status": "failed"}})
			return
		}
		writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
			"task_id": "base", "status": "success", "progress": 100,
			"output": map[string]any{"model_url": "https://cdn.tripo3d.ai/base.glb"},
		}})
	}))
	defer upstream.Close()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, _ := c.Get("tripo/tripo-v3.1-full/text-to-3d")
	request := generation.ObserveRequest{
		AttemptID: "attempt", Offering: offering,
		Job:   generation.UpstreamJob{ProviderID: "tripo", RemoteID: "base"},
		Input: fullTextFrozenInput(t, c),
	}
	adapter := newAdapter(t, upstream)
	for round := 0; round < 60; round++ {
		observation, err := adapter.Observe(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if observation.Phase == generation.UpstreamFailed {
			// Only the blocking GLTF conversion runs before success: the five
			// requested formats are never dispatched once the task is failing.
			if converts != 6 || len(observation.Artifacts) != 0 {
				t.Fatalf("unexpected terminal result: %#v, converts=%d", observation, converts)
			}
			if observation.Failure == nil || observation.Failure.Retryable || observation.Failure.Ambiguous {
				t.Fatalf("child failure must be a definite failure: %+v", observation.Failure)
			}
			return
		}
		if observation.Phase != generation.UpstreamRunning {
			t.Fatalf("unexpected phase %s", observation.Phase)
		}
		request.Opaque = observation.Opaque
	}
	t.Fatal("child failure did not terminate")
}

func TestConfirmedPrivateConversionIsNotPaidAgainAfterRestart(t *testing.T) {
	var dispatched []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			dispatched = append(dispatched, body["format"].(string))
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"task_id": "next-child"}})
			return
		}
		writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
			"task_id": "base", "status": "success", "progress": 100,
			"output": map[string]any{"model_url": "https://cdn.tripo3d.ai/base.glb"},
		}})
	}))
	defer upstream.Close()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, _ := c.Get("tripo/tripo-v3.1-full/text-to-3d")
	request := generation.ObserveRequest{
		AttemptID: "attempt", Offering: offering,
		Job:    generation.UpstreamJob{ProviderID: "tripo", RemoteID: "base"},
		Input:  fullTextFrozenInput(t, c),
		Opaque: json.RawMessage(`{"conversions":{"GLTF":{"status":"done","remote_id":"old-child","locator":"https://cdn.tripo3d.ai/old.gltf"}}}`),
	}
	observation, err := newAdapter(t, upstream).Observe(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Phase != generation.UpstreamCompleted || len(dispatched) != 0 {
		t.Fatalf("confirmed conversion repeated: %#v, formats=%v", observation, dispatched)
	}
	if len(observation.Artifacts) != 2 {
		t.Fatalf("the confirmed GLTF conversion was not delivered: %#v", observation.Artifacts)
	}
}
