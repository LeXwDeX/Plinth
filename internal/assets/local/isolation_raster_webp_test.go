package local

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// webpTestVP8L is the image chunk of a 1x1 lossless WebP (the wire fixture
// webp-1x1), padding byte included.
var webpTestVP8L = []byte("VP8L\x0f\x00\x00\x00\x2f\x00\x00\x00\x00\x07\x10\xfd\x8f\xfe\x07\x22\xa2\xff\x01\x00")

// webpTestXMP is an ordinary XMP packet: every packet names its schemas by
// http:// namespace URIs, which the generic address scan reads as addresses.
const webpTestXMP = `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"><rdf:Description xmlns:xmp="http://ns.adobe.com/xap/1.0/" xmp:CreatorTool="renderer"/></rdf:RDF></x:xmpmeta>`

func webpTestChunk(kind string, payload []byte) []byte {
	chunk := append([]byte(kind), binary.LittleEndian.AppendUint32(nil, uint32(len(payload)))...)
	chunk = append(chunk, payload...)
	if len(payload)%2 == 1 {
		chunk = append(chunk, 0)
	}
	return chunk
}

func webpTestFile(chunks ...[]byte) []byte {
	body := []byte("WEBP")
	for _, chunk := range chunks {
		body = append(body, chunk...)
	}
	return append(append([]byte("RIFF"), binary.LittleEndian.AppendUint32(nil, uint32(len(body)))...), body...)
}

// webpTestVP8X is an extended header for a 1x1 canvas with the given flags.
func webpTestVP8X(flags byte) []byte {
	return webpTestChunk("VP8X", []byte{flags, 0, 0, 0, 0, 0, 0, 0, 0, 0})
}

// webpTestRender is an extended WebP carrying an XMP packet, the shape of a
// renderer's output with metadata.
func webpTestRender() []byte {
	return webpTestFile(webpTestVP8X(0x04), webpTestVP8L, webpTestChunk("XMP ", []byte(webpTestXMP)))
}

func inspectWebPTestFile(t *testing.T, data []byte, variant, mediaType string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "render")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	return inspectDeliveredAsset(file, "thumbnail", variant, mediaType)
}

// #576: a Tripo render (rendered_image, WebP) reached the generic address scan
// and was refused as content_rejected, so its background slot never settled.
// A WebP is now proved by its RIFF walk and a decode, and its metadata is
// admitted as delivered like a PNG's or a JPEG's.
func TestDeliveredWebPRenderIsAdmittedAsRaster576(t *testing.T) {
	render := webpTestRender()
	if err := scanExternalAddresses(bytes.NewReader(render)); err == nil {
		t.Fatal("fixture no longer reproduces #576: the generic scan accepts it")
	}
	for _, label := range []struct{ variant, mediaType string }{
		{"rendered_image", "image/webp"},
		{"rendered_image", "application/octet-stream"}, // known by its header
		{"rendered_image.webp", ""},
	} {
		if err := inspectWebPTestFile(t, render, label.variant, label.mediaType); err != nil {
			t.Fatalf("%+v: WebP render refused: %v (reason %q)", label, err, inspectionReasonOf(err))
		}
	}
	simple := webpTestFile(webpTestVP8L)
	if err := inspectWebPTestFile(t, simple, "rendered_image", "image/webp"); err != nil {
		t.Fatalf("simple lossless WebP refused: %v", err)
	}
}

// The WebP check refuses what PNG and JPEG refuse: trailing bytes, broken
// framing, structures the decoder cannot read, and other bytes under a WebP
// label. None of these reach the generic scan.
func TestDeliveredWebPRejectsMalformedFiles576(t *testing.T) {
	render := webpTestRender()
	corrupt := append([]byte(nil), render...)
	vp8l := bytes.Index(corrupt, []byte("VP8L"))
	corrupt[vp8l+8] = 0x00 // VP8L signature byte
	unknown := webpTestFile(webpTestVP8X(0), webpTestVP8L, webpTestChunk("ZZZZ", []byte("payload")))
	reordered := webpTestFile(webpTestVP8X(0x04), webpTestChunk("XMP ", []byte(webpTestXMP)), webpTestVP8L)
	badPadding := webpTestFile(webpTestVP8X(0x04), webpTestVP8L, webpTestChunk("XMP ", []byte("odd")))
	badPadding[len(badPadding)-1] = 'x'
	cases := []struct {
		name, reason string
		data         []byte
		mediaType    string
	}{
		{"trailing bytes", "raster_trailing_data", append(append([]byte(nil), render...), []byte("junk")...), "image/webp"},
		{"truncated", "raster_invalid_structure", render[:len(render)-4], "image/webp"},
		{"animation flag", "raster_unsupported_format", webpTestFile(webpTestVP8X(0x02), webpTestVP8L), "image/webp"},
		{"animation chunk", "raster_unsupported_format", webpTestFile(webpTestVP8X(0), webpTestChunk("ANIM", make([]byte, 6))), "image/webp"},
		{"unknown chunk", "raster_invalid_structure", unknown, "image/webp"},
		{"chunk order", "raster_invalid_structure", reordered, "image/webp"},
		{"padding byte", "raster_invalid_structure", badPadding, "image/webp"},
		{"two simple images", "raster_invalid_structure", webpTestFile(webpTestVP8L, webpTestVP8L), "image/webp"},
		{"corrupt pixels", "raster_invalid_structure", corrupt, "image/webp"},
		{"not a webp", "raster_invalid_structure", []byte("<html>http://example.test/</html>"), "image/webp"},
	}
	for _, test := range cases {
		err := inspectWebPTestFile(t, test.data, "rendered_image", test.mediaType)
		if err == nil {
			t.Fatalf("%s: accepted", test.name)
		}
		if got := inspectionReasonOf(err); got != test.reason {
			t.Fatalf("%s: reason=%q want %q (%v)", test.name, got, test.reason, err)
		}
	}
}
