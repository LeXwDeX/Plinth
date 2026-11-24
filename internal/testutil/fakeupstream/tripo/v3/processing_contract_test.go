package tripov3

import (
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #541: the source-task rules and the request values of the processing endpoints,
// read from the official pages on 2026-11-24 (processing.go). Every refusal is the
// create envelope of the samples (400/code 1004) unless a case names another code.

// expect sends one create request and checks its status and business code.
func (f *fixture) expect(path string, body map[string]any, wantStatus, wantCode int) {
	f.t.Helper()
	status, _, response := f.do(http.MethodPost, path, key, body)
	if status != wantStatus {
		f.t.Fatalf("POST %s %v = %d %v, want %d", path, body, status, response, wantStatus)
	}
	if wantStatus != http.StatusOK && int(field(f.t, response, "code").(float64)) != wantCode {
		f.t.Fatalf("POST %s %v = code %v, want %d", path, body, response["code"], wantCode)
	}
}

func (f *fixture) multiview() string {
	f.t.Helper()
	id := f.create("/generation/multiview-to-model", generationBody("/generation/multiview-to-model", modelH31, nil), key)
	f.finish(id, key)
	return id
}

func (f *fixture) imageMultiview() string {
	f.t.Helper()
	id := f.create("/generation/image-to-multiview", map[string]any{"input": "file_x"}, key)
	f.finish(id, key)
	return id
}

func (f *fixture) segmented(source string) string {
	f.t.Helper()
	id := f.create("/mesh/segment", map[string]any{"input": source, "model": "v2.0-20260430"}, key)
	f.finish(id, key)
	return id
}

// Rig check and rig take a task of the three generation types, multiview included.
func TestRigAndRigCheckTakeEveryGenerationTaskType(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	text := f.generated()
	image := f.create("/generation/image-to-model", generationBody("/generation/image-to-model", modelH31, nil), key)
	f.finish(image, key)
	multiview := f.multiview()
	for name, source := range map[string]string{"text": text, "image": image, "multiview": multiview} {
		t.Run(name, func(t *testing.T) {
			f.expect("/animations/rig-check", map[string]any{"input": source}, http.StatusOK, 0)
			f.expect("/animations/rig", map[string]any{"input": source, "model": rigModelV3}, http.StatusOK, 0)
		})
	}
	// Any other task is not a generation task.
	segment := f.segmented(text)
	f.expect("/animations/rig-check", map[string]any{"input": segment}, http.StatusBadRequest, 1004)
	f.expect("/animations/rig", map[string]any{"input": segment, "model": rigModelV3}, http.StatusBadRequest, 1004)
}

// Retarget takes the rigging task itself or a later task run on the rigged model.
func TestRetargetTakesTheRiggedModelOrATaskRunOnIt(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	text := f.generated()
	rig := f.rigged(rigModelV3, "biped")
	textureOnRig := f.create("/models/texture", map[string]any{"input": rig}, key)
	f.finish(textureOnRig, key)
	segmentOnRig := f.segmented(rig)

	walk := map[string]any{"animation": "preset:walk"}
	with := func(source string) map[string]any {
		return map[string]any{"input": source, "animation": walk["animation"]}
	}
	f.expect("/animations/retarget", with(rig), http.StatusOK, 0)
	f.expect("/animations/retarget", with(textureOnRig), http.StatusOK, 0)
	f.expect("/animations/retarget", with(segmentOnRig), http.StatusOK, 0)
	// A model that was never rigged is no source, whatever it is.
	f.expect("/animations/retarget", with(text), http.StatusBadRequest, 1004)
	f.expect("/animations/retarget", with(f.segmented(text)), http.StatusBadRequest, 1004)
	f.expect("/animations/retarget", with(f.multiview()), http.StatusBadRequest, 1004)
}

// The one real retarget failure (2026-10-29) was a v3.0 biped preset on a v2.5 biped
// rig, so that is what the fake still does by default, even though the rig page now says
// a v2.5 biped rig is rigged as v3.0: which of the two is true is settled by one real
// rig-and-retarget round (#541), and the observation outranks a reading of the page.
func TestRetargetOfAV25BipedRigFailsByDefaultLikeTheObservedRun(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	for _, rigType := range []string{"biped", ""} {
		rig := f.rigged(rigModelV25, rigType)
		retarget := f.create("/animations/retarget", map[string]any{"input": rig, "animation": "preset:walk"}, key)
		statuses, done := f.finish(retarget, key)
		if statuses[len(statuses)-1] != statusFailed || field(t, done, "data", "error_code").(float64) != 1004 {
			t.Fatalf("rig_type %q: a v3.0 biped preset on a v2.5 biped rig ended %v %v", rigType, statuses, done)
		}
	}
}

// retarget-v25-biped-as-v3 selects the other reading, the rig page's: a v2.5 biped rig
// is a v3.0 rig for retargeting, so the v3.0 biped presets fit it (constructed, not
// observed). Creature presets keep needing a rig of their own type, and the v1.0 names a
// v1.0 biped rig, which a v2.5 biped rig is not.
func TestRetargetV25BipedAsV3ScenarioFollowsTheRigPage(t *testing.T) {
	for name, headers := range map[string][]string{
		"header": {"X-Fake-Scenario", "retarget-v25-biped-as-v3"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
			for _, rigType := range []string{"biped", ""} {
				rig := f.rigged(rigModelV25, rigType)
				retarget := f.create("/animations/retarget", map[string]any{"input": rig, "animation": "preset:walk"}, key, headers...)
				if statuses, done := f.finish(retarget, key); statuses[len(statuses)-1] != statusSuccess {
					t.Fatalf("rig_type %q: a v3.0 biped preset on a v2.5 biped rig ended %v %v", rigType, statuses, done)
				}
			}
			rig := f.rigged(rigModelV25, "biped")
			retarget := f.create("/animations/retarget", map[string]any{"input": rig, "animation": "preset:biped:walk"}, key, headers...)
			if statuses, _ := f.finish(retarget, key); statuses[len(statuses)-1] != statusFailed {
				t.Fatalf("a v1.0 name on a v2.5 biped rig ended %v", statuses)
			}
		})
	}
	t.Run("key form", func(t *testing.T) {
		f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
		rig := f.rigged(rigModelV25, "biped")
		scenarioKey := "fake-retarget-v25-biped-as-v3"
		retarget := f.create("/animations/retarget", map[string]any{"input": rig, "animation": "preset:walk"}, scenarioKey)
		if statuses, done := f.finish(retarget, scenarioKey); statuses[len(statuses)-1] != statusSuccess {
			t.Fatalf("ended %v %v", statuses, done)
		}
	})
}

