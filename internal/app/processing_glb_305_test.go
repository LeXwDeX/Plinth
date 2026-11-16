package app

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/registry"
)

// #305: a Tripo rig delivers the GLB its GLTF conversion writes (PNG textures at
// the purpose's size, the skin kept, the origin at the bottom center) as the model,
// and keeps the GLB the upstream returned as the original file, byte for byte. The
// whole stack runs against the fake upstream: the real Catalog, the kernel, the
// Tripo adapter with its private exports, and the asset store with its delivery
// checks. The fake writes what a conversion asks for (its contract tests hold it to
// the paid run of 2026-11-16); that the real converter writes the same is what the
// run showed for a 2048-pixel source, and is not verified for 8192 to 2048 or for a
// source with an animation.

// processingStack is the Tripo fake stack with the bodies of every conversion
// request and the artifact GETs counted. While failGLB is set the GLTF conversions
// of the rig fail.
type processingStack struct {
	harness   *acmeHarness
	catalog   *catalog.Catalog
	downloads *downloadLog
	bodies    func() []map[string]any
	failGLB   *atomic.Bool
}

func newProcessingStack(t *testing.T, convertDelay time.Duration) processingStack {
	t.Helper()
	fake, err := registry.New(fakeupstream.Config{QueuedPolls: 1, RunningPolls: 1, ArtifactOrigin: "https://" + fakeAssetHost, ConvertURLDelay: convertDelay})
	if err != nil {
		t.Fatal(err)
	}
	failGLB := &atomic.Bool{}
	downloads := &downloadLog{served: map[string]int{}, refused: map[string]int{}}
	log := &pathLog{posts: map[string][]map[string]any{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/artifacts/") {
			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			fake.ServeHTTP(recorder, r)
			downloads.record(r.URL.Path, recorder.status)
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/models/convert") {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			log.mu.Lock()
			log.posts["/models/convert"] = append(log.posts["/models/convert"], body)
			log.mu.Unlock()
			if failGLB.Load() && body["format"] == "GLTF" {
				r.Header.Set("X-Fake-Scenario", "task-failed")
			}
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	fakeURL, _ := url.Parse(server.URL)
	settings := map[string]string{
		"tripo_base_url": server.URL + "/tripo/v3",
		"tripo_api_key":  fakeUpstreamKeyPrefix + "test-key",
	}
	taskCatalog, err := loadCatalogForShape("", upstreamShape{origin: fakeURL})
	if err != nil {
		t.Fatal(err)
	}
	return processingStack{
		harness:   newAcmeHarnessWithTransport(t, taskCatalog, settings, fakeShapeHTTPTransport(fakeURL)),
		catalog:   taskCatalog,
		downloads: downloads,
		bodies:    func() []map[string]any { return log.bodies("/models/convert") },
		failGLB:   failGLB,
	}
}

// riggedUnderAPurpose runs the generation and the rig of the shipped Schema, the
// rig's export size filled by the shared purpose resolver like every path does.
// The generation's own GLB export always succeeds; failGLB fails the rig's.
func riggedUnderAPurpose(t *testing.T, stack processingStack, brief map[string]string, key string, failGLB bool) generation.Task {
	t.Helper()
	harness := stack.harness
	source := submitStep(t, harness, generation.TextTo3D, "tripo/tripo-v3.1-full/text-to-3d",
		`{"prompt":"a game character in an A pose","authorize_extra_credits":true,"output_formats":["GLTF"]}`,
		key+"-source", map[string]string{"output_formats": "only the GLB feeds the rig"})
	sourceTask := harness.runUntilTerminal(t, "owner-acme", source.TaskID)
	if sourceTask.State != generation.TaskSucceeded {
		t.Fatalf("source state = %s, failure = %#v", sourceTask.State, sourceTask.Failure)
	}
	stack.failGLB.Store(failGLB)
	offering, ok := stack.catalog.Get("tripo/processing/rigging-v3")
	if !ok {
		t.Fatal("the rig is not declared")
	}
	chatSchema := stack.catalog.Application().Chat
	applied, err := (chat.PurposeParameters{Model3D: chatSchema.Model3DUsage, Image2D: chatSchema.Image2DUsage}).ApplyToDirectInput(
		briefOf(t, brief), offering, json.RawMessage(`{"source_task":"`+string(sourceTask.ID)+`","authorize_extra_credits":true}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	accepted := submitStep(t, harness, generation.Rigging, "tripo/processing/rigging-v3", string(applied.Input), key, applied.Reasons)
	return harness.runUntilTerminal(t, "owner-acme", accepted.TaskID)
}

func openedBytes(t *testing.T, stack processingStack, task generation.Task, asset generation.AssetID) []byte {
	t.Helper()
	content, err := stack.harness.module.OpenAsset(context.Background(), "owner-acme", task.ID, asset)
	if err != nil {
		t.Fatal(err)
	}
	defer content.Body.Close()
	data, err := io.ReadAll(content.Body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assetOf(task generation.Task, role, variant string) generation.Asset {
	for _, asset := range task.Assets {
		if asset.Role == role && asset.Variant == variant {
			return asset
		}
	}
	return generation.Asset{}
}

// The model slot of a rig is the conversion of its GLB at the purpose's size; the
// upstream's GLB is the original file and is stored as it was; the FBX is
// exported at the same size. Nothing is downloaded twice for a slot of its own.
func TestRigDeliversTheConvertedGLBAsTheModelAtThePurposeSize(t *testing.T) {
	for _, tc := range []struct {
		name  string
		brief map[string]string
		size  int
	}{
		{"game character on a phone", map[string]string{"purpose": "game_character", "target_platform": "mobile"}, 2048},
		{"game character on a PC", map[string]string{"purpose": "game_character", "target_platform": "pc_console"}, 4096},
		{"no purpose", nil, 8192},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stack := newProcessingStack(t, 0)
			task := riggedUnderAPurpose(t, stack, tc.brief, "rig-305-"+strings.ReplaceAll(tc.name, " ", "-"), false)
			if task.State != generation.TaskSucceeded {
				t.Fatalf("rig state = %s, failure = %#v", task.State, task.Failure)
			}

			// The four slots, the model and the original required.
			model, original, fbx := assetOf(task, "model", "glb"), assetOf(task, "original_model", "glb"), assetOf(task, "model", "fbx")
			if model.ID == "" || original.ID == "" || fbx.ID == "" || !model.Required || !original.Required || !fbx.Required {
				t.Fatalf("assets = %+v, want the model, the FBX and the original, all required", task.Assets)
			}
			if model.SHA256 == original.SHA256 || model.StorageKey == original.StorageKey {
				t.Fatalf("the model is the original file: %+v %+v", model, original)
			}

			// The model slot is the conversion the fake wrote for this request: the
			// skin of the rig, one PNG of the purpose's size, the feet at the origin.
			data := openedBytes(t, stack, task, model.ID)
			want, err := fakeupstream.ConvertedModelFile(fakeupstream.ConvertedModelSpec{
				TextureEdge: tc.size, TextureFormat: fakeupstream.TextureFormatPNG, Skeleton: true, Offset: fakeupstream.CenterOrigin(true),
			})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(data, want.Data) {
				t.Fatalf("the model slot is not the converted GLB (%d bytes, want %d)", len(data), len(want.Data))
			}
			parsed, err := modelinspect.Parse(bytes.NewReader(data), int64(len(data)), modelinspect.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			report := modelinspect.Inspect(parsed, modelinspect.DefaultInspectOptions())
			edge := 0
			for _, material := range report.Materials {
				for _, slot := range material.Slots {
					if slot.Slot == "base_color" && slot.Present {
						if slot.MIME != "image/png" {
							t.Errorf("base color is %s, want PNG", slot.MIME)
						}
						edge = slot.Width
					}
				}
			}
			offset := report.BBox.BottomCenterOffset
			if edge != tc.size || len(report.Skins) != 1 || math.Abs(offset[0])+math.Abs(offset[1])+math.Abs(offset[2]) > 1e-4 {
				t.Errorf("model: texture %d (want %d), skins %v, bottom centre %v", edge, tc.size, report.Skins, offset)
			}

			// The original is what the upstream returned, kept byte for byte, with
			// its own (smaller here, 8192 for a real Tripo result) texture.
			native, err := fakeupstream.ModelFile("glb:skeleton")
			if err != nil {
				t.Fatal(err)
			}
			if originalData := openedBytes(t, stack, task, original.ID); !bytes.Equal(originalData, native.Data) {
				t.Fatalf("the original is not the upstream's own GLB (%d bytes, want %d)", len(originalData), len(native.Data))
			}

			// The FBX embeds a PNG of the same size.
			fbxData := openedBytes(t, stack, task, fbx.ID)
			start := bytes.Index(fbxData, []byte("\x89PNG"))
			if start < 0 {
				t.Fatal("the FBX embeds no PNG")
			}
			if size, err := png.DecodeConfig(bytes.NewReader(fbxData[start:])); err != nil || size.Width != tc.size {
				t.Fatalf("the FBX texture is %d pixels (%v), want %d", size.Width, err, tc.size)
			}

			// Both exports carry the purpose's size; only the GLB one the pivot.
			exports := map[string]map[string]any{}
			for _, body := range stack.bodies() {
				format, _ := body["format"].(string)
				if format == "GLTF" || format == "FBX" {
					exports[format] = body
				}
			}
			if len(exports) != 2 {
				t.Fatalf("exports = %v, want the GLTF and the FBX of the rig (after the source's own)", exports)
			}
			for format, body := range exports {
				_, pivot := body["pivot_to_center_bottom"]
				if body["texture_size"] != float64(tc.size) || body["texture_format"] != "PNG" || body["with_animation"] != true || pivot != (format == "GLTF") {
					t.Errorf("%s export = %v", format, body)
				}
			}

			// The files are fetched as often as they must be and no more: the
			// original is checked against its frozen bytes before each of the two
			// exports, once more after them, and read once to store it; each export
			// file is read once.
			counts, paths := stack.downloads.files()
			original4, once := 0, 0
			for _, path := range paths {
				switch {
				case counts[path] == 1:
					once++
				case counts[path] == 4:
					original4++
				default:
					t.Errorf("%s was fetched %d times", path, counts[path])
				}
			}
			if original4 != 1 || stack.downloads.refusals() != 0 {
				t.Errorf("fetches = %v (%d refused): want one file read four times (the original) and the rest once", counts, stack.downloads.refusals())
			}
			_ = once

			// The usage check judges this model by its own bytes (nothing stands in
			// for it), and judges the original by the exports the purpose sized.
			executor, err := generationtool.New(generationtool.Config{
				Gateway: stack.harness.module, References: noReferences{}, Chat: stack.catalog.Application().Chat,
			})
			if err != nil {
				t.Fatal(err)
			}
			if edge := executor.SizedTextureEdge(task, model.ID); edge != 0 {
				t.Errorf("sized edge for the model = %d, want 0 (its own bytes decide)", edge)
			}
			if edge := executor.SizedTextureEdge(task, original.ID); edge != tc.size {
				t.Errorf("sized edge for the original = %d, want %d", edge, tc.size)
			}
		})
	}
}

// A GLB export the upstream fails fails the step. The GLB the rig returned is not
// delivered in its place as the model: the Task has no model at all, and the FBX
// export that would have followed was not started.
func TestARigWhoseGLBExportFailsDeliversNoModelAndNeverTheNativeGLBInItsPlace(t *testing.T) {
	stack := newProcessingStack(t, 0)
	task := riggedUnderAPurpose(t, stack, map[string]string{"purpose": "game_character", "target_platform": "mobile"}, "rig-305-glb-failed", true)
	if task.State != generation.TaskFailed || task.Failure == nil || task.Failure.Retryable {
		t.Fatalf("state = %s failure = %#v, want a final failure", task.State, task.Failure)
	}
	for _, asset := range task.Assets {
		if asset.Role == "model" || asset.Role == "original_model" {
			t.Fatalf("a failed export left %s/%s behind: %+v", asset.Role, asset.Variant, task.Assets)
		}
	}
	for _, body := range stack.bodies() {
		if body["format"] == "FBX" && body["texture_size"] == float64(2048) {
			t.Fatalf("the FBX export started after the GLB export failed: %v", body)
		}
	}
}

// Creating a conversion from a URL took 47 to 48 seconds for the real upstream. The
// request timeout of the Tripo connection the Schema declares is far above it, and
// a stack whose fake answers that slowly (here a second, enough to be sure the wait
// is on the path) still delivers the rig.
func TestRigSurvivesASlowConversionCreation(t *testing.T) {
	stack := newProcessingStack(t, time.Second)
	start := time.Now()
	task := riggedUnderAPurpose(t, stack, map[string]string{"purpose": "game_character", "target_platform": "mobile"}, "rig-305-slow-create", false)
	if task.State != generation.TaskSucceeded {
		t.Fatalf("rig state = %s, failure = %#v", task.State, task.Failure)
	}
	// The two conversions of the rig are made from a URL and wait one second each;
	// the generation's own GLTF export names a task and does not wait.
	if elapsed := time.Since(start); elapsed < 2*time.Second {
		t.Fatalf("the rig was delivered in %v: the slow creation was not on the path", elapsed)
	}
	if assetOf(task, "model", "glb").ID == "" {
		t.Fatalf("assets = %+v", task.Assets)
	}
}
