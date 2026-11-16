package fakeupstream

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"math"
	"sync"

	"github.com/LeXwDeX/Plinth/internal/providers/mock/modelfiles"
)

// A fake whose upstream returns a model "at the size the request asked for" (the
// Hyper3D Rodin generation: a face count and a texture mode) cannot serve the
// 72-triangle fixture whatever the request. SizedModelFile builds the same
// textured humanoid at an exact triangle count and with one embedded PNG per
// requested edge, so the checks that read a delivered model (the triangle
// budget, the texture-size budget, the PBR slots) see the numbers the request
// implies. The bytes are small by construction: the mesh stops at the fixture's
// 16-bit index ceiling and the textures are flat or six-tone, so a 6144-pixel
// PNG is about 1 MB and is built once per process.
const (
	// MaxSizedModelTriangles is the most triangles SizedModelFile carries, the
	// same ceiling as LowPolyModelFile: 36 faces cut into 41 x 41 squares.
	MaxSizedModelTriangles = lowPolyTrianglesPerStep * lowPolyMaxSubdivision * lowPolyMaxSubdivision
	// MaxSizedModelTextureEdge is the longest texture edge, in pixels, that
	// SizedModelFile builds (a 12K pack of the Rodin tables is 12288).
	MaxSizedModelTextureEdge = 16384

	// minSizedTextureEdge keeps the 3 x 2 atlas cells at least one pixel wide.
	minSizedTextureEdge = 6
	// maxSizedTextures are the PBR slots a model carries: base color,
	// metallic-roughness and normal, in that order.
	maxSizedTextures = 3
)

// The kinds of PNG a sized model embeds, in the order of its texture slots.
const (
	textureBaseColor = iota
	textureMetallicRoughness
	textureNormal
)

// atlasTones are the six flat tones of the base color atlas, in the 3 x 2 layout
// the humanoid's UVs expect.
var atlasTones = [6][4]byte{
	{236, 196, 160, 255}, {52, 120, 190, 255}, {64, 90, 150, 255},
	{70, 98, 160, 255}, {90, 70, 50, 255}, {80, 64, 46, 255},
}

// flatTones are the two flat maps of the pack: glTF metallic-roughness (green is
// roughness, blue is metallic, red an unused occlusion) and a tangent-space
// normal pointing straight out.
var flatTones = map[int][4]byte{
	textureMetallicRoughness: {255, 128, 0, 255},
	textureNormal:            {128, 128, 255, 255},
}

// SizedModelTriangles is the triangle count SizedModelFile carries for a request:
// the request kept between the one-step humanoid and MaxSizedModelTriangles and
// rounded down to an even number, because a closed triangle mesh has no other.
func SizedModelTriangles(triangles int) int {
	triangles = min(max(triangles, lowPolyTrianglesPerStep), MaxSizedModelTriangles)
	return triangles &^ 1
}

// SizedModelFile is the fixed textured humanoid GLB with exactly
// SizedModelTriangles(triangles) triangles and one embedded PNG for each of the
// textureEdges (square, in pixels; at most three, wired in the order base color,
// metallic-roughness, normal). No edge means a material without textures. The
// mesh stays one closed, unwrapped, unskinned surface, so a file at the
// humanoid's own step is the plain fixture's geometry and a request between two
// steps gets the next lower step with some of its triangles cut in three.
//
// The count and the edges are what Plinth reads back (modelinspect counts the
// drawable triangles of the index buffer and the edge from each PNG's header);
// nothing is declared that the bytes do not hold.
func SizedModelFile(triangles int, textureEdges ...int) (File, error) {
	if len(textureEdges) > maxSizedTextures {
		return File{}, fmt.Errorf("fakeupstream: a sized model has at most %d textures, got %d", maxSizedTextures, len(textureEdges))
	}
	for _, edge := range textureEdges {
		if edge < minSizedTextureEdge || edge > MaxSizedModelTextureEdge {
			return File{}, fmt.Errorf("fakeupstream: a sized model texture edge is between %d and %d pixels, got %d",
				minSizedTextureEdge, MaxSizedModelTextureEdge, edge)
		}
	}
	mesh, err := sizedHumanoid(SizedModelTriangles(triangles))
	if err != nil {
		return File{}, err
	}
	textures := make([][]byte, len(textureEdges))
	for kind, edge := range textureEdges {
		textures[kind] = sizedTexturePNG(kind, edge)
	}
	glb, err := buildSizedGLB(mesh, textures)
	if err != nil {
		return File{}, err
	}
	return File{MediaType: "model/gltf-binary", Data: glb}, nil
}

