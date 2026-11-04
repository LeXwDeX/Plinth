package localparts

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"
)

// fixtureGLB builds a real static GLB through the same document type the
// adapter parses, so a test source passes modelinput and the rewrite subset
// without hand-encoding JSON.
type fixtureGLB struct {
	doc glbDocument
	bin []byte
}

func newFixtureGLB() *fixtureGLB {
	return &fixtureGLB{doc: glbDocument{Asset: assetDef{Version: "2.0"}}}
}

func (f *fixtureGLB) addView(data []byte, stride, target *int) int {
	for len(f.bin)%4 != 0 {
		f.bin = append(f.bin, 0)
	}
	offset := len(f.bin)
	f.bin = append(f.bin, data...)
	f.doc.BufferViews = append(f.doc.BufferViews, bufferViewDef{
		Buffer: 0, ByteOffset: offset, ByteLength: len(data), ByteStride: stride, Target: target,
	})
	return len(f.doc.BufferViews) - 1
}

func float32Bytes(values []float32) []byte {
	raw := make([]byte, len(values)*4)
	for index, value := range values {
		binary.LittleEndian.PutUint32(raw[index*4:], math.Float32bits(value))
	}
	return raw
}

func vecType(components int) string {
	switch components {
	case 1:
		return "SCALAR"
	case 2:
		return "VEC2"
	case 3:
		return "VEC3"
	case 4:
		return "VEC4"
	default:
		panic("unsupported fixture accessor width")
	}
}

// addVertexAttribute writes a tightly packed float attribute. Positions carry
// the exact min/max modelinput requires; other attributes do not declare them.
func (f *fixtureGLB) addVertexAttribute(values []float32, components int, positions bool) int {
	target := 34962
	raw := float32Bytes(values)
	view := f.addView(raw, nil, &target)
	accessor := accessorDef{BufferView: view, ComponentType: 5126, Count: len(values) / components, Type: vecType(components)}
	if positions {
		for component := 0; component < components; component++ {
			minimum := float64(values[component])
			maximum := minimum
			for vertex := 1; vertex < len(values)/components; vertex++ {
				value := float64(values[vertex*components+component])
				minimum = math.Min(minimum, value)
				maximum = math.Max(maximum, value)
			}
			accessor.Min = append(accessor.Min, minimum)
			accessor.Max = append(accessor.Max, maximum)
		}
	}
	f.doc.Accessors = append(f.doc.Accessors, accessor)
	return len(f.doc.Accessors) - 1
}

// addIndices writes a triangle index buffer, choosing the narrowest component
// type modelinput accepts.
func (f *fixtureGLB) addIndices(values []uint32) int {
	maximum := uint32(0)
	for _, value := range values {
		if value > maximum {
			maximum = value
		}
	}
	var raw []byte
	componentType := 5123
	if maximum < 65536 {
		raw = make([]byte, len(values)*2)
		for index, value := range values {
			binary.LittleEndian.PutUint16(raw[index*2:], uint16(value))
		}
	} else {
		componentType = 5125
		raw = make([]byte, len(values)*4)
		for index, value := range values {
			binary.LittleEndian.PutUint32(raw[index*4:], value)
		}
	}
	target := 34963
	view := f.addView(raw, nil, &target)
	f.doc.Accessors = append(f.doc.Accessors, accessorDef{
		BufferView: view, ComponentType: componentType, Count: len(values), Type: "SCALAR",
	})
	return len(f.doc.Accessors) - 1
}

func (f *fixtureGLB) addImagePNG(t testing.TB, rgba color.NRGBA) int {
	t.Helper()
	var encoded bytes.Buffer
	picture := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			picture.SetNRGBA(x, y, rgba)
		}
	}
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatalf("encode fixture PNG: %v", err)
	}
	view := f.addView(encoded.Bytes(), nil, nil)
	f.doc.Images = append(f.doc.Images, imageDef{BufferView: view, MimeType: "image/png"})
	return len(f.doc.Images) - 1
}

func (f *fixtureGLB) addTexture(image int) int {
	f.doc.Textures = append(f.doc.Textures, textureDef{Source: image})
	return len(f.doc.Textures) - 1
}

func (f *fixtureGLB) addMaterial(name string, texture int) int {
	material := materialDef{Name: name}
	if texture >= 0 {
		material.PBR = &pbrDef{BaseColorTexture: &textureRef{Index: texture}}
	}
	f.doc.Materials = append(f.doc.Materials, material)
	return len(f.doc.Materials) - 1
}

func (f *fixtureGLB) addMesh(name string, primitives ...primitiveDef) int {
	f.doc.Meshes = append(f.doc.Meshes, meshDef{Name: name, Primitives: primitives})
	return len(f.doc.Meshes) - 1
}

