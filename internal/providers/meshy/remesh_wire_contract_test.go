package meshy_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

// Remesh v1 has no documented ai_model field. Exercise the actual default
// declaration through normalization and Start, not a hand-built wire offering.
func TestDefaultRemeshWireContract(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	modelBytes := []byte("remesh-model-bytes")
	for _, tc := range []struct {
		name, input, sourceField, sourceValue string
	}{
		{"task", `{"source_task":"task-local"}`, "input_task_id", "remote-delivered-model"},
		{"url", `{"source_url":"https://assets.test/model.glb"}`, "model_url", "https://assets.test/model.glb"},
		{"asset", `{"source_asset":"asset-model"}`, "model_url", "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(modelBytes)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offering, input, err := c.Normalize(generation.Remesh, "meshy/processing/remesh", json.RawMessage(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			if offering.ModelID != "meshy-processing" {
				t.Fatalf("internal model identity = %q", offering.ModelID)
			}
			posts := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts++
				if r.Method != http.MethodPost || r.URL.Path != "/openapi/v1/remesh" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				var body map[string]any
				decoder := json.NewDecoder(r.Body)
				decoder.UseNumber()
				if err := decoder.Decode(&body); err != nil {
					t.Error(err)
				}
				for _, forbidden := range []string{"ai_model", "source_task", "source_url", "source_asset"} {
					if _, exists := body[forbidden]; exists {
						t.Errorf("unexpected wire field %s: %#v", forbidden, body)
					}
				}
				if body[tc.sourceField] != tc.sourceValue || body["topology"] != "quad" {
					t.Errorf("source/topology = %#v", body)
				}
				if tc.sourceField == "model_url" {
					if _, exists := body["input_task_id"]; exists {
						t.Error("asset/URL request also contains input_task_id")
					}
				} else if _, exists := body["model_url"]; exists {
					t.Error("task request also contains model_url")
				}
				polycount, ok := body["target_polycount"].(json.Number)
				if !ok {
					t.Errorf("target_polycount is not a JSON number: %#v", body["target_polycount"])
				} else if value, err := polycount.Int64(); err != nil || value != 300000 {
					t.Errorf("target_polycount = %q, error = %v", polycount, err)
				}
				formats := []any{"glb", "fbx", "obj", "usdz", "stl", "3mf"}
				if !reflect.DeepEqual(body["target_formats"], formats) {
					t.Errorf("target_formats = %#v", body["target_formats"])
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"result": "remesh-created"})
			}))
			defer upstream.Close()
			adapter := newMeshyAdapter(t, meshy.Config{
				BaseURL: upstream.URL, APIKey: "test", HTTPClient: upstream.Client(),
				InputAssets: memoryInputAssets{content: modelBytes},
			})
			started, err := adapter.Start(context.Background(), generation.StartRequest{
				Offering: offering, Input: input,
				TaskDependencies: map[string]generation.TaskDependency{
					"source_task": {
						TaskID: "task-local", Capability: generation.TextTo3D, ProviderID: "meshy",
						UpstreamJob: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "remote-delivered-model"},
					},
				},
				InputAssets: remeshInlineAssets(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if posts != 1 || started.Job == nil || started.Job.RemoteID != "remesh-created" {
				t.Fatalf("posts = %d, started = %#v", posts, started)
			}
		})
	}
}

func TestDefaultGenerationStillSendsDocumentedModelParameter(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   generation.OfferingID
		path string
	}{
		{"meshy/meshy-7/text-to-3d", "/openapi/v2/text-to-3d"},
		{"meshy/meshy-7/image-to-3d", "/openapi/v1/image-to-3d"},
	} {
		t.Run(string(tc.id), func(t *testing.T) {
			offering, ok := c.Get(tc.id)
			if !ok {
				t.Fatal("missing offering")
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != tc.path {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["ai_model"] != "meshy-7" {
					t.Errorf("ai_model = %#v", body["ai_model"])
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"result": "generated-model"})
			}))
			defer upstream.Close()
			adapter := newMeshyAdapter(t, meshy.Config{
				BaseURL: upstream.URL, APIKey: "test", HTTPClient: upstream.Client(),
				InputAssets: memoryInputAssets{content: []byte("\x89PNG\r\n\x1a\nreference")},
			})
			if _, err := adapter.Start(context.Background(), generation.StartRequest{
				Offering: offering, Input: json.RawMessage(`{"prompt":"castle","reference_assets":["asset-input"]}`),
				InputAssets: map[string][]generation.InputAsset{
					"reference_assets": {{ID: "asset-input", MediaType: "image/png"}},
				},
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
