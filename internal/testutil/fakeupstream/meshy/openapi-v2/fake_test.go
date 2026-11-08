package meshyopenapiv2

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/core"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

const key = "test-key"

func newFixture(t *testing.T) *testkit.Fixture {
	t.Helper()
	return testkit.New(t, fakeupstream.Config{}, Version())
}

func create(t *testing.T, f *testkit.Fixture, body map[string]any, headers ...string) string {
	t.Helper()
	status, _, response := f.Do("POST", Prefix+"/text-to-3d", key, body, headers...)
	if status != http.StatusAccepted {
		t.Fatalf("create = %d %v", status, response)
	}
	return response["result"].(string)
}

func finish(t *testing.T, f *testkit.Fixture, id string) ([]string, map[string]any) {
	t.Helper()
	var seen []string
	for i := 0; i < 8; i++ {
		_, _, body := f.Do("GET", Prefix+"/text-to-3d/"+id, key, nil)
		state := body["status"].(string)
		seen = append(seen, state)
		if state == core.StatusSucceeded || state == core.StatusFailed || state == core.StatusCanceled {
			return seen, body
		}
	}
	t.Fatalf("task %s never finished: %v", id, seen)
	return nil, nil
}

func TestPreviewThenRefineDeliversTheFiles(t *testing.T) {
	f := newFixture(t)
	preview := create(t, f, map[string]any{"mode": "preview", "prompt": "a robot", "target_formats": []string{"glb", "obj"}})
	statuses, body := finish(t, f, preview)
	if strings.Join(statuses, ",") != "PENDING,IN_PROGRESS,SUCCEEDED" || body["type"] != "text-to-3d-preview" {
		t.Fatalf("preview: %v %v", statuses, body)
	}
	refine := create(t, f, map[string]any{"mode": "refine", "preview_task_id": preview})
	_, refined := finish(t, f, refine)
	if refined["type"] != "text-to-3d-refine" {
		t.Fatalf("refine type = %v", refined["type"])
	}
	status, contentType, data := f.Download(testkit.Field(t, refined, "model_urls", "glb").(string))
	if status != http.StatusOK || contentType != "model/gltf-binary" {
		t.Fatalf("glb = %d %q", status, contentType)
	}
	if err := testkit.DeliveryErr(t, data, "model", "glb", "model/gltf-binary"); err != nil {
		t.Fatalf("GLB rejected: %v", err)
	}
	// The preview is mesh-only: an OBJ comes without a material library, while
	// the textured refine carries one (finding #203, captured exchange 36 vs 55).
	if _, has := testkit.Field(t, body, "model_urls").(map[string]any)["mtl"]; has {
		t.Fatalf("a preview OBJ carries an MTL: %v", body["model_urls"])
	}
	refinedWithOBJ := create(t, f, map[string]any{
		"mode": "refine", "preview_task_id": preview, "target_formats": []string{"obj"},
	})
	_, refinedOBJ := finish(t, f, refinedWithOBJ)
	mtl := testkit.Field(t, refinedOBJ, "model_urls", "mtl").(string)
	if status, contentType, data := f.Download(mtl); status != http.StatusOK || contentType != "text/plain" || len(data) == 0 {
		t.Fatalf("refine mtl = %d %q %d bytes", status, contentType, len(data))
	}
}

// The documented terminal status and the documented 403 refusal (finding #203).
func TestCanceledAndForbiddenAnswers(t *testing.T) {
	f := newFixture(t)
	id := create(t, f, map[string]any{"mode": "preview", "prompt": "p"}, "X-Fake-Scenario", "task-canceled")
	statuses, body := finish(t, f, id)
	if got := strings.Join(statuses, ","); got != "PENDING,IN_PROGRESS,CANCELED" {
		t.Fatalf("flow = %s", got)
	}
	if body["status"] != core.StatusCanceled || body["progress"].(float64) != 100 {
		t.Fatalf("canceled body = %v", body)
	}
	if models, _ := body["model_urls"].(map[string]any); len(models) != 0 {
		t.Fatalf("a canceled task offers models: %v", models)
	}
	status, _, failure := f.Do("POST", Prefix+"/text-to-3d", key, map[string]any{"mode": "preview", "prompt": "p"}, "X-Fake-Scenario", "forbidden")
	if status != http.StatusForbidden || len(failure) != 1 || failure["message"] == "" {
		t.Fatalf("forbidden = %d %v", status, failure)
	}
}

