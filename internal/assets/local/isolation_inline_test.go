package local

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestInlineImagesUseAllDeliveryBoundaries(t *testing.T) {
	plainPNG := inlineTestPNG(t, "synth")
	privatePNG := inlineTestPNG(t, "https://provider.example/private?token=synth")
	// Images are cached as delivered, metadata included: the standalone PNG
	// and the same bytes embedded in glTF share one decision at admission and
	// at historical reads (Lex, 2026-10-09).
	t.Run("standalone_private_png", func(t *testing.T) {
		inlineTestBoundary(t, privatePNG, "png", "image/png", "image", true)
	})
	var jpegBody bytes.Buffer
	if err := jpeg.Encode(&jpegBody, image.NewRGBA(image.Rect(0, 0, 1, 1)), nil); err != nil {
		t.Fatal("failed to build JPEG fixture")
	}
	cases := []struct {
		name, uri, mime string
		accepted        bool
	}{
		{"png_base64", inlineTestURI(plainPNG, "image/png", false), "image/png", true},
		{"png_percent", inlineTestURI(plainPNG, "image/png", true), "image/png", true},
		{"jpeg_base64", inlineTestURI(jpegBody.Bytes(), "image/jpeg", false), "image/jpeg", true},
		{"private_http", inlineTestURI(privatePNG, "image/png", false), "image/png", true},
		{"private_percent_resource", inlineTestURI(privatePNG, "image/png", true), "image/png", true},
	}
	for _, form := range []struct{ name, text string }{
		{"protocol_relative", "//provider.example/private?token=synth"},
		{"slash_escaped", `https:\/\/provider.example\/private?token=synth`},
		{"unicode_escaped", `\u0068ttps:\u002f\u002fprovider.example/private?token=synth`},
		{"percent_encoded", `%68%74%74%70%73%3A%2F%2Fprovider.example%2Fprivate%3Ftoken%3Dsynth`},
		{"html_encoded", `https&#58;&#47;&#47;provider.example/private?token=synth`},
	} {
		cases = append(cases, struct {
			name, uri, mime string
			accepted        bool
		}{"private_" + form.name, inlineTestURI(inlineTestPNG(t, form.text), "image/png", false), "image/png", true})
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			inlineTestWrappers(t, inlineTestImageDocument(item.uri, item.mime), item.accepted)
		})
	}
}

func TestInlineBuffersDistinguishTypedNumbersFromUnknownBytes(t *testing.T) {
	private := []byte("https://provider.example/private?token=synth")
	points := make([]byte, 36)
	for index, value := range []float32{0, 0, 0, 1, 0, 0, 0, 1, 0} {
		binary.LittleEndian.PutUint32(points[index*4:], math.Float32bits(value))
	}
	// A protocol-relative byte pattern can occur in finite POSITION values.
	// Identical unreferenced bytes still receive the full address check.
	// Absolute addresses remain rejected even within declared numeric fields.
	uriBits := []byte(" //provider.example/private?token=synth")
	for len(uriBits)%12 != 0 {
		uriBits = append(uriBits, 0)
	}
	absoluteBits := append([]byte(nil), private...)
	for len(absoluteBits)%12 != 0 {
		absoluteBits = append(absoluteBits, 0)
	}
	cases := []struct {
		name     string
		document map[string]any
		accepted bool
	}{
		{"unreferenced_safe_bytes", inlineTestBufferDocument([]byte{0, 1, 2, 3}, "application/octet-stream"), true},
		{"typed_positions", inlineTestPointDocument(t, points), true},
		{"typed_uri_bit_pattern", inlineTestPointDocument(t, uriBits), true},
		{"unreferenced_uri_bit_pattern", inlineTestBufferDocument(uriBits, "application/octet-stream"), false},
		{"typed_absolute_address", inlineTestPointDocument(t, absoluteBits), false},
	}
	for _, form := range []struct {
		name string
		data []byte
	}{
		{"raw", private},
		{"protocol_relative", []byte("//provider.example/private?token=synth")},
		{"slash_escaped", []byte(`https:\/\/provider.example\/private?token=synth`)},
		{"unicode_escaped", []byte(`\u0068ttps:\u002f\u002fprovider.example/private?token=synth`)},
		{"percent_encoded", []byte(`%68%74%74%70%73%3A%2F%2Fprovider.example%2Fprivate%3Ftoken%3Dsynth`)},
		{"html_encoded", []byte(`https&#58;&#47;&#47;provider.example/private?token=synth`)},
		{"utf16_le", inlineTestUTF16(private, true)},
		{"utf16_be", inlineTestUTF16(private, false)},
	} {
		cases = append(cases, struct {
			name     string
			document map[string]any
			accepted bool
		}{"private_" + form.name, inlineTestBufferDocument(form.data, "application/octet-stream"), false})
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			inlineTestWrappers(t, item.document, item.accepted)
		})
	}
}

