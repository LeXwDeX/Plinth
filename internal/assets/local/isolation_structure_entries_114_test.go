package local

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"testing"
)

// manyPartGLB gives every part its own accessor and bufferView, so neither the
// glTF arrays nor the canonical numeric fields collapse below the part count.
func manyPartGLB(t *testing.T, parts int) []byte {
	t.Helper()
	var bin []byte
	var views, accessors, meshes, nodes, children []any
	for part := 0; part < parts; part++ {
		positions := make([]byte, 36)
		binary.LittleEndian.PutUint32(positions[12:], math.Float32bits(float32(part)))
		views = append(views, map[string]any{"buffer": 0, "byteOffset": len(bin), "byteLength": len(positions)})
		bin = append(bin, positions...)
		accessors = append(accessors, map[string]any{"bufferView": part, "componentType": 5126, "count": 3, "type": "VEC3"})
		meshes = append(meshes, map[string]any{"primitives": []any{map[string]any{"attributes": map[string]any{"POSITION": part}}}})
		nodes = append(nodes, map[string]any{"mesh": part})
		children = append(children, part)
	}
	document, err := json.Marshal(map[string]any{
		"asset": map[string]string{"version": "2.0"}, "scene": 0,
		"scenes": []any{map[string]any{"nodes": children}}, "nodes": nodes, "meshes": meshes,
		"accessors": accessors, "bufferViews": views, "buffers": []any{map[string]any{"byteLength": len(bin)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return glbFixtureWithBIN(string(document), bin)
}

// Structure counts grow with the model, not with the archive member count (#114).
func TestManyPartModelIsDelivered114(t *testing.T) {
	body := manyPartGLB(t, maxArchiveFiles+2000)
	assertIsolationBoundary(t, body, "glb", "model/gltf-binary", true)
	assertIsolationBoundary(t, zipModel(t, map[string]string{"scene.glb": string(body)}), "zip", "application/zip", true)
}

func TestStructureEntryCeilingStillRejects114(t *testing.T) {
	if _, err := gltfArray(map[string]any{"accessors": make([]any, maxModelStructureEntries+1)}, "accessors"); err == nil {
		t.Fatal("a glTF array above the structure ceiling must be rejected")
	}
	plan := inspectionPlan{numeric: make([]inspectionSpan, maxModelStructureEntries+1)}
	if err := normalizeInspectionPlan(&plan, 1); err == nil {
		t.Fatal("an inspection plan above the structure ceiling must be rejected")
	}
}
