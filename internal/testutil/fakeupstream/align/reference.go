package align

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
)

// ReferencePNG is a shaded sphere on a white background, size x size pixels: a
// plain object an image-to-image or background-removal request can work on.
// The alignment scripts of the image and matting versions send it as their
// only input picture.
func ReferencePNG(size int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	radius := float64(size) * 0.35
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
			dx, dy := float64(x-size/2), float64(y-size/2)
			if d2 := dx*dx + dy*dy; d2 < radius*radius {
				shade := uint8(220 - int(d2/(radius*radius)*140))
				img.Set(x, y, color.RGBA{R: shade, G: shade / 2, B: 60, A: 255})
			}
		}
	}
	var out bytes.Buffer
	_ = png.Encode(&out, img)
	return out.Bytes()
}

// DataURI is the data: URI of a PNG, the form Plinth sends reference pictures
// in for JSON image protocols.
func DataURI(data []byte) string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
}

// TranslucentPNG is a half-transparent disc on an opaque pale background with
// a fully transparent top-left corner, size x size pixels: a reference picture
// with an alpha channel, so the alpha flattening a protocol asks for has
// something to flatten (#540).
func TranslucentPNG(size int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	radius := float64(size) / 3
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			switch dx, dy := float64(x-size/2), float64(y-size/2); {
			case x < size/4 && y < size/4:
				img.SetNRGBA(x, y, color.NRGBA{})
			case dx*dx+dy*dy <= radius*radius:
				img.SetNRGBA(x, y, color.NRGBA{R: 40, G: 150, B: 90, A: 128})
			default:
				img.SetNRGBA(x, y, color.NRGBA{R: 250, G: 240, B: 200, A: 255})
			}
		}
	}
	var out bytes.Buffer
	_ = png.Encode(&out, img)
	return out.Bytes()
}

// MaskPNG is an edit mask, size x size pixels: opaque everywhere except a
// fully transparent central square, which is the region an edit may change
// (the convention of the OpenAI image edit mask, #540).
func MaskPNG(size int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	low, high := size*3/8, size*5/8
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if x >= low && x < high && y >= low && y < high {
				img.SetNRGBA(x, y, color.NRGBA{})
			} else {
				img.SetNRGBA(x, y, color.NRGBA{A: 255})
			}
		}
	}
	var out bytes.Buffer
	_ = png.Encode(&out, img)
	return out.Bytes()
}
