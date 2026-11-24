package meshyopenapiv1

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #542 (I9, I10, I11, m5): the 400 lists of the Retexture, Image to 3D, Multi
// Image to 3D, Text to Image, Image to Image, Text to Motion and Animation pages,
// read from https://docs.meshy.ai/en/api on 2026-11-24. The refusal texts are
// constructed. Cases marked constructed below rest on a page that lists a value
// set but never says what an unlisted value does; a zero-cost probe of the real
// API settles them.

type postCase struct {
	body map[string]any
	want int
}

func runPosts(t *testing.T, f *fixture, path string, cases map[string]postCase) {
	t.Helper()
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			status, _, body := f.Do("POST", f.v1(path), key, tc.body)
			if status != tc.want {
				t.Fatalf("POST %s = %d %v, want %d", path, status, body, tc.want)
			}
			if status == http.StatusBadRequest {
				if message, ok := body["message"].(string); !ok || message == "" || len(body) != 1 {
					t.Fatalf("the refusal must be the {message} envelope: %v", body)
				}
			}
		})
	}
}

func TestRetextureStyleInputAndModelRules(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := f.source()
	retexture := func(extra map[string]any) map[string]any {
		body := map[string]any{"input_task_id": source}
		for key, value := range extra {
			body[key] = value
		}
		return body
	}
	views := []string{"https://example.test/a.png", "https://example.test/b.png"}
	runPosts(t, f, "/retexture", map[string]postCase{
		"no style input":                        {retexture(nil), http.StatusBadRequest},
		"a text style":                          {retexture(map[string]any{"text_style_prompt": "p"}), http.StatusAccepted},
		"an image style":                        {retexture(map[string]any{"image_style_url": "https://example.test/s.png"}), http.StatusAccepted},
		"multiview with meshy-7":                {retexture(map[string]any{"multiview_image_urls": views, "ai_model": "meshy-7"}), http.StatusAccepted},
		"multiview with meshy-n1":               {retexture(map[string]any{"multiview_image_urls": views, "ai_model": "meshy-n1"}), http.StatusAccepted},
		"multiview with the default model":      {retexture(map[string]any{"multiview_image_urls": views}), http.StatusBadRequest},
		"multiview with latest":                 {retexture(map[string]any{"multiview_image_urls": views, "ai_model": "latest"}), http.StatusBadRequest},
		"multiview with a text style":           {retexture(map[string]any{"multiview_image_urls": views, "ai_model": "meshy-7", "text_style_prompt": "p"}), http.StatusBadRequest},
		"multiview with an image style":         {retexture(map[string]any{"multiview_image_urls": views, "ai_model": "meshy-7", "image_style_url": "u"}), http.StatusBadRequest},
		"four multiview images":                 {retexture(map[string]any{"multiview_image_urls": append(views, views...), "ai_model": "meshy-7"}), http.StatusAccepted},
		"five multiview images":                 {retexture(map[string]any{"multiview_image_urls": append(append(views, views...), views[0]), "ai_model": "meshy-7"}), http.StatusBadRequest},
		"a style prompt over 800 characters":    {retexture(map[string]any{"text_style_prompt": strings.Repeat("a", 801)}), http.StatusBadRequest},
		"a style prompt of 800 characters":      {retexture(map[string]any{"text_style_prompt": strings.Repeat("a", 800)}), http.StatusAccepted},
		"meshy-7":                               {retexture(map[string]any{"text_style_prompt": "p", "ai_model": "meshy-7"}), http.StatusAccepted},
		"latest":                                {retexture(map[string]any{"text_style_prompt": "p", "ai_model": "latest"}), http.StatusAccepted},
		"meshy-7.1 is not in the page's enum":   {retexture(map[string]any{"text_style_prompt": "p", "ai_model": "meshy-7.1"}), http.StatusBadRequest},
		"meshy-6 at 8k":                         {retexture(map[string]any{"text_style_prompt": "p", "ai_model": "meshy-6", "texture_resolution": "8k"}), http.StatusAccepted},
		"meshy-6-lite at 4k":                    {retexture(map[string]any{"text_style_prompt": "p", "ai_model": "meshy-6-lite", "texture_resolution": "4k"}), http.StatusBadRequest},
		"meshy-6-lite with remove_lighting":     {retexture(map[string]any{"text_style_prompt": "p", "ai_model": "meshy-6-lite", "remove_lighting": false}), http.StatusBadRequest},
		"meshy-n1 with PBR":                     {retexture(map[string]any{"image_style_url": "u", "ai_model": "meshy-n1", "enable_pbr": true}), http.StatusBadRequest},
		"meshy-n1 without PBR":                  {retexture(map[string]any{"image_style_url": "u", "ai_model": "meshy-n1", "enable_pbr": false}), http.StatusAccepted},
		"meshy-n1 at 8k":                        {retexture(map[string]any{"image_style_url": "u", "ai_model": "meshy-n1", "texture_resolution": "8k"}), http.StatusBadRequest},
		"meshy-n1 with remove_lighting":         {retexture(map[string]any{"image_style_url": "u", "ai_model": "meshy-n1", "remove_lighting": false}), http.StatusBadRequest},
		"meshy-n1 with hd_texture":              {retexture(map[string]any{"image_style_url": "u", "ai_model": "meshy-n1", "hd_texture": true}), http.StatusBadRequest},
		"meshy-n1 with only a text style":       {retexture(map[string]any{"text_style_prompt": "p", "ai_model": "meshy-n1"}), http.StatusBadRequest},
		"a texture resolution outside the enum": {retexture(map[string]any{"text_style_prompt": "p", "texture_resolution": "1k"}), http.StatusBadRequest},
		"an unknown format":                     {retexture(map[string]any{"text_style_prompt": "p", "target_formats": []string{"blend"}}), http.StatusBadRequest},
		"a style picture under 32 px":           {retexture(map[string]any{"image_style_url": dataURI(t, 16, 16)}), http.StatusBadRequest},
		"a style picture of 64 px":              {retexture(map[string]any{"image_style_url": dataURI(t, 64, 64)}), http.StatusAccepted},
		"a model_url with a style":              {map[string]any{"model_url": "https://example.test/a.glb", "text_style_prompt": "p"}, http.StatusAccepted},
	})
}

