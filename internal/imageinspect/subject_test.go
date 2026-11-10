package imageinspect

import (
	"image"
	"image/color"
	"testing"
)

func subjectPicture(width, height int, paint func(x, y int) color.NRGBA) *image.NRGBA {
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			picture.SetNRGBA(x, y, paint(x, y))
		}
	}
	return picture
}

func TestSubjectSegmentsFindsTheRectOnAPlainBackground(t *testing.T) {
	background := color.NRGBA{R: 250, G: 250, B: 250, A: 255}
	fill := color.NRGBA{R: 200, G: 30, B: 30, A: 255}
	picture := subjectPicture(20, 20, func(x, y int) color.NRGBA {
		if x >= 4 && x < 14 && y >= 6 && y < 16 {
			return fill
		}
		return background
	})
	subject := SubjectSegments(picture, 32)
	if !subject.HasSubject || subject.Pixels != 100 || subject.Box != [4]int{4, 6, 13, 15} {
		t.Fatalf("subject = %+v", subject)
	}
	if subject.Ratio != 0.25 || subject.Width != 20 || subject.Height != 20 {
		t.Fatalf("ratio = %v (%dx%d)", subject.Ratio, subject.Width, subject.Height)
	}
	if subject.Mean != [3]float64{200, 30, 30} {
		t.Fatalf("mean = %v", subject.Mean)
	}
}

func TestSubjectSegmentsUsesAlphaWhenThePictureHasTransparency(t *testing.T) {
	picture := subjectPicture(8, 8, func(x, y int) color.NRGBA {
		if x >= 2 && x < 6 && y >= 1 && y < 7 {
			return color.NRGBA{R: 10, G: 20, B: 30, A: 255}
		}
		return color.NRGBA{}
	})
	subject := SubjectSegments(picture, 32)
	if !subject.HasSubject || subject.Pixels != 24 || subject.Box != [4]int{2, 1, 5, 6} {
		t.Fatalf("subject = %+v", subject)
	}
	if subject.Mean != [3]float64{10, 20, 30} {
		t.Fatalf("mean = %v", subject.Mean)
	}
}

func TestSubjectSegmentsReportsAnEmptySubject(t *testing.T) {
	plain := subjectPicture(6, 6, func(int, int) color.NRGBA {
		return color.NRGBA{R: 12, G: 12, B: 12, A: 255}
	})
	subject := SubjectSegments(plain, 32)
	if subject.HasSubject || subject.Pixels != 0 || subject.Box[2] != -1 || subject.Ratio != 0 {
		t.Fatalf("empty subject = %+v", subject)
	}
}

func TestCompareSubjectsIgnoresTranslationAndScale(t *testing.T) {
	background := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	fill := color.NRGBA{R: 220, G: 40, B: 60, A: 255}
	left := subjectPicture(32, 32, func(x, y int) color.NRGBA {
		if x >= 4 && x < 12 && y >= 8 && y < 24 {
			return fill
		}
		return background
	})
	right := subjectPicture(32, 32, func(x, y int) color.NRGBA {
		if x >= 16 && x < 28 && y >= 4 && y < 28 {
			return fill
		}
		return background
	})
	comparison := CompareSubjects(left, right, 32, 64)
	if comparison.SilhouetteIOU < 0.99 {
		t.Fatalf("translated and scaled square: iou = %v", comparison.SilhouetteIOU)
	}
	if comparison.BBoxAspectDelta > 0.01 {
		t.Fatalf("aspect delta = %v", comparison.BBoxAspectDelta)
	}
	if comparison.DominantColorDistance > 0.01 {
		t.Fatalf("colour distance = %v", comparison.DominantColorDistance)
	}
	if comparison.DominantReference != "#dc283c" || comparison.DominantRender != "#dc283c" {
		t.Fatalf("dominant colours = %q / %q", comparison.DominantReference, comparison.DominantRender)
	}
}

func TestCompareSubjectsReportsDifferentShapesAndColours(t *testing.T) {
	background := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	left := subjectPicture(32, 32, func(x, y int) color.NRGBA {
		if x >= 4 && x < 12 && y >= 8 && y < 24 {
			return color.NRGBA{R: 220, G: 40, B: 60, A: 255}
		}
		return background
	})
	right := subjectPicture(32, 32, func(x, y int) color.NRGBA {
		// An L: the box is the full rectangle but the upper-right quarter is
		// missing, so the normalized silhouette cannot match the left one.
		if x >= 4 && x < 28 && y >= 8 && y < 24 && !(x >= 16 && y < 16) {
			return color.NRGBA{R: 20, G: 120, B: 220, A: 255}
		}
		return background
	})
	comparison := CompareSubjects(left, right, 32, 64)
	if comparison.SilhouetteIOU <= 0 || comparison.SilhouetteIOU >= 1 {
		t.Fatalf("iou = %v, want strictly between 0 and 1", comparison.SilhouetteIOU)
	}
	if comparison.BBoxAspectDelta <= 0 {
		t.Fatalf("aspect delta = %v, want positive", comparison.BBoxAspectDelta)
	}
	if comparison.DominantColorDistance <= 0 {
		t.Fatalf("colour distance = %v, want positive", comparison.DominantColorDistance)
	}
	if comparison.ReferenceSubjectRatio <= 0 || comparison.RenderSubjectRatio <= 0 {
		t.Fatalf("ratios = %v / %v", comparison.ReferenceSubjectRatio, comparison.RenderSubjectRatio)
	}
}

func TestCompareSubjectsHandlesAnEmptySide(t *testing.T) {
	background := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	left := subjectPicture(8, 8, func(int, int) color.NRGBA { return background })
	right := subjectPicture(8, 8, func(x, y int) color.NRGBA {
		if x >= 2 && x < 6 && y >= 2 && y < 6 {
			return color.NRGBA{R: 1, G: 2, B: 3, A: 255}
		}
		return background
	})
	comparison := CompareSubjects(left, right, 32, 128)
	if comparison.SilhouetteIOU != 0 || comparison.BBoxAspectDelta != 0 || comparison.ReferenceSubjectRatio != 0 {
		t.Fatalf("comparison = %+v", comparison)
	}
	if comparison.RenderSubjectRatio <= 0 || comparison.DominantRender == "" {
		t.Fatalf("render facts = %+v", comparison)
	}
}

func TestDecodeEnforcesTheByteAndPixelLimits(t *testing.T) {
	data := encodePNG(t, 8, 8, func(int, int) color.NRGBA {
		return color.NRGBA{R: 1, G: 2, B: 3, A: 255}
	})
	if _, err := Decode(data, Limits{MaxBytes: int64(len(data)) - 1}); err == nil {
		t.Fatal("a picture over the byte limit must be refused")
	}
	if _, err := Decode(data, Limits{MaxPixels: 63}); err == nil {
		t.Fatal("a picture over the pixel limit must be refused")
	}
	picture, err := Decode(data, Limits{MaxBytes: int64(len(data)), MaxPixels: 64})
	if err != nil || picture.Bounds().Dx() != 8 || picture.Bounds().Dy() != 8 {
		t.Fatalf("decode = %v,%v", picture, err)
	}
	if _, err := Decode([]byte("not a picture"), Limits{}); err == nil {
		t.Fatal("garbage must not decode")
	}
}