func TestInlineResourcesRejectInvalidEncodingAndDeclarations(t *testing.T) {
	pngBytes := inlineTestPNG(t, "synth")
	pngURI := inlineTestURI(pngBytes, "image/png", false)
	cases := []struct {
		name     string
		document map[string]any
	}{
		{"base64_alphabet", inlineTestImageDocument("data:image/png;base64,@@@@", "image/png")},
		{"base64_padding", inlineTestImageDocument("data:image/png;base64,YQ=", "image/png")},
		{"base64_noncanonical_tail", inlineTestImageDocument("data:image/png;base64,AB==", "image/png")},
		{"base64_whitespace", inlineTestImageDocument("data:image/png;base64,YQ==\n", "image/png")},
		{"missing_separator", inlineTestImageDocument("data:image/png;base64", "image/png")},
		{"invalid_percent", inlineTestImageDocument("data:image/png,%GG", "image/png")},
		{"empty_content", inlineTestImageDocument("data:image/png;base64,", "image/png")},
		{"mime_conflict", inlineTestImageDocument(pngURI, "image/jpeg")},
		{"magic_conflict", inlineTestImageDocument(inlineTestURI(pngBytes, "image/jpeg", false), "image/jpeg")},
		{"nonimage_resource", inlineTestImageDocument(inlineTestURI([]byte{1, 2, 3, 4}, "application/octet-stream", false), "")},
		{"private_header_parameter", inlineTestImageDocument(`data:image/png;source="https://provider.example/private?token=synth";base64,`+base64.StdEncoding.EncodeToString(pngBytes), "image/png")},
	}
	for _, item := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"buffer_length_mismatch", func(doc map[string]any) { doc["buffers"].([]any)[0].(map[string]any)["byteLength"] = 99 }},
		{"buffer_length_negative", func(doc map[string]any) { doc["buffers"].([]any)[0].(map[string]any)["byteLength"] = -1 }},
		{"buffer_length_fractional", func(doc map[string]any) { doc["buffers"].([]any)[0].(map[string]any)["byteLength"] = 1.5 }},
		{"buffer_length_string", func(doc map[string]any) { doc["buffers"].([]any)[0].(map[string]any)["byteLength"] = "4" }},
		{"buffer_declared_oversize", func(doc map[string]any) { doc["buffers"].([]any)[0].(map[string]any)["byteLength"] = int64(64<<20) + 1 }},
		{"buffer_uri_not_string", func(doc map[string]any) { doc["buffers"].([]any)[0].(map[string]any)["uri"] = true }},
		{"buffer_array_invalid", func(doc map[string]any) { doc["buffers"] = map[string]any{"uri": "synth"} }},
	} {
		document := inlineTestBufferDocument([]byte{0, 1, 2, 3}, "application/octet-stream")
		item.edit(document)
		cases = append(cases, struct {
			name     string
			document map[string]any
		}{item.name, document})
	}
	badView := inlineTestPointDocument(t, make([]byte, 12))
	badView["bufferViews"].([]any)[0].(map[string]any)["byteOffset"] = 16
	badCount := inlineTestPointDocument(t, make([]byte, 12))
	badCount["accessors"].([]any)[0].(map[string]any)["count"] = int64(1) << 62
	cases = append(cases, struct {
		name     string
		document map[string]any
	}{"buffer_view_outside_content", badView}, struct {
		name     string
		document map[string]any
	}{"accessor_count_overflow", badCount})
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			inlineTestWrappers(t, item.document, false)
		})
	}
}