func TestCompleteNeedsASegmentationTask(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	text := f.generated()
	segment := f.segmented(text)
	f.expect("/mesh/complete", map[string]any{"input": segment, "model": "v1.0-20250506"}, http.StatusOK, 0)
	f.expect("/mesh/complete", map[string]any{"input": text, "model": "v1.0-20250506"}, http.StatusBadRequest, 1004)
	f.expect("/mesh/complete", map[string]any{"input": f.rigged(rigModelV3, ""), "model": "v1.0-20250506"}, http.StatusBadRequest, 1004)
	f.expect("/mesh/complete", map[string]any{"input": "00000000-0000-4000-8000-0000000000aa", "model": "v1.0-20250506"}, http.StatusNotFound, 2001)
}

func TestEditMultiviewNeedsAnImageToMultiviewTask(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	source := f.imageMultiview()
	prompts := func(views ...string) []map[string]any {
		var out []map[string]any
		for _, view := range views {
			out = append(out, map[string]any{"view": view, "prompt": "make it green"})
		}
		return out
	}
	f.expect("/generation/edit-multiview", map[string]any{"input": source, "prompts": prompts("front", "back")}, http.StatusOK, 0)
	f.expect("/generation/edit-multiview", map[string]any{"input": f.generated(), "prompts": prompts("front")}, http.StatusBadRequest, 1004)
	f.expect("/generation/edit-multiview", map[string]any{"input": f.multiview(), "prompts": prompts("front")}, http.StatusBadRequest, 1004)
	// One to four prompts, each for a distinct view, each a non-empty text of at most 1024 characters.
	f.expect("/generation/edit-multiview", map[string]any{"input": source, "prompts": prompts("front", "front")}, http.StatusBadRequest, 1004)
	f.expect("/generation/edit-multiview", map[string]any{"input": source, "prompts": []map[string]any{{"view": "front", "prompt": ""}}}, http.StatusBadRequest, 1004)
	f.expect("/generation/edit-multiview", map[string]any{"input": source, "prompts": []map[string]any{{"view": "front", "prompt": string(make([]byte, 0)) + repeat("a", 1025)}}}, http.StatusBadRequest, 1004)
	f.expect("/generation/edit-multiview", map[string]any{"input": source, "prompts": []map[string]any{{"view": "top", "prompt": "x"}}}, http.StatusBadRequest, 1004)
	f.expect("/generation/edit-multiview", map[string]any{"input": source, "prompts": prompts("front", "left", "back", "right", "front")}, http.StatusBadRequest, 1004)
}

