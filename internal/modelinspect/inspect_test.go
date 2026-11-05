package modelinspect

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func approx(t *testing.T, got, want, tolerance float64, label string) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Fatalf("%s = %v, want %v +/- %v", label, got, want, tolerance)
	}
}

func containsWarning(report Report, warning string) bool {
	for _, value := range report.Warnings {
		if value == warning {
			return true
		}
	}
	return false
}

func TestInspectCube(t *testing.T) {
	model := mustParseModel(t, cubeGLB(), DefaultLimits())
	report := Inspect(model, DefaultInspectOptions())

	if report.Triangles != 12 || report.Vertices != 8 || report.Primitives != 1 {
		t.Fatalf("unexpected totals: %+v", report)
	}
	if report.Meshes != 1 || report.Nodes != 1 || report.MaterialCount != 0 || report.TextureCount != 0 {
		t.Fatalf("unexpected counts: %+v", report)
	}
	if len(report.Parts) != 1 {
		t.Fatalf("parts = %d, want 1", len(report.Parts))
	}
	part := report.Parts[0]
	if part.ID != "n0.p0" || part.Node != "cube" || part.Mesh != "mesh_0" {
		t.Fatalf("unexpected part identity: %+v", part)
	}
	if part.Triangles != 12 || part.TriangleRatio != 1 {
		t.Fatalf("unexpected part counts: %+v", part)
	}
	for i := 0; i < 3; i++ {
		approx(t, part.BBox.Min[i], -0.5, 1e-9, "part min")
		approx(t, part.BBox.Max[i], 0.5, 1e-9, "part max")
		approx(t, report.BBox.Size[i], 1, 1e-9, "bbox size")
	}
	if report.BBox.Unit != "m" {
		t.Fatalf("bbox unit = %q, want m", report.BBox.Unit)
	}
	approx(t, report.BBox.BottomCenterOffset[0], 0, 1e-9, "bottom centre x")
	approx(t, report.BBox.BottomCenterOffset[1], -0.5, 1e-9, "bottom centre y")
	approx(t, report.BBox.BottomCenterOffset[2], 0, 1e-9, "bottom centre z")

	topology := report.Topology
	if !topology.Watertight {
		t.Fatalf("cube is not watertight: %+v", topology)
	}
	if topology.ConnectedComponents != 1 || topology.LargestComponentRatio != 1 {
		t.Fatalf("unexpected components: %+v", topology)
	}
	if topology.BoundaryEdges != 0 || topology.NonManifoldEdges != 0 || topology.DegenerateTriangles != 0 {
		t.Fatalf("unexpected topology counters: %+v", topology)
	}
	if topology.DebrisComponents != 0 || topology.DebrisTriangles != 0 {
		t.Fatalf("unexpected debris: %+v", topology)
	}
	if !containsWarning(report, "missing_uv") {
		t.Fatalf("warnings = %v, want missing_uv", report.Warnings)
	}
	if !containsWarning(report, "missing_normals") {
		t.Fatalf("warnings = %v, want missing_normals", report.Warnings)
	}
	if report.UV.AllHaveUV {
		t.Fatal("AllHaveUV = true for a cube without UVs")
	}

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	for _, key := range []string{`"triangle_ratio"`, `"bottom_center_offset"`, `"non_manifold_edges"`, `"largest_component_ratio"`} {
		if !strings.Contains(string(encoded), key) {
			t.Fatalf("report JSON does not contain %s", key)
		}
	}
}

func TestInspectDisconnectedWithDebris(t *testing.T) {
	model := mustParseModel(t, twoCubesAndSliverDocument().finish(), DefaultLimits())
	report := Inspect(model, InspectOptions{WeldEpsilonRatio: 1e-4, DebrisRatio: 0.1})
	if report.Triangles != 25 || len(report.Parts) != 3 {
		t.Fatalf("unexpected totals: triangles=%d parts=%d", report.Triangles, len(report.Parts))
	}
	topology := report.Topology
	if topology.ConnectedComponents != 3 {
		t.Fatalf("ConnectedComponents = %d, want 3", topology.ConnectedComponents)
	}
	approx(t, topology.LargestComponentRatio, 12.0/25.0, 1e-9, "largest ratio")
	if topology.DebrisComponents != 1 || topology.DebrisTriangles != 1 {
		t.Fatalf("unexpected debris: %+v", topology)
	}
	if topology.Watertight {
		t.Fatalf("model with a standalone triangle is watertight: %+v", topology)
	}
	if topology.BoundaryEdges != 3 {
		t.Fatalf("BoundaryEdges = %d, want 3", topology.BoundaryEdges)
	}
	if !containsWarning(report, "has_debris") || !containsWarning(report, "not_watertight") {
		t.Fatalf("warnings = %v, want has_debris and not_watertight", report.Warnings)
	}
}

func TestInspectOpenPlane(t *testing.T) {
	model := mustParseModel(t, openPlaneDocument().finish(), DefaultLimits())
	report := Inspect(model, DefaultInspectOptions())
	topology := report.Topology
	if topology.Watertight || topology.BoundaryEdges != 4 {
		t.Fatalf("unexpected open plane topology: %+v", topology)
	}
	if topology.ConnectedComponents != 1 || topology.NonManifoldEdges != 0 {
		t.Fatalf("unexpected open plane components: %+v", topology)
	}
	if !containsWarning(report, "not_watertight") {
		t.Fatalf("warnings = %v, want not_watertight", report.Warnings)
	}
}