func intAddress(value int) *int { return &value }

// bytes frames the document and its BIN payload exactly as GLB v2 requires;
// the JSON chunk and BIN chunk are both padded to four bytes.
func (f *fixtureGLB) bytes(t testing.TB) []byte {
	t.Helper()
	f.doc.Buffers = []bufferDef{{ByteLength: len(f.bin) + (4-len(f.bin)%4)%4}}
	if f.doc.Scene == nil {
		f.doc.Scene = intAddress(0)
	}
	raw, err := json.Marshal(f.doc)
	if err != nil {
		t.Fatalf("encode fixture document: %v", err)
	}
	return frameFixtureGLB(raw, f.bin)
}

func frameFixtureGLB(raw, bin []byte) []byte {
	raw = append([]byte(nil), raw...)
	bin = append([]byte(nil), bin...)
	for len(raw)%4 != 0 {
		raw = append(raw, ' ')
	}
	for len(bin)%4 != 0 {
		bin = append(bin, 0)
	}
	total := 12 + 8 + len(raw) + 8 + len(bin)
	result := make([]byte, 0, total)
	header := make([]byte, 12)
	binary.LittleEndian.PutUint32(header[0:4], 0x46546C67) // glTF
	binary.LittleEndian.PutUint32(header[4:8], 2)
	binary.LittleEndian.PutUint32(header[8:12], uint32(total))
	result = append(result, header...)
	chunk := make([]byte, 8)
	binary.LittleEndian.PutUint32(chunk[0:4], uint32(len(raw)))
	binary.LittleEndian.PutUint32(chunk[4:8], 0x4E4F534A) // JSON
	result = append(result, chunk...)
	result = append(result, raw...)
	binary.LittleEndian.PutUint32(chunk[0:4], uint32(len(bin)))
	binary.LittleEndian.PutUint32(chunk[4:8], 0x004E4942) // BIN\0
	result = append(result, chunk...)
	result = append(result, bin...)
	return result
}

// withTopLevelJSON injects a top-level member into a framed GLB, for source
// shapes modelinput/the adapter refuse before parsing (skins, animations).
func withTopLevelJSON(t testing.TB, glb []byte, key string, value string) []byte {
	t.Helper()
	length := int(binary.LittleEndian.Uint32(glb[12:16]))
	var document map[string]json.RawMessage
	if err := json.Unmarshal(glb[20:20+length], &document); err != nil {
		t.Fatalf("decode fixture GLB JSON: %v", err)
	}
	document[key] = json.RawMessage(value)
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode injected GLB JSON: %v", err)
	}
	binStart := 20 + length
	var bin []byte
	if binStart+8 <= len(glb) {
		binLength := int(binary.LittleEndian.Uint32(glb[binStart : binStart+4]))
		bin = append([]byte(nil), glb[binStart+8:binStart+8+binLength]...)
	}
	return frameFixtureGLB(raw, bin)
}

// boxGeometry builds a closed axis-aligned cube with four independent vertices
// per face: the shared corners have identical POSITION bytes, so the adapter's
// exact welding must join the six faces into one connected component.
func boxGeometry(center [3]float32, size float32) ([]float32, []uint32) {
	half := size / 2
	corners := [8][3]float32{
		{center[0] - half, center[1] - half, center[2] - half},
		{center[0] + half, center[1] - half, center[2] - half},
		{center[0] + half, center[1] + half, center[2] - half},
		{center[0] - half, center[1] + half, center[2] - half},
		{center[0] - half, center[1] - half, center[2] + half},
		{center[0] + half, center[1] - half, center[2] + half},
		{center[0] + half, center[1] + half, center[2] + half},
		{center[0] - half, center[1] + half, center[2] + half},
	}
	faces := [6][4]int{
		{0, 3, 2, 1}, // -Z
		{4, 5, 6, 7}, // +Z
		{0, 1, 5, 4}, // -Y
		{3, 7, 6, 2}, // +Y
		{0, 4, 7, 3}, // -X
		{1, 2, 6, 5}, // +X
	}
	positions := make([]float32, 0, 6*4*3)
	indices := make([]uint32, 0, 6*6)
	for _, face := range faces {
		base := uint32(len(positions) / 3)
		for _, corner := range face {
			positions = append(positions, corners[corner][0], corners[corner][1], corners[corner][2])
		}
		indices = append(indices, base, base+1, base+2, base, base+2, base+3)
	}
	return positions, indices
}

