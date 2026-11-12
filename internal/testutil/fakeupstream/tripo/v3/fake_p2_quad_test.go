package tripov3

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// The P2 request body is the shared v3 generation body plus the documented P
// Series fields (https://developers.tripo3d.ai/en/docs/generation-image-to-model/p:
// quad, and a face_limit of 48-25000 when quad is on). The fake accepts it on the
// three endpoints P2 uses. What a quad=true task delivers is real since
// 2026-11-12 (#294): an FBX and no GLB (p2_quad.go); the topology of the file is
// not modelled.
func p2Body(extra map[string]any) map[string]any {
	body := map[string]any{
		"model": "P2-20260801", "texture": true, "pbr": true, "export_uv": true, "delight": true,
		"texture_quality": "standard", "texture_version": "v3.5-20260815",
	}
	for name, value := range extra {
		body[name] = value
	}
	return body
}

// p2Endpoints are the three generation endpoints P2 uses, with the fields each
// needs on top of the P2 body.
func p2Endpoints() map[string]map[string]any {
	return map[string]map[string]any{
		"/generation/image-to-model": p2Body(map[string]any{"input": "file_image"}),
		"/generation/text-to-model":  p2Body(map[string]any{"prompt": "a game-ready knight"}),
		"/generation/multiview-to-model": p2Body(map[string]any{
			"inputs": []any{map[string]any{"front": "t1"}, map[string]any{"left": "t2"}},
		}),
	}
}

// modelPath is the path (without the signed query) of a task's model_url.
func modelPath(f *fixture, done map[string]any) string {
	return strings.Split(f.outputURL(done, "model_url"), "?")[0]
}

func TestP2QuadRequestBodiesAreAcceptedOnTheGenerationEndpoints(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for path, body := range p2Endpoints() {
		body["quad"] = true
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

// A real P2 task with quad=true delivered an FBX and no GLB (2026-11-12,
// #294): a model of the role model with no variant, served as an opaque binary,
// that the stage preview and inspect_model cannot read. A P2 task without quad
// output still delivers its GLB.
func TestP2QuadOutputDeliversAnFBXAndNoGLB(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for path, body := range p2Endpoints() {
		t.Run(path+" with quad", func(t *testing.T) {
			quad := p2Body(body)
			quad["quad"], quad["face_limit"] = true, 10000
			_, done := f.finish(f.create(path, quad, key), key)

			if got := modelPath(f, done); !strings.HasSuffix(got, ".fbx") {
				t.Fatalf("model_url = %s, want an FBX", got)
			}
			// No model of the task is a GLB: every URL of the output that is not a
			// picture names the FBX.
			output := field(t, done, "data", "output").(map[string]any)
			for name, value := range output {
				if !strings.HasSuffix(name, "_url") {
					continue
				}
				if url, _ := value.(string); strings.HasSuffix(strings.Split(url, "?")[0], ".glb") {
					t.Fatalf("output.%s is a GLB: %s", name, url)
				}
			}
			status, contentType, data := f.download(f.outputURL(done, "model_url"))
			if status != http.StatusOK || contentType != ArtifactContentType || len(data) == 0 {
				t.Fatalf("the FBX download = %d %q %d bytes", status, contentType, len(data))
			}
			// The file is a binary FBX the delivery check accepts as a model with no
			// variant and the opaque media type the adapter gives it.
			if err := deliveryErr(t, data, "model", "", "application/octet-stream"); err != nil {
				t.Fatalf("the quad FBX is rejected by the delivery check: %v", err)
			}
			if !strings.HasPrefix(string(data), "Kaydara FBX Binary") {
				t.Fatalf("the quad model is not a binary FBX: %q", string(data[:24]))
			}
		})
		t.Run(path+" without quad", func(t *testing.T) {
			for _, quad := range []any{nil, false} {
				plain := p2Body(body)
				plain["face_limit"] = 10000
				if quad != nil {
					plain["quad"] = quad
				}
				_, done := f.finish(f.create(path, plain, key), key)
				if got := modelPath(f, done); !strings.HasSuffix(got, ".glb") {
					t.Fatalf("quad = %v: model_url = %s, want a GLB", quad, got)
				}
			}
		})
	}
}

// Only the P series was observed delivering an FBX for quad output: a quad
// request to an H-series model keeps its GLB, and quad output on another
// endpoint kind is not the generation's FBX.
func TestQuadOutputOfOtherModelFamiliesStaysAGLB(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	body := map[string]any{"model": "v3.1-20260211", "prompt": "a robot", "quad": true, "face_limit": 10000}
	_, done := f.finish(f.create("/generation/text-to-model", body, key), key)
	if got := modelPath(f, done); !strings.HasSuffix(got, ".glb") {
		t.Fatalf("a v3.1 quad task delivered %s, want the GLB the fake always gave it", got)
	}
}
