package matting_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/matting"
)

func TestBatchLifecycleAndAuthenticatedDownload(t *testing.T) {
	image := []byte("matting-output")
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.Header.Get("Authorization") != "Bearer matting-key" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		switch request.URL.Path {
		case "/api/v1/jobs":
			if err := request.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return
			}
			files := request.MultipartForm.File["files"]
			if len(files) != 1 {
				t.Errorf("files = %#v", files)
			}
			var pipeline map[string]any
			_ = json.Unmarshal(
				[]byte(request.FormValue("pipeline_params_json")),
				&pipeline,
			)
			if pipeline["birefnet_process_res"] != float64(768) ||
				pipeline["sdmatte_process_res"] != float64(896) ||
				pipeline["birefnet_mask_blur"] != float64(8) ||
				pipeline["expand"] != float64(240) ||
				pipeline["tapered_corners"] != true ||
				pipeline["blur_radius"] != float64(30) ||
				pipeline["mask_refine"] != true {
				t.Errorf("pipeline = %#v", pipeline)
			}
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"batch_id": "batch-7",
			})
		case "/api/v1/batches/batch-7":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"batch_id":     "batch-7",
				"batch_status": "PARTIAL_SUCCESS",
				"tasks": []any{
					map[string]any{
						"task_id":       "subtask-1",
						"status":        "SUCCESS",
						"downloadable":  true,
						"download_path": "/api/v1/tasks/subtask-1/download",
					},
					map[string]any{
						"task_id":       "subtask-2",
						"status":        "FAILED",
						"downloadable":  false,
						"error_message": "bad image",
					},
				},
			})
		case "/api/v1/tasks/subtask-1/download":
			writer.Header().Set("Content-Type", "image/png")
			_, _ = writer.Write(image)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstream.Close()
	inputs := memoryInputs{content: []byte("input-image")}
	adapter := newAdapter(t, upstream, inputs)
	offering := generation.Offering{
		ProviderID:   "matting",
		Capability:   generation.BackgroundRemove,
		AdapterKey:   "matting.v1",
		PollInterval: time.Second,
		Parameters: []generation.Parameter{
			{
				Name:     "source_assets",
				Type:     "asset-array",
				Required: true,
				MinItems: 1,
				MaxItems: 20,
				Semantic: generation.SemanticReferenceImage,
			},
			{Name: "birefnet_process_res", Type: "number"},
			{Name: "birefnet_mask_blur", Type: "number"},
			{Name: "expand", Type: "number"},
			{Name: "tapered_corners", Type: "boolean"},
			{Name: "blur_radius", Type: "number"},
			{Name: "sdmatte_process_res", Type: "number"},
			{Name: "transparent_object", Type: "boolean"},
			{Name: "mask_refine", Type: "boolean"},
			{Name: "sensitivity", Type: "number"},
		},
		ProviderOptions: defaultProviderOptions(),
	}
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		AttemptID:     "attempt-1",
		SubmissionKey: "submission-1",
		Offering:      offering,
		Input: []byte(
			`{"source_assets":["asset-1"],"birefnet_process_res":768,` +
				`"birefnet_mask_blur":8,"expand":240,"tapered_corners":true,` +
				`"blur_radius":30,"sdmatte_process_res":896,` +
				`"transparent_object":true,"mask_refine":true,"sensitivity":0.1}`,
		),
		InputAssets: map[string][]generation.InputAsset{
			"source_assets": {{
				ID:        "asset-1",
				MediaType: "image/png",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Job == nil || started.Job.RemoteID != "batch-7" {
		t.Fatalf("started = %#v", started)
	}
	completed, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		AttemptID: "attempt-1",
		Offering:  offering,
		Job:       *started.Job,
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Phase != generation.UpstreamCompleted ||
		len(completed.Artifacts) != 1 {
		t.Fatalf("completed = %#v", completed)
	}
	var providerRecords []map[string]any
	if err := json.Unmarshal(completed.Opaque, &providerRecords); err != nil {
		t.Fatalf("opaque = %s, err = %v", completed.Opaque, err)
	}
	if len(providerRecords) != 2 ||
		providerRecords[0]["identifier"] != "subtask-1" ||
		providerRecords[0]["status"] != "SUCCEEDED" ||
		providerRecords[0]["successful_ordinal"] != float64(1) ||
		providerRecords[1]["status"] != "FAILED" ||
		providerRecords[1]["error_message"] != "bad image" {
		t.Fatalf("provider records = %#v", providerRecords)
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
	payload, _ := io.ReadAll(body)
	if !bytes.Equal(payload, image) || metadata.MediaType != "image/png" {
		t.Fatalf("payload = %q, metadata = %#v", payload, metadata)
	}
}

// 回归：live Matting 服务的子任务终态是 SUCCEEDED（批次级才是 SUCCESS），
// 状态集合必须同时覆盖两种词汇，否则成功批次被误判为无可下载产物。
func TestSucceededTaskStatusVocabularyProducesClaims(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if strings.HasPrefix(request.URL.Path, "/api/v1/batches/") {
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"batch_status": "SUCCESS",
				"tasks": []any{map[string]any{
					"task_id":       "subtask-live",
					"status":        "SUCCEEDED",
					"downloadable":  true,
					"download_path": "/api/v1/tasks/subtask-live/download",
				}},
			})
			return
		}
		http.NotFound(writer, request)
	}))
	defer upstream.Close()
	adapter := newAdapter(t, upstream, nil)
	observation, err := adapter.Observe(
		context.Background(),
		generation.ObserveRequest{
			AttemptID: "attempt-live",
			Offering:  defaultOffering(),
			Job:       generation.UpstreamJob{RemoteID: "batch-live"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Phase != generation.UpstreamCompleted ||
		len(observation.Artifacts) != 1 ||
		observation.Artifacts[0].Role != "image" {
		t.Fatalf("observation = %#v", observation)
	}
}

func TestFailedBatchMapsSafeFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if strings.HasPrefix(request.URL.Path, "/api/v1/batches/") {
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"batch_status": "FAILED",
				"tasks": []any{map[string]any{
					"status":        "FAILED",
					"error_message": "unsupported image",
				}},
			})
			return
		}
		http.NotFound(writer, request)
	}))
	defer upstream.Close()
	adapter := newAdapter(t, upstream, nil)
	observation, err := adapter.Observe(
		context.Background(),
		generation.ObserveRequest{
			Offering: defaultOffering(),
			Job:      generation.UpstreamJob{RemoteID: "batch-failed"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Phase != generation.UpstreamFailed ||
		observation.Failure.SafeDetail != "unsupported image" {
		t.Fatalf("observation = %#v", observation)
	}
}

func TestObservationDedupeKeyChangesWhenProviderRecordsChange(t *testing.T) {
	polls := 0
	upstream := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			polls++
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"batch_id":     "batch-changing",
				"batch_status": "RUNNING",
				"tasks": []map[string]any{{
					"task_id":       "subtask-changing",
					"status":        "RUNNING",
					"downloadable":  false,
					"error_message": fmt.Sprintf("detail-%d", polls),
				}},
			})
		},
	))
	t.Cleanup(upstream.Close)
	adapter := newAdapter(t, upstream, memoryInputs{})
	request := generation.ObserveRequest{
		AttemptID: "attempt-changing",
		Offering:  defaultOffering(),
		Job: generation.UpstreamJob{
			RemoteID: "batch-changing",
		},
	}
	first, err := adapter.Observe(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := adapter.Observe(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Progress != second.Progress ||
		first.Phase != second.Phase ||
		string(first.Opaque) == string(second.Opaque) {
		t.Fatalf("observations = %#v / %#v", first, second)
	}
	if first.DedupeKey == second.DedupeKey {
		t.Fatalf("dedupe key swallowed provider record update: %q", first.DedupeKey)
	}
}

func TestDownloadLocatorCannotEscapeProviderBase(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	adapter := newAdapter(t, upstream, nil)
	_, _, err := adapter.OpenAsset(
		context.Background(),
		generation.OpenAssetRequest{
			Offering: defaultOffering(),
			Claim: generation.ArtifactClaim{
				Locator: "api/v1/tasks/../../admin/download",
			},
		},
	)
	var providerErr *generation.ProviderError
	if !errors.As(err, &providerErr) ||
		providerErr.Failure.Class != generation.FailureProtocol {
		t.Fatalf("error = %#v", err)
	}
}

func TestWireProtocolAndArtifactProjectionComeFromOfferingSchema(t *testing.T) {
	image := []byte("schema-output")
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/schema/create":
			if err := request.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return
			}
			if len(request.MultipartForm.File["schema_files"]) != 1 {
				t.Errorf("files = %#v", request.MultipartForm.File)
			}
			var pipeline map[string]any
			_ = json.Unmarshal(
				[]byte(request.FormValue("schema_pipeline")),
				&pipeline,
			)
			if pipeline["wire_strength"] != float64(7) {
				t.Errorf("pipeline = %#v", pipeline)
			}
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"schema_job": "schema-remote",
			})
		case "/schema/observe/schema-remote":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"schema_phase": "DONE",
				"schema_items": []any{
					map[string]any{
						"schema_id":       "schema-output-1",
						"schema_state":    "OK",
						"schema_ready":    true,
						"schema_filename": "source-one.png",
					},
				},
			})
		case "/schema/download/schema-output-1":
			writer.Header().Set("Content-Type", "image/schema")
			_, _ = writer.Write(image)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstream.Close()
	adapter := newAdapter(
		t,
		upstream,
		memoryInputs{content: []byte("schema-input")},
	)
	offering := generation.Offering{
		ProviderID:   "matting",
		Capability:   generation.BackgroundRemove,
		AdapterKey:   "matting.v1",
		PollInterval: time.Second,
		Parameters: []generation.Parameter{
			{
				Name:     "configured_sources",
				Type:     "asset-array",
				Required: true,
				MinItems: 1,
				MaxItems: 2,
				Semantic: generation.SemanticReferenceImage,
			},
			{
				Name:     "strength",
				WireName: "wire_strength",
				Type:     "number",
			},
		},
		ProviderOptions: json.RawMessage(`{
			"create_path":"schema/create",
			"observe_path":"schema/observe/{remote_id}",
			"download_path":"schema/download/{task_id}",
			"multipart":{
				"pipeline_field":"schema_pipeline",
				"file_field":"schema_files",
				"filename_template":"schema_{ordinal}{extension}"
			},
			"response":{
				"batch_id_path":"schema_job",
				"batch_status_path":"schema_phase",
				"tasks_path":"schema_items",
				"task_id_path":"schema_id",
				"task_status_path":"schema_state",
				"downloadable_path":"schema_ready",
				"download_path_path":"schema_path",
				"source_name_path":"schema_filename",
				"error_message_path":"schema_error",
				"invalid_reason_path":"schema_invalid"
			},
			"statuses":{
				"accepted":["ACCEPTED"],
				"running":["WORKING"],
				"completed":["DONE"],
				"failed":["BROKEN"],
				"canceled":["STOPPED"],
				"finished_tasks":["OK","BAD"],
				"successful_tasks":["OK"]
			},
			"artifact":{"role":"schema-image","media_type":"image/schema"},
			"messages":{
				"invalid_capability":"schema invalid capability",
				"invalid_assets":"schema invalid assets",
				"invalid_input":"schema invalid input",
				"missing_batch_id":"schema missing job",
				"no_artifacts":"schema no artifacts",
				"failed":"schema failed",
				"canceled":"schema canceled",
				"unknown_status":"schema unknown {status}",
				"invalid_locator":"schema invalid locator",
				"download_request_failed":"schema request failed",
				"download_failed":"schema download failed",
				"download_http_status":"schema HTTP {status}"
			}
		}`),
	}
	started, err := adapter.Start(
		context.Background(),
		generation.StartRequest{
			AttemptID:     "schema-attempt",
			SubmissionKey: "schema-submission",
			Offering:      offering,
			Input:         json.RawMessage(`{"strength":7}`),
			InputAssets: map[string][]generation.InputAsset{
				"configured_sources": {{
					ID:        "schema-input-1",
					MediaType: "image/png",
				}},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if started.Job == nil || started.Job.RemoteID != "schema-remote" {
		t.Fatalf("started = %#v", started)
	}
	completed, err := adapter.Observe(
		context.Background(),
		generation.ObserveRequest{
			AttemptID: "schema-attempt",
			Offering:  offering,
			Job:       *started.Job,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Phase != generation.UpstreamCompleted ||
		len(completed.Artifacts) != 1 ||
		completed.Artifacts[0].Role != "schema-image" ||
		completed.Artifacts[0].MediaType != "image/schema" ||
		completed.Artifacts[0].Variant != "source-one.png" ||
		completed.Artifacts[0].Locator !=
			"schema/download/schema-output-1" {
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
	payload, _ := io.ReadAll(body)
	if !bytes.Equal(payload, image) ||
		metadata.MediaType != "image/schema" {
		t.Fatalf("payload = %q, metadata = %#v", payload, metadata)
	}
}

func TestValidateOfferingRejectsMissingWireProtocolAtStartup(t *testing.T) {
	err := matting.ValidateOffering(generation.Offering{
		ID:              "invalid-matting",
		AdapterKey:      "matting.v1",
		ProviderOptions: json.RawMessage(`{}`),
	})
	if err == nil {
		t.Fatal("missing Matting wire protocol unexpectedly accepted")
	}
}

func newAdapter(
	t *testing.T,
	upstream *httptest.Server,
	inputs generation.InputAssetResolver,
) *matting.Adapter {
	t.Helper()
	adapter, err := matting.New(matting.Config{
		BaseURL:     upstream.URL,
		APIKey:      "matting-key",
		HTTPClient:  upstream.Client(),
		InputAssets: inputs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func defaultOffering() generation.Offering {
	return generation.Offering{
		ID:           "matting/default/background-removal",
		ProviderID:   "matting",
		Capability:   generation.BackgroundRemove,
		AdapterKey:   "matting.v1",
		PollInterval: time.Second,
		Parameters: []generation.Parameter{{
			Name:     "source_assets",
			Type:     "asset-array",
			Required: true,
			MinItems: 1,
			MaxItems: 20,
			Semantic: generation.SemanticReferenceImage,
		}},
		ProviderOptions: defaultProviderOptions(),
	}
}

func defaultProviderOptions() json.RawMessage {
	return json.RawMessage(`{
		"create_path":"api/v1/jobs",
		"observe_path":"api/v1/batches/{remote_id}",
		"download_path":"api/v1/tasks/{task_id}/download",
		"multipart":{
			"pipeline_field":"pipeline_params_json",
			"file_field":"files",
			"filename_template":"input_{ordinal}{extension}"
		},
		"response":{
			"batch_id_path":"batch_id",
			"batch_status_path":"batch_status",
			"tasks_path":"tasks",
			"task_id_path":"task_id",
			"task_status_path":"status",
			"downloadable_path":"downloadable",
			"download_path_path":"download_path",
			"source_name_path":"source_name",
			"error_message_path":"error_message",
			"invalid_reason_path":"invalid_reason"
		},
		"statuses":{
			"accepted":["RECEIVED","STORED","READY"],
			"running":["RUNNING"],
			"completed":["SUCCESS","PARTIAL_SUCCESS"],
			"failed":["FAILED"],
			"canceled":["CANCELED"],
			"finished_tasks":["SUCCESS","SUCCEEDED","FAILED","CANCELED"],
			"successful_tasks":["SUCCESS","SUCCEEDED"]
		},
		"artifact":{"role":"image","media_type":"image/png"},
		"messages":{
			"invalid_capability":"invalid capability",
			"invalid_assets":"invalid assets",
			"invalid_input":"invalid input",
			"missing_batch_id":"missing batch",
			"no_artifacts":"no artifacts",
			"failed":"failed",
			"canceled":"canceled",
			"unknown_status":"unknown {status}",
			"invalid_locator":"invalid locator",
			"download_request_failed":"request failed",
			"download_failed":"download failed",
			"download_http_status":"HTTP {status}"
		}
	}`)
}

type memoryInputs struct {
	content []byte
}

func (memoryInputs) Resolve(
	context.Context,
	generation.OwnerID,
	generation.Offering,
	json.RawMessage,
) (map[string][]generation.InputAsset, error) {
	return nil, nil
}

func (m memoryInputs) Open(
	context.Context,
	generation.InputAsset,
) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(m.content)), nil
}

// TestDeclaredDownloadAuthOverridesDefaultHeader：W2 起下载鉴权头由
// provider_options.download_auth 声明；未声明时回退 Authorization Bearer。
func TestDeclaredDownloadAuthOverridesDefaultHeader(t *testing.T) {
	image := []byte("matting-output")
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/api/v1/jobs":
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"batch_id": "batch-auth",
			})
		case "/api/v1/batches/batch-auth":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"batch_id":     "batch-auth",
				"batch_status": "SUCCESS",
				"tasks": []any{
					map[string]any{
						"task_id":      "subtask-auth",
						"status":       "SUCCESS",
						"downloadable": true,
					},
				},
			})
		case "/api/v1/tasks/subtask-auth/download":
			if request.Header.Get("X-Acme-Token") != "Token matting-key" {
				t.Errorf("declared auth header = %q", request.Header.Get("X-Acme-Token"))
			}
			writer.Header().Set("Content-Type", "image/png")
			_, _ = writer.Write(image)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstream.Close()
	inputs := memoryInputs{content: []byte("input-image")}
	adapter := newAdapter(t, upstream, inputs)
	offering := mattingOfferingWithWire(t)
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(offering.ProviderOptions, &wire); err != nil {
		t.Fatal(err)
	}
	wire["download_auth"] = json.RawMessage(`{"header":"X-Acme-Token","prefix":"Token "}`)
	offering.ProviderOptions, _ = json.Marshal(wire)
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		AttemptID:     "attempt-matting-auth",
		SubmissionKey: "submission-matting-auth",
		Offering:      offering,
		Input:         []byte(`{"source_assets":["asset-1"]}`),
		InputAssets: map[string][]generation.InputAsset{
			"source_assets": {{
				ID:        "asset-1",
				MediaType: "image/png",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		AttemptID: "attempt-matting-auth",
		Offering:  offering,
		Job:       *started.Job,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.Artifacts) == 0 {
		t.Fatal("no artifacts")
	}
	if _, _, err := adapter.OpenAsset(context.Background(), generation.OpenAssetRequest{
		Claim:    observation.Artifacts[0],
		Offering: offering,
	}); err != nil {
		t.Fatal(err)
	}
}

func mattingOfferingWithWire(t *testing.T) generation.Offering {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, exists := taskCatalog.Get("matting/default/background-removal")
	if !exists {
		t.Fatal("default matting offering missing")
	}
	return offering
}
