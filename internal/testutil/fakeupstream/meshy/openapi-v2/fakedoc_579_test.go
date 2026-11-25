package meshyopenapiv2

import (
	"net/http"
	"testing"
)

// #579: the refine texture conflicts of the Text to 3D page, read from
// https://docs.meshy.ai/en/api/text-to-3d on 2026-11-25. The refusal texts are
// constructed. A refine without ai_model (what the Adapter sends) is never
// judged, and meshy-7.1 stays accepted as the real refine of 2026-11-20 was.
func TestRefineTextureConflictsOfThePage(t *testing.T) {
	f := newFixture(t)
	preview := create(t, f, map[string]any{"mode": "preview", "prompt": "p"})
	finish(t, f, preview)
	refine := func(extra map[string]any) map[string]any {
		body := map[string]any{"mode": "refine", "preview_task_id": preview}
		for key, value := range extra {
			body[key] = value
		}
		return body
	}
	for name, tc := range map[string]struct {
		body map[string]any
		want int
	}{
		"8k with meshy-6-lite":                {refine(map[string]any{"ai_model": "meshy-6-lite", "texture_resolution": "8k"}), http.StatusBadRequest},
		"4k with meshy-n1":                    {refine(map[string]any{"ai_model": "meshy-n1", "texture_resolution": "4k"}), http.StatusBadRequest},
		"hd_texture with meshy-6-lite":        {refine(map[string]any{"ai_model": "meshy-6-lite", "hd_texture": true}), http.StatusBadRequest},
		"8k with meshy-6":                     {refine(map[string]any{"ai_model": "meshy-6", "texture_resolution": "8k"}), http.StatusAccepted},
		"8k with meshy-7":                     {refine(map[string]any{"ai_model": "meshy-7", "texture_resolution": "8k"}), http.StatusAccepted},
		"4k with latest":                      {refine(map[string]any{"ai_model": "latest", "texture_resolution": "4k"}), http.StatusAccepted},
		"8k with meshy-7.1 (not tightened)":   {refine(map[string]any{"ai_model": "meshy-7.1", "texture_resolution": "8k"}), http.StatusAccepted},
		"2k with meshy-6-lite":                {refine(map[string]any{"ai_model": "meshy-6-lite", "texture_resolution": "2k"}), http.StatusAccepted},
		"8k without ai_model (the Adapter)":   {refine(map[string]any{"texture_resolution": "8k", "enable_pbr": true}), http.StatusAccepted},
		"meshy-n1 with PBR":                   {refine(map[string]any{"ai_model": "meshy-n1", "enable_pbr": true}), http.StatusBadRequest},
		"meshy-n1 without PBR":                {refine(map[string]any{"ai_model": "meshy-n1", "enable_pbr": false, "texture_image_url": "https://example.test/t.png"}), http.StatusAccepted},
		"meshy-n1 with a texture_prompt":      {refine(map[string]any{"ai_model": "meshy-n1", "texture_prompt": "p"}), http.StatusBadRequest},
		"meshy-n1 with remove_lighting":       {refine(map[string]any{"ai_model": "meshy-n1", "remove_lighting": true}), http.StatusBadRequest},
		"meshy-6-lite with remove_lighting":   {refine(map[string]any{"ai_model": "meshy-6-lite", "remove_lighting": false}), http.StatusBadRequest},
		"meshy-6 with remove_lighting":        {refine(map[string]any{"ai_model": "meshy-6", "remove_lighting": true}), http.StatusAccepted},
		"a texture_prompt without ai_model":   {refine(map[string]any{"texture_prompt": "p", "enable_pbr": true, "texture_resolution": "2k"}), http.StatusAccepted},
		"the 2026-11-20 real refine (7.1 2k)": {refine(map[string]any{"ai_model": "meshy-7.1", "enable_pbr": true, "texture_resolution": "2k", "should_remesh": true, "target_polycount": 12000, "topology": "triangle"}), http.StatusAccepted},
	} {
		t.Run(name, func(t *testing.T) {
			if got := status(t, f, tc.body); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}
