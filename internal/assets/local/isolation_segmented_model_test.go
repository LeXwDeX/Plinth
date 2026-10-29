package local

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	stdjpeg "image/jpeg"
	"image/png"
	"math"
	"testing"
)

// segmentedModelGLB builds the layout of a Tripo Smart Segmentation result
// (#94): one node, mesh and material per part, and three embedded textures per
// part (normal and base color as JPEG, metallic-roughness as PNG), each in its
// own bufferView. The real 127-part result carried 381 images in 4.4 MB.
func segmentedModelGLB(t *testing.T, parts int) []byte {
	t.Helper()
	pixels := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			pixels.Set(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 8), B: 96, A: 255})
		}
	}
	var jpegBytes, pngBytes bytes.Buffer
	if err := stdjpeg.Encode(&jpegBytes, pixels, &stdjpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&pngBytes, pixels); err != nil {
		t.Fatal(err)
	}
	pad := func(b []byte) []byte {
		for len(b)%4 != 0 {
			b = append(b, ' ')
		}
		return b
	}
	positions := make([]byte, 36)
	for _, offset := range []int{12, 28} {
		binary.LittleEndian.PutUint32(positions[offset:], math.Float32bits(1))
	}
	var bin []byte
	var views, accessors, images, textures, materials, meshes, nodes, children []any
	addView := func(body []byte) int {
		body = pad(body)
		views = append(views, map[string]any{"buffer": 0, "byteOffset": len(bin), "byteLength": len(body)})
		bin = append(bin, body...)
		return len(views) - 1
	}
	for part := 0; part < parts; part++ {
		if part == 0 { // One shared accessor keeps the numeric field count low.
			accessors = append(accessors, map[string]any{"bufferView": addView(positions), "componentType": 5126, "count": 3, "type": "VEC3"})
		}
		first := len(textures)
		for _, texture := range []struct {
			mime string
			body []byte
		}{{"image/jpeg", jpegBytes.Bytes()}, {"image/jpeg", jpegBytes.Bytes()}, {"image/png", pngBytes.Bytes()}} {
			images = append(images, map[string]any{"bufferView": addView(texture.body), "mimeType": texture.mime})
			textures = append(textures, map[string]any{"source": len(images) - 1})
		}
		materials = append(materials, map[string]any{
			"normalTexture":        map[string]any{"index": first},
			"pbrMetallicRoughness": map[string]any{"baseColorTexture": map[string]any{"index": first + 1}, "metallicRoughnessTexture": map[string]any{"index": first + 2}},
		})
		meshes = append(meshes, map[string]any{"primitives": []any{map[string]any{"attributes": map[string]any{"POSITION": 0}, "material": part}}})
		nodes = append(nodes, map[string]any{"mesh": part})
		children = append(children, part)
	}
	document, err := json.Marshal(map[string]any{
		"asset":       map[string]string{"version": "2.0", "generator": "Tripo"},
		"scene":       0,
		"scenes":      []any{map[string]any{"nodes": children}},
		"nodes":       nodes,
		"meshes":      meshes,
		"materials":   materials,
		"textures":    textures,
		"images":      images,
		"accessors":   accessors,
		"bufferViews": views,
		"buffers":     []any{map[string]any{"byteLength": len(bin)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return glbFixtureWithBIN(string(document), bin)
}

// A segmented model with 127 parts and 381 textures is a legitimate result,
// not an inspection-work abuse: it is delivered as a GLB and inside the
// archives that carry it (#94).
func TestSegmentedModelWithManyEmbeddedTexturesIsDelivered(t *testing.T) {
	body := segmentedModelGLB(t, 127)
	assertIsolationBoundary(t, body, "glb", "model/gltf-binary", true)
	assertIsolationBoundary(t, zipModel(t, map[string]string{"scene.glb": string(body)}), "zip", "application/zip", true)
}

// Every image still carries a fixed allowance, so a file made of an excessive
// number of tiny images stays bounded by the work budget.
func TestModelWithExcessiveImageCountIsStillRejectedForWork(t *testing.T) {
	body := segmentedModelGLB(t, 2800) // 8400 images, more than the 512 MiB floor can pay for
	assertIsolationBoundary(t, body, "glb", "model/gltf-binary", false)
}
