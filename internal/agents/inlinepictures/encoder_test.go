package inlinepictures

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

func testPolicy() Policy {
	return Policy{Format: FormatJPEG, Quality: 95, Background: color.RGBA{255, 255, 255, 255}, MaximumPixels: 16777216}
}

func newEncoder(t *testing.T) *Encoder {
	t.Helper()
	encoder, err := New(testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return encoder
}

// texture is a deterministic picture with gradients and fine grain, so that
// its compression ratio is closer to a real render than a flat colour's.
func texture(width, height int, seed uint32) *image.NRGBA {
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	state := seed
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			state = state*1664525 + 1013904223
			grain := int(state>>24) % 8
			picture.Pix[picture.PixOffset(x, y)+0] = uint8((x*255/width + grain) % 256)
			picture.Pix[picture.PixOffset(x, y)+1] = uint8((y*255/height + grain) % 256)
			picture.Pix[picture.PixOffset(x, y)+2] = uint8(((x+y)*127/(width+height) + grain) % 256)
			picture.Pix[picture.PixOffset(x, y)+3] = 255
		}
	}
	return picture
}

func pngURI(t *testing.T, picture image.Image) string {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buffer.Bytes())
}

func jpegBytes(t *testing.T, picture image.Image) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, picture, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func decodeURI(t *testing.T, uri string) (image.Image, string) {
	t.Helper()
	if !strings.HasPrefix(uri, "data:image/jpeg;base64,") {
		t.Fatalf("not a JPEG data URI: %.40s", uri)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, "data:image/jpeg;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return decoded, format
}

func pictureRequest(urls ...string) agents.Request {
	message := agents.Message{Role: "user"}
	for _, url := range urls {
		message.Parts = append(message.Parts,
			agents.ContentPart{Kind: "image_url", URL: url, Detail: "high"},
			agents.ContentPart{Kind: "text", Text: "label"})
	}
	return agents.Request{Model: "m", Messages: []agents.Message{{Role: "system", Content: "s"}, message}}
}

func TestRequestEncodesLargePNGsAsMuchSmallerJPEGsAtFullSize(t *testing.T) {
	encoder := newEncoder(t)
	first := pngURI(t, texture(2048, 2048, 1))
	second := pngURI(t, texture(2048, 2048, 2))
	request := pictureRequest(first, second)

	encoded, stats := encoder.Request(request)

	if stats.Images != 2 || stats.Encoded != 2 || stats.Kept != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	if want := int64(len(first) + len(second)); stats.OriginalBytes != want {
		t.Fatalf("original bytes = %d, want %d", stats.OriginalBytes, want)
	}
	// The ratio, not an absolute size: JPEG must be far smaller than PNG.
	if ratio := float64(stats.Bytes) / float64(stats.OriginalBytes); ratio > 0.5 {
		t.Fatalf("JPEG/PNG ratio = %.2f (bytes %d of %d)", ratio, stats.Bytes, stats.OriginalBytes)
	}
	t.Logf("2×2048² texture: PNG %d bytes → JPEG %d bytes (%.2f)", stats.OriginalBytes, stats.Bytes,
		float64(stats.Bytes)/float64(stats.OriginalBytes))
	for _, index := range []int{0, 2} {
		decoded, _ := decodeURI(t, encoded.Messages[1].Parts[index].URL)
		if bounds := decoded.Bounds(); bounds.Dx() != 2048 || bounds.Dy() != 2048 {
			t.Fatalf("picture resized to %v", bounds)
		}
	}
	if request.Messages[1].Parts[0].URL != first {
		t.Fatal("the caller's request was changed")
	}
	if encoded.Messages[1].Parts[1].Text != "label" || encoded.Messages[1].Parts[0].Detail != "high" {
		t.Fatal("non-picture fields were changed")
	}
}

func TestTransparentPictureIsCompositedOverTheBackground(t *testing.T) {
	picture := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 32; x < 64; x++ {
			picture.SetNRGBA(x, y, color.NRGBA{R: 200, A: 255})
		}
	}
	decoded, _ := decodeURI(t, newEncoder(t).URL(pngURI(t, picture)))
	red, green, blue, _ := decoded.At(8, 8).RGBA()
	if red>>8 < 245 || green>>8 < 245 || blue>>8 < 245 {
		t.Fatalf("transparent area = %d,%d,%d, want white", red>>8, green>>8, blue>>8)
	}
	red, green, _, _ = decoded.At(48, 8).RGBA()
	if red>>8 < 180 || green>>8 > 30 {
		t.Fatalf("opaque area = %d,%d, want red", red>>8, green>>8)
	}
}

