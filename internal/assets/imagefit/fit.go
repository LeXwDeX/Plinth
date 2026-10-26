// Package imagefit re-encodes an input image so it fits an Offering's byte
// limit and accepted formats without changing its width or height (#276).
// It never resizes or crops: an image that cannot fit is refused by the
// caller with the sizes it reports.
package imagefit

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // decode GIF sources
	"image/jpeg"
	"image/png"
	"strconv"
	"strings"

	_ "golang.org/x/image/webp" // decode WebP sources
)

// Step names as declared by the Product Schema image_fitting.steps.
const (
	StepLosslessPNG = "lossless-png"
	StepLossy       = "lossy"
)

// FlattenPrefix marks a lossy method whose image was first composited over
// the parameter's declared alpha_flatten colour (#271), e.g. "flatten-jpeg-q95".
const FlattenPrefix = "flatten-"

// Policy is the Schema's image_fitting declaration.
type Policy struct {
	Steps           []string
	JPEGQualities   []int
	MaxDecodePixels int64
}

// Target is what one Offering parameter accepts.
type Target struct {
	MaxBytes   int64
	MediaTypes []string
	// Flatten is the parameter's alpha_flatten colour when its upstream does
	// not take transparent pixels for this input (no keep_when matched). The
	// wire would composite the image over it anyway, so the lossy step may do
	// the same first and then use JPEG. Nil keeps transparency: such an image
	// is never turned into JPEG.
	Flatten *color.RGBA
}

// Result is a re-encoded image with the same width and height as its source.
type Result struct {
	Body      []byte
	MediaType string
	Method    string
	Width     int
	Height    int
}

// ErrUnreadable means the source is not a decodable image within the decode
// pixel bound.
var ErrUnreadable = errors.New("imagefit: image is unreadable")

// InsufficientError means no declared step produced an accepted encoding
// within the limit. Smallest is the smallest size reached (the source size
// when no step applied). Transparent means the lossy step was skipped because
// the image has real transparency that the parameter keeps: JPEG has no alpha,
// and no lossy encoder that keeps alpha (WebP) is available without cgo.
type InsufficientError struct {
	Smallest    int64
	Transparent bool
}

func (err *InsufficientError) Error() string {
	if err.Transparent {
		return fmt.Sprintf("imagefit: smallest encoding keeping transparency is %d bytes", err.Smallest)
	}
	return fmt.Sprintf("imagefit: smallest encoding is %d bytes", err.Smallest)
}

// Config reads only an image's header.
func Config(source []byte) (image.Config, string, error) {
	return image.DecodeConfig(bytes.NewReader(source))
}

// Fit runs the policy's steps in order and returns the first encoding within
// target.MaxBytes in an accepted format. Pixels are never resampled.
func Fit(source []byte, target Target, policy Policy) (Result, error) {
	config, _, err := Config(source)
	if err != nil || config.Width <= 0 || config.Height <= 0 ||
		int64(config.Width)*int64(config.Height) > policy.MaxDecodePixels {
		return Result{}, ErrUnreadable
	}
	decoded, _, err := image.Decode(bytes.NewReader(source))
	if err != nil {
		return Result{}, ErrUnreadable
	}
	bounds := decoded.Bounds()
	smallest := int64(len(source))
	accept := func(body []byte, mediaType string, method string) (Result, bool) {
		if int64(len(body)) < smallest {
			smallest = int64(len(body))
		}
		if target.MaxBytes > 0 && int64(len(body)) > target.MaxBytes {
			return Result{}, false
		}
		return Result{Body: body, MediaType: mediaType, Method: method,
			Width: bounds.Dx(), Height: bounds.Dy()}, true
	}
	transparent := false
	for _, step := range policy.Steps {
		switch step {
		case StepLosslessPNG:
			if !Accepts(target.MediaTypes, "image/png") {
				continue
			}
			var buffer bytes.Buffer
			encoder := png.Encoder{CompressionLevel: png.BestCompression}
			if err := encoder.Encode(&buffer, decoded); err != nil {
				return Result{}, err
			}
			if result, ok := accept(buffer.Bytes(), "image/png", StepLosslessPNG); ok {
				return result, nil
			}
		case StepLossy:
			if !Accepts(target.MediaTypes, "image/jpeg") {
				continue
			}
			// JPEG has no alpha channel. Real transparency is composited away
			// only when the parameter's upstream flattens it anyway
			// (alpha_flatten), over the same colour the wire would use.
			// Otherwise the image keeps its alpha: no lossy encoder that keeps
			// alpha (WebP) is available without cgo, so it stops here and the
			// caller refuses it with the sizes reached.
			lossy, prefix := decoded, ""
			if !opaque(decoded) {
				if target.Flatten == nil {
					transparent = true
					continue
				}
				lossy, prefix = Flatten(decoded, *target.Flatten), FlattenPrefix
			}
			for _, quality := range policy.JPEGQualities {
				var buffer bytes.Buffer
				if err := jpeg.Encode(&buffer, lossy, &jpeg.Options{Quality: quality}); err != nil {
					return Result{}, err
				}
				if result, ok := accept(buffer.Bytes(), "image/jpeg", fmt.Sprintf("%sjpeg-q%d", prefix, quality)); ok {
					return result, nil
				}
			}
		}
	}
	return Result{}, &InsufficientError{Smallest: smallest, Transparent: transparent}
}

// Accepts reports whether mediaType matches a declared list ("image/*"
// wildcards allowed); an empty list accepts every type.
func Accepts(accepted []string, mediaType string) bool {
	if len(accepted) == 0 {
		return true
	}
	for _, candidate := range accepted {
		if candidate == mediaType ||
			(strings.HasSuffix(candidate, "/*") && strings.HasPrefix(mediaType, strings.TrimSuffix(candidate, "*"))) {
			return true
		}
	}
	return false
}

// Flatten composites an image over an opaque colour at the same width and
// height, as alpha_flatten declares (#271). The wire and the fitting step use
// it alike, so a flattened copy holds exactly the pixels the upstream gets.
func Flatten(source image.Image, background color.RGBA) *image.RGBA {
	bounds := source.Bounds()
	flat := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(flat, flat.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), source, bounds.Min, draw.Over)
	return flat
}

// ParseHexColor reads an alpha_flatten background colour (#rrggbb).
func ParseHexColor(value string) (color.RGBA, error) {
	if len(value) != 7 || value[0] != '#' {
		return color.RGBA{}, fmt.Errorf("invalid colour %q", value)
	}
	rgb, err := strconv.ParseUint(value[1:], 16, 32)
	if err != nil {
		return color.RGBA{}, err
	}
	return color.RGBA{R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb), A: 0xff}, nil
}

// opaque reports whether every pixel is fully opaque, so dropping the alpha
// channel loses nothing. RGBA images whose alpha is all 255 count as opaque.
func opaque(decoded image.Image) bool {
	if checker, ok := decoded.(interface{ Opaque() bool }); ok {
		return checker.Opaque()
	}
	bounds := decoded.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if _, _, _, alpha := decoded.At(x, y).RGBA(); alpha != 0xffff {
				return false
			}
		}
	}
	return true
}
