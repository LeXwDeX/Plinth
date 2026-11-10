package visionview

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func testPicture() image.Image {
	picture := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			picture.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	for y := 30; y < 70; y++ {
		for x := 30; x < 70; x++ {
			picture.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	return picture
}

// A crop keeps the source pixels exactly: the reported rectangle and every
// pixel agree with the original picture, never a scaled copy.
func TestCropRegionKeepsOriginalPixels(t *testing.T) {
	picture := testPicture()
	cropped, rect, err := CropRegion(picture, Region{X: 0.2, Y: 0.2, Width: 0.2, Height: 0.2}, 1<<20, 8)
	if err != nil {
		t.Fatal(err)
	}
	if rect.X != 20 || rect.Y != 20 || rect.Width != 20 || rect.Height != 20 {
		t.Fatalf("rect = %+v", rect)
	}
	if cropped.Bounds().Dx() != 20 || cropped.Bounds().Dy() != 20 {
		t.Fatalf("crop bounds = %v", cropped.Bounds())
	}
	red := color.RGBA{R: 255, A: 255}
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	// The crop starts at (20,20), just outside the red square (30..69).
	if got := color.RGBAModel.Convert(cropped.At(0, 0)).(color.RGBA); got != white {
		t.Fatalf("the edge pixel = %+v; want the original white", got)
	}
	if got := color.RGBAModel.Convert(cropped.At(19, 19)).(color.RGBA); got != red {
		t.Fatalf("the square must stay red at its original pixels: %+v", got)
	}
}

func TestCropRegionRefusesInvalidBoundsAndLimits(t *testing.T) {
	picture := testPicture()
	for name, region := range map[string]Region{
		"outside":  {X: 0.9, Y: 0.1, Width: 0.2, Height: 0.1},
		"empty":    {X: 0.1, Y: 0.1},
		"negative": {X: -0.1, Y: 0.1, Width: 0.2, Height: 0.2},
	} {
		if _, _, err := CropRegion(picture, region, 1<<20, 8); err == nil {
			t.Fatalf("%s region was accepted", name)
		}
	}
	_, _, err := CropRegion(picture, Region{X: 0, Y: 0, Width: 1, Height: 1}, 100, 8)
	var tooLarge *RegionTooLargeError
	if !errors.As(err, &tooLarge) || tooLarge.Maximum != 100 {
		t.Fatalf("too large error = %v", err)
	}
	_, _, err = CropRegion(picture, Region{X: 0.45, Y: 0.45, Width: 0.03, Height: 0.03}, 1<<20, 64)
	var tooSmall *RegionTooSmallError
	if !errors.As(err, &tooSmall) || tooSmall.Minimum != 64 {
		t.Fatalf("too small error = %v", err)
	}
}

// The sheet is one JPEG whose grid holds every tile and whose labels are
// drawn from the declared template.
func TestComposeSheetDrawsAGrid(t *testing.T) {
	tiles := make([]Tile, 6)
	for index := range tiles {
		tiles[index] = Tile{Picture: testPicture(), Label: "[@img" + string(rune('1'+index)) + "]"}
	}
	data, err := ComposeSheet(tiles, SheetOptions{
		Columns: 3, TilePixels: 64, LabelHeight: 12, MaximumTiles: 10,
		MaximumBytes: 1 << 20, JPEGQualities: []int{85, 75},
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("the sheet must decode: %v", err)
	}
	// 3 columns and 2 rows: 3*64 + 4*4 wide, 2*(64+12) + 3*4 high.
	if decoded.Bounds().Dx() != 208 || decoded.Bounds().Dy() != 164 {
		t.Fatalf("sheet bounds = %v", decoded.Bounds())
	}
}

func TestComposeSheetRefusesOverTheDeclaredBounds(t *testing.T) {
	tiles := []Tile{{Picture: testPicture(), Label: "a"}, {Picture: testPicture(), Label: "b"}}
	if _, err := ComposeSheet(tiles, SheetOptions{Columns: 2, TilePixels: 64, LabelHeight: 12, MaximumTiles: 1}); err == nil {
		t.Fatal("more tiles than maximum_tiles was accepted")
	}
	if _, err := ComposeSheet(tiles, SheetOptions{
		Columns: 2, TilePixels: 64, LabelHeight: 12, MaximumBytes: 10, JPEGQualities: []int{85},
	}); err == nil {
		t.Fatal("a sheet over maximum_bytes was accepted")
	}
}

func TestDecodeDataURIReadsBase64Pictures(t *testing.T) {
	picture := testPicture()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeDataURI("data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes()))
	if err != nil || decoded.Bounds().Dx() != 100 {
		t.Fatalf("decoded = %v err = %v", decoded, err)
	}
	if _, err := DecodeDataURI("not a data uri"); err == nil {
		t.Fatal("a non-data URI was accepted")
	}
}
