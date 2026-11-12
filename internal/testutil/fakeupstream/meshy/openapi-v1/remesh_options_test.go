package meshyopenapiv1

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	meshyopenapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/openapi-v2"
)

// #276: the remesh options of a 3D generation (should_remesh, topology,
// target_polycount, decimation_mode) are checked against the Text to 3D and Image
// to 3D pages read on 2026-11-12 (core/generation.go). The same rules answer the
// v1 image-to-3d route and the v2 text-to-3d preview.
func TestGenerationRemeshOptionsFollowTheOfficialPages(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	image := func(changes map[string]any) (string, map[string]any) {
		body := map[string]any{"image_url": "https://example.test/a.png"}
		for name, value := range changes {
			body[name] = value
		}
		return f.v1("/image-to-3d"), body
	}
	text := func(changes map[string]any) (string, map[string]any) {
		body := map[string]any{"mode": "preview", "prompt": "a plain robot"}
		for name, value := range changes {
			body[name] = value
		}
		return meshyopenapiv2.Prefix + "/text-to-3d", body
	}
	for name, tc := range map[string]struct {
		request func(map[string]any) (string, map[string]any)
		changes map[string]any
		refused bool
	}{
		"a remeshed quad character, as the game purpose asks": {image, map[string]any{
			"ai_model": "meshy-7.1", "should_remesh": true, "topology": "quad", "target_polycount": 10000}, false},
		"a remeshed triangle prop, as the game purpose asks": {image, map[string]any{
			"ai_model": "meshy-7.1", "should_remesh": true, "topology": "triangle", "target_polycount": 8000}, false},
		"the same options on the text-to-3d preview": {text, map[string]any{
			"ai_model": "meshy-7.1", "should_remesh": true, "topology": "quad", "target_polycount": 10000}, false},
		"a remeshed face count at the floor":    {image, map[string]any{"should_remesh": true, "target_polycount": 100}, false},
		"a remeshed face count at the ceiling":  {image, map[string]any{"should_remesh": true, "target_polycount": 300000}, false},
		"a remeshed face count below 100":       {image, map[string]any{"should_remesh": true, "target_polycount": 99}, true},
		"a remeshed face count above 300000":    {image, map[string]any{"should_remesh": true, "target_polycount": 300001}, true},
		"the same on the text-to-3d preview":    {text, map[string]any{"should_remesh": true, "target_polycount": 300001}, true},
		"a face count that takes no effect":     {image, map[string]any{"target_polycount": 300001}, false},
		"a face count must be an integer":       {image, map[string]any{"should_remesh": true, "target_polycount": 5000.5}, true},
		"should_remesh must be a boolean":       {image, map[string]any{"should_remesh": "yes"}, true},
		"an unknown topology":                   {image, map[string]any{"should_remesh": true, "topology": "hexagon"}, true},
		"a decimation level":                    {image, map[string]any{"should_remesh": true, "decimation_mode": 2}, false},
		"a decimation level above low":          {image, map[string]any{"should_remesh": true, "decimation_mode": 5}, true},
		"Smart Topology meshy-t2.5 triangles":   {image, map[string]any{"ai_model": "meshy-t2.5", "model_type": "smart-topology", "target_polycount": 30000}, false},
		"Smart Topology meshy-t2.5 above 30000": {image, map[string]any{"ai_model": "meshy-t2.5", "model_type": "smart-topology", "target_polycount": 30001}, true},
		"Smart Topology meshy-t2 at 15000":      {image, map[string]any{"ai_model": "meshy-t2", "model_type": "smart-topology", "target_polycount": 15000}, false},
		"Smart Topology meshy-t2 above 15000":   {image, map[string]any{"ai_model": "meshy-t2", "model_type": "smart-topology", "target_polycount": 15001}, true},
		"Smart Topology is triangle-only":       {image, map[string]any{"ai_model": "meshy-t2.5", "model_type": "smart-topology", "topology": "quad"}, true},
		"Smart Topology triangles are fine":     {image, map[string]any{"ai_model": "meshy-t2.5", "model_type": "smart-topology", "topology": "triangle"}, false},
		"Smart Topology ignores should_remesh":  {image, map[string]any{"ai_model": "meshy-t2.5", "model_type": "smart-topology", "should_remesh": true, "target_polycount": 20000}, false},
	} {
		t.Run(name, func(t *testing.T) {
			path, body := tc.request(tc.changes)
			status, _, response := f.Do("POST", path, key, body)
			if tc.refused {
				if status != 400 {
					t.Fatalf("%v = %d %v, want 400", body, status, response)
				}
				return
			}
			if status/100 != 2 {
				t.Fatalf("%v = %d %v", body, status, response)
			}
		})
	}
}
