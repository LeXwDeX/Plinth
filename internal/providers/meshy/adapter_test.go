package meshy_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

func TestAsyncProtocolCreateObserveAndOpenAsset(t *testing.T) {
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if strings.HasPrefix(request.URL.Path, "/openapi/") &&
			request.Header.Get("Authorization") != "Bearer meshy-key" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		switch request.URL.Path {
		case "/openapi/v2/text-to-3d":
			if request.Method != http.MethodPost {
				t.Errorf("method = %s", request.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["ai_model"] != "meshy-test" || body["prompt"] != "castle" {
				t.Errorf("body = %#v", body)
			}
			_ = json.NewEncoder(writer).Encode(map[string]string{"result": "remote-42"})
		case "/openapi/v2/text-to-3d/remote-42":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"id":       "remote-42",
				"status":   "SUCCEEDED",
				"progress": 100,
				"model_urls": map[string]string{
					"glb": upstream.URL + "/asset.glb",
				},
			})
		case "/asset.glb":
			writer.Header().Set("Content-Type", "model/gltf-binary")
			_, _ = writer.Write([]byte("glTF asset"))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstream.Close()
	parsed, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:         upstream.URL,
		APIKey:          "meshy-key",
		HTTPClient:      upstream.Client(),
		AllowHTTPAssets: true,
	})
	offering := generation.Offering{
		ProviderID:   "meshy",
		ModelID:      "meshy-test",
		Capability:   generation.TextTo3D,
		PollInterval: 3 * time.Second,
		AssetHosts:   []string{parsed.Hostname()},
		ProviderOptions: json.RawMessage(
			`{"endpoint":"text-to-3d","api_version":"v2",` +
				`"model_parameter":"ai_model","result_assets":[` +
				`{"path":"model_urls","role":"model","mode":"map"}]}`,
		),
	}
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		TaskID:        "task-local",
		AttemptID:     "attempt-local",
		SubmissionKey: "submission-local",
		Offering:      offering,
		Input:         []byte(`{"prompt":"castle"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Job == nil ||
		started.Job.RemoteID != "remote-42" ||
		generation.TaskID(started.Job.RemoteID) == "task-local" {
		t.Fatalf("started = %#v", started)
	}
	completed, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		TaskID:    "task-local",
		AttemptID: "attempt-local",
		Offering:  offering,
		Job:       *started.Job,
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Phase != generation.UpstreamCompleted ||
		len(completed.Artifacts) != 1 ||
		completed.Artifacts[0].Role != "model" {
		t.Fatalf("completed = %#v", completed)
	}
	body, metadata, err := adapter.OpenAsset(
		context.Background(),
		generation.OpenAssetRequest{
			Offering: offering,
			Claim:    completed.Artifacts[0],
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	content, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "glTF asset" || metadata.MediaType != "model/gltf-binary" {
		t.Fatalf("content = %q, metadata = %#v", content, metadata)
	}
}

func TestObservedArtifactProjectionComesFromOfferingSchema(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/openapi/v1/remesh/remesh-9" {
			t.Errorf("path = %s", request.URL.Path)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"status":   "SUCCEEDED",
			"progress": 100,
			"model_urls": map[string]string{
				"glb": upstreamAssetURL(request, "/remeshed.glb"),
			},
		})
	}))
	defer upstream.Close()
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:    upstream.URL,
		APIKey:     "meshy-key",
		HTTPClient: upstream.Client(),
	})
	observed, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		AttemptID: "attempt-remesh",
		Offering: generation.Offering{
			ProviderID: "meshy",
			Capability: generation.Remesh,
			ProviderOptions: json.RawMessage(
				`{"endpoint":"remesh","api_version":"v1","result_assets":[` +
					`{"path":"model_urls","role":"schema-remeshed-model","mode":"map"}]}`,
			),
		},
		Job: generation.UpstreamJob{
			ProviderID: "meshy",
			RemoteID:   "remesh-9",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(observed.Artifacts) != 1 ||
		observed.Artifacts[0].Role != "schema-remeshed-model" ||
		observed.Artifacts[0].Variant != "glb" {
		t.Fatalf("observed = %#v", observed)
	}
}

func TestWebhookDecodeUsesSameOfferingDrivenObservationProjection(
	t *testing.T,
) {
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL: "https://api.meshy.invalid",
		APIKey:  "meshy-key",
	})
	observed, err := adapter.DecodeWebhook(
		context.Background(),
		generation.WebhookDecodeRequest{
			TaskID:    "task-webhook",
			AttemptID: "attempt-webhook",
			Offering: generation.Offering{
				ProviderID: "meshy",
				Capability: generation.Remesh,
				ProviderOptions: json.RawMessage(
					`{"endpoint":"remesh","api_version":"v1","result_assets":[` +
						`{"path":"model_urls","role":"schema-webhook-model","mode":"map"}]}`,
				),
			},
			Job: generation.UpstreamJob{
				ProviderID: "meshy",
				RemoteID:   "remote-webhook-9",
			},
			Payload: []byte(`{
				"id":"remote-webhook-9",
				"status":"SUCCEEDED",
				"progress":100,
				"model_urls":{"glb":"https://assets.meshy.invalid/webhook.glb"}
			}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if observed.Phase != generation.UpstreamCompleted ||
		observed.Job == nil ||
		observed.Job.RemoteID != "remote-webhook-9" ||
		len(observed.Artifacts) != 1 ||
		observed.Artifacts[0].Role != "schema-webhook-model" ||
		observed.Artifacts[0].Variant != "glb" {
		t.Fatalf("observed = %#v", observed)
	}
}