func repeat(text string, n int) string {
	out := make([]byte, 0, len(text)*n)
	for i := 0; i < n; i++ {
		out = append(out, text...)
	}
	return string(out)
}

// A conversion names a model by URL or file token or by the task id of a generation
// task; a task id is looked up like every other task input.
func TestConvertLooksUpATaskIDInput(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	text := f.generated()
	f.expect("/models/convert", map[string]any{"input": text, "format": "STL"}, http.StatusOK, 0)
	f.expect("/models/convert", map[string]any{"input": f.multiview(), "format": "STL"}, http.StatusOK, 0)
	f.expect("/models/convert", map[string]any{"input": "00000000-0000-4000-8000-0000000000aa", "format": "STL"}, http.StatusNotFound, 2001)
	f.expect("/models/convert", map[string]any{"input": f.imageMultiview(), "format": "STL"}, http.StatusBadRequest, 1004)
	f.expect("/models/convert", map[string]any{"input": "file_abc", "format": "STL"}, http.StatusOK, 0)
	f.expect("/models/convert", map[string]any{"input": "https://example.test/model.glb", "format": "STL"}, http.StatusOK, 0)
}

func TestSegmentCompleteAndSmartSegmentValuesAreChecked(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	text := f.generated()
	segment := f.segmented(text)
	for name, tc := range map[string]struct {
		path string
		body map[string]any
		code int // 0 is accepted
	}{
		"segment v1.0":                 {"/mesh/segment", map[string]any{"input": text, "model": "v1.0-20250506"}, 0},
		"segment unknown model":        {"/mesh/segment", map[string]any{"input": text, "model": "v3.0-20260909"}, 1004},
		"segment granularity detailed": {"/mesh/segment", map[string]any{"input": text, "model": "v2.0-20260430", "segmentation_granularity": "detailed"}, 0},
		"segment granularity unknown":  {"/mesh/segment", map[string]any{"input": text, "model": "v2.0-20260430", "segmentation_granularity": "fine"}, 1004},
		"segment connectivity type":    {"/mesh/segment", map[string]any{"input": text, "model": "v2.0-20260430", "split_by_connectivity": "yes"}, 1004},
		"complete unknown model":       {"/mesh/complete", map[string]any{"input": segment, "model": "v2.0-20260430"}, 1004},
		"complete quick_cap":           {"/mesh/complete", map[string]any{"input": segment, "model": "v1.0-20250506", "completion_mode": "quick_cap"}, 0},
		"complete unknown mode":        {"/mesh/complete", map[string]any{"input": segment, "model": "v1.0-20250506", "completion_mode": "diffusion"}, 1004},
		"complete part_names":          {"/mesh/complete", map[string]any{"input": segment, "model": "v1.0-20250506", "part_names": []string{"tripo_part_0"}}, 0},
		"complete part_names type":     {"/mesh/complete", map[string]any{"input": segment, "model": "v1.0-20250506", "part_names": "tripo_part_0"}, 1004},
		"smartsegment image":           {"/mesh/smartsegment", map[string]any{"input": "file_x", "seg_type": "image", "granularity": "coarse", "hint": "the legs"}, 0},
		"smartsegment fine":            {"/mesh/smartsegment", map[string]any{"input": "file_x", "seg_type": "image", "granularity": "fine"}, 0},
		"smartsegment bad granularity": {"/mesh/smartsegment", map[string]any{"input": "file_x", "seg_type": "image", "granularity": "balanced"}, 1004},
		"smartsegment hint type":       {"/mesh/smartsegment", map[string]any{"input": "file_x", "seg_type": "image", "hint": 4}, 1004},
		"smartsegment model transform": {"/mesh/smartsegment", map[string]any{"input": "file_x", "seg_type": "model", "transform": identity()}, 0},
		"model needs a transform":      {"/mesh/smartsegment", map[string]any{"input": "file_x", "seg_type": "model"}, 1004},
		"transform of 15 numbers":      {"/mesh/smartsegment", map[string]any{"input": "file_x", "seg_type": "model", "transform": identity()[:15]}, 1004},
		"transform of text":            {"/mesh/smartsegment", map[string]any{"input": "file_x", "seg_type": "model", "transform": make16("a")}, 1004},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.code == 0 {
				f.expect(tc.path, tc.body, http.StatusOK, 0)
				return
			}
			f.expect(tc.path, tc.body, http.StatusBadRequest, tc.code)
		})
	}
}

