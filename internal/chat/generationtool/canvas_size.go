package generationtool

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #272: what the artboard shows is what comes out. The artboard snapshot of a
// canvas turn is rasterised at the canvas pixel size the person chose (#246),
// so on that turn a tool that binds the snapshot takes its output size from
// the canvas instead of any size remembered for the Offering (the Agent's
// set_params, an older turn's choice). A model that cannot produce the canvas
// size gets the nearest legal size, and the tool result says which.

// sameRatioTolerance is how close two aspect ratios must be (as a log ratio)
// to count as the same shape.
const sameRatioTolerance = 1e-3

// canvasLayout is the measured layout of the turn's artboard snapshot, when
// the turn's source is the canvas and the tool binds it.
func (executor *Executor) canvasLayout(command chat.ToolCommand) *chat.CanvasLayout {
	if !executor.canvasTurn(command) || command.Tool.CanvasSource != canvasSourceBind {
		return nil
	}
	for _, attachment := range command.Options.Selection {
		if attachment.Role == chat.AttachmentRoleCanvas && attachment.Canvas != nil &&
			attachment.Canvas.Width > 0 && attachment.Canvas.Height > 0 {
			return attachment.Canvas
		}
	}
	return nil
}

// sizeValue is one output-size value read as pixels ("2048x1536") or as a
// ratio alone ("4:3"); Width and Height are 0 for a ratio.
type sizeValue struct {
	Value         string
	Width, Height int64
	Ratio         float64
}

func parseSizeValue(value string) (sizeValue, bool) {
	text := strings.TrimSpace(value)
	if left, right, found := strings.Cut(strings.ReplaceAll(strings.ToLower(text), "×", "x"), "x"); found {
		width, errWidth := strconv.ParseInt(left, 10, 64)
		height, errHeight := strconv.ParseInt(right, 10, 64)
		if errWidth == nil && errHeight == nil && width > 0 && height > 0 {
			return sizeValue{Value: value, Width: width, Height: height, Ratio: float64(width) / float64(height)}, true
		}
		return sizeValue{}, false
	}
	if left, right, found := strings.Cut(text, ":"); found {
		width, errWidth := strconv.ParseFloat(left, 64)
		height, errHeight := strconv.ParseFloat(right, 64)
		if errWidth == nil && errHeight == nil && width > 0 && height > 0 {
			return sizeValue{Value: value, Ratio: width / height}, true
		}
	}
	return sizeValue{}, false
}

// matches reports whether a size produces the canvas: the same pixels, or
// for a ratio-only value the same shape.
func (size sizeValue) matches(width, height int) bool {
	if size.Width > 0 {
		return size.Width == int64(width) && size.Height == int64(height)
	}
	return math.Abs(math.Log(size.Ratio*float64(height)/float64(width))) < sameRatioTolerance
}

// followsCanvas reports whether a submitted size still shows the artboard as
// it is: the canvas size itself, a ratio-only value of the canvas shape, or a
// larger size of the same shape (an Offering that forces its highest
// resolution). A smaller size of the same shape (a canvas above the model's
// maximum, #284) is the nearest legal size, like any other shape. The canvas
// size notice in the web client (canvasTurnOutput) uses the same rule.
func (size sizeValue) followsCanvas(width, height int) bool {
	if size.matches(width, height) {
		return true
	}
	sameShape := math.Abs(math.Log(size.Ratio*float64(height)/float64(width))) < sameRatioTolerance
	return sameShape && (size.Width == 0 || size.Width*size.Height >= int64(width)*int64(height))
}

// followsCanvas is sizeValue.followsCanvas, with a ratio-only value also read
// as the pixels the Schema maps it to: Gemini's 9:16 at 4K is 3072×5504, not
// exactly 9:16, and on a canvas of that size it still follows the canvas.
func (executor *Executor) followsCanvas(offering generation.Offering, input map[string]any, size sizeValue, width, height int) bool {
	if size.followsCanvas(width, height) {
		return true
	}
	pixels, ok := executor.ratioPixels(offering, input, size)
	return ok && pixels.followsCanvas(width, height)
}

// ratioPixels is the size a ratio-only value produces by the Schema's canvas
// tables (canvas.frontend.artboard): the tier table of a tier parameter set in
// the input (else to its default), otherwise ratio_pixels. The web client
// reads them the same way (outputPixels).
func (executor *Executor) ratioPixels(offering generation.Offering, input map[string]any, size sizeValue) (sizeValue, bool) {
	if size.Width > 0 {
		return sizeValue{}, false
	}
	artboard := executor.canvasArtboard
	names := make([]string, 0, len(artboard.RatioPixelTiers))
	for name := range artboard.RatioPixelTiers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		tier, ok := parameterText(offering, input, name)
		if !ok {
			continue
		}
		if pixels, ok := artboard.RatioPixelTiers[name][tier][size.Value]; ok {
			return pixelSizeValue(pixels), true
		}
	}
	if pixels, ok := artboard.RatioPixels[size.Value]; ok {
		return pixelSizeValue(pixels), true
	}
	return sizeValue{}, false
}

