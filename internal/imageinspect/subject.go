package imageinspect

// Deterministic subject comparison for the 3D reference check (#246): the
// reference picture and the model render are reduced to a subject mask, each
// mask is cropped to its bounding box and scaled to the same square, and the
// two silhouettes are compared. The result is evidence for the Agent, never a
// verdict: the renderer is approximate and frames each subject by its own
// bounding box.

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

// Decode reads a picture within the byte and pixel limits. It returns the
// pixels in NRGBA form so every caller measures the same bytes.
func Decode(data []byte, limits Limits) (*image.NRGBA, error) {
	picture, _, err := decodeNRGBA(data, limits)
	return picture, err
}

// decodeNRGBA is Decode that also names the container format ("png", "jpeg",
// "webp"), so Inspect and the standalone decoder share one set of limits and
// one set of refusal messages.
func decodeNRGBA(data []byte, limits Limits) (*image.NRGBA, string, error) {
	if limits.MaxBytes > 0 && int64(len(data)) > limits.MaxBytes {
		return nil, "", fmt.Errorf("image is %d bytes, over the %d-byte limit", len(data), limits.MaxBytes)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("decode image header: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return nil, "", errors.New("image has no pixels")
	}
	pixels := int64(config.Width) * int64(config.Height)
	if limits.MaxPixels > 0 && pixels > limits.MaxPixels {
		return nil, "", fmt.Errorf("image is %d pixels, over the %d-pixel limit", pixels, limits.MaxPixels)
	}
	decoded, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("decode image: %w", err)
	}
	bounds := decoded.Bounds()
	nrgba := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(nrgba, nrgba.Bounds(), decoded, bounds.Min, draw.Src)
	return nrgba, format, nil
}

// Subject is one picture's deterministic subject facts.
type Subject struct {
	Width  int
	Height int
	// Mask marks the subject pixels, row-major.
	Mask []bool
	// Box is the inclusive pixel box x0,y0,x1,y1; x1 is -1 when empty.
	Box [4]int
	// Pixels is the subject pixel count and Ratio its share of the picture.
	Pixels int
	Ratio  float64
	// Mean is the mean RGB colour of the subject pixels.
	Mean [3]float64
	// HasSubject is false when no pixel passed the threshold.
	HasSubject bool
}

// SubjectSegments reduces one picture to its subject. A picture with any
// transparency uses alpha as the mask (a pixel with alpha >= 16 is subject);
// otherwise the mean border colour is the background and a pixel is subject
// when its squared RGB distance is above threshold squared.
func SubjectSegments(picture *image.NRGBA, threshold float64) Subject {
	subject := Subject{Width: picture.Bounds().Dx(), Height: picture.Bounds().Dy(), Box: [4]int{0, 0, -1, -1}}
	width, height := subject.Width, subject.Height
	subject.Mask = make([]bool, width*height)
	hasAlpha := false
	for y := 0; y < height && !hasAlpha; y++ {
		row := picture.Pix[y*picture.Stride:]
		for x := 0; x < width; x++ {
			if row[4*x+3] < 255 {
				hasAlpha = true
				break
			}
		}
	}
	background := [3]float64{}
	limit := threshold * threshold
	if !hasAlpha {
		background, _ = borderBackground(picture)
	}
	var sum [3]float64
	x0, y0, x1, y1 := width, height, -1, -1
	for y := 0; y < height; y++ {
		row := picture.Pix[y*picture.Stride:]
		for x := 0; x < width; x++ {
			selected := false
			if hasAlpha {
				selected = row[4*x+3] >= 16
			} else {
				selected = colourDistance(row[4*x:4*x+3], background) > limit
			}
			if !selected {
				continue
			}
			subject.Mask[y*width+x] = true
			subject.Pixels++
			sum[0] += float64(row[4*x])
			sum[1] += float64(row[4*x+1])
			sum[2] += float64(row[4*x+2])
			if x < x0 {
				x0 = x
			}
			if x > x1 {
				x1 = x
			}
			if y < y0 {
				y0 = y
			}
			if y > y1 {
				y1 = y
			}
		}
	}
	if subject.Pixels > 0 {
		subject.HasSubject = true
		subject.Box = [4]int{x0, y0, x1, y1}
		subject.Mean = [3]float64{sum[0] / float64(subject.Pixels), sum[1] / float64(subject.Pixels), sum[2] / float64(subject.Pixels)}
		subject.Ratio = float64(subject.Pixels) / float64(width*height)
	}
	return subject
}

