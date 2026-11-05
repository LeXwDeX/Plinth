package modelinspect

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// RetainedBytes grows with what Inspect and Render decode: positions and
// indices for Inspect, then texture coordinates and the decoded texture
// for a textured render.
func TestRetainedBytesCountsDecodedCaches(t *testing.T) {
	model := mustParseModel(t, texturedQuadGLB(t), DefaultLimits())
	parsed := model.RetainedBytes()
	if parsed <= 0 {
		t.Fatalf("parsed model retains %d bytes", parsed)
	}
	Inspect(model, DefaultInspectOptions())
	inspected := model.RetainedBytes()
	// 4 positions x 3 floats and 6 indices, four bytes each.
	if inspected < parsed+4*3*4+6*4 {
		t.Fatalf("after Inspect: %d bytes, want at least %d", inspected, parsed+4*3*4+6*4)
	}
	renderOrFail(t, model, RenderRequest{Width: 16, Height: 16, Mode: ModeTextured, Workers: 1})
	rendered := model.RetainedBytes()
	// 4 UV pairs and a 2x2 RGBA texture.
	if rendered < inspected+4*2*4+2*2*4 {
		t.Fatalf("after Render: %d bytes, want at least %d", rendered, inspected+4*2*4+2*2*4)
	}
}

// A one-byte component is cached as a four-byte float32, so the cache must
// be charged four times the bytes the file stores.
func TestRetainedBytesCountsByteAccessorsExpanded(t *testing.T) {
	b := cubeDocument()
	colors := make([]byte, 8*4)
	for i := range colors {
		colors[i] = 200
	}
	view := b.addBufferView(colors, 0)
	color := b.addAccessor(view, 0, 5121, 8, "VEC4", true, nil, nil)
	b.doc["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)["attributes"].(map[string]int)["COLOR_0"] = color
	model := mustParseModel(t, b.finish(), DefaultLimits())
	before := model.RetainedBytes()
	if _, err := model.colors(model.parts[0]); err != nil {
		t.Fatal(err)
	}
	if grown := model.RetainedBytes() - before; grown != 8*4*4 {
		t.Fatalf("decoding 32 stored colour bytes grew the model by %d bytes, want %d", grown, 8*4*4)
	}
}

func TestReportRetainedBytesGrowsWithContent(t *testing.T) {
	small := Inspect(mustParseModel(t, cubeGLB(), DefaultLimits()), DefaultInspectOptions())
	large := Inspect(mustParseModel(t, twoCubesAndSliverDocument().finish(), DefaultLimits()), DefaultInspectOptions())
	if small.RetainedBytes() <= 0 || large.RetainedBytes() <= small.RetainedBytes() {
		t.Fatalf("report sizes: cube %d, three parts %d", small.RetainedBytes(), large.RetainedBytes())
	}
}

// InspectContext stops with the context's error instead of finishing the
// topology pass, and matches Inspect when the context stays live.
func TestInspectContextStopsWhenCancelled(t *testing.T) {
	model := mustParseModel(t, cubeGLB(), DefaultLimits())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err := InspectContext(ctx, model, DefaultInspectOptions())
	if !errors.Is(err, context.Canceled) || report.Triangles != 0 {
		t.Fatalf("cancelled InspectContext = %+v, %v", report.Topology, err)
	}
	live, err := InspectContext(context.Background(), model, DefaultInspectOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(live, Inspect(model, DefaultInspectOptions())) {
		t.Fatal("InspectContext and Inspect disagree")
	}
}
