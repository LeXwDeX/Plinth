package modelinput_test

import (
	"context"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"testing"
)

func TestSingleBaseColorTextureCoversEveryPrimitive(t *testing.T) {
	for _, name := range []string{"one atlas", "shared atlas", "untextured", "normal only", "part missing", "two atlases"} {
		t.Run(name, func(t *testing.T) {
			doc, bin := modelinputtest.Document(modelinputtest.Options{Textured: name != "untextured", MultiplePrimitives: name == "shared atlas" || name == "part missing" || name == "two atlases"})
			if name == "normal only" {
				doc["materials"] = []any{map[string]any{"normalTexture": map[string]any{"index": 0}}}
			}
			if name == "part missing" || name == "two atlases" {
				mesh := doc["meshes"].([]any)[0].(map[string]any)
				p := mesh["primitives"].([]any)
				copy := map[string]any{}
				for k, v := range p[1].(map[string]any) {
					copy[k] = v
				}
				if name == "part missing" {
					delete(copy, "material")
				} else {
					textures := doc["textures"].([]any)
					doc["textures"] = append(textures, map[string]any{"source": 0})
					doc["materials"] = append(doc["materials"].([]any), map[string]any{"pbrMetallicRoughness": map[string]any{"baseColorTexture": map[string]any{"index": 1}}})
					copy["material"] = 1
				}
				p[1] = copy
			}
			result, err := modelinput.ValidateGLBBytes(context.Background(), modelinputtest.GLB(doc, bin), policy())
			if err != nil {
				t.Fatal(err)
			}
			want := name == "one atlas" || name == "shared atlas"
			if result.HasSingleBaseColorTexture != want {
				t.Fatalf("%s: single base-color texture=%v want %v", name, result.HasSingleBaseColorTexture, want)
			}
			if name == "normal only" && !result.HasTextures {
				t.Fatal("legacy any-texture semantics changed")
			}
		})
	}
}
