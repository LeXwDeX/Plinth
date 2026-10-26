package visionimage

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

const inlineLimit = 4 << 20

func defaultPolicy(t *testing.T) catalog.ChatVisionDerivativeSchema {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return c.Application().Chat.Context.VisionDerivative
}

// photo draws a busy picture (gradient and grain) so its PNG is several MB, like a phone photo.
func photo(t *testing.T, width, height int, alpha bool) []byte {
	t.Helper()
	random := rand.New(rand.NewSource(int64(width*7 + height)))
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			grain := func(base int) uint8 { return uint8(min(255, max(0, base+random.Intn(33)-16))) }
			a := uint8(255)
			if alpha && x < width/2 {
				a = 0
			}
			picture.SetNRGBA(x, y, color.NRGBA{R: grain(x * 255 / width), G: grain(y * 255 / height), B: grain(128), A: a})
		}
	}
	var buffer bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func size(t *testing.T, body []byte) (int, int, string) {
	t.Helper()
	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return config.Width, config.Height, format
}

func TestLargePictureBecomesAScaledJPEGWithinTheLimit(t *testing.T) {
	deriver, err := New(defaultPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	source := photo(t, 3000, 2000, false)
	if len(source) <= inlineLimit {
		t.Fatalf("the source must be over the inline limit, got %d bytes", len(source))
	}
	made, err := deriver.Derive(context.Background(), source, "", inlineLimit)
	if err != nil {
		t.Fatal(err)
	}
	width, height, format := size(t, made.Body)
	if made.MediaType != "image/jpeg" || format != "jpeg" || len(made.Body) > inlineLimit {
		t.Fatalf("copy is %s/%s, %d bytes", made.MediaType, format, len(made.Body))
	}
	// Scaled to the first step (2048 long side), the aspect ratio kept.
	if width != 2048 || height != 1365 || made.Width != width || made.Height != height {
		t.Fatalf("copy is %dx%d (reported %dx%d)", width, height, made.Width, made.Height)
	}
}

func TestCopyIsMadeOncePerSourceAndLimit(t *testing.T) {
	deriver, err := New(defaultPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	source := photo(t, 1800, 1200, false)
	digest := "ab" + string(bytes.Repeat([]byte("0"), 62))
	first, err := deriver.Derive(context.Background(), source, digest, 256<<10)
	if err != nil {
		t.Fatal(err)
	}
	cached, ok := deriver.Cached(digest, 256<<10)
	if !ok || !bytes.Equal(cached.Body, first.Body) {
		t.Fatal("the copy must be cached by the source digest")
	}
	second, err := deriver.Derive(context.Background(), source, digest, 256<<10)
	if err != nil || !bytes.Equal(second.Body, first.Body) || deriver.made != 1 {
		t.Fatalf("second derive: err %v, made %d", err, deriver.made)
	}
	// Another limit is another copy.
	if _, ok := deriver.Cached(digest, 128<<10); ok {
		t.Fatal("a copy is cached per limit")
	}
}

func TestSmallerStepsAndQualitiesAreTriedUntilTheCopyFits(t *testing.T) {
	deriver, err := New(defaultPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	made, err := deriver.Derive(context.Background(), photo(t, 1800, 1200, false), "", 120<<10)
	if err != nil {
		t.Fatal(err)
	}
	width, height, _ := size(t, made.Body)
	// It had to go below its own size, keeping the 3:2 ratio (within rounding).
	if len(made.Body) > 120<<10 || width >= 1800 || abs(width*2-height*3) > 3 {
		t.Fatalf("copy %dx%d, %d bytes", width, height, len(made.Body))
	}
}

func TestTransparencyIsKeptAsPNGWhenItFitsAndFlattenedOnWhiteOtherwise(t *testing.T) {
	deriver, err := New(defaultPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	source := photo(t, 900, 600, true)
	kept, err := deriver.Derive(context.Background(), source, "", int64(len(source))-1)
	if err != nil {
		t.Fatal(err)
	}
	if kept.MediaType != "image/png" {
		t.Fatalf("a transparent picture that fits as PNG stays PNG, got %s", kept.MediaType)
	}
	flat, err := deriver.Derive(context.Background(), source, "", 60<<10)
	if err != nil {
		t.Fatal(err)
	}
	if flat.MediaType != "image/jpeg" {
		t.Fatalf("a transparent picture too large as PNG becomes JPEG, got %s", flat.MediaType)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(flat.Body))
	if err != nil {
		t.Fatal(err)
	}
	// The transparent left half is composited over the Schema colour (white).
	if r, g, b, _ := decoded.At(5, decoded.Bounds().Dy()/2).RGBA(); r < 0xf000 || g < 0xf000 || b < 0xf000 {
		t.Fatalf("transparent pixels must be white, got %x %x %x", r, g, b)
	}
}

func TestPictureThatCannotFitIsLeftToItsLabel(t *testing.T) {
	deriver, err := New(defaultPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deriver.Derive(context.Background(), photo(t, 900, 600, false), "", 64); !errors.Is(err, ErrUnfit) {
		t.Fatalf("err = %v, want ErrUnfit", err)
	}
	if _, err := deriver.Derive(context.Background(), []byte("not a picture"), "", inlineLimit); !errors.Is(err, ErrUnfit) {
		t.Fatalf("err = %v, want ErrUnfit", err)
	}
	policy := defaultPolicy(t)
	policy.MaximumPixels = 1000
	bounded, err := New(policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bounded.Derive(context.Background(), photo(t, 300, 200, false), "", 1024); !errors.Is(err, ErrUnfit) {
		t.Fatalf("a picture over the decode bound: err = %v", err)
	}
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
