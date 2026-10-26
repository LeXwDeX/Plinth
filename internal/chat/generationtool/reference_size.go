package generationtool

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #296: editing one picture keeps its shape. Outside a canvas turn, a tool
// whose Schema declares reference_size "single" takes the output size from
// the one reference picture it sends, when nobody chose a size: no value from
// the turn, the session (the 参数 panel, #246), the Agent's set_params or a
// Skill, only the Offering's default. Each output-size parameter a person may
// choose then gets the legal value closest to the picture, by the same rule as
// the canvas (canvasSizeChoice, #284): the picture's own size when legal, else
// its size snapped to the parameter's grid, else the option of the nearest
// shape and then the nearest pixel count. A chosen size always wins, several
// references keep the defaults (no single shape to follow), and on a canvas
// turn the artboard sets the size (#272). The web client's notice computes the
// same value (referenceTurnOutput in src/lib/workspace/canvas-size.ts); both
// run testdata/canvas_turn_sizes.json.

const referenceSizeSingle = "single"

// referenceSizeApplies reports whether the step's reference may set its output
// size: the tool declares it, the turn's canvas does not set it, and exactly
// one picture is sent.
func (executor *Executor) referenceSizeApplies(command chat.ToolCommand, references int) bool {
	return command.Tool.ReferenceSize == referenceSizeSingle && references == 1 && executor.canvasLayout(command) == nil
}

// referenceDimensions reads the pixel size of an input asset the owner holds.
// It reports false when the resolver cannot measure pictures or the picture
// has no readable size; the size is then left to the defaults.
func (executor *Executor) referenceDimensions(ctx context.Context, owner string, asset generation.AssetID) (int, int, bool) {
	reader, ok := executor.references.(chat.ImageDimensionReader)
	if !ok || asset == "" {
		return 0, 0, false
	}
	width, height, err := reader.ImageDimensions(ctx, owner, string(asset))
	if err != nil || width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

// referenceSizeValues are the output-size values the rule picks for a picture
// of this size, one per output-size parameter a person may choose.
func referenceSizeValues(offering generation.Offering, width, height int) map[string]string {
	values := map[string]string{}
	for _, parameter := range canvasSizeParameters(offering) {
		if value, ok := canvasSizeChoice(parameter, width, height); ok {
			values[parameter.Name] = value
		}
	}
	return values
}

// applyReferenceOutputSize sets the output size from the one reference picture
// when the rule applies and none of the Offering's output-size parameters was
// chosen. chosen holds the parameters some layer set (not the tool's or the
// Offering's defaults).
func (executor *Executor) applyReferenceOutputSize(
	ctx context.Context,
	command chat.ToolCommand,
	offering generation.Offering,
	input map[string]any,
	chosen map[string]bool,
	references []generation.AssetID,
) {
	if !executor.referenceSizeApplies(command, len(references)) {
		return
	}
	parameters := canvasSizeParameters(offering)
	if len(parameters) == 0 {
		return
	}
	for _, parameter := range parameters {
		if chosen[parameter.Name] {
			return
		}
	}
	width, height, ok := executor.referenceDimensions(ctx, command.OwnerID, references[0])
	if !ok {
		return
	}
	for name, value := range referenceSizeValues(offering, width, height) {
		input[name] = value
	}
}

// referenceSizeNote is the Schema's note about the output size of a step whose
// one reference set it, read from the input that was really submitted: it kept
// the picture's shape, or which nearest legal size was used. Empty when the
// rule does not apply or the submitted size is not the one it picks (somebody
// chose it).
func (executor *Executor) referenceSizeNote(ctx context.Context, command chat.ToolCommand, offering generation.Offering, submitted json.RawMessage) string {
	if command.Tool.ResultFields.SizeNote == "" || command.Tool.ReferenceSize != referenceSizeSingle {
		return ""
	}
	var input map[string]any
	if json.Unmarshal(submitted, &input) != nil {
		return ""
	}
	references := submittedReferences(offering, input)
	if !executor.referenceSizeApplies(command, len(references)) {
		return ""
	}
	width, height, ok := executor.referenceDimensions(ctx, command.OwnerID, references[0])
	if !ok {
		return ""
	}
	return executor.referenceSizeNoteFor(offering, input, width, height, executor.localizeText(command))
}

// referenceSizeNoteFor words the note for a reference of width×height: the
// submitted output size must be the one the rule picks (after the Offering's
// forced values); a size of the picture's shape follows it, another shape is
// the nearest legal size.
func (executor *Executor) referenceSizeNoteFor(offering generation.Offering, input map[string]any, width, height int, localize func(string) string) string {
	picked := referenceSizeValues(offering, width, height)
	if len(picked) == 0 {
		return ""
	}
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
	context := executor.chat.Context
	reference := fmt.Sprintf("%d×%d", width, height)
	for _, parameter := range canvasSizeParameters(offering) {
		text, _ := input[parameter.Name].(string)
		size, ok := parseSizeValue(text)
		if !ok {
			continue
		}
		template := context.ReferenceOutputSizeNearestTemplate
		if size.sameShape(width, height) {
			template = context.ReferenceOutputSizeTemplate
		}
		if localize != nil {
			template = localize(template)
		}
		return strings.NewReplacer("{reference}", reference, "{size}", text).Replace(template)
	}
	return ""
}

// sameShape reports whether a size has the shape of width×height. For a
// reference, any size of its shape keeps it: a smaller one is the legal size
// nearest a large picture, not a different picture (unlike a canvas above the
// model's maximum, see followsCanvas).
func (size sizeValue) sameShape(width, height int) bool {
	return math.Abs(math.Log(size.Ratio*float64(height)/float64(width))) < sameRatioTolerance
}

// submittedReferences are the reference assets of a frozen input.
func submittedReferences(offering generation.Offering, input map[string]any) []generation.AssetID {
	for _, parameter := range offering.Parameters {
		if parameter.Semantic != generation.SemanticReferenceImage {
			continue
		}
		switch value := input[parameter.Name].(type) {
		case string:
			if value != "" {
				return []generation.AssetID{generation.AssetID(value)}
			}
		case []any:
			assets := make([]generation.AssetID, 0, len(value))
			for _, item := range value {
				if id, ok := item.(string); ok && id != "" {
					assets = append(assets, generation.AssetID(id))
				}
			}
			return assets
		}
		return nil
	}
	return nil
}
