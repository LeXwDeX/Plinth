package meshy_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

// fakeRefineUpstream serves one preview task and one refine task. Tests set
// the status of each stage and read back every create body.
type fakeRefineUpstream struct {
	mu            sync.Mutex
	server        *httptest.Server
	previewStatus string
	refineStatus  string
	creates       []map[string]any
}

func newFakeRefineUpstream(t *testing.T) *fakeRefineUpstream {
	t.Helper()
	fake := &fakeRefineUpstream{previewStatus: "IN_PROGRESS", refineStatus: "PENDING"}
	fake.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/openapi/v2/text-to-3d":
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			fake.creates = append(fake.creates, body)
			result := "preview-1"
			if body["mode"] == "refine" {
				result = "refine-1"
			}
			_ = json.NewEncoder(writer).Encode(map[string]string{"result": result})
		case request.URL.Path == "/openapi/v2/text-to-3d/preview-1":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"id": "preview-1", "status": fake.previewStatus, "progress": stageProgress(fake.previewStatus, 60),
				"model_urls": map[string]string{"glb": fake.server.URL + "/preview.glb"},
			})
		case request.URL.Path == "/openapi/v2/text-to-3d/refine-1":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"id": "refine-1", "status": fake.refineStatus, "progress": stageProgress(fake.refineStatus, 40),
				"model_urls":          map[string]string{"glb": fake.server.URL + "/refine.glb"},
				"alpha_thumbnail_url": fake.server.URL + "/refine-alpha.png",
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func stageProgress(status string, running int) int {
	switch status {
	case "SUCCEEDED":
		return 100
	case "IN_PROGRESS":
		return running
	}
	return 0
}

func (f *fakeRefineUpstream) set(preview, refine string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.previewStatus, f.refineStatus = preview, refine
}

func (f *fakeRefineUpstream) createBodies() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.creates...)
}

func refineOffering(t *testing.T, upstream *httptest.Server) generation.Offering {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, exists := taskCatalog.Get("meshy/meshy-7.1/text-to-3d")
	if !exists {
		t.Fatal("默认 Catalog 缺少 meshy-7.1 文生 3D Offering")
	}
	parsed, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	offering.AssetHosts = []string{parsed.Hostname()}
	return offering
}

// normalizedInput runs the Product Schema normalization, so the test sees the
// same frozen input the kernel would hand to the adapter.
func normalizedInput(t *testing.T, offering generation.Offering, raw string) json.RawMessage {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	_, normalized, err := taskCatalog.Normalize(offering.Capability, offering.ID, json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	return normalized
}

func TestCatalogKeepsMeshy71AutoRefineWithAdvisedQuality(t *testing.T) {
	for _, tc := range []struct {
		name      string
		raw       string
		polycount float64
	}{
		{"highest-quality-default", `{"prompt":"castle","mode":"refine"}`, 300000},
		{"legal-agent-adjustment", `{"prompt":"castle","mode":"refine","target_polycount":1000}`, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeRefineUpstream(t)
			offering := refineOffering(t, fake.server)
			if offering.EstimatedCredits != 40 {
				t.Fatalf("estimated credits = %d, want preview 25 + refine 15", offering.EstimatedCredits)
			}
			// The adapter consumes normalized input. Agent adjustment reasons are
			// checked and frozen by Generation before this boundary.
			input := normalizedInput(t, offering, tc.raw)
			var values map[string]any
			if err := json.Unmarshal(input, &values); err != nil {
				t.Fatal(err)
			}
			if values["mode"] != "preview" || values["target_polycount"] != tc.polycount ||
				values["geometry_resolution"] != "4k" || values["texture_resolution"] != "8k" || values["enable_pbr"] != true {
				t.Fatalf("normalized quality/protocol values = %#v", values)
			}
			formats, _ := values["target_formats"].([]any)
			if len(formats) != 6 {
				t.Fatalf("complete formats = %#v", formats)
			}
			for _, format := range []string{"glb", "obj", "fbx", "stl", "usdz", "3mf"} {
				found := false
				for _, output := range offering.Outputs {
					if string(output.Role) == "model" && output.Variant == format && output.Minimum != nil && *output.Minimum == 1 {
						found = true
					}
				}
				if !found {
					t.Errorf("missing required model output %s", format)
				}
			}
			adapter := newMeshyAdapter(t, meshy.Config{BaseURL: fake.server.URL, APIKey: "test", HTTPClient: fake.server.Client(), AllowHTTPAssets: true})
			if _, err := adapter.Start(context.Background(), generation.StartRequest{TaskID: "task", AttemptID: "attempt", SubmissionKey: "submission", Offering: offering, Input: input}); err != nil {
				t.Fatal(err)
			}
			body := fake.createBodies()[0]
			if body["mode"] != "preview" || body["target_polycount"] != tc.polycount || body["geometry_resolution"] != "4k" {
				t.Fatalf("preview wire values = %#v", body)
			}
		})
	}
}

