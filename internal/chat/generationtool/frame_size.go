package generationtool

import (
	"encoding/json"
	"math"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #283: the frame of a three-view group follows its source subject instead of
// the Offering's default (a square on every image model), so a standing
// character is not drawn into a quarter of a 1:1 picture. The nearest legal
// size is the one rule the canvas and a single reference picture already use
// (canvasSizeChoice, #284/#296): the size itself when the parameter takes it,
// else the declared option or the grid-snapped size of the nearest shape.
//
// The frame keeps the longer side of the parameter's default size rather than
// its pixel count. The subject fills the frame's height (or width, for a wide
// subject), so the longer side is what its resolution depends on: a portrait
// frame with the default's height gives the character the same pixels as the
// square did, without the empty sides.

// frameDefaultLongestSide stands in for the longer side of a parameter that
// declares its size as a ratio only; only the shape matters for it.
const frameDefaultLongestSide = 1024

// FrameOutputSizes returns, for every output-size parameter of the Offering
// that a person may choose, the legal value whose shape is closest to a frame
// of the given width over height, at the parameter's default longer side.
// A parameter that cannot express the shape at all is left out, so the caller
// keeps the Offering's default for it.
func FrameOutputSizes(offering generation.Offering, ratio float64) map[string]string {
	values := map[string]string{}
	if !(ratio > 0) || math.IsInf(ratio, 0) {
		return values
	}
	for _, parameter := range canvasSizeParameters(offering) {
		width, height := framePixels(parameter, ratio)
		if value, ok := canvasSizeChoice(parameter, width, height); ok {
			values[parameter.Name] = value
		}
	}
	return values
}

// frameMaximumDeclaredSide bounds the default size a frame is derived from.
const frameMaximumDeclaredSide = 1 << 15

// framePixels is a picture of the given shape whose longer side is the longer
// side of the parameter's default size.
func framePixels(parameter generation.Parameter, ratio float64) (int, int) {
	longest := frameDefaultLongestSide
	var declared any
	if len(parameter.Default) > 0 && json.Unmarshal(parameter.Default, &declared) == nil {
		if text, ok := declared.(string); ok {
			// A declared default is a real picture size; a value beyond any image
			// model's output is ignored rather than converted unchecked.
			if size, ok := parseSizeValue(text); ok && size.Width > 0 && size.Height > 0 &&
				max(size.Width, size.Height) <= frameMaximumDeclaredSide {
				longest = int(max(size.Width, size.Height))
			}
		}
	}
	if ratio >= 1 {
		return longest, max(1, int(math.Round(float64(longest)/ratio)))
	}
	return max(1, int(math.Round(float64(longest)*ratio))), longest
}
