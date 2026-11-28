package tripov3

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// The real run of 2026-11-28 (#704, observation-704-texture-off.json): the same
// v3.1 text task at Plinth's defaults (texture and PBR on) delivered a GLB with
// three textures, three images and one material. The fake delivers that pack on
// every generation endpoint whose request has PBR on (an omitted texture and an
// omitted pbr are on), and on the GLTF export of such a model; the image and
// multiview endpoints, texture without PBR and PBR forced on over texture=false
// are constructed (untextured.go).
func TestGenerationWithPBRDeliversTheObservedThreeTextures(t *testing.T) {
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
	var on glbCounts
	for _, run := range observation.Runs {
		if run.Name == "texture-on-default" {
			on = run.GLB
		}
	}
	if on != (glbCounts{3, 3, 1}) {
		t.Fatalf("the observation changed: %+v", on)
	}

	f := newFixture(t, fakeupstream.Config{})
	for _, path := range generationPaths {
		for label, tc := range map[string]struct {
			extra map[string]any
			want  glbCounts
		}{
			"defaults":                   {nil, on},
			"texture and PBR on":         {map[string]any{"texture": true, "pbr": true}, on},
			"texture off, PBR forced on": {map[string]any{"texture": false, "pbr": true}, on},
			"texture on, PBR off":        {map[string]any{"texture": true, "pbr": false}, glbCounts{1, 1, 1}},
			"texture and PBR off":        {map[string]any{"texture": false, "pbr": false}, glbCounts{0, 0, 1}},
		} {
			t.Run(path+" "+label, func(t *testing.T) {
				_, done := f.finish(f.create(path, generationBody(path, modelH31, tc.extra), key), key)
				for name, glb := range generationGLBs(t, f, done) {
					if got := countGLB(t, glb); got != tc.want {
						t.Fatalf("output.%s = %+v, want %+v", name, got, tc.want)
					}
					if err := deliveryErr(t, glb, "model", "glb", "model/gltf-binary"); err != nil {
						t.Fatalf("the GLB is rejected by the delivery check: %v", err)
					}
				}
			})
		}
	}
	// The full Offering's model GLB is the GLTF export of the generation, which is
	// what the observation counted.
	t.Run("GLTF export of the default model", func(t *testing.T) {
		source := f.create("/generation/text-to-model", generationBody("/generation/text-to-model", modelH31, nil), key)
		f.finish(source, key)
		_, done := f.finish(f.create("/models/convert", map[string]any{"input": source, "format": "GLTF"}, key), key)
		_, _, glb := f.download(f.outputURL(done, "model_url"))
		if got := countGLB(t, glb); got != on {
			t.Fatalf("the export = %+v, want the observed %+v", got, on)
		}
	})
}