// PENDING carries the queue fields the official text-to-3d schema documents;
// the dev-test answer committed with the provider holds the same fields
// (finding #203). It is a single observed GET, not a wire sample.
func TestPendingBodyCarriesTheObservedQueueFields(t *testing.T) {
	f := newFixture(t)
	id := create(t, f, map[string]any{"mode": "preview", "prompt": "p"})
	_, _, pending := f.Do("GET", Prefix+"/text-to-3d/"+id, key, nil)
	if pending["status"] != core.StatusPending || pending["progress"].(float64) != 0 {
		t.Fatalf("PENDING = %v", pending)
	}
	if pending["preceding_tasks"].(float64) != 3 {
		t.Fatalf("preceding_tasks = %v", pending["preceding_tasks"])
	}
	if pending["started_at"].(float64) != 0 || pending["finished_at"].(float64) != 0 {
		t.Fatalf("PENDING timestamps = %v %v", pending["started_at"], pending["finished_at"])
	}
	raw, err := os.ReadFile("../../../../providers/meshy/testdata/meshy-text-to-3d-pending.json")
	if err != nil {
		t.Fatal(err)
	}
	var observed map[string]any
	if err := json.Unmarshal(raw, &observed); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"preceding_tasks", "started_at", "finished_at"} {
		if _, has := observed[field]; !has {
			t.Fatalf("the observed pending answer lacks %s", field)
		}
		if _, has := pending[field]; !has {
			t.Errorf("the fake PENDING body lacks %s", field)
		}
	}
}