// parameterText is a parameter's value in the input, else the Offering's
// default for it, as text.
func parameterText(offering generation.Offering, input map[string]any, name string) (string, bool) {
	if value, ok := input[name]; ok && value != nil {
		return fmt.Sprint(value), true
	}
	for _, parameter := range offering.Parameters {
		var value any
		if parameter.Name == name && len(parameter.Default) > 0 && json.Unmarshal(parameter.Default, &value) == nil && value != nil {
			return fmt.Sprint(value), true
		}
	}
	return "", false
}

func pixelSizeValue(pixels catalog.CanvasPixelSize) sizeValue {
	if pixels.Width <= 0 || pixels.Height <= 0 {
		return sizeValue{}
	}
	return sizeValue{
		Value: fmt.Sprintf("%dx%d", pixels.Width, pixels.Height),
		Width: int64(pixels.Width), Height: int64(pixels.Height),
		Ratio: float64(pixels.Width) / float64(pixels.Height),
	}
}

// canvasSizeChoice is the value of one output-size parameter that comes
// closest to the canvas: the canvas size itself when the parameter accepts it,
// else the candidate of the nearest shape and then the nearest area, in
// declared order on a tie. Candidates are the declared options and, for a
// parameter that takes custom sizes, the canvas size snapped to its grid.
func canvasSizeChoice(parameter generation.Parameter, width, height int) (string, bool) {
	exact := fmt.Sprintf("%dx%d", width, height)
	var candidates []sizeValue
	for _, option := range parameter.Options {
		if candidate, ok := parseSizeValue(option.Value); ok {
			if candidate.matches(width, height) && candidate.Width > 0 {
				return option.Value, true
			}
			candidates = append(candidates, candidate)
		}
	}
	if constraint := parameter.ImageSize; constraint != nil {
		if constraint.Check(int64(width), int64(height)) == nil {
			return exact, true
		}
		if multiple := constraint.Multiple; multiple > 0 {
			snappedWidth := max(multiple, int64(math.Round(float64(width)/float64(multiple)))*multiple)
			snappedHeight := max(multiple, int64(math.Round(float64(height)/float64(multiple)))*multiple)
			if constraint.Check(snappedWidth, snappedHeight) == nil {
				candidates = append([]sizeValue{{Value: fmt.Sprintf("%dx%d", snappedWidth, snappedHeight),
					Width: snappedWidth, Height: snappedHeight, Ratio: float64(snappedWidth) / float64(snappedHeight)}}, candidates...)
			}
		}
	}
	canvasRatio := float64(width) / float64(height)
	canvasArea := float64(width) * float64(height)
	best, bestShape, bestArea := "", math.Inf(1), math.Inf(1)
	for _, candidate := range candidates {
		shape := math.Abs(math.Log(candidate.Ratio / canvasRatio))
		area := 0.0
		if candidate.Width > 0 {
			area = math.Abs(math.Log(float64(candidate.Width) * float64(candidate.Height) / canvasArea))
		}
		if shape < bestShape-sameRatioTolerance || (math.Abs(shape-bestShape) <= sameRatioTolerance && area < bestArea) {
			best, bestShape, bestArea = candidate.Value, shape, area
		}
	}
	return best, best != ""
}

// canvasSizeParameters are the output-size parameters the canvas sets: the
// ones a person may choose (a fixed or hidden size is the Offering's own).
func canvasSizeParameters(offering generation.Offering) []generation.Parameter {
	var parameters []generation.Parameter
	for _, parameter := range offering.Parameters {
		if parameter.Semantic == generation.SemanticOutputSize && offering.UserParameterAllowed(parameter) {
			parameters = append(parameters, parameter)
		}
	}
	return parameters
}

// applyCanvasOutputSize sets every output-size parameter the Offering lets a
// person choose to the value closest to the canvas, on a canvas turn of a
// tool that binds the snapshot. Anything else is left as it is.
func (executor *Executor) applyCanvasOutputSize(command chat.ToolCommand, offering generation.Offering, input map[string]any) {
	layout := executor.canvasLayout(command)
	if layout == nil {
		return
	}
	for _, parameter := range canvasSizeParameters(offering) {
		if value, ok := canvasSizeChoice(parameter, layout.Width, layout.Height); ok {
			input[parameter.Name] = value
		}
	}
}

// canvasSizeNote is the Schema's note about the output size of a canvas turn,
// read from the input that was really submitted (after the Offering's own
// forced values): it followed the canvas, or which nearest legal size was
// used. Empty when the canvas did not set the size.
func (executor *Executor) canvasSizeNote(command chat.ToolCommand, offering generation.Offering, submitted json.RawMessage) string {
	layout := executor.canvasLayout(command)
	if layout == nil || command.Tool.ResultFields.SizeNote == "" {
		return ""
	}
	var input map[string]any
	if json.Unmarshal(submitted, &input) != nil {
		return ""
	}
	canvas := fmt.Sprintf("%d×%d", layout.Width, layout.Height)
	context := executor.chat.Context
	for _, parameter := range canvasSizeParameters(offering) {
		text, _ := input[parameter.Name].(string)
		size, ok := parseSizeValue(text)
		if !ok {
			continue
		}
		template := context.CanvasOutputSizeNearestTemplate
		if executor.followsCanvas(offering, input, size, layout.Width, layout.Height) {
			template = context.CanvasOutputSizeTemplate
		}
		template = executor.localized(command, template)
		return strings.NewReplacer("{canvas}", canvas, "{size}", text).Replace(template)
	}
	return ""
}
