package tripov3

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
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

func assertSameShape(t *testing.T, what string, want, got fakeupstream.Shape) {
	t.Helper()
	if diff := fakeupstream.DiffShape(want, got); !diff.Empty() {
		t.Errorf("%s drifted from the real sample: %s", what, diff)
	}
}

// The fake's response structure must stay identical to the latest real sample
// of this version for every registered endpoint (issue #96). A new sample
// (re-captured with the alignment tool) changes the expectation; the fake
// follows because it is built from the sample.
func TestDriftFakeResponsesMatchTheRealSamples(t *testing.T) {
	samples := loadSamples(t)
	f := newFixture(t, fakeupstream.Config{})

	// Fixed-request endpoints.
	for _, tc := range []struct {
		name, method, path, key string
		body                    any
		sampleKey               string
		status                  int
	}{
		{"balance", "GET", "/account/balance", key, nil, "GET /v3/account/balance", 200},
		{"no auth", "GET", "/account/balance", "", nil, "GET /v3/account/balance", 401},
		{"task not found", "GET", "/tasks/00000000-0000-0000-0000-000000000000", key, nil, "GET /v3/tasks/{id}", 404},
		{"invalid create", "POST", "/generation/text-to-model", key, map[string]any{}, "POST /v3/generation/text-to-model", 400},
	} {
		var sample *wirefixture.Exchange
		for _, exchange := range samples.Find(tc.sampleKey, "") {
			if exchange.Response.Status == tc.status {
				sample = &exchange
				break
			}
		}
		if sample == nil {
			t.Fatalf("%s: no real sample with status %d", tc.name, tc.status)
		}
		status, _, body := f.do(tc.method, tc.path, tc.key, tc.body)
		if status != tc.status {
			t.Fatalf("%s: status %d, sample %d", tc.name, status, tc.status)
		}
		assertSameShape(t, tc.name, sampleShape(t, *sample), shapeOfBody(t, body))
	}

	// Task creation: every real kind answers with the sample's create envelope.
	for _, k := range kinds {
		if !k.Real {
			continue
		}
		var sample *wirefixture.Exchange
		for _, exchange := range samples.Find("POST /v3"+k.Path, "") {
			if exchange.Response.Status == http.StatusOK {
				sample = &exchange
				break
			}
		}
		if sample == nil {
			t.Fatalf("real kind %s has no create sample", k.Path)
		}
		parent := f.create("/generation/text-to-model", textBody(), key)
		f.finish(parent, key)
		body := map[string]any{"model": "v3.0-20260909", "prompt": "p", "input": parent, "animation": "preset:walk", "format": "STL", "seg_type": "image",
			"prompts": []map[string]any{{"view": "front", "prompt": "p"}}}
		if k.Path == "/animations/retarget" {
			rig := f.create("/animations/rig", map[string]any{"input": parent, "model": "v3.0-20260909"}, key)
			f.finish(rig, key)
			body["input"] = rig
		}
		if k.Path == "/models/convert" {
			body["input"] = "https://example.test/model.glb"
		}
		status, _, response := f.do("POST", k.Path, key, body)
		if status != http.StatusOK {
			t.Fatalf("%s: %d %v", k.Path, status, response)
		}
		assertSameShape(t, "create "+k.Path, sampleShape(t, *sample), shapeOfBody(t, response))
	}

	// Task polls: for every real kind and every status that has a real sample.
	for _, k := range kinds {
		if !k.Real {
			continue
		}
		parent := f.create("/generation/text-to-model", textBody(), key)
		f.finish(parent, key)
		body := map[string]any{"model": "v3.0-20260909", "prompt": "p", "input": parent, "animation": "preset:walk", "format": "STL", "seg_type": "image",
			"prompts": []map[string]any{{"view": "front", "prompt": "p"}}}
		if k.Path == "/animations/retarget" {
			rig := f.create("/animations/rig", map[string]any{"input": parent, "model": "v3.0-20260909"}, key)
			f.finish(rig, key)
			body["input"] = rig
		}
		if k.Path == "/models/convert" {
			body["input"] = "https://example.test/model.glb"
		}
		id := f.create(k.Path, body, key)
		order := []string{statusQueued, statusRunning, statusSuccess}
		for _, status := range order {
			_, _, response := f.do("GET", "/tasks/"+id, key, nil)
			if field(t, response, "data", "status") != status {
				t.Fatalf("%s: poll returned %v, want %s", k.Type, field(t, response, "data", "status"), status)
			}
			real := realPoll(samples, k.Type, status)
			if real == nil {
				continue // no real sample of this kind and status: nothing to compare
			}
			assertSameShape(t, k.Type+" "+status, sampleShape(t, *real), shapeOfBody(t, response))
		}
	}
	// The failed poll: a retarget of a v2 rig.
	parent := f.create("/generation/text-to-model", textBody(), key)
	rig := f.create("/animations/rig", map[string]any{"input": parent, "model": "v2.5-20260210"}, key)
	f.finish(rig, key)
	retarget := f.create("/animations/retarget", map[string]any{"input": rig, "animation": "preset:walk"}, key)
	_, failed := f.finish(retarget, key)
	real := realPoll(samples, "animate_retarget", statusFailed)
	if real == nil {
		t.Fatal("no real failed poll sample")
	}
	assertSameShape(t, "failed poll", sampleShape(t, *real), shapeOfBody(t, failed))
}