// A refine with enable_pbr=true must carry the PBR maps the captured default
// offering request received (finding #203; the committed provider sample is
// captured, and the fake's own sample used the cheapest texture options).
func TestPBRRefineMatchesTheCapturedDefaultRequest(t *testing.T) {
	raw, err := os.ReadFile("../../../../providers/meshy/testdata/meshy-text-to-3d.candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	var exchanges []wirefixture.Exchange
	if err := json.Unmarshal(raw, &exchanges); err != nil {
		t.Fatal(err)
	}
	var captured map[string]any
	for _, exchange := range exchanges {
		body, err := fakeupstream.JSONBody(exchange)
		if err != nil || body["type"] != "text-to-3d-refine" || body["status"] != "SUCCEEDED" {
			continue
		}
		entries, _ := body["texture_urls"].([]any)
		if len(entries) == 0 {
			continue
		}
		if entry, _ := entries[0].(map[string]any); entry["metallic"] != nil {
			captured = body
			break
		}
	}
	if captured == nil {
		t.Fatal("the captured sample holds no PBR refine poll")
	}
	f := newFixture(t)
	preview := create(t, f, map[string]any{"mode": "preview", "prompt": "p"})
	finish(t, f, preview)
	formats := []string{"glb", "obj", "fbx", "stl", "usdz", "3mf"}
	refine := create(t, f, map[string]any{
		"mode": "refine", "preview_task_id": preview, "ai_model": "meshy-7.1",
		"enable_pbr": true, "texture_resolution": "8k", "target_formats": formats,
	})
	_, body := finish(t, f, refine)
	shapeOf := func(value any) fakeupstream.Shape {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		shape, err := fakeupstream.ShapeOf(raw)
		if err != nil {
			t.Fatal(err)
		}
		return shape
	}
	textureOf := func(body map[string]any) any {
		entries, _ := body["texture_urls"].([]any)
		if len(entries) == 0 {
			t.Fatalf("texture_urls = %v", body["texture_urls"])
		}
		return entries[0]
	}
	if diff := fakeupstream.DiffShape(shapeOf(textureOf(captured)), shapeOf(textureOf(body))); !diff.Empty() {
		t.Errorf("refine texture_urls drifted from the captured PBR poll: %s", diff)
	}
	wantModels, _ := captured["model_urls"].(map[string]any)
	gotModels, _ := body["model_urls"].(map[string]any)
	if len(wantModels) != len(gotModels) {
		t.Fatalf("model_urls = %v, want the captured keys %v", gotModels, wantModels)
	}
	for key := range wantModels {
		if _, has := gotModels[key]; !has {
			t.Errorf("model_urls lacks %s: %v", key, gotModels)
		}
	}
}

// Every model format the Catalog's default offering requires downloads with
// the CDN content type recorded on the same host (finding #203).
func TestEveryRequestedFormatUsesTheCDNContentType(t *testing.T) {
	f := newFixture(t)
	formats := []string{"glb", "obj", "fbx", "stl", "usdz", "3mf"}
	id := create(t, f, map[string]any{"mode": "preview", "prompt": "p", "target_formats": formats})
	_, body := finish(t, f, id)
	models, _ := body["model_urls"].(map[string]any)
	for _, format := range formats {
		url, _ := models[format].(string)
		if url == "" {
			t.Fatalf("model_urls lacks %s: %v", format, models)
		}
		status, contentType, data := f.Download(url)
		if want := ContentTypes["."+format]; status != http.StatusOK || contentType != want || len(data) == 0 {
			t.Errorf("%s = %d %q (%d bytes), want %q", format, status, contentType, len(data), want)
		}
	}
}

// The documented task_error types are reachable on the v2 object too, and its
// object has no expires_at (finding #203).
func TestTaskErrorSelectorWorksOnTheV2Shape(t *testing.T) {
	f := newFixture(t)
	id := create(t, f, map[string]any{"mode": "preview", "prompt": "p"}, core.TaskErrorHeader, "service_unavailable")
	_, body := finish(t, f, id)
	failure, _ := body["task_error"].(map[string]any)
	if body["status"] != core.StatusFailed || failure["type"] != "service_unavailable" || failure["message"] == "" {
		t.Fatalf("task_error = %v", body)
	}
	if _, has := failure["code"]; has {
		t.Errorf("service_unavailable has no documented code: %v", failure)
	}
	if _, has := body["expires_at"]; has {
		t.Errorf("the v2 object has no expires_at: %v", body)
	}
}

func TestValidationAndSources(t *testing.T) {
	f := newFixture(t)
	const missing = "01a18514-059e-73f7-a0ce-0f88a12c9300"
	for name, tc := range map[string]struct {
		method, path, key string
		body              any
		status            int
	}{
		"no key":          {"GET", "/text-to-3d/" + missing, "", nil, http.StatusUnauthorized},
		"no mode":         {"POST", "/text-to-3d", key, map[string]any{}, http.StatusBadRequest},
		"unknown task":    {"GET", "/text-to-3d/" + missing, key, nil, http.StatusNotFound},
		"malformed id":    {"GET", "/text-to-3d/nope", key, nil, http.StatusBadRequest},
		"unknown preview": {"POST", "/text-to-3d", key, map[string]any{"mode": "refine", "preview_task_id": missing}, http.StatusNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			status, _, body := f.Do(tc.method, Prefix+tc.path, tc.key, tc.body)
			if status != tc.status || len(body) != 1 || body["message"] == "" {
				t.Fatalf("status %d body %v", status, body)
			}
		})
	}
}

func TestFaultsShareTheMeshyEnvelope(t *testing.T) {
	f := newFixture(t)
	status, _, body := f.Do("POST", Prefix+"/text-to-3d", key, map[string]any{"mode": "preview", "prompt": "p"}, "X-Fake-Scenario", "key-quota")
	if status != http.StatusPaymentRequired || body["message"] != "API key credit limit reached" {
		t.Fatalf("%d %v", status, body)
	}
	id := create(t, f, map[string]any{"mode": "preview", "prompt": "p"}, "X-Fake-Scenario", "task-failed")
	if statuses, failed := finish(t, f, id); statuses[len(statuses)-1] != core.StatusFailed || failed["task_error"] == nil {
		t.Fatalf("task-failed: %v %v", statuses, failed)
	}
}

