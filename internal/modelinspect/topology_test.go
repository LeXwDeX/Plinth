package modelinspect

import (
	"bytes"
	"math"
	"testing"
)

// A closed cube with one face turned inside out has no boundary or
// non-manifold edge, but its four border edges are walked the same way by
// both neighbours. That is not a printable closed surface.
func TestInspectFlippedFaceIsNotWatertight(t *testing.T) {
	faces := cubeFaces()
	plusZ := faces[0]
	faces[0] = []uint16{plusZ[0], plusZ[2], plusZ[1], plusZ[3], plusZ[5], plusZ[4]}
	b := newTestGLB()
	position := b.addVec3Accessor(cubePositions(), true)
	indices := b.addIndices16(flattenFaces(faces))
	mesh := b.addMesh([]any{map[string]any{"attributes": map[string]int{"POSITION": position}, "indices": indices}})
	b.addScene([]int{b.addNode(map[string]any{"mesh": mesh})})
	report := Inspect(mustParseModel(t, b.finish(), DefaultLimits()), DefaultInspectOptions())
	topology := report.Topology
	if topology.BoundaryEdges != 0 || topology.NonManifoldEdges != 0 {
		t.Fatalf("flipped cube is not closed: %+v", topology)
	}
	if topology.InconsistentWindingEdges != 4 || topology.Watertight {
		t.Fatalf("flipped cube topology = %+v, want 4 inconsistent edges and not watertight", topology)
	}
	if !containsWarning(report, "not_watertight") {
		t.Fatalf("warnings = %v, want not_watertight", report.Warnings)
	}
}

// cubeIndices returns the unit cube's triangles, every one reversed when
// inward is set, which turns all faces inward but keeps them consistent.
func cubeIndices(inward bool) []uint16 {
	indices := flattenFaces(cubeFaces())
	if inward {
		for i := 0; i < len(indices); i += 3 {
			indices[i+1], indices[i+2] = indices[i+2], indices[i+1]
		}
	}
	return indices
}

// cubeNodesDocument places one cube per node; each node lists whether its
// cube faces inward and the node's scale.
func cubeNodesDocument(nodes ...cubeNode) *testGLB {
	b := newTestGLB()
	position := b.addVec3Accessor(cubePositions(), true)
	var roots []int
	for _, node := range nodes {
		mesh := b.addMesh([]any{map[string]any{
			"attributes": map[string]int{"POSITION": position},
			"indices":    b.addIndices16(cubeIndices(node.inward)),
		}})
		roots = append(roots, b.addNode(map[string]any{"mesh": mesh, "scale": node.scale[:]}))
	}
	b.addScene(roots)
	return b
}

type cubeNode struct {
	inward bool
	scale  [3]float64
}

// A closed, consistently wound cube whose faces all point inward encloses
// a negative volume: it is reported inside out and not watertight.
func TestInspectInsideOutCubeIsNotWatertight(t *testing.T) {
	report := Inspect(mustParseModel(t, cubeNodesDocument(cubeNode{inward: true, scale: [3]float64{1, 1, 1}}).finish(),
		DefaultLimits()), DefaultInspectOptions())
	topology := report.Topology
	if topology.BoundaryEdges != 0 || topology.NonManifoldEdges != 0 || topology.InconsistentWindingEdges != 0 {
		t.Fatalf("inverted cube is not a closed, consistent surface: %+v", topology)
	}
	if !topology.InsideOut || topology.Watertight {
		t.Fatalf("inverted cube topology = %+v, want inside out and not watertight", topology)
	}
	if !containsWarning(report, "inside_out") || !containsWarning(report, "not_watertight") {
		t.Fatalf("warnings = %v, want inside_out and not_watertight", report.Warnings)
	}
	outward := Inspect(mustParseModel(t, cubeGLB(), DefaultLimits()), DefaultInspectOptions())
	if outward.Topology.InsideOut || containsWarning(outward, "inside_out") {
		t.Fatalf("outward cube reported inside out: %+v", outward.Topology)
	}
}