func TestUprightJPEGIsKeptByteForByte(t *testing.T) {
	encoder := newEncoder(t)
	uri := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpegBytes(t, texture(96, 64, 3)))
	encoded, stats := encoder.Request(pictureRequest(uri))
	if encoded.Messages[1].Parts[0].URL != uri {
		t.Fatal("an upright JPEG was re-encoded")
	}
	if stats.Kept != 1 || stats.Encoded != 0 || stats.Bytes != int64(len(uri)) {
		t.Fatalf("stats = %+v", stats)
	}
}

// withOrientation inserts an EXIF APP1 segment carrying the orientation tag.
func withOrientation(data []byte, orientation byte) []byte {
	tiff := []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0,
		0x12, 0x01, 3, 0, 1, 0, 0, 0, orientation, 0, 0, 0,
		0, 0, 0, 0}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	length := len(payload) + 2
	segment := append([]byte{0xFF, 0xE1, byte(length >> 8), byte(length)}, payload...)
	result := append([]byte{}, data[:2]...)
	result = append(result, segment...)
	return append(result, data[2:]...)
}

func TestJPEGWithEXIFRotationIsTurnedUpright(t *testing.T) {
	// A 60×40 picture whose left third is black and the rest white, stored
	// with orientation 6 (to be turned 90° clockwise): upright it is 40×60
	// with the black part at the top.
	picture := image.NewNRGBA(image.Rect(0, 0, 60, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 60; x++ {
			value := uint8(255)
			if x < 20 {
				value = 0
			}
			picture.SetNRGBA(x, y, color.NRGBA{value, value, value, 255})
		}
	}
	uri := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(withOrientation(jpegBytes(t, picture), 6))
	output := newEncoder(t).URL(uri)
	if output == uri {
		t.Fatal("a rotated JPEG was kept as it is")
	}
	decoded, _ := decodeURI(t, output)
	if bounds := decoded.Bounds(); bounds.Dx() != 40 || bounds.Dy() != 60 {
		t.Fatalf("bounds = %v, want 40×60", bounds)
	}
	top, _, _, _ := decoded.At(20, 5).RGBA()
	bottom, _, _, _ := decoded.At(20, 50).RGBA()
	if top>>8 > 40 || bottom>>8 < 215 {
		t.Fatalf("top = %d, bottom = %d: not turned clockwise", top>>8, bottom>>8)
	}
}

func TestEncodingIsCachedAndOriginsAreRemembered(t *testing.T) {
	encoder := newEncoder(t)
	uri := pngURI(t, texture(128, 128, 4))
	first := encoder.URL(uri)
	second := encoder.URL(uri)
	if first != second || encoder.CacheHits() != 1 {
		t.Fatalf("hits = %d, same = %v", encoder.CacheHits(), first == second)
	}
	// A request that already carries the encoded picture reports the bytes the
	// picture had before the chat layer encoded it.
	_, stats := encoder.Request(pictureRequest(first))
	if stats.OriginalBytes != int64(len(uri)) || stats.Bytes != int64(len(first)) || stats.Kept != 1 {
		t.Fatalf("stats = %+v (png %d, jpeg %d)", stats, len(uri), len(first))
	}
}

func TestOtherValuesAreLeftAlone(t *testing.T) {
	encoder := newEncoder(t)
	for _, value := range []string{"https://example.test/a.png", "data:image/png;base64,!!!", ""} {
		if output := encoder.URL(value); output != value {
			t.Fatalf("%q became %q", value, output)
		}
	}
	var none *Encoder
	if output := none.URL("data:image/png;base64,AAAA"); output != "data:image/png;base64,AAAA" {
		t.Fatal("a nil encoder changed a picture")
	}
	if _, err := New(Policy{Format: "webp", Quality: 95, MaximumPixels: 1}); err == nil {
		t.Fatal("an unknown format was accepted")
	}
	if _, err := New(Policy{Format: FormatJPEG, Quality: 0, MaximumPixels: 1}); err == nil {
		t.Fatal("quality 0 was accepted")
	}
}
