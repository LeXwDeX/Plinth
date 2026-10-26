package meshy_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

// #329 HTTP boundary: what the default Catalog's Meshy Offerings actually send,
// checked against the 2026-10-22 documentation baseline.

type upgradeUpstream struct {
	mu     sync.Mutex
	server *httptest.Server
	posts  map[string][]map[string]any
}

func newUpgradeUpstream(t *testing.T) *upgradeUpstream {
	t.Helper()
	upstream := &upgradeUpstream{posts: map[string][]map[string]any{}}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstream.mu.Lock()
		defer upstream.mu.Unlock()
		if request.Method == http.MethodPost {
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			upstream.posts[request.URL.Path] = append(upstream.posts[request.URL.Path], body)
			result := "created-" + request.URL.Path
			if body["mode"] == "refine" {
				result = "refine-1"
			} else if body["mode"] == "preview" {
				result = "preview-1"
			}
			_ = json.NewEncoder(writer).Encode(map[string]string{"result": result})
			return
		}
		switch request.URL.Path {
		case "/openapi/v2/text-to-3d/preview-1":
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": "preview-1", "status": "SUCCEEDED", "progress": 100,
				"model_urls": map[string]string{"glb": upstream.server.URL + "/preview.glb"}})
		case "/openapi/v2/text-to-3d/refine-1":
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": "refine-1", "status": "PENDING", "progress": 0})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func (u *upgradeUpstream) bodies(path string) []map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]map[string]any(nil), u.posts[path]...)
}

func upgradeOffering(t *testing.T, id generation.OfferingID, upstream *httptest.Server) (*catalog.Catalog, generation.Offering) {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := taskCatalog.Get(id)
	if !ok {
		t.Fatalf("offering %s is missing", id)
	}
	if err := meshy.ValidateOffering(offering); err != nil {
		t.Fatalf("%s rejected by the adapter: %v", id, err)
	}
	parsed, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	offering.AssetHosts = []string{parsed.Hostname()}
	return taskCatalog, offering
}

// deprecatedWireFields reads the Schema's own deprecation list, so a field
// added to it is checked here too.
func deprecatedWireFields(t *testing.T, taskCatalog *catalog.Catalog, endpoint string) []string {
	t.Helper()
	var fields []string
	for _, entry := range taskCatalog.Application().ProviderDeprecations {
		if entry.ProviderID != "meshy" {
			continue
		}
		for _, field := range entry.Parameters {
			if len(field.Endpoints) == 0 || slices.Contains(field.Endpoints, endpoint) {
				fields = append(fields, field.Name)
			}
		}
	}
	if len(fields) == 0 {
		t.Fatal("the Schema lists no deprecated Meshy fields")
	}
	return fields
}

func assertNoDeprecatedFields(t *testing.T, taskCatalog *catalog.Catalog, endpoint string, body map[string]any) {
	t.Helper()
	for _, field := range deprecatedWireFields(t, taskCatalog, endpoint) {
		if _, sent := body[field]; sent {
			t.Errorf("%s request sends deprecated %s: %#v", endpoint, field, body)
		}
	}
	if body["ai_model"] == "latest" || body["ai_model"] == "meshy-7" && endpoint != "retexture" {
		t.Errorf("%s request sends ai_model %v", endpoint, body["ai_model"])
	}
	if body["model_type"] == "lowpoly" {
		t.Errorf("%s request sends model_type lowpoly", endpoint)
	}
}

func assertAbsent(t *testing.T, label string, body map[string]any, fields ...string) {
	t.Helper()
	for _, field := range fields {
		if _, exists := body[field]; exists {
			t.Errorf("%s carries %s: %#v", label, field, body)
		}
	}
}