// sizedHumanoid is the humanoid cut at the step whose triangles do not pass the
// target, with the remaining triangles made by splitting some of them in three.
func sizedHumanoid(triangles int) (modelfiles.Mesh, error) {
	seed := sha256.Sum256([]byte("fixture\x00"))
	mesh := modelfiles.BuildHumanoid(seed, lowPolySubdivision(triangles))
	return splitTriangles(mesh, (triangles-mesh.TriangleCount())/2)
}

// splitTriangles adds two triangles per split: a triangle is replaced by the
// three that meet at a new vertex at its centroid, which keeps the surface
// closed, the winding and the UV cell, and adds no degenerate triangle. A split
// triangle can be split again, so any count is reachable from the one-step
// model. The result keeps 16-bit indices: the humanoid's vertices plus one per
// split stay below 65536 for every step the fixture allows.
func splitTriangles(mesh modelfiles.Mesh, splits int) (modelfiles.Mesh, error) {
	if splits <= 0 {
		return mesh, nil
	}
	triangles := make([][3]uint16, 0, mesh.TriangleCount()+2*splits)
	for index := 0; index+2 < len(mesh.Index); index += 3 {
		triangles = append(triangles, [3]uint16{mesh.Index[index], mesh.Index[index+1], mesh.Index[index+2]})
	}
	out := modelfiles.Mesh{
		Position: append(make([]float32, 0, len(mesh.Position)+3*splits), mesh.Position...),
		Normal:   append(make([]float32, 0, len(mesh.Normal)+3*splits), mesh.Normal...),
		UV:       append(make([]float32, 0, len(mesh.UV)+2*splits), mesh.UV...),
	}
	for done := 0; done < splits; done++ {
		corner := triangles[done]
		middle := out.VertexCount()
		if middle > math.MaxUint16 {
			return modelfiles.Mesh{}, fmt.Errorf("fakeupstream: %d splits exceed the 16-bit vertex range", splits)
		}
		var normal [3]float64
		for axis := 0; axis < 3; axis++ {
			var position float64
			for _, vertex := range corner {
				position += float64(out.Position[3*int(vertex)+axis])
				normal[axis] += float64(out.Normal[3*int(vertex)+axis])
			}
			out.Position = append(out.Position, float32(position/3))
		}
		length := math.Sqrt(normal[0]*normal[0] + normal[1]*normal[1] + normal[2]*normal[2])
		for axis := 0; axis < 3; axis++ {
			out.Normal = append(out.Normal, float32(normal[axis]/length))
		}
		for axis := 0; axis < 2; axis++ {
			var coordinate float64
			for _, vertex := range corner {
				coordinate += float64(out.UV[2*int(vertex)+axis])
			}
			out.UV = append(out.UV, float32(coordinate/3))
		}
		centre := uint16(middle)
		triangles[done] = [3]uint16{corner[0], corner[1], centre}
		triangles = append(triangles, [3]uint16{corner[1], corner[2], centre}, [3]uint16{corner[2], corner[0], centre})
	}
	out.Index = make([]uint16, 0, 3*len(triangles))
	for _, triangle := range triangles {
		out.Index = append(out.Index, triangle[:]...)
	}
	return out, nil
}

var sizedTextureCache sync.Map // sizedTextureKey -> []byte

type sizedTextureKey struct{ kind, edge int }

