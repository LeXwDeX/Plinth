package localparts

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"strconv"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// glbDocument is the deliberately narrow static-GLB subset this adapter
// rewrites. Fields that modelinput accepts but this adapter neither needs nor
// preserves (extensions, skins, animations, cameras) are intentionally absent;
// the adapter rejects those documents before building anything.
type glbDocument struct {
	Asset       assetDef          `json:"asset"`
	Scene       *int              `json:"scene,omitempty"`
	Scenes      []sceneDef        `json:"scenes"`
	Nodes       []nodeDef         `json:"nodes"`
	Meshes      []meshDef         `json:"meshes"`
	Materials   []materialDef     `json:"materials,omitempty"`
	Textures    []textureDef      `json:"textures,omitempty"`
	Images      []imageDef        `json:"images,omitempty"`
	Samplers    []json.RawMessage `json:"samplers,omitempty"`
	Accessors   []accessorDef     `json:"accessors"`
	BufferViews []bufferViewDef   `json:"bufferViews"`
	Buffers     []bufferDef       `json:"buffers"`
}

type assetDef struct {
	Version    string `json:"version"`
	Generator  string `json:"generator,omitempty"`
	Copyright  string `json:"copyright,omitempty"`
	MinVersion string `json:"minVersion,omitempty"`
}

type sceneDef struct {
	Name  string `json:"name,omitempty"`
	Nodes []int  `json:"nodes"`
	// Extras is carried only for the merge scene name; source scenes are not copied.
	Extras json.RawMessage `json:"extras,omitempty"`
}

type nodeDef struct {
	Name        string          `json:"name,omitempty"`
	Extras      json.RawMessage `json:"extras,omitempty"`
	Mesh        *int            `json:"mesh,omitempty"`
	Children    []int           `json:"children,omitempty"`
	Matrix      []float64       `json:"matrix,omitempty"`
	Translation []float64       `json:"translation,omitempty"`
	Rotation    []float64       `json:"rotation,omitempty"`
	Scale       []float64       `json:"scale,omitempty"`
}

type meshDef struct {
	Name       string         `json:"name,omitempty"`
	Primitives []primitiveDef `json:"primitives"`
}

type primitiveDef struct {
	Attributes map[string]int `json:"attributes"`
	Indices    *int           `json:"indices,omitempty"`
	Material   *int           `json:"material,omitempty"`
	Mode       *int           `json:"mode,omitempty"`
}

type accessorDef struct {
	BufferView    int       `json:"bufferView"`
	ByteOffset    int       `json:"byteOffset,omitempty"`
	ComponentType int       `json:"componentType"`
	Normalized    bool      `json:"normalized,omitempty"`
	Count         int       `json:"count"`
	Type          string    `json:"type"`
	Min           []float64 `json:"min,omitempty"`
	Max           []float64 `json:"max,omitempty"`
}

type bufferViewDef struct {
	Buffer     int  `json:"buffer"`
	ByteOffset int  `json:"byteOffset,omitempty"`
	ByteLength int  `json:"byteLength"`
	ByteStride *int `json:"byteStride,omitempty"`
	Target     *int `json:"target,omitempty"`
}

type bufferDef struct {
	ByteLength int `json:"byteLength"`
}

type imageDef struct {
	Name       string          `json:"name,omitempty"`
	Extras     json.RawMessage `json:"extras,omitempty"`
	BufferView int             `json:"bufferView"`
	MimeType   string          `json:"mimeType"`
}

type textureDef struct {
	Name    string          `json:"name,omitempty"`
	Extras  json.RawMessage `json:"extras,omitempty"`
	Source  int             `json:"source"`
	Sampler *int            `json:"sampler,omitempty"`
}

type textureRef struct {
	Index    int             `json:"index"`
	TexCoord int             `json:"tex_coord,omitempty"`
	Extras   json.RawMessage `json:"extras,omitempty"`
	// Scale/Strength are the scalar factors of normalTexture/occlusionTexture.
	Scale    *float64 `json:"scale,omitempty"`
	Strength *float64 `json:"strength,omitempty"`
}

type pbrDef struct {
	BaseColorFactor          []float64   `json:"baseColorFactor,omitempty"`
	BaseColorTexture         *textureRef `json:"baseColorTexture,omitempty"`
	MetallicFactor           *float64    `json:"metallicFactor,omitempty"`
	RoughnessFactor          *float64    `json:"roughnessFactor,omitempty"`
	MetallicRoughnessTexture *textureRef `json:"metallicRoughnessTexture,omitempty"`
}