func TestValidateOfferingRejectsInvalidResultProjectionAtStartup(t *testing.T) {
	base := generation.Offering{
		ID:         "meshy/processing/remesh",
		AdapterKey: "meshy.v2",
		ProviderID: "meshy",
		Capability: generation.Remesh,
		ProviderOptions: json.RawMessage(
			`{"endpoint":"remesh","api_version":"v1","result_assets":[` +
				`{"path":"model_urls","role":"model","mode":"map"}]}`,
		),
	}
	if err := meshy.ValidateOffering(base); err != nil {
		t.Fatalf("valid wire schema: %v", err)
	}
	for name, providerOptions := range map[string]json.RawMessage{
		"missing projections": json.RawMessage(
			`{"endpoint":"remesh","api_version":"v1"}`,
		),
		"unsupported mode": json.RawMessage(
			`{"endpoint":"remesh","api_version":"v1","result_assets":[` +
				`{"path":"model_urls","role":"model","mode":"hardcoded"}]}`,
		),
		"duplicate path": json.RawMessage(
			`{"endpoint":"remesh","api_version":"v1","result_assets":[` +
				`{"path":"model_urls","role":"model","mode":"map"},` +
				`{"path":"model_urls","role":"other","mode":"map"}]}`,
		),
	} {
		t.Run(name, func(t *testing.T) {
			offering := base
			offering.ProviderOptions = providerOptions
			if err := meshy.ValidateOffering(offering); err == nil {
				t.Fatalf("invalid wire schema accepted: %s", providerOptions)
			}
		})
	}
}

func upstreamAssetURL(request *http.Request, path string) string {
	return "http://" + request.Host + path
}

