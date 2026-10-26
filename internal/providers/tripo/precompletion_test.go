package tripo_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestFullFormatOfferingCompletesOnlyWithSixConversionClaims(t *testing.T) {
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
	for round := 0; round < 30; round++ {
		observation, err := adapter.Observe(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if observation.Phase == generation.UpstreamCompleted {
			if len(converts) != 6 || len(observation.Artifacts) != 7 {
				t.Fatalf("completed early: converts=%v artifacts=%#v", converts, observation.Artifacts)
			}
			variants := map[string]bool{}
			primaryModels := 0
			for _, claim := range observation.Artifacts {
				variants[claim.Variant] = true
				if claim.Role == "model" {
					primaryModels++
					if claim.Locator != upstream.URL+"/gltf.glb" {
						t.Fatalf("primary model used unconverted source: %#v", claim)
					}
				}
			}
			if primaryModels != 1 {
				t.Fatalf("primary model claims = %d, want 1", primaryModels)
			}
			for _, format := range []string{"gltf", "usdz", "fbx", "obj", "stl", "3mf"} {
				if !variants["converted."+format] {
					t.Fatalf("missing %s: %#v", format, observation.Artifacts)
				}
			}
			return
		}
		if observation.Phase != generation.UpstreamRunning || len(observation.Artifacts) != 0 {
			t.Fatalf("premature result: %#v", observation)
		}
		if len(converts) > 0 && (observation.Progress != 98 || observation.ObservedAt.IsZero()) {
			t.Fatalf("private conversion observation must retain ordered running progress: %#v", observation)
		}
		request.Opaque = observation.Opaque
	}
	t.Fatal("conversions did not complete")
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

func TestFullFormatChildFailureFailsPublicTaskWithoutMoreCharges(t *testing.T) {
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
	for round := 0; round < 5; round++ {
		observation, err := adapter.Observe(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if observation.Phase == generation.UpstreamFailed {
			if converts != 1 || len(observation.Artifacts) != 0 {
				t.Fatalf("unexpected terminal result: %#v, converts=%d", observation, converts)
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
	if observation.Phase != generation.UpstreamRunning || len(dispatched) != 1 || dispatched[0] == "GLTF" {
		t.Fatalf("confirmed conversion repeated: %#v, formats=%v", observation, dispatched)
	}
}