func TestMeshy329SmartTopologyTextTo3DWire(t *testing.T) {
	upstream := newUpgradeUpstream(t)
	taskCatalog, offering := upgradeOffering(t, "meshy/meshy-t2.5/text-to-3d", upstream.server)
	_, input, err := taskCatalog.Normalize(offering.Capability, offering.ID, json.RawMessage(`{"prompt":"low poly sword"}`))
	if err != nil {
		t.Fatal(err)
	}
	adapter := newMeshyAdapter(t, meshy.Config{BaseURL: upstream.server.URL, APIKey: "test", HTTPClient: upstream.server.Client(), AllowHTTPAssets: true})
	started, err := adapter.Start(context.Background(), generation.StartRequest{TaskID: "task", AttemptID: "attempt", SubmissionKey: "submission", Offering: offering, Input: input})
	if err != nil {
		t.Fatal(err)
	}
	preview := upstream.bodies("/openapi/v2/text-to-3d")[0]
	if preview["mode"] != "preview" || preview["ai_model"] != "meshy-t2.5" || preview["model_type"] != "smart-topology" ||
		preview["target_polycount"] != float64(15000) || preview["prompt"] != "low poly sword" {
		t.Fatalf("preview body = %#v", preview)
	}
	assertAbsent(t, "Smart Topology preview", preview, "topology", "should_remesh", "geometry_resolution", "texture_resolution",
		"enable_pbr", "preview_task_id", "texture_prompt")
	assertNoDeprecatedFields(t, taskCatalog, "text-to-3d", preview)

	if _, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		TaskID: "task", AttemptID: "attempt", Offering: offering, Job: *started.Job, Input: input,
	}); err != nil {
		t.Fatal(err)
	}
	creates := upstream.bodies("/openapi/v2/text-to-3d")
	if len(creates) != 2 {
		t.Fatalf("creates = %d, want preview and refine", len(creates))
	}
	refine := creates[1]
	if refine["mode"] != "refine" || refine["preview_task_id"] != "preview-1" || refine["texture_resolution"] != "2k" || refine["enable_pbr"] != true {
		t.Fatalf("refine body = %#v", refine)
	}
	// Meshy's refine documents no meshy-t2.5 value: leaving ai_model out keeps the preview's model.
	assertAbsent(t, "Smart Topology refine", refine, "ai_model", "model_type", "target_polycount", "pose_mode", "geometry_resolution")
	assertNoDeprecatedFields(t, taskCatalog, "text-to-3d", refine)
}

func TestMeshy329SmartTopologyImageTo3DWire(t *testing.T) {
	upstream := newUpgradeUpstream(t)
	taskCatalog, offering := upgradeOffering(t, "meshy/meshy-t2.5/image-to-3d", upstream.server)
	_, input, err := taskCatalog.Normalize(offering.Capability, offering.ID, json.RawMessage(`{"reference_assets":["asset-input"],"target_polycount":8000}`))
	if err != nil {
		t.Fatal(err)
	}
	image := []byte("\x89PNG\r\n\x1a\nreference")
	adapter := newMeshyAdapter(t, meshy.Config{BaseURL: upstream.server.URL, APIKey: "test", HTTPClient: upstream.server.Client(), InputAssets: memoryInputAssets{content: image}})
	if _, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering: offering, Input: input,
		InputAssets: map[string][]generation.InputAsset{"reference_assets": {{ID: "asset-input", MediaType: "image/png"}}},
	}); err != nil {
		t.Fatal(err)
	}
	body := upstream.bodies("/openapi/v1/image-to-3d")[0]
	if body["ai_model"] != "meshy-t2.5" || body["model_type"] != "smart-topology" || body["target_polycount"] != float64(8000) ||
		body["texture_resolution"] != "8k" || body["should_texture"] != true || body["enable_pbr"] != true ||
		body["image_url"] != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(image) {
		t.Fatalf("image to 3d body = %#v", body)
	}
	// image_enhancement and remove_lighting return 400 with Smart Topology.
	assertAbsent(t, "Smart Topology image to 3D", body, "topology", "should_remesh", "geometry_resolution", "image_enhancement",
		"remove_lighting", "image_urls", "reference_assets")
	assertNoDeprecatedFields(t, taskCatalog, "image-to-3d", body)
	if _, _, err := taskCatalog.Normalize(offering.Capability, offering.ID, json.RawMessage(`{"reference_assets":["asset-input"],"target_polycount":30001}`)); err == nil {
		t.Fatal("accepted a face count above Meshy T2.5's 30000")
	}
}

