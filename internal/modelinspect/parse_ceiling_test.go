package modelinspect

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// emptyPrimitiveGLB instances a mesh of zero-vertex primitives on many nodes:
// it passes MaxTriangles and MaxVertices yet would create primitives x nodes
// parts.
func emptyPrimitiveGLB(primitives, nodes int, rootName string) []byte {
	b := newTestGLB()
	b.appendDoc("accessors", map[string]any{"componentType": 5126, "count": 0, "type": "VEC3"})
	list := make([]any, primitives)
	for i := range list {
		list[i] = map[string]any{"attributes": map[string]any{"POSITION": 0}}
	}
	b.appendDoc("meshes", map[string]any{"primitives": list})
	children := make([]any, nodes)
	b.appendDoc("nodes", map[string]any{"name": rootName, "children": children})
	for i := range children {
		children[i] = b.appendDoc("nodes", map[string]any{"mesh": 0})
	}
	b.doc["scenes"] = []any{map[string]any{"nodes": []any{0}}}
	b.doc["scene"] = 0
	return b.finish()
}

func TestParseRejectsPartExplosion(t *testing.T) {
	data := emptyPrimitiveGLB(256, 1024, "root")
	_, err := Parse(bytes.NewReader(data), int64(len(data)), DefaultLimits())
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Parse with %d parts: err = %v", 256*1024, err)
	}
}

func TestParseRejectsOversizedNodePath(t *testing.T) {
	data := emptyPrimitiveGLB(1, 1, strings.Repeat("n", maxPathBytes+1))
	_, err := Parse(bytes.NewReader(data), int64(len(data)), DefaultLimits())
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Parse with an oversized node path: err = %v", err)
	}
}

func TestParseKeepsModestEmptyPrimitives(t *testing.T) {
	data := emptyPrimitiveGLB(4, 8, "root")
	model, err := Parse(bytes.NewReader(data), int64(len(data)), DefaultLimits())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := len(model.parts); got != 32 {
		t.Fatalf("parts = %d, want 32", got)
	}
}
