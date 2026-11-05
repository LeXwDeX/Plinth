package modelinspect

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

// testGLB builds GLB v2 containers for tests. It deliberately writes JSON as
// generic maps so malformed documents can be produced without a second set of
// wire structs.
type testGLB struct {
	doc       map[string]any
	bin       bytes.Buffer
	bufferURI string
	// extraBuffers follow buffer 0 in the document, for example an
	// EXT_meshopt_compression fallback buffer.
	extraBuffers []any
}

func newTestGLB() *testGLB {
	return &testGLB{doc: map[string]any{
		"asset": map[string]any{"version": "2.0"},
	}}
}

func (b *testGLB) appendDoc(key string, value any) int {
	var list []any
	if existing, ok := b.doc[key]; ok {
		list = existing.([]any)
	}
	index := len(list)
	b.doc[key] = append(list, value)
	return index
}

func (b *testGLB) align4() {
	for b.bin.Len()%4 != 0 {
		b.bin.WriteByte(0)
	}
}

func (b *testGLB) addBufferView(data []byte, stride int) int {
	b.align4()
	offset := b.bin.Len()
	b.bin.Write(data)
	view := map[string]any{
		"buffer":     0,
		"byteOffset": offset,
		"byteLength": len(data),
	}
	if stride > 0 {
		view["byteStride"] = stride
	}
	return b.appendDoc("bufferViews", view)
}

func (b *testGLB) addAccessor(view, byteOffset, componentType, count int, typeName string, normalized bool, minValues, maxValues []float64) int {
	accessor := map[string]any{
		"bufferView":    view,
		"byteOffset":    byteOffset,
		"componentType": componentType,
		"count":         count,
		"type":          typeName,
	}
	if normalized {
		accessor["normalized"] = true
	}
	if minValues != nil {
		accessor["min"] = minValues
	}
	if maxValues != nil {
		accessor["max"] = maxValues
	}
	return b.appendDoc("accessors", accessor)
}

func floatBytes(values []float32) []byte {
	data := make([]byte, len(values)*4)
	for i, value := range values {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(value))
	}
	return data
}

func uint16Bytes(values []uint16) []byte {
	data := make([]byte, len(values)*2)
	for i, value := range values {
		binary.LittleEndian.PutUint16(data[i*2:], value)
	}
	return data
}

func uint32Bytes(values []uint32) []byte {
	data := make([]byte, len(values)*4)
	for i, value := range values {
		binary.LittleEndian.PutUint32(data[i*4:], value)
	}
	return data
}

func (b *testGLB) addVec3Accessor(values []float32, withBounds bool) int {
	view := b.addBufferView(floatBytes(values), 0)
	var minValues, maxValues []float64
	if withBounds {
		minValues = []float64{math.Inf(1), math.Inf(1), math.Inf(1)}
		maxValues = []float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
		for i := 0; i+2 < len(values); i += 3 {
			for component := 0; component < 3; component++ {
				value := float64(values[i+component])
				minValues[component] = math.Min(minValues[component], value)
				maxValues[component] = math.Max(maxValues[component], value)
			}
		}
	}
	return b.addAccessor(view, 0, 5126, len(values)/3, "VEC3", false, minValues, maxValues)
}

func (b *testGLB) addVec2Accessor(values []float32) int {
	view := b.addBufferView(floatBytes(values), 0)
	return b.addAccessor(view, 0, 5126, len(values)/2, "VEC2", false, nil, nil)
}

func (b *testGLB) addScalarFloatAccessor(values []float32, withBounds bool) int {
	view := b.addBufferView(floatBytes(values), 0)
	var minValues, maxValues []float64
	if withBounds && len(values) > 0 {
		minValue, maxValue := float64(values[0]), float64(values[0])
		for _, value := range values[1:] {
			minValue = math.Min(minValue, float64(value))
			maxValue = math.Max(maxValue, float64(value))
		}
		minValues = []float64{minValue}
		maxValues = []float64{maxValue}
	}
	return b.addAccessor(view, 0, 5126, len(values), "SCALAR", false, minValues, maxValues)
}

func (b *testGLB) addIndices16(values []uint16) int {
	view := b.addBufferView(uint16Bytes(values), 0)
	return b.addAccessor(view, 0, 5123, len(values), "SCALAR", false, nil, nil)
}

func (b *testGLB) addIndices32(values []uint32) int {
	view := b.addBufferView(uint32Bytes(values), 0)
	return b.addAccessor(view, 0, 5125, len(values), "SCALAR", false, nil, nil)
}