func TestInlineModelResourcesShareRecursiveBoundary(t *testing.T) {
	for _, item := range []struct {
		name, text string
		layers     int
		accepted   bool
	}{
		{"bounded_selfcontained", "synth", 1, true},
		{"nested_private_metadata", "https://provider.example/private?token=synth", 1, true},
		{"depth_exhausted", "synth", 6, false},
	} {
		t.Run(item.name, func(t *testing.T) {
			doc := inlineTestImageDocument(inlineTestURI(inlineTestPNG(t, item.text), "image/png", false), "image/png")
			for depth := 0; depth < item.layers; depth++ {
				doc = map[string]any{
					"asset":  map[string]any{"version": "2.0"},
					"extras": map[string]any{"uri": inlineTestURI(inlineTestJSON(t, doc), "model/gltf+json", false)},
				}
			}
			inlineTestWrappers(t, doc, item.accepted)
		})
	}
	for _, text := range []string{
		"https://provider.example/private?token=synth",
		`\u0068ttps:\u002f\u002fprovider.example/private?token=synth`,
	} {
		doc := inlineTestImageDocument(inlineTestURI(inlineTestPNG(t, "synth"), "image/png", false), "image/png")
		doc["extras"] = map[string]any{"unknown_source": text}
		t.Run("unknown_field_"+inlineTestHash([]byte(text))[:8], func(t *testing.T) {
			inlineTestWrappers(t, doc, false)
		})
	}
	for _, item := range []struct {
		name, text string
		accepted   bool
	}{
		{"extension_safe_inline", "synth", true},
		{"extension_private_inline", "https://provider.example/private?token=synth", true},
	} {
		t.Run(item.name, func(t *testing.T) {
			doc := map[string]any{
				"asset": map[string]any{"version": "2.0"},
				"extensions": map[string]any{"TEST_metadata": map[string]any{
					"payload": inlineTestURI(inlineTestPNG(t, item.text), "image/png", false),
				}},
			}
			inlineTestWrappers(t, doc, item.accepted)
		})
	}
}

func TestInlineDuplicateURIKeyCannotHideEncodedBytes(t *testing.T) {
	privateURI := inlineTestURI(inlineTestPNG(t, "https://provider.example/private?token=synth"), "image/png", false)
	safeURI := inlineTestURI(inlineTestPNG(t, "synth"), "image/png", false)
	// Decoding into a map keeps only the last URI. The original delivered JSON
	// still includes both encoded resources and must inspect or reject both.
	body := []byte(fmt.Sprintf(`{"asset":{"version":"2.0"},"images":[{"uri":%q,"uri":%q,"mimeType":"image/png"}]}`, privateURI, safeURI))
	if !json.Valid(body) {
		t.Fatal("duplicate-key fixture must remain valid JSON syntax")
	}
	inlineTestByteWrappers(t, body, false)
}

