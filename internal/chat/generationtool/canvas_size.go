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
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #272: on a canvas turn of a tool that binds the artboard snapshot, the
// canvas sets the output size instead of a size the person's panel remembered
// for the Offering or a Skill set. #614: a size the Agent chose (set_params for
// the Offering, or its call's own argument) is carried out instead; the
// artboard does not silently replace it.
//
// #653: the canvas is a ratio. Its pixels are only editing precision (and the
// resolution of the snapshot); the model decides the output size: of the sizes
// it may produce, the one with the most pixels at the canvas ratio (an option,
// or a custom size within its image_size bounds), at the highest resolution
// tier a person may choose. A model without that ratio gets the nearest ratio
// it has, at its highest quality, and the tool result says so. The web client
// computes the same size (canvasRatioTurnOutput in
// src/lib/workspace/canvas-size.ts); both run testdata/canvas_turn_sizes.json.

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
// closest to a picture of width×height: the size itself when the parameter
// accepts it, else the candidate of the nearest shape and then the nearest
// area, in declared order on a tie. Candidates are the declared options and,
// for a parameter that takes custom sizes, the size snapped to its grid. One
// reference picture (#296) and a three-view frame (#283) use it; the canvas
// uses canvasRatioChoice since #653.
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

// canvasShape is the aspect ratio a canvas stands for (#653): Label is how it
// is named ("16:9"), Ratio its width over height.
type canvasShape struct {
	Label string
	Ratio float64
}

// sameRatio reports whether two aspect ratios are the same shape.
func sameRatio(left, right float64) bool {
	return math.Abs(math.Log(left/right)) < sameRatioTolerance
}

// canvasShapeOf reads a canvas's ratio from its pixels; a canvas is never
// migrated. It is a ratio the Schema names (a key of ratio_pixels) when the
// canvas has that shape, or exactly the pixels the Schema maps that ratio to
// (a canvas made for a ratio-only model before #653, such as Gemini's
// 3072×5504 for 9:16); otherwise its own width over height, named by the
// simplest ratio of that shape (else width:height in lowest terms). The names
// are tried in sorted order, like the web client (canvasShapeOf).
func canvasShapeOf(artboard catalog.CanvasArtboardSchema, width, height int) canvasShape {
	ratio := float64(width) / float64(height)
	names := make([]string, 0, len(artboard.RatioPixels))
	for name := range artboard.RatioPixels {
		if named, ok := parseSizeValue(name); ok && named.Width == 0 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if named, _ := parseSizeValue(name); sameRatio(named.Ratio, ratio) {
			return canvasShape{Label: name, Ratio: named.Ratio}
		}
	}
	for _, name := range names {
		if ratioMapsTo(artboard, name, width, height) {
			named, _ := parseSizeValue(name)
			return canvasShape{Label: name, Ratio: named.Ratio}
		}
	}
	for denominator := 1; denominator <= ratioLabelMaxTerm; denominator++ {
		numerator := int(math.Round(ratio * float64(denominator)))
		if numerator >= 1 && sameRatio(float64(numerator)/float64(denominator), ratio) {
			return canvasShape{Label: fmt.Sprintf("%d:%d", numerator, denominator), Ratio: ratio}
		}
	}
	divisor := width
	for rest := height; rest != 0; {
		divisor, rest = rest, divisor%rest
	}
	return canvasShape{Label: fmt.Sprintf("%d:%d", width/divisor, height/divisor), Ratio: ratio}
}

// ratioLabelMaxTerm bounds the denominator of the simple ratio a canvas of an
// uncommon shape is named by (2048×683 reads as 3:1, not 2048:683).
const ratioLabelMaxTerm = 32

// ratioMapsTo reports whether the Schema maps the ratio to exactly
// width×height, in ratio_pixels or in any resolution tier.
func ratioMapsTo(artboard catalog.CanvasArtboardSchema, ratio string, width, height int) bool {
	same := func(pixels catalog.CanvasPixelSize, ok bool) bool {
		return ok && pixels.Width == width && pixels.Height == height
	}
	if pixels, ok := artboard.RatioPixels[ratio]; same(pixels, ok) {
		return true
	}
	for _, tiers := range artboard.RatioPixelTiers {
		for _, table := range tiers {
			if pixels, ok := table[ratio]; same(pixels, ok) {
				return true
			}
		}
	}
	return false
}

