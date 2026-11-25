package meshyopenapiv1

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/core"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// #608: the list and stream routes, against the real round of 2026-11-25
// (meshy-openapi-v1-list-stream-2026-11-25.candidate.json).

func listStreamExchange(t *testing.T, method, path string, status int) wirefixture.Exchange {
	t.Helper()
	for _, exchange := range loadSamples(t).Find(method+" "+path, "") {
		if exchange.Response.Status == status && exchange.Metadata.CapturedAt.Format("2006-01-02") == "2026-11-25" {
			return exchange
		}
	}
	t.Fatalf("no %d sample of %s %s", status, method, path)
	return wirefixture.Exchange{}
}

func sampleMessage(t *testing.T, exchange wirefixture.Exchange) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(exchange.Response.Body.JSON, &body); err != nil {
		t.Fatal(err)
	}
	return body["message"].(string)
}

// sseBlocks splits an SSE body into (event, data) pairs; the official stream
// writes "event:" and "data:" with or without a space.
func sseBlocks(t *testing.T, raw string) (events []string, data []map[string]any) {
	t.Helper()
	for _, block := range strings.Split(strings.TrimSpace(raw), "\n\n") {
		var event string
		for _, line := range strings.Split(block, "\n") {
			if value, ok := strings.CutPrefix(line, "event:"); ok {
				event = strings.TrimSpace(value)
			} else if value, ok := strings.CutPrefix(line, "data:"); ok {
				var decoded map[string]any
				if err := json.Unmarshal([]byte(strings.TrimSpace(value)), &decoded); err != nil {
					t.Fatalf("data %q: %v", value, err)
				}
				events, data = append(events, event), append(data, decoded)
			}
		}
	}
	return events, data
}

func TestListAnswersTheTaskObjectsAsTheRealListDoes(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	var ids []string
	for i := 0; i < 3; i++ {
		ids = append(ids, f.create("/image-to-3d", map[string]any{"image_url": "https://example.test/a.png"}))
	}
	f.finish("/image-to-3d", ids[0], key)

	status, _, raw := f.Raw("GET", f.v1("/image-to-3d"), key, nil)
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); status != http.StatusOK || err != nil {
		t.Fatalf("list = %d %s (%v); want a bare JSON array", status, raw, err)
	}
	// Newest first by default.
	if len(items) != 3 || items[0]["id"] != ids[2] || items[2]["id"] != ids[0] {
		t.Fatalf("default order: %v", items)
	}
	// A list item is the task object a GET answers for the same state, and
	// listing does not advance the task.
	_, _, got := f.Do("GET", f.v1("/image-to-3d/"+ids[0]), key, nil)
	if !reflect.DeepEqual(shapeOfBody(t, items[2]), shapeOfBody(t, got)) || got["status"] != core.StatusSucceeded {
		t.Errorf("list item and GET body differ:\n%v\n%v", items[2], got)
	}
	// The real list item has the keys of the real GET body of an image-to-3d task.
	real := listStreamExchange(t, "GET", "/openapi/v1/image-to-3d", http.StatusOK)
	var realItems []map[string]any
	if err := json.Unmarshal(real.Response.Body.JSON, &realItems); err != nil || len(realItems) != 1 {
		t.Fatalf("sample list: %v %v", realItems, err)
	}
	if realItems[0]["type"] != "image-to-3d" {
		t.Errorf("sample list item type %v", realItems[0]["type"])
	}

	for _, check := range []struct {
		query string
		want  []string
	}{
		{"?sort_by=%2Bcreated_at", ids},
		{"?page_size=1&page_num=2", ids[1:2]},
		{"?page_size=0", []string{ids[2], ids[1], ids[0]}},
		{"?page_num=0&page_size=1", ids[2:3]},
		{"?page_size=101", []string{ids[2], ids[1], ids[0]}},
		{"?page_num=255", nil},
	} {
		status, _, raw := f.Raw("GET", f.v1("/image-to-3d"+check.query), key, nil)
		var page []map[string]any
		if err := json.Unmarshal(raw, &page); status != http.StatusOK || err != nil || page == nil {
			t.Fatalf("%s = %d %s", check.query, status, raw)
		}
		var got []string
		for _, item := range page {
			got = append(got, item["id"].(string))
		}
		if !reflect.DeepEqual(got, check.want) {
			t.Errorf("%s ids %v, want %v", check.query, got, check.want)
		}
	}
}

