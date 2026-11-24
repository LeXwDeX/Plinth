package meshyopenapiv2

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/core"
)

// #542 (I8): the Text to 3D page's 400 list and the price of each stage, read
// from https://docs.meshy.ai/en/api/text-to-3d and /pricing on 2026-11-24.
// Every refusal text is constructed; none was requested from the real API.

func status(t *testing.T, f interface {
	Do(method, path, key string, body any, headers ...string) (int, http.Header, map[string]any)
}, body map[string]any) int {
	t.Helper()
	code, _, _ := f.Do("POST", Prefix+"/text-to-3d", key, body)
	return code
}

func TestPreviewCreateRulesOfThePage(t *testing.T) {
	f := newFixture(t)
	long := strings.Repeat("字", 801)
	for name, tc := range map[string]struct {
		body map[string]any
		want int
	}{
		"no prompt":                  {map[string]any{"mode": "preview"}, http.StatusBadRequest},
		"blank prompt":               {map[string]any{"mode": "preview", "prompt": "  "}, http.StatusBadRequest},
		"prompt over 800 characters": {map[string]any{"mode": "preview", "prompt": long}, http.StatusBadRequest},
		"prompt at 800 characters":   {map[string]any{"mode": "preview", "prompt": long[:800*3]}, http.StatusAccepted},
		"unknown mode":               {map[string]any{"mode": "final", "prompt": "p"}, http.StatusBadRequest},
		"unknown ai_model":           {map[string]any{"mode": "preview", "prompt": "p", "ai_model": "meshy-9"}, http.StatusBadRequest},
		"unknown model_type":         {map[string]any{"mode": "preview", "prompt": "p", "model_type": "organic"}, http.StatusBadRequest},
		"unknown format":             {map[string]any{"mode": "preview", "prompt": "p", "target_formats": []string{"glb", "blend"}}, http.StatusBadRequest},
		"formats not an array":       {map[string]any{"mode": "preview", "prompt": "p", "target_formats": "glb"}, http.StatusBadRequest},
		"all six formats":            {map[string]any{"mode": "preview", "prompt": "p", "target_formats": []string{"glb", "obj", "fbx", "stl", "usdz", "3mf"}}, http.StatusAccepted},
		"a flag that is not boolean": {map[string]any{"mode": "preview", "prompt": "p", "auto_size": "yes"}, http.StatusBadRequest},
		"origin_at outside the enum": {map[string]any{"mode": "preview", "prompt": "p", "origin_at": "top"}, http.StatusBadRequest},
		"meshy-n1 is not a preview":  {map[string]any{"mode": "preview", "prompt": "p", "ai_model": "meshy-n1"}, http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			if got := status(t, f, tc.body); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}

// geometry_resolution follows the model: "2k needs meshy-7.1, latest, or
// meshy-7. 4k needs meshy-7.1 or latest. Other models return 400."
func TestGeometryResolutionFollowsTheModel(t *testing.T) {
	f := newFixture(t)
	preview := func(model, resolution string, extra map[string]any) map[string]any {
		body := map[string]any{"mode": "preview", "prompt": "p"}
		if model != "" {
			body["ai_model"] = model
		}
		if resolution != "" {
			body["geometry_resolution"] = resolution
		}
		for key, value := range extra {
			body[key] = value
		}
		return body
	}
	for name, tc := range map[string]struct {
		body map[string]any
		want int
	}{
		"4k on meshy-7.1":                  {preview("meshy-7.1", "4k", nil), http.StatusAccepted},
		"4k on latest":                     {preview("latest", "4k", nil), http.StatusAccepted},
		"4k with no model (latest)":        {preview("", "4k", nil), http.StatusAccepted},
		"4k on meshy-7":                    {preview("meshy-7", "4k", nil), http.StatusBadRequest},
		"4k on meshy-6":                    {preview("meshy-6", "4k", nil), http.StatusBadRequest},
		"2k on meshy-7":                    {preview("meshy-7", "2k", nil), http.StatusAccepted},
		"2k on meshy-6":                    {preview("meshy-6", "2k", nil), http.StatusBadRequest},
		"2k on meshy-6-lite":               {preview("meshy-6-lite", "2k", nil), http.StatusBadRequest},
		"standard on any model":            {preview("meshy-6", "standard", nil), http.StatusAccepted},
		"2k on a Smart Topology model":     {preview("meshy-t2.5", "2k", nil), http.StatusBadRequest},
		"2k on Smart Topology by type":     {preview("", "2k", map[string]any{"model_type": "smart-topology"}), http.StatusBadRequest},
		"a resolution outside the enum":    {preview("meshy-7.1", "8k", nil), http.StatusBadRequest},
		"a Smart Topology preview":         {preview("meshy-t2.5", "", map[string]any{"model_type": "smart-topology", "target_polycount": 15000}), http.StatusAccepted},
		"quad on a Smart Topology model":   {preview("meshy-t2", "", map[string]any{"model_type": "smart-topology", "topology": "quad"}), http.StatusBadRequest},
		"target_polycount over t2 limit":   {preview("meshy-t2", "", map[string]any{"model_type": "smart-topology", "target_polycount": 15001}), http.StatusBadRequest},
		"target_polycount under 100":       {preview("meshy-7.1", "", map[string]any{"should_remesh": true, "target_polycount": 99}), http.StatusBadRequest},
		"standard remesh at the ceiling":   {preview("meshy-7.1", "", map[string]any{"should_remesh": true, "target_polycount": 300000}), http.StatusAccepted},
		"standard remesh over the ceiling": {preview("meshy-7.1", "", map[string]any{"should_remesh": true, "target_polycount": 300001}), http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			if got := status(t, f, tc.body); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRefineCreateRulesOfThePage(t *testing.T) {
	f := newFixture(t)
	preview := create(t, f, map[string]any{"mode": "preview", "prompt": "p"})
	finish(t, f, preview)
	// A preview that is still queued is "not ready".
	pending := create(t, f, map[string]any{"mode": "preview", "prompt": "p"})
	refine := create(t, f, map[string]any{"mode": "refine", "preview_task_id": preview})
	finish(t, f, refine)

	for name, tc := range map[string]struct {
		body map[string]any
		want int
	}{
		"no preview_task_id":                  {map[string]any{"mode": "refine"}, http.StatusBadRequest},
		"a preview_task_id that is blank":     {map[string]any{"mode": "refine", "preview_task_id": " "}, http.StatusBadRequest},
		"a refine task as the source":         {map[string]any{"mode": "refine", "preview_task_id": refine}, http.StatusBadRequest},
		"a preview that has not succeeded":    {map[string]any{"mode": "refine", "preview_task_id": pending}, http.StatusBadRequest},
		"a succeeded preview":                 {map[string]any{"mode": "refine", "preview_task_id": preview}, http.StatusAccepted},
		"texture_resolution 8k":               {map[string]any{"mode": "refine", "preview_task_id": preview, "texture_resolution": "8k"}, http.StatusAccepted},
		"texture_resolution outside the enum": {map[string]any{"mode": "refine", "preview_task_id": preview, "texture_resolution": "1k"}, http.StatusBadRequest},
		"texture_prompt over 800 characters":  {map[string]any{"mode": "refine", "preview_task_id": preview, "texture_prompt": strings.Repeat("a", 801)}, http.StatusBadRequest},
		"enable_pbr not boolean":              {map[string]any{"mode": "refine", "preview_task_id": preview, "enable_pbr": "true"}, http.StatusBadRequest},
		"no ai_model at all":                  {map[string]any{"mode": "refine", "preview_task_id": preview, "texture_resolution": "2k"}, http.StatusAccepted},
		// The real refine of 2026-11-20 sent meshy-7.1 and got a 202.
		"a generation model name": {map[string]any{"mode": "refine", "preview_task_id": preview, "ai_model": "meshy-7.1"}, http.StatusAccepted},
		"an unknown ai_model":     {map[string]any{"mode": "refine", "preview_task_id": preview, "ai_model": "meshy-9"}, http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			if got := status(t, f, tc.body); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}

// consumed_credits follows the request (the Pricing page) in every state that
// reports one, and the real captures of 2026-11-20 and 2026-11-22 fix the
// Meshy 7.1 prices: a 4k preview 25, a 2k refine 10, an 8k refine 15.
func TestConsumedCreditsFollowThePricingPage(t *testing.T) {
	f := newFixture(t)
	for name, tc := range map[string]struct {
		preview map[string]any
		refine  map[string]any
		want    [2]float64
	}{
		"meshy-7.1 4k preview and a 2k refine (real 25 and 10)": {
			map[string]any{"ai_model": "meshy-7.1", "geometry_resolution": "4k"},
			map[string]any{"texture_resolution": "2k"}, [2]float64{25, 10}},
		"meshy-7.1 standard preview and an 8k refine (real 15)": {
			map[string]any{"ai_model": "meshy-7.1"},
			map[string]any{"texture_resolution": "8k"}, [2]float64{20, 15}},
		"meshy-7 2k preview and a default refine": {
			map[string]any{"ai_model": "meshy-7", "geometry_resolution": "2k"},
			nil, [2]float64{25, 10}},
		"meshy-6-lite preview": {
			map[string]any{"ai_model": "meshy-6-lite"}, map[string]any{"texture_resolution": "4k"}, [2]float64{5, 10}},
		"meshy-t2.5 preview": {
			map[string]any{"ai_model": "meshy-t2.5", "model_type": "smart-topology"}, nil, [2]float64{5, 10}},
		"the deprecated ultra_mode is a 2k surcharge": {
			map[string]any{"ai_model": "meshy-7.1", "ultra_mode": true}, nil, [2]float64{25, 10}},
	} {
		t.Run(name, func(t *testing.T) {
			previewBody := map[string]any{"mode": "preview", "prompt": "p"}
			for key, value := range tc.preview {
				previewBody[key] = value
			}
			preview := create(t, f, previewBody)
			// The price is reported from the first poll on.
			_, _, first := f.Do("GET", Prefix+"/text-to-3d/"+preview, key, nil)
			if first["consumed_credits"] != tc.want[0] {
				t.Fatalf("PENDING consumed_credits = %v, want %v", first["consumed_credits"], tc.want[0])
			}
			_, previewDone := finish(t, f, preview)
			if previewDone["consumed_credits"] != tc.want[0] {
				t.Fatalf("preview consumed_credits = %v, want %v", previewDone["consumed_credits"], tc.want[0])
			}
			refineBody := map[string]any{"mode": "refine", "preview_task_id": preview}
			for key, value := range tc.refine {
				refineBody[key] = value
			}
			_, refineDone := finish(t, f, create(t, f, refineBody))
			if refineDone["consumed_credits"] != tc.want[1] {
				t.Fatalf("refine consumed_credits = %v, want %v", refineDone["consumed_credits"], tc.want[1])
			}
		})
	}
	failed := create(t, f, map[string]any{"mode": "preview", "prompt": "p"}, "X-Fake-Task-Error", "timeout")
	if _, body := finish(t, f, failed); body["status"] != core.StatusFailed || body["consumed_credits"] != float64(0) {
		t.Fatalf("a failed task is refunded: %v", body)
	}
	canceled := create(t, f, map[string]any{"mode": "preview", "prompt": "p"}, "X-Fake-Scenario", "task-canceled")
	if _, body := finish(t, f, canceled); body["consumed_credits"] != float64(0) {
		t.Fatalf("a canceled task is priced 0: %v", body)
	}
}

// A Standard task carries no geometry_resolution (the page: "absent for
// standard") and a 4k one carries it with ultra_mode set.
func TestGeometryResolutionIsAbsentForAStandardPreview(t *testing.T) {
	f := newFixture(t)
	_, standard := finish(t, f, create(t, f, map[string]any{"mode": "preview", "prompt": "p", "geometry_resolution": "standard"}))
	if _, has := standard["geometry_resolution"]; has || standard["ultra_mode"] != false {
		t.Fatalf("standard preview = %v %v", standard["geometry_resolution"], standard["ultra_mode"])
	}
	_, ultra := finish(t, f, create(t, f, map[string]any{"mode": "preview", "prompt": "p", "geometry_resolution": "4k"}))
	if ultra["geometry_resolution"] != "4k" || ultra["ultra_mode"] != true {
		t.Fatalf("4k preview = %v %v", ultra["geometry_resolution"], ultra["ultra_mode"])
	}
}
