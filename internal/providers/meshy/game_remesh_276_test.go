package meshy_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	meshyopenapiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/openapi-v1"
	meshyopenapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/openapi-v2"
)

// #276: what Meshy 7.1 sends when a game purpose generates with it. The values
// come from the shipped purposes (their parameter_overrides per target platform),
// the request goes through the Catalog's normalization and the real adapter to
// the fake Meshy upstream, which checks the remesh options the way the Text to 3D
// and Image to 3D pages (read 2026-11-12) document them (a face count outside
// the model's range, quads from a Smart Topology model and a topology that is
// neither quad nor triangle answer 400). The pages do not say how a quad counts
// toward target_polycount or whether the texture of a remeshed task is generated
// on the remeshed mesh. One real quad task (2026-11-12, #294) delivered about 2.25
// triangles per target face, which is why the character's targets are 0.4 of the
// budget (the Catalog test checks the arithmetic); nothing real backs the texture
// question, so nothing here asserts it.

// postRecorder is the fake Meshy upstream that keeps every POST body in order
// (the preview and the refine of a text-to-3d share one route).
type postRecorder struct {
	server *httptest.Server
	mu     sync.Mutex
	posts  map[string][]map[string]any
}

func newPostRecorder(t *testing.T) *postRecorder {
	t.Helper()
	fake, err := fakeupstream.New(fakeupstream.Config{}, meshyopenapiv1.Version(), meshyopenapiv2.Version())
	if err != nil {
		t.Fatal(err)
	}
	recorder := &postRecorder{posts: map[string][]map[string]any{}}
	recorder.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			raw, _ := io.ReadAll(request.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			recorder.mu.Lock()
			recorder.posts[request.URL.Path] = append(recorder.posts[request.URL.Path], body)
			recorder.mu.Unlock()
			request.Body = io.NopCloser(bytes.NewReader(raw))
		}
		fake.ServeHTTP(w, request)
	}))
	t.Cleanup(recorder.server.Close)
	return recorder
}

func (r *postRecorder) bodies(path string) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), r.posts[path]...)
}

// gamePurposeInput is the frozen input of a Meshy 7.1 step under a game purpose
// on a target platform: the purpose's overrides for that Offering, the way the
// Agent's model-choice layer applies them, normalized by the Catalog.
func gamePurposeInput(t *testing.T, taskCatalog *catalog.Catalog, purpose, platform string, offering generation.Offering, base map[string]any) json.RawMessage {
	t.Helper()
	usage := taskCatalog.Application().Chat.Model3DUsage
	for _, profile := range usage.Profiles {
		if profile.ID != purpose {
			continue
		}
		resolved, _ := usage.ForPlatform(profile, platform)
		input := map[string]any{}
		for name, value := range base {
			input[name] = value
		}
		for name, value := range resolved.ParameterOverrides[string(offering.ID)] {
			var decoded any
			if err := json.Unmarshal(value, &decoded); err != nil {
				t.Fatal(err)
			}
			input[name] = decoded
		}
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		_, normalized, err := taskCatalog.Normalize(offering.Capability, offering.ID, raw)
		if err != nil {
			t.Fatalf("%s on %q: %v", purpose, platform, err)
		}
		return normalized
	}
	t.Fatalf("purpose %q is not declared", purpose)
	return nil
}

var gameRemeshCases = []struct {
	purpose, platform, topology string
	target                      float64
	pose                        any
}{
	// A quad target delivers about 2.25 triangles per face (observed 2026-11-12,
	// #294), so the character asks for 0.4 of the triangle budget: 8000 on the
	// 20000-triangle mobile class (about 18000 triangles) and 40000 on the
	// 100000-triangle PC and console class (about 90000).
	{"game_character", "", "quad", 8000, "a-pose"},
	{"game_character", "mobile", "quad", 8000, "a-pose"},
	{"game_character", "pc_console", "quad", 40000, "a-pose"},
	{"game_prop", "", "triangle", 8000, nil},
	{"game_prop", "mobile", "triangle", 8000, nil},
	{"game_prop", "pc_console", "triangle", 8000, nil},
}

// poseIs reports the pose a request names; a request that names none carries
// the Offering's empty default or no field at all.
func poseIs(body map[string]any, want any) bool {
	pose, set := body["pose_mode"]
	if want == nil {
		return !set || pose == ""
	}
	return pose == want
}