// This private-budget test is omitted only from the baseline source overlay:
// the old implementation has no budget API. All public boundary tests above
// run unchanged against the exact old source to produce business red evidence.
func TestInlineInspectionBudgetRejectsBeforeDecodingAndBoundsDepth(t *testing.T) {
	for _, percent := range []bool{false, true} {
		t.Run(fmt.Sprintf("percent_%t", percent), func(t *testing.T) {
			uri := inlineTestURI([]byte{1, 2, 3, 4}, "application/octet-stream", percent)
			short := &inlineInspectionBudget{remaining: 3}
			if decoded, _, err := decodeInlineDataURI(uri, short); err == nil || len(decoded) != 0 || short.remaining != 3 {
				t.Fatal("resource exceeding remaining bytes was decoded or charged")
			}
			budget := &inlineInspectionBudget{remaining: 4}
			decoded, _, err := decodeInlineDataURI(uri, budget)
			if err != nil || !bytes.Equal(decoded, []byte{1, 2, 3, 4}) || budget.remaining != 0 {
				t.Fatal("exact remaining budget rejected valid content")
			}
			if decoded, _, err := decodeInlineDataURI(uri, budget); err == nil || len(decoded) != 0 || budget.remaining != 0 {
				t.Fatal("cumulative budget was reset between resources")
			}
		})
	}
	for _, uri := range []string{
		"data:application/octet-stream;base64,AB==",
		"data:application/octet-stream,%G0",
		"data:" + strings.Repeat("x", 4097) + ",synth",
	} {
		if decoded, _, err := decodeInlineDataURI(uri, newInlineInspectionBudget()); err == nil || len(decoded) != 0 {
			t.Fatal("invalid encoding or excessive header accepted")
		}
	}
	uri := inlineTestURI(inlineTestPNG(t, "synth"), "image/png", false)
	blocked := &inlineInspectionBudget{remaining: 64 << 20, depth: 4}
	if err := inspectInlineResource(uri, "image", "image/png", -1, blocked); err == nil || blocked.remaining != 64<<20 || blocked.depth != 4 {
		t.Fatal("exhausted recursion decoded another resource")
	}
	allowed := &inlineInspectionBudget{remaining: 64 << 20, depth: 3}
	if err := inspectInlineResource(uri, "image", "image/png", -1, allowed); err != nil || allowed.depth != 3 {
		t.Fatal("last permitted depth rejected a valid PNG or leaked depth")
	}
}

func inlineTestWrappers(t *testing.T, document map[string]any, accepted bool) {
	t.Helper()
	inlineTestByteWrappers(t, inlineTestJSON(t, document), accepted)
}

func inlineTestByteWrappers(t *testing.T, body []byte, accepted bool) {
	t.Helper()
	glb := inlineTestGLB(body)
	for _, item := range []struct {
		name, variant, mime string
		body                []byte
	}{
		{"gltf", "gltf", "model/gltf+json", body},
		{"glb", "glb", "model/gltf-binary", glb},
		{"zip_gltf", "zip", "application/zip", inlineTestZIP(t, "scene.gltf", body)},
		{"zip_glb", "zip", "application/zip", inlineTestZIP(t, "scene.glb", glb)},
	} {
		t.Run(item.name, func(t *testing.T) {
			inlineTestBoundary(t, item.body, item.variant, item.mime, "model", accepted)
		})
	}
}

func inlineTestBoundary(t *testing.T, body []byte, variant, mediaType, role string, accepted bool) {
	t.Helper()
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal("failed to create fixture store")
	}
	t.Cleanup(func() { _ = store.Close() })
	digest := inlineTestHash(body)
	t.Run("validation", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "fixture")
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal("failed to write fixture")
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal("failed to open fixture")
		}
		defer file.Close()
		err = ValidateDeliveredFile(file, role, variant, mediaType)
		inlineTestGate(t, err, accepted)
		if accepted {
			data, err := io.ReadAll(file)
			if err != nil || inlineTestHash(data) != digest {
				t.Fatal("validation changed bytes or did not reset the reader")
			}
		}
	})
	t.Run("put", func(t *testing.T) {
		asset, err := store.Put(context.Background(), generation.AssetWrite{
			AssetID: "asset-inline", OwnerID: "owner-inline", TaskID: "task-inline", AttemptID: "attempt-inline",
			Role: role, Variant: variant, MediaType: mediaType, MaxBytes: int64(len(body)) + 1, Body: bytes.NewReader(body),
		})
		inlineTestGate(t, err, accepted)
		if accepted && (asset.Size != int64(len(body)) || asset.SHA256 != digest) {
			t.Fatal("admission changed fixture bytes")
		}
	})
	t.Run("historical_open", func(t *testing.T) {
		// Raw Blob admission is only a test fixture for an old READY record.
		// It does not inspect generated assets; Open must independently enforce
		// the current content boundary, even if modern Put rejected the bytes.
		blob, err := store.PutBlob(context.Background(), bytes.NewReader(body), mediaType, int64(len(body))+1)
		if err != nil {
			t.Fatal("failed to seed historical fixture")
		}
		reader, err := store.Open(context.Background(), generation.Asset{
			Role: role, Variant: variant, MediaType: blob.MediaType, StorageKey: blob.StorageKey, Size: blob.Size, SHA256: blob.SHA256,
		})
		if reader != nil {
			defer reader.Close()
		}
		inlineTestGate(t, err, accepted)
		if accepted {
			data, err := io.ReadAll(reader)
			if err != nil || inlineTestHash(data) != digest {
				t.Fatal("historical delivery changed fixture bytes")
			}
		}
	})
}