// sizedTexturePNG is the PNG of one texture slot at the given edge, built once
// per process: the 6144-pixel base color takes about 70 ms and the 12288-pixel one
// about 0.3 s (on a laptop).
func sizedTexturePNG(kind, edge int) []byte {
	key := sizedTextureKey{kind, edge}
	if cached, ok := sizedTextureCache.Load(key); ok {
		return cached.([]byte)
	}
	built, _ := sizedTextureCache.LoadOrStore(key, encodeSizedPNG(kind, edge))
	return built.([]byte)
}

// encodeSizedPNG writes an edge x edge, 8-bit RGBA PNG without ever holding the
// pixels: every row is one of at most two prebuilt rows (the atlas has two bands,
// a flat map one), so the zlib stream is fed row by row and the memory stays near
// the size of the output. The atlas has exactly the pixels fixtureTexture draws.
func encodeSizedPNG(kind, edge int) []byte {
	row := func(tones ...[4]byte) []byte {
		out := make([]byte, 1+4*edge) // filter type 0 (none) in front of the pixels
		for x := 0; x < edge; x++ {
			tone := tones[0]
			if len(tones) > 1 {
				tone = tones[min(x/(edge/3), 2)]
			}
			copy(out[1+4*x:], tone[:])
		}
		return out
	}
	var bands [][]byte
	bandOf := func(int) int { return 0 }
	if kind == textureBaseColor {
		bands = [][]byte{row(atlasTones[0], atlasTones[1], atlasTones[2]), row(atlasTones[3], atlasTones[4], atlasTones[5])}
		bandOf = func(y int) int { return min(y/(edge/2), 1) }
	} else {
		bands = [][]byte{row(flatTones[kind])}
	}
	var packed bytes.Buffer
	writer, _ := zlib.NewWriterLevel(&packed, zlib.BestSpeed)
	for y := 0; y < edge; y++ {
		_, _ = writer.Write(bands[bandOf(y)])
	}
	_ = writer.Close()

	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header[0:], uint32(edge))
	binary.BigEndian.PutUint32(header[4:], uint32(edge))
	header[8], header[9] = 8, 6 // bit depth 8, color type RGBA
	writePNGChunk(&out, "IHDR", header)
	// The same 32 KiB IDAT pieces the standard encoder writes.
	for data := packed.Bytes(); len(data) > 0; {
		piece := min(len(data), 1<<15)
		writePNGChunk(&out, "IDAT", data[:piece])
		data = data[piece:]
	}
	writePNGChunk(&out, "IEND", nil)
	return out.Bytes()
}

func writePNGChunk(out *bytes.Buffer, kind string, data []byte) {
	_ = binary.Write(out, binary.BigEndian, uint32(len(data)))
	out.WriteString(kind)
	out.Write(data)
	checksum := crc32.NewIEEE()
	_, _ = checksum.Write([]byte(kind))
	_, _ = checksum.Write(data)
	_ = binary.Write(out, binary.BigEndian, checksum.Sum32())
}