func TestImageTo3DOptionCombinations(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	image := func(extra map[string]any) map[string]any {
		body := map[string]any{"image_url": "https://example.test/a.png"}
		for key, value := range extra {
			body[key] = value
		}
		return body
	}
	runPosts(t, f, "/image-to-3d", map[string]postCase{
		"4k geometry on meshy-7.1":                {image(map[string]any{"ai_model": "meshy-7.1", "geometry_resolution": "4k", "texture_resolution": "2k"}), http.StatusAccepted},
		"4k geometry on meshy-7":                  {image(map[string]any{"ai_model": "meshy-7", "geometry_resolution": "4k"}), http.StatusBadRequest},
		"2k geometry on meshy-6":                  {image(map[string]any{"ai_model": "meshy-6", "geometry_resolution": "2k"}), http.StatusBadRequest},
		"a Smart Topology model with 8k":          {image(map[string]any{"ai_model": "meshy-t2.5", "model_type": "smart-topology", "texture_resolution": "8k"}), http.StatusAccepted},
		"meshy-6-lite with a 2k texture":          {image(map[string]any{"ai_model": "meshy-6-lite", "texture_resolution": "2k"}), http.StatusAccepted},
		"meshy-6-lite with an 8k texture":         {image(map[string]any{"ai_model": "meshy-6-lite", "texture_resolution": "8k"}), http.StatusBadRequest},
		"meshy-6-lite with an 8k meshy-7 texture": {image(map[string]any{"ai_model": "meshy-6-lite", "texture_model": "meshy-7", "texture_resolution": "8k"}), http.StatusAccepted},
		"meshy-6-lite untextured at 8k":           {image(map[string]any{"ai_model": "meshy-6-lite", "should_texture": false, "texture_resolution": "8k"}), http.StatusAccepted},
		"meshy-n1 at 4k":                          {image(map[string]any{"texture_model": "meshy-n1", "texture_resolution": "4k"}), http.StatusBadRequest},
		"meshy-n1 with PBR":                       {image(map[string]any{"texture_model": "meshy-n1", "enable_pbr": true}), http.StatusBadRequest},
		"meshy-6 texture on another model":        {image(map[string]any{"ai_model": "meshy-7.1", "texture_model": "meshy-6"}), http.StatusBadRequest},
		"meshy-6 texture on meshy-6":              {image(map[string]any{"ai_model": "meshy-6", "texture_model": "meshy-6"}), http.StatusAccepted},
		"an unknown texture model, untextured":    {image(map[string]any{"should_texture": false, "texture_model": "meshy-9"}), http.StatusBadRequest},
		"an unknown ai_model":                     {image(map[string]any{"ai_model": "meshy-9"}), http.StatusBadRequest},
		"a texture prompt over 800 characters":    {image(map[string]any{"texture_prompt": strings.Repeat("a", 801)}), http.StatusBadRequest},
		"an unknown target format":                {image(map[string]any{"target_formats": []string{"glb", "dae"}}), http.StatusBadRequest},
	})
}