func inlineTestGate(t *testing.T, err error, accepted bool) {
	t.Helper()
	if (err == nil) != accepted {
		if err != nil {
			t.Fatalf("expected accepted=%t, error_sha256=%s", accepted, inlineTestHash([]byte(err.Error())))
		}
		t.Fatalf("expected accepted=%t, gate accepted unsafe or invalid content", accepted)
	}
	if err != nil && (strings.Contains(err.Error(), "provider.example") || strings.Contains(err.Error(), "token=synth")) {
		t.Fatal("rejection exposed a private locator")
	}
}

func inlineTestPNG(t *testing.T, text string) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal("failed to create PNG fixture")
	}
	metadata := []byte("probe\x00" + text)
	chunk := make([]byte, len(metadata)+12)
	binary.BigEndian.PutUint32(chunk[:4], uint32(len(metadata)))
	copy(chunk[4:8], "tEXt")
	copy(chunk[8:], metadata)
	binary.BigEndian.PutUint32(chunk[len(chunk)-4:], crc32.ChecksumIEEE(chunk[4:len(chunk)-4]))
	base := out.Bytes()
	result := append([]byte(nil), base[:len(base)-12]...)
	result = append(result, chunk...)
	result = append(result, base[len(base)-12:]...)
	if _, err := png.Decode(bytes.NewReader(result)); err != nil {
		t.Fatal("metadata PNG must remain decodable with valid CRC")
	}
	return result
}

func inlineTestURI(body []byte, mediaType string, percent bool) string {
	if !percent {
		return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(body)
	}
	var encoded strings.Builder
	for _, value := range body {
		fmt.Fprintf(&encoded, "%%%02X", value)
	}
	return "data:" + mediaType + "," + encoded.String()
}

func inlineTestImageDocument(uri, mediaType string) map[string]any {
	image := map[string]any{"uri": uri}
	if mediaType != "" {
		image["mimeType"] = mediaType
	}
	return map[string]any{"asset": map[string]any{"version": "2.0"}, "images": []any{image}}
}

func inlineTestBufferDocument(body []byte, mediaType string) map[string]any {
	return map[string]any{
		"asset":   map[string]any{"version": "2.0"},
		"buffers": []any{map[string]any{"uri": inlineTestURI(body, mediaType, false), "byteLength": len(body)}},
	}
}

func inlineTestPointDocument(t *testing.T, body []byte) map[string]any {
	t.Helper()
	if len(body) == 0 || len(body)%12 != 0 {
		t.Fatal("POSITION fixture must contain complete VEC3 values")
	}
	minimum, maximum := [3]float64{}, [3]float64{}
	for offset := 0; offset < len(body); offset += 4 {
		value := float64(math.Float32frombits(binary.LittleEndian.Uint32(body[offset : offset+4])))
		if math.IsNaN(value) || math.IsInf(value, 0) {
			t.Fatal("POSITION fixture values must be finite")
		}
		axis := offset / 4 % 3
		if offset < 12 || value < minimum[axis] {
			minimum[axis] = value
		}
		if offset < 12 || value > maximum[axis] {
			maximum[axis] = value
		}
	}
	doc := inlineTestBufferDocument(body, "application/gltf-buffer")
	doc["bufferViews"] = []any{map[string]any{"buffer": 0, "byteOffset": 0, "byteLength": len(body)}}
	doc["accessors"] = []any{map[string]any{"bufferView": 0, "byteOffset": 0, "componentType": 5126, "count": len(body) / 12, "type": "VEC3", "min": minimum, "max": maximum}}
	doc["meshes"] = []any{map[string]any{"primitives": []any{map[string]any{"attributes": map[string]any{"POSITION": 0}, "mode": 0}}}}
	doc["nodes"] = []any{map[string]any{"mesh": 0}}
	doc["scenes"] = []any{map[string]any{"nodes": []int{0}}}
	doc["scene"] = 0
	return doc
}

