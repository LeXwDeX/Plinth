package modelinspect

import (
	"testing"
)

// skinnedCubeOptions describe a unit cube skinned to an armature, the way
// DCC exports place characters: an armature root node (often scaled 0.01 by
// centimetre exports) whose children are the joints and, optionally, the
// skinned mesh node itself.
type skinnedCubeOptions struct {
	// armatureScale scales the armature root node; zero means 1.
	armatureScale float64
	// meshUnderArmature makes the skinned mesh node a child of the armature,
	// so its own node transform includes the armature scale.
	meshUnderArmature bool
	// inverseBindUndoesJoints stores each inverse bind matrix as the inverse
	// of its joint's world matrix: the vertices are already where a viewer
	// shows them. Otherwise every inverse bind matrix is the identity and the
	// joints carry the armature transform onto the vertices.
	inverseBindUndoesJoints bool
	// secondJointOffset > 0 adds a joint translated along X that drives the
	// +X half of the cube, so the joints no longer share one matrix.
	secondJointOffset float64
	// jointsOutsideScene leaves the armature out of the active scene.
	jointsOutsideScene bool
	// singularInverseBind writes all-zero inverse bind matrices.
	singularInverseBind bool
}

func skinnedCubeDocument(options skinnedCubeOptions) *testGLB {
	scale := options.armatureScale
	if scale == 0 {
		scale = 1
	}
	b := newTestGLB()
	corners := cubePositions()
	position := b.addVec3Accessor(corners, true)
	indices := b.addIndices16(flattenFaces(cubeFaces()))
	joints := make([]byte, 8*4)
	weights := make([]float32, 8*4)
	for vertex := 0; vertex < 8; vertex++ {
		if options.secondJointOffset > 0 && corners[vertex*3] > 0 {
			joints[vertex*4] = 1
		}
		weights[vertex*4] = 1
	}
	jointsView := b.addBufferView(joints, 0)
	jointsAccessor := b.addAccessor(jointsView, 0, 5121, 8, "VEC4", false, nil, nil)
	weightsView := b.addBufferView(floatBytes(weights), 0)
	weightsAccessor := b.addAccessor(weightsView, 0, 5126, 8, "VEC4", false, nil, nil)
	mesh := b.addMesh([]any{map[string]any{
		"attributes": map[string]int{
			"POSITION": position, "JOINTS_0": jointsAccessor, "WEIGHTS_0": weightsAccessor,
		},
		"indices": indices,
	}})

	// Joint world matrices: the armature scale S, then each joint's offset.
	jointWorlds := []mat4{trsMatrix([3]float64{}, [4]float64{0, 0, 0, 1}, [3]float64{scale, scale, scale})}
	jointNodes := []int{1}
	nodeCount := 2
	if options.secondJointOffset > 0 {
		jointWorlds = append(jointWorlds, jointWorlds[0].mul(
			trsMatrix([3]float64{options.secondJointOffset, 0, 0}, [4]float64{0, 0, 0, 1}, [3]float64{1, 1, 1})))
		jointNodes = append(jointNodes, 2)
		nodeCount = 3
	}
	meshNode := nodeCount
	armatureChildren := append([]int(nil), jointNodes...)
	if options.meshUnderArmature {
		armatureChildren = append(armatureChildren, meshNode)
	}
	b.addNode(map[string]any{
		"name": "armature", "children": armatureChildren,
		"scale": []float64{scale, scale, scale},
	})
	b.addNode(map[string]any{"name": "joint_root"})
	if options.secondJointOffset > 0 {
		b.addNode(map[string]any{"name": "joint_right", "translation": []float64{options.secondJointOffset, 0, 0}})
	}
	b.addNode(map[string]any{"name": "body", "mesh": mesh, "skin": 0})

	inverseBind := make([]float32, 0, 16*len(jointWorlds))
	for _, world := range jointWorlds {
		matrix := identity4()
		switch {
		case options.singularInverseBind:
			matrix = mat4{}
		case options.inverseBindUndoesJoints:
			matrix = inverseAffine(world)
		}
		for _, value := range matrix {
			inverseBind = append(inverseBind, float32(value))
		}
	}
	inverseView := b.addBufferView(floatBytes(inverseBind), 0)
	inverseAccessor := b.addAccessor(inverseView, 0, 5126, len(jointWorlds), "MAT4", false, nil, nil)
	b.appendDoc("skins", map[string]any{"name": "rig", "joints": jointNodes, "inverseBindMatrices": inverseAccessor})

	switch {
	case options.jointsOutsideScene:
		b.addScene([]int{meshNode})
	case options.meshUnderArmature:
		b.addScene([]int{0})
	default:
		b.addScene([]int{0, meshNode})
	}
	return b
}

// inverseAffine inverts the T*R*S matrices these tests build (uniform scale
// and translation only).
func inverseAffine(m mat4) mat4 {
	scale := m[0]
	out := identity4()
	out[0], out[5], out[10] = 1/scale, 1/scale, 1/scale
	out[12], out[13], out[14] = -m[12]/scale, -m[13]/scale, -m[14]/scale
	return out
}

