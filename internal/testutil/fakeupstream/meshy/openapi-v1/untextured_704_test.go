package meshyopenapiv1

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
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

// The real run of 2026-11-28 (#704, testdata/observation-704-texture-off.json):
// a Meshy 7.1 Image to 3D task frozen with should_texture=false delivered a model
// GLB with no texture, no image and one material. The fake delivers the
// untextured GLB for should_texture=false on Image to 3D and (constructed, no
// real run) Multi Image to 3D; true or omitted keeps the textured GLB.
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
	if len(observation.Runs) != 1 || observation.Runs[0].Name != "texture-off" || observation.Runs[0].GLB != (glbCounts{0, 0, 1}) {
		t.Fatalf("the observation changed: %+v", observation.Runs)
	}
	off := observation.Runs[0].GLB

	f := newFixture(t, fakeupstream.Config{})
	sources := map[string]map[string]any{
		"/image-to-3d":       {"image_url": "https://example.test/a.png", "ai_model": "meshy-7.1", "target_formats": []string{"glb", "obj"}},
		"/multi-image-to-3d": {"image_urls": []string{"https://example.test/a.png", "https://example.test/b.png"}, "ai_model": "meshy-7.1"},
	}
	glbs := func(t *testing.T, path string, extra map[string]any) map[string][]byte {
		t.Helper()
		_, done := f.finish(path, f.create(path, with(sources[path], extra)), key)
		files := map[string][]byte{}
		for field, url := range urls(done["model_urls"], "model_urls", map[string]string{}) {
			if strings.HasSuffix(strings.Split(url, "?")[0], ".glb") {
				_, _, files[field] = f.Download(url)
			}
		}
		if len(files) == 0 {
			t.Fatalf("%s delivered no GLB: %v", path, done["model_urls"])
		}
		return files
	}
	for path := range sources {
		t.Run(path+" texture off", func(t *testing.T) {
			for field, glb := range glbs(t, path, map[string]any{"should_texture": false, "enable_pbr": false}) {
				if got := countGLB(t, glb); got != off {
					t.Fatalf("%s = %+v, want the observed %+v", field, got, off)
				}
				if err := testkit.DeliveryErr(t, glb, "model", "glb", "model/gltf-binary"); err != nil {
					t.Fatalf("the untextured GLB is rejected by the delivery check: %v", err)
				}
			}
		})
		for label, extra := range map[string]map[string]any{
			"texture omitted": nil,
			"texture on":      {"should_texture": true, "enable_pbr": true},
		} {
			t.Run(path+" "+label, func(t *testing.T) {
				for field, glb := range glbs(t, path, extra) {
					if got := countGLB(t, glb); got.Textures == 0 || got.Images == 0 || got.Materials != 1 {
						t.Fatalf("%s = %+v, want a textured GLB", field, got)
					}
				}
			})
		}
	}
}