func TestMultiImageCreateRulesOfThePage(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	pictures := func(count int) []string {
		list := make([]string, count)
		for i := range list {
			list[i] = "https://example.test/a.png"
		}
		return list
	}
	multi := func(extra map[string]any) map[string]any {
		body := map[string]any{"image_urls": pictures(2)}
		for key, value := range extra {
			body[key] = value
		}
		return body
	}
	runPosts(t, f, "/multi-image-to-3d", map[string]postCase{
		"one picture":                                             {multi(map[string]any{"image_urls": pictures(1)}), http.StatusAccepted},
		"four pictures":                                           {multi(map[string]any{"image_urls": pictures(4)}), http.StatusAccepted},
		"five pictures":                                           {multi(map[string]any{"image_urls": pictures(5)}), http.StatusBadRequest},
		"2k geometry on meshy-7.1":                                {multi(map[string]any{"ai_model": "meshy-7.1", "geometry_resolution": "2k"}), http.StatusAccepted},
		"4k geometry is refused on this page":                     {multi(map[string]any{"ai_model": "meshy-7.1", "geometry_resolution": "4k"}), http.StatusBadRequest},
		"2k geometry on meshy-6":                                  {multi(map[string]any{"ai_model": "meshy-6", "geometry_resolution": "2k"}), http.StatusBadRequest},
		"a Smart Topology model is not offered":                   {multi(map[string]any{"ai_model": "meshy-t2.5"}), http.StatusBadRequest},
		"texture_model meshy-6 on a textured request":             {multi(map[string]any{"ai_model": "meshy-6", "texture_model": "meshy-6"}), http.StatusBadRequest},
		"texture_model meshy-n1":                                  {multi(map[string]any{"texture_model": "meshy-n1"}), http.StatusAccepted},
		"meshy-n1 with PBR":                                       {multi(map[string]any{"texture_model": "meshy-n1", "enable_pbr": true}), http.StatusBadRequest},
		"meshy-n1 with a texture prompt":                          {multi(map[string]any{"texture_model": "meshy-n1", "texture_prompt": "p"}), http.StatusBadRequest},
		"meshy-6-lite with image_enhancement":                     {multi(map[string]any{"ai_model": "meshy-6-lite", "image_enhancement": true}), http.StatusBadRequest},
		"8k on the default model":                                 {multi(map[string]any{"texture_resolution": "8k", "enable_pbr": true}), http.StatusAccepted},
		"texture views on the default model":                      {multi(map[string]any{"texture_image_urls": pictures(2)}), http.StatusAccepted},
		"five texture views":                                      {multi(map[string]any{"texture_image_urls": pictures(5)}), http.StatusBadRequest},
		"texture views with texture_image_url":                    {multi(map[string]any{"texture_image_urls": pictures(1), "texture_image_url": "https://example.test/t.png"}), http.StatusBadRequest},
		"texture views with a texture prompt":                     {multi(map[string]any{"texture_image_urls": pictures(1), "texture_prompt": "p"}), http.StatusBadRequest},
		"texture views on meshy-6":                                {multi(map[string]any{"ai_model": "meshy-6", "texture_image_urls": pictures(1)}), http.StatusBadRequest},
		"texture views on meshy-6 with the meshy-7 texture model": {multi(map[string]any{"ai_model": "meshy-6", "texture_model": "meshy-7", "texture_image_urls": pictures(1)}), http.StatusAccepted},
	})
}