func assertBox(t *testing.T, box BBoxReport, min, max [3]float64, label string) {
	t.Helper()
	for axis := 0; axis < 3; axis++ {
		approx(t, box.Min[axis], min[axis], 1e-5, label+" min")
		approx(t, box.Max[axis], max[axis], 1e-5, label+" max")
	}
}

// A skinned mesh ignores its own node transform (glTF 2.0, skins). A
// centimetre export whose mesh node sits under a 0.01 armature, with inverse
// bind matrices that undo the joints, is shown at its stored size; applying
// the node transform shrank it a hundredfold.
func TestInspectSkinnedMeshIgnoresItsNodeTransform(t *testing.T) {
	model := mustParseModel(t, skinnedCubeDocument(skinnedCubeOptions{
		armatureScale: 0.01, meshUnderArmature: true, inverseBindUndoesJoints: true,
	}).finish(), DefaultLimits())
	report := Inspect(model, DefaultInspectOptions())
	assertBox(t, report.BBox, [3]float64{-0.5, -0.5, -0.5}, [3]float64{0.5, 0.5, 0.5}, "bbox")
	approx(t, report.BBox.BottomCenterOffset[1], -0.5, 1e-5, "bottom centre y")
	if containsWarning(report, "skin_bind_pose") || !report.Topology.Watertight {
		t.Fatalf("warnings %v topology %+v", report.Warnings, report.Topology)
	}
	img := renderOrFail(t, model, RenderRequest{Width: 32, Height: 32, View: ViewFront, Mode: ModeClay, Workers: 1})
	if countNonBackground(img) < 100 {
		t.Fatalf("skinned cube rendered %d pixels", countNonBackground(img))
	}
}

// The joints, not the mesh node, place a skinned mesh: with identity inverse
// bind matrices the armature's 0.01 scale reaches the vertices even though
// the mesh node itself is unscaled.
func TestInspectSkinnedMeshFollowsItsJoints(t *testing.T) {
	model := mustParseModel(t, skinnedCubeDocument(skinnedCubeOptions{armatureScale: 0.01}).finish(), DefaultLimits())
	report := Inspect(model, DefaultInspectOptions())
	assertBox(t, report.BBox, [3]float64{-0.005, -0.005, -0.005}, [3]float64{0.005, 0.005, 0.005}, "bbox")
	for axis := 0; axis < 3; axis++ {
		approx(t, report.Parts[0].BBox.Size[axis], 0.01, 1e-7, "part size")
	}
}

// Joints with different matrices blend per vertex: the half of the cube
// bound to a joint ten metres to the right moves there.
func TestInspectSkinnedMeshBlendsPerVertex(t *testing.T) {
	model := mustParseModel(t, skinnedCubeDocument(skinnedCubeOptions{secondJointOffset: 10}).finish(), DefaultLimits())
	report := Inspect(model, DefaultInspectOptions())
	assertBox(t, report.BBox, [3]float64{-0.5, -0.5, -0.5}, [3]float64{10.5, 0.5, 0.5}, "bbox")
	if !report.Topology.Watertight || report.Topology.ConnectedComponents != 1 {
		t.Fatalf("stretched cube topology = %+v", report.Topology)
	}
	pose := model.pose(model.parts[0])
	if pose.skin == nil {
		t.Fatal("different joint matrices were posed rigidly")
	}
	img := renderOrFail(t, model, RenderRequest{Width: 32, Height: 32, View: ViewFront, Mode: ModeNormals, Workers: 1})
	if countNonBackground(img) < 50 {
		t.Fatalf("blended cube rendered %d pixels", countNonBackground(img))
	}
}

// Joints outside the active scene have no pose; the stored (bind pose)
// positions are reported and the approximation is named.
func TestInspectSkinWithoutJointsFallsBackToBindPose(t *testing.T) {
	model := mustParseModel(t, skinnedCubeDocument(skinnedCubeOptions{
		armatureScale: 0.01, jointsOutsideScene: true,
	}).finish(), DefaultLimits())
	report := Inspect(model, DefaultInspectOptions())
	assertBox(t, report.BBox, [3]float64{-0.5, -0.5, -0.5}, [3]float64{0.5, 0.5, 0.5}, "bbox")
	if !containsWarning(report, "skin_bind_pose") {
		t.Fatalf("warnings = %v, want skin_bind_pose", report.Warnings)
	}
}

// A singular inverse bind matrix (all zeros, as some broken exporters write)
// would collapse the mesh to a point; the skin is treated as unusable.
func TestInspectSingularInverseBindFallsBackToBindPose(t *testing.T) {
	model := mustParseModel(t, skinnedCubeDocument(skinnedCubeOptions{
		armatureScale: 0.01, singularInverseBind: true,
	}).finish(), DefaultLimits())
	report := Inspect(model, DefaultInspectOptions())
	assertBox(t, report.BBox, [3]float64{-0.5, -0.5, -0.5}, [3]float64{0.5, 0.5, 0.5}, "bbox")
	if !containsWarning(report, "skin_bind_pose") {
		t.Fatalf("warnings = %v, want skin_bind_pose", report.Warnings)
	}
}
