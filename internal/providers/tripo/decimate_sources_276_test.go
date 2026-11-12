package tripo

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #276: the baked decimation names its source Tasks by Offering. The Retopology
// page (read 2026-11-12) lists text, image and multiview generation tasks as the
// task input, so a Tripo v3.1 multiview result runs; it says nothing about a
// P-series task, so a P2 result is refused by the declared Offering list before
// any request is made. The real Adapter runs against the Tripo v3 fake, which
// checks the decimation request against the same page (its model, face_limit and
// smart_uv rules, and which upstream tasks can be a source).
func TestDecimationTakesMultiviewSourcesAndRefusesP2BeforeAnyRequest(t *testing.T) {
	stack := newRigStack(t)
	image := stack.createDirect("generation/image-to-model", `{"model":"v3.1-20260211","input":"file_x"}`)
	multiview := stack.createDirect("generation/multiview-to-model",
		`{"model":"v3.1-20260211","inputs":[{"front":"file_front"},{"left":"file_left"}]}`)
	for _, id := range []string{image, multiview} {
		stack.finish(id)
	}

	const decimate = "tripo/geometry/decimate-triangle"
	for _, tc := range []struct {
		name       string
		offeringID generation.OfferingID
		capability generation.Capability
		upstream   string
		accepted   bool
	}{
		{"text to 3D", "tripo/tripo-v3.1-full/text-to-3d", generation.TextTo3D, stack.source, true},
		{"image to 3D", "tripo/tripo-v3.1-full/image-to-3d", generation.ImageTo3D, image, true},
		{"multiview", "tripo/tripo-v3.1-multiview/image-to-3d", generation.ImageTo3D, multiview, true},
		{"P2 text to 3D", "tripo/tripo-p2/text-to-3d", generation.TextTo3D, stack.source, false},
		{"P2 image to 3D", "tripo/tripo-p2/image-to-3d", generation.ImageTo3D, image, false},
		{"P2 multiview", "tripo/tripo-p2-multiview/image-to-3d", generation.ImageTo3D, multiview, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offering, input := stack.normalize(generation.Remesh, decimate, `{"source_task":"source","face_limit":8000,"authorize_extra_credits":true}`)
			before := len(stack.recorder.posts("mesh/decimate"))
			accepted, err := stack.adapter.Start(generation.WithDispatchCheckpointer(context.Background(), &rigCheckPermit{}), generation.StartRequest{
				OwnerID: "owner", TaskID: "decimate-task", AttemptID: "decimate-attempt", SubmissionKey: "decimate-key",
				Offering: offering, Input: input,
				TaskDependencies: map[string]generation.TaskDependency{"source_task": {
					TaskID: "source", OfferingID: tc.offeringID, Capability: tc.capability, ProviderID: "tripo",
					UpstreamJob: generation.UpstreamJob{ProviderID: "tripo", RemoteID: tc.upstream},
				}},
			})
			posts := stack.recorder.posts("mesh/decimate")[before:]
			if !tc.accepted {
				if err == nil {
					t.Fatalf("the Adapter accepted a %s source", tc.name)
				}
				if len(posts) != 0 {
					t.Fatalf("a refused source still reached the upstream: %q", posts)
				}
				return
			}
			// The wire is the decimation's own: the source's upstream task id and the
			// fixed baked, unwrapped triangle request at the requested face count.
			if err != nil || accepted.Job == nil {
				t.Fatalf("the Adapter or the fake refused a %s source: %v", tc.name, err)
			}
			want := `{"bake":true,"face_limit":8000,"input":"` + tc.upstream + `","model":"v3.0","quad":false,"smart_uv":true}`
			if len(posts) != 1 || posts[0] != want {
				t.Fatalf("posts = %q, want exactly %s", posts, want)
			}
			task := stack.finish(accepted.Job.RemoteID)
			if task["status"] != "success" || task["type"] != "highpoly_to_lowpoly" {
				t.Fatalf("the fake's decimation = %v", task)
			}
		})
	}
}
