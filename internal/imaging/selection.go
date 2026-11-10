package imaging

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"math"
	"strconv"
	"strings"

	_ "golang.org/x/image/webp"
)

// SelectionPreviewStyle bounds and styles a server-composed selection preview
// (#244): the snapshot dimmed outside the selected region and tinted inside it.
type SelectionPreviewStyle struct {
	MaxPixels   int64
	DimOpacity  float64
	TintColor   string
	TintOpacity float64
}

// maximumPreviewInputBytes bounds each decoded preview input. The snapshot and
// mask are owned uploads; an upload limit far below this is enforced earlier.
const maximumPreviewInputBytes = 64 << 20

// RenderSelectionPreview composes the picture the Agent reads beside a frozen
// selection mask: pixels outside the transparent (redrawn) region are dimmed,
// pixels inside it are tinted, so the selected area stands out. The mask is
// scaled to the picture if the two sizes differ (the turn's own check already
// requires equality; scaling keeps a preview harmless). The picture is
// downscaled to stay within MaxPixels. The result is an opaque PNG.
func RenderSelectionPreview(picture, mask []byte, style SelectionPreviewStyle) ([]byte, error) {
	decodedPicture, err := decodePreviewImage(picture)
	if err != nil {
		return nil, fmt.Errorf("selection preview: decode picture: %w", err)
	}
	decodedMask, err := decodePreviewImage(mask)
	if err != nil {
		return nil, fmt.Errorf("selection preview: decode mask: %w", err)
	}
	width, height := decodedPicture.Bounds().Dx(), decodedPicture.Bounds().Dy()
	if width <= 0 || height <= 0 {
		return nil, errors.New("selection preview: picture has no pixels")
	}
	if style.MaxPixels > 0 && int64(width)*int64(height) > style.MaxPixels {
		side := int(math.Sqrt(float64(style.MaxPixels)))
		targetWidth, targetHeight := FitWithin(width, height, max(1, side))
		decodedPicture = ResizeBilinear(decodedPicture, targetWidth, targetHeight)
		decodedMask = ResizeBilinear(decodedMask, targetWidth, targetHeight)
		width, height = targetWidth, targetHeight
	}
	tint, err := parseHexColour(style.TintColor)
	if err != nil {
		return nil, err
	}
	dim := clampUnit(style.DimOpacity)
	tintOpacity := clampUnit(style.TintOpacity)
	maskBounds := decodedMask.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		sourceRow := decodedPicture.Pix[y*decodedPicture.Stride:]
		maskY := maskBounds.Min.Y + y*maskBounds.Dy()/height
		maskRow := decodedMask.Pix[maskY*decodedMask.Stride:]
		outRow := out.Pix[y*out.Stride:]
		for x := 0; x < width; x++ {
			maskX := maskBounds.Min.X + x*maskBounds.Dx()/width
			alpha := maskRow[4*maskX+3]
			red := float64(sourceRow[4*x])
			green := float64(sourceRow[4*x+1])
			blue := float64(sourceRow[4*x+2])
			if alpha < 128 {
				// The selected region: tint it, keeping some of the picture.
				red = red*(1-tintOpacity) + float64(tint.R)*tintOpacity
				green = green*(1-tintOpacity) + float64(tint.G)*tintOpacity
				blue = blue*(1-tintOpacity) + float64(tint.B)*tintOpacity
			} else {
				// Kept region: dim it so the selection stands out.
				red *= 1 - dim
				green *= 1 - dim
				blue *= 1 - dim
			}
			outRow[4*x+0] = clampByte(red)
			outRow[4*x+1] = clampByte(green)
			outRow[4*x+2] = clampByte(blue)
			outRow[4*x+3] = 255
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, out); err != nil {
		return nil, fmt.Errorf("selection preview: encode: %w", err)
	}
	return encoded.Bytes(), nil
}

func decodePreviewImage(data []byte) (*image.NRGBA, error) {
	if len(data) == 0 || len(data) > maximumPreviewInputBytes {
		return nil, errors.New("empty or oversized picture")
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	bounds := decoded.Bounds()
	nrgba := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(nrgba, nrgba.Bounds(), decoded, bounds.Min, draw.Src)
	return nrgba, nil
}

func parseHexColour(value string) (color.NRGBA, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "#")
	if len(value) != 6 {
		return color.NRGBA{}, fmt.Errorf("selection preview: tint colour %q is not #rrggbb", value)
	}
	number, err := strconv.ParseUint(value, 16, 32)
	if err != nil {
		return color.NRGBA{}, fmt.Errorf("selection preview: tint colour %q is not #rrggbb", value)
	}
	return color.NRGBA{R: uint8(number >> 16), G: uint8(number >> 8), B: uint8(number), A: 255}, nil
}

func clampUnit(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func clampByte(value float64) uint8 {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return uint8(math.Round(value))
}
