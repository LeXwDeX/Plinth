// Package modelfiles writes the deterministic model files (GLB, glTF, FBX, OBJ, STL, USDZ, 3MF) of the
// Mock provider from one small humanoid mesh. The Mock adapter and test tooling (the fake upstream under
// internal/testutil) share it, so test-only entry points live under internal/testutil and not in the Mock adapter.
package modelfiles

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// 本文件把一次 3D 请求画成一个由方块拼成的小人，并按 Offering 声明的
// 每种格式各写一份字节。所有字节都通过 internal/assets/local 的交付检查
// （GLB 结构、FBX 帧、STL、ZIP/3MF/USDZ、PNG 与外部地址扫描），所以任务
// 可以真正成功，3D 舞台、统计和格式页看到的是真实可解析的模型。

//go:embed testdata/mock_model.usdc
var mockUSDC []byte

// Mesh 是一份展开后的三角网格（每个面独立顶点，没有共享）。
type Mesh struct {
	Position []float32 // 3 per vertex
	Normal   []float32 // 3 per vertex
	UV       []float32 // 2 per vertex
	Index    []uint16
}

func (m Mesh) VertexCount() int   { return len(m.Position) / 3 }
func (m Mesh) TriangleCount() int { return len(m.Index) / 3 }

type Part struct {
	CX, CY, CZ float64
	SX, SY, SZ float64
	Cell       int
}

// HumanoidParts 返回头、躯干、双臂、双腿六个方块，比例随种子微调。
func HumanoidParts(seed [32]byte) []Part {
	jitter := func(index int, amount float64) float64 {
		return 1 + (float64(seed[index%32])/255-0.5)*2*amount
	}
	height := jitter(0, 0.12)
	width := jitter(1, 0.18)
	legs := 0.8 * height * jitter(2, 0.08)
	torso := 0.6 * height * jitter(3, 0.08)
	head := 0.26 * jitter(4, 0.1)
	arm := 0.58 * height * jitter(5, 0.1)
	return []Part{
		{0, legs + torso + head/2, 0, 0.22 * width, head, 0.22 * width, 0},
		{0, legs + torso/2, 0, 0.42 * width, torso, 0.24 * width, 1},
		{-0.30 * width, legs + torso*0.55, 0, 0.12, arm, 0.12, 2},
		{0.30 * width, legs + torso*0.55, 0, 0.12, arm, 0.12, 3},
		{-0.11 * width, legs / 2, 0, 0.16, legs, 0.16, 4},
		{0.11 * width, legs / 2, 0, 0.16, legs, 0.16, 5},
	}
}

// BuildHumanoid 按 3x2 的贴图图集给每个方块分配一个单元，面内再分段。
func BuildHumanoid(seed [32]byte, subdivision int) Mesh {
	subdivision = max(1, subdivision)
	var mesh Mesh
	for _, part := range HumanoidParts(seed) {
		u0 := float32(part.Cell%3) / 3
		v0 := float32(part.Cell/3) / 2
		type face struct{ origin, du, dv, normal [3]float64 }
		hx, hy, hz := part.SX/2, part.SY/2, part.SZ/2
		faces := []face{
			{[3]float64{-hx, -hy, hz}, [3]float64{2 * hx, 0, 0}, [3]float64{0, 2 * hy, 0}, [3]float64{0, 0, 1}},
			{[3]float64{hx, -hy, -hz}, [3]float64{-2 * hx, 0, 0}, [3]float64{0, 2 * hy, 0}, [3]float64{0, 0, -1}},
			{[3]float64{hx, -hy, hz}, [3]float64{0, 0, -2 * hz}, [3]float64{0, 2 * hy, 0}, [3]float64{1, 0, 0}},
			{[3]float64{-hx, -hy, -hz}, [3]float64{0, 0, 2 * hz}, [3]float64{0, 2 * hy, 0}, [3]float64{-1, 0, 0}},
			{[3]float64{-hx, hy, hz}, [3]float64{2 * hx, 0, 0}, [3]float64{0, 0, -2 * hz}, [3]float64{0, 1, 0}},
			{[3]float64{-hx, -hy, -hz}, [3]float64{2 * hx, 0, 0}, [3]float64{0, 0, 2 * hz}, [3]float64{0, -1, 0}},
		}
		for _, f := range faces {
			base := uint16(mesh.VertexCount())
			for j := 0; j <= subdivision; j++ {
				for i := 0; i <= subdivision; i++ {
					s, t := float64(i)/float64(subdivision), float64(j)/float64(subdivision)
					for axis := 0; axis < 3; axis++ {
						mesh.Position = append(mesh.Position, float32(f.origin[axis]+f.du[axis]*s+f.dv[axis]*t+[3]float64{part.CX, part.CY, part.CZ}[axis]))
						mesh.Normal = append(mesh.Normal, float32(f.normal[axis]))
					}
					mesh.UV = append(mesh.UV, u0+float32(s)/3*0.96+0.0066, v0+float32(1-t)/2*0.96+0.0066)
				}
			}
			stride := uint16(subdivision + 1)
			for j := uint16(0); j < uint16(subdivision); j++ {
				for i := uint16(0); i < uint16(subdivision); i++ {
					a := base + j*stride + i
					b, c, d := a+1, a+stride, a+stride+1
					mesh.Index = append(mesh.Index, a, b, d, a, d, c)
				}
			}
		}
	}
	return mesh
}

