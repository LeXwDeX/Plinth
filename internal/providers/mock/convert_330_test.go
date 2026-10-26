package mock

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func sourceRemoteID(adapter *Adapter, submission string) string {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return adapter.submissions[submission]
}

// completeRequest runs one job like complete, with the request adjusted.
func completeRequest(t *testing.T, adapter *Adapter, offering generation.Offering, submission string, input any, adjust func(*generation.StartRequest)) []deliveredFile {
	t.Helper()
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := generation.StartRequest{TaskID: generation.TaskID("task-" + submission), AttemptID: generation.AttemptID("attempt-" + submission), SubmissionKey: submission, Offering: offering, Input: payload}
	adjust(&request)
	started, err := adapter.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	observe := generation.ObserveRequest{TaskID: request.TaskID, AttemptID: request.AttemptID, Offering: offering, Job: *started.Job, Input: payload}
	var observation generation.Observation
	for range 4 {
		if observation, err = adapter.Observe(context.Background(), observe); err != nil {
			t.Fatal(err)
		}
		if observation.Phase == generation.UpstreamCompleted {
			break
		}
	}
	return open(t, adapter, offering, observe, observation.Artifacts)
}

func deliveryError(t *testing.T, role, variant, mediaType string, content []byte) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	return local.ValidateDeliveredFile(file, role, variant, mediaType)
}

// #330: the mock runs Meshy Convert as declared — the six model files and
// nothing else (no thumbnail, no textures, no material library). Its OBJ is
// shaped like the real one (dev-test note 99526: a Blender export with
// "mtllib temp.mtl", one usemtl and complete UVs, while temp.mtl is never
// delivered), so local tests see the dangling reference the README explains.
func TestMeshyConvertDeliversTheSixModelFilesOnly(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get("meshy/processing/convert")
	if !ok {
		t.Fatal("meshy convert missing")
	}
	input := map[string]any{"source_task": "task-source", "target_formats": []string{"glb", "fbx", "obj", "usdz", "stl", "3mf"}}
	files := complete(t, New(), offering, "convert-330", input)
	var variants []string
	for _, file := range files {
		if file.claim.Role != "model" {
			t.Fatalf("delivered %s/%s: Convert returns model files only", file.claim.Role, file.claim.Variant)
		}
		variants = append(variants, file.claim.Variant)
		validateDelivered(t, "convert", file.claim.Role, file.claim.Variant, file.claim.MediaType, file.content)
		if file.claim.Variant == "obj" {
			for _, want := range []string{"\nmtllib temp.mtl\n", "\nusemtl ", "\nvt ", "\nf "} {
				if !bytes.Contains(file.content, []byte(want)) {
					t.Fatalf("the Convert OBJ lacks %q like the real one:\n%s", want, file.content[:120])
				}
			}
		}
	}
	sort.Strings(variants)
	if strings.Join(variants, ",") != "3mf,fbx,glb,obj,stl,usdz" {
		t.Fatalf("variants = %v", variants)
	}
	// A generation result that delivers its MTL keeps referencing it.
	generation, _ := product.Get("meshy/meshy-7.1/text-to-3d")
	objects := 0
	for _, file := range complete(t, New(), generation, "meshy-text-330", map[string]any{"prompt": "a stool"}) {
		if file.claim.Variant == "obj" {
			objects++
			if !bytes.Contains(file.content, []byte("mtllib model.mtl")) {
				t.Fatal("the Meshy generation OBJ lost its material library reference")
			}
		}
	}
	if objects != 1 {
		t.Fatalf("Meshy generation delivered %d OBJ files", objects)
	}
}

