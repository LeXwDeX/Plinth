package imaging_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/imaging"
)

func pngBytes(t *testing.T, picture image.Image) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func solid(width, height int, pixel color.NRGBA) *image.NRGBA {
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			picture.SetNRGBA(x, y, pixel)
		}
	}
	return picture
}

func decodeNRGBA(t *testing.T, data []byte) *image.NRGBA {
	t.Helper()
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	bounds := decoded.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			out.SetNRGBA(x, y, color.NRGBAModel.Convert(decoded.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.NRGBA))
		}
	}
	return out
}

// #244: the preview dims the kept region and tints the selected one.
func TestRenderSelectionPreviewDimsKeptAndTintsSelected(t *testing.T) {
	picture := solid(10, 10, color.NRGBA{R: 200, G: 100, B: 50, A: 255})
	mask := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			if x < 5 {
				continue // transparent: selected
			}
			mask.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	encoded, err := imaging.RenderSelectionPreview(pngBytes(t, picture), pngBytes(t, mask), imaging.SelectionPreviewStyle{
		MaxPixels: 1000, DimOpacity: 0.5, TintColor: "#0000ff", TintOpacity: 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	preview := decodeNRGBA(t, encoded)
	kept := preview.NRGBAAt(8, 5)
	if kept.R != 100 || kept.G != 50 || kept.B != 25 || kept.A != 255 {
		t.Fatalf("kept pixel = %+v", kept)
	}
	selected := preview.NRGBAAt(2, 5)
	if selected.R != 100 || selected.G != 50 || selected.B != 153 || selected.A != 255 {
		t.Fatalf("selected pixel = %+v", selected)
	}
}

// #244: an oversized snapshot is downscaled to the Schema bound; a mask of a
// different size is scaled with it.
func TestRenderSelectionPreviewRespectsThePixelBound(t *testing.T) {
	picture := solid(100, 100, color.NRGBA{R: 200, G: 100, B: 50, A: 255})
	mask := image.NewNRGBA(image.Rect(0, 0, 50, 50))
	for y := 0; y < 50; y++ {
		for x := 0; x < 50; x++ {
			if x < 25 {
				continue
			}
			mask.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	encoded, err := imaging.RenderSelectionPreview(pngBytes(t, picture), pngBytes(t, mask), imaging.SelectionPreviewStyle{
		MaxPixels: 2500, DimOpacity: 0.5, TintColor: "#ff8a00", TintOpacity: 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	preview := decodeNRGBA(t, encoded)
	if preview.Bounds().Dx()*preview.Bounds().Dy() > 2500 {
		t.Fatalf("preview size = %v", preview.Bounds())
	}
	if _, err := imaging.RenderSelectionPreview(pngBytes(t, picture), []byte("not a picture"),
		imaging.SelectionPreviewStyle{}); err == nil {
		t.Fatal("unreadable mask composed")
	}
}
