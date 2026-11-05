package modelinspect

import (
	"math"
	"strconv"
)

// partPose maps a part's stored vertices to world space.
//
// glTF places an unskinned mesh with its node's world matrix. A skinned mesh
// ignores its node's transform: every vertex is the weighted sum of its
// joints' matrices, each the joint's world matrix times its inverse bind
// matrix, applied to the stored position. Inspection and rendering use the
// pose the file stores (the scene's node transforms, no animation), which is
// what a conforming viewer shows before playing an animation.
//
// When every joint matrix of a skin is the same, which is the case when the
// stored pose is the bind pose, the blend reduces to that one matrix and the
// part is posed rigidly. Otherwise each vertex is blended.
type partPose struct {
	rigid       mat4
	rigidNormal mat3
	skin        *skinBlend
	// mirrored marks a negative-determinant transform, which reverses the
	// winding of every triangle.
	mirrored bool
	// bindPose marks a skinned part whose joints could not be resolved. Its
	// stored positions are used as they are: per glTF its node transform does
	// not apply, and without joints the bind pose is the best estimate.
	bindPose bool
}

// skinBlend holds the per-vertex joint influences of one skinned part.
// joints and weights hold one VEC4 set per JOINTS_n/WEIGHTS_n pair.
type skinBlend struct {
	matrices []mat4
	normals  []mat3
	joints   [][]float32
	weights  [][]float32
}

func rigidPose(world mat4) partPose {
	return partPose{rigid: world, rigidNormal: world.normalMatrix(), mirrored: world.determinant3() < 0}
}

// pose returns how p reaches world space. It never fails: a skinned part
// whose skin cannot be evaluated falls back to its bind pose.
func (m *Model) pose(p *part) partPose {
	if p.skin < 0 {
		return rigidPose(p.world)
	}
	matrices, err := m.skinJointMatrices(p.skin)
	if err != nil {
		return bindPose()
	}
	if common, ok := commonMatrix(matrices); ok {
		return rigidPose(common)
	}
	blend, err := m.skinBlend(p, matrices)
	if err != nil {
		return bindPose()
	}
	return partPose{skin: blend, mirrored: matrices[0].determinant3() < 0}
}

func bindPose() partPose {
	pose := rigidPose(identity4())
	pose.bindPose = true
	return pose
}

// point returns vertex's position in world space; x, y and z are its stored
// position.
func (pose *partPose) point(vertex int, x, y, z float64) vec3 {
	if pose.skin == nil {
		return pose.rigid.transformPoint(x, y, z)
	}
	return pose.skin.point(vertex, x, y, z)
}

// normal returns vertex's normal in world space, normalized.
func (pose *partPose) normal(vertex int, n vec3) vec3 {
	if pose.skin == nil {
		return pose.rigidNormal.transform(n).normalized()
	}
	return pose.skin.normal(vertex, n)
}

// point blends the joint matrices by the vertex weights. Weights are
// normalized by their sum, as viewers do; a vertex without any usable weight
// keeps its stored position instead of collapsing to the origin.
func (s *skinBlend) point(vertex int, x, y, z float64) vec3 {
	var sum vec3
	total := 0.0
	for set := range s.joints {
		for component := 0; component < 4; component++ {
			weight := float64(s.weights[set][vertex*4+component])
			if !(weight > 0) || !finite(weight) {
				continue
			}
			joint := int(s.joints[set][vertex*4+component])
			sum = sum.add(s.matrices[joint].transformPoint(x, y, z).scale(weight))
			total += weight
		}
	}
	if !(total > 0) || !finite(total) {
		return vec3{x, y, z}
	}
	return sum.scale(1 / total)
}

func (s *skinBlend) normal(vertex int, n vec3) vec3 {
	var sum vec3
	for set := range s.joints {
		for component := 0; component < 4; component++ {
			weight := float64(s.weights[set][vertex*4+component])
			if !(weight > 0) || !finite(weight) {
				continue
			}
			joint := int(s.joints[set][vertex*4+component])
			sum = sum.add(s.normals[joint].transform(n).scale(weight))
		}
	}
	if sum.length() == 0 {
		return n.normalized()
	}
	return sum.normalized()
}

// skinJointMatrices returns a skin's joint matrices, computing them once.
// A failure is not cached, so a transient read error can succeed later.
func (m *Model) skinJointMatrices(index int) ([]mat4, error) {
	m.skinCache.mu.Lock()
	defer m.skinCache.mu.Unlock()
	if cached, ok := m.skinCache.matrices[index]; ok {
		return cached, nil
	}
	matrices, err := m.computeSkinMatrices(index)
	if err != nil {
		return nil, err
	}
	m.skinCache.matrices[index] = matrices
	m.decodedBytes.Add(int64(len(matrices)) * mat4Bytes)
	return matrices, nil
}

const mat4Bytes = 16 * 8

