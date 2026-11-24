package meshyopenapiv2

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// #568: the meshy-t2.5 Text to 3D of the 1.9 real round (2026-11-24): a Smart
// Topology preview of 15000 faces (5 credits) and its 2K PBR refine (10
// credits, the t2.5 refine price the Pricing page leaves open). Neither object
// repeats a resolution key: the preview names no geometry_resolution and a 2K
// texture is not repeated. The fake replays the recorded create bodies.
func TestRealRoundT25PreviewAndRefine(t *testing.T) {
	raw, err := sampleFS.ReadFile("testdata/meshy-openapi-v2-real-round-2026-11-24.candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	var exchanges []wirefixture.Exchange
	if err := json.Unmarshal(raw, &exchanges); err != nil {
		t.Fatal(err)
	}
	creates, succeeded := map[string]map[string]any{}, map[string]map[string]any{}
	for _, exchange := range exchanges {
		body, err := fakeupstream.JSONBody(exchange)
		if err != nil {
			t.Fatal(err)
		}
		typ, _ := body["type"].(string)
		switch {
		case exchange.Request.Method == http.MethodPost:
			var request map[string]any
			if err := json.Unmarshal(exchange.Request.Body.JSON, &request); err != nil {
				t.Fatal(err)
			}
			creates[typ] = request
		case body["status"] == "SUCCEEDED":
			succeeded[typ] = body
		}
	}

	f := newFixture(t)
	preview := creates["text-to-3d-preview"]
	preview["prompt"] = "p"
	previewID := create(t, f, preview)
	_, previewBody := finish(t, f, previewID)
	refine := creates["text-to-3d-refine"]
	refine["prompt"], refine["preview_task_id"] = "p", previewID
	_, refineBody := finish(t, f, create(t, f, refine))

	for typ, got := range map[string]map[string]any{"text-to-3d-preview": previewBody, "text-to-3d-refine": refineBody} {
		want := succeeded[typ]
		if want == nil {
			t.Fatalf("the sample has no succeeded %s poll", typ)
		}
		// The sample decodes numbers as json.Number and the fixture as float64.
		if fmt.Sprint(got["consumed_credits"]) != fmt.Sprint(want["consumed_credits"]) {
			t.Errorf("%s consumed_credits = %v, the real round answered %v", typ, got["consumed_credits"], want["consumed_credits"])
		}
		for _, field := range []string{"geometry_resolution", "ultra_mode", "texture_resolution", "hd_texture"} {
			_, wantHas := want[field]
			if _, has := got[field]; has != wantHas {
				t.Errorf("%s carries %s = %v, the real round: present %v", typ, field, has, wantHas)
			}
		}
	}
}
