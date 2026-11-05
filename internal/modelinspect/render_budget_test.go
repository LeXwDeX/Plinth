package modelinspect

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func parseTestGLB(t *testing.T, data []byte) *Model {
	t.Helper()
	model, err := Parse(bytes.NewReader(data), int64(len(data)), DefaultLimits())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return model
}

// A cancelled context stops rendering instead of drawing the whole view.
func TestRenderStopsOnCancelledContext(t *testing.T) {
	model := parseTestGLB(t, cubeGLB())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Render(model, RenderRequest{Width: 32, Height: 32, Workers: 1, Context: ctx})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Render with a cancelled context: err = %v", err)
	}
}

// One triangle repeated many times covers the image thousands of times over;
// the coverage budget rejects it instead of rasterising for hours.
func TestRenderRejectsExcessiveOverdraw(t *testing.T) {
	b := newTestGLB()
	position := b.addVec3Accessor([]float32{-1, -1, 0, 1, -1, 0, 0, 1, 0}, true)
	repeated := make([]uint16, 0, 3*4000)
	for range 4000 {
		repeated = append(repeated, 0, 1, 2)
	}
	indices := b.addIndices16(repeated)
	mesh := b.addMesh([]any{map[string]any{
		"attributes": map[string]int{"POSITION": position},
		"indices":    indices,
	}})
	b.addScene([]int{b.addNode(map[string]any{"mesh": mesh})})
	model := parseTestGLB(t, b.finish())
	_, err := Render(model, RenderRequest{Width: 16, Height: 16, Workers: 2})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Render with heavy overdraw: err = %v", err)
	}
}

// A model whose only faces are degenerate has no closed surface to report.
func TestTopologyDoesNotCallUnanalysedGeometryWatertight(t *testing.T) {
	b := newTestGLB()
	position := b.addVec3Accessor([]float32{0, 0, 0, 1, 0, 0, 0, 1, 0}, true)
	indices := b.addIndices16([]uint16{0, 0, 0, 1, 1, 1})
	mesh := b.addMesh([]any{map[string]any{
		"attributes": map[string]int{"POSITION": position},
		"indices":    indices,
	}})
	b.addScene([]int{b.addNode(map[string]any{"mesh": mesh})})
	report := Inspect(parseTestGLB(t, b.finish()), DefaultInspectOptions())
	if report.Topology.Watertight {
		t.Fatalf("degenerate-only model reported watertight: %+v", report.Topology)
	}
	cube := Inspect(parseTestGLB(t, cubeGLB()), DefaultInspectOptions())
	if !cube.Topology.Watertight {
		t.Fatalf("closed cube not watertight: %+v", cube.Topology)
	}
}