// buildSizedGLB writes the mesh and its PNGs (base color, metallic-roughness,
// normal; fewer when fewer were asked for) as one self-contained GLB 2.0.
func buildSizedGLB(mesh modelfiles.Mesh, textures [][]byte) ([]byte, error) {
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
		for index, value := range values {
			binary.LittleEndian.PutUint32(out[4*index:], math.Float32bits(value))
		}
		return out
	}
	indices := make([]byte, 2*len(mesh.Index))
	for index, value := range mesh.Index {
		binary.LittleEndian.PutUint16(indices[2*index:], value)
	}
	positionView := addView(floats(mesh.Position), 34962)
	normalView := addView(floats(mesh.Normal), 34962)
	uvView := addView(floats(mesh.UV), 34962)
	indexView := addView(indices, 34963)
	imageViews := make([]int, len(textures))
	for index, data := range textures {
		imageViews[index] = addView(data, 0)
	}
	for bin.Len()%4 != 0 {
		bin.WriteByte(0)
	}

	minimum, maximum := []float64{math.Inf(1), math.Inf(1), math.Inf(1)}, []float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	for index := 0; index < len(mesh.Position); index += 3 {
		for axis := 0; axis < 3; axis++ {
			value := float64(mesh.Position[index+axis])
			minimum[axis], maximum[axis] = math.Min(minimum[axis], value), math.Max(maximum[axis], value)
		}
	}
	material := map[string]any{"name": "fixture-material"}
	pbr := map[string]any{"metallicFactor": 0.0, "roughnessFactor": 0.8}
	if len(textures) > textureBaseColor {
		pbr["baseColorTexture"] = map[string]int{"index": textureBaseColor}
	}
	if len(textures) > textureMetallicRoughness {
		// The factors scale the map, so they stay at their neutral value.
		pbr["metallicRoughnessTexture"] = map[string]int{"index": textureMetallicRoughness}
		pbr["metallicFactor"], pbr["roughnessFactor"] = 1.0, 1.0
	}
	material["pbrMetallicRoughness"] = pbr
	if len(textures) > textureNormal {
		material["normalTexture"] = map[string]int{"index": textureNormal}
	}
	document := map[string]any{
		"asset":  map[string]any{"version": "2.0", "generator": "art-ai fake sized model"},
		"scene":  0,
		"scenes": []map[string]any{{"nodes": []int{0}}},
		"nodes":  []map[string]any{{"mesh": 0, "name": "Fixture"}},
		"meshes": []map[string]any{{"name": "Fixture", "primitives": []map[string]any{{
			"attributes": map[string]int{"POSITION": 0, "NORMAL": 1, "TEXCOORD_0": 2},
			"indices":    3,
			"material":   0,
			"mode":       4,
		}}}},
		"materials": []map[string]any{material},
		"accessors": []map[string]any{
			{"bufferView": positionView, "componentType": 5126, "count": mesh.VertexCount(), "type": "VEC3", "min": minimum, "max": maximum},
			{"bufferView": normalView, "componentType": 5126, "count": mesh.VertexCount(), "type": "VEC3"},
			{"bufferView": uvView, "componentType": 5126, "count": mesh.VertexCount(), "type": "VEC2"},
			{"bufferView": indexView, "componentType": 5123, "count": len(mesh.Index), "type": "SCALAR"},
		},
		"buffers": []map[string]any{{"byteLength": bin.Len()}},
	}
	if len(textures) > 0 {
		document["samplers"] = []map[string]any{{"magFilter": 9729, "minFilter": 9987, "wrapS": 33071, "wrapT": 33071}}
		var glTextures, images []map[string]any
		for index, imageView := range imageViews {
			glTextures = append(glTextures, map[string]any{"sampler": 0, "source": index})
			images = append(images, map[string]any{"bufferView": imageView, "mimeType": "image/png"})
		}
		document["textures"], document["images"] = glTextures, images
	}
	bufferViews := make([]map[string]any, 0, len(views))
	for _, v := range views {
		entry := map[string]any{"buffer": 0, "byteOffset": v.offset, "byteLength": v.length}
		if v.target != 0 {
			entry["target"] = v.target
		}
		bufferViews = append(bufferViews, entry)
	}
	document["bufferViews"] = bufferViews

	jsonBytes, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	for len(jsonBytes)%4 != 0 {
		jsonBytes = append(jsonBytes, ' ')
	}
	var out bytes.Buffer
	out.WriteString("glTF")
	_ = binary.Write(&out, binary.LittleEndian, uint32(2))
	_ = binary.Write(&out, binary.LittleEndian, uint32(12+8+len(jsonBytes)+8+bin.Len()))
	_ = binary.Write(&out, binary.LittleEndian, uint32(len(jsonBytes)))
	out.WriteString("JSON")
	out.Write(jsonBytes)
	_ = binary.Write(&out, binary.LittleEndian, uint32(bin.Len()))
	out.WriteString("BIN\x00")
	out.Write(bin.Bytes())
	return out.Bytes(), nil
}