func inlineTestJSON(t *testing.T, document map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatal("failed to serialize glTF fixture")
	}
	return body
}

func inlineTestGLB(document []byte) []byte {
	jsonBytes := append([]byte(nil), document...)
	for len(jsonBytes)%4 != 0 {
		jsonBytes = append(jsonBytes, ' ')
	}
	body := make([]byte, 20+len(jsonBytes))
	copy(body[:4], "glTF")
	binary.LittleEndian.PutUint32(body[4:8], 2)
	binary.LittleEndian.PutUint32(body[8:12], uint32(len(body)))
	binary.LittleEndian.PutUint32(body[12:16], uint32(len(jsonBytes)))
	copy(body[16:20], "JSON")
	copy(body[20:], jsonBytes)
	return body
}

func inlineTestZIP(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	archive := zip.NewWriter(&out)
	entry, err := archive.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		t.Fatal("failed to create ZIP fixture")
	}
	if _, err := entry.Write(body); err != nil {
		t.Fatal("failed to write ZIP fixture")
	}
	if err := archive.Close(); err != nil {
		t.Fatal("failed to finish ZIP fixture")
	}
	return out.Bytes()
}

func inlineTestUTF16(body []byte, little bool) []byte {
	result := make([]byte, 2*len(body))
	for index, value := range body {
		position := index * 2
		if !little {
			position++
		}
		result[position] = value
	}
	return result
}

func inlineTestHash(body []byte) string {
	value := sha256.Sum256(body)
	return hex.EncodeToString(value[:])
}