type gltfDocument struct {
	Asset       map[string]any   `json:"asset"`
	Scene       int              `json:"scene"`
	Scenes      []map[string]any `json:"scenes"`
	Nodes       []map[string]any `json:"nodes"`
	Meshes      []map[string]any `json:"meshes"`
	Materials   []map[string]any `json:"materials"`
	Textures    []map[string]any `json:"textures"`
	Samplers    []map[string]any `json:"samplers"`
	Images      []map[string]any `json:"images"`
	Accessors   []map[string]any `json:"accessors"`
	BufferViews []map[string]any `json:"bufferViews"`
	Buffers     []map[string]any `json:"buffers"`
	Skins       []map[string]any `json:"skins,omitempty"`
	Animations  []map[string]any `json:"animations,omitempty"`
}

// glbParts 组装 glTF 文档与二进制缓冲区；embedPNG 为真时贴图放在缓冲区里
// （GLB），否则调用方改成 data URI（glTF）。
func glbParts(mesh Mesh, texture []byte, name string) (gltfDocument, []byte) {
	var bin bytes.Buffer
	type view struct{ offset, length, target int }
	var views []view
	addView := func(data []byte, target int) int {
		for bin.Len()%4 != 0 {
			bin.WriteByte(0)
		}
		views = append(views, view{bin.Len(), len(data), target})
		bin.Write(data)
		return len(views) - 1
	}
	floats := func(values []float32) []byte {
		out := make([]byte, 4*len(values))
		for i, v := range values {
			binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(v))
		}
		return out
	}
	indices := make([]byte, 2*len(mesh.Index))
	for i, v := range mesh.Index {
		binary.LittleEndian.PutUint16(indices[2*i:], v)
	}
	positionView := addView(floats(mesh.Position), 34962)
	normalView := addView(floats(mesh.Normal), 34962)
	uvView := addView(floats(mesh.UV), 34962)
	indexView := addView(indices, 34963)
	imageView := addView(texture, 0)
	for bin.Len()%4 != 0 {
		bin.WriteByte(0)
	}

	minimum, maximum := []float64{math.Inf(1), math.Inf(1), math.Inf(1)}, []float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	for i := 0; i < len(mesh.Position); i += 3 {
		for axis := 0; axis < 3; axis++ {
			value := float64(mesh.Position[i+axis])
			minimum[axis], maximum[axis] = math.Min(minimum[axis], value), math.Max(maximum[axis], value)
		}
	}
	document := gltfDocument{
		Asset:  map[string]any{"version": "2.0", "generator": "art-ai mock model"},
		Scene:  0,
		Scenes: []map[string]any{{"nodes": []int{0}}},
		Nodes:  []map[string]any{{"mesh": 0, "name": name}},
		Meshes: []map[string]any{{"name": name, "primitives": []map[string]any{{
			"attributes": map[string]int{"POSITION": 0, "NORMAL": 1, "TEXCOORD_0": 2},
			"indices":    3,
			"material":   0,
			"mode":       4,
		}}}},
		Materials: []map[string]any{{"name": "mock-material", "pbrMetallicRoughness": map[string]any{
			"baseColorTexture": map[string]int{"index": 0},
			"metallicFactor":   0.0,
			"roughnessFactor":  0.8,
		}}},
		Textures: []map[string]any{{"sampler": 0, "source": 0}},
		Samplers: []map[string]any{{"magFilter": 9729, "minFilter": 9987, "wrapS": 33071, "wrapT": 33071}},
		Images:   []map[string]any{{"bufferView": imageView, "mimeType": "image/png"}},
		Accessors: []map[string]any{
			{"bufferView": positionView, "componentType": 5126, "count": mesh.VertexCount(), "type": "VEC3", "min": minimum, "max": maximum},
			{"bufferView": normalView, "componentType": 5126, "count": mesh.VertexCount(), "type": "VEC3"},
			{"bufferView": uvView, "componentType": 5126, "count": mesh.VertexCount(), "type": "VEC2"},
			{"bufferView": indexView, "componentType": 5123, "count": len(mesh.Index), "type": "SCALAR"},
		},
		Buffers: []map[string]any{{"byteLength": bin.Len()}},
	}
	for _, v := range views {
		entry := map[string]any{"buffer": 0, "byteOffset": v.offset, "byteLength": v.length}
		if v.target != 0 {
			entry["target"] = v.target
		}
		document.BufferViews = append(document.BufferViews, entry)
	}
	return document, bin.Bytes()
}

