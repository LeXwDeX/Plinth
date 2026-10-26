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
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type multiImageInputStore map[generation.AssetID][]byte

func (multiImageInputStore) Resolve(context.Context, generation.OwnerID, generation.Offering, json.RawMessage) (map[string][]generation.InputAsset, error) {
	return nil, nil
}

func (assets multiImageInputStore) Open(_ context.Context, asset generation.InputAsset) (io.ReadCloser, error) {
	content, ok := assets[asset.ID]
	if !ok {
		return nil, errors.New("asset bytes not found")
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

func multiImageOffering() generation.Offering {
	one, zero := 1, 0
	maxBytes := int64(1024)
	return generation.Offering{
		ID: "meshy/test/multi-image-to-3d", Revision: "wire-test", ProviderID: "meshy",
		ModelID: "meshy-7.1-multiview", Capability: generation.ImageTo3D, PollInterval: time.Second,
		AssetHosts: []string{"assets.meshy.ai"},
		Parameters: []schema.Parameter{
			{Name: "source_task", Type: "task", Semantic: schema.SemanticSourceTask, WireName: "input_task_id", TaskSourceKind: schema.TaskSourceUpstreamJob, SameProvider: true, TaskCapabilities: []generation.Capability{generation.TextToImage, generation.ImageToImage}, TaskOfferings: []generation.OfferingID{"meshy/test/text-to-image", "meshy/test/image-to-image"}},
			{Name: "reference_assets", Type: "asset-array", Semantic: schema.SemanticReferenceImage, WireName: "image_urls", MinItems: 1, MaxItems: 4, MaxBytes: maxBytes, MediaTypes: []string{"image/jpeg", "image/png", "image/webp"}},
			{Name: "target_formats", Type: "string-array", WireName: "target_formats"},
		},
		Outputs:         []generation.AssetRequirement{{Role: "model", Variant: "glb", MediaTypes: []string{"model/gltf-binary"}, Minimum: &one}, {Role: "thumbnail", Variant: "primary", Delivery: "background", Minimum: &zero}},
		ProviderOptions: json.RawMessage(`{"endpoint":"multi-image-to-3d","api_version":"v1","model_parameter":"ai_model","model_parameter_value":"meshy-7.1","result_assets":[{"path":"model_urls","role":"model","mode":"map"},{"path":"thumbnail_url","role":"thumbnail","mode":"scalar","variant":"primary"},{"path":"texture_urls","role":"texture","mode":"array-map"}]}`),
	}
}

func TestMultiImageOfferingSchemaContract(t *testing.T) {
	offering := multiImageOffering()
	if err := meshy.ValidateOffering(offering); err != nil {
		t.Fatalf("valid multi-image schema rejected: %v", err)
	}
	for name, mutate := range map[string]func(*generation.Offering){
		"idempotent replay":    func(o *generation.Offering) { o.IdempotentSubmission = true },
		"wrong capability":     func(o *generation.Offering) { o.Capability = generation.TextTo3D },
		"oversized byte bound": func(o *generation.Offering) { o.Parameters[1].MaxBytes = 21 << 20 },
		"wrong API version": func(o *generation.Offering) {
			o.ProviderOptions = json.RawMessage(strings.Replace(string(o.ProviderOptions), `"v1"`, `"v2"`, 1))
		},
		"deprecated model": func(o *generation.Offering) {
			o.ProviderOptions = json.RawMessage(strings.Replace(string(o.ProviderOptions), `"meshy-7.1"`, `"meshy-7"`, 1))
		},
		"bad task capability": func(o *generation.Offering) {
			o.Parameters[0].TaskCapabilities = []generation.Capability{generation.TextTo3D}
		},
		"no source form":               func(o *generation.Offering) { o.Parameters = o.Parameters[2:] },
		"asset count exceeds endpoint": func(o *generation.Offering) { o.Parameters[1].MaxItems = 5 },
		"unsupported media type": func(o *generation.Offering) {
			o.Parameters[1].MediaTypes = append(o.Parameters[1].MediaTypes, "image/gif")
		},
		"missing model projection": func(o *generation.Offering) {
			o.ProviderOptions = json.RawMessage(`{"endpoint":"multi-image-to-3d","api_version":"v1","model_parameter":"ai_model","result_assets":[{"path":"thumbnail_url","role":"thumbnail","mode":"scalar"}]}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := multiImageOffering()
			mutate(&candidate)
			if err := meshy.ValidateOffering(candidate); err == nil {
				t.Fatal("invalid schema was accepted")
			}
		})
	}
}

// 标量投影必须在启动时就覆盖每个必需模型槽位，并保持 path 与 variant 一致；
// 混合 map 与标量投影会重复交付同一槽位。否则配置错误要等付费任务进入物化
// 阶段才暴露（#191）。
func TestMultiImageScalarProjectionsMustCoverEveryRequiredModelSlot(t *testing.T) {
	glb := `{"path":"model_urls.glb","role":"model","mode":"scalar","variant":"glb","media_type":"model/gltf-binary"}`
	options := func(projections ...string) json.RawMessage {
		return json.RawMessage(`{"endpoint":"multi-image-to-3d","api_version":"v1","model_parameter":"ai_model","model_parameter_value":"meshy-7.1","result_assets":[` + strings.Join(projections, ",") + `]}`)
	}
	one := 1

	t.Run("exact scalar slot accepted", func(t *testing.T) {
		offering := multiImageOffering()
		offering.ProviderOptions = options(glb)
		if err := meshy.ValidateOffering(offering); err != nil {
			t.Fatalf("exact scalar projection rejected: %v", err)
		}
	})
	t.Run("missing required slot", func(t *testing.T) {
		offering := multiImageOffering()
		offering.Outputs = append(offering.Outputs, generation.AssetRequirement{Role: "model", Variant: "obj", MediaTypes: []string{"model/obj"}, Minimum: &one})
		offering.ProviderOptions = options(glb)
		if err := meshy.ValidateOffering(offering); err == nil {
			t.Fatal("schema without an obj scalar projection was accepted")
		}
	})
	t.Run("path does not match variant", func(t *testing.T) {
		offering := multiImageOffering()
		offering.ProviderOptions = options(`{"path":"model_urls.glbx","role":"model","mode":"scalar","variant":"glb","media_type":"model/gltf-binary"}`)
		if err := meshy.ValidateOffering(offering); err == nil {
			t.Fatal("projection whose path does not match its variant was accepted")
		}
	})
	t.Run("mixed map and scalar", func(t *testing.T) {
		offering := multiImageOffering()
		offering.ProviderOptions = options(`{"path":"model_urls","role":"model","mode":"map"}`, glb)
		if err := meshy.ValidateOffering(offering); err == nil {
			t.Fatal("mixed map and scalar projections were accepted")
		}
	})
}

func TestMultiImageDirectAssetsAlwaysUsePluralFieldAndPreserveBytes(t *testing.T) {
	imageBytes := []byte("owned-original-image-bytes")
	var upstreamCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		if r.Method != http.MethodPost || r.URL.Path != "/openapi/v1/multi-image-to-3d" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["ai_model"] != "meshy-7.1" || body["image_url"] != nil || body["reference_assets"] != nil {
			t.Errorf("body = %#v", body)
		}
		urls, ok := body["image_urls"].([]any)
		if !ok || len(urls) != 1 {
			t.Fatalf("image_urls = %#v", body["image_urls"])
		}
		want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes)
		if urls[0] != want {
			t.Errorf("image_urls[0] did not preserve the source bytes")
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"result": "remote-multi-1"})
	}))
	defer server.Close()

	adapter := newMeshyAdapter(t, meshy.Config{BaseURL: server.URL, APIKey: "test-key", HTTPClient: server.Client(), InputAssets: memoryInputAssets{content: imageBytes}, AllowHTTPAssets: true})
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		TaskID: "task", AttemptID: "attempt", SubmissionKey: "submit", OwnerID: "owner",
		Offering: multiImageOffering(), Input: json.RawMessage(`{"reference_assets":["asset-1"],"target_formats":["glb"]}`),
		InputAssets: map[string][]generation.InputAsset{"reference_assets": {{ID: "asset-1", OwnerID: "owner", MediaType: "image/png", Size: int64(len(imageBytes))}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Job == nil || started.Job.RemoteID != "remote-multi-1" || upstreamCalls != 1 {
		t.Fatalf("started=%#v calls=%d", started, upstreamCalls)
	}
}

func TestMultiImageTaskSourceUsesResolvedRemoteTaskID(t *testing.T) {
	var upstreamCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		if r.Method != http.MethodPost || r.URL.Path != "/openapi/v1/multi-image-to-3d" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["input_task_id"] != "mesh-image-task" || body["image_urls"] != nil || body["source_task"] != nil {
			t.Errorf("body = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"result": "remote-multi-task"})
	}))
	defer server.Close()
	adapter := newMeshyAdapter(t, meshy.Config{BaseURL: server.URL, APIKey: "test-key", HTTPClient: server.Client()})
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		TaskID: "task", AttemptID: "attempt", SubmissionKey: "submit", OwnerID: "owner",
		Offering: multiImageOffering(), Input: json.RawMessage(`{"source_task":"local-task"}`),
		TaskDependencies: map[string]generation.TaskDependency{"source_task": {TaskID: "local-task", Capability: generation.ImageToImage, OfferingID: "meshy/test/image-to-image", ProviderID: "meshy", UpstreamJob: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "mesh-image-task"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Job == nil || started.Job.RemoteID != "remote-multi-task" || upstreamCalls != 1 {
		t.Fatalf("started=%#v calls=%d", started, upstreamCalls)
	}
}

func TestMultiImageAssetOrderPreservedForPrimaryView(t *testing.T) {
	front, back := []byte("front-image"), []byte("back-image")
	wantURLs := []string{
		"data:image/png;base64," + base64.StdEncoding.EncodeToString(front),
		"data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(back),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		urls, ok := body["image_urls"].([]any)
		if !ok || len(urls) != len(wantURLs) {
			t.Fatalf("image_urls = %#v", body["image_urls"])
		}
		for i, want := range wantURLs {
			if urls[i] != want {
				t.Errorf("image_urls[%d] lost its original order/bytes", i)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"result": "remote-ordered"})
	}))
	defer server.Close()
	assets := multiImageInputStore{"front": front, "back": back}
	adapter := newMeshyAdapter(t, meshy.Config{BaseURL: server.URL, APIKey: "test-key", HTTPClient: server.Client(), InputAssets: assets})
	_, err := adapter.Start(context.Background(), generation.StartRequest{
		TaskID: "task", AttemptID: "attempt", SubmissionKey: "submit", OwnerID: "owner",
		Offering: multiImageOffering(), Input: json.RawMessage(`{"reference_assets":["front","back"]}`),
		InputAssets: map[string][]generation.InputAsset{"reference_assets": {
			{ID: "front", OwnerID: "owner", MediaType: "image/png", Size: int64(len(front))},
			{ID: "back", OwnerID: "owner", MediaType: "image/jpeg", Size: int64(len(back))},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMultiImageRejectsMissingOrAmbiguousInputBeforePost(t *testing.T) {
	upstreamCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		_ = json.NewEncoder(w).Encode(map[string]string{"result": "should-not-run"})
	}))
	defer server.Close()
	adapter := newMeshyAdapter(t, meshy.Config{BaseURL: server.URL, APIKey: "test-key", HTTPClient: server.Client(), InputAssets: memoryInputAssets{content: []byte("image")}})
	asset := generation.InputAsset{ID: "asset-1", OwnerID: "owner", MediaType: "image/png", Size: 5}
	for name, request := range map[string]generation.StartRequest{
		"missing": {Offering: multiImageOffering(), Input: json.RawMessage(`{}`)},
		"both": {
			Offering: multiImageOffering(), Input: json.RawMessage(`{"source_task":"task"}`),
			TaskDependencies: map[string]generation.TaskDependency{"source_task": {TaskID: "task", ProviderID: "meshy", UpstreamJob: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "remote"}}},
			InputAssets:      map[string][]generation.InputAsset{"reference_assets": {asset}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			request.TaskID, request.AttemptID, request.SubmissionKey = "task", "attempt", "submit"
			if _, err := adapter.Start(context.Background(), request); err == nil {
				t.Fatal("invalid source selection was accepted")
			}
		})
	}
	if upstreamCalls != 0 {
		t.Fatalf("invalid inputs made %d paid POSTs", upstreamCalls)
	}
}

func TestMultiImageObserveProjectsDeclaredModelSlots(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/openapi/v1/multi-image-to-3d/remote-task" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "remote-task", "status": "SUCCEEDED", "progress": 100,
			"model_urls":    map[string]string{"glb": "https://assets.meshy.ai/model.glb?Expires=fixture"},
			"thumbnail_url": "https://assets.meshy.ai/thumbnail.png?Expires=fixture",
			"texture_urls":  []map[string]string{{"base_color": "https://assets.meshy.ai/base-color.png?Expires=fixture"}},
		})
	}))
	defer server.Close()
	adapter := newMeshyAdapter(t, meshy.Config{BaseURL: server.URL, APIKey: "test-key", HTTPClient: server.Client()})
	observed, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		TaskID: "task", AttemptID: "attempt", Offering: multiImageOffering(),
		Job: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "remote-task"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Phase != generation.UpstreamCompleted || len(observed.Artifacts) != 3 {
		t.Fatalf("observed=%#v", observed)
	}
	if observed.Artifacts[0].Role != "model" || observed.Artifacts[0].Variant != "glb" {
		t.Errorf("model artifact = %#v", observed.Artifacts[0])
	}
	for _, artifact := range observed.Artifacts {
		if !strings.Contains(artifact.Locator, "assets.meshy.ai") {
			t.Errorf("unexpected artifact locator: %q", artifact.Locator)
		}
	}
}

// 默认 Catalog 的多图 Offering 必须在观察阶段就给出六个目标格式的精确 MIME，
// 使物化不依赖 CDN 的 octet-stream 或 OBJ 的 application/x-tgif 错标（#191）。
func TestDefaultMultiviewObservationCarriesExactDeliveryTypes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/openapi/v1/multi-image-to-3d/remote-multiview" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "remote-multiview", "status": "SUCCEEDED", "progress": 100,
			"model_urls": map[string]string{
				"glb":  "https://assets.meshy.ai/model.glb?Expires=fixture",
				"obj":  "https://assets.meshy.ai/model.obj?Expires=fixture",
				"fbx":  "https://assets.meshy.ai/model.fbx?Expires=fixture",
				"stl":  "https://assets.meshy.ai/model.stl?Expires=fixture",
				"usdz": "https://assets.meshy.ai/model.usdz?Expires=fixture",
				"3mf":  "https://assets.meshy.ai/model.3mf?Expires=fixture",
				"mtl":  "https://assets.meshy.ai/model.mtl?Expires=fixture",
			},
			"thumbnail_url": "https://assets.meshy.ai/thumbnail.png?Expires=fixture",
			"texture_urls":  []map[string]string{{"base_color": "https://assets.meshy.ai/base-color.png?Expires=fixture"}},
		})
	}))
	defer server.Close()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := taskCatalog.Get("meshy/meshy-7.1-multiview/image-to-3d")
	if !ok {
		t.Fatal("multiview offering missing")
	}
	adapter := newMeshyAdapter(t, meshy.Config{BaseURL: server.URL, APIKey: "test-key", HTTPClient: server.Client()})
	observed, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		TaskID: "task", AttemptID: "attempt", Offering: offering,
		Job: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "remote-multiview"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Phase != generation.UpstreamCompleted {
		t.Fatalf("phase = %s", observed.Phase)
	}
	want := map[string]string{
		"glb": "model/gltf-binary", "obj": "model/obj", "fbx": "model/fbx",
		"stl": "model/stl", "usdz": "model/vnd.usdz+zip", "3mf": "model/3mf",
	}
	got := map[string]string{}
	for _, artifact := range observed.Artifacts {
		if artifact.Role == "model" {
			got[artifact.Variant] = artifact.MediaType
		}
	}
	for variant, media := range want {
		if got[variant] != media {
			t.Fatalf("%s media_type = %q, want %q (all=%v)", variant, got[variant], media, got)
		}
	}
}

func TestMultiImageMissingCreateResultIsUnknownWithoutRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	adapter := newMeshyAdapter(t, meshy.Config{BaseURL: server.URL, APIKey: "test-key", HTTPClient: server.Client()})
	_, err := adapter.Start(context.Background(), generation.StartRequest{
		TaskID: "task", AttemptID: "attempt", SubmissionKey: "submit", OwnerID: "owner",
		Offering: multiImageOffering(), Input: json.RawMessage(`{"source_task":"local-task"}`),
		TaskDependencies: map[string]generation.TaskDependency{"source_task": {TaskID: "local-task", Capability: generation.ImageToImage, OfferingID: "meshy/test/image-to-image", ProviderID: "meshy", UpstreamJob: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "mesh-image-task"}}},
	})
	var providerErr *generation.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Failure.Class != generation.FailureSubmissionUnknown || !providerErr.Failure.Ambiguous {
		t.Fatalf("error = %#v, want an ambiguous submission result", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want exactly one POST", calls)
	}
}

func TestMultiImageThreeAndFourViewsKeepExactBytesAndFirstView(t *testing.T) {
	for _, count := range []int{3, 4} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			assets := multiImageInputStore{}
			inputAssets := []generation.InputAsset{}
			ids := []generation.AssetID{}
			for i := 0; i < count; i++ {
				id := generation.AssetID(fmt.Sprintf("view-%d", i))
				assets[id] = []byte(fmt.Sprintf("original-view-%d", i))
				ids = append(ids, id)
				inputAssets = append(inputAssets, generation.InputAsset{ID: id, OwnerID: "owner", MediaType: "image/webp", Size: int64(len(assets[id]))})
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				urls, ok := body["image_urls"].([]any)
				if !ok || len(urls) != count {
					t.Fatalf("views=%v", body)
				}
				for i, id := range ids {
					if urls[i] != "data:image/webp;base64,"+base64.StdEncoding.EncodeToString(assets[id]) {
						t.Fatalf("view %d changed", i)
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"result": "remote"})
			}))
			defer server.Close()
			a := newMeshyAdapter(t, meshy.Config{BaseURL: server.URL, APIKey: "test", HTTPClient: server.Client(), InputAssets: assets})
			raw, _ := json.Marshal(map[string]any{"reference_assets": ids})
			_, err := a.Start(context.Background(), generation.StartRequest{TaskID: "task", AttemptID: "attempt", SubmissionKey: "key", OwnerID: "owner", Offering: multiImageOffering(), Input: raw, InputAssets: map[string][]generation.InputAsset{"reference_assets": inputAssets}})
			if err != nil || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestMultiImageRejectsForeignOwnerAndExcessViewsBeforeDispatch(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	a := newMeshyAdapter(t, meshy.Config{BaseURL: server.URL, APIKey: "test", HTTPClient: server.Client(), InputAssets: multiImageInputStore{"image": []byte("bytes")}})
	for _, kind := range []string{"foreign", "count", "media"} {
		t.Run(kind, func(t *testing.T) {
			assets := []generation.InputAsset{{ID: "image", OwnerID: "owner", MediaType: "image/png", Size: 5}}
			switch kind {
			case "foreign":
				assets[0].OwnerID = "other"
			case "count":
				for len(assets) < 5 {
					assets = append(assets, assets[0])
				}
			case "media":
				assets[0].MediaType = "image/gif"
			}
			_, err := a.Start(context.Background(), generation.StartRequest{TaskID: "task", AttemptID: "attempt", SubmissionKey: "key", OwnerID: "owner", Offering: multiImageOffering(), Input: []byte(`{"reference_assets":["image"]}`), InputAssets: map[string][]generation.InputAsset{"reference_assets": assets}})
			if err == nil {
				t.Fatal("unsafe input accepted")
			}
		})
	}
	if calls != 0 {
		t.Fatal("unsafe input reached upstream")
	}
}

func TestMultiImageTaskDependencyMustMatchDeclaredScopeBeforePost(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	a := newMeshyAdapter(t, meshy.Config{BaseURL: server.URL, APIKey: "test", HTTPClient: server.Client()})
	for _, kind := range []string{"provider", "offering", "capability", "remote", "missing-task"} {
		t.Run(kind, func(t *testing.T) {
			dep := generation.TaskDependency{TaskID: "local-task", ProviderID: "meshy", OfferingID: "meshy/test/image-to-image", Capability: generation.ImageToImage, UpstreamJob: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "remote"}}
			switch kind {
			case "provider":
				dep.ProviderID = "other"
			case "offering":
				dep.OfferingID = "other"
			case "capability":
				dep.Capability = generation.TextTo3D
			case "remote":
				dep.UpstreamJob.ProviderID = "other"
			case "missing-task":
				dep.TaskID = ""
			}
			_, err := a.Start(context.Background(), generation.StartRequest{TaskID: "task", AttemptID: "attempt", SubmissionKey: "key", OwnerID: "owner", Offering: multiImageOffering(), Input: []byte(`{"source_task":"local-task"}`), TaskDependencies: map[string]generation.TaskDependency{"source_task": dep}})
			if err == nil {
				t.Fatal("unqualified dependency accepted")
			}
		})
	}
	if calls != 0 {
		t.Fatal("unqualified task reached upstream")
	}
}
