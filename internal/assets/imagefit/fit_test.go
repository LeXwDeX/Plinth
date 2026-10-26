package imagefit

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"testing"
)

var policy = Policy{
	Steps:           []string{StepLosslessPNG, StepLossy},
	JPEGQualities:   []int{95, 90, 85, 80, 75},
	MaxDecodePixels: 100_000_000,
}

// noisy is an image whose PNG encoding is large; gradient areas keep part of
// it compressible so the lossless step has something to gain.
func noisy(width, height int, alpha bool) *image.NRGBA {
	random := rand.New(rand.NewSource(276))
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			value := uint8(random.Intn(256))
			if x < width/2 {
				value = uint8(x + y)
			}
			opacity := uint8(255)
			if alpha {
				opacity = uint8(y % 256)
			}
			picture.SetNRGBA(x, y, color.NRGBA{R: value, G: uint8(x), B: uint8(y), A: opacity})
		}
	}
	return picture
}

func encodePNG(t *testing.T, picture image.Image, level png.CompressionLevel) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: level}).Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func decode(t *testing.T, body []byte) image.Image {
	t.Helper()
	decoded, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestLosslessPNGKeepsPixelsAndSize(t *testing.T) {
	picture := noisy(256, 256, false)
	source := encodePNG(t, picture, png.NoCompression)
	best := encodePNG(t, picture, png.BestCompression)
	if len(best) >= len(source) {
		t.Fatalf("fixture not compressible: %d >= %d", len(best), len(source))
	}
	result, err := Fit(source, Target{MaxBytes: int64(len(best)), MediaTypes: []string{"image/png", "image/jpeg"}}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if result.Method != StepLosslessPNG || result.MediaType != "image/png" || int64(len(result.Body)) > int64(len(best)) {
		t.Fatalf("result = %s %s %d bytes", result.Method, result.MediaType, len(result.Body))
	}
	decoded := decode(t, result.Body)
	if decoded.Bounds() != picture.Bounds() || result.Width != 256 || result.Height != 256 {
		t.Fatalf("size changed: %v", decoded.Bounds())
	}
	for y := range 256 {
		for x := range 256 {
			if color.NRGBAModel.Convert(decoded.At(x, y)) != picture.At(x, y) {
				t.Fatalf("lossless step changed pixel (%d,%d)", x, y)
			}
		}
	}
}

func TestLossyStepFitsOpaqueImageAtSameSize(t *testing.T) {
	picture := noisy(256, 256, false)
	source := encodePNG(t, picture, png.BestCompression)
	limit := int64(len(source)) / 3
	result, err := Fit(source, Target{MaxBytes: limit, MediaTypes: []string{"image/png", "image/jpeg"}}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if result.MediaType != "image/jpeg" || int64(len(result.Body)) > limit || result.Method[:6] != "jpeg-q" {
		t.Fatalf("result = %s %s %d bytes, limit %d", result.Method, result.MediaType, len(result.Body), limit)
	}
	if decoded := decode(t, result.Body); decoded.Bounds().Dx() != 256 || decoded.Bounds().Dy() != 256 {
		t.Fatalf("size changed: %v", decoded.Bounds())
	}
}

// A PNG whose alpha is all 255 (as some generators write) is opaque and may
// become JPEG.
func TestFullyOpaqueAlphaChannelMayBecomeJPEG(t *testing.T) {
	source := encodePNG(t, noisy(128, 128, false), png.BestCompression)
	result, err := Fit(source, Target{MaxBytes: int64(len(source)) - 1, MediaTypes: []string{"image/jpeg"}}, policy)
	if err != nil || result.MediaType != "image/jpeg" {
		t.Fatalf("result=%v error=%v", result.Method, err)
	}
}

// Transparency is never flattened: without a WebP encoder an image with real
// alpha only gets the lossless step, and keeps its alpha.
func TestTransparentImageKeepsAlphaOrIsRefused(t *testing.T) {
	picture := noisy(128, 128, true)
	source := encodePNG(t, picture, png.NoCompression)
	best := encodePNG(t, picture, png.BestCompression)
	result, err := Fit(source, Target{MaxBytes: int64(len(best)), MediaTypes: []string{"image/png", "image/jpeg"}}, policy)
	if err != nil || result.MediaType != "image/png" {
		t.Fatalf("result=%v error=%v", result.Method, err)
	}
	decoded := decode(t, result.Body)
	if decoded.Bounds() != picture.Bounds() {
		t.Fatalf("size changed: %v", decoded.Bounds())
	}
	if _, _, _, alpha := decoded.At(3, 10).RGBA(); alpha != 0x0a0a {
		t.Fatalf("alpha lost: %#x", alpha)
	}
	_, err = Fit(source, Target{MaxBytes: int64(len(best)) / 4, MediaTypes: []string{"image/png", "image/jpeg"}}, policy)
	var insufficient *InsufficientError
	if !errors.As(err, &insufficient) || insufficient.Smallest != int64(len(best)) || !insufficient.Transparent {
		t.Fatalf("error = %v", err)
	}
}

// With a flatten colour (the upstream drops alpha anyway) the lossy step
// composites over it first, keeps the size and marks the method.
func TestFlattenCompositesThenJPEG(t *testing.T) {
	picture := noisy(128, 128, true)
	source := encodePNG(t, picture, png.BestCompression)
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	result, err := Fit(source, Target{MaxBytes: int64(len(source)) / 3, MediaTypes: []string{"image/png", "image/jpeg"}, Flatten: &white}, policy)
	if err != nil || result.MediaType != "image/jpeg" || result.Method[:len(FlattenPrefix)+6] != FlattenPrefix+"jpeg-q" {
		t.Fatalf("result=%v error=%v", result.Method, err)
	}
	decoded := decode(t, result.Body)
	if decoded.Bounds() != picture.Bounds() {
		t.Fatalf("size changed: %v", decoded.Bounds())
	}
	// Row 0 is fully transparent: it becomes the background colour.
	if r, g, b, _ := decoded.At(100, 0).RGBA(); r>>8 < 240 || g>>8 < 240 || b>>8 < 240 {
		t.Fatalf("transparent pixel = %d,%d,%d, want white", r>>8, g>>8, b>>8)
	}
	// An opaque image is never marked as flattened.
	opaqueSource := encodePNG(t, noisy(128, 128, false), png.BestCompression)
	result, err = Fit(opaqueSource, Target{MaxBytes: int64(len(opaqueSource)) / 3, MediaTypes: []string{"image/jpeg"}, Flatten: &white}, policy)
	if err != nil || result.Method[:6] != "jpeg-q" {
		t.Fatalf("opaque result=%v error=%v", result.Method, err)
	}
}

func TestParseHexColor(t *testing.T) {
	if got, err := ParseHexColor("#12ab0f"); err != nil || got != (color.RGBA{R: 0x12, G: 0xab, B: 0x0f, A: 0xff}) {
		t.Fatalf("got %v %v", got, err)
	}
	for _, bad := range []string{"", "#fff", "12ab0f0", "#12ab0g"} {
		if _, err := ParseHexColor(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestInsufficientReportsSmallestSizeReached(t *testing.T) {
	source := encodePNG(t, noisy(128, 128, false), png.BestCompression)
	_, err := Fit(source, Target{MaxBytes: 10, MediaTypes: []string{"image/png", "image/jpeg"}}, policy)
	var insufficient *InsufficientError
	if !errors.As(err, &insufficient) || insufficient.Smallest <= 10 || insufficient.Smallest >= int64(len(source)) {
		t.Fatalf("error = %v", err)
	}
}

func TestUnreadableAndOverDecodeBound(t *testing.T) {
	if _, err := Fit([]byte("not an image"), Target{MaxBytes: 10}, policy); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("error = %v", err)
	}
	source := encodePNG(t, noisy(64, 64, false), png.BestCompression)
	bounded := policy
	bounded.MaxDecodePixels = 64*64 - 1
	if _, err := Fit(source, Target{MaxBytes: 10}, bounded); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("error = %v", err)
	}
}

func TestAccepts(t *testing.T) {
	for _, test := range []struct {
		accepted []string
		media    string
		want     bool
	}{
		{nil, "image/png", true},
		{[]string{"image/*"}, "image/webp", true},
		{[]string{"image/png", "image/jpeg"}, "image/webp", false},
		{[]string{"image/jpeg"}, "image/jpeg", true},
	} {
		if got := Accepts(test.accepted, test.media); got != test.want {
			t.Fatalf("Accepts(%v, %s) = %v", test.accepted, test.media, got)
		}
	}
}