// Smart Segmentation reports smartsegment_model for seg_type model and failed
// once on a plain picture (both captured): the fake keeps both structures.
func TestSmartSegmentationTypesAndFailureMatchTheRealSamples(t *testing.T) {
	samples := loadSamples(t)
	f := newFixture(t, fakeupstream.Config{})
	model := f.create("/mesh/smartsegment", map[string]any{"input": "file_x", "seg_type": "model"}, key)
	_, done := f.finish(model, key)
	if field(t, done, "data", "type") != "smartsegment_model" {
		t.Fatalf("type = %v", field(t, done, "data", "type"))
	}
	real := realPoll(samples, "smartsegment_model", statusSuccess)
	if real == nil {
		t.Fatal("no real smartsegment_model success poll")
	}
	assertSameShape(t, "smartsegment_model success", sampleShape(t, *real), shapeOfBody(t, done))

	image := f.create("/mesh/smartsegment", map[string]any{"input": "file_x", "seg_type": "image"}, "fake-task-failed")
	_, failed := f.finish(image, "fake-task-failed")
	real = realPoll(samples, "smartsegment_image", statusFailed)
	if real == nil {
		t.Fatal("no real smartsegment_image failed poll")
	}
	assertSameShape(t, "smartsegment_image failed", sampleShape(t, *real), shapeOfBody(t, failed))
}

func realPoll(samples *fakeupstream.Samples, typ, status string) *wirefixture.Exchange {
	for _, exchange := range samples.Find("GET /v3/tasks/{id}", "") {
		body, err := fakeupstream.JSONBody(exchange)
		if err != nil {
			continue
		}
		data, _ := fakeupstream.DigMap(body, "data")
		if data["type"] == typ && data["status"] == status {
			return &exchange
		}
	}
	return nil
}

// Every endpoint the version declares is served, real ones name a sample that
// exists, constructed ones carry the documentation URL and check date, and no
// route exists outside the table.
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
		if endpoint.Path == "/artifacts/{task_id}/{name}" {
			continue
		}
		path := strings.NewReplacer("{task_id}", "00000000-0000-0000-0000-000000000000").Replace(endpoint.Path)
		status, _, _ := f.do(endpoint.Method, path, key, map[string]any{})
		if status == http.StatusNotFound && endpoint.Method == "POST" || status == http.StatusMethodNotAllowed {
			t.Errorf("declared endpoint %s is not routed (%d)", endpoint.Key(), status)
		}
	}
	// Every create route has an endpoint row.
	declared := map[string]bool{}
	for _, endpoint := range endpoints {
		declared[endpoint.Path] = true
	}
	for _, k := range kinds {
		if !declared[k.Path] {
			t.Errorf("route %s is missing from the endpoint table", k.Path)
		}
	}
}

// The committed alignment report must show the version aligned with the real
// API: no unexplained difference, and real requests behind it.
func TestAlignmentReportIsCommittedAndClean(t *testing.T) {
	raw, err := os.ReadFile("testdata/alignment-report.json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Official  string `json:"official_base"`
		CheckedAt string `json:"checked_at"`
		Requests  int    `json:"official_requests"`
		Paid      int    `json:"official_paid_requests"`
		Diffs     []struct {
			Note string `json:"note"`
		} `json:"diffs"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.Official != OfficialBase || report.CheckedAt == "" || report.Requests == 0 || report.Paid == 0 {
		t.Fatalf("report header incomplete: %+v", report)
	}
	for _, diff := range report.Diffs {
		if strings.TrimSpace(diff.Note) == "" {
			t.Fatalf("report holds an unexplained difference")
		}
	}
}

// The second official run (#89: the seven endpoints that had no real sample) has
// its own report, because the first run's raw exchanges were not kept to merge
// into one: it must be clean as well, with its paid requests counted.
func TestSecondAlignmentReportIsCommittedAndClean(t *testing.T) {
	testkit.AssertAlignmentReport(t, "testdata/alignment-report-89.json", OfficialBase, true)
}