func TestDeleteProviderTaskUsesOfferingWireSchemaAndRemoteIdentity(t *testing.T) {
	var method string
	var path string
	var authorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		method = request.Method
		path = request.URL.Path
		authorization = request.Header.Get("Authorization")
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:    upstream.URL,
		APIKey:     "meshy-key",
		HTTPClient: upstream.Client(),
	})
	err := adapter.DeleteProviderTask(
		context.Background(),
		generation.DeleteProviderRequest{
			TaskID:    "local-task",
			AttemptID: "local-attempt",
			Offering: generation.Offering{
				ProviderID:      "meshy",
				Capability:      generation.TextTo3D,
				ProviderOptions: json.RawMessage(`{"endpoint":"text-to-3d","api_version":"v2"}`),
			},
			Job: generation.UpstreamJob{
				ProviderID: "meshy",
				RemoteID:   "remote-42",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodDelete ||
		path != "/openapi/v2/text-to-3d/remote-42" ||
		authorization != "Bearer meshy-key" {
		t.Fatalf("method = %q, path = %q, auth = %q", method, path, authorization)
	}
}

func TestWireRouteComesFromOfferingSchema(t *testing.T) {
	var observedPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		observedPath = request.URL.Path
		_ = json.NewEncoder(writer).Encode(map[string]string{
			"result": "schema-routed-job",
		})
	}))
	defer upstream.Close()
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:    upstream.URL,
		APIKey:     "meshy-key",
		HTTPClient: upstream.Client(),
	})

	_, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering: generation.Offering{
			ProviderID:      "meshy",
			ModelID:         "schema-model",
			Capability:      generation.TextToImage,
			ProviderOptions: json.RawMessage(`{"endpoint":"schema-endpoint","api_version":"v9","request_template":{"ai_model":"$model"}}`),
		},
		Input: []byte(`{"prompt":"schema routed"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if observedPath != "/openapi/v9/schema-endpoint" {
		t.Fatalf("upstream path = %q", observedPath)
	}
}

func TestTextTo3DRefineMapsPlatformPreviewTaskToUpstreamJob(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/openapi/v2/text-to-3d" {
			t.Errorf("path = %s", request.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["mode"] != "refine" ||
			body["preview_task_id"] != "preview-remote-42" {
			t.Errorf("body = %#v", body)
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{
			"result": "refine-remote-43",
		})
	}))
	defer upstream.Close()
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:    upstream.URL,
		APIKey:     "meshy-key",
		HTTPClient: upstream.Client(),
	})

	started, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering: generation.Offering{
			ProviderID: "meshy",
			ModelID:    "meshy-6",
			Capability: generation.TextTo3D,
			Parameters: []generation.Parameter{{
				Name: "preview_task_id",
				Type: "task",
			}},
			ProviderOptions: json.RawMessage(`{"endpoint":"text-to-3d","api_version":"v2"}`),
		},
		Input: []byte(`{
			"mode":"refine",
			"preview_task_id":"task-preview-local",
			"enable_pbr":true
		}`),
		TaskDependencies: map[string]generation.TaskDependency{
			"preview_task_id": {
				TaskID:     "task-preview-local",
				Capability: generation.TextTo3D,
				ProviderID: "meshy",
				UpstreamJob: generation.UpstreamJob{
					ProviderID: "meshy",
					RemoteID:   "preview-remote-42",
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Job == nil || started.Job.RemoteID != "refine-remote-43" {
		t.Fatalf("started = %#v", started)
	}
}

func TestInputAssetCapabilityFailsBeforeUpstreamCall(t *testing.T) {
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(
		http.ResponseWriter,
		*http.Request,
	) {
		called = true
	}))
	defer upstream.Close()
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:    upstream.URL,
		APIKey:     "meshy-key",
		HTTPClient: upstream.Client(),
	})
	_, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering: generation.Offering{
			Capability:      generation.ImageTo3D,
			ProviderOptions: json.RawMessage(`{"endpoint":"image-to-3d","api_version":"v1"}`),
		},
		Input: []byte(`{"reference_assets":["asset-1"]}`),
	})
	if err == nil || called {
		t.Fatalf("error = %#v, called = %t", err, called)
	}
}

func TestImageToImageConvertsResolvedAssetsToDataURI(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/openapi/v1/image-to-image" {
			t.Errorf("path = %s", request.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		references, _ := body["reference_image_urls"].([]any)
		if len(references) != 1 ||
			!strings.HasPrefix(references[0].(string), "data:image/png;base64,") ||
			body["ai_model"] != "nano-test" {
			t.Errorf("body = %#v", body)
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{"result": "remote-image"})
	}))
	defer upstream.Close()
	inputs := memoryInputAssets{content: []byte("\x89PNG\r\n\x1a\nreference")}
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:     upstream.URL,
		APIKey:      "meshy-key",
		HTTPClient:  upstream.Client(),
		InputAssets: inputs,
	})
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering: generation.Offering{
			ProviderID: "meshy",
			ModelID:    "nano-test",
			Capability: generation.ImageToImage,
			Parameters: []generation.Parameter{{Name: "prompt", Type: "text"}, {Name: "reference_assets", Type: "asset-array", WireName: "reference_image_urls", MaxBytes: 20 << 20}},
			ProviderOptions: json.RawMessage(
				`{"endpoint":"image-to-image","api_version":"v1",` +
					`"model_parameter":"ai_model","request_template":{"ai_model":"$model"}}`,
			),
		},
		Input: []byte(`{"prompt":"restyle","reference_assets":["asset-input"]}`),
		InputAssets: map[string][]generation.InputAsset{
			"reference_assets": {{
				ID:        "asset-input",
				MediaType: "image/png",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Job == nil || started.Job.RemoteID != "remote-image" {
		t.Fatalf("started = %#v", started)
	}
}

func TestAnimationMapsPlatformFieldAndResultAssets(t *testing.T) {
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/openapi/v1/animations":
			var body map[string]any
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body["rig_task_id"] != "rig-remote-7" ||
				body["action_id"] != float64(3) {
				t.Errorf("body = %#v", body)
			}
			if _, leaked := body["source_task"]; leaked {
				t.Errorf("platform field leaked: %#v", body)
			}
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"result": "animation-8",
			})
		case "/openapi/v1/animations/animation-8":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"status":   "SUCCEEDED",
				"progress": 100,
				"result": map[string]any{
					"animation_glb_url": upstream.URL + "/animation.glb",
					"basic_animations": map[string]string{
						"idle": upstream.URL + "/idle.fbx",
					},
				},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstream.Close()
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:    upstream.URL,
		APIKey:     "meshy-key",
		HTTPClient: upstream.Client(),
	})
	offering := generation.Offering{
		ProviderID: "meshy",
		Capability: generation.Animation,
		Parameters: []generation.Parameter{{
			Name:     "source_task",
			Type:     "task",
			WireName: "rig_task_id",
		}},
		ProviderOptions: json.RawMessage(
			`{"endpoint":"animations","api_version":"v1","result_assets":[` +
				`{"path":"result","role":"model","mode":"tree"}]}`,
		),
	}
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering: offering,
		Input:    []byte(`{"source_task":"rig-remote-7","action_id":3}`),
		TaskDependencies: map[string]generation.TaskDependency{
			"source_task": {
				TaskID:     "task-rigging",
				Capability: generation.Rigging,
				ProviderID: "meshy",
				UpstreamJob: generation.UpstreamJob{
					ProviderID: "meshy",
					RemoteID:   "rig-remote-7",
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		AttemptID: "attempt-animation",
		Offering:  offering,
		Job:       *started.Job,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.Artifacts) != 2 ||
		completed.Artifacts[0].MediaType != "model/gltf-binary" ||
		completed.Artifacts[0].Variant != "animation_glb_url" ||
		completed.Artifacts[1].Variant != "basic_animations.idle" {
		t.Fatalf("completed = %#v", completed)
	}
}

func TestInputProjectionUsesOfferingWireSchemaInsteadOfCapabilityBranches(t *testing.T) {
	for name, testCase := range map[string]struct {
		input        json.RawMessage
		dependencies map[string]generation.TaskDependency
		wireField    string
		wireValue    string
	}{
		"task dependency": {
			input: json.RawMessage(`{"source_task":"task-model-local"}`),
			dependencies: map[string]generation.TaskDependency{
				"source_task": {
					TaskID:     "task-model-local",
					Capability: generation.Remesh,
					ProviderID: "meshy",
					UpstreamJob: generation.UpstreamJob{
						ProviderID: "meshy",
						RemoteID:   "model-remote-42",
					},
				},
			},
			wireField: "schema_input_task",
			wireValue: "model-remote-42",
		},
		"direct url": {
			input:     json.RawMessage(`{"source_url":"https://assets.test/model.glb"}`),
			wireField: "schema_model_url",
			wireValue: "https://assets.test/model.glb",
		},
	} {
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(
				writer http.ResponseWriter,
				request *http.Request,
			) {
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body[testCase.wireField] != testCase.wireValue {
					t.Errorf("body = %#v", body)
				}
				if _, leaked := body["source_task"]; leaked {
					t.Errorf("task source leaked: %#v", body)
				}
				if _, leaked := body["source_url"]; leaked {
					t.Errorf("URL source leaked: %#v", body)
				}
				_ = json.NewEncoder(writer).Encode(map[string]string{
					"result": "rigging-remote-43",
				})
			}))
			defer upstream.Close()
			adapter := newMeshyAdapter(t, meshy.Config{
				BaseURL:    upstream.URL,
				APIKey:     "meshy-key",
				HTTPClient: upstream.Client(),
			})
			started, err := adapter.Start(context.Background(), generation.StartRequest{
				Offering: generation.Offering{
					ProviderID: "meshy",
					Capability: generation.Rigging,
					Parameters: []generation.Parameter{
						{
							Name:     "source_task",
							Type:     "task",
							WireName: "schema_input_task",
						},
						{
							Name:     "source_url",
							Type:     "text",
							WireName: "schema_model_url",
						},
					},
					ProviderOptions: json.RawMessage(
						`{"endpoint":"rigging","api_version":"v1"}`,
					),
				},
				Input:            testCase.input,
				TaskDependencies: testCase.dependencies,
			})
			if err != nil {
				t.Fatal(err)
			}
			if started.Job == nil ||
				started.Job.RemoteID != "rigging-remote-43" {
				t.Fatalf("started = %#v", started)
			}
		})
	}
}

func TestCrossProviderTaskDependencyProjectsRemoteIDWithoutAdapterGuard(
	t *testing.T,
) {
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["schema_input_task"] != "tripo-remote-9" {
			t.Errorf("body = %#v", body)
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{
			"result": "rigging-remote-44",
		})
	}))
	defer upstream.Close()
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:    upstream.URL,
		APIKey:     "meshy-key",
		HTTPClient: upstream.Client(),
	})
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering: generation.Offering{
			ProviderID: "meshy",
			Capability: generation.Rigging,
			Parameters: []generation.Parameter{{
				Name:     "source_task",
				Type:     "task",
				WireName: "schema_input_task",
			}},
			ProviderOptions: json.RawMessage(
				`{"endpoint":"rigging","api_version":"v1"}`,
			),
		},
		Input: []byte(`{"source_task":"task-tripo-local"}`),
		TaskDependencies: map[string]generation.TaskDependency{
			"source_task": {
				TaskID:     "task-tripo-local",
				Capability: generation.TextTo3D,
				ProviderID: "tripo",
				UpstreamJob: generation.UpstreamJob{
					ProviderID: "tripo",
					RemoteID:   "tripo-remote-9",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("cross-provider dependency rejected by adapter guard: %v", err)
	}
	if started.Job == nil || started.Job.RemoteID != "rigging-remote-44" {
		t.Fatalf("started = %#v", started)
	}
}

func TestModelParameterNameComesFromOfferingWireSchema(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["schema_model_field"] != "schema-model" {
			t.Errorf("body = %#v", body)
		}
		if _, hardcoded := body["ai_model"]; hardcoded {
			t.Errorf("hardcoded model field leaked: %#v", body)
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{
			"result": "schema-model-task",
		})
	}))
	defer upstream.Close()
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:    upstream.URL,
		APIKey:     "meshy-key",
		HTTPClient: upstream.Client(),
	})
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering: generation.Offering{
			ProviderID: "meshy",
			ModelID:    "schema-model",
			Capability: generation.TextToImage,
			ProviderOptions: json.RawMessage(
				`{"endpoint":"text-to-image","api_version":"v1",` +
					`"model_parameter":"schema_model_field","request_template":{"schema_model_field":"$model"}}`,
			),
		},
		Input: json.RawMessage(`{"prompt":"schema model"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Job == nil ||
		started.Job.RemoteID != "schema-model-task" {
		t.Fatalf("started = %#v", started)
	}
}

func TestRemeshPublishesPlatformAssetAsCapabilityURL(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/openapi/v1/remesh" {
			t.Errorf("path = %s", request.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		formats, _ := body["target_formats"].([]any)
		if body["model_url"] != "https://art-ai.test/provider-asset-token" ||
			len(formats) != 1 ||
			formats[0] != "glb" {
			t.Errorf("body = %#v", body)
		}
		if _, leaked := body["source_asset"]; leaked {
			t.Errorf("platform Asset ID leaked: %#v", body)
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{
			"result": "remesh-9",
		})
	}))
	defer upstream.Close()
	inputs := memoryInputAssets{}
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:        upstream.URL,
		APIKey:         "meshy-key",
		HTTPClient:     upstream.Client(),
		InputAssets:    inputs,
		InputPublisher: inputs,
	})
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering: generation.Offering{
			ProviderID: "meshy",
			Capability: generation.Remesh,
			Timeout:    time.Hour,
			Parameters: []generation.Parameter{{
				Name:     "source_asset",
				Type:     "asset",
				WireName: "model_url",
			}},
			ProviderOptions: json.RawMessage(`{"endpoint":"remesh","api_version":"v1"}`),
		},
		Input: []byte(
			`{"source_asset":"asset-model","target_formats":["glb"],` +
				`"topology":"quad","target_polycount":10000}`,
		),
		InputAssets: map[string][]generation.InputAsset{
			"source_asset": {{
				ID:        "asset-model",
				OwnerID:   "owner-a",
				MediaType: "model/gltf-binary",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Job == nil || started.Job.RemoteID != "remesh-9" {
		t.Fatalf("started = %#v", started)
	}
}

func TestSuccessfulSubmissionWithoutRemoteIDIsAmbiguous(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		_, _ = writer.Write([]byte(`{}`))
	}))
	defer upstream.Close()
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:    upstream.URL,
		APIKey:     "meshy-key",
		HTTPClient: upstream.Client(),
	})
	_, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering: generation.Offering{
			Capability:      generation.TextTo3D,
			ProviderOptions: json.RawMessage(`{"endpoint":"text-to-3d","api_version":"v2"}`),
		},
		Input: []byte(`{"prompt":"castle"}`),
	})
	var providerErr *generation.ProviderError
	if !errors.As(err, &providerErr) ||
		providerErr.Failure.Class != generation.FailureSubmissionUnknown ||
		!providerErr.Failure.Ambiguous {
		t.Fatalf("error = %#v", err)
	}
}

