// Package visionview renders the model-facing views of the purpose-based
// vision budget (#247): original-pixel crops and labelled candidate contact
// sheets. Nothing here is a Provider reference or a stored asset: both are
// made for one request and dropped with it.
package visionview

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// Region is a normalized area of a picture: x and y are the top-left corner,
// width and height its size, all in 0..1 of the original dimensions.
type Region struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// PixelRect is the region resolved to original pixels.
type PixelRect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// String renders the region the way the crop label names it.
func (rect PixelRect) String() string {
	return fmt.Sprintf("%d,%d %d×%d", rect.X, rect.Y, rect.Width, rect.Height)
}

// RegionTooLargeError refuses a crop whose pixel area exceeds the Schema's
// maximum_pixels.
type RegionTooLargeError struct {
	Width, Height, Maximum int
}

func (err *RegionTooLargeError) Error() string {
	return fmt.Sprintf("visionview: crop is %dx%d pixels, the maximum is %d", err.Width, err.Height, err.Maximum)
}

// RegionTooSmallError refuses a crop thinner than minimum_side_pixels, where
// the model could not see any detail.
type RegionTooSmallError struct {
	Width, Height, Minimum int
}

func (err *RegionTooSmallError) Error() string {
	return fmt.Sprintf("visionview: crop is %dx%d pixels, the minimum side is %d", err.Width, err.Height, err.Minimum)
}

// InvalidRegionError reports a region outside 0..1 or with no area.
type InvalidRegionError struct{}

func (err *InvalidRegionError) Error() string {
	return "visionview: region must be inside 0..1 and have an area"
}

// Decode reads a picture from its stored bytes.
func Decode(data []byte) (image.Image, error) {
	picture, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("visionview: decode picture: %w", err)
	}
	return picture, nil
}

// DecodeDataURI reads a picture from a base64 data URI.
func DecodeDataURI(value string) (image.Image, error) {
	lower := strings.ToLower(value)
	if !strings.HasPrefix(lower, "data:image/") {
		return nil, errors.New("visionview: not an image data URI")
	}
	comma := strings.Index(value, ",")
	if comma < 0 || !strings.Contains(strings.ToLower(value[:comma]), ";base64") {
		return nil, errors.New("visionview: data URI is not base64")
	}
	data, err := base64.StdEncoding.DecodeString(value[comma+1:])
	if err != nil {
		return nil, fmt.Errorf("visionview: decode data URI: %w", err)
	}
	return Decode(data)
}

// CropRegion cuts region out of picture at its original resolution. It never
// scales: every returned pixel is a source pixel.
func CropRegion(picture image.Image, region Region, maximumPixels, minimumSide int) (image.Image, PixelRect, error) {
	if region.Width <= 0 || region.Height <= 0 || region.X < 0 || region.Y < 0 ||
		region.X >= 1 || region.Y >= 1 || region.X+region.Width > 1.000001 ||
		region.Y+region.Height > 1.000001 {
		return nil, PixelRect{}, &InvalidRegionError{}
	}
	bounds := picture.Bounds()
	rect := PixelRect{
		X:      bounds.Min.X + int(region.X*float64(bounds.Dx())),
		Y:      bounds.Min.Y + int(region.Y*float64(bounds.Dy())),
		Width:  int(region.Width*float64(bounds.Dx()) + 0.5),
		Height: int(region.Height*float64(bounds.Dy()) + 0.5),
	}
	if rect.Width < 1 {
		rect.Width = 1
	}
	if rect.Height < 1 {
		rect.Height = 1
	}
	if rect.X+rect.Width > bounds.Max.X {
		rect.Width = bounds.Max.X - rect.X
	}
	if rect.Y+rect.Height > bounds.Max.Y {
		rect.Height = bounds.Max.Y - rect.Y
	}
	if minimumSide > 0 && (rect.Width < minimumSide || rect.Height < minimumSide) {
		return nil, rect, &RegionTooSmallError{Width: rect.Width, Height: rect.Height, Minimum: minimumSide}
	}
	if maximumPixels > 0 && rect.Width*rect.Height > maximumPixels {
		return nil, rect, &RegionTooLargeError{Width: rect.Width, Height: rect.Height, Maximum: maximumPixels}
	}
	source := image.Rect(rect.X, rect.Y, rect.X+rect.Width, rect.Y+rect.Height)
	target := image.NewRGBA(image.Rect(0, 0, rect.Width, rect.Height))
	draw.Draw(target, target.Bounds(), picture, source.Min, draw.Src)
	// Coordinates in the report are relative to the original picture, whose
	// top-left is not always (0,0).
	rect.X -= bounds.Min.X
	rect.Y -= bounds.Min.Y
	return target, rect, nil
}