func TestImageGenerationRules(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	text := func(extra map[string]any) map[string]any {
		body := map[string]any{"ai_model": "nano-banana-2", "prompt": "p"}
		for key, value := range extra {
			body[key] = value
		}
		return body
	}
	runPosts(t, f, "/text-to-image", map[string]postCase{
		"a plain request":                  {text(nil), http.StatusAccepted},
		"no ai_model":                      {map[string]any{"prompt": "p"}, http.StatusBadRequest},
		"an unknown ai_model":              {text(map[string]any{"ai_model": "imagen"}), http.StatusBadRequest},
		"every GPT Image model":            {text(map[string]any{"ai_model": "gpt-image-2-6-sunburst", "aspect_ratio": "2:3"}), http.StatusAccepted},
		"3:2 on a GPT Image model":         {text(map[string]any{"ai_model": "gpt-image-2", "aspect_ratio": "3:2"}), http.StatusAccepted},
		"3:2 on nano-banana":               {text(map[string]any{"ai_model": "nano-banana", "aspect_ratio": "3:2"}), http.StatusBadRequest},
		"2:3 on nano-banana-pro":           {text(map[string]any{"ai_model": "nano-banana-pro", "aspect_ratio": "2:3"}), http.StatusBadRequest},
		"2:3 on nano-banana-2-lite":        {text(map[string]any{"ai_model": "nano-banana-2-lite", "aspect_ratio": "2:3"}), http.StatusBadRequest},
		"an aspect ratio outside the enum": {text(map[string]any{"aspect_ratio": "21:9"}), http.StatusBadRequest},
		"multi-view with aspect_ratio":     {text(map[string]any{"generate_multi_view": true, "aspect_ratio": "1:1"}), http.StatusBadRequest},
		"multi-view alone":                 {text(map[string]any{"generate_multi_view": true}), http.StatusAccepted},
		"no multi-view with aspect_ratio":  {text(map[string]any{"generate_multi_view": false, "aspect_ratio": "16:9"}), http.StatusAccepted},
		"an unknown pose":                  {text(map[string]any{"pose_mode": "x-pose"}), http.StatusBadRequest},
		"a-pose":                           {text(map[string]any{"pose_mode": "a-pose"}), http.StatusAccepted},
		"remove_background not boolean":    {text(map[string]any{"remove_background": "yes"}), http.StatusBadRequest},
	})

	reference := func(count int) []string {
		list := make([]string, count)
		for i := range list {
			list[i] = "https://example.test/r.png"
		}
		return list
	}
	edit := func(extra map[string]any) map[string]any {
		body := map[string]any{"ai_model": "nano-banana-2", "prompt": "p"}
		for key, value := range extra {
			body[key] = value
		}
		return body
	}
	source := f.create("/text-to-image", text(nil))
	f.finish("/text-to-image", source, key)
	edited := f.create("/image-to-image", edit(map[string]any{"reference_image_urls": reference(1)}))
	f.finish("/image-to-image", edited, key)
	notAnImage := f.source()
	runPosts(t, f, "/image-to-image", map[string]postCase{
		"one reference":                         {edit(map[string]any{"reference_image_urls": reference(1)}), http.StatusAccepted},
		"five references":                       {edit(map[string]any{"reference_image_urls": reference(5)}), http.StatusAccepted},
		"six references":                        {edit(map[string]any{"reference_image_urls": reference(6)}), http.StatusBadRequest},
		"an empty reference list":               {edit(map[string]any{"reference_image_urls": []string{}}), http.StatusBadRequest},
		"neither references nor a task":         {edit(nil), http.StatusBadRequest},
		"a text-to-image task":                  {edit(map[string]any{"input_task_id": source}), http.StatusAccepted},
		"an image-to-image task":                {edit(map[string]any{"input_task_id": edited}), http.StatusAccepted},
		"a task that is not an image":           {edit(map[string]any{"input_task_id": notAnImage}), http.StatusBadRequest},
		"a task that does not exist":            {edit(map[string]any{"input_task_id": "01a18514-059e-73f7-a0ce-0f88a12c9300"}), http.StatusNotFound},
		"a task and references (the task wins)": {edit(map[string]any{"input_task_id": source, "reference_image_urls": reference(6)}), http.StatusAccepted},
		"a reference under 32 px":               {edit(map[string]any{"reference_image_urls": []string{dataURI(t, 16, 16)}}), http.StatusBadRequest},
		"3:2 on nano-banana":                    {edit(map[string]any{"ai_model": "nano-banana", "reference_image_urls": reference(1), "aspect_ratio": "3:2"}), http.StatusBadRequest},
		"no ai_model":                           {map[string]any{"prompt": "p", "reference_image_urls": reference(1)}, http.StatusBadRequest},
		"multi-view with aspect_ratio":          {edit(map[string]any{"reference_image_urls": reference(1), "generate_multi_view": true, "aspect_ratio": "1:1"}), http.StatusBadRequest},
	})

	// A multi-image task takes a succeeded image generation as its input.
	runPosts(t, f, "/multi-image-to-3d", map[string]postCase{
		"an image task":          {map[string]any{"input_task_id": source}, http.StatusAccepted},
		"a task of another kind": {map[string]any{"input_task_id": notAnImage}, http.StatusBadRequest},
	})
}

