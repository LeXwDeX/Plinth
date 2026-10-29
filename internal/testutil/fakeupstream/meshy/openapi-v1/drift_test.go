package meshyopenapiv1

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

func loadSamples(t *testing.T) *fakeupstream.Samples {
	t.Helper()
	samples, err := fakeupstream.LoadSamples(sampleFS, sampleFiles...)
	if err != nil {
		t.Fatal(err)
	}
	return samples
}

func shapeOfBody(t *testing.T, body any) fakeupstream.Shape {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	shape, err := fakeupstream.ShapeOf(raw)
	if err != nil {
		t.Fatal(err)
	}
	return shape
}

func sampleShape(t *testing.T, exchange wirefixture.Exchange) fakeupstream.Shape {
	t.Helper()
	shape, err := fakeupstream.ShapeOf(exchange.Response.Body.JSON)
	if err != nil {
		t.Fatal(err)
	}
	return shape
}

// withoutOmitted drops the paths of keys the samples cannot hold; the fake adds
// them back by hand.
func withoutOmitted(shape fakeupstream.Shape) fakeupstream.Shape {
	out := fakeupstream.Shape{}
	for path, types := range shape {
		omitted := false
		for _, key := range OmittedKeys {
			omitted = omitted || strings.HasSuffix(path, "."+key) || strings.Contains(path, "."+key+".")
		}
		if !omitted {
			out[path] = types
		}
	}
	return out
}

func assertSameShape(t *testing.T, what string, want, got fakeupstream.Shape) {
	t.Helper()
	if diff := fakeupstream.DiffShape(want, withoutOmitted(got)); !diff.Empty() {
		t.Errorf("%s drifted from the real sample: %s", what, diff)
	}
}

// find returns the first exchange of method on apiPath (a sample key path)
// with status; poll samples are filtered by task type and status.
func find(samples *fakeupstream.Samples, method, apiPath string, status int) *wirefixture.Exchange {
	for _, exchange := range samples.Find(method+" "+samplePrefix+apiPath, "") {
		if exchange.Response.Status == status {
			return &exchange
		}
	}
	return nil
}

func realPoll(samples *fakeupstream.Samples, typ, status string) *wirefixture.Exchange {
	for _, exchange := range samples.Exchanges {
		if exchange.Request.Method != "GET" || exchange.Response.Status != http.StatusOK ||
			exchange.Response.Body.Encoding != wirefixture.PayloadJSON {
			continue
		}
		body, err := fakeupstream.JSONBody(exchange)
		if err == nil && body["type"] == typ && body["status"] == status {
			return &exchange
		}
	}
	return nil
}

func kindRequests(source, rig string) map[string]map[string]any {
	return map[string]map[string]any{
		"/text-to-image":     {"prompt": "p"},
		"/image-to-image":    {"prompt": "p", "reference_image_urls": []string{"https://example.test/a.png"}},
		"/image-to-3d":       {"image_url": "https://example.test/a.png"},
		"/multi-image-to-3d": {"image_urls": []string{"https://example.test/a.png"}},
		"/remesh":            {"input_task_id": source},
		"/convert":           {"input_task_id": source, "target_formats": []string{"glb", "stl"}},
		"/retexture":         {"input_task_id": source, "text_style_prompt": "p"},
		"/rigging":           {"input_task_id": source},
		"/animations":        {"rig_task_id": rig, "action_id": 0},
		"/uv-unwrap":         {"input_task_id": source},
		"/resize":            {"input_task_id": source, "resize_height": 1.7},
		"/print/analyze":     {"input_task_id": source},
		"/print/multi-color": {"input_task_id": source},
		"/text-to-motion":    {"prompt": "p", "duration": 2},
	}
}

