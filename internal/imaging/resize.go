package imaging

import (
	"image"
	"image/color"
	"math"
)

func FitWithin(
	width int,
	height int,
	maximumDimension int,
) (int, int) {
	if width <= 0 || height <= 0 || maximumDimension <= 0 {
		return 0, 0
	}
	if width <= maximumDimension && height <= maximumDimension {
		return width, height
	}
	scale := math.Min(
		float64(maximumDimension)/float64(width),
		float64(maximumDimension)/float64(height),
	)
	targetWidth := max(1, int(math.Round(float64(width)*scale)))
	targetHeight := max(1, int(math.Round(float64(height)*scale)))
	return targetWidth, targetHeight
}

func ResizeBilinear(
	source image.Image,
	width int,
	height int,
) *image.NRGBA {
	if width <= 0 || height <= 0 {
		return image.NewNRGBA(image.Rectangle{})
	}
	result := image.NewNRGBA(image.Rect(0, 0, width, height))
	bounds := source.Bounds()
	sourceWidth, sourceHeight := bounds.Dx(), bounds.Dy()
	if sourceWidth == 0 || sourceHeight == 0 {
		return result
	}
	for y := 0; y < height; y++ {
		sourceY := (float64(y)+0.5)*float64(sourceHeight)/
			float64(height) - 0.5
		y0, y1, vertical := interpolationBounds(sourceY, sourceHeight)
		for x := 0; x < width; x++ {
			sourceX := (float64(x)+0.5)*float64(sourceWidth)/
				float64(width) - 0.5
			x0, x1, horizontal := interpolationBounds(sourceX, sourceWidth)
			result.Set(
				x,
				y,
				bilinearColor(
					source,
					bounds.Min.X+x0,
					bounds.Min.Y+y0,
					bounds.Min.X+x1,
					bounds.Min.Y+y1,
					horizontal,
					vertical,
				),
			)
		}
	}
	return result
}

func interpolationBounds(value float64, size int) (int, int, float64) {
	value = math.Max(0, math.Min(value, float64(size-1)))
	lower := int(math.Floor(value))
	upper := min(lower+1, size-1)
	return lower, upper, value - float64(lower)
}

func bilinearColor(
	source image.Image,
	x0 int,
	y0 int,
	x1 int,
	y1 int,
	horizontal float64,
	vertical float64,
) color.RGBA64 {
	topLeft := rgbaValues(source.At(x0, y0))
	topRight := rgbaValues(source.At(x1, y0))
	bottomLeft := rgbaValues(source.At(x0, y1))
	bottomRight := rgbaValues(source.At(x1, y1))
	var channels [4]uint16
	for index := range channels {
		top := interpolate(topLeft[index], topRight[index], horizontal)
		bottom := interpolate(
			bottomLeft[index],
			bottomRight[index],
			horizontal,
		)
		channels[index] = uint16(math.Round(
			top + (bottom-top)*vertical,
		))
	}
	return color.RGBA64{
		R: channels[0],
		G: channels[1],
		B: channels[2],
		A: channels[3],
	}
}

func rgbaValues(value color.Color) [4]float64 {
	red, green, blue, alpha := value.RGBA()
	return [4]float64{
		float64(red),
		float64(green),
		float64(blue),
		float64(alpha),
	}
}

func interpolate(left float64, right float64, weight float64) float64 {
	return left + (right-left)*weight
}