func TestTextToMotionAndAnimationRules(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := f.source()
	rig := f.create("/rigging", map[string]any{"input_task_id": source})
	f.finish("/rigging", rig, key)
	runPosts(t, f, "/text-to-motion", map[string]postCase{
		"prime":                   {map[string]any{"prompt": "walk", "duration": 2}, http.StatusAccepted},
		"swift at a half step":    {map[string]any{"prompt": "walk", "duration": 2.5, "mode": "swift"}, http.StatusAccepted},
		"ten seconds":             {map[string]any{"prompt": "walk", "duration": 10}, http.StatusAccepted},
		"eleven seconds":          {map[string]any{"prompt": "walk", "duration": 11}, http.StatusBadRequest},
		"one second":              {map[string]any{"prompt": "walk", "duration": 1}, http.StatusBadRequest},
		"a quarter step":          {map[string]any{"prompt": "walk", "duration": 2.25}, http.StatusBadRequest},
		"an unknown mode":         {map[string]any{"prompt": "walk", "duration": 2, "mode": "turbo"}, http.StatusBadRequest},
		"a prompt over 400":       {map[string]any{"prompt": strings.Repeat("a", 401), "duration": 2}, http.StatusBadRequest},
		"a prompt of 400":         {map[string]any{"prompt": strings.Repeat("a", 400), "duration": 2}, http.StatusAccepted},
		"a duration that is text": {map[string]any{"prompt": "walk", "duration": "2"}, http.StatusBadRequest},
	})
	ids := func(values ...int) []int { return values }
	runPosts(t, f, "/animations", map[string]postCase{
		"one action":              {map[string]any{"rig_task_id": rig, "action_id": 0}, http.StatusAccepted},
		"two actions":             {map[string]any{"rig_task_id": rig, "action_ids": ids(1, 2)}, http.StatusAccepted},
		"ten actions":             {map[string]any{"rig_task_id": rig, "action_ids": ids(1, 2, 3, 4, 5, 6, 7, 8, 9, 10)}, http.StatusAccepted},
		"eleven actions":          {map[string]any{"rig_task_id": rig, "action_ids": ids(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11)}, http.StatusBadRequest},
		"a repeated action":       {map[string]any{"rig_task_id": rig, "action_ids": ids(1, 1)}, http.StatusBadRequest},
		"no action source":        {map[string]any{"rig_task_id": rig}, http.StatusBadRequest},
		"two action sources":      {map[string]any{"rig_task_id": rig, "action_id": 1, "action_ids": ids(2)}, http.StatusBadRequest},
		"a post process":          {map[string]any{"rig_task_id": rig, "action_id": 1, "post_process": map[string]any{"operation_type": "change_fps", "fps": 60}}, http.StatusAccepted},
		"an unknown post process": {map[string]any{"rig_task_id": rig, "action_id": 1, "post_process": map[string]any{"operation_type": "bake"}}, http.StatusBadRequest},
	})
}