func TestInlineBufferImageViewsUseAllDeliveryBoundaries(t *testing.T) {
	plainPNG := inlineTestPNG(t, "synth")
	privatePNG := inlineTestCompressedPNG(t, strings.Repeat("https://provider.example/private?token=synth ", 64))
	if containsExternalAddress(privatePNG) {
		t.Fatal("compressed fixture must conceal the address from the outer byte scan")
	}
	t.Run("standalone_private_compressed_png", func(t *testing.T) {
		inlineTestBoundary(t, privatePNG, "png", "image/png", "image", true)
	})
	var jpegBody bytes.Buffer
	if err := jpeg.Encode(&jpegBody, image.NewRGBA(image.Rect(0, 0, 1, 1)), nil); err != nil {
		t.Fatal("failed to build JPEG fixture")
	}
	cases := []struct {
		name     string
		document map[string]any
		accepted bool
	}{
		{"private_compressed_png", inlineTestBufferImageDocument(privatePNG, "image/png", 0, 4, false), true},
		{"private_compressed_png_buffer2", inlineTestBufferImageDocument(privatePNG, "image/png", 2, 8, false), true},
		{"png_offset", inlineTestBufferImageDocument(plainPNG, "image/png", 0, 4, false), true},
		{"png_buffer2", inlineTestBufferImageDocument(plainPNG, "image/png", 2, 8, false), true},
		{"shared_png_view", inlineTestBufferImageDocument(plainPNG, "image/png", 1, 4, true), true},
		{"compressed_public_metadata", inlineTestBufferImageDocument(inlineTestCompressedPNG(t, strings.Repeat("synth ", 64)), "image/png", 2, 4, true), true},
		{"jpeg_buffer1", inlineTestBufferImageDocument(jpegBody.Bytes(), "image/jpeg", 1, 4, false), true},
		{"png_mime_jpeg", inlineTestBufferImageDocument(plainPNG, "image/jpeg", 1, 4, false), false},
		{"jpeg_mime_png", inlineTestBufferImageDocument(jpegBody.Bytes(), "image/png", 2, 4, false), false},
		{"png_mime_unknown_codec", inlineTestBufferImageDocument(privatePNG, "image/webp", 2, 4, false), false},
		{"jpeg_mime_unknown_codec", inlineTestBufferImageDocument(jpegBody.Bytes(), "image/webp", 1, 4, false), false},
	}
	for _, item := range []struct {
		name     string
		edit     func(map[string]any)
		accepted bool
	}{
		{"image_numeric_overlap", func(doc map[string]any) {
			doc["accessors"] = []any{map[string]any{"bufferView": 0, "componentType": 5121, "count": len(plainPNG), "type": "SCALAR"}}
		}, false},
		{"disjoint_numeric_prefix", func(doc map[string]any) {
			doc["bufferViews"] = append(doc["bufferViews"].([]any), map[string]any{"buffer": 1, "byteOffset": 0, "byteLength": 4})
			doc["accessors"] = []any{map[string]any{"bufferView": 1, "componentType": 5125, "count": 1, "type": "SCALAR"}}
		}, true},
		{"image_buffer_index_outside_document", func(doc map[string]any) {
			doc["bufferViews"].([]any)[0].(map[string]any)["buffer"] = len(doc["buffers"].([]any))
		}, false},
		{"image_view_outside_buffer", func(doc map[string]any) {
			buffer := doc["buffers"].([]any)[1].(map[string]any)
			doc["bufferViews"].([]any)[0].(map[string]any)["byteOffset"] = buffer["byteLength"].(int) + 1
		}, false},
		{"image_view_missing_mime", func(doc map[string]any) {
			delete(doc["images"].([]any)[0].(map[string]any), "mimeType")
		}, false},
		{"image_view_source_conflict", func(doc map[string]any) {
			doc["images"].([]any)[0].(map[string]any)["uri"] = inlineTestURI(plainPNG, "image/png", false)
		}, false},
		{"image_views_partial_overlap", func(doc map[string]any) {
			doc["bufferViews"] = append(doc["bufferViews"].([]any), map[string]any{"buffer": 1, "byteOffset": 5, "byteLength": len(plainPNG) - 1})
			doc["images"] = append(doc["images"].([]any), map[string]any{"bufferView": 1, "mimeType": "image/png"})
		}, false},
		{"unknown_tail_private_address", func(doc map[string]any) {
			body := append(make([]byte, 4), plainPNG...)
			body = append(body, []byte(" //provider.example/private?token=synth")...)
			buffer := doc["buffers"].([]any)[1].(map[string]any)
			buffer["uri"], buffer["byteLength"] = inlineTestURI(body, "application/octet-stream", false), len(body)
		}, false},
	} {
		doc := inlineTestBufferImageDocument(plainPNG, "image/png", 1, 4, false)
		item.edit(doc)
		cases = append(cases, struct {
			name     string
			document map[string]any
			accepted bool
		}{item.name, doc, item.accepted})
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			inlineTestWrappers(t, item.document, item.accepted)
		})
	}
}

func TestInlineBufferImageViewsShareResourceBudget(t *testing.T) {
	for _, bufferIndex := range []int{0, 2} {
		t.Run(fmt.Sprintf("buffer_%d", bufferIndex), func(t *testing.T) {
			doc := inlineTestBufferImageDocument(inlineTestPNG(t, "synth"), "image/png", bufferIndex, 4, true)
			var total int64
			for _, value := range doc["buffers"].([]any) {
				total += int64(value.(map[string]any)["byteLength"].(int))
			}
			data := inlineTestJSON(t, doc)
			// Decoded buffers share one budget. Two images may point to one
			// view without decoding or charging that buffer a second time.
			allowed := &inlineInspectionBudget{remaining: total}
			_, err := decodeGLTFDocumentBounded(data, nil, "", allowed)
			inlineTestGate(t, err, true)
			if allowed.remaining != 0 || allowed.depth != 0 {
				t.Fatal("shared image view recharged a buffer or reset its budget")
			}
			short := &inlineInspectionBudget{remaining: total - 1}
			_, err = decodeGLTFDocumentBounded(data, nil, "", short)
			inlineTestGate(t, err, false)
			blocked := &inlineInspectionBudget{remaining: total, depth: 4}
			_, err = decodeGLTFDocumentBounded(data, nil, "", blocked)
			inlineTestGate(t, err, false)
			if blocked.remaining != total || blocked.depth != 4 {
				t.Fatal("exhausted depth decoded an image buffer")
			}
		})
	}
}

