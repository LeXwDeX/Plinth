package meshy_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

// #330, checked against https://docs.meshy.ai/en/api/convert on 2026-10-22:
// POST /openapi/v1/convert takes input_task_id (a SUCCEEDED task) or
// model_url (a data URI up to 100 MB) and a required target_formats array;
// GET /openapi/v1/convert/{id} returns a task object whose only result field
// is model_urls, one URL per requested format (no thumbnail, no textures).
// The request goes out from the default Catalog declaration, as Remesh's does.
func TestDefaultConvertWireContract(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	modelBytes := []byte("convert-model-bytes")
	formats := []any{"glb", "fbx", "obj", "usdz", "stl", "3mf"}
	for _, tc := range []struct {
		name, input, sourceField, sourceValue string
	}{
		{"task", `{"source_task":"task-local"}`, "input_task_id", "remote-delivered-model"},
		{"asset", `{"source_asset":"asset-model"}`, "model_url", "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(modelBytes)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offering, input, err := c.Normalize(generation.FormatConversion, "meshy/processing/convert", json.RawMessage(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			posts, gets := 0, 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/openapi/v1/convert":
					posts++
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if !reflect.DeepEqual(body["target_formats"], formats) || body[tc.sourceField] != tc.sourceValue || len(body) != 2 {
						t.Errorf("convert body = %#v; want only %s and the six target_formats", body, tc.sourceField)
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"result": "convert-created"})
				case r.Method == http.MethodGet && r.URL.Path == "/openapi/v1/convert/convert-created":
					gets++
					// The documented task object, as Retrieve returns it.
					_ = json.NewEncoder(w).Encode(map[string]any{
						"id": "convert-created", "type": "convert", "status": "SUCCEEDED", "progress": 100,
						"model_urls": map[string]string{
							"glb": "https://assets.meshy.ai/c/model.glb?Expires=1", "fbx": "https://assets.meshy.ai/c/model.fbx?Expires=1",
							"obj": "https://assets.meshy.ai/c/model.obj?Expires=1", "usdz": "https://assets.meshy.ai/c/model.usdz?Expires=1",
							"stl": "https://assets.meshy.ai/c/model.stl?Expires=1", "3mf": "https://assets.meshy.ai/c/model.3mf?Expires=1",
						},
						"created_at": 1699999999000, "started_at": 1700000000000, "finished_at": 1700000001000,
						"task_error": nil, "consumed_credits": 1,
					})
				default:
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
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
			if posts != 1 || started.Job == nil || started.Job.RemoteID != "convert-created" {
				t.Fatalf("posts = %d, started = %#v", posts, started)
			}
			completed, err := adapter.Observe(context.Background(), generation.ObserveRequest{
				AttemptID: "attempt-convert", Offering: offering, Job: *started.Job,
			})
			if err != nil {
				t.Fatal(err)
			}
			var variants []string
			for _, artifact := range completed.Artifacts {
				if artifact.Role != "model" || !strings.HasPrefix(artifact.Locator, "https://assets.meshy.ai/") {
					t.Fatalf("artifact = %#v", artifact)
				}
				variants = append(variants, artifact.Variant)
			}
			sort.Strings(variants)
			if gets != 1 || completed.Phase != generation.UpstreamCompleted ||
				strings.Join(variants, ",") != "3mf,fbx,glb,obj,stl,usdz" {
				t.Fatalf("observed %d times: phase %s variants %v", gets, completed.Phase, variants)
			}
		})
	}
}

// The Offering validates as a Meshy wire Offering, including its 70 MiB
// asset bound under the inline data URI limit.
func TestDefaultConvertOfferingValidates(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := c.Get("meshy/processing/convert")
	if !ok {
		t.Fatal("meshy/processing/convert is missing")
	}
	if err := meshy.ValidateOffering(offering); err != nil {
		t.Fatal(err)
	}
	if !meshy.OfferingSupportsOfflineAssets(offering) {
		t.Fatal("Convert must run without a public asset gateway (data URI input)")
	}
}
