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

// A long name on a node with thousands of children is copied into every
// child's path: 4500 children of a 16000-byte name would allocate about
// 69 MiB of paths from a JSON chunk of about 100 KiB. Each path alone is
// below maxPathBytes, so only the total ceiling stops it.
func TestParseRejectsNodePathAmplification(t *testing.T) {
	name := strings.Repeat("n", 16000)
	data := emptyPrimitiveGLB(1, 4500, name)
	_, err := Parse(bytes.NewReader(data), int64(len(data)), DefaultLimits())
	if !errors.Is(err, ErrTooLarge) || !strings.Contains(err.Error(), "in total") {
		t.Fatalf("Parse with %d bytes of node paths: err = %v", 4500*16008, err)
	}
	// The same hierarchy with 1000 children (about 16 MiB of paths) is
	// within the ceiling.
	data = emptyPrimitiveGLB(1, 1000, name)
	model, err := Parse(bytes.NewReader(data), int64(len(data)), DefaultLimits())
	if err != nil {
		t.Fatalf("Parse with 1000 long paths: %v", err)
	}
	if len(model.parts) != 1000 {
		t.Fatalf("parts = %d, want 1000", len(model.parts))
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
