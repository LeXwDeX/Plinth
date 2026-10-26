package modelinput_test

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
)

func policy() modelinput.Policy {
	return modelinput.Policy{ProfileID: modelinput.ProfileID, Revision: modelinput.ProfileRevision, Limits: modelinput.Limits{MaxBytes: 10 << 20, MaxJSONBytes: 2 << 20, MaxDepth: 32, MaxElements: 100000, MaxVertices: 1000000, MaxTriangles: 1000000, MaxNodes: 10000, MaxWork: 64 << 20, MaxImagePixels: 1000000, MaxTotalImagePixels: 2000000}}
}
func validate(t *testing.T, raw []byte, p modelinput.Policy) (modelinput.Profile, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.glb")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	return modelinput.ValidateGLB(context.Background(), f, p)
}
func TestValidStaticModels(t *testing.T) {
	for _, o := range []modelinputtest.Options{{}, {Indexed: true}, {Interleaved: true}, {Indexed: true, Interleaved: true, Textured: true, MultiplePrimitives: true}} {
		p, err := validate(t, modelinputtest.Triangle(o), policy())
		if err != nil {
			t.Fatalf("%+v: %v", o, err)
		}
		want := int64(1)
		if o.MultiplePrimitives {
			want = 2
		}
		if p.Triangles != want || p.Vertices != 3*want || p.HasTextures != o.Textured || p.SHA256 == "" || p.ProfileID != modelinput.ProfileID {
			t.Fatalf("profile=%+v", p)
		}
	}
}
func TestInvalidGeometry(t *testing.T) {
	cases := map[string]func(map[string]any, []byte){
		"buffer_range":   func(d map[string]any, b []byte) { d["buffers"].([]any)[0].(map[string]any)["byteLength"] = len(b) + 8 },
		"accessor_range": func(d map[string]any, b []byte) { d["accessors"].([]any)[0].(map[string]any)["count"] = 100000 },
		"bad_index":      func(d map[string]any, b []byte) { binary.LittleEndian.PutUint16(b[36:], 3) },
		"nan": func(d map[string]any, b []byte) {
			binary.LittleEndian.PutUint32(b, math.Float32bits(float32(math.NaN())))
		},
		"bad_reference": func(d map[string]any, b []byte) { d["nodes"].([]any)[0].(map[string]any)["mesh"] = 3 },
		"cycle":         func(d map[string]any, b []byte) { d["nodes"].([]any)[0].(map[string]any)["children"] = []any{0} },
		"attribute_count": func(d map[string]any, b []byte) {
			d["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)["attributes"].(map[string]any)["NORMAL"] = 1
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d, b := modelinputtest.Document(modelinputtest.Options{Indexed: true})
			mutate(d, b)
			_, err := validate(t, modelinputtest.GLB(d, b), policy())
			var e *modelinput.Error
			if !errors.As(err, &e) || e.Kind != modelinput.Invalid {
				t.Fatalf("want invalid, got %v", err)
			}
		})
	}
}
func TestUnsupportedProfile(t *testing.T) {
	for _, feature := range []string{"sparse", "skin", "animation", "morph", "extensions", "uri"} {
		t.Run(feature, func(t *testing.T) {
			d, b := modelinputtest.Document(modelinputtest.Options{})
			switch feature {
			case "sparse":
				d["accessors"].([]any)[0].(map[string]any)["sparse"] = map[string]any{}
			case "skin":
				d["skins"] = []any{map[string]any{}}
			case "animation":
				d["animations"] = []any{map[string]any{}}
			case "morph":
				d["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)["targets"] = []any{map[string]any{}}
			case "extensions":
				d["extensionsUsed"] = []any{"KHR_draco_mesh_compression"}
			case "uri":
				d["buffers"].([]any)[0].(map[string]any)["uri"] = "https://PRIVATE.invalid/model.bin"
			}
			_, err := validate(t, modelinputtest.GLB(d, b), policy())
			var e *modelinput.Error
			if !errors.As(err, &e) || e.Kind != modelinput.Unsupported {
				t.Fatalf("want unsupported, got %v", err)
			}
		})
	}
}
func TestPolicyLimits(t *testing.T) {
	raw := modelinputtest.Triangle(modelinputtest.Options{Textured: true, MultiplePrimitives: true})
	for _, kind := range []string{"bytes", "json", "depth", "elements", "vertices", "triangles", "work"} {
		t.Run(kind, func(t *testing.T) {
			p := policy()
			switch kind {
			case "bytes":
				p.Limits.MaxBytes = 20
				p.Limits.MaxJSONBytes = 20
			case "json":
				p.Limits.MaxJSONBytes = 20
			case "depth":
				p.Limits.MaxDepth = 1
			case "elements":
				p.Limits.MaxElements = 2
			case "vertices":
				p.Limits.MaxVertices = 2
			case "triangles":
				p.Limits.MaxTriangles = 1
			case "work":
				p.Limits.MaxWork = 8
			}
			_, err := validate(t, raw, p)
			var e *modelinput.Error
			if !errors.As(err, &e) || e.Kind != modelinput.Limit {
				t.Fatalf("want limit, got %v", err)
			}
		})
	}
}