func BuildGLB(mesh Mesh, texture []byte, name string, fidelity ...string) ([]byte, error) {
	document, bin := glbParts(mesh, texture, name)
	if len(fidelity) > 0 {
		bin = addModelFidelity(&document, bin, mesh.VertexCount(), fidelity[0])
	}
	return encodeGLB(document, bin)
}

// BuildPaddedGLB is the same model grown to about total bytes with zeros at
// the end of its binary buffer, which no accessor reads.
func BuildPaddedGLB(mesh Mesh, texture []byte, name, fidelity string, total int) ([]byte, error) {
	document, bin := glbParts(mesh, texture, name)
	bin = addModelFidelity(&document, bin, mesh.VertexCount(), fidelity)
	if pad := total - len(bin) - 4096; pad > 0 {
		bin = append(bin, make([]byte, pad&^3)...)
		document.Buffers[0]["byteLength"] = len(bin)
	}
	return encodeGLB(document, bin)
}

func encodeGLB(document gltfDocument, bin []byte) ([]byte, error) {
	jsonBytes, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	for len(jsonBytes)%4 != 0 {
		jsonBytes = append(jsonBytes, ' ')
	}
	total := 12 + 8 + len(jsonBytes) + 8 + len(bin)
	var out bytes.Buffer
	out.WriteString("glTF")
	_ = binary.Write(&out, binary.LittleEndian, uint32(2))
	_ = binary.Write(&out, binary.LittleEndian, uint32(total))
	_ = binary.Write(&out, binary.LittleEndian, uint32(len(jsonBytes)))
	out.WriteString("JSON")
	out.Write(jsonBytes)
	_ = binary.Write(&out, binary.LittleEndian, uint32(len(bin)))
	out.WriteString("BIN\x00")
	out.Write(bin)
	return out.Bytes(), nil
}

// BuildGLTF 输出单文件 glTF：缓冲区内联为 data URI，贴图留在缓冲区里。
func BuildGLTF(mesh Mesh, texture []byte, name string, fidelity ...string) ([]byte, error) {
	document, bin := glbParts(mesh, texture, name)
	if len(fidelity) > 0 {
		bin = addModelFidelity(&document, bin, mesh.VertexCount(), fidelity[0])
	}
	document.Buffers[0]["uri"] = "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(bin)
	return json.Marshal(document)
}

