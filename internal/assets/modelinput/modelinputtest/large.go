package modelinputtest

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"math"
	"math/rand/v2"
)

// LargeOptions describes a synthetic static GLB the size of a real generation
// result. Grid is the vertex count per side (Grid*Grid vertices and
// 2*(Grid-1)^2 uint32 triangles); every image is a valid PNG.
type LargeOptions struct {
	Grid   int
	Images []LargeImage
}

// LargeImage is a grayscale PNG. A uniform image compresses to a small file
// however large its dimensions; Noise makes every pixel incompressible, so the
// fixture can reach a real file size.
type LargeImage struct {
	Width, Height int
	Noise         bool
	// RGBA writes 8-bit RGBA pixels (a real base-color map) instead of gray.
	RGBA bool
}

// Large builds the GLB in memory. Positions span [0,1]x[0,1] in z=0 with +Z
// normals and matching UVs, so every accessor bound is exact.
func Large(options LargeOptions) []byte {
	side := options.Grid
	vertices := side * side
	triangles := 2 * (side - 1) * (side - 1)
	positionsLength := vertices * 12
	normalsLength := vertices * 12
	uvLength := vertices * 8
	indicesLength := triangles * 3 * 4
	bin := make([]byte, 0, positionsLength+normalsLength+uvLength+indicesLength)
	putFloat := func(value float32) {
		bin = binary.LittleEndian.AppendUint32(bin, math.Float32bits(value))
	}
	step := float32(1) / float32(side-1)
	coordinate := func(index int) float32 {
		if index == side-1 {
			return 1
		}
		return float32(index) * step
	}
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			putFloat(coordinate(x))
			putFloat(coordinate(y))
			putFloat(0)
		}
	}
	for range vertices {
		putFloat(0)
		putFloat(0)
		putFloat(1)
	}
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			putFloat(coordinate(x))
			putFloat(coordinate(y))
		}
	}
	for y := 0; y < side-1; y++ {
		for x := 0; x < side-1; x++ {
			a := uint32(y*side + x)
			b, c, d := a+1, a+uint32(side), a+uint32(side)+1
			for _, index := range []uint32{a, b, c, b, d, c} {
				bin = binary.LittleEndian.AppendUint32(bin, index)
			}
		}
	}
	views := []any{
		map[string]any{"buffer": 0, "byteOffset": 0, "byteLength": positionsLength, "target": 34962},
		map[string]any{"buffer": 0, "byteOffset": positionsLength, "byteLength": normalsLength, "target": 34962},
		map[string]any{"buffer": 0, "byteOffset": positionsLength + normalsLength, "byteLength": uvLength, "target": 34962},
		map[string]any{"buffer": 0, "byteOffset": positionsLength + normalsLength + uvLength, "byteLength": indicesLength, "target": 34963},
	}
	accessors := []any{
		map[string]any{"bufferView": 0, "componentType": 5126, "count": vertices, "type": "VEC3", "min": []float64{0, 0, 0}, "max": []float64{1, 1, 0}},
		map[string]any{"bufferView": 1, "componentType": 5126, "count": vertices, "type": "VEC3"},
		map[string]any{"bufferView": 2, "componentType": 5126, "count": vertices, "type": "VEC2"},
		map[string]any{"bufferView": 3, "componentType": 5125, "count": triangles * 3, "type": "SCALAR"},
	}
	primitive := map[string]any{"attributes": map[string]any{"POSITION": 0, "NORMAL": 1, "TEXCOORD_0": 2}, "indices": 3, "mode": 4}
	doc := map[string]any{"asset": map[string]any{"version": "2.0", "generator": "modelinputtest.Large"}, "scene": 0, "scenes": []any{map[string]any{"nodes": []any{0}}}, "nodes": []any{map[string]any{"mesh": 0}}}
	if len(options.Images) > 0 {
		var images, textures []any
		material := map[string]any{}
		for index, spec := range options.Images {
			for len(bin)%4 != 0 {
				bin = append(bin, 0)
			}
			encoded := grayPNG(spec, uint64(index+1))
			views = append(views, map[string]any{"buffer": 0, "byteOffset": len(bin), "byteLength": len(encoded)})
			bin = append(bin, encoded...)
			images = append(images, map[string]any{"bufferView": len(views) - 1, "mimeType": "image/png"})
			textures = append(textures, map[string]any{"source": index})
		}
		pbr := map[string]any{"baseColorTexture": map[string]any{"index": 0}}
		if len(options.Images) > 1 {
			pbr["metallicRoughnessTexture"] = map[string]any{"index": 1}
		}
		material["pbrMetallicRoughness"] = pbr
		if len(options.Images) > 2 {
			material["normalTexture"] = map[string]any{"index": 2}
		}
		doc["images"], doc["textures"], doc["materials"] = images, textures, []any{material}
		primitive["material"] = 0
	}
	doc["meshes"] = []any{map[string]any{"primitives": []any{primitive}}}
	doc["bufferViews"], doc["accessors"] = views, accessors
	doc["buffers"] = []any{map[string]any{"byteLength": len(bin)}}
	return GLB(doc, bin)
}

// grayPNG writes an 8-bit grayscale (or RGBA) PNG with filter type 0 rows. The standard
// encoder tries every filter per row; this keeps an 8192-square fixture fast.
func grayPNG(spec LargeImage, seed uint64) []byte {
	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	chunk := func(kind string, data []byte) {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(data)))
		out.Write(length[:])
		crc := crc32.NewIEEE()
		crc.Write([]byte(kind))
		crc.Write(data)
		out.WriteString(kind)
		out.Write(data)
		out.Write(binary.BigEndian.AppendUint32(nil, crc.Sum32()))
	}
	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header[0:], uint32(spec.Width))
	binary.BigEndian.PutUint32(header[4:], uint32(spec.Height))
	header[8], header[9] = 8, 0 // 8-bit grayscale
	channels := 1
	if spec.RGBA {
		header[9], channels = 6, 4
	}
	chunk("IHDR", header)
	var compressed bytes.Buffer
	writer, _ := zlib.NewWriterLevel(&compressed, zlib.BestSpeed)
	row := make([]byte, spec.Width*channels+1)
	random := rand.New(rand.NewPCG(seed, 264))
	for y := 0; y < spec.Height; y++ {
		if spec.Noise {
			for x := 1; x < len(row); x++ {
				row[x] = byte(random.Uint32())
			}
		} else {
			for x := 1; x < len(row); x++ {
				row[x] = byte(128 + y%2)
			}
		}
		_, _ = writer.Write(row)
	}
	_ = writer.Close()
	chunk("IDAT", compressed.Bytes())
	chunk("IEND", nil)
	return out.Bytes()
}