// glTF makes a mirrored instance's front faces clockwise: an outward cube
// under scale (-1, 1, 1) still faces outward, and an inverted one inward.
func TestInspectInsideOutHonoursMirroring(t *testing.T) {
	mirror := [3]float64{-1, 1, 1}
	outward := Inspect(mustParseModel(t, cubeNodesDocument(cubeNode{scale: mirror}).finish(),
		DefaultLimits()), DefaultInspectOptions())
	if outward.Topology.InsideOut || !outward.Topology.Watertight {
		t.Fatalf("mirrored outward cube = %+v", outward.Topology)
	}
	inward := Inspect(mustParseModel(t, cubeNodesDocument(cubeNode{inward: true, scale: mirror}).finish(),
		DefaultLimits()), DefaultInspectOptions())
	if !inward.Topology.InsideOut || inward.Topology.Watertight {
		t.Fatalf("mirrored inverted cube = %+v", inward.Topology)
	}
	halves := Inspect(mustParseModel(t, halfBoxDocument().finish(), DefaultLimits()), DefaultInspectOptions())
	if halves.Topology.InsideOut {
		t.Fatalf("mirrored halves reported inside out: %+v", halves.Topology)
	}
}

// A hollow solid's cavity shell faces inward by design; it subtracts from
// the outer shell's volume, so the model is not inside out.
func TestInspectHollowSolidIsNotInsideOut(t *testing.T) {
	report := Inspect(mustParseModel(t, cubeNodesDocument(
		cubeNode{scale: [3]float64{2, 2, 2}},
		cubeNode{inward: true, scale: [3]float64{1, 1, 1}},
	).finish(), DefaultLimits()), DefaultInspectOptions())
	topology := report.Topology
	if topology.InsideOut || !topology.Watertight || topology.ConnectedComponents != 2 {
		t.Fatalf("hollow box topology = %+v", topology)
	}
}

// halfBoxDocument is the x in [0, 1] half of a 1 x 1 x 1 box without its
// x = 0 face, used twice: once as is and once mirrored by scale (-1, 1, 1).
// Together the halves close the box along the seam at x = 0.
func halfBoxDocument() *testGLB {
	corners := cubePositions()
	for vertex := 0; vertex < 8; vertex++ {
		corners[vertex*3] += 0.5
	}
	faces := cubeFaces()
	var indices []uint16
	for face, list := range faces {
		if face == 3 { // the -X face lies on the seam
			continue
		}
		indices = append(indices, list...)
	}
	b := newTestGLB()
	position := b.addVec3Accessor(corners, true)
	index := b.addIndices16(indices)
	mesh := b.addMesh([]any{map[string]any{"attributes": map[string]int{"POSITION": position}, "indices": index}})
	right := b.addNode(map[string]any{"name": "right", "mesh": mesh})
	left := b.addNode(map[string]any{"name": "left", "mesh": mesh, "scale": []float64{-1, 1, 1}})
	b.addScene([]int{right, left})
	return b
}

// A mirrored instance's triangles wind the other way in world space, which
// glTF defines as its front face. Judging winding without the flip would
// report every seam edge as inconsistent.
func TestInspectMirroredHalvesWindConsistently(t *testing.T) {
	report := Inspect(mustParseModel(t, halfBoxDocument().finish(), DefaultLimits()), DefaultInspectOptions())
	topology := report.Topology
	if !topology.Watertight || topology.InconsistentWindingEdges != 0 || topology.BoundaryEdges != 0 ||
		topology.ConnectedComponents != 1 {
		t.Fatalf("mirrored halves topology = %+v", topology)
	}
}

// stitchedStripDocument draws two separate quads with one indexed triangle
// strip, joined by repeating indices 3 and 4: ten indices declare eight
// strip triangles, four of which are stitching.
func stitchedStripDocument() *testGLB {
	b := newTestGLB()
	position := b.addVec3Accessor([]float32{
		0, 0, 0, 1, 0, 0, 0, 1, 0, 1, 1, 0,
		2, 0, 0, 3, 0, 0, 2, 1, 0, 3, 1, 0,
	}, true)
	indices := b.addIndices16([]uint16{0, 1, 2, 3, 3, 4, 4, 5, 6, 7})
	mesh := b.addMesh([]any{map[string]any{
		"attributes": map[string]int{"POSITION": position}, "indices": indices, "mode": 5,
	}})
	b.addScene([]int{b.addNode(map[string]any{"mesh": mesh})})
	return b
}