// A real one-joint skin binds every vertex. Animation moves that joint with
// two keyframes. Static mock generation keeps its old bytes and behavior.
func addModelFidelity(document *gltfDocument, bin []byte, vertices int, level string) []byte {
	if level != "skeleton" && level != "animation" {
		return bin
	}
	addAccessor := func(data []byte, componentType, count int, shape string) int {
		for len(bin)%4 != 0 {
			bin = append(bin, 0)
		}
		view := len(document.BufferViews)
		document.BufferViews = append(document.BufferViews, map[string]any{"buffer": 0, "byteOffset": len(bin), "byteLength": len(data)})
		bin = append(bin, data...)
		index := len(document.Accessors)
		document.Accessors = append(document.Accessors, map[string]any{"bufferView": view, "componentType": componentType, "count": count, "type": shape})
		return index
	}
	floatBytes := func(values []float32) []byte {
		data := make([]byte, len(values)*4)
		for i, value := range values {
			binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(value))
		}
		return data
	}
	joints := addAccessor(make([]byte, vertices*8), 5123, vertices, "VEC4")
	weights := make([]float32, vertices*4)
	for i := 0; i < vertices; i++ {
		weights[i*4] = 1
	}
	weight := addAccessor(floatBytes(weights), 5126, vertices, "VEC4")
	primitives := document.Meshes[0]["primitives"].([]map[string]any)
	attributes := primitives[0]["attributes"].(map[string]int)
	attributes["JOINTS_0"], attributes["WEIGHTS_0"] = joints, weight
	document.Nodes[0]["skin"] = 0
	document.Nodes = append(document.Nodes, map[string]any{"name": "MockRoot"})
	document.Scenes[0]["nodes"] = []int{0, 1}
	document.Skins = []map[string]any{{"joints": []int{1}, "skeleton": 1}}
	if level == "animation" {
		input := addAccessor(floatBytes([]float32{0, 1}), 5126, 2, "SCALAR")
		document.Accessors[input]["min"], document.Accessors[input]["max"] = []float32{0}, []float32{1}
		output := addAccessor(floatBytes([]float32{0, 0, 0, 0, 0.1, 0}), 5126, 2, "VEC3")
		document.Animations = []map[string]any{{"name": "MockBounce", "samplers": []map[string]any{{"input": input, "output": output, "interpolation": "LINEAR"}}, "channels": []map[string]any{{"sampler": 0, "target": map[string]any{"node": 1, "path": "translation"}}}}}
	}
	for len(bin)%4 != 0 {
		bin = append(bin, 0)
	}
	document.Buffers[0]["byteLength"] = len(bin)
	return bin
}

// OBJMaterial says which material library the mock OBJ names.
type OBJMaterial int

const (
	// OBJGeometryOnly names no library: the result declares no MTL slot.
	OBJGeometryOnly OBJMaterial = iota
	// OBJDeliveredLibrary names model.mtl, which the result also delivers.
	OBJDeliveredLibrary
	// OBJDanglingLibrary is the real Meshy Convert OBJ (#330, dev-test note
	// 99526): a Blender export that names temp.mtl and uses its material,
	// while the conversion never delivers that file.
	OBJDanglingLibrary
)

// BuildOBJ writes the mock OBJ with the material reference its result has.
func BuildOBJ(mesh Mesh, label string, material OBJMaterial) []byte {
	var out strings.Builder
	fmt.Fprintf(&out, "# mock model %s\n", label)
	switch material {
	case OBJDeliveredLibrary:
		out.WriteString("mtllib model.mtl\n")
	case OBJDanglingLibrary:
		out.WriteString("mtllib temp.mtl\n")
	}
	out.WriteString("o MockHumanoid\n")
	for i := 0; i < mesh.VertexCount(); i++ {
		fmt.Fprintf(&out, "v %.5f %.5f %.5f\n", mesh.Position[3*i], mesh.Position[3*i+1], mesh.Position[3*i+2])
	}
	for i := 0; i < mesh.VertexCount(); i++ {
		fmt.Fprintf(&out, "vt %.5f %.5f\n", mesh.UV[2*i], mesh.UV[2*i+1])
	}
	for i := 0; i < mesh.VertexCount(); i++ {
		fmt.Fprintf(&out, "vn %.3f %.3f %.3f\n", mesh.Normal[3*i], mesh.Normal[3*i+1], mesh.Normal[3*i+2])
	}
	if material != OBJGeometryOnly {
		out.WriteString("usemtl mock-material\n")
	}
	for i := 0; i+2 < len(mesh.Index); i += 3 {
		a, b, c := int(mesh.Index[i])+1, int(mesh.Index[i+1])+1, int(mesh.Index[i+2])+1
		fmt.Fprintf(&out, "f %d/%d/%d %d/%d/%d %d/%d/%d\n", a, a, a, b, b, b, c, c, c)
	}
	return []byte(out.String())
}