// Tile is one picture of a contact sheet, already decoded.
type Tile struct {
	Picture image.Image
	Label   string
}

// SheetOptions is the Schema's contact_sheet declaration.
type SheetOptions struct {
	Columns       int
	TilePixels    int
	LabelHeight   int
	MaximumTiles  int
	MaximumBytes  int64
	JPEGQualities []int
}

// ComposeSheet draws the tiles into a grid with a label above each picture and
// encodes the result as JPEG within maximum bytes.
func ComposeSheet(tiles []Tile, options SheetOptions) ([]byte, error) {
	if len(tiles) == 0 {
		return nil, errors.New("visionview: contact sheet has no tiles")
	}
	if options.MaximumTiles > 0 && len(tiles) > options.MaximumTiles {
		return nil, fmt.Errorf("visionview: contact sheet has %d tiles, the maximum is %d", len(tiles), options.MaximumTiles)
	}
	columns := options.Columns
	if columns <= 0 {
		return nil, errors.New("visionview: contact sheet has no columns")
	}
	tilePixels := options.TilePixels
	labelHeight := options.LabelHeight
	if tilePixels <= 0 || labelHeight < 0 {
		return nil, errors.New("visionview: contact sheet sizes are not positive")
	}
	if columns > len(tiles) {
		columns = len(tiles)
	}
	rows := (len(tiles) + columns - 1) / columns
	cellWidth := tilePixels
	cellHeight := tilePixels + labelHeight
	// A little padding keeps neighbouring pictures apart.
	const padding = 4
	width := columns*cellWidth + (columns+1)*padding
	height := rows*cellHeight + (rows+1)*padding
	sheet := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(sheet, sheet.Bounds(), image.White, image.Point{}, draw.Src)
	for index, tile := range tiles {
		column, row := index%columns, index/columns
		cellX := padding + column*(cellWidth+padding)
		cellY := padding + row*(cellHeight+padding)
		drawTileLabel(sheet, tile.Label, cellX, cellY, cellWidth, labelHeight)
		drawScaled(sheet, tile.Picture, image.Rect(
			cellX, cellY+labelHeight, cellX+cellWidth, cellY+labelHeight+tilePixels))
	}
	return encodeJPEG(sheet, options.MaximumBytes, options.JPEGQualities)
}

func drawTileLabel(target *image.RGBA, label string, x, y, width, height int) {
	if height <= 0 || label == "" {
		return
	}
	face := basicfont.Face7x13
	// Clip the label to the cell so a long handle cannot overlap a neighbour.
	text := label
	if limit := width / face.Advance; limit > 0 && len(text) > limit {
		text = text[:limit]
	}
	drawer := &font.Drawer{
		Dst: target, Src: image.Black, Face: face,
		Dot: fixed.P(x, y+height-3),
	}
	drawer.DrawString(text)
}

// drawScaled fits picture inside target preserving its aspect ratio, with the
// unused space left white.
func drawScaled(target *image.RGBA, picture image.Image, area image.Rectangle) {
	bounds := picture.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return
	}
	scale := float64(area.Dx()) / float64(bounds.Dx())
	if vertical := float64(area.Dy()) / float64(bounds.Dy()); vertical < scale {
		scale = vertical
	}
	width := int(float64(bounds.Dx()) * scale)
	height := int(float64(bounds.Dy()) * scale)
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	x := area.Min.X + (area.Dx()-width)/2
	y := area.Min.Y + (area.Dy()-height)/2
	destination := image.Rect(x, y, x+width, y+height)
	xdraw.CatmullRom.Scale(target, destination, picture, bounds, xdraw.Over, nil)
}

// EncodeJPEG encodes picture as JPEG, trying the declared qualities until the
// result fits maximumBytes. It returns an error when no quality fits.
func EncodeJPEG(picture image.Image, maximumBytes int64, qualities []int) ([]byte, error) {
	return encodeJPEG(picture, maximumBytes, qualities)
}

func encodeJPEG(picture image.Image, maximumBytes int64, qualities []int) ([]byte, error) {
	if len(qualities) == 0 {
		qualities = []int{90}
	}
	var last []byte
	for _, quality := range qualities {
		if quality <= 0 || quality > 100 {
			continue
		}
		var encoded bytes.Buffer
		if err := jpeg.Encode(&encoded, picture, &jpeg.Options{Quality: quality}); err != nil {
			return nil, fmt.Errorf("visionview: encode picture: %w", err)
		}
		last = encoded.Bytes()
		if maximumBytes <= 0 || int64(len(last)) <= maximumBytes {
			return last, nil
		}
	}
	if last == nil {
		return nil, errors.New("visionview: no JPEG quality to encode with")
	}
	return nil, fmt.Errorf("visionview: encoded picture is %d bytes, the maximum is %d", len(last), maximumBytes)
}