// canvasRatioChoice is the value of one output-size parameter for a canvas of
// this shape (#653): of its declared options and, when it takes custom sizes,
// the largest custom size of the canvas ratio, the candidates of the canvas
// ratio, else those of the nearest ratio; among them the one with the most
// pixels (a ratio-only value counts none), the first in declared order on a
// tie.
func canvasRatioChoice(parameter generation.Parameter, shape canvasShape) (string, bool) {
	var candidates []sizeValue
	for _, option := range parameter.Options {
		if candidate, ok := parseSizeValue(option.Value); ok {
			candidates = append(candidates, candidate)
		}
	}
	if constraint := parameter.ImageSize; constraint != nil {
		if custom, ok := largestOfRatio(*constraint, shape.Ratio); ok {
			candidates = append(candidates, custom)
		} else if bounded := boundedRatio(*constraint, shape.Ratio); bounded != shape.Ratio {
			// A ratio beyond the parameter's bounds: its most extreme legal ratio.
			if custom, ok := largestOfRatio(*constraint, bounded); ok {
				candidates = append(candidates, custom)
			}
		}
	}
	return highestOfNearestRatio(candidates, shape.Ratio)
}

// highestOfNearestRatio picks, among the candidates of the target ratio (else
// of the ratio nearest it), the one with the most pixels.
func highestOfNearestRatio(candidates []sizeValue, ratio float64) (string, bool) {
	nearest := math.Inf(1)
	for _, candidate := range candidates {
		nearest = min(nearest, math.Abs(math.Log(candidate.Ratio/ratio)))
	}
	best, bestArea := "", int64(-1)
	for _, candidate := range candidates {
		distance := math.Abs(math.Log(candidate.Ratio / ratio))
		// The canvas ratio when there is one; else the nearest ratio.
		if eligible := distance < sameRatioTolerance || (nearest >= sameRatioTolerance && distance <= nearest+sameRatioTolerance); !eligible {
			continue
		}
		if area := candidate.Width * candidate.Height; area > bestArea {
			best, bestArea = candidate.Value, area
		}
	}
	return best, best != ""
}

// largestOfRatio is the custom size with the most pixels that the constraint
// accepts and that has the ratio: every edge on the constraint's grid, from
// the longest down, paired with the other edge rounded to the grid. The first
// found wins a tie; the web client searches in the same order.
func largestOfRatio(constraint generation.ImageSizeConstraint, ratio float64) (sizeValue, bool) {
	if !(ratio > 0) || math.IsInf(ratio, 0) || constraint.MaxPixels <= 0 {
		return sizeValue{}, false
	}
	step := max(constraint.Multiple, 1)
	limit := constraint.MaxEdge
	if limit <= 0 {
		limit = int64(math.Ceil(math.Sqrt(float64(constraint.MaxPixels) * max(ratio, 1/ratio))))
	}
	var best sizeValue
	snap := func(value float64) int64 { return int64(math.Round(value/float64(step))) * step }
	for multiple := limit / step; multiple >= 1; multiple-- {
		edge := multiple * step
		for _, size := range [2][2]int64{{edge, snap(float64(edge) / ratio)}, {snap(float64(edge) * ratio), edge}} {
			width, height := size[0], size[1]
			if width <= 0 || height <= 0 || width*height <= best.Width*best.Height {
				continue
			}
			if constraint.Check(width, height) != nil || !sameRatio(float64(width)/float64(height), ratio) {
				continue
			}
			best = sizeValue{Value: fmt.Sprintf("%dx%d", width, height), Width: width, Height: height, Ratio: float64(width) / float64(height)}
		}
	}
	return best, best.Width > 0
}

// boundedRatio is the ratio within the constraint's longest-to-shortest bound.
func boundedRatio(constraint generation.ImageSizeConstraint, ratio float64) float64 {
	if constraint.MaxRatio <= 0 {
		return ratio
	}
	return min(max(ratio, 1/constraint.MaxRatio), constraint.MaxRatio)
}

// canvasTierParameters are the resolution tiers a person may choose: the
// parameters the Schema's ratio_pixel_tiers covers (Gemini's image_size). On a
// canvas turn they take their highest tier; one the person may not choose
// (Gemini declares image_size read-only at 4K) is the Offering's own.
func canvasTierParameters(offering generation.Offering, artboard catalog.CanvasArtboardSchema) []generation.Parameter {
	var parameters []generation.Parameter
	for _, parameter := range offering.Parameters {
		if _, tiered := artboard.RatioPixelTiers[parameter.Name]; tiered && offering.UserParameterAllowed(parameter) {
			parameters = append(parameters, parameter)
		}
	}
	return parameters
}