type memoryInputAssets struct {
	content []byte
}

func (memoryInputAssets) Resolve(
	context.Context,
	generation.OwnerID,
	generation.Offering,
	json.RawMessage,
) (map[string][]generation.InputAsset, error) {
	return nil, nil
}

func (m memoryInputAssets) Open(
	context.Context,
	generation.InputAsset,
) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(m.content)), nil
}

func (memoryInputAssets) ExternalURL(
	context.Context,
	generation.InputAsset,
	time.Duration,
) (string, error) {
	return "https://art-ai.test/provider-asset-token", nil
}

// failingPublisher 模拟资产网关已接线但未配置（ExternalURL 运行时失败）。
type failingPublisher struct {
	memoryInputAssets
}

func (failingPublisher) ExternalURL(
	context.Context,
	generation.InputAsset,
	time.Duration,
) (string, error) {
	return "", errors.New("Provider 资产网关未配置")
}

func remeshInlineOffering(maxBytes int64) generation.Offering {
	return generation.Offering{
		ProviderID: "meshy",
		Capability: generation.Remesh,
		Timeout:    time.Hour,
		Parameters: []generation.Parameter{{
			Name:     "source_asset",
			Type:     "asset",
			WireName: "model_url",
		}},
		ProviderOptions: json.RawMessage(
			`{"endpoint":"remesh","api_version":"v1",` +
				`"inline_model_input":{"max_bytes":` +
				strconv.FormatInt(maxBytes, 10) + `}}`,
		),
	}
}

func remeshInlineInput() []byte {
	return []byte(
		`{"source_asset":"asset-model","target_formats":["glb"],` +
			`"topology":"quad","target_polycount":10000}`,
	)
}

func remeshInlineAssets() map[string][]generation.InputAsset {
	return map[string][]generation.InputAsset{
		"source_asset": {{
			ID:        "asset-model",
			OwnerID:   "owner-a",
			MediaType: "model/gltf-binary",
		}},
	}
}