func identity() []any {
	out := make([]any, 16)
	for i := range out {
		out[i] = 0
	}
	for _, i := range []int{0, 5, 10, 15} {
		out[i] = 1
	}
	return out
}

func make16(value any) []any {
	out := make([]any, 16)
	for i := range out {
		out[i] = value
	}
	return out
}

// Smart Segmentation from a task id: the task has to have succeeded (2007) and to hold
// a usable picture or GLB (2006).
func TestSmartSegmentChecksItsSourceTask(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	done := f.generated()
	pending := f.create("/generation/text-to-model", textBody(), key) // never polled: still queued
	splat := f.create("/generation/image-to-splat", map[string]any{"input": "file_x"}, key)
	f.finish(splat, key)
	multiview := f.imageMultiview()
	rigCheck := f.create("/animations/rig-check", map[string]any{"input": done}, key)
	f.finish(rigCheck, key)

	f.expect("/mesh/smartsegment", map[string]any{"input": done, "seg_type": "image"}, http.StatusOK, 0)
	f.expect("/mesh/smartsegment", map[string]any{"input": done, "seg_type": "model", "transform": identity()}, http.StatusOK, 0)
	f.expect("/mesh/smartsegment", map[string]any{"input": multiview, "seg_type": "image"}, http.StatusOK, 0)
	f.expect("/mesh/smartsegment", map[string]any{"input": pending, "seg_type": "image"}, http.StatusBadRequest, 2007)
	f.expect("/mesh/smartsegment", map[string]any{"input": pending, "seg_type": "model", "transform": identity()}, http.StatusBadRequest, 2007)
	// A GLB is needed for the model pipeline; pictures and a splat have none.
	f.expect("/mesh/smartsegment", map[string]any{"input": multiview, "seg_type": "model", "transform": identity()}, http.StatusBadRequest, 2006)
	f.expect("/mesh/smartsegment", map[string]any{"input": splat, "seg_type": "model", "transform": identity()}, http.StatusBadRequest, 2006)
	f.expect("/mesh/smartsegment", map[string]any{"input": rigCheck, "seg_type": "image"}, http.StatusBadRequest, 2006)
	f.expect("/mesh/smartsegment", map[string]any{"input": "00000000-0000-4000-8000-0000000000aa", "seg_type": "image"}, http.StatusNotFound, 2001)
}