type materialDef struct {
	Name           string          `json:"name,omitempty"`
	Extras         json.RawMessage `json:"extras,omitempty"`
	PBR            *pbrDef         `json:"pbrMetallicRoughness,omitempty"`
	Normal         *textureRef     `json:"normalTexture,omitempty"`
	Occlusion      *textureRef     `json:"occlusionTexture,omitempty"`
	Emissive       *textureRef     `json:"emissiveTexture,omitempty"`
	EmissiveFactor []float64       `json:"emissiveFactor,omitempty"`
	AlphaMode      string          `json:"alphaMode,omitempty"`
	AlphaCutoff    *float64        `json:"alphaCutoff,omitempty"`
	DoubleSided    bool            `json:"doubleSided,omitempty"`
}

// workBudget bounds total adapter work, not just one source document.
type workBudget struct {
	remaining int64
}

func newWorkBudget(limit int64) *workBudget { return &workBudget{remaining: limit} }

func (b *workBudget) charge(amount int64) error {
	if amount < 0 {
		return limitError("work")
	}
	b.remaining -= amount
	if b.remaining < 0 {
		return limitError("work")
	}
	return nil
}

// sourceModel is one parsed source GLB with its BIN payload.
type sourceModel struct {
	asset  generation.InputAsset
	doc    *glbDocument
	bin    []byte
	budget *workBudget
}

// parseGLB parses the already validated GLB container. Validation happened
// against modelinput before this call; the checks here protect the rewrite.
func parseGLB(raw []byte, policyLimits modelinput.Limits) (*glbDocument, []byte, error) {
	if len(raw) < 20 {
		return nil, nil, invalidModel("container")
	}
	jsonLength := int(binary.LittleEndian.Uint32(raw[12:16]))
	if jsonLength <= 0 || 20+jsonLength > len(raw) {
		return nil, nil, invalidModel("container")
	}
	doc := &glbDocument{}
	if err := json.Unmarshal(raw[20:20+jsonLength], doc); err != nil {
		return nil, nil, invalidModel("json")
	}
	binStart := 20 + jsonLength
	var bin []byte
	if binStart < len(raw) {
		if binStart+8 > len(raw) {
			return nil, nil, invalidModel("container")
		}
		binLength := int(binary.LittleEndian.Uint32(raw[binStart : binStart+4]))
		if binStart+8+binLength != len(raw) {
			return nil, nil, invalidModel("container")
		}
		bin = raw[binStart+8 : binStart+8+binLength]
	}
	if len(doc.Buffers) != 1 || doc.Buffers[0].ByteLength != len(bin) ||
		len(doc.Accessors) == 0 || len(doc.BufferViews) == 0 || len(doc.Meshes) == 0 || len(doc.Nodes) == 0 || len(doc.Scenes) == 0 {
		return nil, nil, invalidModel("structure")
	}
	if len(doc.Nodes) > int(policyLimits.MaxNodes) || len(doc.Accessors) > int(policyLimits.MaxElements) {
		return nil, nil, limitError("structure")
	}
	return doc, bin, nil
}

// ---------------------------------------------------------------------------
// Transform math. It intentionally mirrors internal/modelinspect so a world
// bounding box computed there for a source node instance and one computed here
// for the same node chain use the same float64 operations in the same order.
// ---------------------------------------------------------------------------

type vec3 [3]float64