func TestRemeshInlinesAssetAsDataURIWithoutGateway(t *testing.T) {
	modelContent := []byte("glb-binary-bytes")
	expectedURI := "data:application/octet-stream;base64," +
		base64.StdEncoding.EncodeToString(modelContent)
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		if body["model_url"] != expectedURI {
			t.Errorf("model_url = %#v", body["model_url"])
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{
			"result": "remesh-inline-1",
		})
	}))
	defer upstream.Close()
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:     upstream.URL,
		APIKey:      "meshy-key",
		HTTPClient:  upstream.Client(),
		InputAssets: memoryInputAssets{content: modelContent},
	})
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering:    remeshInlineOffering(1 << 20),
		Input:       remeshInlineInput(),
		InputAssets: remeshInlineAssets(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Job == nil || started.Job.RemoteID != "remesh-inline-1" {
		t.Fatalf("started = %#v", started)
	}
}

func TestRemeshInlineFallsBackWhenGatewayPublishFails(t *testing.T) {
	modelContent := []byte("fallback-bytes")
	expectedURI := "data:application/octet-stream;base64," +
		base64.StdEncoding.EncodeToString(modelContent)
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		if body["model_url"] != expectedURI {
			t.Errorf("model_url = %#v", body["model_url"])
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{
			"result": "remesh-inline-2",
		})
	}))
	defer upstream.Close()
	publisher := failingPublisher{memoryInputAssets{content: modelContent}}
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:        upstream.URL,
		APIKey:         "meshy-key",
		HTTPClient:     upstream.Client(),
		InputAssets:    publisher,
		InputPublisher: publisher,
	})
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering:    remeshInlineOffering(1 << 20),
		Input:       remeshInlineInput(),
		InputAssets: remeshInlineAssets(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Job == nil || started.Job.RemoteID != "remesh-inline-2" {
		t.Fatalf("started = %#v", started)
	}
}

func TestRemeshDeclaredInlineIgnoresGateway(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		// 声明 inline_model_input 即内联，即使资产网关可用也不发布公网 URL。
		modelURL, _ := body["model_url"].(string)
		if !strings.HasPrefix(modelURL, "data:application/octet-stream;base64,") {
			t.Errorf("model_url = %#v", body["model_url"])
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{
			"result": "remesh-inline-3",
		})
	}))
	defer upstream.Close()
	inputs := memoryInputAssets{content: []byte("unused")}
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:        upstream.URL,
		APIKey:         "meshy-key",
		HTTPClient:     upstream.Client(),
		InputAssets:    inputs,
		InputPublisher: inputs,
	})
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering:    remeshInlineOffering(1 << 20),
		Input:       remeshInlineInput(),
		InputAssets: remeshInlineAssets(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Job == nil || started.Job.RemoteID != "remesh-inline-3" {
		t.Fatalf("started = %#v", started)
	}
}

func TestRemeshInlineRejectsOversizeAsset(t *testing.T) {
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:     "https://meshy.test",
		APIKey:      "meshy-key",
		InputAssets: memoryInputAssets{content: []byte("0123456789")},
	})
	_, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering:    remeshInlineOffering(4),
		Input:       remeshInlineInput(),
		InputAssets: remeshInlineAssets(),
	})
	var providerErr *generation.ProviderError
	if !errors.As(err, &providerErr) ||
		providerErr.Failure.Class != generation.FailureInvalidInput {
		t.Fatalf("error = %#v", err)
	}
	if !strings.Contains(providerErr.Failure.SafeDetail, "内联上限") {
		t.Fatalf("detail = %q", providerErr.Failure.SafeDetail)
	}
}

func TestValidateOfferingRejectsInvalidInlineDeclaration(t *testing.T) {
	offering := remeshInlineOffering(0)
	offering.ProviderOptions = json.RawMessage(
		`{"endpoint":"remesh","api_version":"v1",` +
			`"result_assets":[{"path":"model_urls","role":"model","mode":"map"}],` +
			`"inline_model_input":{"max_bytes":0}}`,
	)
	if err := meshy.ValidateOffering(offering); err == nil {
		t.Fatal("expected invalid inline_model_input to be rejected")
	}
}

func TestValidateOfferingRequiresAssetLimitWithinInlineLimit(t *testing.T) {
	offering := remeshInlineOffering(60 << 20)
	offering.ProviderOptions = json.RawMessage(
		`{"endpoint":"remesh","api_version":"v1",` +
			`"result_assets":[{"path":"model_urls","role":"model","mode":"map"}],` +
			`"inline_model_input":{"max_bytes":62914560}}`,
	)
	for _, limit := range []int64{0, 100 << 20} {
		offering.Parameters[0].MaxBytes = limit
		if err := meshy.ValidateOffering(offering); err == nil {
			t.Fatalf("max_bytes %d should be rejected", limit)
		}
	}
	offering.Parameters[0].MaxBytes = 60 << 20
	if err := meshy.ValidateOffering(offering); err != nil {
		t.Fatal(err)
	}
}

func TestOfferingSupportsOfflineAssets(t *testing.T) {
	inline := remeshInlineOffering(1 << 20)
	if !meshy.OfferingSupportsOfflineAssets(inline) {
		t.Fatal("inline-declared offering should support offline assets")
	}
	withoutInline := remeshInlineOffering(1 << 20)
	withoutInline.ProviderOptions = json.RawMessage(
		`{"endpoint":"remesh","api_version":"v1"}`,
	)
	if meshy.OfferingSupportsOfflineAssets(withoutInline) {
		t.Fatal("asset parameter without inline declaration must not be offline-ready")
	}
	taskOnly := generation.Offering{
		ProviderID: "meshy",
		Capability: generation.Animation,
		Parameters: []generation.Parameter{{
			Name: "source_task",
			Type: "task",
		}},
		ProviderOptions: json.RawMessage(
			`{"endpoint":"animations","api_version":"v1"}`,
		),
	}
	if !meshy.OfferingSupportsOfflineAssets(taskOnly) {
		t.Fatal("task-only offering should support offline assets")
	}
}