func TestTextureRequestValuesAreChecked(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	text := f.generated()
	for name, tc := range map[string]struct {
		extra map[string]any
		code  int
	}{
		"defaults":                       {nil, 0},
		"v3.5 model":                     {map[string]any{"model": "v3.5-20260815", "texture_quality": "extreme"}, 0},
		"v3.0 model":                     {map[string]any{"model": "v3.0-20250812"}, 0},
		"v2.5 model":                     {map[string]any{"model": "v2.5-20250123", "texture_quality": "standard"}, 0},
		"unknown model":                  {map[string]any{"model": "v4.0-20270101"}, 1004},
		"unknown quality":                {map[string]any{"texture_quality": "ultra"}, 1004},
		"fast on the default model":      {map[string]any{"texture_quality": "fast"}, 0},
		"fast on v3.5":                   {map[string]any{"model": "v3.5-20260815", "texture_quality": "fast"}, 0},
		"fast on v2.5":                   {map[string]any{"model": "v2.5-20250123", "texture_quality": "fast"}, 1004},
		"pbr type":                       {map[string]any{"pbr": "yes"}, 1004},
		"bake type":                      {map[string]any{"bake": 1}, 1004},
		"alignment unknown":              {map[string]any{"texture_alignment": "front"}, 1004},
		"text prompt":                    {map[string]any{"texture_prompt": map[string]any{"text": "rusty metal"}}, 0},
		"text with a style image":        {map[string]any{"texture_prompt": map[string]any{"text": "rusty", "style_image": "file_s"}}, 0},
		"image prompt":                   {map[string]any{"texture_prompt": map[string]any{"image": "file_i"}}, 0},
		"images prompt":                  {map[string]any{"texture_prompt": map[string]any{"images": []any{map[string]any{"front": "file_a"}, map[string]any{"left": "file_b"}}}}, 0},
		"text and image are exclusive":   {map[string]any{"texture_prompt": map[string]any{"text": "rusty", "image": "file_i"}}, 1004},
		"text and images are exclusive":  {map[string]any{"texture_prompt": map[string]any{"text": "rusty", "images": []any{}}}, 1004},
		"image and images are exclusive": {map[string]any{"texture_prompt": map[string]any{"image": "file_i", "images": []any{}}}, 1004},
		"style_image needs text":         {map[string]any{"texture_prompt": map[string]any{"image": "file_i", "style_image": "file_s"}}, 1004},
		"texture_prompt is an object":    {map[string]any{"texture_prompt": "rusty"}, 1004},
	} {
		t.Run(name, func(t *testing.T) {
			body := map[string]any{"input": text}
			for field, value := range tc.extra {
				body[field] = value
			}
			if tc.code == 0 {
				f.expect("/models/texture", body, http.StatusOK, 0)
				return
			}
			f.expect("/models/texture", body, http.StatusBadRequest, tc.code)
		})
	}
}