func BuildMTL() []byte {
	return []byte("newmtl mock-material\nKa 1.0 1.0 1.0\nKd 1.0 1.0 1.0\nKs 0.0 0.0 0.0\nNs 10.0\nmap_Kd texture.png\n")
}

// BuildSTL 写二进制 STL。抬头避开 "solid"，免得被当成文本 STL。
func BuildSTL(mesh Mesh) []byte {
	var out bytes.Buffer
	header := make([]byte, 80)
	copy(header, "mock binary stl")
	out.Write(header)
	_ = binary.Write(&out, binary.LittleEndian, uint32(mesh.TriangleCount()))
	for i := 0; i+2 < len(mesh.Index); i += 3 {
		a, b, c := int(mesh.Index[i]), int(mesh.Index[i+1]), int(mesh.Index[i+2])
		for _, value := range mesh.Normal[3*a : 3*a+3] {
			_ = binary.Write(&out, binary.LittleEndian, value)
		}
		for _, vertex := range []int{a, b, c} {
			for _, value := range mesh.Position[3*vertex : 3*vertex+3] {
				_ = binary.Write(&out, binary.LittleEndian, value)
			}
		}
		out.Write([]byte{0, 0})
	}
	return out.Bytes()
}

type fbxNode struct {
	name       string
	properties [][]byte
	children   []fbxNode
}

func fbxString(value string) []byte {
	out := []byte{'S'}
	out = binary.LittleEndian.AppendUint32(out, uint32(len(value)))
	return append(out, value...)
}

func fbxInt32(value int32) []byte {
	return binary.LittleEndian.AppendUint32([]byte{'I'}, uint32(value))
}

func fbxInt64(value int64) []byte {
	return binary.LittleEndian.AppendUint64([]byte{'L'}, uint64(value))
}

func fbxDoubles(values []float64) []byte {
	raw := make([]byte, 8*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint64(raw[8*i:], math.Float64bits(v))
	}
	return fbxArray('d', len(values), raw)
}

func fbxFloats(values []float32) []byte {
	raw := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(raw[4*i:], math.Float32bits(v))
	}
	return fbxArray('f', len(values), raw)
}

func fbxInt64s(values []int64) []byte {
	raw := make([]byte, 8*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint64(raw[8*i:], uint64(v))
	}
	return fbxArray('l', len(values), raw)
}

func fbxInts(values []int32) []byte {
	raw := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(raw[4*i:], uint32(v))
	}
	return fbxArray('i', len(values), raw)
}

// fbxArray 用 zlib 压缩数组载荷（编码 1），既小又走检查器的压缩分支。
func fbxArray(kind byte, count int, raw []byte) []byte {
	var packed bytes.Buffer
	writer := zlib.NewWriter(&packed)
	_, _ = writer.Write(raw)
	_ = writer.Close()
	out := []byte{kind}
	out = binary.LittleEndian.AppendUint32(out, uint32(count))
	out = binary.LittleEndian.AppendUint32(out, 1)
	out = binary.LittleEndian.AppendUint32(out, uint32(packed.Len()))
	return append(out, packed.Bytes()...)
}

func writeFBXNode(body *bytes.Buffer, node fbxNode) {
	const header = 13
	start := body.Len()
	body.Write(make([]byte, header))
	body.Bytes()[start+header-1] = byte(len(node.name))
	body.WriteString(node.name)
	propertyBytes := 0
	for _, property := range node.properties {
		body.Write(property)
		propertyBytes += len(property)
	}
	for _, child := range node.children {
		writeFBXNode(body, child)
	}
	if node.children != nil {
		body.Write(make([]byte, header))
	}
	data := body.Bytes()
	binary.LittleEndian.PutUint32(data[start:], uint32(body.Len()))
	binary.LittleEndian.PutUint32(data[start+4:], uint32(len(node.properties)))
	binary.LittleEndian.PutUint32(data[start+8:], uint32(propertyBytes))
}

