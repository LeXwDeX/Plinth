package meshyopenapiv1

import (
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/core"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

// #579: the 400 rules of the Image to 3D, Multi Image to 3D, Rigging,
// Animation, Remesh and Resize pages, and the 409 and 403 texts, read from
// https://docs.meshy.ai/en/api on 2026-11-25. Every refusal is constructed;
// each rule has a refused and an accepted case, and the accepted ones include
// the bodies the Catalog builds.

func with(base map[string]any, extra map[string]any) map[string]any {
	body := map[string]any{}
	for key, value := range base {
		body[key] = value
	}
	for key, value := range extra {
		body[key] = value
	}
	return body
}

func TestImageTo3DTextureOptionRulesOfThePage(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	single := map[string]any{"image_url": "https://example.test/a.png"}
	runPosts(t, f, "/image-to-3d", map[string]postCase{
		"image_enhancement with meshy-6-lite":   {with(single, map[string]any{"ai_model": "meshy-6-lite", "image_enhancement": true}), http.StatusBadRequest},
		"image_enhancement with smart-topology": {with(single, map[string]any{"model_type": "smart-topology", "image_enhancement": false}), http.StatusBadRequest},
		"image_enhancement with meshy-t2.5":     {with(single, map[string]any{"ai_model": "meshy-t2.5", "image_enhancement": true}), http.StatusBadRequest},
		"image_enhancement with meshy-7.1":      {with(single, map[string]any{"ai_model": "meshy-7.1", "image_enhancement": true}), http.StatusAccepted},
		"image_enhancement with meshy-7":        {with(single, map[string]any{"ai_model": "meshy-7", "image_enhancement": true}), http.StatusAccepted},
		"texture_prompt with meshy-n1":          {with(single, map[string]any{"texture_model": "meshy-n1", "texture_prompt": "p"}), http.StatusBadRequest},
		"texture_prompt with meshy-7":           {with(single, map[string]any{"texture_model": "meshy-7", "texture_prompt": "p"}), http.StatusAccepted},
		"remove_lighting true with meshy-n1":    {with(single, map[string]any{"texture_model": "meshy-n1", "remove_lighting": true}), http.StatusBadRequest},
		"remove_lighting false with meshy-n1":   {with(single, map[string]any{"texture_model": "meshy-n1", "remove_lighting": false}), http.StatusAccepted},
		"remove_lighting with smart-topology":   {with(single, map[string]any{"model_type": "smart-topology", "remove_lighting": false}), http.StatusBadRequest},
		"remove_lighting with 6-lite default":   {with(single, map[string]any{"ai_model": "meshy-6-lite", "remove_lighting": true}), http.StatusBadRequest},
		"remove_lighting with meshy-6":          {with(single, map[string]any{"ai_model": "meshy-6", "remove_lighting": true}), http.StatusAccepted},
		"quad with meshy-n1 remeshed":           {with(single, map[string]any{"texture_model": "meshy-n1", "topology": "quad", "should_remesh": true}), http.StatusBadRequest},
		"quad with meshy-n1 on 6-lite":          {with(single, map[string]any{"ai_model": "meshy-6-lite", "texture_model": "meshy-n1", "topology": "quad"}), http.StatusBadRequest},
		"quad with meshy-n1 not remeshed":       {with(single, map[string]any{"texture_model": "meshy-n1", "topology": "quad", "should_remesh": false}), http.StatusAccepted},
		"triangle with meshy-n1 remeshed":       {with(single, map[string]any{"texture_model": "meshy-n1", "topology": "triangle", "should_remesh": true}), http.StatusAccepted},
		"surface_mode outside the enum":         {with(single, map[string]any{"surface_mode": "soft"}), http.StatusBadRequest},
		"surface_mode hard":                     {with(single, map[string]any{"surface_mode": "hard"}), http.StatusAccepted},
		"surface_mode organic":                  {with(single, map[string]any{"surface_mode": "organic"}), http.StatusAccepted},
		// The enable_pbr / should_texture 400 is real since #581
		// (TestImageTo3DRefusesPBRWithoutTextureAsTheRealProbe).
		"PBR without texture": {with(single, map[string]any{"should_texture": false, "enable_pbr": true}), http.StatusBadRequest},
		// What the meshy-7.1 and meshy-t2.5 Offerings build.
		"the meshy-7.1 Catalog body": {with(single, map[string]any{"ai_model": "meshy-7.1", "geometry_resolution": "4k", "topology": "triangle",
			"target_polycount": 300000, "should_remesh": false, "should_texture": true, "enable_pbr": true, "texture_resolution": "8k",
			"auto_size": true, "image_enhancement": true, "pose_mode": "a-pose", "target_formats": []string{"glb", "obj", "fbx", "stl", "usdz", "3mf"}}), http.StatusAccepted},
		"the meshy-t2.5 Catalog body": {with(single, map[string]any{"ai_model": "meshy-t2.5", "model_type": "smart-topology", "target_polycount": 15000,
			"should_texture": true, "enable_pbr": true, "texture_resolution": "2k", "target_formats": []string{"glb", "obj", "fbx", "stl", "usdz", "3mf"}}), http.StatusAccepted},
	})
	multi := map[string]any{"image_urls": []string{"https://example.test/a.png", "https://example.test/b.png"}}
	runPosts(t, f, "/multi-image-to-3d", map[string]postCase{
		"remove_lighting with 6-lite default":  {with(multi, map[string]any{"ai_model": "meshy-6-lite", "remove_lighting": false}), http.StatusBadRequest},
		"remove_lighting with meshy-7.1":       {with(multi, map[string]any{"ai_model": "meshy-7.1", "remove_lighting": true}), http.StatusAccepted},
		"quad with meshy-n1 remeshed":          {with(multi, map[string]any{"texture_model": "meshy-n1", "topology": "quad", "should_remesh": true}), http.StatusBadRequest},
		"quad with meshy-7 remeshed":           {with(multi, map[string]any{"texture_model": "meshy-7", "topology": "quad", "should_remesh": true}), http.StatusAccepted},
		"image_enhancement with meshy-6-lite":  {with(multi, map[string]any{"ai_model": "meshy-6-lite", "image_enhancement": true}), http.StatusBadRequest},
		"image_enhancement with meshy-7.1":     {with(multi, map[string]any{"ai_model": "meshy-7.1", "image_enhancement": true}), http.StatusAccepted},
		"surface_mode is not checked on multi": {with(multi, map[string]any{"ai_model": "meshy-7.1", "surface_mode": "soft"}), http.StatusAccepted},
	})
}

func TestRiggingAnimationTypeAndTextureRulesOfThePage(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	rig := map[string]any{"model_url": "https://example.test/a.glb"}
	runPosts(t, f, "/rigging", map[string]postCase{
		"an unknown animation_type":           {with(rig, map[string]any{"animation_type": "octopod"}), http.StatusBadRequest},
		"biped":                               {with(rig, map[string]any{"animation_type": "biped", "skeleton_template": "mixamo", "include_fingers": true}), http.StatusAccepted},
		"quadruped":                           {with(rig, map[string]any{"animation_type": "quadruped", "height_meters": 0}), http.StatusAccepted},
		"quadruped with fingers":              {with(rig, map[string]any{"animation_type": "quadruped", "include_fingers": true}), http.StatusBadRequest},
		"quadruped without fingers":           {with(rig, map[string]any{"animation_type": "quadruped", "include_fingers": false}), http.StatusAccepted},
		"quadruped with a skeleton_template":  {with(rig, map[string]any{"animation_type": "quadruped", "skeleton_template": "meshyrig"}), http.StatusBadRequest},
		"a JPEG texture_image_url":            {with(rig, map[string]any{"texture_image_url": "https://example.test/t.jpg"}), http.StatusBadRequest},
		"a JPEG data URI":                     {with(rig, map[string]any{"texture_image_url": "data:image/jpeg;base64,AAAA"}), http.StatusBadRequest},
		"a PNG texture_image_url":             {with(rig, map[string]any{"texture_image_url": "https://example.test/t.PNG?sig=1"}), http.StatusAccepted},
		"a PNG data URI":                      {with(rig, map[string]any{"texture_image_url": dataURI(t, 64, 64)}), http.StatusAccepted},
		"a texture URL without extension":     {with(rig, map[string]any{"texture_image_url": "https://example.test/texture"}), http.StatusAccepted},
		"the Catalog body (height, template)": {with(rig, map[string]any{"height_meters": 1.7, "skeleton_template": "unity_humanoid"}), http.StatusAccepted},
	})
}

func TestAnimationFPSAndMotionRigRulesOfThePage(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := f.source()
	biped := f.create("/rigging", map[string]any{"input_task_id": source, "height_meters": 1.7})
	f.finish("/rigging", biped, key)
	quadruped := f.create("/rigging", map[string]any{"input_task_id": source, "animation_type": "quadruped"})
	f.finish("/rigging", quadruped, key)
	motion := f.create("/text-to-motion", map[string]any{"prompt": "a person waves one hand", "duration": 2, "mode": "prime"})
	f.finish("/text-to-motion", motion, key)
	runPosts(t, f, "/animations", map[string]postCase{
		"fps 30 with change_fps":            {map[string]any{"rig_task_id": biped, "action_id": 0, "post_process": map[string]any{"operation_type": "change_fps", "fps": 30}}, http.StatusAccepted},
		"fps 60 with change_fps":            {map[string]any{"rig_task_id": biped, "action_id": 0, "post_process": map[string]any{"operation_type": "change_fps", "fps": 60}}, http.StatusAccepted},
		"fps 48":                            {map[string]any{"rig_task_id": biped, "action_id": 0, "post_process": map[string]any{"operation_type": "change_fps", "fps": 48}}, http.StatusBadRequest},
		"fps as a string":                   {map[string]any{"rig_task_id": biped, "action_id": 0, "post_process": map[string]any{"operation_type": "change_fps", "fps": "30"}}, http.StatusBadRequest},
		"fps beside fbx2usdz is ignored":    {map[string]any{"rig_task_id": biped, "action_id": 0, "post_process": map[string]any{"operation_type": "fbx2usdz", "fps": 24}}, http.StatusAccepted},
		"a motion on a biped rig":           {map[string]any{"rig_task_id": biped, "motion_task_id": motion}, http.StatusAccepted},
		"a motion on a quadruped rig":       {map[string]any{"rig_task_id": quadruped, "motion_task_id": motion}, http.StatusBadRequest},
		"an action on a quadruped rig":      {map[string]any{"rig_task_id": quadruped, "action_id": 0}, http.StatusAccepted},
		"the Catalog body (rig and action)": {map[string]any{"rig_task_id": biped, "action_id": 0}, http.StatusAccepted},
	})
}

func TestRemeshRequestRulesOfThePage(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := f.source()
	remesh := map[string]any{"input_task_id": source}
	runPosts(t, f, "/remesh", map[string]postCase{
		"target_polycount 99":                   {with(remesh, map[string]any{"target_polycount": 99}), http.StatusBadRequest},
		"target_polycount 300001":               {with(remesh, map[string]any{"target_polycount": 300001}), http.StatusBadRequest},
		"target_polycount 100":                  {with(remesh, map[string]any{"target_polycount": 100}), http.StatusAccepted},
		"target_polycount 300000 quad (real)":   {with(remesh, map[string]any{"target_polycount": 300000, "topology": "quad", "target_formats": []string{"glb"}}), http.StatusAccepted},
		"topology outside the enum":             {with(remesh, map[string]any{"topology": "ngon"}), http.StatusBadRequest},
		"topology triangle":                     {with(remesh, map[string]any{"topology": "triangle"}), http.StatusAccepted},
		"two resize options":                    {with(remesh, map[string]any{"resize_height": 1.7, "resize_longest_side": 2}), http.StatusBadRequest},
		"a size and auto_size":                  {with(remesh, map[string]any{"resize_height": 1.7, "auto_size": true}), http.StatusBadRequest},
		"one resize option":                     {with(remesh, map[string]any{"resize_height": 1.7}), http.StatusAccepted},
		"a size and auto_size false":            {with(remesh, map[string]any{"resize_longest_side": 2, "auto_size": false}), http.StatusAccepted},
		"convert_format_only with no formats":   {with(remesh, map[string]any{"convert_format_only": true, "target_formats": []string{}}), http.StatusBadRequest},
		"convert_format_only with null formats": {with(remesh, map[string]any{"convert_format_only": true, "target_formats": nil}), http.StatusBadRequest},
		"convert_format_only with formats":      {with(remesh, map[string]any{"convert_format_only": true, "target_formats": []string{"fbx"}}), http.StatusAccepted},
		"blend":                                 {with(remesh, map[string]any{"target_formats": []string{"glb", "blend"}}), http.StatusAccepted},
		"an unknown format":                     {with(remesh, map[string]any{"target_formats": []string{"dae"}}), http.StatusBadRequest},
		"the Catalog body (six formats)": {with(remesh, map[string]any{"topology": "triangle", "target_polycount": 5800,
			"target_formats": []string{"glb", "fbx", "obj", "usdz", "stl", "3mf"}}), http.StatusAccepted},
	})
}

func TestResizeSizesMustBeGreaterThanZero(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := f.source()
	runPosts(t, f, "/resize", map[string]postCase{
		"a negative height":       {map[string]any{"input_task_id": source, "resize_height": -1}, http.StatusBadRequest},
		"a zero height":           {map[string]any{"input_task_id": source, "resize_height": 0}, http.StatusBadRequest},
		"a negative longest side": {map[string]any{"input_task_id": source, "resize_longest_side": -0.5}, http.StatusBadRequest},
		"a zero longest side":     {map[string]any{"input_task_id": source, "resize_longest_side": 0}, http.StatusBadRequest},
		"a height as a string":    {map[string]any{"input_task_id": source, "resize_height": "1.7"}, http.StatusBadRequest},
		"a small height":          {map[string]any{"input_task_id": source, "resize_height": 0.001, "origin_at": "bottom"}, http.StatusAccepted},
		"a longest side":          {map[string]any{"input_task_id": source, "resize_longest_side": 2.5}, http.StatusAccepted},
		"auto_size":               {map[string]any{"input_task_id": source, "auto_size": true}, http.StatusAccepted},
	})
}

// The 409 of deleting a running task carries the Text to 3D page's text.
func TestDeletingARunningTaskAnswersTheDocumentedConflict(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	id := f.create("/image-to-3d", map[string]any{"image_url": "https://example.test/a.png"})
	for i := 0; i < 8; i++ {
		_, _, body := f.Do("GET", f.v1("/image-to-3d/"+id), key, nil)
		if testkit.Field(t, body, "status") == core.StatusInProgress {
			break
		}
	}
	status, _, body := f.Do("DELETE", f.v1("/image-to-3d/"+id), key, nil)
	if status != http.StatusConflict || len(body) != 1 || body["message"] != "Task is IN_PROGRESS and cannot be deleted." {
		t.Fatalf("DELETE of a running task = %d %v", status, body)
	}
}

// The forbidden scenario answers the Rigging and Animation pages' text on those
// routes and the generic text elsewhere.
func TestForbiddenTextFollowsTheRoute(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := f.source()
	rig := f.create("/rigging", map[string]any{"input_task_id": source})
	f.finish("/rigging", rig, key)
	for path, tc := range map[string]struct {
		body map[string]any
		want string
	}{
		"/rigging":    {map[string]any{"input_task_id": source}, processingForbiddenMessage},
		"/animations": {map[string]any{"rig_task_id": rig, "action_id": 0}, processingForbiddenMessage},
		"/remesh":     {map[string]any{"input_task_id": source}, "You aren't allowed to make this request"},
	} {
		status, _, body := f.Do("POST", f.v1(path), key, tc.body, "X-Fake-Scenario", "forbidden")
		if status != http.StatusForbidden || len(body) != 1 || body["message"] != tc.want {
			t.Fatalf("forbidden %s = %d %v, want %q", path, status, body, tc.want)
		}
	}
}
