package meshyopenapiv1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/core"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// #568: the 1.9 real round on dev-test (2026-11-24) ran every Meshy function once
// through Plinth. The fake already matched it on structure, prices, formats and
// the CDN content types; it differed on which resolution keys a task object
// repeats. These tests hold the fake to the committed sample of that round.

// resolutionKeys are the keys a task object repeats only for some requests.
var resolutionKeys = []string{"geometry_resolution", "ultra_mode", "texture_resolution", "hd_texture"}

// realRoundSucceeded returns the SUCCEEDED poll of each task type in the 2026-11-24
// sample.
func realRoundSucceeded(t *testing.T) map[string]map[string]any {
	t.Helper()
	raw, err := sampleFS.ReadFile("testdata/meshy-openapi-v1-real-round-2026-11-24.candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	var exchanges []wirefixture.Exchange
	if err := json.Unmarshal(raw, &exchanges); err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	for _, exchange := range exchanges {
		if exchange.Request.Method != http.MethodGet {
			continue
		}
		body, err := fakeupstream.JSONBody(exchange)
		if err != nil {
			t.Fatal(err)
		}
		if typ, _ := body["type"].(string); body["status"] == core.StatusSucceeded {
			out[typ] = body
		}
	}
	return out
}

// assertSameResolutionKeysAndPrice compares the keys of resolutionKeys and
// consumed_credits of a fake task object with the real one.
func assertSameResolutionKeysAndPrice(t *testing.T, what string, real, fake map[string]any) {
	t.Helper()
	for _, key := range resolutionKeys {
		want, wantHas := real[key]
		got, gotHas := fake[key]
		if wantHas != gotHas || want != got {
			t.Errorf("%s %s = %v (present %v), the real round answered %v (present %v)", what, key, got, gotHas, want, wantHas)
		}
	}
	// The sample decodes numbers as json.Number and the fixture as float64.
	if fmt.Sprint(real["consumed_credits"]) != fmt.Sprint(fake["consumed_credits"]) {
		t.Errorf("%s consumed_credits = %v, the real round answered %v", what, fake["consumed_credits"], real["consumed_credits"])
	}
}

// The three tasks of the round whose objects decide the resolution echo. The
// create bodies of Image to 3D and Multi Image to 3D were over the recorder's
// 1 MiB limit (inlined pictures) and were not kept; their requests here are the
// L5 script's steps as the Catalog sends them (t25_image: meshy-t2.5, 15000
// faces, 2K PBR texture; multiview: meshy-7.1, 2k geometry, 8K PBR texture).
// The retexture is the recorded body.
func TestRealRoundResolutionEchoAndPrices(t *testing.T) {
	real := realRoundSucceeded(t)
	f := newFixture(t, fakeupstream.Config{})
	six := []string{"glb", "obj", "fbx", "stl", "usdz", "3mf"}
	source := f.source()
	for _, tc := range []struct {
		path, typ string
		request   map[string]any
	}{
		// meshy-t2.5 takes no geometry_resolution and a 2K texture is not
		// repeated: the object carries none of the four keys (15 credits).
		{"/image-to-3d", "image-to-3d", map[string]any{
			"image_url": "https://example.test/a.png", "ai_model": "meshy-t2.5", "target_polycount": 15000,
			"texture_resolution": "2k", "enable_pbr": true, "should_texture": true, "target_formats": six}},
		// 2k geometry and an 8K texture: all four keys (40 credits, the 8K
		// texture accepted).
		{"/multi-image-to-3d", "multi-image-to-3d", map[string]any{
			"image_urls": []string{"https://example.test/a.png", "https://example.test/b.png"}, "ai_model": "meshy-7.1",
			"geometry_resolution": "2k", "texture_resolution": "8k", "enable_pbr": true, "target_formats": six}},
		// A 2K retexture repeats neither texture key (10 credits).
		{"/retexture", "retexture", map[string]any{
			"input_task_id": source, "ai_model": "meshy-7", "text_style_prompt": "p", "texture_resolution": "2k",
			"enable_pbr": true, "target_formats": six}},
	} {
		want := real[tc.typ]
		if want == nil {
			t.Fatalf("the 2026-11-24 sample has no succeeded %s poll", tc.typ)
		}
		_, got := f.finish(tc.path, f.create(tc.path, tc.request), key)
		assertSameResolutionKeysAndPrice(t, tc.typ, want, got)
	}
}

// Each resolution key follows the request and never the template: the newest
// Multi Image to 3D template repeats an 8K texture, the newest Image to 3D one
// (meshy-t2.5) repeats no geometry, and neither may leak into another request.
func TestResolutionEchoFollowsTheRequestNotTheTemplate(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	_, plain := f.finish("/multi-image-to-3d", f.create("/multi-image-to-3d", map[string]any{
		"image_urls": []string{"https://example.test/a.png"}, "texture_resolution": "2k",
	}), key)
	for _, field := range []string{"texture_resolution", "hd_texture"} {
		if _, has := plain[field]; has {
			t.Errorf("a 2K multi-image task answers %s = %v from the 8K template", field, plain[field])
		}
	}
	_, ultra := f.finish("/image-to-3d", f.create("/image-to-3d", map[string]any{
		"image_url": "https://example.test/a.png", "ai_model": "meshy-7.1", "geometry_resolution": "4k", "texture_resolution": "8k",
	}), key)
	if ultra["geometry_resolution"] != "4k" || ultra["ultra_mode"] != true || ultra["texture_resolution"] != "8k" || ultra["hd_texture"] != true {
		t.Errorf("a 4k/8K image-to-3d task answers %v %v %v %v", ultra["geometry_resolution"], ultra["ultra_mode"], ultra["texture_resolution"], ultra["hd_texture"])
	}
}
