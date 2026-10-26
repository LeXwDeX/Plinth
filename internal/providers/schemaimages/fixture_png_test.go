package schemaimages_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// opaqueFixturePNG is a real, fully opaque PNG: Offerings that declare alpha_flatten decode their
// sources (#271), and an opaque one passes through byte for byte. The shade tells fixtures apart.
func opaqueFixturePNG(shade uint8) []byte {
	picture := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			picture.SetNRGBA(x, y, color.NRGBA{R: shade, G: shade, B: shade, A: 0xff})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		panic(err)
	}
	return encoded.Bytes()
}
