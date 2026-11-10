package imageinspect

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"
)

func encodePNG(t *testing.T, width, height int, paint func(x, y int) color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, paint(x, y))
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buffer.Bytes()
}

func square(minX, minY, maxX, maxY int, fill color.NRGBA) func(x, y int) color.NRGBA {
	return func(x, y int) color.NRGBA {
		if x >= minX && x <= maxX && y >= minY && y <= maxY {
			return fill
		}
		return color.NRGBA{}
	}
}

func closeTo(t *testing.T, got, want, tolerance float64, label string) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Fatalf("%s = %.4f, want %.4f (+/- %.4f)", label, got, want, tolerance)
	}
}

func TestTransparentSquare(t *testing.T) {
	data := encodePNG(t, 100, 100, square(40, 40, 59, 59, color.NRGBA{R: 255, A: 255}))
	report, err := Inspect(data, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.HasAlpha || report.Format != "png" {
		t.Fatalf("report = %+v", report)
	}
	closeTo(t, report.SubjectRatio, 0.04, 0.002, "subject ratio")
	if report.SubjectBBox != [4]int{40, 40, 59, 59} {
		t.Fatalf("bbox = %v", report.SubjectBBox)
	}
	if report.TouchesEdge || report.ComponentCount != 1 {
		t.Fatalf("edge=%t components=%d", report.TouchesEdge, report.ComponentCount)
	}
}

func TestOpaquePlainBackground(t *testing.T) {
	data := encodePNG(t, 100, 100, func(x, y int) color.NRGBA {
		if x >= 30 && x <= 69 && y >= 30 && y <= 69 {
			return color.NRGBA{R: 200, G: 30, B: 30, A: 255}
		}
		return color.NRGBA{R: 250, G: 250, B: 250, A: 255}
	})
	report, err := Inspect(data, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if report.HasAlpha {
		t.Fatal("opaque png reported alpha")
	}
	closeTo(t, report.BackgroundSpread, 0, 0.01, "background spread")
	closeTo(t, report.SubjectRatio, 0.16, 0.002, "subject ratio")
	if report.TouchesEdge || report.ComponentCount != 1 {
		t.Fatalf("edge=%t components=%d", report.TouchesEdge, report.ComponentCount)
	}
}

func TestTwoComponentsAndEdge(t *testing.T) {
	data := encodePNG(t, 100, 100, func(x, y int) color.NRGBA {
		left := x >= 0 && x <= 19 && y >= 40 && y <= 59
		right := x >= 70 && x <= 89 && y >= 40 && y <= 59
		if left || right {
			return color.NRGBA{R: 255, A: 255}
		}
		return color.NRGBA{}
	})
	report, err := Inspect(data, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if report.ComponentCount != 2 {
		t.Fatalf("components = %d, want 2", report.ComponentCount)
	}
	if !report.TouchesEdge {
		t.Fatal("a square on the left edge was not reported")
	}
	closeTo(t, report.LargestComponentRatio, 0.5, 0.01, "largest component")
}

func TestBusyBorder(t *testing.T) {
	data := encodePNG(t, 100, 100, func(x, y int) color.NRGBA {
		if x == 0 || y == 0 || x == 99 || y == 99 {
			if (x+y)%2 == 0 {
				return color.NRGBA{A: 255}
			}
			return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		}
		return color.NRGBA{R: 250, G: 250, B: 250, A: 255}
	})
	report, err := Inspect(data, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if report.BackgroundSpread < 50 {
		t.Fatalf("busy border spread = %.1f", report.BackgroundSpread)
	}
}

func TestLimits(t *testing.T) {
	data := encodePNG(t, 10, 10, func(x, y int) color.NRGBA { return color.NRGBA{A: 255} })
	if _, err := Inspect(data, Limits{MaxBytes: 10}); err == nil {
		t.Fatal("MaxBytes was not enforced")
	}
	if _, err := Inspect(data, Limits{MaxPixels: 10}); err == nil {
		t.Fatal("MaxPixels was not enforced")
	}
	if _, err := Inspect([]byte("not an image"), Limits{}); err == nil {
		t.Fatal("invalid data was accepted")
	}
}

func TestReview(t *testing.T) {
	transparent := encodePNG(t, 100, 100, square(30, 30, 69, 69, color.NRGBA{R: 255, A: 255}))
	report, err := Inspect(transparent, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	checklist := Checklist{RequirePlainBackground: true, MaxBackgroundSpread: 20,
		MinSubjectRatio: 0.05, MaxSubjectRatio: 0.9, RequireEdgeClear: true, MaxComponents: 1}
	if checks := report.Review(checklist); Failed(checks) {
		t.Fatalf("a clean transparent square failed: %+v", checks)
	}

	busy := encodePNG(t, 100, 100, func(x, y int) color.NRGBA {
		if x == 0 || y == 0 || x == 99 || y == 99 {
			if (x+y)%2 == 0 {
				return color.NRGBA{A: 255}
			}
			return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		}
		return color.NRGBA{R: 250, G: 250, B: 250, A: 255}
	})
	report, err = Inspect(busy, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	checks := report.Review(Checklist{RequirePlainBackground: true, MaxBackgroundSpread: 20})
	if !Failed(checks) {
		t.Fatal("a busy background passed the plain-background check")
	}
}