// BuildFBX 写 7400 版二进制 FBX：文件头扩展、一个带顶点与索引数组的几何
// 体。不含材质；静态模型也不含骨骼，目的只是让格式页的 FBX 是一份结构合法的
// 文件。fidelity 为 skeleton/animation 时与 GLB 相同地加入单骨骼蒙皮与两帧
// 动画（见 addFBXFidelity），绑骨与动画产物才能通过声明的结构检查（#265）。
func BuildFBX(mesh Mesh, label string, fidelity ...string) []byte {
	vertices := make([]float64, len(mesh.Position))
	for i, v := range mesh.Position {
		vertices[i] = float64(v)
	}
	polygon := make([]int32, 0, len(mesh.Index))
	for i := 0; i+2 < len(mesh.Index); i += 3 {
		polygon = append(polygon, int32(mesh.Index[i]), int32(mesh.Index[i+1]), -int32(mesh.Index[i+2])-1)
	}
	nodes := []fbxNode{
		{name: "FBXHeaderExtension", children: []fbxNode{
			{name: "FBXHeaderVersion", properties: [][]byte{fbxInt32(1003)}},
			{name: "Creator", properties: [][]byte{fbxString("art-ai mock model " + label)}},
		}},
		{name: "Objects", children: []fbxNode{
			{name: "Geometry", properties: [][]byte{fbxInt64(1000), fbxString("MockHumanoid\x00\x01Geometry"), fbxString("Mesh")}, children: []fbxNode{
				{name: "Vertices", properties: [][]byte{fbxDoubles(vertices)}},
				{name: "PolygonVertexIndex", properties: [][]byte{fbxInts(polygon)}},
			}},
		}},
	}
	if len(fidelity) > 0 {
		nodes = addFBXFidelity(nodes, mesh.VertexCount(), fidelity[0])
	}
	var body bytes.Buffer
	body.WriteString("Kaydara FBX Binary  \x00\x1a\x00")
	body.Write(binary.LittleEndian.AppendUint32(nil, 7400))
	for _, node := range nodes {
		writeFBXNode(&body, node)
	}
	body.Write(make([]byte, 13))
	body.Write([]byte{0xfa, 0xbc, 0xab, 0x09, 0xd0, 0xc8, 0xd4, 0x66, 0xb1, 0x76, 0xfb, 0x83, 0x1c, 0xf7, 0x26, 0x7e})
	body.Write(make([]byte, 4))
	body.Write(make([]byte, 16-body.Len()%16))
	body.Write(binary.LittleEndian.AppendUint32(nil, 7400))
	body.Write(make([]byte, 120))
	body.Write([]byte{0xf8, 0x5a, 0x8c, 0x6a, 0xde, 0xf5, 0xd9, 0x7e, 0xec, 0xe9, 0x0c, 0xe3, 0x75, 0x8f, 0x29, 0x0b})
	return body.Bytes()
}