// quadGeometry builds one two-triangle rectangle in the XY plane plus its UV
// and normal streams.
func quadGeometry(minX, maxX, minY, maxY float32, z float32) (positions, uvs, normals []float32) {
	positions = []float32{
		minX, minY, z, maxX, minY, z, maxX, maxY, z, minX, maxY, z,
	}
	uvs = []float32{0, 0, 1, 0, 1, 1, 0, 1}
	normals = []float32{0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1}
	return positions, uvs, normals
}

func quadIndices(base uint32) []uint32 {
	return []uint32{base, base + 1, base + 2, base, base + 2, base + 3}
}

func (f *fixtureGLB) addQuadPrimitive(minX, maxX, minY, maxY, z float32, material int) primitiveDef {
	positions, uvs, normals := quadGeometry(minX, maxX, minY, maxY, z)
	position := f.addVertexAttribute(positions, 3, true)
	uv := f.addVertexAttribute(uvs, 2, false)
	normal := f.addVertexAttribute(normals, 3, false)
	indices := f.addIndices(quadIndices(0))
	return primitiveDef{
		Attributes: map[string]int{"POSITION": position, "TEXCOORD_0": uv, "NORMAL": normal},
		Indices:    intAddress(indices), Material: intAddress(material),
	}
}

// twoCubesAndFragment is a single node with three primitives: two closed cubes
// of different sizes, different materials, and an unconnected one-triangle
// fragment near the second cube. 25 triangles in total.
func twoCubesAndFragment(t testing.TB) []byte {
	t.Helper()
	f := newFixtureGLB()
	materialA := f.addMaterial("crimson", -1)
	materialB := f.addMaterial("azure", -1)

	positionsA, indicesA := boxGeometry([3]float32{-3, 0, 0}, 2)
	cubeA := primitiveDef{
		Attributes: map[string]int{"POSITION": f.addVertexAttribute(positionsA, 3, true)},
		Indices:    intAddress(f.addIndices(indicesA)), Material: intAddress(materialA),
	}
	positionsB, indicesB := boxGeometry([3]float32{3, 0, 0}, 1)
	cubeB := primitiveDef{
		Attributes: map[string]int{"POSITION": f.addVertexAttribute(positionsB, 3, true)},
		Indices:    intAddress(f.addIndices(indicesB)), Material: intAddress(materialB),
	}
	fragmentPositions := []float32{3.2, 2.4, 0, 3.8, 2.4, 0, 3.5, 2.8, 0}
	fragment := primitiveDef{
		Attributes: map[string]int{"POSITION": f.addVertexAttribute(fragmentPositions, 3, true)},
		Indices:    intAddress(f.addIndices([]uint32{0, 1, 2})), Material: intAddress(materialB),
	}
	mesh := f.addMesh("toy", cubeA, cubeB, fragment)
	f.doc.Nodes = []nodeDef{{Name: "toy", Mesh: intAddress(mesh)}}
	f.doc.Scenes = []sceneDef{{Name: "scene", Nodes: []int{0}}}
	return f.bytes(t)
}

// texturedHierarchy is a three-node chain with rotations, a scale and a leaf
// translation. The leaf's mesh has two textured, multi-material primitives, so
// a split must copy each material's texture into the output that uses it.
func texturedHierarchy(t testing.TB) []byte {
	t.Helper()
	f := newFixtureGLB()
	red := f.addTexture(f.addImagePNG(t, color.NRGBA{R: 255, A: 255}))
	blue := f.addTexture(f.addImagePNG(t, color.NRGBA{B: 255, A: 255}))
	materialA := f.addMaterial("red", red)
	materialB := f.addMaterial("blue", blue)
	first := f.addQuadPrimitive(0, 1, 0, 1, 0, materialA)
	second := f.addQuadPrimitive(2, 3, 0, 1, 0, materialB)
	mesh := f.addMesh("model", first, second)
	root := nodeDef{Name: "stage", Translation: []float64{10, 0, 0}}
	child := nodeDef{
		Name:     "turntable",
		Rotation: []float64{0, 0, math.Sin(math.Pi / 4), math.Cos(math.Pi / 4)},
		Scale:    []float64{2, 2, 2},
	}
	leaf := nodeDef{Name: "model", Translation: []float64{1, 0, 0}, Mesh: intAddress(mesh)}
	f.doc.Nodes = []nodeDef{root, child, leaf}
	f.doc.Nodes[0].Children = []int{1}
	f.doc.Nodes[1].Children = []int{2}
	f.doc.Scenes = []sceneDef{{Name: "scene", Nodes: []int{0}}}
	return f.bytes(t)
}

// skinnedModel is a structurally valid static GLB that also declares a skin;
// the adapter must refuse it before any rewrite.
func skinnedModel(t testing.TB) []byte {
	t.Helper()
	return withTopLevelJSON(t, twoCubesAndFragment(t), "skins", `[{"joints":[0]}]`)
}
