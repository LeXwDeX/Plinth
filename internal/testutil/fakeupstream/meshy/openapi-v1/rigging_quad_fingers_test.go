package meshyopenapiv1

import (
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/core"
	meshyopenapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/openapi-v2"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

// #294: the real API refused include_fingers for the rigging of a model made with
// quad topology (observed 2026-11-12 on a quad-topology model): HTTP 400 with a
// message-only body, "include_fingers is not available for a quad-topology model.
// Set include_fingers to false...". The fake follows the source task's requests
// back to the topology its model was made with and refuses the same combination.

const realFingersQuadMessage = "include_fingers is not available for a quad-topology model. Set include_fingers to false"

// refusesFingers posts a rigging request with finger bones for a source task and
// reports whether the fake refused it, checking the refusal is the real shape.
func (f *fixture) refusesFingers(t *testing.T, source string) bool {
	t.Helper()
	status, _, body := f.Do("POST", f.v1("/rigging"), key, map[string]any{"input_task_id": source, "include_fingers": true})
	if status == http.StatusAccepted {
		return false
	}
	if status != http.StatusBadRequest {
		t.Fatalf("status %d body %v", status, body)
	}
	message, _ := body["message"].(string)
	if len(body) != 1 || message != realFingersQuadMessage {
		t.Fatalf("the refusal must be the real message-only body, got %v", body)
	}
	return true
}

func TestRiggingRefusesFingersOnAQuadTopologySource(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	generate := func(extra map[string]any) string {
		body := map[string]any{"image_url": "https://example.test/a.png", "should_remesh": true, "target_polycount": 8000}
		for name, value := range extra {
			body[name] = value
		}
		id := f.create("/image-to-3d", body)
		f.finish("/image-to-3d", id, key)
		return id
	}
	quad := generate(map[string]any{"topology": "quad"})
	triangle := generate(map[string]any{"topology": "triangle"})
	unnamed := generate(map[string]any{"should_remesh": false})

	t.Run("a quad source with fingers is refused", func(t *testing.T) {
		if !f.refusesFingers(t, quad) {
			t.Fatal("the quad-topology source was accepted with include_fingers")
		}
	})
	t.Run("a quad source without fingers rigs", func(t *testing.T) {
		for _, body := range []map[string]any{
			{"input_task_id": quad},
			{"input_task_id": quad, "include_fingers": false},
			{"input_task_id": quad, "include_fingers": false, "skeleton_template": "ue5_mannequin"},
		} {
			rig := f.create("/rigging", body)
			if statuses, _ := f.finish("/rigging", rig, key); statuses[len(statuses)-1] != core.StatusSucceeded {
				t.Fatalf("%v: flow = %v", body, statuses)
			}
		}
	})
	t.Run("a triangle source keeps its fingers", func(t *testing.T) {
		if f.refusesFingers(t, triangle) {
			t.Fatal("a triangle-topology source was refused")
		}
	})
	t.Run("a source whose requests name no topology is accepted", func(t *testing.T) {
		if f.refusesFingers(t, unnamed) {
			t.Fatal("a source with no named topology was refused; the fake applies no API default")
		}
	})
	t.Run("a remesh made quad is refused and one made triangle is not", func(t *testing.T) {
		remeshQuad := f.create("/remesh", map[string]any{"input_task_id": triangle, "topology": "quad", "target_polycount": 8000})
		f.finish("/remesh", remeshQuad, key)
		remeshTriangle := f.create("/remesh", map[string]any{"input_task_id": quad, "topology": "triangle", "target_polycount": 8000})
		f.finish("/remesh", remeshTriangle, key)
		if !f.refusesFingers(t, remeshQuad) || f.refusesFingers(t, remeshTriangle) {
			t.Fatal("the remesh's own topology did not decide")
		}
	})
	t.Run("a retexture follows its input task back to the topology", func(t *testing.T) {
		retextured := f.create("/retexture", map[string]any{"input_task_id": quad, "text_style_prompt": "p"})
		f.finish("/retexture", retextured, key)
		if !f.refusesFingers(t, retextured) {
			t.Fatal("a retexture of a quad-topology model was accepted with include_fingers")
		}
	})
	t.Run("a model upload cannot be judged and other kinds do not care", func(t *testing.T) {
		upload := f.create("/rigging", map[string]any{"model_url": "data:application/octet-stream;base64,AAAA", "include_fingers": true})
		f.finish("/rigging", upload, key)
		f.create("/remesh", map[string]any{"input_task_id": quad, "include_fingers": true})
	})
}

// A text-to-3d model is two tasks, and the refine repeats nothing of the remesh
// options (the Plinth adapter repeats them, a direct caller need not): the
// topology is the preview's.
func TestRiggingRefusesFingersOnARefinedQuadModel(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	v2 := func(body map[string]any) string {
		status, _, response := f.Do("POST", meshyopenapiv2.Prefix+"/text-to-3d", key, body)
		if status != http.StatusAccepted {
			t.Fatalf("text-to-3d %v = %d %v", body, status, response)
		}
		id := testkit.Field(t, response, "result").(string)
		for range 8 {
			_, _, polled := f.Do("GET", meshyopenapiv2.Prefix+"/text-to-3d/"+id, key, nil)
			if state := polled["status"]; state == core.StatusSucceeded {
				return id
			}
		}
		t.Fatalf("text-to-3d %s never succeeded", id)
		return ""
	}
	preview := v2(map[string]any{"mode": "preview", "prompt": "a robot", "should_remesh": true, "topology": "quad", "target_polycount": 8000})
	refine := v2(map[string]any{"mode": "refine", "preview_task_id": preview})
	if !f.refusesFingers(t, refine) {
		t.Fatal("the refine of a quad preview was accepted with include_fingers")
	}
	plain := v2(map[string]any{"mode": "preview", "prompt": "a robot"})
	if f.refusesFingers(t, v2(map[string]any{"mode": "refine", "preview_task_id": plain})) {
		t.Fatal("the refine of a preview with no named topology was refused")
	}
}
