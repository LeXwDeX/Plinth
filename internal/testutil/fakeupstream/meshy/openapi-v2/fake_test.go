package meshyopenapiv2

import (
	"encoding/json"
	"net/http"
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
		if state == core.StatusSucceeded || state == core.StatusFailed {
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
	if _, ok := testkit.Field(t, body, "model_urls").(map[string]any)["mtl"]; !ok {
		t.Fatalf("an OBJ request must come with its material library: %v", body["model_urls"])
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