func TestTextTo3DRefineSubmissionOmitsUltraMode(t *testing.T) {
	var refineBody map[string]any
	var previewBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/openapi/v2/text-to-3d" {
			t.Errorf("path = %s", request.URL.Path)
			http.NotFound(writer, request)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["mode"] == "refine" {
			refineBody = body
		} else {
			previewBody = body
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{
			"result": "remote-" + body["mode"].(string),
		})
	}))
	defer upstream.Close()
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:    upstream.URL,
		APIKey:     "meshy-key",
		HTTPClient: upstream.Client(),
	})
	// omit_when 规则声明在 Offering 参数上（W2 从 adapter 身份分支迁移）：
	// 直接采用默认目录 meshy 文生 3D Offering 的参数声明。
	offering := catalogMeshyTextTo3DOffering(t)
	offering.ProviderOptions = json.RawMessage(
		`{"endpoint":"text-to-3d","api_version":"v2",` +
			`"model_parameter":"ai_model","result_assets":[` +
			`{"path":"model_urls","role":"model","mode":"map"}]}`,
	)
	if _, err := adapter.Start(context.Background(), generation.StartRequest{
		TaskID:        "task-refine",
		AttemptID:     "attempt-refine",
		SubmissionKey: "submission-refine",
		Offering:      offering,
		Input:         []byte(`{"mode":"refine","ultra_mode":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, exists := refineBody["ultra_mode"]; exists {
		t.Errorf("refine body must not carry ultra_mode, got %#v", refineBody)
	}
	if _, err := adapter.Start(context.Background(), generation.StartRequest{
		TaskID:        "task-preview",
		AttemptID:     "attempt-preview",
		SubmissionKey: "submission-preview",
		Offering:      offering,
		Input:         []byte(`{"mode":"preview","ultra_mode":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	if previewBody["ultra_mode"] != true {
		t.Errorf("preview body must keep ultra_mode, got %#v", previewBody)
	}
}

func TestTransientCongestionFailureIsRetryableUpstreamUnavailable(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.WriteHeader(http.StatusConflict)
		_, _ = writer.Write([]byte(
			`{"message":"Your request took too long to process. Please retry."}`,
		))
	}))
	defer upstream.Close()
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:    upstream.URL,
		APIKey:     "meshy-key",
		HTTPClient: upstream.Client(),
	})
	_, err := adapter.Start(context.Background(), generation.StartRequest{
		TaskID:        "task-congested",
		AttemptID:     "attempt-congested",
		SubmissionKey: "submission-congested",
		Offering: generation.Offering{
			ProviderID: "meshy",
			ModelID:    "meshy-7",
			Capability: generation.TextTo3D,
			ProviderOptions: json.RawMessage(
				`{"endpoint":"text-to-3d","api_version":"v2","result_assets":[]}`,
			),
		},
		Input: []byte(`{"mode":"refine","preview_task_id":"task-preview"}`),
	})
	var providerErr *generation.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Failure == nil {
		t.Fatalf("expected provider error, got %#v", err)
	}
	if providerErr.Failure.Class != generation.FailureUpstreamUnavailable ||
		!providerErr.Failure.Retryable {
		t.Fatalf("failure = %#v", providerErr.Failure)
	}
}

func TestPermanentRejectionCannotBecomeRetryableFromPleaseRetryText(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"Permission denied. Please retry with an authorized account."}`))
			}))
			defer server.Close()
			adapter := newMeshyAdapter(t, meshy.Config{BaseURL: server.URL, APIKey: "test"})
			_, err := adapter.Start(context.Background(), generation.StartRequest{Offering: generation.Offering{ProviderID: "meshy", ModelID: "meshy-7", Capability: generation.TextTo3D, ProviderOptions: json.RawMessage(`{"endpoint":"text-to-3d","api_version":"v2","result_assets":[]}`)}, Input: json.RawMessage(`{"prompt":"test"}`)})
			var pe *generation.ProviderError
			if !errors.As(err, &pe) || pe.Failure.Retryable {
				t.Fatalf("permanent rejection=%#v", err)
			}
		})
	}
}

// TestRiggingResultVariantsSatisfyCatalogPrimaryPrefix 覆盖 §3.3/§3.5：
// rigging 结果树展开的主产物变体必须命中当前目录的 required 前缀，
// basic_animations.* 只命中 background 前缀；两次观察变体稳定。
func TestRiggingStartWithTwoRequiredFormatsNeedsNoTargetFormats(t *testing.T) {
	posts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/openapi/v1/rigging" {
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
			http.NotFound(writer, request)
			return
		}
		posts++
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model_url"] != "https://assets.test/source.glb" {
			t.Errorf("body = %#v", body)
		}
		if _, exists := body["target_formats"]; exists {
			t.Errorf("unexpected target_formats: %#v", body)
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{"result": "rigging-remote"})
	}))
	defer upstream.Close()
	adapter := newMeshyAdapter(t, meshy.Config{BaseURL: upstream.URL, APIKey: "test", HTTPClient: upstream.Client()})
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, exists := taskCatalog.Get("meshy/processing/rigging")
	if !exists {
		t.Fatal("rigging offering missing from default catalog")
	}
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering: offering, Input: json.RawMessage(`{"source_url":"https://assets.test/source.glb"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if posts != 1 || started.Job == nil || started.Job.RemoteID != "rigging-remote" {
		t.Fatalf("posts=%d started=%#v", posts, started)
	}
}