func TestMeshy71TextTo3DGeneratesTheGameLowPolyInOnePreviewAndRefine(t *testing.T) {
	for _, tc := range gameRemeshCases {
		t.Run(tc.purpose+"/"+tc.platform, func(t *testing.T) {
			upstream := newPostRecorder(t)
			taskCatalog, offering := upgradeOffering(t, "meshy/meshy-7.1/text-to-3d", upstream.server)
			input := gamePurposeInput(t, taskCatalog, tc.purpose, tc.platform, offering, map[string]any{"prompt": "a game-ready robot"})
			adapter := newMeshyAdapter(t, meshy.Config{BaseURL: upstream.server.URL + "/meshy", APIKey: "test", HTTPClient: upstream.server.Client(), AllowHTTPAssets: true})
			started, err := adapter.Start(context.Background(), generation.StartRequest{TaskID: "task", AttemptID: "attempt", SubmissionKey: "submission", Offering: offering, Input: input})
			if err != nil || started.Job == nil {
				t.Fatalf("the fake refused the preview: %+v, %v", started, err)
			}
			preview := upstream.bodies("/meshy/openapi/v2/text-to-3d")[0]
			if preview["mode"] != "preview" || preview["ai_model"] != "meshy-7.1" || preview["should_remesh"] != true ||
				preview["topology"] != tc.topology || preview["target_polycount"] != tc.target || !poseIs(preview, tc.pose) {
				t.Fatalf("preview body = %#v", preview)
			}
			// The texture size is a refine option: the preview carries none.
			assertAbsent(t, "7.1 preview", preview, "texture_resolution", "preview_task_id", "decimation_mode")
			assertNoDeprecatedFields(t, taskCatalog, "text-to-3d", preview)

			// The fake queues the preview, runs it, then succeeds; the adapter
			// submits the refine once the preview has succeeded and finishes with it.
			request := generation.ObserveRequest{TaskID: "task", AttemptID: "attempt", Offering: offering, Job: *started.Job, Input: input}
			var last generation.Observation
			for range 10 {
				if last, err = adapter.Observe(context.Background(), request); err != nil {
					t.Fatalf("the fake refused the refine: %v", err)
				}
				request.Opaque = last.Opaque
				if last.Phase == generation.UpstreamCompleted {
					break
				}
			}
			if last.Phase != generation.UpstreamCompleted || len(last.Artifacts) == 0 {
				t.Fatalf("the preview and the refine did not complete: %+v", last)
			}
			creates := upstream.bodies("/meshy/openapi/v2/text-to-3d")
			if len(creates) != 2 {
				t.Fatalf("creates = %d, want the preview and the refine", len(creates))
			}
			refine := creates[1]
			if refine["mode"] != "refine" || refine["texture_resolution"] != "2k" || refine["enable_pbr"] != true {
				t.Fatalf("refine body = %#v", refine)
			}
			// The mesh was fixed by the preview. The Text to 3D page lists the remesh
			// options and the pose as preview options, yet the adapter repeats the
			// ones the Offering does not restrict to a stage in the refine as well (the
			// reviewed real sample of a default 7.1 task shows the same repetition and
			// Meshy accepted it). Whether Meshy ignores them at the refine of a task that
			// remeshed has not been requested; what must hold here is that the refine
			// never contradicts the preview, and sends no geometry-resolution option.
			assertAbsent(t, "7.1 refine", refine, "geometry_resolution")
			for _, name := range []string{"should_remesh", "topology", "target_polycount", "pose_mode"} {
				if repeated, sent := refine[name]; sent && repeated != preview[name] {
					t.Errorf("the refine sends %s = %v after a preview of %v", name, repeated, preview[name])
				}
			}
			assertNoDeprecatedFields(t, taskCatalog, "text-to-3d", refine)
		})
	}
}

func TestMeshy71ImageTo3DGeneratesTheGameLowPolyInOneRequest(t *testing.T) {
	image := []byte("\x89PNG\r\n\x1a\nreference")
	for _, tc := range gameRemeshCases {
		t.Run(tc.purpose+"/"+tc.platform, func(t *testing.T) {
			upstream := newPostRecorder(t)
			taskCatalog, offering := upgradeOffering(t, "meshy/meshy-7.1/image-to-3d", upstream.server)
			parsed, _ := url.Parse(upstream.server.URL)
			offering.AssetHosts = []string{parsed.Hostname()}
			input := gamePurposeInput(t, taskCatalog, tc.purpose, tc.platform, offering, map[string]any{"reference_assets": []string{"asset-input"}})
			adapter := newMeshyAdapter(t, meshy.Config{BaseURL: upstream.server.URL + "/meshy", APIKey: "test", HTTPClient: upstream.server.Client(),
				InputAssets: memoryInputAssets{content: image}, AllowHTTPAssets: true})
			started, err := adapter.Start(context.Background(), generation.StartRequest{
				Offering: offering, Input: input,
				InputAssets: map[string][]generation.InputAsset{"reference_assets": {{ID: "asset-input", MediaType: "image/png"}}},
			})
			if err != nil || started.Job == nil {
				t.Fatalf("the fake refused the request: %+v, %v", started, err)
			}
			body := upstream.bodies("/meshy/openapi/v1/image-to-3d")[0]
			// One request: the remesh, the topology, the face count and the texture
			// size of the purpose, and the texture the page documents in the same task.
			if body["ai_model"] != "meshy-7.1" || body["should_remesh"] != true || body["topology"] != tc.topology ||
				body["target_polycount"] != tc.target || !poseIs(body, tc.pose) ||
				body["texture_resolution"] != "2k" || body["should_texture"] != true ||
				body["image_url"] != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(image) {
				t.Fatalf("image to 3d body = %#v", body)
			}
			assertAbsent(t, "7.1 image to 3d", body, "decimation_mode", "reference_assets", "image_urls")
			assertNoDeprecatedFields(t, taskCatalog, "image-to-3d", body)
		})
	}
}