// What a task is billed (billing.go): the pricing page's table. The generation
// price follows Plinth's native_pricing for the same request.
func TestPricesFollowTheOfficialTable(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	credits := func(path string, body map[string]any) float64 {
		t.Helper()
		_, done := f.finish(f.create(path, body, key), key)
		value, _ := field(t, done, "data", "credits_consumed").(float64)
		return value
	}
	text, image, multiview := "/generation/text-to-model", "/generation/image-to-model", "/generation/multiview-to-model"
	for name, tc := range map[string]struct {
		path  string
		model string
		extra map[string]any
		want  float64
	}{
		"text, geometry only":            {text, modelH31, map[string]any{"texture": false, "pbr": false}, 10},
		"text, standard texture":         {text, modelH31, nil, 20},
		"text, HD texture":               {text, modelH31, map[string]any{"texture_quality": "detailed"}, 30},
		"text, ultra texture":            {text, modelH31, map[string]any{"texture_quality": "extreme"}, 40},
		"text, fast texture is standard": {text, modelH31, map[string]any{"texture_quality": "fast"}, 20},
		"text, HD geometry":              {text, modelH31, map[string]any{"geometry_quality": "detailed"}, 40},
		"text, quad and smart low poly":  {text, modelH31, map[string]any{"quad": true, "smart_low_poly": true, "face_limit": 5000}, 35},
		"text, parts":                    {text, modelH31, map[string]any{"generate_parts": true, "texture": false, "pbr": false}, 30},
		"image, geometry only":           {image, modelH31, map[string]any{"texture": false, "pbr": false}, 20},
		"image, standard texture":        {image, modelH31, nil, 30},
		"multiview, production defaults": {multiview, modelH31, map[string]any{"texture_quality": "extreme", "geometry_quality": "detailed", "face_limit": 2000000}, 70},
		"P2 text, geometry only":         {text, modelP2, map[string]any{"texture": false, "pbr": false}, 100},
		"P2 text, standard texture":      {text, modelP2, nil, 110},
		"P2 text, HD texture":            {text, modelP2, map[string]any{"texture_quality": "detailed"}, 120},
		"P2 text, ultra texture":         {text, modelP2, map[string]any{"texture_quality": "extreme"}, 130},
		"P2 image, quad adds nothing":    {image, modelP2, map[string]any{"quad": true}, 110},
	} {
		t.Run(name, func(t *testing.T) {
			if got := credits(tc.path, generationBody(tc.path, tc.model, tc.extra)); got != tc.want {
				t.Fatalf("credits_consumed = %v, want %v", got, tc.want)
			}
		})
	}
	// The P1 price is not on the pricing page: the fake keeps the sample's placeholder.
	if got := credits(text, generationBody(text, modelP1, nil)); got == 110 || got == 100 {
		t.Fatalf("P1 is not priced by the fake, got %v", got)
	}

	source := f.generated()
	segment := f.segmented(source)
	for name, tc := range map[string]struct {
		path string
		body map[string]any
		want float64
	}{
		"texture fast":          {"/models/texture", map[string]any{"input": source, "texture_quality": "fast"}, 10},
		"texture standard":      {"/models/texture", map[string]any{"input": source}, 10},
		"texture HD":            {"/models/texture", map[string]any{"input": source, "texture_quality": "detailed"}, 20},
		"texture 8K":            {"/models/texture", map[string]any{"input": source, "texture_quality": "extreme"}, 30},
		"segmentation":          {"/mesh/segment", map[string]any{"input": source, "model": "v2.0-20260430"}, 40},
		"completion":            {"/mesh/complete", map[string]any{"input": segment, "model": "v1.0-20250506"}, 50},
		"quick cap":             {"/mesh/complete", map[string]any{"input": segment, "model": "v1.0-20250506", "completion_mode": "quick_cap"}, 30},
		"smart segment, image":  {"/mesh/smartsegment", map[string]any{"input": "file_x", "seg_type": "image"}, 85},
		"smart segment, model":  {"/mesh/smartsegment", map[string]any{"input": "file_x", "seg_type": "model", "transform": identity()}, 55},
		"image to multiview":    {"/generation/image-to-multiview", map[string]any{"input": "file_x"}, 10},
		"retarget, one preset":  {"/animations/retarget", map[string]any{"input": f.rigged(rigModelV3, ""), "animation": "preset:walk"}, 10},
		"retarget, two presets": {"/animations/retarget", map[string]any{"input": f.rigged(rigModelV3, ""), "animations": []string{"preset:walk", "preset:run"}}, 20},
	} {
		t.Run(name, func(t *testing.T) {
			if got := credits(tc.path, tc.body); got != tc.want {
				t.Fatalf("credits_consumed = %v, want %v", got, tc.want)
			}
		})
	}
	edit := credits("/generation/edit-multiview", map[string]any{"input": f.imageMultiview(), "prompts": []map[string]any{
		{"view": "front", "prompt": "a"}, {"view": "back", "prompt": "b"}, {"view": "left", "prompt": "c"},
	}})
	if edit != 15 {
		t.Fatalf("edit multiview of three views = %v, want 15 (5 per edited image)", edit)
	}
}
