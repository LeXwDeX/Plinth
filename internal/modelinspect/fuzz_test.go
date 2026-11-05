package modelinspect

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

// FuzzParse checks that hostile containers and accessor layouts never panic or
// allocate without bound. When parsing succeeds, every public read path is
// exercised with small limits so the fuzzer covers payload decoding too.
func FuzzParse(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("glTF"))
	f.Add(cubeGLB())
	f.Add(openPlaneDocument().finish())
	f.Add(stripDocument().finish())
	f.Add(skinAnimationDocument().finish())
	f.Add(texturedQuadGLB(f))
	f.Add(meshoptCubeDocument(f).finish())
	f.Add(skinnedCubeDocument(skinnedCubeOptions{armatureScale: 0.01}).finish())

	f.Fuzz(func(t *testing.T, data []byte) {
		model, err := Parse(bytes.NewReader(data), int64(len(data)), DefaultLimits())
		if err != nil {
			if model != nil {
				t.Fatal("Parse returned a model together with an error")
			}
			return
		}
		report := Inspect(model, DefaultInspectOptions())
		if _, err := json.Marshal(report); err != nil {
			t.Fatalf("json.Marshal(report): %v", err)
		}
		if report.Triangles == 0 || report.Triangles > 5000 {
			return
		}
		if _, err := Render(model, RenderRequest{
			Width: 32, Height: 32, View: ViewThreeQuarter,
			Mode: ModeTextured, Workers: 1,
		}); err != nil &&
			!errors.Is(err, ErrInvalid) && !errors.Is(err, ErrUnsupported) &&
			!errors.Is(err, ErrTooLarge) && !errors.Is(err, ErrTooDense) {
			t.Fatalf("Render: %v", err)
		}
	})
}