func TestRiggingResultVariantsSatisfyCatalogPrimaryPrefix(t *testing.T) {
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/openapi/v1/rigging":
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"result": "rigging-remote-9",
			})
		case "/openapi/v1/rigging/rigging-remote-9":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"status":   "SUCCEEDED",
				"progress": 100,
				"result": map[string]any{
					"rigged_character_glb_url": upstream.URL + "/rigged.glb",
					"basic_animations": map[string]string{
						"idle": upstream.URL + "/idle.fbx",
					},
				},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstream.Close()
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:    upstream.URL,
		APIKey:     "meshy-key",
		HTTPClient: upstream.Client(),
	})
	offering := generation.Offering{
		ProviderID: "meshy",
		Capability: generation.Rigging,
		Parameters: []generation.Parameter{{
			Name:     "source_url",
			Type:     "text",
			WireName: "model_url",
		}},
		ProviderOptions: json.RawMessage(
			`{"endpoint":"rigging","api_version":"v1","result_assets":[` +
				`{"path":"result","role":"model","mode":"tree"}]}`,
		),
	}
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering: offering,
		Input:    []byte(`{"source_url":"https://assets.test/source.glb"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	observe := func() []generation.ArtifactClaim {
		completed, err := adapter.Observe(context.Background(), generation.ObserveRequest{
			AttemptID: "attempt-rigging",
			Offering:  offering,
			Job:       *started.Job,
		})
		if err != nil {
			t.Fatal(err)
		}
		return completed.Artifacts
	}
	artifacts := observe()
	variants := map[string]bool{}
	for _, artifact := range artifacts {
		variants[artifact.Variant] = true
	}
	if len(artifacts) != 2 ||
		!variants["rigged_character_glb_url"] ||
		!variants["basic_animations.idle"] {
		t.Fatalf("artifacts = %#v", artifacts)
	}
	repeated := observe()
	for index := range artifacts {
		if repeated[index].Variant != artifacts[index].Variant {
			t.Fatalf(
				"variant unstable: %#v vs %#v",
				artifacts[index].Variant,
				repeated[index].Variant,
			)
		}
	}

	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	declared, exists := taskCatalog.Get("meshy/processing/rigging")
	if !exists {
		t.Fatal("rigging offering missing from default catalog")
	}
	var requiredVariants, conditionalPrefixes []string
	for _, output := range declared.Outputs {
		if output.Delivery != generation.AssetDeliveryRequired {
			t.Fatalf("non-required rigging output = %#v", output)
		}
		if output.Minimum != nil && *output.Minimum == 1 {
			requiredVariants = append(requiredVariants, output.Variant)
		} else if output.Minimum != nil && *output.Minimum == 0 {
			conditionalPrefixes = append(conditionalPrefixes, output.VariantPrefix)
		}
	}
	if len(requiredVariants) != 2 || len(conditionalPrefixes) != 1 ||
		conditionalPrefixes[0] != "basic_animations." {
		t.Fatalf("rigging declarations = %#v", declared.Outputs)
	}
	for _, artifact := range artifacts {
		if artifact.Variant == "rigged_character_glb_url" {
			if requiredVariants[0] != artifact.Variant && requiredVariants[1] != artifact.Variant {
				t.Fatalf("primary %q missing exact required slot", artifact.Variant)
			}
			continue
		}
		if !strings.HasPrefix(artifact.Variant, conditionalPrefixes[0]) {
			t.Fatalf(
				"collection %q must hit required-if-present prefix %q",
				artifact.Variant,
				conditionalPrefixes[0],
			)
		}
	}
}

// catalogMeshyTextTo3DOffering 读取默认目录的 meshy 文生 3D Offering
// （含 ultra_mode 的 omit_when 声明）。
func catalogMeshyTextTo3DOffering(t *testing.T) generation.Offering {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, exists := taskCatalog.Get("meshy/meshy-7/text-to-3d")
	if !exists {
		t.Fatal("默认 Catalog 缺少 meshy 文生 3D Offering")
	}
	return offering
}

// TestTextureUrlsRemainNonContractualExtraAssets 是 P4 登记锁定测试
// （2026-09-21 REGISTRATION-ONLY 用户裁决；冻结矩阵 §1 不可表达项、
// 计划 §11.9 P4 登记）。meshy texture_urls 的 wire 变体是索引前缀族
// （result_assets array-map 按数组位置投影，形如 "1.base_color"），在
// 计划 §3.2 前缀重叠规则下无法既覆盖全部索引又不与同 role 其他声明
// 冲突；两项触发重评条件经复核均未满足（上游无稳定非索引变体——索引
// 族本身即 wire 形状；§3.2 修订未获授权）。产品裁决：texture_urls 保持
// 非契约额外资产——照常落盘展示，但不进入 outputs[]、不参与成功门槛与
// 资源完成度计数。本测试锁定：
//  1. 真实目录 meshy 文生 3D outputs[] 零 texture 声明——任何
//     texture_urls 契约化（新增 role/variant/prefix 声明）使本测试 RED；
//  2. 携带 texture_urls 的真实形状 SUCCEEDED 响应把索引族整体投影为
//     claims 且可经 OpenAsset 下载——额外资产交付被移除（result_assets
//     声明删除、投影变更或索引族形状漂移）使本测试 RED；
//  3. 无 texture_urls 的同形响应成功观察与契约相关 claims 完全一致——
//     任务成功不依赖 texture 资产；内核按 role 匹配（coordinator.go
//     materialize 对非契约 claims blocking=false、validateClaims 只按
//     声明槽位计数、assignRequiredOutputs 只标记命中资产），outputs[]
//     无 texture 声明时 texture claims 既不能满足也不能失败任何门槛。
func TestTextureUrlsRemainNonContractualExtraAssets(t *testing.T) {
	offering := catalogMeshyTextTo3DOffering(t)

	// 锁 1：真实 outputs[] 契约面 = legacy model + thumbnail primary
	// background，无任何 texture role 声明。
	if len(offering.Outputs) != 2 {
		t.Fatalf("outputs = %#v", offering.Outputs)
	}
	for _, output := range offering.Outputs {
		if output.Role == "texture" {
			t.Fatalf("texture_urls 已被契约化进 outputs[]: %#v", output)
		}
	}
	modelOutput := offering.Outputs[0]
	if modelOutput.Role != "model" || modelOutput.Variant != "" ||
		modelOutput.VariantPrefix != "" || modelOutput.Delivery != "" ||
		modelOutput.Minimum == nil || *modelOutput.Minimum != 1 {
		t.Fatalf("model declaration = %#v", modelOutput)
	}
	thumbnailOutput := offering.Outputs[1]
	if thumbnailOutput.Role != "thumbnail" ||
		thumbnailOutput.Variant != "primary" ||
		thumbnailOutput.Delivery != generation.AssetDeliveryBackground ||
		thumbnailOutput.Minimum == nil || *thumbnailOutput.Minimum != 0 {
		t.Fatalf("thumbnail declaration = %#v", thumbnailOutput)
	}

	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		result := map[string]any{
			"status":   "SUCCEEDED",
			"progress": 100,
			"model_urls": map[string]string{
				"glb": upstream.URL + "/model.glb",
			},
			"thumbnail_url": upstream.URL + "/thumbnail.png",
		}
		switch request.URL.Path {
		case "/openapi/v2/text-to-3d/remote-textured":
			result["id"] = "remote-textured"
			result["texture_urls"] = []map[string]string{{
				"base_color": upstream.URL + "/base_color.png",
				"metallic":   upstream.URL + "/metallic.png",
				"normal":     upstream.URL + "/normal.png",
				"roughness":  upstream.URL + "/roughness.png",
			}}
			_ = json.NewEncoder(writer).Encode(result)
		case "/openapi/v2/text-to-3d/remote-plain":
			result["id"] = "remote-plain"
			_ = json.NewEncoder(writer).Encode(result)
		case "/model.glb":
			writer.Header().Set("Content-Type", "model/gltf-binary")
			_, _ = writer.Write([]byte("glTF asset"))
		case "/thumbnail.png", "/base_color.png", "/metallic.png",
			"/normal.png", "/roughness.png":
			writer.Header().Set("Content-Type", "image/png")
			_, _ = writer.Write([]byte("png:" + request.URL.Path))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstream.Close()
	parsed, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:         upstream.URL,
		APIKey:          "meshy-key",
		HTTPClient:      upstream.Client(),
		AllowHTTPAssets: true,
	})
	// 沙箱只放宽资产主机白名单；ProviderOptions（wire result_assets，含
	// texture_urls array-map 声明）与 Outputs（资源契约）保持真实目录原样。
	sandbox := offering
	sandbox.AssetHosts = []string{parsed.Hostname()}

	observe := func(remoteID string) []generation.ArtifactClaim {
		t.Helper()
		completed, err := adapter.Observe(
			context.Background(),
			generation.ObserveRequest{
				TaskID:    "task-texture-lock",
				AttemptID: "attempt-texture-lock",
				Offering:  sandbox,
				Job: generation.UpstreamJob{
					ProviderID: "meshy",
					RemoteID:   remoteID,
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if completed.Phase != generation.UpstreamCompleted {
			t.Fatalf("phase = %#v", completed.Phase)
		}
		return completed.Artifacts
	}

	// 锁 2：真实形状响应 → 索引族整体投影为 texture claims，变体恰为
	// "1.<field>"（数组位置索引 = 现行 wire 形状，无稳定非索引变体）。
	textured := observe("remote-textured")
	if len(textured) != 6 {
		t.Fatalf("claims = %#v", textured)
	}
	byVariant := map[string]generation.ArtifactClaim{}
	for _, claim := range textured {
		byVariant[claim.Role+"/"+claim.Variant] = claim
	}
	for _, want := range []string{
		"model/glb",
		"thumbnail/primary",
		"texture/1.base_color",
		"texture/1.metallic",
		"texture/1.normal",
		"texture/1.roughness",
	} {
		if _, exists := byVariant[want]; !exists {
			t.Fatalf("claim %q missing: %#v", want, byVariant)
		}
	}
	for _, claim := range textured {
		if claim.Role != "texture" {
			continue
		}
		if !strings.HasPrefix(claim.Variant, "1.") {
			t.Fatalf(
				"texture variant %q 不是索引前缀族（wire 形状漂移）",
				claim.Variant,
			)
		}
		// 锁 3：内核对每个 texture 变体交付类别探针均为空（outputs[]
		// 无声明可匹配 → 非契约，不参与门槛/完成度计数）。
		if delivery := generation.AssetDelivery(
			offering.Outputs,
			claim.Role,
			claim.Variant,
		); delivery != "" {
			t.Fatalf(
				"texture claim %q 已进入资源契约: delivery = %q",
				claim.Variant,
				delivery,
			)
		}
	}
	// 正对照：探针能区分已声明槽位（非恒空，防锁 3 空洞）。
	if delivery := generation.AssetDelivery(
		offering.Outputs,
		"thumbnail",
		"primary",
	); delivery != generation.AssetDeliveryBackground {
		t.Fatalf("positive control delivery = %q", delivery)
	}

	// 锁 4：无 texture_urls 的响应成功观察一致、契约相关 claims 相同——
	// 任务成功与资源完成度不依赖 texture 资产（texture 纯增量）。
	plain := observe("remote-plain")
	if len(plain) != 2 {
		t.Fatalf("plain claims = %#v", plain)
	}
	for _, claim := range plain {
		key := claim.Role + "/" + claim.Variant
		if key != "model/glb" && key != "thumbnail/primary" {
			t.Fatalf("plain claims = %#v", plain)
		}
	}

	// 锁 5：额外资产真实可交付——内核 materialize 对非契约 claims 走
	// 同一 OpenAsset 并以 Required=false 落盘（照常展示，不入门槛）。
	body, metadata, err := adapter.OpenAsset(
		context.Background(),
		generation.OpenAssetRequest{
			Offering: sandbox,
			Claim:    byVariant["texture/1.base_color"],
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	content, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "png:/base_color.png" ||
		metadata.MediaType != "image/png" {
		t.Fatalf("content = %q, metadata = %#v", content, metadata)
	}
}

func TestCapabilitySupportsOfflineAssetsRequiresEveryOffering(t *testing.T) {
	inline := remeshInlineOffering(1 << 20)
	blocked := remeshInlineOffering(1 << 20)
	blocked.ProviderOptions = json.RawMessage(`{"endpoint":"remesh","api_version":"v1"}`)
	unrelated := blocked
	unrelated.Capability = generation.Animation
	for _, test := range []struct {
		name      string
		offerings []generation.Offering
		want      bool
	}{
		{"no offering", nil, false},
		{"inline offering", []generation.Offering{inline}, true},
		{"mixed routes", []generation.Offering{inline, blocked}, false},
		{"other capability", []generation.Offering{inline, unrelated}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := meshy.CapabilitySupportsOfflineAssets(test.offerings, inline.Capability); got != test.want {
				t.Fatalf("offline support = %v, want %v", got, test.want)
			}
		})
	}
}