func TestMeshy71AdvisedQualityStillRejectsInvalidInputs(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, _ := c.Get("meshy/meshy-7.1/text-to-3d")
	for _, raw := range []string{
		`{"prompt":"castle","target_polycount":99}`,
		`{"prompt":"castle","target_polycount":300001}`,
		`{"prompt":"castle","geometry_resolution":"unsupported"}`,
		`{"prompt":"castle","topology":"quad","texture_resolution":"8k"}`,
		`{"prompt":"castle","target_formats":["glb"]}`,
		`{"prompt":"castle","unexpected_field":true}`,
	} {
		if _, _, err := c.Normalize(o.Capability, o.ID, []byte(raw)); err == nil {
			t.Errorf("accepted invalid input %s", raw)
		}
	}
}

func TestTextTo3DRefinesAutomaticallyAfterPreview(t *testing.T) {
	fake := newFakeRefineUpstream(t)
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:         fake.server.URL,
		APIKey:          "meshy-key",
		HTTPClient:      fake.server.Client(),
		AllowHTTPAssets: true,
	})
	offering := refineOffering(t, fake.server)
	input := normalizedInput(t, offering, `{"prompt":"castle"}`)
	ctx := context.Background()

	started, err := adapter.Start(ctx, generation.StartRequest{
		TaskID: "task-1", AttemptID: "attempt-1", SubmissionKey: "submission-1",
		Offering: offering, Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Job == nil || started.Job.RemoteID != "preview-1" {
		t.Fatalf("started = %#v", started)
	}
	preview := fake.createBodies()[0]
	if preview["mode"] != "preview" || preview["geometry_resolution"] != "4k" {
		t.Fatalf("preview body = %#v", preview)
	}
	for _, refineOnly := range []string{"texture_resolution", "preview_task_id"} {
		if _, exists := preview[refineOnly]; exists {
			t.Fatalf("preview body must not carry %s: %#v", refineOnly, preview)
		}
	}

	request := generation.ObserveRequest{
		TaskID: "task-1", AttemptID: "attempt-1", Offering: offering,
		Job: *started.Job, Input: input,
	}
	running, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if running.Phase != generation.UpstreamRunning || running.Progress != 30 {
		t.Fatalf("preview running = phase %s progress %d", running.Phase, running.Progress)
	}

	fake.set("SUCCEEDED", "PENDING")
	submitted, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if submitted.Phase != generation.UpstreamRunning || submitted.Progress != 50 ||
		len(submitted.Artifacts) != 0 || submitted.Job == nil || submitted.Job.RemoteID != "preview-1" {
		t.Fatalf("refine submitted = %#v", submitted)
	}
	creates := fake.createBodies()
	if len(creates) != 2 {
		t.Fatalf("create calls = %d, want preview and refine", len(creates))
	}
	refine := creates[1]
	if refine["mode"] != "refine" || refine["preview_task_id"] != "preview-1" ||
		refine["texture_resolution"] != "8k" || refine["enable_pbr"] != true {
		t.Fatalf("refine body = %#v", refine)
	}
	if _, exists := refine["geometry_resolution"]; exists {
		t.Fatalf("refine body must not carry geometry_resolution: %#v", refine)
	}

	request.Opaque = submitted.Opaque
	fake.set("SUCCEEDED", "IN_PROGRESS")
	refining, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if refining.Phase != generation.UpstreamRunning || refining.Progress != 70 {
		t.Fatalf("refine running = phase %s progress %d", refining.Phase, refining.Progress)
	}

	fake.set("SUCCEEDED", "SUCCEEDED")
	completed, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Phase != generation.UpstreamCompleted || completed.Job == nil || completed.Job.RemoteID != "preview-1" {
		t.Fatalf("completed = %#v", completed)
	}
	if len(completed.Artifacts) == 0 {
		t.Fatal("completed refine must deliver artifacts")
	}
	alphaThumbnail := false
	for _, claim := range completed.Artifacts {
		if claim.Role == "thumbnail" && claim.Variant == "alpha" {
			alphaThumbnail = containsText(claim.Locator, "refine-alpha.png")
			continue
		}
		encoded, _ := json.Marshal(claim)
		if !json.Valid(encoded) || !containsText(string(encoded), "refine.glb") {
			t.Fatalf("artifact must come from the refine task: %s", encoded)
		}
	}
	if !alphaThumbnail {
		t.Fatal("the transparent thumbnail must be kept as an extra asset")
	}
	if len(fake.createBodies()) != 2 {
		t.Fatal("observing a running or finished refine must not create another task")
	}
}

func TestTextTo3DRefineDispatchOfUnknownOutcomeIsNeverResent(t *testing.T) {
	fake := newFakeRefineUpstream(t)
	fake.set("SUCCEEDED", "PENDING")
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL: fake.server.URL, APIKey: "meshy-key", HTTPClient: fake.server.Client(), AllowHTTPAssets: true,
	})
	offering := refineOffering(t, fake.server)
	observation, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		TaskID: "task-1", AttemptID: "attempt-1", Offering: offering,
		Job:    generation.UpstreamJob{ProviderID: "meshy", RemoteID: "preview-1"},
		Input:  normalizedInput(t, offering, `{"prompt":"castle"}`),
		Opaque: json.RawMessage(`{"stage":"text-to-3d.refine","submission_key":"attempt-1:refine"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.Phase != generation.UpstreamFailed || observation.Failure == nil {
		t.Fatalf("observation = %#v", observation)
	}
	if len(fake.createBodies()) != 0 {
		t.Fatal("a pending refine intent must not be posted again")
	}
}

func TestTextTo3DPostSuccessObservationNeverSubmitsRefine(t *testing.T) {
	fake := newFakeRefineUpstream(t)
	fake.set("SUCCEEDED", "PENDING")
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL: fake.server.URL, APIKey: "meshy-key", HTTPClient: fake.server.Client(), AllowHTTPAssets: true,
	})
	offering := refineOffering(t, fake.server)
	if _, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		TaskID: "task-1", AttemptID: "attempt-1", Offering: offering,
		Job:         generation.UpstreamJob{ProviderID: "meshy", RemoteID: "preview-1"},
		Input:       normalizedInput(t, offering, `{"prompt":"castle"}`),
		PostSuccess: true,
	}); err != nil {
		t.Fatal(err)
	}
	if len(fake.createBodies()) != 0 {
		t.Fatal("post-success observation must not create a paid refine")
	}
}

func TestTextTo3DAutoRefineIgnoresWebhooks(t *testing.T) {
	fake := newFakeRefineUpstream(t)
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL: fake.server.URL, APIKey: "meshy-key", HTTPClient: fake.server.Client(),
	})
	offering := refineOffering(t, fake.server)
	_, err := adapter.DecodeWebhook(context.Background(), generation.WebhookDecodeRequest{
		TaskID: "task-1", AttemptID: "attempt-1", Offering: offering,
		Job:     generation.UpstreamJob{ProviderID: "meshy", RemoteID: "preview-1"},
		Payload: []byte(`{"id":"preview-1","status":"SUCCEEDED","progress":100}`),
	})
	if !errors.Is(err, generation.ErrWebhookNotApplicable) {
		t.Fatalf("err = %v, want ErrWebhookNotApplicable", err)
	}
}

// Task snapshots frozen before 2026-10-10 carry refine_after_preview: true.
// They must keep refining with the same Meshy values the Schema now declares.
func TestTextTo3DLegacyBooleanRefineDeclarationStillRefines(t *testing.T) {
	fake := newFakeRefineUpstream(t)
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL: fake.server.URL, APIKey: "meshy-key", HTTPClient: fake.server.Client(), AllowHTTPAssets: true,
	})
	offering := refineOffering(t, fake.server)
	var options map[string]any
	if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	options["refine_after_preview"] = true
	offering.ProviderOptions, _ = json.Marshal(options)
	if err := meshy.ValidateOffering(offering); err != nil {
		t.Fatal(err)
	}
	input := normalizedInput(t, offering, `{"prompt":"castle"}`)
	ctx := context.Background()
	started, err := adapter.Start(ctx, generation.StartRequest{
		TaskID: "task-1", AttemptID: "attempt-1", SubmissionKey: "submission-1", Offering: offering, Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	fake.set("SUCCEEDED", "PENDING")
	if _, err := adapter.Observe(ctx, generation.ObserveRequest{
		TaskID: "task-1", AttemptID: "attempt-1", Offering: offering, Job: *started.Job, Input: input,
	}); err != nil {
		t.Fatal(err)
	}
	creates := fake.createBodies()
	if len(creates) != 2 || creates[1]["mode"] != "refine" || creates[1]["preview_task_id"] != "preview-1" {
		t.Fatalf("legacy boolean declaration must still refine: %#v", creates)
	}
}

// An incomplete declaration would silently deliver the untextured preview as
// a finished task, so startup validation rejects it.
func TestValidateOfferingRejectsIncompleteRefineDeclaration(t *testing.T) {
	fake := newFakeRefineUpstream(t)
	offering := refineOffering(t, fake.server)
	if err := meshy.ValidateOffering(offering); err != nil {
		t.Fatal(err)
	}
	for _, declaration := range []string{
		`{"set":{"mode":"refine"}}`,
		`{"source_parameter":"preview_task_id"}`,
		`{"set":{"mode":"refine"},"source_parameter":"missing"}`,
	} {
		var options map[string]any
		if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
			t.Fatal(err)
		}
		options["refine_after_preview"] = json.RawMessage(declaration)
		broken := offering
		broken.ProviderOptions, _ = json.Marshal(options)
		if err := meshy.ValidateOffering(broken); err == nil {
			t.Fatalf("%s must be rejected at startup", declaration)
		}
	}
}

func containsText(haystack, needle string) bool {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return true
		}
	}
	return false
}
