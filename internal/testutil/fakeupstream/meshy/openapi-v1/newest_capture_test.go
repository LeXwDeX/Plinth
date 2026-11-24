package meshyopenapiv1

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/core"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// newestMultiImagePolls reads the 2026-11-23 live window's Multi Image to 3D
// capture (one task, 46 polls) and returns the newest poll body per status.
// The runtime recorder wrote them; they are the newest sample of the kind, so
// they are the fake's template.
func newestMultiImagePolls(t *testing.T) map[string]map[string]any {
	t.Helper()
	raw, err := sampleFS.ReadFile("testdata/meshy-openapi-v1-multi-image-to-3d-2026-11-23.candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	var exchanges []wirefixture.Exchange
	if err := json.Unmarshal(raw, &exchanges); err != nil {
		t.Fatal(err)
	}
	polls, held := map[string]map[string]any{}, map[string]wirefixture.Exchange{}
	for _, exchange := range exchanges {
		if exchange.Request.Method != http.MethodGet {
			continue
		}
		body, err := fakeupstream.JSONBody(exchange)
		if err != nil {
			t.Fatal(err)
		}
		status, _ := body["status"].(string)
		if status == "" {
			continue
		}
		if current, has := held[status]; !has || exchange.Metadata.CapturedAt.After(current.Metadata.CapturedAt) {
			held[status] = exchange
			polls[status] = body
		}
	}
	if polls[core.StatusInProgress] == nil || polls[core.StatusSucceeded] == nil {
		t.Fatalf("the capture lacks a running or successful poll: %v", polls)
	}
	return polls
}

// The 2026-11-23 capture is the newest Multi Image to 3D sample, and every one
// of its polls repeats geometry_resolution "2k" and ultra_mode: the task was
// created with the Catalog's 2k geometry. The fake templates from that sample,
// so a request that names no geometry option still answers both keys, while a
// request that names one keeps answering what it asked for (#497).
func TestNewestCaptureDecidesTheMultiImageGeometryEcho(t *testing.T) {
	polls := newestMultiImagePolls(t)
	sample := polls[core.StatusInProgress]
	for _, field := range []string{"geometry_resolution", "ultra_mode"} {
		if _, has := sample[field]; !has {
			t.Fatalf("the newest capture has no %s: %v", field, sample)
		}
	}

	f := newFixture(t, fakeupstream.Config{})
	id := f.create("/multi-image-to-3d", map[string]any{"image_urls": []string{"https://example.test/a.png"}})
	var running map[string]any
	for attempt := 0; attempt < 8 && running == nil; attempt++ {
		_, _, body := f.Do("GET", f.v1("/multi-image-to-3d/"+id), key, nil)
		if body["status"] == core.StatusInProgress {
			running = body
		}
	}
	if running == nil {
		t.Fatal("the task never reported IN_PROGRESS")
	}
	for _, field := range []string{"geometry_resolution", "ultra_mode"} {
		if running[field] != sample[field] {
			t.Errorf("%s = %v, the newest capture answers %v", field, running[field], sample[field])
		}
	}

	low := f.create("/multi-image-to-3d", map[string]any{
		"image_urls": []string{"https://example.test/a.png"}, "geometry_resolution": "1k",
	})
	_, body := f.finish("/multi-image-to-3d", low, key)
	if body["geometry_resolution"] != "1k" || body["ultra_mode"] != false {
		t.Errorf("a 1k request answers %v %v", body["geometry_resolution"], body["ultra_mode"])
	}
}
