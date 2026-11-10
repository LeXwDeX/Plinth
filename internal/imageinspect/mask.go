package imageinspect

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
)

// Mask transparency semantics (#244) are shared with the canvas painter, the
// mock provider and the fake upstream: a transparent pixel is redrawn, an
// opaque pixel is kept. A pixel at alpha < maskAlphaThreshold is editable.
const maskAlphaThreshold = 128

// ErrNoSubject reports a subject split that found nothing to select.
var ErrNoSubject = errors.New("imageinspect: picture has no subject")

// RegionError reports a rectangle that is empty or outside the picture.
type RegionError struct {
	Width, Height  int
	X0, Y0, X1, Y1 int
}

func (err *RegionError) Error() string {
	return fmt.Sprintf("imageinspect: region %d,%d-%d,%d is not inside %dx%d",
		err.X0, err.Y0, err.X1, err.Y1, err.Width, err.Height)
}

// MaskReport is the measured part of a selection mask.
type MaskReport struct {
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	BBox     [4]int  `json:"bbox"`
	Coverage float64 `json:"coverage"`
}

// MeasureMask decodes a PNG mask and measures the transparent region it marks
// as editable: its pixel size, the inclusive pixel box x0,y0,x1,y1 and the
// region's share of the picture. A picture without a transparent pixel reports
// a zero box and zero coverage; the caller decides whether that is usable.
func MeasureMask(data []byte, limits Limits) (MaskReport, error) {
	decoded, format, err := decodeMaskPicture(data, limits)
	if err != nil {
		return MaskReport{}, err
	}
	if format != "png" {
		return MaskReport{}, fmt.Errorf("imageinspect: mask must be PNG, got %s", format)
	}
	report := MaskReport{Width: decoded.Bounds().Dx(), Height: decoded.Bounds().Dy()}
	editable := 0
	x0, y0, x1, y1 := report.Width, report.Height, -1, -1
	for y := 0; y < report.Height; y++ {
		row := decoded.Pix[y*decoded.Stride:]
		for x := 0; x < report.Width; x++ {
			if row[4*x+3] >= maskAlphaThreshold {
				continue
			}
			editable++
			if x < x0 {
				x0 = x
			}
			if y < y0 {
				y0 = y
			}
			if x > x1 {
				x1 = x
			}
			if y > y1 {
				y1 = y
			}
		}
	}
	if editable > 0 {
		report.BBox = [4]int{x0, y0, x1, y1}
		report.Coverage = float64(editable) / float64(report.Width*report.Height)
	}
	return report, nil
}

// RenderSubjectMask builds a mask where the picture's subject is the editable
// (transparent) region: the opaque pixels when the picture has an alpha
// channel, else every pixel that differs from the mean border colour by the
// same rule Inspect uses. A picture with no subject is ErrNoSubject.
func RenderSubjectMask(data []byte, limits Limits) ([]byte, MaskReport, error) {
	decoded, _, err := decodeMaskPicture(data, limits)
	if err != nil {
		return nil, MaskReport{}, err
	}
	width, height := decoded.Bounds().Dx(), decoded.Bounds().Dy()
	editable := make([]bool, width*height)
	hasAlpha := false
	for index := 0; index < width*height; index++ {
		if decoded.Pix[index*4+3] < 255 {
			hasAlpha = true
			break
		}
	}
	if hasAlpha {
		for index := 0; index < width*height; index++ {
			editable[index] = decoded.Pix[index*4+3] >= 16
		}
	} else {
		background, _ := borderBackground(decoded)
		for y := 0; y < height; y++ {
			row := decoded.Pix[y*decoded.Stride:]
			for x := 0; x < width; x++ {
				editable[y*width+x] = colourDistance(row[4*x:4*x+3], background) > 32*32
			}
		}
	}
	selected := 0
	for _, value := range editable {
		if value {
			selected++
		}
	}
	if selected == 0 {
		return nil, MaskReport{}, ErrNoSubject
	}
	// In the mask the editable region is transparent and the kept region is
	// opaque white.
	mask := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		row := mask.Pix[y*mask.Stride:]
		for x := 0; x < width; x++ {
			if editable[y*width+x] {
				continue
			}
			row[4*x+0], row[4*x+1], row[4*x+2], row[4*x+3] = 255, 255, 255, 255
		}
	}
	return encodeMaskPNG(mask, width, height)
}

