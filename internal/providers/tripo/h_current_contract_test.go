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
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

// Dated official request fixture (retrieved 2026-10-17):
// https://developers.tripo3d.ai/en/docs/generation-text-to-model/standard
// https://developers.tripo3d.ai/en/docs/generation-image-to-model/standard
// https://developers.tripo3d.ai/en/docs/task-query
// Both endpoints require Bearer JSON POSTs under /v3; geometry's highest
// documented enum is detailed, while the newest texture model is pinned by
// the independent texture_version field. The success task body below follows
// the official example; the failure task body is a synthetic negative control.
func TestTripoHCurrentCatalogInputReachesOfficialWire(t *testing.T) {
	for _, tc := range []struct {
		capability      generation.Capability
		input, endpoint string
	}{
		{generation.TextTo3D, `{"prompt":"a brass compass","authorize_extra_credits":true}`, "/v3/generation/text-to-model"},
		{generation.ImageTo3D, `{"reference_assets":["asset-one"],"authorize_extra_credits":true}`, "/v3/generation/image-to-model"},
	} {
		t.Run(string(tc.capability), func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			id := generation.OfferingID("tripo/tripo-v3.1-full/" + string(tc.capability))
			offering, frozen, err := c.Normalize(tc.capability, id, json.RawMessage(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			var posted map[string]any
			calls := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.URL.Path)
				if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer tripo-key" {
					t.Errorf("unexpected method/auth: %s %q", r.Method, r.Header.Get("Authorization"))
				}
				if r.URL.Path == "/v3/files" {
					if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
						t.Errorf("upload content-type %q", r.Header.Get("Content-Type"))
					}
					writeJSON(w, map[string]any{"code": 0, "data": map[string]string{"file_token": "file-token-one"}})
					return
				}
				if r.URL.Path != tc.endpoint || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
					t.Errorf("unexpected generation route/content-type: %s %q", r.URL.Path, r.Header.Get("Content-Type"))
				}
				if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
					t.Error(err)
				}
				writeJSON(w, map[string]any{"code": 0, "data": map[string]string{"task_id": "task-official-fixture"}})
			}))
			defer server.Close()
			errorMapping, statusMapping := declaredTripoMappings(t)
			config := tripo.Config{BaseURL: server.URL + "/v3", APIKey: "tripo-key", HTTPClient: server.Client(), AllowHTTPAssets: true, OfferingRoutes: []generation.Offering{offering}, ErrorMapping: errorMapping, StatusMapping: statusMapping}
			if tc.capability == generation.ImageTo3D {
				config.InputAssets = memoryInputAssets{content: []byte("fixture-image-bytes")}
			}
			adapter, err := tripo.New(config)
			if err != nil {
				t.Fatal(err)
			}
			request := generation.StartRequest{AttemptID: "attempt-h", SubmissionKey: "submit-h", Offering: offering, Input: frozen}
			if tc.capability == generation.ImageTo3D {
				request.InputAssets = map[string][]generation.InputAsset{"reference_assets": {{ID: "asset-one", MediaType: "image/png"}}}
			}
			result, err := adapter.Start(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if result.Job == nil || result.Job.RemoteID != "task-official-fixture" {
				t.Fatalf("task response=%+v", result.Job)
			}
			if len(calls) != 1 && tc.capability == generation.TextTo3D || len(calls) != 2 && tc.capability == generation.ImageTo3D {
				t.Fatalf("unexpected calls=%v", calls)
			}
			for key, want := range map[string]any{"model": "v3.1-20260211", "texture_version": "v3.5-20260815", "texture_quality": "extreme", "geometry_quality": "detailed", "texture": true, "pbr": true} {
				if posted[key] != want {
					t.Errorf("body.%s=%v want %v", key, posted[key], want)
				}
			}
			for _, private := range []string{"output_formats", "authorize_extra_credits", "reference_assets"} {
				if _, found := posted[private]; found {
					t.Errorf("private %s leaked to generation POST", private)
				}
			}
			if tc.capability == generation.ImageTo3D && posted["input"] != "file-token-one" {
				t.Errorf("image token=%v", posted["input"])
			}
		})
	}
}

func TestTripoHCurrentTaskQueryOfficialEnvelopeAndFailedStatus(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	// The disabled legacy H Offering uses the same task-query wire without
	// the full Offering's private six-format pre-completion conversion planner.
	offering, ok := c.Get("tripo/tripo-v3.1/text-to-3d")
	if !ok {
		t.Fatal("current H Offering missing")
	}
	for _, tc := range []struct {
		name, response string
		phase          generation.UpstreamPhase
		artifacts      int
	}{
		{"official success example", `{"code":0,"data":{"task_id":"task_abc123","type":"text_to_model","status":"success","progress":100,"output":{"model_url":"https://cdn.tripo3d.ai/output/model_pbr.glb","rendered_image_url":"https://cdn.tripo3d.ai/output/preview.png"},"credits_consumed":100.00,"created_at":"2026-04-28T12:00:00Z","completed_at":"2026-04-28T12:01:30Z"}}`, generation.UpstreamCompleted, 2},
		{"synthetic failed task control", `{"code":0,"data":{"task_id":"task_abc123","type":"text_to_model","status":"failed","progress":100,"error_code":1004,"error_message":"synthetic invalid combination","created_at":"2026-04-28T12:00:00Z"}}`, generation.UpstreamFailed, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v3/tasks/task_abc123" || r.Header.Get("Authorization") != "Bearer tripo-key" {
					t.Errorf("task query wire: %s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()
			errorMapping, statusMapping := declaredTripoMappings(t)
			adapter, err := tripo.New(tripo.Config{BaseURL: server.URL + "/v3", APIKey: "tripo-key", HTTPClient: server.Client(), OfferingRoutes: []generation.Offering{offering}, ErrorMapping: errorMapping, StatusMapping: statusMapping})
			if err != nil {
				t.Fatal(err)
			}
			observed, err := adapter.Observe(context.Background(), generation.ObserveRequest{AttemptID: "attempt-h", Offering: offering, Job: generation.UpstreamJob{ProviderID: "tripo", RemoteID: "task_abc123"}})
			if err != nil || observed.Phase != tc.phase || len(observed.Artifacts) != tc.artifacts {
				t.Fatalf("task observation phase=%s artifacts=%d err=%v", observed.Phase, len(observed.Artifacts), err)
			}
			if tc.phase == generation.UpstreamFailed && observed.Failure == nil {
				t.Fatal("failed task lost failure evidence")
			}
			if tc.phase == generation.UpstreamCompleted {
				roles := map[string]bool{}
				for _, claim := range observed.Artifacts {
					roles[claim.Role] = true
				}
				if !roles["model"] || !roles["thumbnail"] {
					t.Fatalf("official model/preview output lost: %+v", observed.Artifacts)
				}
			}
		})
	}
}