// SubjectComparison is the deterministic reference-vs-render report.
type SubjectComparison struct {
	// SilhouetteIOU is the intersection-over-union of the two masks after each
	// is cropped to its box and scaled to NormalizedSide.
	SilhouetteIOU float64 `json:"silhouette_iou"`
	// BBoxAspectDelta is |aspect(reference) - aspect(render)|, where aspect is
	// width/height of the subject box.
	BBoxAspectDelta float64 `json:"bbox_aspect_delta"`
	// DominantColorDistance is the Euclidean RGB distance of the two subject
	// mean colours, on a 0..255 scale.
	DominantColorDistance float64 `json:"dominant_color_distance"`
	DominantReference     string  `json:"dominant_reference"`
	DominantRender        string  `json:"dominant_render"`
	ReferenceSubjectRatio float64 `json:"reference_subject_ratio"`
	RenderSubjectRatio    float64 `json:"render_subject_ratio"`
	Method                string  `json:"method"`
}

// CompareSubjects measures the two subject masks. normalizedSide is the square
// edge both silhouettes are scaled to; a value below 1 keeps the larger box
// edge, and a side above 512 is capped because the comparison is a summary,
// not a pixel-diff.
func CompareSubjects(reference, render *image.NRGBA, threshold float64, normalizedSide int) SubjectComparison {
	if normalizedSide < 1 {
		normalizedSide = 128
	}
	if normalizedSide > 512 {
		normalizedSide = 512
	}
	ref := SubjectSegments(reference, threshold)
	rendered := SubjectSegments(render, threshold)
	comparison := SubjectComparison{
		DominantReference:     colourHex(ref.Mean),
		DominantRender:        colourHex(rendered.Mean),
		ReferenceSubjectRatio: roundRatio(ref.Ratio),
		RenderSubjectRatio:    roundRatio(rendered.Ratio),
		Method:                "normalized_silhouette_iou_v1",
	}
	comparison.DominantColorDistance = roundMetric(colourDistanceBetween(ref.Mean, rendered.Mean))
	if !ref.HasSubject || !rendered.HasSubject {
		return comparison
	}
	referenceAspect, renderAspect := boxAspect(ref.Box), boxAspect(rendered.Box)
	comparison.BBoxAspectDelta = roundMetric(absFloat(referenceAspect - renderAspect))
	referenceMask := normalizeMask(ref, normalizedSide)
	renderMask := normalizeMask(rendered, normalizedSide)
	intersection, union := 0, 0
	for index := range referenceMask {
		if referenceMask[index] && renderMask[index] {
			intersection++
		}
		if referenceMask[index] || renderMask[index] {
			union++
		}
	}
	if union > 0 {
		comparison.SilhouetteIOU = roundMetric(float64(intersection) / float64(union))
	}
	return comparison
}

// normalizeMask crops the subject to its box and samples it onto a
// normalizedSide square by nearest neighbour, which removes scale and
// translation differences.
func normalizeMask(subject Subject, side int) []bool {
	box := subject.Box
	boxWidth, boxHeight := box[2]-box[0]+1, box[3]-box[1]+1
	mask := make([]bool, side*side)
	if boxWidth <= 0 || boxHeight <= 0 {
		return mask
	}
	for y := 0; y < side; y++ {
		sourceY := box[1] + y*boxHeight/side
		for x := 0; x < side; x++ {
			sourceX := box[0] + x*boxWidth/side
			mask[y*side+x] = subject.Mask[sourceY*subject.Width+sourceX]
		}
	}
	return mask
}

func boxAspect(box [4]int) float64 {
	width, height := box[2]-box[0]+1, box[3]-box[1]+1
	if width <= 0 || height <= 0 {
		return 0
	}
	return float64(width) / float64(height)
}

func colourHex(mean [3]float64) string {
	return fmt.Sprintf("#%02x%02x%02x", clampByte(mean[0]), clampByte(mean[1]), clampByte(mean[2]))
}

func clampByte(value float64) int {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return int(value + 0.5)
}

func colourDistanceBetween(left, right [3]float64) float64 {
	dr, dg, db := left[0]-right[0], left[1]-right[1], left[2]-right[2]
	return sqrt(dr*dr + dg*dg + db*db)
}

func roundMetric(value float64) float64 {
	if value < 0 {
		return 0
	}
	return float64(int(value*10000+0.5)) / 10000
}

func roundRatio(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return roundMetric(value)
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
