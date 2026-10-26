package local

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
	stdjpeg "image/jpeg"
	stdpng "image/png"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func rasterNoiseFixture(t *testing.T, format string) []byte {
	t.Helper()
	if format == "png" {
		// Committed bytes (256x256 noise, seed 2026100512, image/png from Go
		// 1.26): an encoder change must not silently drop the numeric
		// protocol-relative pattern this fixture exists to reproduce.
		body, err := os.ReadFile(filepath.Join("testdata", "raster-noise.png"))
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	seed := int64(2026100512)
	pixels := image.NewRGBA(image.Rect(0, 0, 512, 512))
	if _, err := rand.New(rand.NewSource(seed)).Read(pixels.Pix); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := stdjpeg.Encode(&output, pixels, &stdjpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

// rasterRequireWholeImagePlan asserts the cached-as-delivered policy: a fully
// validated image is admitted as one allowed span with no numeric sub-ranges.
func rasterRequireWholeImagePlan(t *testing.T, plan inspectionPlan, body []byte) {
	t.Helper()
	if len(plan.allowed) != 1 || plan.allowed[0].start != 0 || plan.allowed[0].end != int64(len(body)) || len(plan.numeric) != 0 {
		t.Fatalf("validated image was not admitted whole: allowed=%v numeric=%d", plan.allowed, len(plan.numeric))
	}
}

func rasterWrite(body []byte, format string) generation.AssetWrite {
	write := modelWrite(body, format, "image/"+format)
	write.Role, write.MaxBytes = "image", int64(len(body))+1
	return write
}

func rasterCheckDeliveryPaths(t *testing.T, body []byte, format string, reject bool) {
	t.Helper()
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	write := rasterWrite(body, format)
	if format == "zip" {
		write.Role, write.MediaType, write.Variant = "model", "application/zip", "zip"
	}
	asset, putErr := store.Put(context.Background(), write)
	if (putErr != nil) != reject {
		t.Fatalf("Put rejection=%t, expected=%t: %v", putErr != nil, reject, putErr)
	}
	if reject {
		entries, err := os.ReadDir(filepath.Join(store.root, "blobs"))
		if err != nil || len(entries) != 0 {
			t.Fatalf("rejected raster changed content-addressed storage: %v", err)
		}
	}
	// PutBlob represents bytes cached before the current delivery rule. It
	// intentionally bypasses the inspector so Open's boundary is independent.
	if reject {
		blob, err := store.PutBlob(context.Background(), bytes.NewReader(body), write.MediaType, write.MaxBytes)
		if err != nil {
			t.Fatal(err)
		}
		asset = generation.Asset{Role: write.Role, Variant: write.Variant, MediaType: blob.MediaType, StorageKey: blob.StorageKey, Size: blob.Size, SHA256: blob.SHA256}
	}
	reader, openErr := store.Open(context.Background(), asset)
	if (openErr != nil) != reject {
		if reader != nil {
			reader.Close()
		}
		t.Fatalf("Open rejection=%t, expected=%t: %v", openErr != nil, reject, openErr)
	}
	if reader != nil {
		got, err := io.ReadAll(reader)
		reader.Close()
		if err != nil || !bytes.Equal(got, body) {
			t.Fatalf("raster bytes changed: %v", err)
		}
	}
	file, err := os.CreateTemp(t.TempDir(), "legacy-raster-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write(body); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	legacyErr := ValidateDeliveredFile(file, write.Role, write.Variant, write.MediaType)
	if (legacyErr != nil) != reject {
		t.Fatalf("legacy rejection=%t, expected=%t: %v", legacyErr != nil, reject, legacyErr)
	}
	if !reject {
		offset, err := file.Seek(0, io.SeekCurrent)
		if err != nil || offset != 0 {
			t.Fatalf("successful legacy check did not restore offset: %v", err)
		}
	}
	for _, result := range []error{putErr, openErr, legacyErr} {
		if result != nil && strings.Contains(result.Error(), "private.example") {
			t.Fatal("raster rejection disclosed the external address")
		}
	}
}

func TestRasterEntropyDoesNotBlockImageDelivery(t *testing.T) {
	for _, format := range []string{"png", "jpeg"} {
		t.Run(format, func(t *testing.T) {
			body := rasterNoiseFixture(t, format)
			if !containsExternalAddress(body) || containsAddress(body, absoluteAddress) {
				t.Fatal("fixture must reproduce a numeric protocol-relative false positive only")
			}
			plan, err := validateRasterBytes(body, format)
			if err != nil {
				t.Fatalf("ordinary encoded pixels were not verified: %v", err)
			}
			rasterRequireWholeImagePlan(t, plan, body)
			if err := inspectPlannedBytes(bytes.NewReader(body), plan); err != nil {
				t.Fatalf("verified image bytes rejected: %v", err)
			}
			// Pin the small reproducible fixtures used in the original report.
			digest := sha256.Sum256(body)
			wantSize, wantHash := 262588, "dac4562a58f3a75e74ca4ebc20a7a90c4f249b5ee2554e14351cd2afb3bb88e3"
			if format == "jpeg" {
				wantSize, wantHash = 235473, "6f113fbf25c6c6e1c0bd57b74c9ff751df46a908978e65a088a5bb19705d8ef9"
			}
			if len(body) != wantSize || hex.EncodeToString(digest[:]) != wantHash {
				t.Fatal("deterministic image fixture changed")
			}
			rasterCheckDeliveryPaths(t, body, format, false)
		})
	}
}

func TestRasterPNGAncillaryChunkBodiesAreCachedAsDelivered(t *testing.T) {
	for _, test := range []struct {
		name, kind, payload string
		reject              bool
	}{
		{"safe", "vpAg", "local metadata", false},
		{"index_token", "vpAg", "vertex=1//2", false},
		{"raw", "vpAg", "//provider.example/private", false},
		{"escaped", "vpAg", `\/\/provider.example/private`, false},
		{"percent", "vpAg", "%2F%2Flocalhost/private", false},
		{"utf16", "vpAg", "\x00/\x00/\x00a\x00/\x00b", false},
		{"window_boundary", "vpAg", string(bytes.Repeat([]byte{' '}, isolationChunkBytes-3)) + "//provider.example/private", false},
		{"keyword_boundary", "tEXt", "//provider.example/private\x00local text", false},
		{"type_to_body", "htTP", "s://provider.example/private", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := rasterRawPNG(t, 1, 1, 8, 6, 0, []byte{0, 255, 0, 0, 255}, rasterPNGChunk(test.kind, []byte(test.payload)))
			// All variants have correct CRCs and valid pixels. Addresses in
			// ancillary chunk bodies are cached as delivered (Lex, 2026-10-09).
			if _, err := stdpng.Decode(bytes.NewReader(body)); err != nil {
				t.Fatal("unknown-chunk fixture is not a valid PNG")
			}
			rasterCheckDeliveryPaths(t, body, "png", test.reject)
			rasterCheckDeliveryPaths(t, zipModel(t, map[string]string{"texture.png": string(body)}), "zip", test.reject)
		})
	}
}

func rasterZlib(t *testing.T, body []byte) []byte {
	t.Helper()
	var encoded bytes.Buffer
	writer, err := zlib.NewWriterLevel(&encoded, zlib.NoCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func rasterPNGChunk(kind string, payload []byte) []byte {
	chunk := make([]byte, 12+len(payload))
	binary.BigEndian.PutUint32(chunk[:4], uint32(len(payload)))
	copy(chunk[4:8], kind)
	copy(chunk[8:], payload)
	binary.BigEndian.PutUint32(chunk[len(chunk)-4:], crc32.ChecksumIEEE(chunk[4:len(chunk)-4]))
	return chunk
}

func rasterRawPNG(t *testing.T, width, height int, depth, color, interlace byte, rows []byte, beforePixels ...[]byte) []byte {
	t.Helper()
	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header[:4], uint32(width))
	binary.BigEndian.PutUint32(header[4:8], uint32(height))
	header[8], header[9], header[12] = depth, color, interlace
	output := append([]byte("\x89PNG\r\n\x1a\n"), rasterPNGChunk("IHDR", header)...)
	for _, chunk := range beforePixels {
		output = append(output, chunk...)
	}
	output = append(output, rasterPNGChunk("IDAT", rasterZlib(t, rows))...)
	return append(output, rasterPNGChunk("IEND", nil)...)
}

func TestRasterPaletteNumbersDoNotBlockImageDelivery(t *testing.T) {
	palette := []byte(" //texture.local/palette.png ")
	for len(palette)%3 != 0 {
		palette = append(palette, 0)
	}
	body := rasterRawPNG(t, 1, 1, 8, 3, 0, []byte{0, 0}, rasterPNGChunk("PLTE", palette))
	if !containsExternalAddress(body) {
		t.Fatal("fixture did not contain a protocol-relative palette pattern")
	}
	rasterCheckDeliveryPaths(t, body, "png", false)
}

func rasterPNGText(t *testing.T, kind string, text []byte) []byte {
	t.Helper()
	var payload []byte
	switch kind {
	case "tEXt":
		payload = append([]byte("Comment\x00"), text...)
	case "zTXt", "iCCP":
		payload = append([]byte("Comment\x00\x00"), rasterZlib(t, text)...)
	case "iTXt":
		payload = append([]byte("Comment\x00\x01\x00en\x00Comment\x00"), rasterZlib(t, text)...)
	default:
		payload = text
	}
	return rasterRawPNG(t, 1, 1, 8, 6, 0, []byte{0, 255, 0, 0, 255}, rasterPNGChunk(kind, payload))
}

func rasterJPEGMetadata(body []byte, marker byte, payload []byte) []byte {
	segment := make([]byte, 4+len(payload))
	segment[0], segment[1] = 0xff, marker
	binary.BigEndian.PutUint16(segment[2:4], uint16(len(payload)+2))
	copy(segment[4:], payload)
	result := append([]byte(nil), body[:2]...)
	result = append(result, segment...)
	return append(result, body[2:]...)
}

func TestRasterMetadataAddressesAreCachedAsDelivered(t *testing.T) {
	addresses := [][]byte{
		[]byte("https://private.example/image.png"),
		[]byte(" //private.example/image.png"),
		[]byte(` %2F%2Fprivate.example%2Fimage.png`),
		[]byte(` \/\/private.example\/image.png`),
		[]byte(` \u002f\u002fprivate.example/image.png`),
		[]byte(` &sol;&sol;private.example/image.png`),
		utf16LE(` //private.example/image.png`),
		utf16LE(` %2F%2Fprivate.example%2Fimage.png`),
		utf16LE(` \u002f\u002fprivate.example/image.png`),
	}
	jpeg := rasterNoiseFixture(t, "jpeg")
	for _, kind := range []string{"tEXt", "zTXt", "iTXt", "iCCP", "caBX", "raST"} {
		t.Run("PNG/"+kind, func(t *testing.T) {
			for _, address := range addresses {
				rasterCheckDeliveryPaths(t, rasterPNGText(t, kind, address), "png", false)
			}
		})
	}
	for _, marker := range []byte{0xe1, 0xe2, 0xee, 0xfe} {
		t.Run("JPEG/"+hex.EncodeToString([]byte{marker}), func(t *testing.T) {
			for _, address := range addresses {
				rasterCheckDeliveryPaths(t, rasterJPEGMetadata(jpeg, marker, address), "jpeg", false)
			}
		})
	}
}

func TestRasterPixelBytesWithAddressesAreCachedAsDelivered(t *testing.T) {
	for _, address := range [][]byte{
		[]byte(" https://private.example/texture.png"),
		[]byte(` %68%74%74%70%73%3a%2f%2fprivate.example/texture.png`),
		[]byte(` \u0068ttps:\/\/private.example/texture.png`),
		utf16LE(` https://private.example/texture.png`),
		utf16LE(` %68%74%74%70%73%3a%2f%2fprivate.example/texture.png`),
	} {
		width := (len(address) + 3) / 4
		rows := make([]byte, 1+4*width)
		copy(rows[1:], address)
		body := rasterRawPNG(t, width, 1, 8, 6, 0, rows)
		plan, err := validateRasterBytes(body, "png")
		if err != nil {
			t.Fatalf("fixture is not a valid compressed numeric image: %v", err)
		}
		rasterRequireWholeImagePlan(t, plan, body)
		if err := inspectPlannedBytes(bytes.NewReader(body), plan); err != nil {
			t.Fatalf("address-like pixel bytes of a valid image rejected: %v", err)
		}
		rasterCheckDeliveryPaths(t, body, "png", false)
	}
}

func rasterC2PABox(kind string, payload []byte) []byte {
	box := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(box[:4], uint32(len(box)))
	copy(box[4:8], kind)
	copy(box[8:], payload)
	return box
}

func rasterC2PAIdentifiers(fields ...string) []byte {
	// A definite-length CBOR array makes string boundaries explicit. Each
	// field in these small generated fixtures fits in an 8-bit string length.
	cbor := []byte{0x80 | byte(len(fields))}
	for _, field := range fields {
		if len(field) < 24 {
			cbor = append(cbor, 0x60|byte(len(field)))
		} else {
			cbor = append(cbor, 0x78, byte(len(field)))
		}
		cbor = append(cbor, field...)
	}
	return rasterC2PABox("jumb", rasterC2PABox("cbor", cbor))
}

func TestRasterC2PAManifestIsCachedAsDelivered(t *testing.T) {
	const identifier = "http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia"
	for _, metadata := range [][]byte{
		rasterC2PAIdentifiers(identifier, "adjacent CBOR text"),
		rasterC2PAIdentifiers(identifier + "/private"),
		rasterC2PAIdentifiers(identifier, "https://private.example/image.png"),
		rasterC2PABox("jumb", rasterC2PABox("opaque", []byte(identifier))),
		[]byte(identifier),
	} {
		body := rasterPNGText(t, "caBX", metadata)
		plan, err := validateRasterBytes(body, "png")
		if err != nil {
			t.Fatalf("provenance chunk with a valid CRC rejected: %v", err)
		}
		rasterRequireWholeImagePlan(t, plan, body)
		rasterCheckDeliveryPaths(t, body, "png", false)
	}
}

func TestRasterC2PACRCPrecedesIdentifierAdmission(t *testing.T) {
	const identifier = "http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia"
	for _, kind := range []string{"caBX", "IDAT"} {
		body := rasterPNGText(t, "caBX", rasterC2PAIdentifiers(identifier))
		_, end := rasterPNGChunkLocation(t, body, kind)
		body[end] ^= 1
		plan, err := validateRasterBytes(body, "png")
		var rejected *inspectionRejection
		wantReason := "png_invalid_structure"
		if kind == "caBX" {
			wantReason = "png_metadata_checksum"
		}
		if !errors.As(err, &rejected) || rejected.reason != wantReason || len(plan.numeric) != 0 || len(plan.allowed) != 0 {
			t.Fatalf("%s CRC bypassed admission or changed the stable diagnostic: %v", kind, err)
		}
		rasterCheckDeliveryPaths(t, body, "png", true)
	}
}

func rasterPNGChunkLocation(t *testing.T, body []byte, kind string) (int, int) {
	t.Helper()
	for offset := 8; offset+12 <= len(body); {
		end := offset + 8 + int(binary.BigEndian.Uint32(body[offset:offset+4]))
		if string(body[offset+4:offset+8]) == kind {
			return offset, end
		}
		offset = end + 4
	}
	t.Fatalf("missing fixture chunk %s", kind)
	return 0, 0
}

func TestRasterMalformedPixelsAndFramingGetNoNumericWaiver(t *testing.T) {
	png := rasterRawPNG(t, 1, 1, 8, 6, 0, []byte{0, 255, 0, 0, 255})
	jpeg := rasterNoiseFixture(t, "jpeg")
	pngBadCRC := append([]byte(nil), png...)
	_, crcOffset := rasterPNGChunkLocation(t, pngBadCRC, "IDAT")
	pngBadCRC[crcOffset] ^= 1
	pngBadPixels := append([]byte(nil), png...)
	idatStart, idatEnd := rasterPNGChunkLocation(t, pngBadPixels, "IDAT")
	pngBadPixels[idatEnd-1] ^= 1
	binary.BigEndian.PutUint32(pngBadPixels[idatEnd:idatEnd+4], crc32.ChecksumIEEE(pngBadPixels[idatStart+4:idatEnd]))
	pngTrailingIDAT := append([]byte(nil), png[:len(png)-12]...)
	pngTrailingIDAT = append(pngTrailingIDAT, rasterPNGChunk("IDAT", []byte(" //private.example/unused"))...)
	pngTrailingIDAT = append(pngTrailingIDAT, png[len(png)-12:]...)
	if _, err := stdpng.Decode(bytes.NewReader(pngTrailingIDAT)); err != nil {
		t.Fatalf("unused IDAT regression must reproduce the standard decoder's permissive behavior: %v", err)
	}
	jpegEntropyTrailer := append([]byte(nil), jpeg[:len(jpeg)-2]...)
	jpegEntropyTrailer = append(jpegEntropyTrailer, []byte(" //private.example/unused")...)
	jpegEntropyTrailer = append(jpegEntropyTrailer, 0xff, 0xd9)
	if _, err := stdjpeg.Decode(bytes.NewReader(jpegEntropyTrailer)); err != nil {
		t.Fatalf("unused entropy regression must reproduce the standard decoder's permissive behavior: %v", err)
	}
	for _, testCase := range []struct {
		name, format string
		body         []byte
	}{
		{"PNG invalid CRC", "png", pngBadCRC},
		{"PNG invalid compressed pixels", "png", pngBadPixels},
		{"PNG unused IDAT payload", "png", pngTrailingIDAT},
		{"PNG bytes after IEND", "png", append(append([]byte(nil), png...), []byte("benign trailer")...)},
		{"PNG missing IEND", "png", png[:len(png)-12]},
		{"JPEG unused entropy payload", "jpeg", jpegEntropyTrailer},
		{"JPEG bytes after EOI", "jpeg", append(append([]byte(nil), jpeg...), []byte("benign trailer")...)},
		{"JPEG missing EOI", "jpeg", jpeg[:len(jpeg)-2]},
		{"JPEG invalid segment length", "jpeg", append([]byte{0xff, 0xd8, 0xff, 0xe1, 0xff, 0xff}, jpeg[2:]...)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			plan, err := validateRasterBytes(testCase.body, testCase.format)
			if err == nil || len(plan.numeric) != 0 || len(plan.allowed) != 0 {
				t.Fatal("malformed image received an inspection waiver")
			}
			rasterCheckDeliveryPaths(t, testCase.body, testCase.format, true)
		})
	}
}

func TestRasterOverBudgetImagesAreWalkedNotDecoded(t *testing.T) {
	for _, format := range []string{"png", "jpeg"} {
		for _, dimensions := range [][2]int{{4096, 4096}, {3840, 2160}, {2160, 3840}} {
			if err := checkRasterDecodeBudget(dimensions[0], dimensions[1], 64<<20, format); err != nil {
				t.Fatalf("legal 4K decoder envelope rejected for %s: %v", format, err)
			}
		}
		for _, dimensions := range [][2]int{{0, 4096}, {-1, 1}, {10000, 10000}, {1 << 30, 1 << 30}, {int(^uint(0) >> 1), 1}} {
			if err := checkRasterDecodeBudget(dimensions[0], dimensions[1], 64, format); err == nil {
				t.Fatalf("unbounded decoder envelope accepted for %s", format)
			}
		}
	}
	// Over the decode budget the image is cached after its structure walk
	// alone (Lex, 2026-10-10): the declared 10000x10000 pixels are never
	// inflated or allocated, so even this one-row stream is not decoded.
	png := rasterRawPNG(t, 10000, 10000, 8, 6, 0, []byte{0, 255, 0, 0, 255})
	jpeg := rasterProgressiveJPEG(10000, 10000)
	for _, testCase := range []struct {
		format string
		body   []byte
	}{{"png", png}, {"jpeg", jpeg}} {
		plan, err := validateRasterBytes(testCase.body, testCase.format)
		if err != nil || len(plan.allowed) != 1 || plan.allowed[0] != (inspectionSpan{0, int64(len(testCase.body))}) {
			t.Fatalf("%s over the decode budget was not admitted after its structure walk: %v", testCase.format, err)
		}
		rasterCheckDeliveryPaths(t, testCase.body, testCase.format, false)
	}
	// The walk still rejects broken framing: a bad IHDR CRC or a missing EOI.
	brokenPNG := append([]byte(nil), png...)
	brokenPNG[29] ^= 1
	brokenJPEG := jpeg[:len(jpeg)-2]
	for _, testCase := range []struct {
		format string
		body   []byte
	}{{"png", brokenPNG}, {"jpeg", brokenJPEG}} {
		if _, err := validateRasterBytes(testCase.body, testCase.format); err == nil {
			t.Fatalf("%s over the decode budget with broken framing was admitted", testCase.format)
		}
	}
}

func rasterProgressiveJPEG(width, height int) []byte {
	segment := func(marker byte, payload []byte) []byte {
		result := []byte{0xff, marker, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
		return append(result, payload...)
	}
	output := []byte{0xff, 0xd8}
	quantization := append([]byte{0}, bytes.Repeat([]byte{1}, 64)...)
	output = append(output, segment(0xdb, quantization)...)
	output = append(output, segment(0xc2, []byte{8, byte(height >> 8), byte(height), byte(width >> 8), byte(width), 1, 1, 0x11, 0})...)
	// One-bit DC category 0 and AC EOB codes are sufficient for a zero block.
	huffman := append([]byte{0, 1}, make([]byte, 15)...)
	huffman = append(huffman, 0)
	huffman = append(huffman, 0x10, 1)
	huffman = append(huffman, make([]byte, 15)...)
	huffman = append(huffman, 0)
	output = append(output, segment(0xc4, huffman)...)
	output = append(output, segment(0xda, []byte{1, 1, 0, 0, 0, 0})...)
	output = append(output, 0x7f)
	output = append(output, segment(0xda, []byte{1, 1, 0, 1, 63, 0})...)
	output = append(output, 0x7f, 0xff, 0xd9)
	return output
}

func TestRasterCompleteProgressiveJPEGIsVerified(t *testing.T) {
	body := rasterProgressiveJPEG(8, 8)
	if _, err := stdjpeg.Decode(bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	rasterCheckDeliveryPaths(t, body, "jpeg", false)
	withTrailer := append([]byte(nil), body[:len(body)-2]...)
	withTrailer = append(withTrailer, []byte(" //private.example/progressive")...)
	withTrailer = append(withTrailer, 0xff, 0xd9)
	rasterCheckDeliveryPaths(t, withTrailer, "jpeg", true)
}

func rasterJPEGTestSegment(marker byte, payload []byte) []byte {
	result := []byte{0xff, marker, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	return append(result, payload...)
}

func rasterRefinedProgressiveJPEG(initialNonzero bool) []byte {
	output := []byte{0xff, 0xd8}
	output = append(output, rasterJPEGTestSegment(0xdb, append([]byte{0}, bytes.Repeat([]byte{1}, 64)...))...)
	output = append(output, rasterJPEGTestSegment(0xc2, []byte{8, 0, 8, 0, 8, 1, 1, 0x11, 0})...)
	// DC code 0 means category 0. AC codes 00 and 01 mean (run=2,
	// size=1) and EOB. The latter also lets refinement add a new coefficient.
	huffman := append([]byte{0, 1}, make([]byte, 15)...)
	huffman = append(huffman, 0, 0x10, 0, 2)
	huffman = append(huffman, make([]byte, 14)...)
	huffman = append(huffman, 0x21, 0)
	output = append(output, rasterJPEGTestSegment(0xc4, huffman)...)
	output = append(output, rasterJPEGTestSegment(0xda, []byte{1, 1, 0, 0, 0, 1})...)
	output = append(output, 0x7f) // Initial DC: code 0, all-one padding.
	output = append(output, rasterJPEGTestSegment(0xda, []byte{1, 1, 0, 0, 0, 0x10})...)
	output = append(output, 0xff, 0) // DC refinement bit 1 plus padding; stuffed FF.
	output = append(output, rasterJPEGTestSegment(0xda, []byte{1, 1, 0, 1, 63, 1})...)
	if initialNonzero {
		output = append(output, 0x2f) // 00, sign 1, EOB 01, padding.
	} else {
		output = append(output, 0x7f) // EOB 01, padding.
	}
	output = append(output, rasterJPEGTestSegment(0xda, []byte{1, 1, 0, 1, 63, 0x10})...)
	if initialNonzero {
		output = append(output, 0x7f) // EOB 01, existing coefficient correction 1.
	} else {
		output = append(output, 0x2f) // New coefficient: 00, sign 1, EOB 01.
	}
	return append(output, 0xff, 0xd9)
}

func TestRasterProgressiveRefinementConsumesAllBits(t *testing.T) {
	for _, initialNonzero := range []bool{false, true} {
		body := rasterRefinedProgressiveJPEG(initialNonzero)
		if _, err := stdjpeg.Decode(bytes.NewReader(body)); err != nil {
			t.Fatalf("refinement fixture is not a valid JPEG: %v", err)
		}
		rasterCheckDeliveryPaths(t, body, "jpeg", false)
		withUnusedByte := append([]byte(nil), body[:len(body)-2]...)
		withUnusedByte = append(withUnusedByte, 0x7f, 0xff, 0xd9)
		if _, err := validateRasterBytes(withUnusedByte, "jpeg"); err == nil {
			t.Fatal("refinement scan did not enforce exact entropy consumption")
		}
	}
}

func TestRasterJPEGRestartMarkersAreVerified(t *testing.T) {
	output := []byte{0xff, 0xd8}
	output = append(output, rasterJPEGTestSegment(0xdb, append([]byte{0}, bytes.Repeat([]byte{1}, 64)...))...)
	output = append(output, rasterJPEGTestSegment(0xc0, []byte{8, 0, 8, 0, 16, 1, 1, 0x11, 0})...)
	huffman := append([]byte{0, 1}, make([]byte, 15)...)
	huffman = append(huffman, 0, 0x10, 1)
	huffman = append(huffman, make([]byte, 15)...)
	huffman = append(huffman, 0)
	output = append(output, rasterJPEGTestSegment(0xc4, huffman)...)
	output = append(output, rasterJPEGTestSegment(0xdd, []byte{0, 1})...)
	output = append(output, rasterJPEGTestSegment(0xda, []byte{1, 1, 0, 0, 63, 0})...)
	output = append(output, 0x3f, 0xff, 0xd0, 0x3f, 0xff, 0xd9)
	if _, err := stdjpeg.Decode(bytes.NewReader(output)); err != nil {
		t.Fatalf("restart fixture is not a valid JPEG: %v", err)
	}
	rasterCheckDeliveryPaths(t, output, "jpeg", false)
	wrongRestart := append([]byte(nil), output...)
	wrongRestart[len(wrongRestart)-4] = 0xd1
	rasterCheckDeliveryPaths(t, wrongRestart, "jpeg", true)
}

func TestRasterPNGAdam7PixelsAndLocalMetadataAreVerified(t *testing.T) {
	const width, height = 17, 13
	var rows []byte
	passes := [7][4]int{{0, 0, 8, 8}, {4, 0, 8, 8}, {0, 4, 4, 8}, {2, 0, 4, 4}, {0, 2, 2, 4}, {1, 0, 2, 2}, {0, 1, 1, 2}}
	for _, pass := range passes {
		for y := pass[1]; y < height; y += pass[3] {
			rows = append(rows, 0)
			for x := pass[0]; x < width; x += pass[2] {
				rows = append(rows, byte(x), byte(y), 0, 255)
			}
		}
	}
	body := rasterRawPNG(t, width, height, 8, 6, 1, rows)
	decoded, err := stdpng.Decode(bytes.NewReader(body))
	if err != nil || decoded.Bounds() != image.Rect(0, 0, width, height) {
		t.Fatalf("Adam7 fixture is not a valid PNG: %v", err)
	}
	rasterCheckDeliveryPaths(t, body, "png", false)
	for _, kind := range []string{"tEXt", "zTXt", "iTXt", "iCCP"} {
		rasterCheckDeliveryPaths(t, rasterPNGText(t, kind, []byte("local image description")), "png", false)
	}
}

func TestRasterCommon4KImagesDecodeWithinBudget(t *testing.T) {
	pixels := image.NewGray(image.Rect(0, 0, 4096, 4096))
	for _, format := range []string{"png", "jpeg"} {
		t.Run(format, func(t *testing.T) {
			var output bytes.Buffer
			var err error
			if format == "png" {
				err = stdpng.Encode(&output, pixels)
			} else {
				err = stdjpeg.Encode(&output, pixels, &stdjpeg.Options{Quality: 90})
			}
			if err != nil {
				t.Fatal(err)
			}
			plan, err := validateRasterBytes(output.Bytes(), format)
			if err != nil {
				t.Fatalf("legal 4K image rejected: %v", err)
			}
			if err := inspectPlannedBytes(bytes.NewReader(output.Bytes()), plan); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestArchiveRasterEntriesUseSameDeliveryBoundary(t *testing.T) {
	for _, format := range []string{"png", "jpeg"} {
		t.Run(format, func(t *testing.T) {
			body := rasterNoiseFixture(t, format)
			archive := zipModel(t, map[string]string{
				"scene.gltf":               `{"asset":{"version":"2.0"},"images":[{"uri":"textures/pixel.` + format + `"}]}`,
				"textures/pixel." + format: string(body),
			})
			rasterCheckDeliveryPaths(t, archive, "zip", false)
			if format == "png" {
				body = rasterPNGText(t, "zTXt", []byte(" //private.example/texture.png"))
			} else {
				body = rasterJPEGMetadata(body, 0xe1, []byte(" //private.example/texture.png"))
			}
			archive = zipModel(t, map[string]string{"textures/pixel." + format: string(body)})
			rasterCheckDeliveryPaths(t, archive, "zip", false)
		})
	}
}

func TestRasterParserWorkAndMetadataExpansionAreBounded(t *testing.T) {
	base := rasterRawPNG(t, 1, 1, 8, 6, 0, []byte{0, 255, 0, 0, 255})
	chunks := append([]byte(nil), base[:33]...)
	for count := 0; count < maxRasterSections; count++ {
		chunks = append(chunks, rasterPNGChunk("raST", nil)...)
	}
	chunks = append(chunks, base[33:]...)
	if _, err := validateRasterBytes(chunks, "png"); err == nil {
		t.Fatal("PNG chunk work was not bounded")
	}
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	block := bytes.Repeat([]byte{'a'}, isolationChunkBytes)
	for written := 0; written <= maxRasterTextBytes; written += len(block) {
		if _, err := writer.Write(block); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	payload := append([]byte("Comment\x00\x00"), compressed.Bytes()...)
	bomb := rasterRawPNG(t, 1, 1, 8, 6, 0, []byte{0, 255, 0, 0, 255}, rasterPNGChunk("zTXt", payload))
	var rejected *inspectionRejection
	_, err := validateRasterBytes(bomb, "png")
	if !errors.As(err, &rejected) || rejected.reason != "png_metadata_limit" {
		t.Fatalf("metadata expansion limit was not enforced: %v", err)
	}
}

// Real upstream 4K images (Seedream, Gemini) carry XMP and C2PA manifests whose
// text holds many addresses: IPTC vocabulary, W3C/Adobe namespaces and
// certificate CRL/OCSP URLs. They are cached byte for byte (Lex, 2026-10-09).
const rasterXMPPacket = `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>` +
	`<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
	`<rdf:Description xmlns:xmp="http://ns.adobe.com/xap/1.0/" xmlns:Iptc4xmpExt="http://iptc.org/std/Iptc4xmpExt/2008-02-29/" ` +
	`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:photoshop="http://ns.adobe.com/photoshop/1.0/">` +
	`<Iptc4xmpExt:DigitalSourceType>http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia</Iptc4xmpExt:DigitalSourceType>` +
	`</rdf:Description></rdf:RDF></x:xmpmeta><?xpacket end="w"?>`

func rasterManifestPayload() []byte {
	return rasterC2PAIdentifiers("http://crl.globalsign.com/x.crl", "//provider.example/private")
}

var rasterLarge struct {
	once     sync.Once
	plainPNG []byte
	plainJPG []byte
	pngMeta  []byte
	jpegMeta []byte
	err      error
}

// rasterLargeImages builds one 4096x4096 PNG and JPEG (smooth gradient plus
// mild noise) without and with address-bearing metadata, once per test binary.
func rasterLargeImages(t *testing.T) (plainPNG, pngMeta, plainJPEG, jpegMeta []byte) {
	t.Helper()
	rasterLarge.once.Do(func() {
		const side = 4096
		pixels := image.NewRGBA(image.Rect(0, 0, side, side))
		state := uint32(2026100901)
		for y := 0; y < side; y++ {
			row := pixels.Pix[y*pixels.Stride : y*pixels.Stride+side*4]
			for x := 0; x < side; x++ {
				state ^= state << 13
				state ^= state >> 17
				state ^= state << 5
				noise := int(state&3) - 1
				clamp := func(v int) uint8 { return uint8(min(max(v, 0), 255)) }
				row[x*4+0] = clamp(x*255/side + noise)
				row[x*4+1] = clamp(y*255/side + noise)
				row[x*4+2] = clamp((x+y)*255/(2*side) + noise)
				row[x*4+3] = 255
			}
		}
		var pngBuffer bytes.Buffer
		if err := (&stdpng.Encoder{CompressionLevel: stdpng.BestSpeed}).Encode(&pngBuffer, pixels); err != nil {
			rasterLarge.err = err
			return
		}
		var jpegBuffer bytes.Buffer
		if err := stdjpeg.Encode(&jpegBuffer, pixels, &stdjpeg.Options{Quality: 85}); err != nil {
			rasterLarge.err = err
			return
		}
		rasterLarge.plainPNG, rasterLarge.plainJPG = pngBuffer.Bytes(), jpegBuffer.Bytes()

		// PNG: uncompressed iTXt XMP and a caBX manifest, both before IDAT.
		xmp := append([]byte("XML:com.adobe.xmp\x00\x00\x00\x00\x00"), rasterXMPPacket...)
		withMeta := append([]byte(nil), rasterLarge.plainPNG[:33]...)
		withMeta = append(withMeta, rasterPNGChunk("iTXt", xmp)...)
		withMeta = append(withMeta, rasterPNGChunk("caBX", rasterManifestPayload())...)
		rasterLarge.pngMeta = append(withMeta, rasterLarge.plainPNG[33:]...)

		// JPEG: APP1 XMP, then APP11 JUMBF, directly after SOI.
		app1 := append([]byte("http://ns.adobe.com/xap/1.0/\x00"), rasterXMPPacket...)
		app11 := append([]byte("JP\x00\x01\x00\x00\x00\x01"), rasterManifestPayload()...)
		rasterLarge.jpegMeta = rasterJPEGMetadata(rasterJPEGMetadata(rasterLarge.plainJPG, 0xeb, app11), 0xe1, app1)
	})
	if rasterLarge.err != nil {
		t.Fatal(rasterLarge.err)
	}
	return rasterLarge.plainPNG, rasterLarge.pngMeta, rasterLarge.plainJPG, rasterLarge.jpegMeta
}

func TestRasterLarge4KImagesWithProviderMetadataAreCachedAsDelivered(t *testing.T) {
	plainPNG, pngMeta, plainJPEG, jpegMeta := rasterLargeImages(t)
	for _, test := range []struct {
		format       string
		plain, image []byte
		wanted       []string
	}{
		{"png", plainPNG, pngMeta, []string{"http://ns.adobe.com/xap/1.0/", "http://www.w3.org/1999/02/22-rdf-syntax-ns#", "http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia", "http://crl.globalsign.com/x.crl", "//provider.example/private"}},
		{"jpeg", plainJPEG, jpegMeta, []string{"http://ns.adobe.com/xap/1.0/", "http://www.w3.org/1999/02/22-rdf-syntax-ns#", "http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia", "http://crl.globalsign.com/x.crl", "//provider.example/private"}},
	} {
		t.Run(test.format, func(t *testing.T) {
			for _, want := range test.wanted {
				if !bytes.Contains(test.image, []byte(want)) {
					t.Fatalf("fixture lost its metadata address %q", want)
				}
			}
			decoded, _, err := image.Decode(bytes.NewReader(test.image))
			if err != nil || decoded.Bounds() != image.Rect(0, 0, 4096, 4096) {
				t.Fatalf("fixture is not a valid 4096x4096 image: %v", err)
			}
			plan, err := validateRasterBytes(test.image, test.format)
			if err != nil {
				t.Fatalf("4K image with provider metadata rejected: %v", err)
			}
			rasterRequireWholeImagePlan(t, plan, test.image)
			// Put, Open (bytes unchanged) and ValidateDeliveredFile all accept.
			rasterCheckDeliveryPaths(t, test.image, test.format, false)
		})
	}
}

func TestRasterLarge4KImagesWithProviderMetadataStillRejectMalformedStructure(t *testing.T) {
	_, pngMeta, _, jpegMeta := rasterLargeImages(t)
	corruptPNGCRC := bytes.Clone(pngMeta)
	_, idatEnd := rasterPNGChunkLocation(t, corruptPNGCRC, "IDAT")
	corruptPNGCRC[idatEnd+1] ^= 0x40
	corruptPNGMetadataCRC := bytes.Clone(pngMeta)
	_, manifestEnd := rasterPNGChunkLocation(t, corruptPNGMetadataCRC, "caBX")
	corruptPNGMetadataCRC[manifestEnd] ^= 1
	truncatedPNG := pngMeta[:len(pngMeta)-12-4096] // Pixel data cut short, IEND gone.
	truncatedJPEG := append(bytes.Clone(jpegMeta[:len(jpegMeta)-4096]), 0xff, 0xd9)
	jpegNoEOI := jpegMeta[:len(jpegMeta)-4096]
	for _, test := range []struct {
		name, format string
		body         []byte
	}{
		{"PNG IDAT CRC", "png", corruptPNGCRC},
		{"PNG caBX CRC", "png", corruptPNGMetadataCRC},
		{"PNG truncated pixel data", "png", truncatedPNG},
		{"JPEG truncated entropy data", "jpeg", truncatedJPEG},
		{"JPEG truncated without EOI", "jpeg", jpegNoEOI},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := validateRasterBytes(test.body, test.format)
			if err == nil || len(plan.allowed) != 0 || len(plan.numeric) != 0 {
				t.Fatalf("malformed 4K image with metadata addresses was admitted: %v", err)
			}
			rasterCheckDeliveryPaths(t, test.body, test.format, true)
		})
	}
}

// TestRaster8KTexturesAreCachedAfterStructureWalk covers Meshy's 8K PBR
// textures: over the decode budget, standalone and embedded in a GLB, and
// over the old 64 MiB encoded limit (Lex, 2026-10-10).
func TestRaster8KTexturesAreCachedAfterStructureWalk(t *testing.T) {
	const side = 8192
	pixels := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		row := pixels.Pix[y*pixels.Stride : y*pixels.Stride+side*4]
		for x := 0; x < side; x++ {
			row[x*4], row[x*4+1], row[x*4+2], row[x*4+3] = uint8(x>>5), uint8(y>>5), 128, 255
		}
	}
	var pngBuffer, jpegBuffer bytes.Buffer
	if err := (&stdpng.Encoder{CompressionLevel: stdpng.BestSpeed}).Encode(&pngBuffer, pixels); err != nil {
		t.Fatal(err)
	}
	if err := stdjpeg.Encode(&jpegBuffer, pixels, &stdjpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	// A private ancillary chunk pushes the PNG past the old 64 MiB limit.
	padded := append([]byte(nil), pngBuffer.Bytes()[:33]...)
	padded = append(padded, rasterPNGChunk("prVt", make([]byte, 65<<20))...)
	padded = append(padded, pngBuffer.Bytes()[33:]...)
	for _, testCase := range []struct {
		name, format string
		body         []byte
	}{{"png", "png", pngBuffer.Bytes()}, {"jpeg", "jpeg", jpegBuffer.Bytes()}, {"png_over_64MiB", "png", padded}} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := checkRasterDecodeBudget(side, side, len(testCase.body), testCase.format); err == nil {
				t.Fatal("8K texture fits the decode budget; this test no longer covers the structure walk")
			}
			plan, err := validateRasterBytes(testCase.body, testCase.format)
			if err != nil || len(plan.allowed) != 1 || plan.allowed[0] != (inspectionSpan{0, int64(len(testCase.body))}) {
				t.Fatalf("8K texture was not cached as delivered: %v", err)
			}
			rasterCheckDeliveryPaths(t, testCase.body, testCase.format, false)
		})
	}
	for _, format := range []string{"png", "jpeg"} {
		t.Run("glb_"+format, func(t *testing.T) {
			body := pngBuffer.Bytes()
			if format == "jpeg" {
				body = jpegBuffer.Bytes()
			}
			assertIsolationBoundary(t, isolatedGLBFixture(t, body, "image/"+format), "glb", "model/gltf-binary", true)
		})
	}
	// Large textures inside one model: their walks exceed the fixed work
	// floor, so the budget has to scale with the delivered file.
	t.Run("fbx_png_over_100MiB", func(t *testing.T) {
		if testing.Short() {
			t.Skip("builds a fixture over 100 MiB")
		}
		texture := append([]byte(nil), pngBuffer.Bytes()[:33]...)
		texture = append(texture, rasterPNGChunk("prVt", make([]byte, 120<<20))...)
		texture = append(texture, pngBuffer.Bytes()[33:]...)
		body := fbxTestFile(t, 7400, fbxTestNode{name: []byte("Content"), properties: [][]byte{fbxTestBytes('R', texture)}})
		if err := inspectBinaryFBX(bytes.NewReader(body), int64(len(body)), newInspectionWorkBudget()); err == nil {
			t.Fatal("the fixed work floor admits this FBX; this test no longer covers budget scaling")
		}
		assertIsolationBoundary(t, body, "fbx", "application/octet-stream", true)
	})
	t.Run("glb_eight_large_textures", func(t *testing.T) {
		if testing.Short() {
			t.Skip("builds a fixture over 100 MiB")
		}
		body := rasterTestGLBWithImages(t, padded, 8)
		if err := inspectGLBStreamBounded(bytes.NewReader(body), int64(len(body)), nil, "", newInlineInspectionBudget()); err == nil {
			t.Fatal("the fixed work floor admits this GLB; this test no longer covers budget scaling")
		}
		if err := inspectGLB(bytes.NewReader(body), int64(len(body)), nil, ""); err != nil {
			t.Fatalf("a GLB with eight large textures was rejected: %v", err)
		}
	})
}

// rasterTestGLBWithImages stores count copies of one PNG as separate images.
func rasterTestGLBWithImages(t *testing.T, texture []byte, count int) []byte {
	t.Helper()
	positions := make([]byte, 36)
	for _, offset := range []int{12, 28} {
		binary.LittleEndian.PutUint32(positions[offset:], math.Float32bits(1))
	}
	bin := append([]byte(nil), positions...)
	views := []any{map[string]any{"buffer": 0, "byteOffset": 0, "byteLength": 36}}
	images := []any{}
	for index := 0; index < count; index++ {
		for len(bin)%4 != 0 {
			bin = append(bin, 0)
		}
		views = append(views, map[string]any{"buffer": 0, "byteOffset": len(bin), "byteLength": len(texture)})
		images = append(images, map[string]any{"bufferView": index + 1, "mimeType": "image/png"})
		bin = append(bin, texture...)
	}
	document, err := json.Marshal(map[string]any{
		"asset":       map[string]string{"version": "2.0"},
		"buffers":     []any{map[string]any{"byteLength": len(bin)}},
		"bufferViews": views,
		"accessors":   []any{map[string]any{"bufferView": 0, "componentType": 5126, "count": 3, "type": "VEC3"}},
		"images":      images,
	})
	if err != nil {
		t.Fatal(err)
	}
	return glbFixtureWithBIN(string(document), bin)
}

// A Meshy/Tripo PBR model holds several 4K textures. Each fits the decode
// memory budget, but their decodes together exceed the shared work budget;
// the textures the budget cannot decode are cached after the structure walk.
func TestRasterModelWithSeveral4KTexturesIsCached(t *testing.T) {
	const side, count = 4096, 4
	pixels := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		row := pixels.Pix[y*pixels.Stride : y*pixels.Stride+side*4]
		for x := 0; x < side; x++ {
			row[x*4], row[x*4+1], row[x*4+2], row[x*4+3] = uint8(x>>4), uint8(y>>4), 64, 255
		}
	}
	var texture bytes.Buffer
	if err := (&stdpng.Encoder{CompressionLevel: stdpng.BestSpeed}).Encode(&texture, pixels); err != nil {
		t.Fatal(err)
	}
	if err := checkRasterDecodeBudget(side, side, texture.Len(), "png"); err != nil {
		t.Fatalf("a 4K texture must fit the decode budget: %v", err)
	}
	pixelWork, err := pngFrameWork(rasterPNGFrame{width: side, height: side, depth: 8, color: 6})
	if err != nil {
		t.Fatal(err)
	}
	if decode := 2*int64(texture.Len()) + pixelWork; count*decode <= maxInspectionWork {
		t.Fatal("the decodes fit the work floor; this test no longer covers the structure-only fallback")
	}
	body := rasterTestGLBWithImages(t, texture.Bytes(), count)
	assertIsolationBoundary(t, body, "glb", "model/gltf-binary", true)
	properties := make([][]byte, count)
	for index := range properties {
		properties[index] = fbxTestBytes('R', texture.Bytes())
	}
	body = fbxTestFile(t, 7400, fbxTestNode{name: []byte("Content"), properties: properties})
	assertIsolationBoundary(t, body, "fbx", "application/octet-stream", true)
}
