// Package imageinspect reports deterministic facts about a 2D picture: its
// pixels, not its meaning. It exists for the Agent's read-only image self-check
// and for the image-to-3D input gate (#245).
//
// It decodes PNG, JPEG and WebP through the standard library and x/image. It
// never reads a URL, never calls a model, and never writes anything. A mask is
// built from alpha when the picture has any, else from the distance to the mean
// border colour; connected components are counted on a coarse grid so a large
// picture costs linear time and bounded memory.
package imageinspect

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

// Limits bounds what Inspect accepts. A zero field disables that cap.
type Limits struct {
	MaxBytes  int64
	MaxPixels int64
}

// Report is the JSON-ready inspection result. Ratios are in [0, 1].
type Report struct {
	Format  string `json:"format"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	HasAlpha bool  `json:"has_alpha"`
	// TransparentRatio is the share of fully transparent pixels; 0 when the
	// picture has no alpha channel.
	TransparentRatio float64 `json:"transparent_ratio"`
	// BackgroundSpread is the root-mean-square colour distance of the border
	// pixels from their mean, on a 0..255 scale. A plain studio background is
	// small; a photo or a busy background is large. It is 0 for a picture with
	// a transparent background.
	BackgroundSpread float64 `json:"background_spread"`
	// SubjectRatio is the share of pixels that are not background.
	SubjectRatio float64 `json:"subject_ratio"`
	// SubjectBBox is the inclusive pixel box x0,y0,x1,y1, or all zero when the
	// picture has no subject.
	SubjectBBox [4]int `json:"subject_bbox"`
	// TouchesEdge says the subject reaches any picture border.
	TouchesEdge bool `json:"touches_edge"`
	// ComponentCount counts subject components at least MinComponentRatio of
	// the picture, and LargestComponentRatio is the largest such component's
	// share of all subject pixels.
	ComponentCount        int     `json:"component_count"`
	LargestComponentRatio float64 `json:"largest_component_ratio"`
}

// Inspect decodes data and reports its deterministic picture facts.
func Inspect(data []byte, limits Limits) (Report, error) {
	if limits.MaxBytes > 0 && int64(len(data)) > limits.MaxBytes {
		return Report{}, fmt.Errorf("image is %d bytes, over the %d-byte limit", len(data), limits.MaxBytes)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Report{}, fmt.Errorf("decode image header: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return Report{}, errors.New("image has no pixels")
	}
	pixels := int64(config.Width) * int64(config.Height)
	if limits.MaxPixels > 0 && pixels > limits.MaxPixels {
		return Report{}, fmt.Errorf("image is %d pixels, over the %d-pixel limit", pixels, limits.MaxPixels)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Report{}, fmt.Errorf("decode image: %w", err)
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	nrgba := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(nrgba, nrgba.Bounds(), img, bounds.Min, draw.Src)

	report := Report{Format: format, Width: width, Height: height}
	mask := make([]bool, width*height)
	hasAlpha := false
	for y := 0; y < height; y++ {
		row := nrgba.Pix[y*nrgba.Stride:]
		for x := 0; x < width; x++ {
			if row[4*x+3] < 255 {
				hasAlpha = true
				break
			}
		}
		if hasAlpha {
			break
		}
	}
	report.HasAlpha = hasAlpha
	if hasAlpha {
		subject := 0
		for y := 0; y < height; y++ {
			row := nrgba.Pix[y*nrgba.Stride:]
			for x := 0; x < width; x++ {
				if row[4*x+3] >= 16 {
					mask[y*width+x] = true
					subject++
				}
			}
		}
		report.TransparentRatio = 1 - float64(subject)/float64(width*height)
	} else {
		background, spread := borderBackground(nrgba)
		report.BackgroundSpread = spread
		subject := 0
		for y := 0; y < height; y++ {
			row := nrgba.Pix[y*nrgba.Stride:]
			for x := 0; x < width; x++ {
				if colourDistance(row[4*x:4*x+3], background) > 32*32 {
					mask[y*width+x] = true
					subject++
				}
			}
		}
		report.SubjectRatio = float64(subject) / float64(width*height)
	}
	if report.HasAlpha {
		report.SubjectRatio = 1 - report.TransparentRatio
	}
	report.subjectBox(mask)
	components, largest := subjectComponents(mask, width, height)
	report.ComponentCount, report.LargestComponentRatio = components, largest
	return report, nil
}

func colourDistance(pixel []byte, background [3]float64) float64 {
	dr := float64(pixel[0]) - background[0]
	dg := float64(pixel[1]) - background[1]
	db := float64(pixel[2]) - background[2]
	return dr*dr + dg*dg + db*db
}

// borderBackground is the mean colour of the one-pixel border and the RMS
// distance of those pixels from it.
func borderBackground(img *image.NRGBA) ([3]float64, float64) {
	width, height := img.Bounds().Dx(), img.Bounds().Dy()
	var sum [3]float64
	var count int
	add := func(x, y int) {
		offset := y*img.Stride + x*4
		sum[0] += float64(img.Pix[offset])
		sum[1] += float64(img.Pix[offset+1])
		sum[2] += float64(img.Pix[offset+2])
		count++
	}
	for x := 0; x < width; x++ {
		add(x, 0)
		if height > 1 {
			add(x, height-1)
		}
	}
	for y := 1; y < height-1; y++ {
		add(0, y)
		if width > 1 {
			add(width-1, y)
		}
	}
	mean := [3]float64{sum[0] / float64(count), sum[1] / float64(count), sum[2] / float64(count)}
	var squares float64
	visit := func(x, y int) {
		offset := y*img.Stride + x*4
		squares += colourDistance(img.Pix[offset:offset+3], mean)
	}
	for x := 0; x < width; x++ {
		visit(x, 0)
		if height > 1 {
			visit(x, height-1)
		}
	}
	for y := 1; y < height-1; y++ {
		visit(0, y)
		if width > 1 {
			visit(width-1, y)
		}
	}
	return mean, sqrt(squares / float64(count))
}

func sqrt(value float64) float64 {
	if value <= 0 {
		return 0
	}
	// Newton's method avoids importing math for one call.
	guess := value
	for i := 0; i < 24; i++ {
		guess = (guess + value/guess) / 2
	}
	return guess
}

func (r *Report) subjectBox(mask []bool) {
	width := r.Width
	x0, y0, x1, y1 := width, r.Height, -1, -1
	for index, subject := range mask {
		if !subject {
			continue
		}
		x, y := index%width, index/width
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
	if x1 >= 0 {
		r.SubjectBBox = [4]int{x0, y0, x1, y1}
		r.TouchesEdge = x0 == 0 || y0 == 0 || x1 == width-1 || y1 == r.Height-1
	}
}

// subjectComponents counts components on a coarse grid. A component must cover
// at least 0.5% of the picture to be counted; largest is its share of all
// subject pixels.
func subjectComponents(mask []bool, width, height int) (int, float64) {
	scale := 1
	if longest := max(width, height); longest > 256 {
		scale = (longest + 255) / 256
	}
	gridWidth := (width + scale - 1) / scale
	gridHeight := (height + scale - 1) / scale
	grid := make([]bool, gridWidth*gridHeight)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if mask[y*width+x] {
				grid[(y/scale)*gridWidth+x/scale] = true
			}
		}
	}
	total := 0
	for _, set := range grid {
		if set {
			total++
		}
	}
	if total == 0 {
		return 0, 0
	}
	minimum := max(1, int(float64(gridWidth*gridHeight)*0.005))
	seen := make([]bool, len(grid))
	count, largest := 0, 0
	stack := make([]int, 0, 64)
	for start := range grid {
		if !grid[start] || seen[start] {
			continue
		}
		seen[start] = true
		stack = append(stack[:0], start)
		size := 0
		for len(stack) > 0 {
			at := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			size++
			x, y := at%gridWidth, at/gridWidth
			for _, neighbour := range [4]int{at - 1, at + 1, at - gridWidth, at + gridWidth} {
				if neighbour < 0 || neighbour >= len(grid) || seen[neighbour] || !grid[neighbour] {
					continue
				}
				nx, ny := neighbour%gridWidth, neighbour/gridWidth
				if (neighbour == at-1 && nx != x-1) || (neighbour == at+1 && nx != x+1) || (neighbour == at-gridWidth && ny != y-1) || (neighbour == at+gridWidth && ny != y+1) {
					continue
				}
				seen[neighbour] = true
				stack = append(stack, neighbour)
			}
		}
		if size >= minimum {
			count++
			if size > largest {
				largest = size
			}
		}
	}
	if total == 0 {
		return 0, 0
	}
	return count, float64(largest) / float64(total)
}