func TestInspectTRSHierarchy(t *testing.T) {
	model := mustParseModel(t, trsHierarchyDocument().finish(), DefaultLimits())
	if len(model.parts) != 1 {
		t.Fatalf("parts = %d, want 1", len(model.parts))
	}
	if model.parts[0].path != "parent/child" || model.parts[0].id != "n0.p0" {
		t.Fatalf("unexpected part path: %+v", model.parts[0])
	}
	report := Inspect(model, DefaultInspectOptions())
	wantMin := [3]float64{9, 1, -1}
	wantMax := [3]float64{11, 3, 1}
	for i := 0; i < 3; i++ {
		approx(t, report.BBox.Min[i], wantMin[i], 1e-9, "bbox min")
		approx(t, report.BBox.Max[i], wantMax[i], 1e-9, "bbox max")
		approx(t, report.Parts[0].BBox.Min[i], wantMin[i], 1e-9, "part bbox min")
		approx(t, report.Parts[0].BBox.Max[i], wantMax[i], 1e-9, "part bbox max")
	}
	approx(t, report.BBox.BottomCenterOffset[0], 10, 1e-9, "bottom centre x")
	approx(t, report.BBox.BottomCenterOffset[1], 1, 1e-9, "bottom centre y")
}

func TestInspectMaterialsAndUV(t *testing.T) {
	model := mustParseModel(t, texturedQuadGLB(t), DefaultLimits())
	report := Inspect(model, DefaultInspectOptions())
	if report.MaterialCount != 1 || report.TextureCount != 1 {
		t.Fatalf("unexpected material/texture counts: %+v", report)
	}
	if len(report.Materials) != 1 || len(report.Materials[0].Slots) != 5 {
		t.Fatalf("unexpected material report: %+v", report.Materials)
	}
	baseColor := report.Materials[0].Slots[0]
	if baseColor.Slot != "base_color" || !baseColor.Present {
		t.Fatalf("unexpected base color slot: %+v", baseColor)
	}
	if baseColor.Texture != 0 || baseColor.Image != 0 || baseColor.MIME != "image/png" {
		t.Fatalf("unexpected base color image reference: %+v", baseColor)
	}
	if baseColor.Width != 2 || baseColor.Height != 2 || baseColor.TexCoord != 0 {
		t.Fatalf("unexpected base color dimensions: %+v", baseColor)
	}
	if len(baseColor.Factor) != 4 || baseColor.OverLimit {
		t.Fatalf("unexpected base color factors: %+v", baseColor)
	}
	for _, slot := range report.Materials[0].Slots[1:] {
		if slot.Present {
			t.Fatalf("slot %s unexpectedly present", slot.Slot)
		}
	}
	if len(report.UV.Primitives) != 1 {
		t.Fatalf("UV primitives = %d, want 1", len(report.UV.Primitives))
	}
	sets := report.UV.Primitives[0].Sets
	if len(sets) != 1 || sets[0] != 0 || !report.UV.AllHaveUV {
		t.Fatalf("unexpected UV report: %+v", report.UV)
	}
	if containsWarning(report, "missing_uv") {
		t.Fatalf("warnings = %v, unexpected missing_uv", report.Warnings)
	}
}

func TestInspectTextureOverLimit(t *testing.T) {
	model := mustParseModel(t, texturedQuadGLB(t), Limits{MaxTexturePixels: 1})
	report := Inspect(model, DefaultInspectOptions())
	if !report.Materials[0].Slots[0].OverLimit {
		t.Fatalf("base color slot not marked over limit: %+v", report.Materials[0].Slots[0])
	}
	if !containsWarning(report, "texture_over_limit") {
		t.Fatalf("warnings = %v, want texture_over_limit", report.Warnings)
	}
}

func TestInspectSkinsAndAnimations(t *testing.T) {
	model := mustParseModel(t, skinAnimationDocument().finish(), DefaultLimits())
	report := Inspect(model, DefaultInspectOptions())
	if len(report.Skins) != 1 {
		t.Fatalf("skins = %d, want 1", len(report.Skins))
	}
	if report.Skins[0].Name != "rig" || report.Skins[0].Joints != 2 {
		t.Fatalf("unexpected skin report: %+v", report.Skins[0])
	}
	if len(report.Animations) != 1 {
		t.Fatalf("animations = %d, want 1", len(report.Animations))
	}
	animation := report.Animations[0]
	if animation.Name != "wave" || animation.Channels != 1 || animation.Translations != 1 {
		t.Fatalf("unexpected animation report: %+v", animation)
	}
	approx(t, animation.Duration, 2.5, 1e-9, "animation duration")
}

func TestInspectPointPrimitive(t *testing.T) {
	builder := newTestGLB()
	position := builder.addVec3Accessor([]float32{0, 0, 0}, true)
	mesh := builder.addMesh([]any{map[string]any{
		"attributes": map[string]int{"POSITION": position},
		"mode":       0,
	}})
	node := builder.addNode(map[string]any{"mesh": mesh})
	builder.addScene([]int{node})
	model := mustParseModel(t, builder.finish(), DefaultLimits())
	report := Inspect(model, DefaultInspectOptions())
	if report.PointPrimitives != 1 || report.Triangles != 0 {
		t.Fatalf("unexpected point counts: %+v", report)
	}
	if !containsWarning(report, "unsupported_primitive_mode") || !containsWarning(report, "no_triangles") {
		t.Fatalf("warnings = %v, want unsupported_primitive_mode and no_triangles", report.Warnings)
	}
}

func TestInspectNilModel(t *testing.T) {
	report := Inspect(nil, DefaultInspectOptions())
	if len(report.Warnings) != 1 || report.Warnings[0] != "nil_model" {
		t.Fatalf("unexpected nil model report: %+v", report)
	}
}
