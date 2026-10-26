// Package modelinputtest builds real, nonempty static triangle GLBs for tests.
package modelinputtest

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/png"
	"math"
)

type Options struct{ Indexed, Interleaved, Textured, MultiplePrimitives bool }

func Document(options Options) (map[string]any, []byte) {
	var bin []byte
	appendFloat := func(value float32) {
		var raw [4]byte
		binary.LittleEndian.PutUint32(raw[:], math.Float32bits(value))
		bin = append(bin, raw[:]...)
	}
	positions := [][3]float32{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}}
	for _, p := range positions {
		for _, v := range p {
			appendFloat(v)
		}
		if options.Interleaved {
			appendFloat(0)
			appendFloat(0)
			appendFloat(1)
		}
	}
	view := map[string]any{"buffer": 0, "byteOffset": 0, "byteLength": len(bin), "target": 34962}
	if options.Interleaved {
		view["byteStride"] = 24
	}
	views := []any{view}
	accessors := []any{map[string]any{"bufferView": 0, "componentType": 5126, "count": 3, "type": "VEC3", "min": []float64{0, 0, 0}, "max": []float64{1, 1, 0}}}
	attrs := map[string]any{"POSITION": 0}
	if options.Interleaved {
		accessors = append(accessors, map[string]any{"bufferView": 0, "byteOffset": 12, "componentType": 5126, "count": 3, "type": "VEC3"})
		attrs["NORMAL"] = 1
	}
	primitive := map[string]any{"attributes": attrs, "mode": 4}
	if options.Indexed {
		views = append(views, map[string]any{"buffer": 0, "byteOffset": len(bin), "byteLength": 6, "target": 34963})
		bin = append(bin, 0, 0, 1, 0, 2, 0, 0, 0)
		primitive["indices"] = len(accessors)
		accessors = append(accessors, map[string]any{"bufferView": len(views) - 1, "componentType": 5123, "count": 3, "type": "SCALAR"})
	}
	doc := map[string]any{"asset": map[string]any{"version": "2.0"}, "scene": 0, "scenes": []any{map[string]any{"nodes": []any{0}}}, "nodes": []any{map[string]any{"mesh": 0}}}
	if options.Textured {
		start := len(bin)
		for _, v := range []float32{0, 0, 1, 0, 0, 1} {
			appendFloat(v)
		}
		views = append(views, map[string]any{"buffer": 0, "byteOffset": start, "byteLength": 24, "target": 34962})
		attrs["TEXCOORD_0"] = len(accessors)
		accessors = append(accessors, map[string]any{"bufferView": len(views) - 1, "componentType": 5126, "count": 3, "type": "VEC2"})
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
			panic(err)
		}
		views = append(views, map[string]any{"buffer": 0, "byteOffset": len(bin), "byteLength": encoded.Len()})
		bin = append(bin, encoded.Bytes()...)
		doc["images"] = []any{map[string]any{"bufferView": len(views) - 1, "mimeType": "image/png"}}
		doc["textures"] = []any{map[string]any{"source": 0}}
		doc["materials"] = []any{map[string]any{"pbrMetallicRoughness": map[string]any{"baseColorTexture": map[string]any{"index": 0}}}}
		primitive["material"] = 0
	}
	primitives := []any{primitive}
	if options.MultiplePrimitives {
		primitives = append(primitives, primitive)
	}
	doc["meshes"] = []any{map[string]any{"primitives": primitives}}
	doc["bufferViews"] = views
	doc["accessors"] = accessors
	doc["buffers"] = []any{map[string]any{"byteLength": len(bin)}}
	return doc, bin
}
func GLB(doc map[string]any, bin []byte) []byte {
	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return Frame(raw, bin)
}
func Frame(raw, bin []byte) []byte {
	raw = append([]byte{}, raw...)
	bin = append([]byte{}, bin...)
	for len(raw)%4 != 0 {
		raw = append(raw, ' ')
	}
	for len(bin)%4 != 0 {
		bin = append(bin, 0)
	}
	result := make([]byte, 12)
	copy(result, "glTF")
	binary.LittleEndian.PutUint32(result[4:], 2)
	binary.LittleEndian.PutUint32(result[8:], uint32(28+len(raw)+len(bin)))
	add := func(kind string, body []byte) {
		var header [8]byte
		binary.LittleEndian.PutUint32(header[:], uint32(len(body)))
		copy(header[4:], kind)
		result = append(result, header[:]...)
		result = append(result, body...)
	}
	add("JSON", raw)
	add("BIN\x00", bin)
	return result
}
func Triangle(options Options) []byte { doc, bin := Document(options); return GLB(doc, bin) }
