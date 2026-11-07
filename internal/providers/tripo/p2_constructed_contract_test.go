package tripo_test

// Constructed-from-docs P2 contract fixture, read 2026-11-04:
// https://developers.tripo3d.ai/en/docs/generation-image-to-model/p
// https://developers.tripo3d.ai/en/docs/generation-multiview-to-model/p
// https://developers.tripo3d.ai/en/docs/generation-text-to-model/p
// The Offering is enabled with the official P Series price read 2026-11-08.
// No real P2 sample exists yet, so this test sends the documented field set
// through the shared Tripo v3 adapter: model P2-20260801, the same endpoints
// and the same upload/observe wire. The hidden extra-credit consent is frozen
// into the input the way the approval card authorizes it.

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
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func p2FrozenInput(t *testing.T, offering generation.Offering, input map[string]any) json.RawMessage {
	t.Helper()
	values := map[string]any{"authorize_extra_credits": true}
	for _, parameter := range offering.Parameters {
		if len(parameter.Default) == 0 {
			continue
		}
		var value any
		if err := json.Unmarshal(parameter.Default, &value); err != nil {
			t.Fatal(err)
		}
		values[parameter.Name] = value
	}
	for key, value := range input {
		values[key] = value
	}
	schema.ApplyForcedValues(offering.Parameters, values)
	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestTripoP2ConstructedInputReachesTheSharedV3Wire(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	image, ok := c.Get(schema.OfferingID("tripo/tripo-p2/image-to-3d"))
	if !ok {
		t.Fatal("P2 image Offering missing")
	}
	multiview, ok := c.Get(schema.OfferingID("tripo/tripo-p2-multiview/image-to-3d"))
	if !ok {
		t.Fatal("P2 multiview Offering missing")
	}
	text, ok := c.Get(schema.OfferingID("tripo/tripo-p2/text-to-3d"))
	if !ok {
		t.Fatal("P2 text Offering missing")
	}

	cases := []struct {
		name       string
		offering   generation.Offering
		input      map[string]any
		assets     map[string][]generation.InputAsset
		endpoint   string
		uploads    int
		wantBody   map[string]any
		wantAbsent []string
	}{
		{
			name:     "image with a quad face budget",
			offering: image,
			input:    map[string]any{"reference_assets": []any{"asset-one"}, "quad": true, "face_limit": 150},
			assets: map[string][]generation.InputAsset{
				"reference_assets": {{ID: "asset-one", MediaType: "image/png"}},
			},
			endpoint: "/v3/generation/image-to-model",
			uploads:  1,
			wantBody: map[string]any{
				"model": "P2-20260801", "input": "file-token-one",
				"texture": true, "pbr": true, "export_uv": true, "delight": true,
				"texture_quality": "standard", "texture_version": "v3.5-20260815",
				"texture_alignment": "original_image", "orientation": "default",
				"quad": true, "face_limit": float64(150),
			},
			wantAbsent: []string{"reference_assets", "reference_views", "output_formats", "authorize_extra_credits", "export_orientation"},
		},
		{
			name:     "image without a face budget stays adaptive",
			offering: image,
			input:    map[string]any{"reference_assets": []any{"asset-one"}},
			assets: map[string][]generation.InputAsset{
				"reference_assets": {{ID: "asset-one", MediaType: "image/png"}},
			},
			endpoint:   "/v3/generation/image-to-model",
			uploads:    1,
			wantBody:   map[string]any{"model": "P2-20260801", "input": "file-token-one"},
			wantAbsent: []string{"face_limit"},
		},
		{
			name:     "multiview keeps the view-keyed inputs wire",
			offering: multiview,
			input: map[string]any{
				"reference_assets": []any{"asset-one", "asset-two"},
				"reference_views":  []any{"front", "left"},
			},
			assets: map[string][]generation.InputAsset{
				"reference_assets": {
					{ID: "asset-one", MediaType: "image/png"},
					{ID: "asset-two", MediaType: "image/png"},
				},
			},
			endpoint: "/v3/generation/multiview-to-model",
			uploads:  2,
			wantBody: map[string]any{
				"model": "P2-20260801",
				"inputs": []any{
					map[string]any{"front": "file-token-one"},
					map[string]any{"left": "file-token-two"},
				},
			},
			wantAbsent: []string{"input", "reference_assets", "reference_views"},
		},
		{
			name:     "text-to-3D shares the prompt wire",
			offering: text,
			input:    map[string]any{"prompt": "a game-ready wooden crate", "quad": true},
			endpoint: "/v3/generation/text-to-model",
			wantBody: map[string]any{
				"model": "P2-20260801", "prompt": "a game-ready wooden crate",
				"quad": true, "texture": true, "pbr": true, "delight": true,
			},
			wantAbsent: []string{"input", "reference_assets", "output_formats", "authorize_extra_credits"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var posted map[string]any
			calls := []string{}
			uploaded := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.URL.Path)
				if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer tripo-key" {
					t.Errorf("unexpected method/auth: %s %q", r.Method, r.Header.Get("Authorization"))
				}
				if r.URL.Path == "/v3/files" {
					if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
						t.Errorf("upload content-type %q", r.Header.Get("Content-Type"))
					}
					uploaded++
					token := "file-token-one"
					if uploaded == 2 {
						token = "file-token-two"
					}
					writeJSON(w, map[string]any{"code": 0, "data": map[string]string{"file_token": token}})
					return
				}
				if r.URL.Path != tc.endpoint || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
					t.Errorf("unexpected generation route/content-type: %s %q", r.URL.Path, r.Header.Get("Content-Type"))
				}
				if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
					t.Error(err)
				}
				writeJSON(w, map[string]any{"code": 0, "data": map[string]string{"task_id": "task-p2-fixture"}})
			}))
			defer server.Close()

			errorMapping, statusMapping := declaredTripoMappings(t)
			config := tripo.Config{
				BaseURL: server.URL + "/v3", APIKey: "tripo-key", HTTPClient: server.Client(),
				AllowHTTPAssets: true, OfferingRoutes: []generation.Offering{tc.offering},
				ErrorMapping: errorMapping, StatusMapping: statusMapping,
			}
			if len(tc.assets) > 0 {
				config.InputAssets = memoryInputAssets{content: []byte("fixture-image-bytes")}
			}
			adapter, err := tripo.New(config)
			if err != nil {
				t.Fatal(err)
			}
			request := generation.StartRequest{
				AttemptID: "attempt-p2", SubmissionKey: "submit-p2", Offering: tc.offering,
				Input: p2FrozenInput(t, tc.offering, tc.input), InputAssets: tc.assets,
			}
			result, err := adapter.Start(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if result.Job == nil || result.Job.RemoteID != "task-p2-fixture" {
				t.Fatalf("task response=%+v", result.Job)
			}
			if len(calls) != tc.uploads+1 {
				t.Fatalf("calls=%v, want %d uploads then generation", calls, tc.uploads)
			}
			for key, want := range tc.wantBody {
				if !jsonEqual(posted[key], want) {
					t.Errorf("body.%s=%v want %v", key, posted[key], want)
				}
			}
			for _, key := range tc.wantAbsent {
				if value, found := posted[key]; found {
					t.Errorf("private/non-wire field %s leaked: %v", key, value)
				}
			}
		})
	}
}