// #330 B on the mock stack. MarkerTexturePadding makes a result whose GLB
// texture view counts one alignment space after IEND (the dev-test keychain);
// a conversion of it copies the padded texture into its USDZ and GLB, as Meshy
// Convert does, and those files are still delivered. MarkerConvertRejected
// leaves its own files clean but gives every conversion of it a texture with a
// real trailer, so that conversion finishes upstream and then fails delivery:
// the local stack can show the charged-but-undelivered path (free refetch,
// then a retry that charges again).
func TestTextureMarkersReachConversionsBySourceTaskAndSavedFile330(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	generation3D, _ := product.Get("meshy/meshy-7.1/text-to-3d")
	convert, _ := product.Get("meshy/processing/convert")
	textureOf := func(t *testing.T, files []deliveredFile, variant string) []byte {
		t.Helper()
		for _, file := range files {
			if file.claim.Variant != variant {
				continue
			}
			reader, err := zip.NewReader(bytes.NewReader(file.content), int64(len(file.content)))
			if err != nil {
				t.Fatal(err)
			}
			for _, member := range reader.File {
				if member.Name == "texture.png" {
					body, _ := member.Open()
					defer body.Close()
					data, _ := io.ReadAll(body)
					return data
				}
			}
		}
		t.Fatalf("no %s texture", variant)
		return nil
	}
	iend := []byte{0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}
	for _, tc := range []struct {
		marker, sourceTail, convertTail string
		convertAccepted                 bool
	}{
		{MarkerTexturePadding, " ", " ", true},
		{MarkerConvertRejected, "", "mock-trailer", false},
	} {
		t.Run(tc.marker, func(t *testing.T) {
			adapter := New()
			source := complete(t, adapter, generation3D, "source", map[string]any{"prompt": "a stool " + tc.marker})
			if tail := textureOf(t, source, "usdz"); !bytes.HasSuffix(tail, append(bytes.Clone(iend), tc.sourceTail...)) {
				t.Fatalf("source texture tail = %q", tail[len(tail)-16:])
			}
			for _, file := range source {
				validateDelivered(t, "source", file.claim.Role, file.claim.Variant, file.claim.MediaType, file.content)
			}
			var glb []byte
			for _, file := range source {
				if file.claim.Variant == "glb" {
					glb = file.content
				}
			}
			// By the source task's upstream job (Convert input_task_id) and by
			// the saved file (Convert model_url, Rodin/keychain sources).
			for name, request := range map[string]func(*generation.StartRequest){
				"source_task": func(request *generation.StartRequest) {
					request.TaskDependencies = map[string]generation.TaskDependency{"source_task": {TaskID: "task-source", UpstreamJob: generation.UpstreamJob{RemoteID: sourceRemoteID(adapter, "source")}}}
				},
				"source_asset": func(request *generation.StartRequest) {
					sum := sha256.Sum256(glb)
					request.InputAssets = map[string][]generation.InputAsset{"source_asset": {{ID: "asset-glb", SHA256: hex.EncodeToString(sum[:]), Role: "model", Variant: "glb"}}}
				},
			} {
				t.Run(name, func(t *testing.T) {
					files := completeRequest(t, adapter, convert, "convert-"+name, map[string]any{"target_formats": []string{"glb", "fbx", "obj", "usdz", "stl", "3mf"}}, request)
					if tail := textureOf(t, files, "usdz"); !bytes.HasSuffix(tail, append(bytes.Clone(iend), tc.convertTail...)) {
						t.Fatalf("converted texture tail = %q", tail[len(tail)-24:])
					}
					accepted := true
					for _, file := range files {
						if file.claim.Variant == "usdz" && deliveryError(t, file.claim.Role, file.claim.Variant, file.claim.MediaType, file.content) != nil {
							accepted = false
						}
					}
					if accepted != tc.convertAccepted {
						t.Fatalf("converted USDZ accepted = %v", accepted)
					}
				})
			}
		})
	}
}

// #330 second follow-ups. MarkerConvertRejectedOnce rejects a conversion the
// way MarkerConvertRejected does until those files were served; a free
// refetch, which observes the finished job again under the source Task's IDs,
// gets clean files and succeeds, so the local stack can show the state after
// a successful refetch (delivered eligibility, the folded version chain).
func TestConvertRejectedOnceIsCleanForTheRefetch330(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	generation3D, _ := product.Get("meshy/meshy-7.1/text-to-3d")
	convert, _ := product.Get("meshy/processing/convert")
	adapter := New()
	source := complete(t, adapter, generation3D, "source", map[string]any{"prompt": "a stool " + MarkerConvertRejectedOnce})
	for _, file := range source {
		validateDelivered(t, "source", file.claim.Role, file.claim.Variant, file.claim.MediaType, file.content)
	}
	payload, _ := json.Marshal(map[string]any{"target_formats": []string{"glb", "fbx", "obj", "usdz", "stl", "3mf"}})
	request := generation.StartRequest{TaskID: "task-convert", AttemptID: "attempt-convert", SubmissionKey: "convert", Offering: convert, Input: payload,
		TaskDependencies: map[string]generation.TaskDependency{"source_task": {TaskID: "task-source", UpstreamJob: generation.UpstreamJob{RemoteID: sourceRemoteID(adapter, "source")}}}}
	started, err := adapter.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	observe := generation.ObserveRequest{TaskID: request.TaskID, AttemptID: request.AttemptID, Offering: convert, Job: *started.Job, Input: payload}
	var observation generation.Observation
	for range 4 {
		if observation, err = adapter.Observe(context.Background(), observe); err != nil {
			t.Fatal(err)
		}
		if observation.Phase == generation.UpstreamCompleted {
			break
		}
	}
	usdzAccepted := func(files []deliveredFile) bool {
		for _, file := range files {
			if file.claim.Variant == "usdz" {
				return deliveryError(t, file.claim.Role, file.claim.Variant, file.claim.MediaType, file.content) == nil
			}
		}
		t.Fatal("no usdz")
		return false
	}
	if usdzAccepted(open(t, adapter, convert, observe, observation.Artifacts)) {
		t.Fatal("the first fetch must be refused")
	}
	// The same Task reads the same bytes again.
	if usdzAccepted(open(t, adapter, convert, observe, observation.Artifacts)) {
		t.Fatal("a second read by the same Task must be refused too")
	}
	// The failing delivery re-observes the finished job once per retry and
	// is refused every time; the refetch observes it once more afterwards.
	attempts := convert.MaterializeAttempts
	if attempts <= 0 {
		attempts = 3
	}
	reobserve := func() generation.Observation {
		again, err := adapter.Observe(context.Background(), observe)
		if err != nil || again.Phase != generation.UpstreamCompleted {
			t.Fatalf("observe = %v %v", again.Phase, err)
		}
		return again
	}
	for retry := 1; retry < attempts; retry++ {
		if usdzAccepted(open(t, adapter, convert, observe, reobserve().Artifacts)) {
			t.Fatalf("delivery retry %d must be refused", retry)
		}
	}
	if !usdzAccepted(open(t, adapter, convert, observe, reobserve().Artifacts)) {
		t.Fatal("the refetch must get clean files")
	}
}