// The fake's response structure must stay identical to the real samples of this
// version for every registered endpoint (issue #96). A new sample (re-captured
// with the alignment tool) changes the expectation; the fake follows because it
// is built from the sample.
func TestDriftFakeResponsesMatchTheRealSamples(t *testing.T) {
	samples := loadSamples(t)
	f := newFixture(t, fakeupstream.Config{})
	const missing = "01a18514-059e-73f7-a0ce-0f88a12c9300"

	for _, tc := range []struct {
		name, method, path, key string
		body                    any
		status                  int
	}{
		{"balance", "GET", "/balance", key, nil, 200},
		{"no auth", "GET", "/balance", "", nil, 401},
		{"task not found", "GET", "/image-to-3d/" + missing, key, nil, 404},
		{"bad id", "GET", "/image-to-3d/not-an-id", key, nil, 400},
		{"invalid create", "POST", "/image-to-3d", key, map[string]any{}, 400},
		{"invalid remesh", "POST", "/remesh", key, map[string]any{}, 400},
	} {
		sample := find(samples, tc.method, strings.SplitN(tc.path, "/"+missing, 2)[0], tc.status)
		switch tc.name {
		case "task not found":
			sample = find(samples, tc.method, "/image-to-3d/{id}", tc.status)
		case "bad id":
			sample = find(samples, tc.method, "/image-to-3d/not-an-id", tc.status)
		}
		if sample == nil {
			t.Fatalf("%s: no real sample with status %d", tc.name, tc.status)
		}
		status, _, body := f.Do(tc.method, f.v1(tc.path), tc.key, tc.body)
		if status != tc.status {
			t.Fatalf("%s: status %d, sample %d", tc.name, status, tc.status)
		}
		assertSameShape(t, tc.name, sampleShape(t, *sample), shapeOfBody(t, body))
	}

	source := f.source()
	rig := f.create("/rigging", map[string]any{"input_task_id": source})
	f.finish("/rigging", rig, key)
	for _, kind := range kinds {
		request := kindRequests(source, rig)[kind.Path]
		// Task creation answers the sample's create envelope.
		sample := find(samples, "POST", kind.Path, http.StatusAccepted)
		if sample == nil {
			t.Fatalf("kind %s has no create sample", kind.Path)
		}
		status, _, created := f.Do("POST", f.v1(kind.Path), key, request)
		if status != http.StatusAccepted {
			t.Fatalf("%s: %d %v", kind.Path, status, created)
		}
		assertSameShape(t, "create "+kind.Path, sampleShape(t, *sample), shapeOfBody(t, created))
		// Every status the sample has for this task type keeps its structure.
		id := created["result"].(string)
		for _, want := range []string{"PENDING", "IN_PROGRESS", "SUCCEEDED"} {
			_, _, response := f.Do("GET", f.v1(kind.Path+"/"+id), key, nil)
			if response["status"] != want {
				t.Fatalf("%s poll returned %v, want %s", kind.Type, response["status"], want)
			}
			real := realPoll(samples, kind.Type, want)
			if real == nil {
				continue // no real sample of this type and status: nothing to compare
			}
			assertSameShape(t, kind.Type+" "+want, sampleShape(t, *real), shapeOfBody(t, response))
		}
		// Delete answers like the samples (an empty 200).
		if sample := find(samples, "DELETE", kind.Path+"/{id}", http.StatusOK); sample == nil {
			t.Errorf("kind %s has no delete sample", kind.Path)
		}
		if status, _, _ := f.Do("DELETE", f.v1(kind.Path+"/"+id), key, nil); status != http.StatusOK {
			t.Errorf("delete %s = %d", kind.Path, status)
		}
	}

	// The failed poll: an oversized rigging input, the one failure observed.
	big := f.create("/remesh", map[string]any{"input_task_id": source, "topology": "quad", "target_polycount": 300000})
	f.finish("/remesh", big, key)
	failedRig := f.create("/rigging", map[string]any{"input_task_id": big})
	_, failed := f.finish("/rigging", failedRig, key)
	real := realPoll(samples, "rig", "FAILED")
	if real == nil {
		t.Fatal("no real failed rigging sample")
	}
	assertSameShape(t, "failed rigging poll", sampleShape(t, *real), shapeOfBody(t, failed))
}

// Every endpoint the version declares names a committed sample (real) or
// carries the documentation URL and check date (constructed).
func TestEndpointTableIsCompleteAndHonest(t *testing.T) {
	samples := loadSamples(t)
	f := newFixture(t, fakeupstream.Config{})
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
		// The route exists: an unauthenticated request is refused (401), not
		// unrouted (404).
		path := strings.ReplaceAll(endpoint.Path, "{id}", "01a18514-059e-73f7-a0ce-0f88a12c9300")
		if status, _, _ := f.Do(endpoint.Method, f.v1(path), "", nil); status != http.StatusUnauthorized {
			t.Errorf("%s answers %d without a key, want 401", endpoint.Key(), status)
		}
	}
	// No kind is served that the table does not declare.
	if want := 2 + 3*len(kinds); len(endpoints) != want {
		t.Errorf("endpoint table has %d rows, want %d", len(endpoints), want)
	}
}