func (b *testGLB) addMat4Accessor(count int) int {
	view := b.addBufferView(make([]byte, count*64), 0)
	return b.addAccessor(view, 0, 5126, count, "MAT4", false, nil, nil)
}

func (b *testGLB) addMesh(primitives []any) int {
	return b.appendDoc("meshes", map[string]any{"primitives": primitives})
}

func (b *testGLB) addNode(fields map[string]any) int {
	return b.appendDoc("nodes", fields)
}

func (b *testGLB) addScene(nodes []int) int {
	return b.appendDoc("scenes", map[string]any{"nodes": nodes})
}

func (b *testGLB) addMaterial(fields map[string]any) int {
	return b.appendDoc("materials", fields)
}

func (b *testGLB) addMaterialFactor(factor [4]float64) int {
	return b.addMaterial(map[string]any{
		"pbrMetallicRoughness": map[string]any{
			"baseColorFactor": []float64{factor[0], factor[1], factor[2], factor[3]},
		},
	})
}

func (b *testGLB) addTexture(fields map[string]any) int {
	return b.appendDoc("textures", fields)
}

func (b *testGLB) addImage(fields map[string]any) int {
	return b.appendDoc("images", fields)
}

func (b *testGLB) addSampler(fields map[string]any) int {
	return b.appendDoc("samplers", fields)
}

func (b *testGLB) mutateAccessor(index int, mutate func(map[string]any)) {
	accessors := b.doc["accessors"].([]any)
	mutate(accessors[index].(map[string]any))
}

// finish serializes the document, pads both chunks to four bytes and writes
// the GLB header. The BIN chunk is omitted when no binary data was added.
func (b *testGLB) finish() []byte {
	switch {
	case b.bufferURI != "":
		b.doc["buffers"] = []any{map[string]any{
			"uri": b.bufferURI, "byteLength": b.bin.Len(),
		}}
	case b.bin.Len() > 0:
		b.doc["buffers"] = []any{map[string]any{"byteLength": b.bin.Len()}}
	}
	if len(b.extraBuffers) > 0 {
		b.doc["buffers"] = append(b.doc["buffers"].([]any), b.extraBuffers...)
	}
	jsonBytes, err := json.Marshal(b.doc)
	if err != nil {
		panic(err)
	}
	for len(jsonBytes)%4 != 0 {
		jsonBytes = append(jsonBytes, ' ')
	}
	binLength := b.bin.Len()
	binPaddedLength := (binLength + 3) &^ 3
	total := 12 + 8 + len(jsonBytes)
	if binLength > 0 {
		total += 8 + binPaddedLength
	}
	out := make([]byte, 0, total)
	var header [12]byte
	binary.LittleEndian.PutUint32(header[0:], glbMagic)
	binary.LittleEndian.PutUint32(header[4:], glbVersion)
	binary.LittleEndian.PutUint32(header[8:], uint32(total))
	out = append(out, header[:]...)

	var chunkHeader [8]byte
	binary.LittleEndian.PutUint32(chunkHeader[0:], uint32(len(jsonBytes)))
	binary.LittleEndian.PutUint32(chunkHeader[4:], chunkJSON)
	out = append(out, chunkHeader[:]...)
	out = append(out, jsonBytes...)
	if binLength > 0 {
		binary.LittleEndian.PutUint32(chunkHeader[0:], uint32(binPaddedLength))
		binary.LittleEndian.PutUint32(chunkHeader[4:], chunkBIN)
		out = append(out, chunkHeader[:]...)
		out = append(out, b.bin.Bytes()...)
		for i := binLength; i < binPaddedLength; i++ {
			out = append(out, 0)
		}
	}
	return out
}

func cubePositions() []float32 {
	return []float32{
		-0.5, -0.5, -0.5, // 0
		0.5, -0.5, -0.5, // 1
		0.5, 0.5, -0.5, // 2
		-0.5, 0.5, -0.5, // 3
		-0.5, -0.5, 0.5, // 4
		0.5, -0.5, 0.5, // 5
		0.5, 0.5, 0.5, // 6
		-0.5, 0.5, 0.5, // 7
	}
}

// cubeFaces lists two counter-clockwise triangles per face in this order:
// +Z, -Z, +X, -X, +Y, -Y.
func cubeFaces() [6][]uint16 {
	return [6][]uint16{
		{4, 5, 6, 4, 6, 7},
		{1, 0, 3, 1, 3, 2},
		{5, 1, 2, 5, 2, 6},
		{0, 4, 7, 0, 7, 3},
		{7, 6, 2, 7, 2, 3},
		{0, 1, 5, 0, 5, 4},
	}
}