// addFBXFidelity mirrors addModelFidelity for FBX: a Skin deformer with one
// Cluster binds every vertex of the geometry to the LimbNode "MockRoot", and
// animation adds an AnimationStack whose layer moves that bone with two keys.
// Static mock generation keeps its old bytes and behavior.
func addFBXFidelity(nodes []fbxNode, vertices int, level string) []fbxNode {
	if level != "skeleton" && level != "animation" {
		return nodes
	}
	const geometry, bone, skin, cluster = 1000, 2000, 3000, 3001
	indexes := make([]int32, vertices)
	weights := make([]float64, vertices)
	for i := range indexes {
		indexes[i], weights[i] = int32(i), 1
	}
	objects := []fbxNode{
		{name: "Model", properties: [][]byte{fbxInt64(bone), fbxString("MockRoot\x00\x01Model"), fbxString("LimbNode")}},
		{name: "Deformer", properties: [][]byte{fbxInt64(skin), fbxString("MockSkin\x00\x01Deformer"), fbxString("Skin")}},
		{name: "Deformer", properties: [][]byte{fbxInt64(cluster), fbxString("MockRoot\x00\x01SubDeformer"), fbxString("Cluster")}, children: []fbxNode{
			{name: "Indexes", properties: [][]byte{fbxInts(indexes)}},
			{name: "Weights", properties: [][]byte{fbxDoubles(weights)}},
		}},
	}
	connections := []fbxNode{
		fbxConnection("OO", bone, 0),
		fbxConnection("OO", skin, geometry),
		fbxConnection("OO", cluster, skin),
		fbxConnection("OO", bone, cluster),
	}
	if level == "animation" {
		const stack, layer, curveNode, curve = 4000, 4001, 4002, 4003
		objects = append(objects,
			fbxNode{name: "AnimationStack", properties: [][]byte{fbxInt64(stack), fbxString("MockBounce\x00\x01AnimStack"), fbxString("")}},
			fbxNode{name: "AnimationLayer", properties: [][]byte{fbxInt64(layer), fbxString("BaseLayer\x00\x01AnimLayer"), fbxString("")}},
			fbxNode{name: "AnimationCurveNode", properties: [][]byte{fbxInt64(curveNode), fbxString("T\x00\x01AnimCurveNode"), fbxString("")}},
			// One second is 46186158000 FBX ticks.
			fbxNode{name: "AnimationCurve", properties: [][]byte{fbxInt64(curve), fbxString("\x00\x01AnimCurve"), fbxString("")}, children: []fbxNode{
				{name: "KeyTime", properties: [][]byte{fbxInt64s([]int64{0, 46186158000})}},
				{name: "KeyValueFloat", properties: [][]byte{fbxFloats([]float32{0, 0.1})}},
			}},
		)
		connections = append(connections,
			fbxConnection("OO", layer, stack),
			fbxConnection("OO", curveNode, layer),
			fbxConnection("OP", curveNode, bone, "Lcl Translation"),
			fbxConnection("OP", curve, curveNode, "d|Y"),
		)
	}
	for i := range nodes {
		if nodes[i].name == "Objects" {
			nodes[i].children = append(nodes[i].children, objects...)
		}
	}
	return append(nodes, fbxNode{name: "Connections", children: connections})
}

func fbxConnection(kind string, child, parent int64, property ...string) fbxNode {
	properties := [][]byte{fbxString(kind), fbxInt64(child), fbxInt64(parent)}
	for _, name := range property {
		properties = append(properties, fbxString(name))
	}
	return fbxNode{name: "C", properties: properties}
}

func BuildZIP(files map[string][]byte, order []string) ([]byte, error) {
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, name := range order {
		entry, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write(files[name]); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// BuildUSDZ 打包一份合法的 USDC 小 crate 和贴图。crate 是仓库里随检查器
// 一起维护的合成夹具，不含场景网格；USDZ 在这里的作用是让格式页有一份
// 能下载、能通过交付检查的 USDZ。
func BuildUSDZ(texture []byte) ([]byte, error) {
	return BuildZIP(map[string][]byte{"model.usdc": mockUSDC, "texture.png": texture}, []string{"model.usdc", "texture.png"})
}

func Build3MF(mesh Mesh) ([]byte, error) {
	var model strings.Builder
	model.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	model.WriteString(`<model unit="meter" xml:lang="en-US" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><resources><object id="1" type="model"><mesh><vertices>`)
	for i := 0; i < mesh.VertexCount(); i++ {
		fmt.Fprintf(&model, `<vertex x="%.5f" y="%.5f" z="%.5f"/>`, mesh.Position[3*i], -mesh.Position[3*i+2], mesh.Position[3*i+1])
	}
	model.WriteString(`</vertices><triangles>`)
	for i := 0; i+2 < len(mesh.Index); i += 3 {
		fmt.Fprintf(&model, `<triangle v1="%d" v2="%d" v3="%d"/>`, mesh.Index[i], mesh.Index[i+1], mesh.Index[i+2])
	}
	model.WriteString(`</triangles></mesh></object></resources><build><item objectid="1"/></build></model>`)
	files := map[string][]byte{
		"[Content_Types].xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="model" ContentType="application/vnd.ms-package.3dmanufacturing-3dmodel+xml"/></Types>`),
		"_rels/.rels":         []byte(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Target="/3D/3dmodel.model" Id="rel0" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/></Relationships>`),
		"3D/3dmodel.model":    []byte(model.String()),
	}
	return BuildZIP(files, []string{"[Content_Types].xml", "_rels/.rels", "3D/3dmodel.model"})
}

// BuildBlend 写 Blender 文件头加零填充。没有对应的解析检查，只走通用的外部
// 地址扫描。
func BuildBlend() []byte {
	out := []byte("BLENDER-v404")
	return append(out, make([]byte, 244)...)
}