func inlineTestBufferImageDocument(pixels []byte, mediaType string, bufferIndex, offset int, shared bool) map[string]any {
	buffers := make([]any, bufferIndex+1)
	for index := range buffers {
		data := make([]byte, 4)
		if index == bufferIndex {
			data = append(make([]byte, offset), pixels...)
		}
		buffers[index] = map[string]any{"uri": inlineTestURI(data, "application/octet-stream", false), "byteLength": len(data)}
	}
	images := []any{map[string]any{"bufferView": 0, "mimeType": mediaType}}
	if shared {
		images = append(images, map[string]any{"bufferView": 0, "mimeType": mediaType})
	}
	return map[string]any{
		"asset":       map[string]any{"version": "2.0"},
		"buffers":     buffers,
		"bufferViews": []any{map[string]any{"buffer": bufferIndex, "byteOffset": offset, "byteLength": len(pixels)}},
		"images":      images,
	}
}

func inlineTestCompressedPNG(t *testing.T, text string) []byte {
	t.Helper()
	var encoded bytes.Buffer
	w := zlib.NewWriter(&encoded)
	if _, err := w.Write([]byte(text)); err != nil {
		t.Fatal("failed to compress metadata fixture")
	}
	if err := w.Close(); err != nil {
		t.Fatal("failed to finish compressed metadata fixture")
	}
	metadata := append([]byte("probe\x00\x00"), encoded.Bytes()...)
	chunk := make([]byte, len(metadata)+12)
	binary.BigEndian.PutUint32(chunk[:4], uint32(len(metadata)))
	copy(chunk[4:8], "zTXt")
	copy(chunk[8:], metadata)
	binary.BigEndian.PutUint32(chunk[len(chunk)-4:], crc32.ChecksumIEEE(chunk[4:len(chunk)-4]))
	base := inlineTestPNG(t, "synth")
	result := append([]byte(nil), base[:len(base)-12]...)
	result = append(result, chunk...)
	result = append(result, base[len(base)-12:]...)
	if _, err := png.Decode(bytes.NewReader(result)); err != nil {
		t.Fatal("compressed metadata PNG must remain decodable with valid CRC")
	}
	return result
}

func TestInlineBufferContextDoesNotEscapeRootDocument(t *testing.T) {
	bits := []byte(" //provider.example/private")
	for len(bits) < 36 {
		bits = append(bits, 0)
	}
	t.Run("real_typed_root_numbers", func(t *testing.T) {
		inlineTestWrappers(t, inlineTestPointDocument(t, bits), true)
	})
	t.Run("safe_unknown_nested_resource", func(t *testing.T) {
		doc := inlineTestPointDocument(t, make([]byte, len(bits)))
		doc["extras"] = inlineTestBufferDocument(make([]byte, len(bits)), "application/octet-stream")
		inlineTestWrappers(t, doc, true)
	})
	for _, item := range []struct {
		name string
		edit func(map[string]any, map[string]any)
	}{
		{"extras_buffers", func(doc, nested map[string]any) { doc["extras"] = nested }},
		{"extension_buffers", func(doc, nested map[string]any) {
			doc["extensions"] = map[string]any{"TEST_metadata": nested}
		}},
		{"image_extras_buffers", func(doc, nested map[string]any) {
			doc["images"] = []any{map[string]any{
				"uri": inlineTestURI(inlineTestPNG(t, "synth"), "image/png", false), "mimeType": "image/png", "extras": nested,
			}}
		}},
		{"nested_images_buffers", func(doc, nested map[string]any) {
			doc["extras"] = map[string]any{"images": []any{nested}}
		}},
		{"arbitrary_nested_buffers", func(doc, nested map[string]any) {
			doc["extras"] = map[string]any{"synth": []any{nested}}
		}},
	} {
		t.Run(item.name, func(t *testing.T) {
			// Only the root buffer's bytes are accessor storage. An unrelated
			// extras/extension resource cannot inherit those numeric ranges.
			doc := inlineTestPointDocument(t, make([]byte, len(bits)))
			item.edit(doc, inlineTestBufferDocument(bits, "application/octet-stream"))
			inlineTestWrappers(t, doc, false)
		})
	}
}