func flattenFaces(faces [6][]uint16) []uint16 {
	var indices []uint16
	for _, face := range faces {
		indices = append(indices, face...)
	}
	return indices
}

func cubeDocument() *testGLB {
	b := newTestGLB()
	position := b.addVec3Accessor(cubePositions(), true)
	indices := b.addIndices16(flattenFaces(cubeFaces()))
	mesh := b.addMesh([]any{map[string]any{
		"attributes": map[string]int{"POSITION": position},
		"indices":    indices,
	}})
	node := b.addNode(map[string]any{"name": "cube", "mesh": mesh})
	b.addScene([]int{node})
	return b
}

func cubeGLB() []byte { return cubeDocument().finish() }

// twoToneBoxDocument assigns red to +Z, green to -Z and grey to the remaining
// four faces. A front view must see red and a back view green.
func twoToneBoxDocument() *testGLB {
	b := newTestGLB()
	position := b.addVec3Accessor(cubePositions(), true)
	red := b.addMaterialFactor([4]float64{1, 0, 0, 1})
	green := b.addMaterialFactor([4]float64{0, 1, 0, 1})
	grey := b.addMaterialFactor([4]float64{0.5, 0.5, 0.5, 1})
	faces := cubeFaces()
	primitive := func(indices []uint16, material int) any {
		return map[string]any{
			"attributes": map[string]int{"POSITION": position},
			"indices":    b.addIndices16(indices),
			"material":   material,
		}
	}
	var rest []uint16
	for _, face := range faces[2:] {
		rest = append(rest, face...)
	}
	mesh := b.addMesh([]any{
		primitive(faces[0], red),
		primitive(faces[1], green),
		primitive(rest, grey),
	})
	node := b.addNode(map[string]any{"name": "box", "mesh": mesh})
	b.addScene([]int{node})
	return b
}

func encodePNG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buffer.Bytes()
}

// texturedQuadDocument builds a quad in the XY plane with UVs whose v=0 edge
// is the quad's top (+Y in world space). pixels use image row order.
func texturedQuadDocument(t testing.TB, pixels [4]color.NRGBA, factor [4]float64) *testGLB {
	t.Helper()
	b := newTestGLB()
	positions := []float32{
		-1, -1, 0,
		1, -1, 0,
		1, 1, 0,
		-1, 1, 0,
	}
	uvs := []float32{
		0, 1,
		1, 1,
		1, 0,
		0, 0,
	}
	position := b.addVec3Accessor(positions, true)
	uv := b.addVec2Accessor(uvs)
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for i, pixel := range pixels {
		img.SetNRGBA(i%2, i/2, pixel)
	}
	imageView := b.addBufferView(encodePNG(t, img), 0)
	imageIndex := b.addImage(map[string]any{
		"name": "checker", "bufferView": imageView, "mimeType": "image/png",
	})
	sampler := b.addSampler(map[string]any{"wrapS": 10497, "wrapT": 10497})
	texture := b.addTexture(map[string]any{"source": imageIndex, "sampler": sampler})
	material := b.addMaterial(map[string]any{
		"pbrMetallicRoughness": map[string]any{
			"baseColorFactor":  []float64{factor[0], factor[1], factor[2], factor[3]},
			"baseColorTexture": map[string]any{"index": texture},
		},
	})
	indices := b.addIndices16([]uint16{0, 1, 2, 0, 2, 3})
	mesh := b.addMesh([]any{map[string]any{
		"attributes": map[string]int{"POSITION": position, "TEXCOORD_0": uv},
		"indices":    indices,
		"material":   material,
	}})
	node := b.addNode(map[string]any{"name": "quad", "mesh": mesh})
	b.addScene([]int{node})
	return b
}

func texturedQuadGLB(t testing.TB) []byte {
	pixel := color.NRGBA{R: 200, G: 100, B: 50, A: 255}
	return texturedQuadDocument(t, [4]color.NRGBA{pixel, pixel, pixel, pixel},
		[4]float64{1, 1, 1, 1}).finish()
}

