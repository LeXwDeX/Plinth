package tripov3

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #275: the game character purpose asks Tripo P2 for quad output. The P2 request
// body is the shared v3 generation body plus the documented P Series fields
// (https://developers.tripo3d.ai/en/docs/generation-image-to-model/p: quad, and
// a face_limit of 48-25000 when quad is on). The fake accepts it on the three
// endpoints P2 uses and finishes the task with the same fixed model as any
// other: it promises structure, not realism, so neither the topology nor the
// delivered format changes with quad. What a real quad=true task delivers has
// no sample yet (docs/architecture.md, Tripo P2).
func TestP2QuadRequestBodiesAreAcceptedOnTheGenerationEndpoints(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	p2 := func(extra map[string]any) map[string]any {
		body := map[string]any{
			"model": "P2-20260801", "texture": true, "pbr": true, "export_uv": true, "delight": true,
			"texture_quality": "standard", "texture_version": "v3.5-20260815", "quad": true,
		}
		for name, value := range extra {
			body[name] = value
		}
		return body
	}
	endpoints := map[string]map[string]any{
		"/generation/image-to-model": p2(map[string]any{"input": "file_image"}),
		"/generation/text-to-model":  p2(map[string]any{"prompt": "a game-ready knight"}),
		"/generation/multiview-to-model": p2(map[string]any{
			"inputs": []any{map[string]any{"front": "t1"}, map[string]any{"left": "t2"}},
		}),
	}
	for path, body := range endpoints {
		for _, faces := range []int{10000, 25000} {
			body["face_limit"] = faces
			id := f.create(path, body, key)
			statuses, done := f.finish(id, key)
			if last := statuses[len(statuses)-1]; last != statusSuccess {
				t.Fatalf("%s with %d quad faces ended %q", path, faces, last)
			}
			if url := f.outputURL(done, "model_url"); url == "" {
				t.Fatalf("%s with %d quad faces delivered no model_url", path, faces)
			}
		}
	}
}
