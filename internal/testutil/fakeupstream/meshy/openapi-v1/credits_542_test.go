package meshyopenapiv1

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #542 (I12): consumed_credits follows the request, from the Pricing page
// (https://docs.meshy.ai/en/api/pricing, read 2026-11-24). Cases marked real
// match a price a real capture of 2026-11-20 to 2026-11-23 showed; the others
// are the page's table, and a few cells are constructed (see core.Credits).
func TestConsumedCreditsFollowTheRequestOptions(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := f.source()
	image := f.create("/text-to-image", map[string]any{"ai_model": "nano-banana-pro", "prompt": "p"})
	f.finish("/text-to-image", image, key)
	rig := f.create("/rigging", map[string]any{"input_task_id": source})
	f.finish("/rigging", rig, key)
	views := []string{"https://example.test/a.png", "https://example.test/b.png"}

	for name, tc := range map[string]struct {
		path string
		body map[string]any
		want float64
	}{
		"nano-banana text, real 3 on the page":                 {"/text-to-image", map[string]any{"ai_model": "nano-banana", "prompt": "p"}, 3},
		"nano-banana-2 text":                                   {"/text-to-image", map[string]any{"ai_model": "nano-banana-2", "prompt": "p"}, 6},
		"nano-banana-2-lite text":                              {"/text-to-image", map[string]any{"ai_model": "nano-banana-2-lite", "prompt": "p"}, 3},
		"nano-banana-2-1 text":                                 {"/text-to-image", map[string]any{"ai_model": "nano-banana-2-1", "prompt": "p"}, 4},
		"nano-banana-pro text, real":                           {"/text-to-image", map[string]any{"ai_model": "nano-banana-pro", "prompt": "p"}, 9},
		"gpt-image text":                                       {"/text-to-image", map[string]any{"ai_model": "gpt-image-2", "prompt": "p"}, 9},
		"nano-banana-pro image, real":                          {"/image-to-image", map[string]any{"ai_model": "nano-banana-pro", "prompt": "p", "reference_image_urls": views[:1]}, 9},
		"gpt-image image":                                      {"/image-to-image", map[string]any{"ai_model": "gpt-image-2", "prompt": "p", "input_task_id": image}, 12},
		"meshy-7.1 image-to-3d, 4k geometry, 2k texture, real": {"/image-to-3d", map[string]any{"image_url": views[0], "ai_model": "meshy-7.1", "geometry_resolution": "4k", "texture_resolution": "2k"}, 35},
		"meshy-7.1 image-to-3d, 8k texture":                    {"/image-to-3d", map[string]any{"image_url": views[0], "ai_model": "meshy-7.1", "texture_resolution": "8k"}, 35},
		"meshy-7.1 image-to-3d, mesh only":                     {"/image-to-3d", map[string]any{"image_url": views[0], "ai_model": "meshy-7.1", "should_texture": false}, 20},
		"meshy-7.1 image-to-3d, default 2k":                    {"/image-to-3d", map[string]any{"image_url": views[0]}, 30},
		"meshy-6-lite image-to-3d, 2k texture":                 {"/image-to-3d", map[string]any{"image_url": views[0], "ai_model": "meshy-6-lite"}, 15},
		"meshy-6-lite image-to-3d, mesh only":                  {"/image-to-3d", map[string]any{"image_url": views[0], "ai_model": "meshy-6-lite", "should_texture": false}, 5},
		"meshy-6-lite with the meshy-7 texture at 8k":          {"/image-to-3d", map[string]any{"image_url": views[0], "ai_model": "meshy-6-lite", "texture_model": "meshy-7", "texture_resolution": "8k"}, 20},
		"meshy-t2.5 image-to-3d, 8k texture":                   {"/image-to-3d", map[string]any{"image_url": views[0], "ai_model": "meshy-t2.5", "texture_resolution": "8k"}, 20},
		"meshy-t2 image-to-3d, 4k texture":                     {"/image-to-3d", map[string]any{"image_url": views[0], "ai_model": "meshy-t2", "texture_resolution": "4k"}, 15},
		"multi-image 2k geometry, 2k texture, real":            {"/multi-image-to-3d", map[string]any{"image_urls": views, "ai_model": "meshy-7.1", "geometry_resolution": "2k", "texture_resolution": "2k"}, 35},
		"multi-image 2k geometry, 8k texture":                  {"/multi-image-to-3d", map[string]any{"image_urls": views, "ai_model": "meshy-7.1", "geometry_resolution": "2k", "texture_resolution": "8k"}, 40},
		"multi-image standard, mesh only":                      {"/multi-image-to-3d", map[string]any{"image_urls": views, "should_texture": false}, 20},
		"retexture 8k, real":                                   {"/retexture", map[string]any{"input_task_id": source, "text_style_prompt": "p", "ai_model": "meshy-7", "texture_resolution": "8k"}, 15},
		"retexture 4k":                                         {"/retexture", map[string]any{"input_task_id": source, "text_style_prompt": "p", "texture_resolution": "4k"}, 10},
		"retexture default 2k":                                 {"/retexture", map[string]any{"input_task_id": source, "text_style_prompt": "p"}, 10},
		"remesh, real":                                         {"/remesh", map[string]any{"input_task_id": source}, 5},
		"uv-unwrap":                                            {"/uv-unwrap", map[string]any{"model_url": "https://example.test/a.glb"}, 5},
		"convert, real":                                        {"/convert", map[string]any{"input_task_id": source, "target_formats": []string{"glb"}}, 1},
		"resize, real":                                         {"/resize", map[string]any{"input_task_id": source, "auto_size": true}, 1},
		"rigging, real":                                        {"/rigging", map[string]any{"input_task_id": source}, 5},
		"one animation, real":                                  {"/animations", map[string]any{"rig_task_id": rig, "action_id": 0}, 3},
		"three animations":                                     {"/animations", map[string]any{"rig_task_id": rig, "action_ids": []int{1, 2, 3}}, 9},
		"text-to-motion prime":                                 {"/text-to-motion", map[string]any{"prompt": "p", "duration": 2}, 10},
		"text-to-motion swift":                                 {"/text-to-motion", map[string]any{"prompt": "p", "duration": 2, "mode": "swift"}, 3},
		"analyze printability is free":                         {"/print/analyze", map[string]any{"input_task_id": source}, 0},
		"multi-color print":                                    {"/print/multi-color", map[string]any{"input_task_id": source}, 10},
	} {
		t.Run(name, func(t *testing.T) {
			id := f.create(tc.path, tc.body)
			// The price shows from the first poll, as the captures do.
			_, _, first := f.Do("GET", f.v1(tc.path+"/"+id), key, nil)
			if first["consumed_credits"] != tc.want {
				t.Fatalf("PENDING consumed_credits = %v, want %v", first["consumed_credits"], tc.want)
			}
			if _, done := f.finish(tc.path, id, key); done["consumed_credits"] != tc.want {
				t.Fatalf("SUCCEEDED consumed_credits = %v, want %v", done["consumed_credits"], tc.want)
			}
		})
	}

	// A refunded (failed) task reports 0.
	failed := f.create("/remesh", map[string]any{"input_task_id": source}, "X-Fake-Task-Error", "server_error")
	if _, body := f.finish("/remesh", failed, key); body["consumed_credits"] != float64(0) {
		t.Fatalf("a failed task reports %v", body["consumed_credits"])
	}
}
