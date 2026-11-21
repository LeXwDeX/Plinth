package mattingapiv1

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// newestBatchPolls reads the 2026-11-20 live-window capture (a batch of one
// uploaded PNG through the production path) and returns its batch bodies keyed
// by batch_status. The runtime recorder wrote them; they are evidence and not a
// template of the fake.
func newestBatchPolls(t *testing.T) map[string]map[string]any {
	t.Helper()
	raw, err := sampleFS.ReadFile("testdata/matting-api-v1-batch-2026-11-20.candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	var exchanges []wirefixture.Exchange
	if err := json.Unmarshal(raw, &exchanges); err != nil {
		t.Fatal(err)
	}
	polls := map[string]map[string]any{}
	for _, exchange := range exchanges {
		if exchange.Request.Method != http.MethodGet {
			continue
		}
		body, err := fakeupstream.JSONBody(exchange)
		if err != nil {
			t.Fatal(err)
		}
		if status, _ := body["batch_status"].(string); status != "" {
			polls[status] = body
		}
	}
	if polls["RUNNING"] == nil || polls["SUCCESS"] == nil {
		t.Fatalf("the capture lacks a RUNNING or SUCCESS batch: %v", polls)
	}
	return polls
}

// The 2026-11-20 window confirmed the batch poll structure the fake answers:
// one task per uploaded file, the counts, the download locator and the
// downloadable flag.
func TestTheFakeMatchesTheNewestBatchCapture(t *testing.T) {
	polls := newestBatchPolls(t)
	f := newFixture(t, fakeupstream.Config{})
	status, created := submit(f, t)
	if status != http.StatusOK {
		t.Fatalf("create = %d %v", status, created)
	}
	batchID, _ := created["batch_id"].(string)
	if batchID == "" {
		t.Fatalf("create body = %v", created)
	}
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
	for _, want := range []string{"RUNNING", "SUCCESS"} {
		var body map[string]any
		for attempt := 0; attempt < 6; attempt++ {
			_, _, body = f.Do("GET", Prefix+"/batches/"+batchID, key, nil)
			if body["batch_status"] == want {
				break
			}
		}
		if body["batch_status"] != want {
			t.Fatalf("batch never reached %s: %v", want, body)
		}
		if diff := fakeupstream.DiffShape(shapeOf(polls[want]), shapeOf(body)); !diff.Empty() {
			t.Errorf("the %s batch drifted from the 2026-11-20 capture: %s", want, diff)
		}
	}
}
