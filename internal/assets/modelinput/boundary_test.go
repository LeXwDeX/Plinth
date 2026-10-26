package modelinput_test

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
)

func expectKind(t *testing.T, raw []byte, p modelinput.Policy, kind modelinput.Kind) {
	t.Helper()
	_, err := validate(t, raw, p)
	var e *modelinput.Error
	if !errors.As(err, &e) || e.Kind != kind {
		t.Fatalf("want %s; got %v", kind, err)
	}
}
func TestFramingAndUniqueJSONBoundaries(t *testing.T) {
	raw := modelinputtest.Triangle(modelinputtest.Options{})
	doc, bin := modelinputtest.Document(modelinputtest.Options{})
	j, _ := json.Marshal(doc)
	for _, name := range []string{"header", "version", "length", "truncated", "json_type", "bin_type", "third_chunk", "duplicate", "trailing", "utf8"} {
		t.Run(name, func(t *testing.T) {
			b := append([]byte{}, raw...)
			switch name {
			case "header":
				b[0] = 'x'
			case "version":
				binary.LittleEndian.PutUint32(b[4:], 1)
			case "length":
				binary.LittleEndian.PutUint32(b[8:], uint32(len(b)-4))
			case "truncated":
				b = b[:len(b)-1]
			case "json_type":
				copy(b[16:], "BIN\x00")
			case "bin_type":
				off := 20 + int(binary.LittleEndian.Uint32(b[12:]))
				copy(b[off+4:], "JUNK")
			case "third_chunk":
				b = append(b, 0, 0, 0, 0, 'J', 'U', 'N', 'K')
				binary.LittleEndian.PutUint32(b[8:], uint32(len(b)))
			case "duplicate":
				b = modelinputtest.Frame(bytes.Replace(j, []byte(`"version":"2.0"`), []byte(`"version":"2.0","version":"2.0"`), 1), bin)
			case "trailing":
				b = modelinputtest.Frame(append(j, []byte(` {}`)...), bin)
			case "utf8":
				b = modelinputtest.Frame(bytes.Replace(j, []byte(`"2.0"`), []byte{'"', 0xff, '"'}, 1), bin)
			}
			expectKind(t, b, policy(), modelinput.Invalid)
		})
	}
}
func TestCrossReferencesAndRanges(t *testing.T) {
	cases := map[string]func(map[string]any, []byte){
		"buffer_index":       func(d map[string]any, b []byte) { d["bufferViews"].([]any)[0].(map[string]any)["buffer"] = 1 },
		"view_offset":        func(d map[string]any, b []byte) { d["bufferViews"].([]any)[0].(map[string]any)["byteOffset"] = len(b) },
		"accessor_reference": func(d map[string]any, b []byte) { d["accessors"].([]any)[0].(map[string]any)["bufferView"] = 99 },
		"accessor_alignment": func(d map[string]any, b []byte) { d["accessors"].([]any)[0].(map[string]any)["byteOffset"] = 1 },
		"stride":             func(d map[string]any, b []byte) { d["bufferViews"].([]any)[0].(map[string]any)["byteStride"] = 4 },
		"position_bounds": func(d map[string]any, b []byte) {
			d["accessors"].([]any)[0].(map[string]any)["max"] = []float64{2, 2, 2}
		},
		"restart_index":     func(d map[string]any, b []byte) { binary.LittleEndian.PutUint16(b[36:], 65535) },
		"image_reference":   func(d map[string]any, b []byte) { d["images"].([]any)[0].(map[string]any)["bufferView"] = 999 },
		"image_type":        func(d map[string]any, b []byte) { d["images"].([]any)[0].(map[string]any)["mimeType"] = "image/jpeg" },
		"texture_reference": func(d map[string]any, b []byte) { d["textures"].([]any)[0].(map[string]any)["source"] = 999 },
		"sampler_reference": func(d map[string]any, b []byte) { d["textures"].([]any)[0].(map[string]any)["sampler"] = 0 },
		"material_reference": func(d map[string]any, b []byte) {
			d["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)["material"] = 999
		},
		"texture_coordinate": func(d map[string]any, b []byte) {
			d["materials"].([]any)[0].(map[string]any)["pbrMetallicRoughness"].(map[string]any)["baseColorTexture"].(map[string]any)["texCoord"] = 1
		},
		"node_child":      func(d map[string]any, b []byte) { d["nodes"].([]any)[0].(map[string]any)["children"] = []any{99} },
		"scene_reference": func(d map[string]any, b []byte) { d["scene"] = 999 },
		"scene_root":      func(d map[string]any, b []byte) { d["scenes"].([]any)[0].(map[string]any)["nodes"] = []any{999} },
		"transform": func(d map[string]any, b []byte) {
			n := d["nodes"].([]any)[0].(map[string]any)
			n["matrix"] = []int{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
			n["translation"] = []int{0, 0, 0}
		},
		"huge_transform": func(d map[string]any, b []byte) {
			d["nodes"].([]any)[0].(map[string]any)["translation"] = []float64{1e300, 0, 0}
		},
		"rotation": func(d map[string]any, b []byte) {
			d["nodes"].([]any)[0].(map[string]any)["rotation"] = []int{1, 1, 1, 1}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d, b := modelinputtest.Document(modelinputtest.Options{Indexed: true, Textured: true})
			mutate(d, b)
			expectKind(t, modelinputtest.GLB(d, b), policy(), modelinput.Invalid)
		})
	}
}
func replaceFixtureImage(t *testing.T, doc map[string]any, bin []byte, size int) []byte {
	t.Helper()
	views := doc["bufferViews"].([]any)
	view := views[len(views)-1].(map[string]any)
	off := view["byteOffset"].(int)
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewNRGBA(image.Rect(0, 0, size, size))); err != nil {
		t.Fatal(err)
	}
	bin = append(append([]byte{}, bin[:off]...), imageBytes.Bytes()...)
	view["byteLength"] = imageBytes.Len()
	doc["buffers"].([]any)[0].(map[string]any)["byteLength"] = len(bin)
	return bin
}
func TestExplicitNodeAndImageLimits(t *testing.T) {
	d, b := modelinputtest.Document(modelinputtest.Options{Textured: true})
	b = replaceFixtureImage(t, d, b, 2)
	p := policy()
	p.Limits.MaxImagePixels = 3
	expectKind(t, modelinputtest.GLB(d, b), p, modelinput.Limit)
	p = policy()
	p.Limits.MaxImagePixels = 4
	p.Limits.MaxTotalImagePixels = 4
	// Two distinct images each fit the single-image limit but exceed the total.
	view := d["bufferViews"].([]any)[len(d["bufferViews"].([]any))-1].(map[string]any)
	start := view["byteOffset"].(int)
	length := view["byteLength"].(int)
	copyImage := append([]byte{}, b[start:start+length]...)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	views := d["bufferViews"].([]any)
	d["bufferViews"] = append(views, map[string]any{"buffer": 0, "byteOffset": len(b), "byteLength": length})
	b = append(b, copyImage...)
	d["buffers"].([]any)[0].(map[string]any)["byteLength"] = len(b)
	d["images"] = append(d["images"].([]any), map[string]any{"bufferView": len(views), "mimeType": "image/png"})
	expectKind(t, modelinputtest.GLB(d, b), p, modelinput.Limit)
	d, b = modelinputtest.Document(modelinputtest.Options{})
	d["nodes"] = append(d["nodes"].([]any), map[string]any{})
	p = policy()
	p.Limits.MaxNodes = 1
	expectKind(t, modelinputtest.GLB(d, b), p, modelinput.Limit)
}
func TestHasTexturesRequiresActiveMeshReference(t *testing.T) {
	d, b := modelinputtest.Document(modelinputtest.Options{Textured: true})
	d["meshes"] = append(d["meshes"].([]any), map[string]any{"primitives": []any{map[string]any{"attributes": map[string]any{"POSITION": 0}}}})
	d["nodes"].([]any)[0].(map[string]any)["mesh"] = 1
	p, err := validate(t, modelinputtest.GLB(d, b), policy())
	if err != nil {
		t.Fatal(err)
	}
	if p.HasTextures {
		t.Fatal("unreferenced textured mesh qualified the active untextured scene")
	}
}
func TestProfileIdentityAndFileOwnership(t *testing.T) {
	raw := modelinputtest.Triangle(modelinputtest.Options{Textured: true})
	path := filepath.Join(t.TempDir(), "triangle.glb")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	p, err := modelinput.ValidateGLB(context.Background(), file, policy())
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if p.SHA256 != hex.EncodeToString(sum[:]) || p.Size != int64(len(raw)) || p.Revision != "1" {
		t.Fatalf("identity=%+v", p)
	}
	again, err := io.ReadAll(file)
	if err != nil || !bytes.Equal(again, raw) {
		t.Fatal("validator closed or failed to rewind caller file")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = modelinput.ValidateGLB(ctx, file, policy())
	var e *modelinput.Error
	if !errors.As(err, &e) || e.Kind != modelinput.Canceled {
		t.Fatalf("cancel=%v", err)
	}
}
func TestPolicyAndFailureEvidence(t *testing.T) {
	p := policy()
	p.Revision = "unknown"
	if p.Validate() == nil {
		t.Fatal("unknown wire revision silently admitted")
	}
	p = policy()
	p.Limits.MaxBytes = math.MaxInt64
	if p.Validate() == nil {
		t.Fatal("unsafe policy silently clamped")
	}
	d, b := modelinputtest.Document(modelinputtest.Options{})
	d["buffers"].([]any)[0].(map[string]any)["uri"] = "https://PRIVATE_TOKEN.invalid/PRIVATE_PATH?key=PRIVATE_SECRET"
	_, err := validate(t, modelinputtest.GLB(d, b), policy())
	if err == nil || strings.Contains(err.Error(), "PRIVATE_") {
		t.Fatalf("unsafe evidence: %v", err)
	}
}

func TestNormalizedIntegerAttributeBoundsAndJPEG(t *testing.T) {
	d, b := modelinputtest.Document(modelinputtest.Options{})
	views := d["bufferViews"].([]any)
	accessors := d["accessors"].([]any)
	views = append(views, map[string]any{"buffer": 0, "byteOffset": len(b), "byteLength": 12, "target": 34962, "byteStride": 4})
	b = append(b, 255, 0, 0, 255, 0, 255, 0, 255, 0, 0, 255, 255)
	accessors = append(accessors, map[string]any{"bufferView": len(views) - 1, "componentType": 5121, "normalized": true, "count": 3, "type": "VEC4", "min": []int{0, 0, 0, 255}, "max": []int{255, 255, 255, 255}})
	d["bufferViews"] = views
	d["accessors"] = accessors
	d["buffers"].([]any)[0].(map[string]any)["byteLength"] = len(b)
	d["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)["attributes"].(map[string]any)["COLOR_0"] = len(accessors) - 1
	if _, err := validate(t, modelinputtest.GLB(d, b), policy()); err != nil {
		t.Fatalf("normalized stored-value bounds rejected: %v", err)
	}
	d, b = modelinputtest.Document(modelinputtest.Options{Textured: true})
	view := d["bufferViews"].([]any)[len(d["bufferViews"].([]any))-1].(map[string]any)
	offset := view["byteOffset"].(int)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	b = append(append([]byte{}, b[:offset]...), encoded.Bytes()...)
	view["byteLength"] = encoded.Len()
	d["buffers"].([]any)[0].(map[string]any)["byteLength"] = len(b)
	d["images"].([]any)[0].(map[string]any)["mimeType"] = "image/jpeg"
	if p, err := validate(t, modelinputtest.GLB(d, b), policy()); err != nil || !p.HasTextures {
		t.Fatalf("valid embedded JPEG rejected: %+v %v", p, err)
	}
}
func TestUnitNormalAccessorWorkIsNotMultipliedByPrimitiveReuse(t *testing.T) {
	d, b := modelinputtest.Document(modelinputtest.Options{Interleaved: true})
	triangle := append([]byte{}, b...)
	for i := 1; i < 3000; i++ {
		b = append(b, triangle...)
	}
	d["bufferViews"].([]any)[0].(map[string]any)["byteLength"] = len(b)
	d["buffers"].([]any)[0].(map[string]any)["byteLength"] = len(b)
	for _, a := range d["accessors"].([]any) {
		a.(map[string]any)["count"] = 9000
	}
	primitives := d["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)
	primitive := primitives[0]
	for i := 0; i < 100; i++ {
		primitives = append(primitives, primitive)
	}
	d["meshes"].([]any)[0].(map[string]any)["primitives"] = primitives
	// The quota admits one geometry decode and normal check, plus wire/JSON.
	// Rechecking the same 9000 normals for all 101 primitives would exceed it.
	raw := modelinputtest.GLB(d, b)
	p := policy()
	p.Limits.MaxWork = 4*int64(len(raw)) + 100000
	if result, err := validate(t, raw, p); err != nil || result.Triangles != 303000 {
		t.Fatalf("shared geometry work rejected: %+v %v", result, err)
	}
}
func TestMalformedSurrogateAndNonAffineMatrix(t *testing.T) {
	d, b := modelinputtest.Document(modelinputtest.Options{})
	j, _ := json.Marshal(d)
	j = bytes.Replace(j, []byte(`"version":"2.0"`), []byte(`"version":"2.0","generator":"\ud800"`), 1)
	expectKind(t, modelinputtest.Frame(j, b), policy(), modelinput.Invalid)
	d, b = modelinputtest.Document(modelinputtest.Options{})
	d["nodes"].([]any)[0].(map[string]any)["matrix"] = []int{1, 0, 0, 1, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
	expectKind(t, modelinputtest.GLB(d, b), policy(), modelinput.Invalid)
}
func FuzzProfileJSONNeverPanics(f *testing.F) {
	d, b := modelinputtest.Document(modelinputtest.Options{Indexed: true, Textured: true})
	raw, _ := json.Marshal(d)
	f.Add(raw)
	f.Add([]byte(`{"asset":null}`))
	f.Add([]byte(`{"asset":{"version":"2.0"},"buffers":[null]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 256<<10 {
			return
		}
		_, _ = validate(t, modelinputtest.Frame(data, b), policy())
	})
}

func TestNearUploadLimitContainsRealNonrepeatingTexture(t *testing.T) {
	d, b := modelinputtest.Document(modelinputtest.Options{Textured: true})
	picture := image.NewNRGBA(image.Rect(0, 0, 1800, 1800))
	rng := rand.New(rand.NewPCG(17, 29))
	for i := 0; i < len(picture.Pix); i += 4 {
		n := rng.Uint64()
		picture.Pix[i] = byte(n)
		picture.Pix[i+1] = byte(n >> 8)
		picture.Pix[i+2] = byte(n >> 16)
		picture.Pix[i+3] = 255
	}
	var encoded bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.NoCompression}
	if err := encoder.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	views := d["bufferViews"].([]any)
	view := views[len(views)-1].(map[string]any)
	offset := view["byteOffset"].(int)
	b = append(append([]byte{}, b[:offset]...), encoded.Bytes()...)
	view["byteLength"] = encoded.Len()
	d["buffers"].([]any)[0].(map[string]any)["byteLength"] = len(b)
	raw := modelinputtest.GLB(d, b)
	if len(raw) < 9<<20 || len(raw) > 10<<20 {
		t.Fatalf("fixture is not near the actual 10MiB limit: %d", len(raw))
	}
	p := policy()
	p.Limits.MaxImagePixels = 16777216
	p.Limits.MaxTotalImagePixels = 33554432
	p.Limits.MaxWork = 128 << 20
	profile, err := validate(t, raw, p)
	if err != nil || !profile.HasTextures || profile.Size != int64(len(raw)) {
		t.Fatalf("real large texture rejected: %+v %v", profile, err)
	}
	p.Limits.MaxWork = 4 * int64(len(raw))
	expectKind(t, raw, p, modelinput.Limit)
}
func invalidLargeRaster() []byte {
	var body bytes.Buffer
	writer := zlib.NewWriter(&body)
	_, _ = writer.Write([]byte{0, 0, 0, 0, 0})
	_ = writer.Close()
	result := []byte("\x89PNG\r\n\x1a\n")
	chunk := func(kind string, raw []byte) {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(raw)))
		result = append(result, size[:]...)
		result = append(result, kind...)
		result = append(result, raw...)
		binary.BigEndian.PutUint32(size[:], crc32.ChecksumIEEE(append([]byte(kind), raw...)))
		result = append(result, size[:]...)
	}
	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header, 4096)
	binary.BigEndian.PutUint32(header[4:], 4096)
	header[8] = 8
	header[9] = 6
	chunk("IHDR", header)
	chunk("IDAT", body.Bytes())
	chunk("IEND", nil)
	return result
}
func TestLargeTextureStillRequiresCompletePixelStream(t *testing.T) {
	d, b := modelinputtest.Document(modelinputtest.Options{Textured: true})
	views := d["bufferViews"].([]any)
	view := views[len(views)-1].(map[string]any)
	offset := view["byteOffset"].(int)
	imageBytes := invalidLargeRaster()
	b = append(append([]byte{}, b[:offset]...), imageBytes...)
	view["byteLength"] = len(imageBytes)
	d["buffers"].([]any)[0].(map[string]any)["byteLength"] = len(b)
	p := policy()
	p.Limits.MaxImagePixels = 16777216
	p.Limits.MaxTotalImagePixels = 33554432
	p.Limits.MaxWork = 128 << 20
	expectKind(t, modelinputtest.GLB(d, b), p, modelinput.Invalid)
}
