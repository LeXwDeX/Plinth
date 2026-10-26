package local

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"testing"
)

func isolatedGLBFixture(t *testing.T, pixels []byte, mimeType string) []byte {
	t.Helper()
	positions := make([]byte, 36)
	for _, offset := range []int{12, 28} {
		binary.LittleEndian.PutUint32(positions[offset:], math.Float32bits(1))
	}
	bin := append(positions, pixels...)
	document, err := json.Marshal(map[string]any{
		"asset":   map[string]string{"version": "2.0"},
		"buffers": []any{map[string]any{"byteLength": len(bin)}},
		"bufferViews": []any{
			map[string]any{"buffer": 0, "byteOffset": 0, "byteLength": 36},
			map[string]any{"buffer": 0, "byteOffset": 36, "byteLength": len(pixels)},
		},
		"accessors": []any{map[string]any{"bufferView": 0, "componentType": 5126, "count": 3, "type": "VEC3"}},
		"images":    []any{map[string]any{"bufferView": 1, "mimeType": mimeType}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return glbFixtureWithBIN(string(document), bin)
}

func glbFixtureWithBIN(document string, bin []byte) []byte {
	output := glbWithJSON(document)
	for len(bin)%4 != 0 {
		bin = append(bin, 0)
	}
	chunk := make([]byte, 8)
	binary.LittleEndian.PutUint32(chunk[:4], uint32(len(bin)))
	copy(chunk[4:], "BIN\x00")
	output = append(output, chunk...)
	output = append(output, bin...)
	binary.LittleEndian.PutUint32(output[8:12], uint32(len(output)))
	return output
}

func TestGLBEmbeddedRasterUsesSameDeliveryBoundary(t *testing.T) {
	for _, format := range []string{"png", "jpeg"} {
		t.Run(format, func(t *testing.T) {
			pixels := rasterNoiseFixture(t, format)
			body := isolatedGLBFixture(t, pixels, "image/"+format)
			assertIsolationBoundary(t, body, "glb", "model/gltf-binary", true)
			archive := zipModel(t, map[string]string{"scene.glb": string(body)})
			assertIsolationBoundary(t, archive, "zip", "application/zip", true)
			if format == "png" {
				pixels = rasterPNGText(t, "zTXt", []byte(" //private.example/embedded"))
			} else {
				pixels = rasterJPEGMetadata(pixels, 0xe1, []byte(" //private.example/embedded"))
			}
			// A validated embedded image is admitted whole, metadata included.
			body = isolatedGLBFixture(t, pixels, "image/"+format)
			assertIsolationBoundary(t, body, "glb", "model/gltf-binary", true)
			assertIsolationBoundary(t, zipModel(t, map[string]string{"scene.glb": string(body)}), "zip", "application/zip", true)
			// The same image does not excuse an address in the model JSON.
			// (#189 admits a plain address only in asset.generator/copyright
			// metadata, so the leak sits in extras.)
			jsonLength := int(binary.LittleEndian.Uint32(body[12:16]))
			document := bytes.Replace(bytes.TrimRight(body[20:20+jsonLength], " "), []byte(`"asset":{"version":"2.0"}`), []byte(`"asset":{"version":"2.0"},"extras":{"source":"https://private.example/model"}`), 1)
			bin := body[20+jsonLength+8:]
			leaked := glbFixtureWithBIN(string(document), bin)
			assertIsolationBoundary(t, leaked, "glb", "model/gltf-binary", false)
			assertIsolationBoundary(t, zipModel(t, map[string]string{"scene.glb": string(leaked)}), "zip", "application/zip", false)
		})
	}
}

func TestGLBGeometryWaiverRequiresValidatedAccessor(t *testing.T) {
	bin := make([]byte, 36)
	binary.LittleEndian.PutUint32(bin[:4], 0x20612f2f) // finite FLOAT bit pattern //a
	const base = `{"asset":{"version":"2.0"},"buffers":[{"byteLength":36}],"bufferViews":[{"buffer":0,"byteLength":36}],"accessors":[{"bufferView":0,"componentType":5126,"count":3,"type":"VEC3"}]}`
	assertIsolationBoundary(t, glbFixtureWithBIN(base, bin), "glb", "model/gltf-binary", true)
	const unreferenced = `{"asset":{"version":"2.0"},"buffers":[{"byteLength":36}],"bufferViews":[{"buffer":0,"byteLength":36}]}`
	assertIsolationBoundary(t, glbFixtureWithBIN(unreferenced, bin), "glb", "model/gltf-binary", false)
	for _, replacement := range []struct{ before, after string }{
		{`"count":3`, `"count":4`},
		{`"componentType":5126`, `"componentType":5127`},
		{`"bufferView":0`, `"bufferView":1`},
		{`"type":"VEC3"`, `"type":"TEXT"`},
	} {
		body := bytes.Replace([]byte(base), []byte(replacement.before), []byte(replacement.after), 1)
		assertIsolationBoundary(t, glbFixtureWithBIN(string(body), bin), "glb", "model/gltf-binary", false)
	}
	for _, value := range []uint32{0x7f800000, 0xff800000, 0x7fc00000} {
		invalid := append([]byte(nil), bin...)
		binary.LittleEndian.PutUint32(invalid[12:16], value)
		assertIsolationBoundary(t, glbFixtureWithBIN(base, invalid), "glb", "model/gltf-binary", false)
	}
	absolute := append([]byte(nil), bin...)
	copy(absolute, "https://private.example/model")
	assertIsolationBoundary(t, glbFixtureWithBIN(base, absolute), "glb", "model/gltf-binary", false)
}

func TestGLBFullFramingAndUnknownFieldsRemainChecked(t *testing.T) {
	base := glbWithJSON(`{"asset":{"version":"2.0"}}`)
	for _, body := range [][]byte{
		append(append([]byte(nil), base...), []byte("tail")...),
		base[:len(base)-1],
		glbWithJSON(`{"asset":{"version":"2.0"}} {}`),
		glbWithJSON(`{"asset":{"version":"2.0"},"extras":{"source":" //localhost/model"}}`),
		glbFixtureWithBIN(`{"asset":{"version":"2.0"},"buffers":[{"byteLength":16}]}`, []byte(" //localhost/x ")),
	} {
		assertIsolationBoundary(t, body, "glb", "model/gltf-binary", false)
	}
	pixels := rasterPNGText(t, "tEXt", []byte("local image"))
	valid := isolatedGLBFixture(t, pixels, "image/png")
	for _, offset := range []int{12, 20 + int(binary.LittleEndian.Uint32(valid[12:16]))} {
		malformed := append([]byte(nil), valid...)
		binary.LittleEndian.PutUint32(malformed[offset:offset+4], ^uint32(0))
		assertIsolationBoundary(t, malformed, "glb", "model/gltf-binary", false)
	}
	assertIsolationBoundary(t, isolatedGLBFixture(t, pixels, "image/jpeg"), "glb", "model/gltf-binary", false)
}

type fieldShortReader struct {
	*bytes.Reader
	chunk int
}

func (reader fieldShortReader) Read(output []byte) (int, error) {
	return reader.Reader.Read(output[:min(reader.chunk, len(output))])
}

func TestTypedNumericFieldsPreserveBoundariesAndRejectNonfiniteValues(t *testing.T) {
	for _, chunk := range []int{1, 3, 65535, isolationChunkBytes} {
		start := 65532
		if chunk <= 3 {
			start = 4
		}
		body := make([]byte, start+28)
		binary.LittleEndian.PutUint32(body[start:start+4], 0x20612f2f)
		plan := inspectionPlan{fields: []inspectionField{{int64(start), 12, 12, 2, true}}}
		reader := fieldShortReader{bytes.NewReader(body), chunk}
		if err := inspectPlannedBytes(reader, plan); err != nil {
			t.Fatal("numeric field falsely interpreted as an address")
		}
		binary.LittleEndian.PutUint32(body[start+4:start+8], 0x7f800000)
		reader = fieldShortReader{bytes.NewReader(body), chunk}
		if err := inspectPlannedBytes(reader, plan); err == nil {
			t.Fatal("nonfinite value crossing an input boundary accepted")
		}
	}
}

func glbUnknownChunkFixture(kind string, payload []byte) []byte {
	body := glbWithJSON(`{"asset":{"version":"2.0"}}`)
	payload = append([]byte(nil), payload...)
	for len(payload)%4 != 0 {
		payload = append(payload, ' ')
	}
	chunk := make([]byte, 8)
	binary.LittleEndian.PutUint32(chunk[:4], uint32(len(payload)))
	copy(chunk[4:], kind)
	body = append(body, chunk...)
	body = append(body, payload...)
	binary.LittleEndian.PutUint32(body[8:12], uint32(len(body)))
	return body
}

func TestGLBUnknownChunkUsesItsOwnBodyBoundary(t *testing.T) {
	for _, test := range []struct {
		name, kind, payload string
		accepted            bool
	}{
		{"safe", "XXXX", "local metadata", true},
		{"index_token", "XXXX", "vertex=1//2", true},
		{"raw", "XXXX", "//provider.example/private", false},
		{"escaped", "XXXX", `\/\/provider.example/private`, false},
		{"percent", "XXXX", "%2F%2F127.0.0.1/private", false},
		{"utf16", "XXXX", "\x00/\x00/\x00a\x00/\x00b", false},
		{"window_boundary", "XXXX", string(bytes.Repeat([]byte{' '}, isolationChunkBytes-3)) + "//provider.example/private", false},
		{"type_to_body", "http", "s://provider.example/private", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := glbUnknownChunkFixture(test.kind, []byte(test.payload))
			assertIsolationBoundary(t, body, "glb", "model/gltf-binary", test.accepted)
			assertIsolationBoundary(t, zipModel(t, map[string]string{"scene.glb": string(body)}), "zip", "application/zip", test.accepted)
		})
	}
}
