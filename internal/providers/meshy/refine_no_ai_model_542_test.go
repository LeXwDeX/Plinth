package meshy_test

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #542: the Text to 3D page's refine table has no generation model (its ai_model
// is the texture model "inherited from preview"), and the Catalog declares the
// field omitted for a refine. Against the Meshy fake, which now enforces the
// preview and refine rules of that page, the Adapter's preview carries the
// model and the 4k geometry, its refine carries neither the model nor the
// geometry resolution, and the fake accepts both and delivers.
func TestTextTo3DRefineBodyOmitsAIModelAgainstTheFake542(t *testing.T) {
	upstream := newRefineRefusingUpstream(t, fakeupstream.ScenarioNone, 0)
	adapter, request := startRefineTask(t, upstream)
	ctx := context.Background()
	for range 12 {
		observed, err := adapter.Observe(ctx, request)
		if err != nil {
			t.Fatalf("the fake refused an Adapter request: %v", err)
		}
		if observed.Phase == generation.UpstreamCompleted {
			bodies := upstream.createBodies()
			if len(bodies) != 2 {
				t.Fatalf("create bodies = %d, want preview and refine", len(bodies))
			}
			preview, refine := bodies[0], bodies[1]
			if preview["mode"] != "preview" || preview["ai_model"] != "meshy-7.1" || preview["geometry_resolution"] != "4k" {
				t.Fatalf("preview body = %#v", preview)
			}
			if refine["mode"] != "refine" || refine["preview_task_id"] == "" {
				t.Fatalf("refine body = %#v", refine)
			}
			for _, field := range []string{"ai_model", "geometry_resolution"} {
				if _, present := refine[field]; present {
					t.Fatalf("refine body carries %s: %#v", field, refine)
				}
			}
			return
		}
		request.Opaque = observed.Opaque
	}
	t.Fatal("the refine never completed")
}
