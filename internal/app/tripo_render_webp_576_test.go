package app

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #576: Tripo text-to-3D at the product's defaults (texture at extreme quality,
// detailed geometry, six export formats) succeeded on dev-test with every
// format ready, but its render (rendered_image, a WebP) was refused by the
// asset store's generic address scan, and the background thumbnail slot stayed
// pending: resources_pending was still true 40 minutes later. The whole stack
// runs against the fake upstream, whose render for such a request is now a
// WebP of the shape the old scan refused: the real Catalog and Tripo adapter,
// the kernel and the local asset store with its delivery checks. The render
// has to be stored as a WebP and the task's resources have to settle.
func TestTripoDefaultTextTaskStoresItsWebPRenderAndSettles576(t *testing.T) {
	harness, _, log := fakeStackHarness(t, func(origin string) map[string]string {
		return map[string]string{"tripo_base_url": origin + "/tripo/v3", "tripo_api_key": fakeUpstreamKeyPrefix + "test-key"}
	}, "/generation/text-to-model")
	accepted := submitStep(t, harness, generation.TextTo3D, "tripo/tripo-v3.1-full/text-to-3d",
		`{"prompt":"a lantern","authorize_extra_credits":true}`, "render-webp-576", nil)
	task := harness.driveMixedPostSuccess(t, "owner-acme", harness.runUntilTerminal(t, "owner-acme", accepted.TaskID).ID)
	if task.State != generation.TaskSucceeded || task.Failure != nil {
		t.Fatalf("state = %s, failure = %#v", task.State, task.Failure)
	}
	requests := log.bodies("/generation/text-to-model")
	if len(requests) != 1 || requests[0]["texture_quality"] != "extreme" || requests[0]["geometry_quality"] != "detailed" {
		t.Fatalf("text-to-model requests = %v, want one at extreme texture and detailed geometry", requests)
	}
	renders := 0
	for _, asset := range task.Assets {
		if asset.Role == "thumbnail" && asset.Variant == "rendered_image" {
			renders++
			if asset.MediaType != "image/webp" {
				t.Errorf("the render is stored as %s, want image/webp", asset.MediaType)
			}
		}
	}
	if renders != 1 {
		t.Fatalf("rendered_image thumbnails = %d, want 1: %+v", renders, task.Assets)
	}
	for key, state := range task.Resources {
		if state.State != generation.ResourceReady {
			t.Errorf("slot %s = %+v, want ready", key, state)
		}
	}
	if generation.ResourcesPending(task.Resources) {
		t.Fatalf("resources_pending stays true: %+v", task.Resources)
	}
}
