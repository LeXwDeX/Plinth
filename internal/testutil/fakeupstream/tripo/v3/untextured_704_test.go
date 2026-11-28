package tripov3

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// glbCounts is what a GLB's JSON chunk declares: textures, images, materials.
type glbCounts struct {
	Textures  int `json:"textures"`
	Images    int `json:"images"`
	Materials int `json:"materials"`
}

func countGLB(t *testing.T, data []byte) glbCounts {
	t.Helper()
	if len(data) < 20 || !bytes.Equal(data[:4], []byte("glTF")) || string(data[16:20]) != "JSON" {
		t.Fatalf("not a GLB: %q", data[:min(len(data), 20)])
	}
	size := int(binary.LittleEndian.Uint32(data[12:16]))
	var document struct {
		Textures, Images, Materials []json.RawMessage
	}
	if err := json.Unmarshal(data[20:20+size], &document); err != nil {
		t.Fatal(err)
	}
	return glbCounts{len(document.Textures), len(document.Images), len(document.Materials)}
}

// generationGLBs downloads every GLB of a finished generation's output.
func generationGLBs(t *testing.T, f *fixture, done map[string]any) map[string][]byte {
	t.Helper()
	output := field(t, done, "data", "output").(map[string]any)
	files := map[string][]byte{}
	for name, value := range output {
		url, _ := value.(string)
		if !strings.HasSuffix(name, "_url") || !strings.HasSuffix(strings.Split(url, "?")[0], ".glb") {
			continue
		}
		_, _, files[name] = f.download(url)
	}
	if len(files) == 0 {
		t.Fatalf("the generation delivered no GLB: %v", output)
	}
	return files
}

// The real run of 2026-11-28 (#704, observation-704-texture-off.json): a v3.1
// text task with texture=false and pbr=false delivered a GLB with no texture, no
// image and one material; with texture and PBR on (Plinth's default) the GLB was
// textured. The fake delivers the untextured GLB on the generation endpoints for
// texture=false (the image and multiview endpoints are constructed, untextured.go).
func TestGenerationWithTextureOffDeliversAnUntexturedGLB(t *testing.T) {
	raw, err := os.ReadFile("testdata/observation-704-texture-off.json")
	if err != nil {
		t.Fatal(err)
	}
	var observation struct {
		Runs []struct {
			Name string    `json:"name"`
			GLB  glbCounts `json:"glb"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(raw, &observation); err != nil {
		t.Fatal(err)
	}
	observed := map[string]glbCounts{}
	for _, run := range observation.Runs {
		observed[run.Name] = run.GLB
	}
	off, on := observed["texture-off"], observed["texture-on-default"]
	if off != (glbCounts{0, 0, 1}) || on.Textures == 0 {
		t.Fatalf("the observation changed: %+v", observed)
	}

	f := newFixture(t, fakeupstream.Config{})
	// The full Offering's model GLB is the GLTF export of the generation: the
	// export of an untextured model stays untextured, of a textured one textured.
	for label, texture := range map[string]bool{"untextured": false, "textured": true} {
		t.Run("GLTF export of the "+label+" model", func(t *testing.T) {
			source := f.create("/generation/text-to-model", generationBody("/generation/text-to-model", modelH31,
				map[string]any{"texture": texture, "pbr": texture}), key)
			f.finish(source, key)
			_, done := f.finish(f.create("/models/convert", map[string]any{"input": source, "format": "GLTF"}, key), key)
			_, _, glb := f.download(f.outputURL(done, "model_url"))
			if got := countGLB(t, glb); (got.Textures == 0) == texture || got.Materials != 1 {
				t.Fatalf("the export = %+v, want textured %v", got, texture)
			}
		})
	}
	for _, path := range generationPaths {
		t.Run(path+" texture off", func(t *testing.T) {
			body := generationBody(path, modelH31, map[string]any{"texture": false, "pbr": false})
			_, done := f.finish(f.create(path, body, key), key)
			for name, glb := range generationGLBs(t, f, done) {
				if got := countGLB(t, glb); got != off {
					t.Fatalf("output.%s = %+v, want the observed %+v", name, got, off)
				}
				if err := deliveryErr(t, glb, "model", "glb", "model/gltf-binary"); err != nil {
					t.Fatalf("the untextured GLB is rejected by the delivery check: %v", err)
				}
			}
		})
		for label, extra := range map[string]map[string]any{
			"texture omitted":            nil,
			"texture and PBR on":         {"texture": true, "pbr": true},
			"texture off, PBR forced on": {"texture": false, "pbr": true},
		} {
			t.Run(path+" "+label, func(t *testing.T) {
				_, done := f.finish(f.create(path, generationBody(path, modelH31, extra), key), key)
				for name, glb := range generationGLBs(t, f, done) {
					if got := countGLB(t, glb); got.Textures == 0 || got.Images == 0 || got.Materials != 1 {
						t.Fatalf("output.%s = %+v, want a textured GLB", name, got)
					}
				}
			})
		}
	}
}