// canvasRatioValues are the values the canvas sets on a canvas turn (#653),
// one per output-size parameter and resolution tier a person may choose: each
// output size gets canvasRatioChoice; each tier, the option whose pixels for
// the chosen ratio are the most (first in declared order on a tie).
func canvasRatioValues(offering generation.Offering, artboard catalog.CanvasArtboardSchema, width, height int) map[string]string {
	shape := canvasShapeOf(artboard, width, height)
	values := map[string]string{}
	ratio := ""
	for _, parameter := range canvasSizeParameters(offering) {
		if _, tiered := artboard.RatioPixelTiers[parameter.Name]; tiered {
			continue
		}
		if value, ok := canvasRatioChoice(parameter, shape); ok {
			values[parameter.Name] = value
			if size, _ := parseSizeValue(value); size.Width == 0 && ratio == "" {
				ratio = value
			}
		}
	}
	for _, parameter := range canvasTierParameters(offering, artboard) {
		best, bestArea := "", 0
		for _, option := range parameter.Options {
			pixels := artboard.RatioPixelTiers[parameter.Name][option.Value][ratio]
			if area := pixels.Width * pixels.Height; area > bestArea {
				best, bestArea = option.Value, area
			}
		}
		if best != "" {
			values[parameter.Name] = best
		}
	}
	return values
}

// applyCanvasOutputSize sets the output size from the canvas on a canvas turn
// of a tool that binds the snapshot (canvasRatioValues). Anything else is left
// as it is.
//
// #614: an output size the Agent chose (agentChosen: its set_params value
// for the Offering or its call's own argument) is carried out, not replaced
// by the artboard's: the canvas then sets none of the output-size parameters.
func (executor *Executor) applyCanvasOutputSize(command chat.ToolCommand, offering generation.Offering, input map[string]any, agentChosen map[string]bool) {
	layout := executor.canvasLayout(command)
	if layout == nil {
		return
	}
	parameters := append(canvasSizeParameters(offering), canvasTierParameters(offering, executor.canvasArtboard)...)
	for _, parameter := range parameters {
		if agentChosen[parameter.Name] {
			return
		}
	}
	for name, value := range canvasRatioValues(offering, executor.canvasArtboard, layout.Width, layout.Height) {
		input[name] = value
	}
}

// canvasSizeNote is the Schema's note about the output size of a canvas turn,
// read from the input that was really submitted (after the Offering's own
// forced values): the size at the canvas ratio, or that the model has no such
// ratio and the nearest one was used. Empty when the canvas did not set the
// size: the submitted size is not the one the canvas rule picks (the Agent
// chose it, #614).
func (executor *Executor) canvasSizeNote(command chat.ToolCommand, offering generation.Offering, submitted json.RawMessage) string {
	layout := executor.canvasLayout(command)
	if layout == nil || command.Tool.ResultFields.SizeNote == "" {
		return ""
	}
	var input map[string]any
	if json.Unmarshal(submitted, &input) != nil {
		return ""
	}
	picked := canvasRatioValues(offering, executor.canvasArtboard, layout.Width, layout.Height)
	expected := make(map[string]any, len(input))
	for name, value := range input {
		expected[name] = value
	}
	for name, value := range picked {
		expected[name] = value
	}
	schema.ApplyForcedValues(offering.Parameters, expected)
	for name := range picked {
		if fmt.Sprint(expected[name]) != fmt.Sprint(input[name]) {
			return ""
		}
	}
	shape := canvasShapeOf(executor.canvasArtboard, layout.Width, layout.Height)
	context := executor.chat.Context
	for _, parameter := range canvasSizeParameters(offering) {
		text, _ := input[parameter.Name].(string)
		size, ok := parseSizeValue(text)
		if !ok {
			continue
		}
		template := context.CanvasOutputSizeNearestTemplate
		if sameRatio(size.Ratio, shape.Ratio) {
			template = context.CanvasOutputSizeTemplate
		}
		template = executor.localized(command, template)
		return strings.NewReplacer("{ratio}", shape.Label, "{size}", text).Replace(template)
	}
	return ""
}
