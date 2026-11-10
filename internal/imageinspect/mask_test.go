package imageinspect_test

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/imageinspect"
)

func encode(t *testing.T, picture image.Image) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func decode(t *testing.T, data []byte) *image.NRGBA {
	t.Helper()
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	bounds := decoded.Bounds()
	nrgba := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			nrgba.SetNRGBA(x, y, color.NRGBAModel.Convert(decoded.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.NRGBA))
		}
	}
	return nrgba
}

func TestMeasureMaskReportsTheTransparentRegion(t *testing.T) {
	mask := image.NewNRGBA(image.Rect(0, 0, 20, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 20; x++ {
			if x >= 2 && x < 7 && y >= 3 && y < 6 {
				continue
			}
			mask.SetNRGBA(x, y, color.NRGBA{255, 255, 255, 255})
		}
	}
	report, err := imageinspect.MeasureMask(encode(t, mask), imageinspect.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Width != 20 || report.Height != 10 || report.BBox != [4]int{2, 3, 6, 5} {
		t.Fatalf("report = %+v", report)
	}
	if want := 5.0 * 3.0 / (20.0 * 10.0); report.Coverage != want {
		t.Fatalf("coverage = %v, want %v", report.Coverage, want)
	}
}

func TestMeasureMaskRejectsNonPNGAndOversizedPictures(t *testing.T) {
	jpegPicture := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, jpegPicture); err != nil {
		t.Fatal(err)
	}
	if _, err := imageinspect.MeasureMask(append([]byte{0xff, 0xd8}, encoded.Bytes()...), imageinspect.Limits{}); err == nil {
		t.Fatal("corrupt mask measured")
	}
	if _, err := imageinspect.MeasureMask(encoded.Bytes(), imageinspect.Limits{MaxPixels: 1}); err == nil {
		t.Fatal("oversized mask measured")
	}
	if _, err := imageinspect.MeasureMask(encoded.Bytes(), imageinspect.Limits{MaxBytes: 1}); err == nil {
		t.Fatal("oversized bytes measured")
	}
}

func TestRenderRegionMaskAndInvert(t *testing.T) {
	picture := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	encoded := encode(t, picture)
	mask, report, err := imageinspect.RenderRegionMask(encoded, 2, 2, 6, 6, false, imageinspect.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if report.BBox != [4]int{2, 2, 5, 5} || report.Coverage != 16.0/100.0 {
		t.Fatalf("region report = %+v", report)
	}
	decoded := decode(t, mask)
	if _, _, _, alpha := decoded.At(3, 3).RGBA(); alpha>>8 >= 128 {
		t.Fatalf("region pixel is opaque")
	}
	if _, _, _, alpha := decoded.At(0, 0).RGBA(); alpha>>8 < 128 {
		t.Fatalf("kept pixel is transparent")
	}
	inverted, invertedReport, err := imageinspect.InvertMask(mask, imageinspect.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if invertedReport.Coverage != 84.0/100.0 {
		t.Fatalf("inverted coverage = %v", invertedReport.Coverage)
	}
	invertedPicture := decode(t, inverted)
	if _, _, _, alpha := invertedPicture.At(3, 3).RGBA(); alpha>>8 < 128 {
		t.Fatalf("inverted region pixel is transparent")
	}
	if _, _, _, alpha := invertedPicture.At(0, 0).RGBA(); alpha>>8 >= 128 {
		t.Fatalf("inverted kept pixel is opaque")
	}
	if _, _, err := imageinspect.RenderRegionMask(encoded, 5, 5, 5, 6, false, imageinspect.Limits{}); err == nil {
		t.Fatal("empty region rendered")
	}
	var region *imageinspect.RegionError
	if _, _, err := imageinspect.RenderRegionMask(encoded, -1, 0, 6, 6, false, imageinspect.Limits{}); !errors.As(err, &region) {
		t.Fatalf("outside region err = %v", err)
	}
}

func TestRenderSubjectMaskKeepsTheSubjectEditable(t *testing.T) {
	picture := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	for y := 2; y < 8; y++ {
		for x := 3; x < 7; x++ {
			picture.SetNRGBA(x, y, color.NRGBA{R: 200, G: 40, B: 40, A: 255})
		}
	}
	mask, report, err := imageinspect.RenderSubjectMask(encode(t, picture), imageinspect.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if report.BBox != [4]int{3, 2, 6, 7} || report.Coverage != 24.0/100.0 {
		t.Fatalf("subject report = %+v", report)
	}
	decoded := decode(t, mask)
	if _, _, _, alpha := decoded.At(4, 4).RGBA(); alpha>>8 >= 128 {
		t.Fatal("subject pixel is opaque")
	}
	if _, _, _, alpha := decoded.At(0, 0).RGBA(); alpha>>8 < 128 {
		t.Fatal("background pixel is transparent")
	}
	blank := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			blank.SetNRGBA(x, y, color.NRGBA{255, 255, 255, 255})
		}
	}
	if _, _, err := imageinspect.RenderSubjectMask(encode(t, blank), imageinspect.Limits{}); !errors.Is(err, imageinspect.ErrNoSubject) {
		t.Fatalf("blank subject err = %v", err)
	}
}
