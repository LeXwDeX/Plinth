package mattingapiv1

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// newestBatchPolls reads the newest committed live-window capture (a batch of
// one uploaded PNG through the production path, 2026-11-20 and re-captured
// 2026-11-23) and returns its batch bodies keyed by batch_status. The runtime
// recorder wrote them; they are evidence and not a template of the fake.
func newestBatchPolls(t *testing.T) map[string]map[string]any {
	t.Helper()
	names, err := fs.Glob(sampleFS, "testdata/matting-api-v1-batch-*.candidate.json")
	if err != nil || len(names) == 0 {
		t.Fatalf("no committed batch capture: %v", err)
	}
	polls, held := map[string]map[string]any{}, map[string]time.Time{}
	for _, name := range names {
		raw, err := sampleFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var exchanges []wirefixture.Exchange
		if err := json.Unmarshal(raw, &exchanges); err != nil {
			t.Fatal(err)
		}
		for _, exchange := range exchanges {
			if exchange.Request.Method != http.MethodGet ||
				exchange.Response.Body.Encoding != wirefixture.PayloadJSON {
				continue // the artifact download of the same batch
			}
			body, err := fakeupstream.JSONBody(exchange)
			if err != nil {
				t.Fatal(err)
			}
			status, _ := body["batch_status"].(string)
			if status == "" {
				continue
			}
			if at, has := held[status]; !has || exchange.Metadata.CapturedAt.After(at) {
				held[status] = exchange.Metadata.CapturedAt
				polls[status] = body
			}
		}
	}
	if polls["RUNNING"] == nil || polls["SUCCESS"] == nil {
		t.Fatalf("the capture lacks a RUNNING or SUCCESS batch: %v", polls)
	}
	return polls
}

// The newest window confirmed the batch poll structure the fake answers: one
// task per uploaded file, the counts, the download locator and the downloadable
// flag.
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
			t.Errorf("the %s batch drifted from the newest batch capture: %s", want, diff)
		}
	}
}
