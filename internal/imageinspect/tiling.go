package imageinspect

import "image"

// Seamless-tiling measurement (#287). A texture tiles when placing a copy of it
// beside itself leaves no visible step: the pixel line along the right edge
// must continue into the line along the left edge, and the bottom line into the
// top line. The measurement compares each pair of opposite edge lines with the
// difference between neighbouring pixels inside the picture, which is what a
// continuous picture looks like at any other column or row. A seam is judged
// segment by segment along its length, so the report can say where it breaks.
//
// The result is evidence, not a verdict: the thresholds live in the Schema
// (Requirements.Tiling) and the Agent still looks at the picture.

// SeamSegment is one stretch of a seam. From and To are the inclusive pixel
// range along the seam (rows for the left/right seam, columns for the
// top/bottom seam). Delta is the mean absolute RGB difference, on a 0..255
// scale, between the two edge pixels that would touch; Inside is the same
// difference between neighbouring pixels across the picture in that stretch.
type SeamSegment struct {
	From   int     `json:"from"`
	To     int     `json:"to"`
	Delta  float64 `json:"delta"`
	Inside float64 `json:"inside"`
}

// TilingReport is the seam measurement of both axes. LeftRight is the seam
// where the right edge meets the left edge (segments run down the rows);
// TopBottom is where the bottom edge meets the top edge (segments run along the
// columns). An axis shorter than two pixels has no segments.
type TilingReport struct {
	LeftRight []SeamSegment `json:"left_right"`
	TopBottom []SeamSegment `json:"top_bottom"`
}

// maxSeamSegments bounds the segment count a caller can request, so a Schema
// typo cannot turn the report into a per-pixel dump.
const maxSeamSegments = 64

// measureTiling compares opposite edges of the picture. RGB only: alpha is
// ignored, because a texture is judged on the colours it would show.
func measureTiling(picture *image.NRGBA, segments int) *TilingReport {
	width, height := picture.Bounds().Dx(), picture.Bounds().Dy()
	segments = max(1, min(segments, maxSeamSegments))
	report := &TilingReport{}
	if width >= 2 {
		seam := make([]float64, height)
		inside := make([]float64, height)
		for y := 0; y < height; y++ {
			row := picture.Pix[y*picture.Stride:]
			seam[y] = pixelDifference(row[4*(width-1):], row[0:])
			var sum float64
			for x := 0; x+1 < width; x++ {
				sum += pixelDifference(row[4*x:], row[4*(x+1):])
			}
			inside[y] = sum / float64(width-1)
		}
		report.LeftRight = seamSegments(seam, inside, segments)
	}
	if height >= 2 {
		seam := make([]float64, width)
		inside := make([]float64, width)
		last := picture.Pix[(height-1)*picture.Stride:]
		for x := 0; x < width; x++ {
			seam[x] = pixelDifference(last[4*x:], picture.Pix[4*x:])
		}
		for y := 0; y+1 < height; y++ {
			upper := picture.Pix[y*picture.Stride:]
			lower := picture.Pix[(y+1)*picture.Stride:]
			for x := 0; x < width; x++ {
				inside[x] += pixelDifference(upper[4*x:], lower[4*x:])
			}
		}
		for x := range inside {
			inside[x] /= float64(height - 1)
		}
		report.TopBottom = seamSegments(seam, inside, segments)
	}
	return report
}

// pixelDifference is the mean absolute difference of the R, G and B channels of
// two pixels (each given as the start of its four NRGBA bytes), 0..255.
func pixelDifference(left, right []byte) float64 {
	return float64(absInt(int(left[0])-int(right[0]))+
		absInt(int(left[1])-int(right[1]))+
		absInt(int(left[2])-int(right[2]))) / 3
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// seamSegments averages per-line seam and inside differences over equal
// stretches of the line.
func seamSegments(seam, inside []float64, segments int) []SeamSegment {
	length := len(seam)
	segments = min(segments, length)
	result := make([]SeamSegment, 0, segments)
	for index := 0; index < segments; index++ {
		from := index * length / segments
		to := (index+1)*length/segments - 1
		var seamSum, insideSum float64
		for position := from; position <= to; position++ {
			seamSum += seam[position]
			insideSum += inside[position]
		}
		count := float64(to - from + 1)
		result = append(result, SeamSegment{
			From: from, To: to,
			Delta:  roundMetric(seamSum / count),
			Inside: roundMetric(insideSum / count),
		})
	}
	return result
}