// computeSkinMatrices evaluates jointMatrix = world(joint) * inverseBind for
// every joint. A joint outside the active scene has no world matrix, so the
// skin cannot be evaluated.
func (m *Model) computeSkinMatrices(index int) ([]mat4, error) {
	skin := m.Skins[index]
	if len(skin.Joints) == 0 {
		return nil, invalidf("skin %d has no joints", index)
	}
	var inverse []float32
	if skin.inverseBind >= 0 {
		a := m.accessors[skin.inverseBind]
		if a.components != 16 || a.componentType != 5126 || a.count < len(skin.Joints) {
			return nil, invalidf("skin %d inverseBindMatrices is not a MAT4 float per joint", index)
		}
		values, err := m.decodeF32(skin.inverseBind)
		if err != nil {
			return nil, err
		}
		inverse = values
	}
	matrices := make([]mat4, len(skin.Joints))
	for j, node := range skin.Joints {
		world, ok := m.jointWorld[node]
		if !ok {
			return nil, invalidf("skin %d joint node %d is not in the active scene", index, node)
		}
		inverseBind := identity4()
		if inverse != nil {
			for component := 0; component < 16; component++ {
				inverseBind[component] = float64(inverse[j*16+component])
			}
			// An inverse bind matrix is by definition invertible; a singular
			// one (for example all zeros) would collapse the mesh.
			if determinant := inverseBind.determinant3(); determinant == 0 || !finite(determinant) {
				return nil, invalidf("skin %d joint %d inverse bind matrix is singular", index, j)
			}
		}
		matrices[j] = world.mul(inverseBind)
		if !matrices[j].finite() {
			return nil, invalidf("skin %d joint %d matrix is not finite", index, j)
		}
	}
	return matrices, nil
}

// commonMatrix reports whether every joint matrix equals the first one up to
// float32 rounding of the inverse bind matrices. The linear part and the
// translation are compared at their own scale, so a small skeleton scale
// such as 0.01 does not hide a real difference behind a large translation.
func commonMatrix(matrices []mat4) (mat4, bool) {
	first := matrices[0]
	linearScale, translationScale := 1.0, 1.0
	for column := 0; column < 3; column++ {
		for row := 0; row < 3; row++ {
			linearScale = math.Max(linearScale, math.Abs(first[column*4+row]))
		}
		translationScale = math.Max(translationScale, math.Abs(first[12+column]))
	}
	const relative = 1e-6
	for _, matrix := range matrices[1:] {
		for column := 0; column < 4; column++ {
			for row := 0; row < 4; row++ {
				tolerance := relative * linearScale
				if column == 3 {
					tolerance = relative * translationScale
				}
				if math.Abs(matrix[column*4+row]-first[column*4+row]) > tolerance {
					return mat4{}, false
				}
			}
		}
	}
	return first, true
}

// skinBlend decodes the JOINTS_n/WEIGHTS_n pairs of a skinned part. Every
// joint index with a positive weight must name one of the skin's joints.
func (m *Model) skinBlend(p *part, matrices []mat4) (*skinBlend, error) {
	attributes := m.Meshes[p.mesh].Primitives[p.primitive].Attributes
	blend := &skinBlend{matrices: matrices, normals: make([]mat3, len(matrices))}
	for j, matrix := range matrices {
		blend.normals[j] = matrix.normalMatrix()
	}
	for set := 0; ; set++ {
		jointsIndex, hasJoints := attributes["JOINTS_"+strconv.Itoa(set)]
		weightsIndex, hasWeights := attributes["WEIGHTS_"+strconv.Itoa(set)]
		if !hasJoints && !hasWeights {
			break
		}
		if !hasJoints || !hasWeights {
			return nil, invalidf("part %s has unpaired skinning attributes", p.id)
		}
		jointsAccessor, weightsAccessor := m.accessors[jointsIndex], m.accessors[weightsIndex]
		if jointsAccessor.components != 4 || jointsAccessor.normalized ||
			(jointsAccessor.componentType != 5121 && jointsAccessor.componentType != 5123) ||
			weightsAccessor.components != 4 {
			return nil, invalidf("part %s skinning attributes have the wrong layout", p.id)
		}
		joints, err := m.decodeF32(jointsIndex)
		if err != nil {
			return nil, err
		}
		weights, err := m.decodeF32(weightsIndex)
		if err != nil {
			return nil, err
		}
		if len(joints) != p.vertexCount*4 || len(weights) != p.vertexCount*4 {
			return nil, invalidf("part %s skinning attributes have the wrong length", p.id)
		}
		for i, weight := range weights {
			if weight > 0 && int(joints[i]) >= len(matrices) {
				return nil, invalidf("part %s references joint %d of %d", p.id, int(joints[i]), len(matrices))
			}
		}
		blend.joints = append(blend.joints, joints)
		blend.weights = append(blend.weights, weights)
	}
	if len(blend.joints) == 0 {
		return nil, invalidf("part %s is skinned without JOINTS_0 and WEIGHTS_0", p.id)
	}
	return blend, nil
}