// The fake keeps the structure of the real samples for both task types, for
// the create envelope and the errors.
func TestDriftFakeResponsesMatchTheRealSamples(t *testing.T) {
	samples, err := fakeupstream.LoadSamples(sampleFS, sampleFiles...)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	shapeOf := func(raw []byte) fakeupstream.Shape {
		shape, err := fakeupstream.ShapeOf(raw)
		if err != nil {
			t.Fatal(err)
		}
		return shape
	}
	same := func(what string, real wirefixture.Exchange, got []byte) {
		t.Helper()
		if diff := fakeupstream.DiffShape(shapeOf(real.Response.Body.JSON), shapeOf(got)); !diff.Empty() {
			t.Errorf("%s drifted from the real sample: %s", what, diff)
		}
	}
	pollSample := func(typ, status string) *wirefixture.Exchange {
		for _, exchange := range samples.Exchanges {
			body, err := fakeupstream.JSONBody(exchange)
			if err == nil && exchange.Request.Method == "GET" && body["type"] == typ && body["status"] == status {
				return &exchange
			}
		}
		return nil
	}
	bySample := func(method, apiPath string, status int) wirefixture.Exchange {
		for _, exchange := range samples.Find(method+" "+samplePrefix+apiPath, "") {
			if exchange.Response.Status == status {
				return exchange
			}
		}
		t.Fatalf("no real sample %s %s %d", method, apiPath, status)
		return wirefixture.Exchange{}
	}

	const missing = "01a18514-059e-73f7-a0ce-0f88a12c9300"
	_, _, raw := f.Raw("GET", Prefix+"/text-to-3d/"+missing, key, nil)
	same("task not found", bySample("GET", "/text-to-3d/{id}", 404), raw)
	_, _, raw = f.Raw("GET", Prefix+"/text-to-3d/"+missing, "", nil)
	same("no auth", bySample("GET", "/text-to-3d/{id}", 401), raw)
	_, _, raw = f.Raw("POST", Prefix+"/text-to-3d", key, map[string]any{})
	same("invalid create", bySample("POST", "/text-to-3d", 400), raw)

	preview := ""
	for _, typ := range []string{"text-to-3d-preview", "text-to-3d-refine"} {
		body := map[string]any{"mode": "preview", "prompt": "p"}
		if typ == "text-to-3d-refine" {
			body = map[string]any{"mode": "refine", "preview_task_id": preview}
		}
		status, _, created := f.Raw("POST", Prefix+"/text-to-3d", key, body)
		if status != http.StatusAccepted {
			t.Fatalf("create %s = %d", typ, status)
		}
		var decoded map[string]any
		_ = json.Unmarshal(created, &decoded)
		same("create "+typ, bySample("POST", "/text-to-3d", 202), created)
		id := decoded["result"].(string)
		if typ == "text-to-3d-preview" {
			preview = id
		}
		for _, want := range []string{"PENDING", "IN_PROGRESS", "SUCCEEDED"} {
			_, _, polled := f.Raw("GET", Prefix+"/text-to-3d/"+id, key, nil)
			var state map[string]any
			_ = json.Unmarshal(polled, &state)
			if state["status"] != want || state["type"] != typ {
				t.Fatalf("%s poll = %v %v, want %s", typ, state["type"], state["status"], want)
			}
			if real := pollSample(typ, want); real != nil {
				same(typ+" "+want, *real, polled)
			}
		}
	}
}

// Every endpoint names a committed sample (real) or a documentation URL and
// check date (constructed), and is routed.
func TestEndpointTableIsCompleteAndHonest(t *testing.T) {
	samples, err := fakeupstream.LoadSamples(sampleFS, sampleFiles...)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	for _, endpoint := range endpoints {
		switch endpoint.Evidence {
		case fakeupstream.EvidenceReal:
			if len(samples.Find(endpoint.Sample, "")) == 0 {
				t.Errorf("%s names sample %q, which is not committed", endpoint.Key(), endpoint.Sample)
			}
		case fakeupstream.EvidenceConstructed:
			if !strings.HasPrefix(endpoint.DocURL, "https://") || len(endpoint.CheckedAt) != len("2026-10-29") {
				t.Errorf("%s: constructed endpoints need a documentation URL and a check date", endpoint.Key())
			}
		default:
			t.Errorf("%s has no evidence class", endpoint.Key())
		}
		if strings.Contains(endpoint.Path, "{task_id}") {
			continue
		}
		path := strings.ReplaceAll(endpoint.Path, "{id}", "01a18514-059e-73f7-a0ce-0f88a12c9300")
		if status, _, _ := f.Do(endpoint.Method, Prefix+path, "", nil); status != http.StatusUnauthorized {
			t.Errorf("%s answers %d without a key, want 401", endpoint.Key(), status)
		}
	}
}