// RenderRegionMask builds a mask where the given rectangle (x0,y0 inclusive,
// x1,y1 exclusive) is editable; invert swaps the two regions. A rectangle
// outside the picture or empty is a *RegionError.
func RenderRegionMask(data []byte, x0, y0, x1, y1 int, invert bool, limits Limits) ([]byte, MaskReport, error) {
	decoded, _, err := decodeMaskPicture(data, limits)
	if err != nil {
		return nil, MaskReport{}, err
	}
	width, height := decoded.Bounds().Dx(), decoded.Bounds().Dy()
	if x0 < 0 || y0 < 0 || x1 > width || y1 > height || x1 <= x0 || y1 <= y0 {
		return nil, MaskReport{}, &RegionError{Width: width, Height: height, X0: x0, Y0: y0, X1: x1, Y1: y1}
	}
	selected := (x1 - x0) * (y1 - y0)
	if invert {
		selected = width*height - selected
	}
	if selected <= 0 {
		return nil, MaskReport{}, &RegionError{Width: width, Height: height, X0: x0, Y0: y0, X1: x1, Y1: y1}
	}
	mask := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		row := mask.Pix[y*mask.Stride:]
		for x := 0; x < width; x++ {
			inside := x >= x0 && x < x1 && y >= y0 && y < y1
			if inside != invert {
				// Editable: the mask stays transparent there.
				continue
			}
			row[4*x+0], row[4*x+1], row[4*x+2], row[4*x+3] = 255, 255, 255, 255
		}
	}
	encoded, _, err := encodeMaskPNG(mask, width, height)
	if err != nil {
		return nil, MaskReport{}, err
	}
	report := MaskReport{Width: width, Height: height}
	if !invert {
		report.BBox = [4]int{x0, y0, x1 - 1, y1 - 1}
	} else {
		// The kept rectangle is the transparent... no: after inversion the
		// editable region is everything outside the rectangle, whose bounding
		// box is the whole picture.
		report.BBox = [4]int{0, 0, width - 1, height - 1}
	}
	report.Coverage = float64(selected) / float64(width*height)
	return encoded, report, nil
}

// InvertMask swaps the editable and kept regions of an existing PNG mask.
func InvertMask(data []byte, limits Limits) ([]byte, MaskReport, error) {
	decoded, format, err := decodeMaskPicture(data, limits)
	if err != nil {
		return nil, MaskReport{}, err
	}
	if format != "png" {
		return nil, MaskReport{}, errors.New("imageinspect: mask must be PNG")
	}
	width, height := decoded.Bounds().Dx(), decoded.Bounds().Dy()
	for y := 0; y < height; y++ {
		row := decoded.Pix[y*decoded.Stride:]
		for x := 0; x < width; x++ {
			if row[4*x+3] >= maskAlphaThreshold {
				row[4*x+0], row[4*x+1], row[4*x+2], row[4*x+3] = 0, 0, 0, 0
				continue
			}
			row[4*x+0], row[4*x+1], row[4*x+2], row[4*x+3] = 255, 255, 255, 255
		}
	}
	return encodeMaskPNG(decoded, width, height)
}

func decodeMaskPicture(data []byte, limits Limits) (*image.NRGBA, string, error) {
	if limits.MaxBytes > 0 && int64(len(data)) > limits.MaxBytes {
		return nil, "", fmt.Errorf("image is %d bytes, over the %d-byte limit", len(data), limits.MaxBytes)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
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
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("decode image: %w", err)
	}
	bounds := decoded.Bounds()
	nrgba := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(nrgba, nrgba.Bounds(), decoded, bounds.Min, draw.Src)
	return nrgba, format, nil
}

func encodeMaskPNG(mask *image.NRGBA, width, height int) ([]byte, MaskReport, error) {
	report := MaskReport{Width: width, Height: height}
	editable := 0
	x0, y0, x1, y1 := width, height, -1, -1
	for y := 0; y < height; y++ {
		row := mask.Pix[y*mask.Stride:]
		for x := 0; x < width; x++ {
			if row[4*x+3] >= maskAlphaThreshold {
				continue
			}
			editable++
			if x < x0 {
				x0 = x
			}
			if y < y0 {
				y0 = y
			}
			if x > x1 {
				x1 = x
			}
			if y > y1 {
				y1 = y
			}
		}
	}
	if editable > 0 {
		report.BBox = [4]int{x0, y0, x1, y1}
		report.Coverage = float64(editable) / float64(width*height)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, mask); err != nil {
		return nil, MaskReport{}, err
	}
	return encoded.Bytes(), report, nil
}
