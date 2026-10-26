package etl_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// validPNGFixture produces a real, deterministic 1x1 PNG for fixtures that
// represent successfully stored image assets. Corruption tests should keep
// their intentionally invalid byte sequences instead.
func validPNGFixture(t testing.TB) []byte {
	t.Helper()
	return validPNGFixtureColor(t, color.NRGBA{R: 20, G: 40, B: 60, A: 255})
}

func validPNGFixtureColor(t testing.TB, pixel color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.SetNRGBA(0, 0, pixel)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}