func (a vec3) add(b vec3) vec3 { return vec3{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func (a vec3) sub(b vec3) vec3 { return vec3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func (a vec3) scale(s float64) vec3 {
	return vec3{a[0] * s, a[1] * s, a[2] * s}
}
func (a vec3) dot(b vec3) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func (a vec3) length() float64    { return math.Sqrt(a.dot(a)) }
func (a vec3) finite() bool {
	return finite(a[0]) && finite(a[1]) && finite(a[2])
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

type mat4 [16]float64

func identity4() mat4 {
	var m mat4
	m[0], m[5], m[10], m[15] = 1, 1, 1, 1
	return m
}

func (a mat4) mul(b mat4) mat4 {
	var out mat4
	for col := 0; col < 4; col++ {
		for row := 0; row < 4; row++ {
			out[col*4+row] =
				a[0*4+row]*b[col*4+0] +
					a[1*4+row]*b[col*4+1] +
					a[2*4+row]*b[col*4+2] +
					a[3*4+row]*b[col*4+3]
		}
	}
	return out
}

func (m mat4) transformPoint(x, y, z float64) vec3 {
	w := m[3]*x + m[7]*y + m[11]*z + m[15]
	if w == 0 || w == 1 {
		return vec3{
			m[0]*x + m[4]*y + m[8]*z + m[12],
			m[1]*x + m[5]*y + m[9]*z + m[13],
			m[2]*x + m[6]*y + m[10]*z + m[14],
		}
	}
	inv := 1 / w
	return vec3{
		(m[0]*x + m[4]*y + m[8]*z + m[12]) * inv,
		(m[1]*x + m[5]*y + m[9]*z + m[13]) * inv,
		(m[2]*x + m[6]*y + m[10]*z + m[14]) * inv,
	}
}

func trsMatrix(t [3]float64, q [4]float64, s [3]float64) mat4 {
	x, y, z, w := q[0], q[1], q[2], q[3]
	n := math.Sqrt(x*x + y*y + z*z + w*w)
	if n == 0 || !finite(n) {
		x, y, z, w = 0, 0, 0, 1
	} else {
		x, y, z, w = x/n, y/n, z/n, w/n
	}
	x2, y2, z2 := x+x, y+y, z+z
	xx, yy, zz := x*x2, y*y2, z*z2
	xy, xz, yz := x*y2, x*z2, y*z2
	wx, wy, wz := w*x2, w*y2, w*z2

	var m mat4
	m[0] = (1 - (yy + zz)) * s[0]
	m[1] = (xy + wz) * s[0]
	m[2] = (xz - wy) * s[0]
	m[4] = (xy - wz) * s[1]
	m[5] = (1 - (xx + zz)) * s[1]
	m[6] = (yz + wx) * s[1]
	m[8] = (xz + wy) * s[2]
	m[9] = (yz - wx) * s[2]
	m[10] = (1 - (xx + yy)) * s[2]
	m[12], m[13], m[14], m[15] = t[0], t[1], t[2], 1
	return m
}

func floatTriple(values []float64, fallback [3]float64) [3]float64 {
	if len(values) != 3 {
		return fallback
	}
	return [3]float64{values[0], values[1], values[2]}
}

func localMatrix(node nodeDef) mat4 {
	if len(node.Matrix) == 16 {
		var m mat4
		copy(m[:], node.Matrix)
		return m
	}
	translation := floatTriple(node.Translation, [3]float64{})
	rotation := [4]float64{0, 0, 0, 1}
	if len(node.Rotation) == 4 {
		rotation = [4]float64{node.Rotation[0], node.Rotation[1], node.Rotation[2], node.Rotation[3]}
	}
	scale := floatTriple(node.Scale, [3]float64{1, 1, 1})
	return trsMatrix(translation, rotation, scale)
}

type nodeInstance struct {
	index int
	chain []int
	path  string
	world mat4
}

// walkActiveScene returns every node instance reachable from the active scene,
// depth first, with the source node chain and its accumulated world matrix.
func walkActiveScene(doc *glbDocument) ([]nodeInstance, error) {
	sceneIndex := 0
	if doc.Scene != nil {
		sceneIndex = *doc.Scene
	}
	if sceneIndex < 0 || sceneIndex >= len(doc.Scenes) {
		return nil, invalidModel("scene")
	}
	visited := make([]bool, len(doc.Nodes))
	var instances []nodeInstance
	type item struct {
		node  int
		chain []int
		world mat4
		path  string
	}
	stack := make([]item, 0, len(doc.Nodes))
	for i := len(doc.Scenes[sceneIndex].Nodes) - 1; i >= 0; i-- {
		stack = append(stack, item{node: doc.Scenes[sceneIndex].Nodes[i], world: identity4()})
	}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current.node < 0 || current.node >= len(doc.Nodes) {
			return nil, invalidModel("node")
		}
		if visited[current.node] {
			return nil, invalidModel("node_graph")
		}
		visited[current.node] = true
		node := doc.Nodes[current.node]
		world := current.world.mul(localMatrix(node))
		name := node.Name
		if name == "" {
			name = "node_" + strconv.Itoa(current.node)
		}
		path := name
		if current.path != "" {
			path = current.path + "/" + name
		}
		chain := append(append([]int(nil), current.chain...), current.node)
		instances = append(instances, nodeInstance{index: current.node, chain: chain, path: path, world: world})
		for i := len(node.Children) - 1; i >= 0; i-- {
			stack = append(stack, item{node: node.Children[i], chain: chain, world: world, path: path})
		}
	}
	return instances, nil
}

// ---------------------------------------------------------------------------
// Accessor reads
// ---------------------------------------------------------------------------

func componentWidth(componentType int) int {
	switch componentType {
	case 5120, 5121:
		return 1
	case 5122, 5123:
		return 2
	case 5125, 5126:
		return 4
	default:
		return 0
	}
}

func componentCount(accessorType string) int {
	switch accessorType {
	case "SCALAR":
		return 1
	case "VEC2":
		return 2
	case "VEC3":
		return 3
	case "VEC4":
		return 4
	default:
		return 0
	}
}

func (m *sourceModel) elementOffset(accessor accessorDef, element int) (int, int, error) {
	if accessor.BufferView < 0 || accessor.BufferView >= len(m.doc.BufferViews) || element < 0 || element >= accessor.Count {
		return 0, 0, invalidModel("accessor")
	}
	view := m.doc.BufferViews[accessor.BufferView]
	width := componentWidth(accessor.ComponentType)
	components := componentCount(accessor.Type)
	if width == 0 || components == 0 {
		return 0, 0, invalidModel("accessor")
	}
	size := width * components
	stride := size
	if view.ByteStride != nil {
		stride = *view.ByteStride
	}
	offset := view.ByteOffset + accessor.ByteOffset + element*stride
	if offset < 0 || offset+size > len(m.bin) {
		return 0, 0, invalidModel("accessor_bounds")
	}
	return offset, size, nil
}

func (m *sourceModel) position(accessorIndex, vertex int) (vec3, error) {
	if accessorIndex < 0 || accessorIndex >= len(m.doc.Accessors) {
		return vec3{}, invalidModel("position")
	}
	accessor := m.doc.Accessors[accessorIndex]
	offset, size, err := m.elementOffset(accessor, vertex)
	if err != nil {
		return vec3{}, err
	}
	if accessor.ComponentType != 5126 || accessor.Type != "VEC3" || size != 12 {
		return vec3{}, invalidModel("position")
	}
	return vec3{
		float64(math.Float32frombits(binary.LittleEndian.Uint32(m.bin[offset:]))),
		float64(math.Float32frombits(binary.LittleEndian.Uint32(m.bin[offset+4:]))),
		float64(math.Float32frombits(binary.LittleEndian.Uint32(m.bin[offset+8:]))),
	}, nil
}

func (m *sourceModel) triangleIndices(primitive primitiveDef) ([]uint32, error) {
	positionAccessor, ok := primitive.Attributes["POSITION"]
	if !ok || positionAccessor < 0 || positionAccessor >= len(m.doc.Accessors) {
		return nil, invalidModel("position")
	}
	count := m.doc.Accessors[positionAccessor].Count
	if count <= 0 || count > int(m.budget.remaining) {
		if count > 0 {
			return nil, limitError("vertices")
		}
		return nil, invalidModel("position")
	}
	if err := m.budget.charge(int64(count)); err != nil {
		return nil, err
	}
	if primitive.Indices == nil {
		if count%3 != 0 {
			return nil, invalidModel("triangles")
		}
		indices := make([]uint32, count)
		for i := 0; i < count; i++ {
			indices[i] = uint32(i)
		}
		return indices, nil
	}
	accessorIndex := *primitive.Indices
	if accessorIndex < 0 || accessorIndex >= len(m.doc.Accessors) {
		return nil, invalidModel("indices")
	}
	accessor := m.doc.Accessors[accessorIndex]
	if accessor.Count%3 != 0 || accessor.Type != "SCALAR" {
		return nil, invalidModel("triangles")
	}
	if err := m.budget.charge(int64(accessor.Count)); err != nil {
		return nil, err
	}
	indices := make([]uint32, accessor.Count)
	for i := range indices {
		offset, size, err := m.elementOffset(accessor, i)
		if err != nil {
			return nil, err
		}
		var value uint32
		switch accessor.ComponentType {
		case 5121:
			value = uint32(m.bin[offset])
		case 5123:
			value = uint32(binary.LittleEndian.Uint16(m.bin[offset:]))
		case 5125:
			value = binary.LittleEndian.Uint32(m.bin[offset:])
		default:
			return nil, invalidModel("indices")
		}
		if size == 0 || int(value) >= count {
			return nil, invalidModel("indices")
		}
		indices[i] = value
	}
	return indices, nil
}

// ---------------------------------------------------------------------------
// Bounds
// ---------------------------------------------------------------------------

type bounds struct {
	min, max vec3
	valid    bool
}

func (b *bounds) add(p vec3) {
	if !p.finite() {
		return
	}
	if !b.valid {
		b.min, b.max, b.valid = p, p, true
		return
	}
	for i := 0; i < 3; i++ {
		if p[i] < b.min[i] {
			b.min[i] = p[i]
		}
		if p[i] > b.max[i] {
			b.max[i] = p[i]
		}
	}
}

func (b *bounds) union(o bounds) {
	if !o.valid {
		return
	}
	b.add(o.min)
	b.add(o.max)
}

func (b bounds) center() vec3 {
	if !b.valid {
		return vec3{}
	}
	return b.min.add(b.max).scale(0.5)
}

// ---------------------------------------------------------------------------
// Naming
// ---------------------------------------------------------------------------
// nodeSourceName names the leaf node instance of a part. A split of a named
// object stays recognizable.
func nodeSourceName(instance nodeInstance, doc *glbDocument) string {
	name := doc.Nodes[instance.index].Name
	if name == "" {
		name = "node_" + strconv.Itoa(instance.index)
	}
	return name
}