func twoCubesAndSliverDocument() *testGLB {
	b := newTestGLB()
	position := b.addVec3Accessor(cubePositions(), true)
	cubeIndices := b.addIndices16(flattenFaces(cubeFaces()))
	cubeMesh := b.addMesh([]any{map[string]any{
		"attributes": map[string]int{"POSITION": position},
		"indices":    cubeIndices,
	}})
	sliverPosition := b.addVec3Accessor([]float32{
		0, 0, 0,
		0.01, 0, 0,
		0, 0.01, 0,
	}, true)
	sliverIndices := b.addIndices16([]uint16{0, 1, 2})
	sliverMesh := b.addMesh([]any{map[string]any{
		"attributes": map[string]int{"POSITION": sliverPosition},
		"indices":    sliverIndices,
	}})
	first := b.addNode(map[string]any{"name": "cube_a", "mesh": cubeMesh})
	second := b.addNode(map[string]any{
		"name": "cube_b", "mesh": cubeMesh, "translation": []float64{3, 0, 0},
	})
	sliver := b.addNode(map[string]any{
		"name": "sliver", "mesh": sliverMesh, "translation": []float64{6, 0, 0},
	})
	b.addScene([]int{first, second, sliver})
	return b
}

func openPlaneDocument() *testGLB {
	b := newTestGLB()
	position := b.addVec3Accessor([]float32{
		-1, 0, -1,
		1, 0, -1,
		1, 0, 1,
		-1, 0, 1,
	}, true)
	indices := b.addIndices16([]uint16{0, 1, 2, 0, 2, 3})
	mesh := b.addMesh([]any{map[string]any{
		"attributes": map[string]int{"POSITION": position},
		"indices":    indices,
	}})
	node := b.addNode(map[string]any{"mesh": mesh})
	b.addScene([]int{node})
	return b
}

func trsHierarchyDocument() *testGLB {
	b := newTestGLB()
	position := b.addVec3Accessor(cubePositions(), true)
	indices := b.addIndices16(flattenFaces(cubeFaces()))
	mesh := b.addMesh([]any{map[string]any{
		"attributes": map[string]int{"POSITION": position},
		"indices":    indices,
	}})
	child := b.addNode(map[string]any{
		"name": "child", "mesh": mesh, "translation": []float64{0, 1, 0},
	})
	parent := b.addNode(map[string]any{
		"name": "parent", "children": []int{child},
		"translation": []float64{10, 0, 0}, "scale": []float64{2, 2, 2},
	})
	b.addScene([]int{parent})
	return b
}

func skinAnimationDocument() *testGLB {
	b := newTestGLB()
	position := b.addVec3Accessor(cubePositions(), true)
	indices := b.addIndices16(flattenFaces(cubeFaces()))
	jointsView := b.addBufferView(make([]byte, 8*4), 0)
	joints := b.addAccessor(jointsView, 0, 5121, 8, "VEC4", false, nil, nil)
	weightsView := b.addBufferView(make([]byte, 8*16), 0)
	weights := b.addAccessor(weightsView, 0, 5126, 8, "VEC4", false, nil, nil)
	mesh := b.addMesh([]any{map[string]any{
		"attributes": map[string]int{
			"POSITION": position, "JOINTS_0": joints, "WEIGHTS_0": weights,
		},
		"indices": indices,
	}})
	skinned := b.addNode(map[string]any{"name": "skinned", "mesh": mesh, "skin": 0})
	jointA := b.addNode(map[string]any{"name": "joint_a"})
	jointB := b.addNode(map[string]any{"name": "joint_b"})
	inverseBind := b.addMat4Accessor(2)
	input := b.addScalarFloatAccessor([]float32{0, 2.5}, true)
	output := b.addVec3Accessor([]float32{
		0, 0, 0,
		1, 0, 0,
	}, false)
	b.appendDoc("skins", map[string]any{
		"name": "rig", "joints": []int{jointA, jointB},
		"inverseBindMatrices": inverseBind,
	})
	b.appendDoc("animations", map[string]any{
		"name": "wave",
		"samplers": []any{map[string]any{
			"input": input, "output": output, "interpolation": "LINEAR",
		}},
		"channels": []any{map[string]any{
			"sampler": 0,
			"target":  map[string]any{"node": jointA, "path": "translation"},
		}},
	})
	b.addScene([]int{skinned})
	return b
}

func stripDocument() *testGLB {
	b := newTestGLB()
	position := b.addVec3Accessor([]float32{
		-1, 0, -1,
		1, 0, -1,
		-1, 0, 1,
		1, 0, 1,
	}, true)
	mesh := b.addMesh([]any{map[string]any{
		"attributes": map[string]int{"POSITION": position},
		"mode":       5,
	}})
	node := b.addNode(map[string]any{"mesh": mesh})
	b.addScene([]int{node})
	return b
}

// mustParseModel parses data or fails the test. It also asserts that Parse
// never returns a model alongside an error.
func mustParseModel(t testing.TB, data []byte, limits Limits) *Model {
	t.Helper()
	model, err := Parse(bytes.NewReader(data), int64(len(data)), limits)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if model == nil {
		t.Fatal("Parse returned a nil model without an error")
	}
	return model
}