func TestListRefusalsUseTheRealTexts(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	pageNum := sampleMessage(t, listStreamExchange(t, "GET", "/openapi/v1/image-to-3d", http.StatusBadRequest))
	for _, check := range []struct {
		query, want string
		status      int
	}{
		{"?page_num=256", "page_num must be an integer between 1 and 255", 400},
		{"?page_size=-1", "page_size must be an integer between 1 and 100", 400},
		{"?page_size=abc", "page_size must be an integer between 1 and 100", 400},
		{"?sort_by=created", `invalid column: "reated"`, 400},
		{"?sort_by=bogus", `invalid column: "ogus"`, 400},
	} {
		status, _, body := f.Do("GET", f.v1("/image-to-3d"+check.query), key, nil)
		if status != check.status || body["message"] != check.want {
			t.Errorf("%s = %d %v, want %d %q", check.query, status, body, check.status, check.want)
		}
	}
	// The sampled refusals (page_num 256 and sort_by "created") say the same.
	samples := loadSamples(t)
	var texts []string
	for _, exchange := range samples.Find("GET /openapi/v1/image-to-3d", "") {
		if exchange.Response.Status == http.StatusBadRequest {
			texts = append(texts, sampleMessage(t, exchange))
		}
	}
	if !strings.Contains(strings.Join(texts, "|"), "page_num must be an integer between 1 and 255") ||
		!strings.Contains(strings.Join(texts, "|"), `invalid column: "reated"`) || pageNum == "" {
		t.Errorf("sampled refusals: %v", texts)
	}
	if status, _, body := f.Do("GET", f.v1("/rigging"), "", nil); status != http.StatusUnauthorized || body["message"] != "Missing API key" {
		t.Errorf("list without a key = %d %v", status, body)
	}
}

func TestStreamOfAFinishedTaskIsOneMessageBlock(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	id := f.create("/image-to-3d", map[string]any{"image_url": "https://example.test/a.png"})
	_, final := f.finish("/image-to-3d", id, key)

	status, header, raw := f.Raw("GET", f.v1("/image-to-3d/"+id+"/stream"), key, nil)
	if status != http.StatusOK || header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream = %d %q", status, header.Get("Content-Type"))
	}
	if !strings.HasPrefix(string(raw), "event:message\ndata:{") {
		t.Errorf("blocks are written without a space after the colon: %q", string(raw)[:30])
	}
	events, data := sseBlocks(t, string(raw))
	if !reflect.DeepEqual(events, []string{"message"}) || data[0]["status"] != core.StatusSucceeded {
		t.Fatalf("stream of a finished task: %v %v", events, data)
	}
	if !reflect.DeepEqual(shapeOfBody(t, data[0]), shapeOfBody(t, final)) {
		t.Errorf("message data and GET body differ")
	}
	// So did the real stream of a finished task.
	sample := listStreamExchange(t, "GET", "/openapi/v1/image-to-3d/{id}/stream", http.StatusOK)
	realEvents, realData := sseBlocks(t, sample.Response.Body.SSE)
	if !reflect.DeepEqual(realEvents, []string{"message"}) || realData[0]["status"] != core.StatusSucceeded {
		t.Errorf("sampled stream: %v", realEvents)
	}
}

func TestStreamOfARunningTaskEndsWithTheFinalState(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{QueuedPolls: 1, RunningPolls: 2})
	id := f.create("/image-to-3d", map[string]any{"image_url": "https://example.test/a.png"})
	_, _, raw := f.Raw("GET", f.v1("/image-to-3d/"+id+"/stream"), key, nil)
	events, data := sseBlocks(t, string(raw))
	var states []string
	for _, block := range data {
		states = append(states, block["status"].(string))
	}
	want := []string{core.StatusPending, core.StatusInProgress, core.StatusInProgress, core.StatusSucceeded}
	if !reflect.DeepEqual(states, want) || len(events) != len(want) {
		t.Errorf("states %v, want %v", states, want)
	}
}

func TestStreamErrorsAreEventsAsTheRealStreamSendsThem(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	samples := loadSamples(t)
	for _, check := range []struct {
		path, contentType string
	}{
		{"/image-to-3d/" + missingTask + "/stream", "text/event-stream"},
		{"/image-to-3d/not-an-id/stream", "text/event-stream;charset=utf-8"},
	} {
		status, header, raw := f.Raw("GET", f.v1(check.path), key, nil)
		events, data := sseBlocks(t, string(raw))
		if status != http.StatusOK || header.Get("Content-Type") != check.contentType || len(events) != 1 || events[0] != "error" {
			t.Fatalf("%s = %d %q %v", check.path, status, header.Get("Content-Type"), events)
		}
		// The sample of the same request carries the same block.
		var real []map[string]any
		for _, exchange := range samples.Find(fakeupstream.SampleKey("GET", "/openapi/v1"+check.path), "") {
			if _, blocks := sseBlocks(t, exchange.Response.Body.SSE); blocks[0]["message"] == data[0]["message"] {
				real = blocks
			}
		}
		if real == nil {
			t.Fatalf("%s: no sample with the message %v", check.path, data[0]["message"])
		}
		if !reflect.DeepEqual(shapeOfBody(t, real[0]), shapeOfBody(t, data[0])) ||
			real[0]["message"] != data[0]["message"] || real[0]["status_code"] != data[0]["status_code"] {
			t.Errorf("%s: fake %v, real %v", check.path, data[0], real[0])
		}
	}
	if status, _, body := f.Do("GET", f.v1("/image-to-3d/"+missingTask+"/stream"), "", nil); status != http.StatusUnauthorized || body["message"] != "Missing API key" {
		t.Errorf("stream without a key = %d %v", status, body)
	}
}