func jsonEqual(got, want any) bool {
	gotJSON, err := json.Marshal(got)
	if err != nil {
		return false
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		return false
	}
	return string(gotJSON) == string(wantJSON)
}

func TestTripoP2ObserveKeepsTheRequiredModelAndThumbnail(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := c.Get(schema.OfferingID("tripo/tripo-p2/image-to-3d"))
	if !ok {
		t.Fatal("P2 image Offering missing")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v3/tasks/task-p2" {
			t.Errorf("task query wire: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"task_id":"task-p2","type":"image_to_model","status":"success","progress":100,"output":{"model_url":"https://cdn.tripo3d.ai/output/p2.glb","rendered_image_url":"https://cdn.tripo3d.ai/output/p2.png"},"credits_consumed":0,"created_at":"2026-11-04T12:00:00Z","completed_at":"2026-11-04T12:01:00Z"}}`))
	}))
	defer server.Close()
	errorMapping, statusMapping := declaredTripoMappings(t)
	adapter, err := tripo.New(tripo.Config{
		BaseURL: server.URL + "/v3", APIKey: "tripo-key", HTTPClient: server.Client(),
		OfferingRoutes: []generation.Offering{offering}, ErrorMapping: errorMapping, StatusMapping: statusMapping,
	})
	if err != nil {
		t.Fatal(err)
	}
	observed, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		AttemptID: "attempt-p2", Offering: offering,
		Job: generation.UpstreamJob{ProviderID: "tripo", RemoteID: "task-p2"},
	})
	if err != nil || observed.Phase != generation.UpstreamCompleted {
		t.Fatalf("phase=%s err=%v", observed.Phase, err)
	}
	roles := map[string]bool{}
	for _, claim := range observed.Artifacts {
		roles[claim.Role] = true
	}
	if !roles["model"] || !roles["thumbnail"] {
		t.Fatalf("P2 model/preview output lost: %+v", observed.Artifacts)
	}
}
