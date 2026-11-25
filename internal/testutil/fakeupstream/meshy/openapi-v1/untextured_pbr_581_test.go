package meshyopenapiv1

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// #581: Image to 3D answers enable_pbr true with should_texture false with a
// 400. The one real probe of 2026-11-25 (no credits spent) is the committed
// sample below; the fake answers the same status and message. The Multi Image
// to 3D page states no such rule, so the pair stays accepted there.
const untexturedPBRSample = "testdata/meshy-openapi-v1-image-to-3d-untextured-pbr-2026-11-25.candidate.json"

func untexturedPBRExchange(t *testing.T) wirefixture.Exchange {
	t.Helper()
	raw, err := sampleFS.ReadFile(untexturedPBRSample)
	if err != nil {
		t.Fatal(err)
	}
	var exchanges []wirefixture.Exchange
	if err := json.Unmarshal(raw, &exchanges); err != nil {
		t.Fatal(err)
	}
	if len(exchanges) != 1 || exchanges[0].Request.Method != http.MethodPost || exchanges[0].Response.Status != http.StatusBadRequest {
		t.Fatalf("%s holds %d exchanges, want the one refused create", untexturedPBRSample, len(exchanges))
	}
	return exchanges[0]
}

func TestImageTo3DRefusesPBRWithoutTextureAsTheRealProbe(t *testing.T) {
	exchange := untexturedPBRExchange(t)
	real, err := fakeupstream.JSONBody(exchange)
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(exchange.Request.Body.JSON, &request); err != nil {
		t.Fatal(err)
	}
	request["image_url"] = "https://example.test/a.png"
	f := newFixture(t, fakeupstream.Config{})
	status, _, body := f.Do("POST", f.v1("/image-to-3d"), key, request)
	if status != exchange.Response.Status || body["message"] != real["message"] || len(body) != len(real) {
		t.Fatalf("the probe's request = %d %v, the real API answered %d %v", status, body, exchange.Response.Status, real)
	}
}

func TestImageTo3DAcceptsUntexturedWithoutPBR(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	six := []string{"glb", "obj", "fbx", "stl", "usdz", "3mf"}
	single := map[string]any{"image_url": "https://example.test/a.png"}
	runPosts(t, f, "/image-to-3d", map[string]postCase{
		"PBR with texture":         {with(single, map[string]any{"should_texture": true, "enable_pbr": true}), http.StatusAccepted},
		"PBR by default texturing": {with(single, map[string]any{"enable_pbr": true}), http.StatusAccepted},
		"no PBR without texture":   {with(single, map[string]any{"should_texture": false, "enable_pbr": false}), http.StatusAccepted},
		// What the meshy-7.1 and meshy-t2.5 Offerings build with texturing off:
		// enable_pbr is left out.
		"the untextured meshy-7.1 Catalog body": {with(single, map[string]any{"ai_model": "meshy-7.1", "geometry_resolution": "4k", "topology": "triangle",
			"target_polycount": 300000, "should_remesh": false, "should_texture": false, "texture_resolution": "8k",
			"image_enhancement": true, "target_formats": six}), http.StatusAccepted},
		"the untextured meshy-t2.5 Catalog body": {with(single, map[string]any{"ai_model": "meshy-t2.5", "model_type": "smart-topology", "target_polycount": 15000,
			"should_texture": false, "texture_resolution": "8k", "target_formats": six}), http.StatusAccepted},
	})
	multi := map[string]any{"image_urls": []string{"https://example.test/a.png", "https://example.test/b.png"}}
	runPosts(t, f, "/multi-image-to-3d", map[string]postCase{
		"PBR without texture (no such rule on the page)": {with(multi, map[string]any{"ai_model": "meshy-7.1", "should_texture": false, "enable_pbr": true}), http.StatusAccepted},
	})
}