// Strip stitching triangles are how strips join and restart, not faces: they
// count neither as triangles nor as degenerate faces.
func TestInspectStripStitchingIsNotATriangle(t *testing.T) {
	model := mustParseModel(t, stitchedStripDocument().finish(), DefaultLimits())
	if model.parts[0].triangleCount != 8 {
		t.Fatalf("declared strip triangles = %d, want 8", model.parts[0].triangleCount)
	}
	report := Inspect(model, DefaultInspectOptions())
	if report.Triangles != 4 || report.Parts[0].Triangles != 4 || report.Parts[0].TriangleRatio != 1 {
		t.Fatalf("triangles = %d, part = %+v, want 4 drawable triangles", report.Triangles, report.Parts[0])
	}
	if report.Topology.DegenerateTriangles != 0 || report.Topology.ConnectedComponents != 2 {
		t.Fatalf("topology = %+v, want no degenerate faces and two quads", report.Topology)
	}
	// A TRIANGLES face that repeats an index is still a degenerate face.
	b := cubeDocument()
	degenerate := b.addIndices16(append(flattenFaces(cubeFaces()), 0, 0, 1))
	b.doc["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)["indices"] = degenerate
	report = Inspect(mustParseModel(t, b.finish(), DefaultLimits()), DefaultInspectOptions())
	if report.Triangles != 13 || report.Topology.DegenerateTriangles != 1 {
		t.Fatalf("triangles = %d, topology = %+v, want 13 triangles with 1 degenerate", report.Triangles, report.Topology)
	}
}

// cubeWithStrayGeometry is the unit cube whose POSITION accessor also holds
// a far vertex and a NaN vertex no index uses, plus a line primitive 50 m
// away.
func cubeWithStrayGeometry() *testGLB {
	b := newTestGLB()
	positions := append(cubePositions(), 100, 100, 100, float32(math.NaN()), 0, 0)
	position := b.addVec3Accessor(positions, false)
	indices := b.addIndices16(flattenFaces(cubeFaces()))
	line := b.addVec3Accessor([]float32{50, 0, 0, 51, 0, 0}, true)
	mesh := b.addMesh([]any{
		map[string]any{"attributes": map[string]int{"POSITION": position}, "indices": indices},
		map[string]any{"attributes": map[string]int{"POSITION": line}, "mode": 1},
	})
	b.addScene([]int{b.addNode(map[string]any{"name": "cube", "mesh": mesh})})
	return b
}

// The model box covers what triangles draw: unreferenced vertices and point
// or line helpers neither stretch it nor (for a NaN) poison the topology.
func TestInspectBBoxCoversOnlyReferencedTriangleGeometry(t *testing.T) {
	report := Inspect(mustParseModel(t, cubeWithStrayGeometry().finish(), DefaultLimits()), DefaultInspectOptions())
	assertBox(t, report.BBox, [3]float64{-0.5, -0.5, -0.5}, [3]float64{0.5, 0.5, 0.5}, "model bbox")
	if !report.Topology.Watertight || containsWarning(report, "invalid_positions") {
		t.Fatalf("topology %+v warnings %v", report.Topology, report.Warnings)
	}
	if report.LinePrimitives != 1 || !containsWarning(report, "unsupported_primitive_mode") {
		t.Fatalf("line primitive not reported: %+v %v", report.LinePrimitives, report.Warnings)
	}
	// The line part still reports where it is.
	line := report.Parts[1]
	approx(t, line.BBox.Min[0], 50, 1e-9, "line min x")
	approx(t, line.BBox.Max[0], 51, 1e-9, "line max x")
	cube := report.Parts[0]
	approx(t, cube.BBox.Max[0], 0.5, 1e-9, "cube part max x")
}

// Render frames the same geometry: an unreferenced far vertex does not
// shrink the model in the picture.
func TestRenderFramesOnlyReferencedGeometry(t *testing.T) {
	stray := mustParseModel(t, cubeWithStrayGeometry().finish(), DefaultLimits())
	plain := mustParseModel(t, cubeGLB(), DefaultLimits())
	request := RenderRequest{Width: 32, Height: 32, View: ViewThreeQuarter, Mode: ModeClay, Workers: 1}
	got, want := renderOrFail(t, stray, request), renderOrFail(t, plain, request)
	if !bytes.Equal(got.Pix, want.Pix) {
		t.Fatalf("stray geometry changed the framing: %d vs %d drawn pixels",
			countNonBackground(got), countNonBackground(want))
	}
}