func TestMeshy329MultiImageSendsTextureImageURLs(t *testing.T) {
	upstream := newUpgradeUpstream(t)
	taskCatalog, offering := upgradeOffering(t, "meshy/meshy-7.1-multiview/image-to-3d", upstream.server)
	_, input, err := taskCatalog.Normalize(offering.Capability, offering.ID, json.RawMessage(
		`{"reference_assets":["view-front","view-back"],"texture_assets":["texture-front"],"authorize_extra_credits":true}`))
	if err != nil {
		t.Fatal(err)
	}
	store := multiImageInputStore{
		"view-front": []byte("\x89PNG\r\n\x1a\nfront"), "view-back": []byte("\x89PNG\r\n\x1a\nback"),
		"texture-front": []byte("RIFF\x00\x00\x00\x00WEBPtexture"),
	}
	adapter := newMeshyAdapter(t, meshy.Config{BaseURL: upstream.server.URL, APIKey: "test", HTTPClient: upstream.server.Client(), InputAssets: store})
	if _, err := adapter.Start(context.Background(), generation.StartRequest{
		OwnerID: "owner", Offering: offering, Input: input,
		InputAssets: map[string][]generation.InputAsset{
			"reference_assets": {{ID: "view-front", OwnerID: "owner", MediaType: "image/png"}, {ID: "view-back", OwnerID: "owner", MediaType: "image/png"}},
			"texture_assets":   {{ID: "texture-front", OwnerID: "owner", MediaType: "image/webp"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	body := upstream.bodies("/openapi/v1/multi-image-to-3d")[0]
	wantTexture := []any{"data:image/webp;base64," + base64.StdEncoding.EncodeToString(store["texture-front"])}
	if !reflect.DeepEqual(body["texture_image_urls"], wantTexture) {
		t.Fatalf("texture_image_urls = %#v", body["texture_image_urls"])
	}
	if images, _ := body["image_urls"].([]any); len(images) != 2 || body["ai_model"] != "meshy-7.1" {
		t.Fatalf("multi-image body = %#v", body)
	}
	assertAbsent(t, "multi-image", body, "texture_assets", "texture_image_url", "texture_prompt", "reference_assets")
	assertNoDeprecatedFields(t, taskCatalog, "multi-image-to-3d", body)
}

func TestMeshy329ProcessingWire(t *testing.T) {
	model := []byte("glb-model-bytes")
	modelURI := "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(model)
	for _, tc := range []struct {
		id       generation.OfferingID
		path     string
		input    string
		endpoint string
		check    func(t *testing.T, body map[string]any)
	}{
		{"meshy/processing/retexture", "/openapi/v1/retexture", `{"source_asset":"asset-model","prompt":"rusty metal"}`, "retexture",
			func(t *testing.T, body map[string]any) {
				// Retexture's newest model is meshy-7; it rejects meshy-7.1.
				if body["ai_model"] != "meshy-7" || body["model_url"] != modelURI || body["text_style_prompt"] != "rusty metal" {
					t.Fatalf("retexture body = %#v", body)
				}
			}},
		{"meshy/processing/remesh", "/openapi/v1/remesh", `{"source_asset":"asset-model"}`, "remesh",
			func(t *testing.T, body map[string]any) {
				if body["model_url"] != modelURI || body["topology"] != "quad" {
					t.Fatalf("remesh body = %#v", body)
				}
				assertAbsent(t, "remesh", body, "ai_model", "resize_height", "auto_size", "convert_format_only", "origin_at", "resize_longest_side")
				// Format completion uses Meshy Convert since #330 (convert_wire_330_test.go).
			}},
	} {
		t.Run(string(tc.id), func(t *testing.T) {
			upstream := newUpgradeUpstream(t)
			taskCatalog, offering := upgradeOffering(t, tc.id, upstream.server)
			_, input, err := taskCatalog.Normalize(offering.Capability, offering.ID, json.RawMessage(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			adapter := newMeshyAdapter(t, meshy.Config{BaseURL: upstream.server.URL, APIKey: "test", HTTPClient: upstream.server.Client(), InputAssets: memoryInputAssets{content: model}})
			if _, err := adapter.Start(context.Background(), generation.StartRequest{
				Offering: offering, Input: input, InputAssets: remeshInlineAssets(),
			}); err != nil {
				t.Fatal(err)
			}
			bodies := upstream.bodies(tc.path)
			if len(bodies) != 1 {
				t.Fatalf("posts to %s = %d", tc.path, len(bodies))
			}
			tc.check(t, bodies[0])
			assertNoDeprecatedFields(t, taskCatalog, tc.endpoint, bodies[0])
		})
	}
}

// Every enabled default Meshy generation Offering sends a pinned, current model.
func TestMeshy329DefaultGenerationSendsPinnedCurrentModels(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range []generation.Capability{generation.TextTo3D, generation.ImageTo3D} {
		for _, offering := range taskCatalog.List(capability) {
			if offering.ProviderID != "meshy" {
				continue
			}
			var options struct {
				ModelParameter      string `json:"model_parameter"`
				ModelParameterValue string `json:"model_parameter_value"`
			}
			if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
				t.Fatal(err)
			}
			model := options.ModelParameterValue
			if model == "" && options.ModelParameter != "" {
				model = offering.ModelID
			}
			for _, parameter := range offering.Parameters {
				if parameter.Name == "ai_model" && parameter.Fixed {
					_ = json.Unmarshal(parameter.Default, &model)
				}
			}
			if model != "meshy-7.1" && model != "meshy-t2.5" {
				t.Errorf("%s sends ai_model %q", offering.ID, model)
			}
		}
	}
}

func TestMeshy329MultiImageTextureImagesSchemaContract(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	base, _ := taskCatalog.Get("meshy/meshy-7.1-multiview/image-to-3d")
	if err := meshy.ValidateOffering(base); err != nil {
		t.Fatal(err)
	}
	textureIndex := slices.IndexFunc(base.Parameters, func(parameter generation.Parameter) bool { return parameter.WireName == "texture_image_urls" })
	if textureIndex < 0 {
		t.Fatal("texture_image_urls is not declared")
	}
	for name, mutate := range map[string]func(*generation.Offering){
		"required":    func(o *generation.Offering) { o.Parameters[textureIndex].Required = true },
		"five images": func(o *generation.Offering) { o.Parameters[textureIndex].MaxItems = 5 },
		"over 20 MB":  func(o *generation.Offering) { o.Parameters[textureIndex].MaxBytes = 20_000_001 },
		"gif":         func(o *generation.Offering) { o.Parameters[textureIndex].MediaTypes = []string{"image/gif"} },
		"meshy-6 model": func(o *generation.Offering) {
			o.ProviderOptions = json.RawMessage(`{"endpoint":"multi-image-to-3d","api_version":"v1","model_parameter":"ai_model","model_parameter_value":"meshy-6","result_assets":[{"path":"model_urls","role":"model","mode":"map"}]}`)
		},
		"latest model": func(o *generation.Offering) {
			o.ProviderOptions = json.RawMessage(`{"endpoint":"multi-image-to-3d","api_version":"v1","model_parameter":"ai_model","model_parameter_value":"latest","result_assets":[{"path":"model_urls","role":"model","mode":"map"}]}`)
		},
		"texture prompt": func(o *generation.Offering) {
			o.Parameters = append(o.Parameters, generation.Parameter{Name: "texture_prompt", Type: "text"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			offering := base
			offering.Parameters = slices.Clone(base.Parameters)
			mutate(&offering)
			if err := meshy.ValidateOffering(offering); err == nil {
				t.Fatal("invalid texture_image_urls declaration accepted")
			}
		})
	}
}

// Meshy documents image_url (one picture) on Image to 3D; several views go to
// Multi-Image to 3D. A request with more pictures than that is refused before
// anything is sent, whatever its Offering snapshot declares.
func TestMeshy329SingleImageTo3DSendsOneImageURLOnly(t *testing.T) {
	image := []byte("\x89PNG\r\n\x1a\nreference")
	for _, id := range []generation.OfferingID{"meshy/meshy-7.1/image-to-3d", "meshy/meshy-t2.5/image-to-3d"} {
		t.Run(string(id), func(t *testing.T) {
			upstream := newUpgradeUpstream(t)
			taskCatalog, offering := upgradeOffering(t, id, upstream.server)
			_, input, err := taskCatalog.Normalize(offering.Capability, offering.ID, json.RawMessage(`{"reference_assets":["asset-a"]}`))
			if err != nil {
				t.Fatal(err)
			}
			adapter := newMeshyAdapter(t, meshy.Config{BaseURL: upstream.server.URL, APIKey: "test", HTTPClient: upstream.server.Client(), InputAssets: memoryInputAssets{content: image}})
			if _, err := adapter.Start(context.Background(), generation.StartRequest{
				Offering: offering, Input: input,
				InputAssets: map[string][]generation.InputAsset{"reference_assets": {{ID: "asset-a", MediaType: "image/png"}}},
			}); err != nil {
				t.Fatal(err)
			}
			body := upstream.bodies("/openapi/v1/image-to-3d")[0]
			if body["image_url"] != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(image) {
				t.Fatalf("image_url = %v", body["image_url"])
			}
			assertAbsent(t, string(id), body, "image_urls", "reference_assets")

			// Two pictures (an older snapshot allowed up to four) are refused.
			_, err = adapter.Start(context.Background(), generation.StartRequest{
				Offering: offering, Input: input,
				InputAssets: map[string][]generation.InputAsset{"reference_assets": {{ID: "asset-a", MediaType: "image/png"}, {ID: "asset-b", MediaType: "image/png"}}},
			})
			var providerErr *generation.ProviderError
			if !errors.As(err, &providerErr) || providerErr.Failure == nil || providerErr.Failure.Class != generation.FailureInvalidInput {
				t.Fatalf("two pictures: %v", err)
			}
			if posts := upstream.bodies("/openapi/v1/image-to-3d"); len(posts) != 1 {
				t.Fatalf("two pictures reached Meshy: %d posts", len(posts))
			}
		})
	}
}

// Retexture documents no negative_prompt: the Offering no longer accepts it,
// so no prepared input carries it and the request never sends it. (A session
// setting left over from before is dropped earlier: turn and session
// parameters reach an Offering only when it declares them.)
func TestMeshy329RetextureSendsNoNegativePrompt(t *testing.T) {
	model := []byte("glb-model-bytes")
	upstream := newUpgradeUpstream(t)
	taskCatalog, offering := upgradeOffering(t, "meshy/processing/retexture", upstream.server)
	if _, _, err := taskCatalog.Normalize(offering.Capability, offering.ID, json.RawMessage(`{"source_asset":"asset-model","prompt":"rusty metal","negative_prompt":"blurry"}`)); err == nil {
		t.Fatal("retexture accepted negative_prompt")
	}
	_, normalized, err := taskCatalog.Normalize(offering.Capability, offering.ID, json.RawMessage(`{"source_asset":"asset-model","prompt":"rusty metal"}`))
	if err != nil {
		t.Fatal(err)
	}
	adapter := newMeshyAdapter(t, meshy.Config{BaseURL: upstream.server.URL, APIKey: "test", HTTPClient: upstream.server.Client(), InputAssets: memoryInputAssets{content: model}})
	if _, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering: offering, Input: normalized, InputAssets: remeshInlineAssets(),
	}); err != nil {
		t.Fatal(err)
	}
	body := upstream.bodies("/openapi/v1/retexture")[0]
	assertAbsent(t, "retexture", body, "negative_prompt")
	assertNoDeprecatedFields(t, taskCatalog, "retexture", body)
}
