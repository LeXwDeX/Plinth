package hyper3d

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Rodin Gen-2.5 takes image_label next to images: an array that matches the
// pictures by position, one mark for the side each shows (#376). The request
// carries it as one text part per mark, in picture order and ahead of the
// pictures (two real requests of 2026-11-19; the wire is the Offering's own
// declaration order, imagewire.EncodeMultipart). More marks than pictures is
// refused by the service (IMAGE_LABEL_LENGTH_TOO_LONG). A short list is
// documented, not invented: "Entries default to ? (unknown) and the list is
// padded to the image count" (https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5,
// read 2026-11-23), so Plinth pads the missing positions with ? before sending
// (#487; before that the comment claimed the behaviour was unobserved).

// imageLabelsWire is the field the marks are sent in.
const imageLabelsWire = "image_label"

// imagesWire is the field the pictures are sent in.
const imagesWire = "images"

// rodinImageLabels are the marks the Gen-2.5 page lists for image_label:
// front, front-left, front-right, back, back-left, back-right, left, right, up,
// down, and ? when the side is not known.
var rodinImageLabels = []string{"F", "FL", "FR", "B", "BL", "BR", "L", "R", "U", "D", "?"}

// imageLabelsParameter finds the parameter the Offering declares with the
// reference-image-labels semantic. Adapter code never keys off an Offering ID or
// a parameter name: the Schema says which parameter holds the marks.
func imageLabelsParameter(offering generation.Offering) (generation.Parameter, bool) {
	for _, parameter := range offering.Parameters {
		if parameter.Semantic == schema.SemanticReferenceImageLabels {
			return parameter, true
		}
	}
	return generation.Parameter{}, false
}

// validateImageLabelsDeclaration checks the wire shape of an Offering that
// declares the marks. An Offering without the semantic is not checked here.
// The rules keep what was observed true of every request the Schema can make:
// the marks are sent verbatim, never defaulted (a default would give pictures of
// another count the wrong number of marks), may repeat (two pictures can both
// show the front, or both be unknown) and come before the pictures.
func validateImageLabelsDeclaration(offering generation.Offering) error {
	declared := 0
	for _, parameter := range offering.Parameters {
		if parameter.Semantic == schema.SemanticReferenceImageLabels {
			declared++
		}
	}
	if declared == 0 {
		return nil
	}
	if declared > 1 {
		return fmt.Errorf("Rodin 只能声明一个 reference-image-labels 参数")
	}
	if offering.Capability != generation.ImageTo3D {
		return fmt.Errorf("Rodin 的 reference-image-labels 只用于图生 3D")
	}
	labelsIndex, imagesIndex := -1, -1
	for index, parameter := range offering.Parameters {
		switch {
		case parameter.Semantic == schema.SemanticReferenceImageLabels:
			labelsIndex = index
		case effectiveWireName(parameter) == imagesWire:
			imagesIndex = index
		}
	}
	if imagesIndex < 0 {
		return fmt.Errorf("Rodin 的 reference-image-labels 需要 %s 输入映射", imagesWire)
	}
	parameter, images := offering.Parameters[labelsIndex], offering.Parameters[imagesIndex]
	switch {
	case parameter.Type != "string-array" || effectiveWireName(parameter) != imageLabelsWire:
		return fmt.Errorf("Rodin 的 reference-image-labels 参数必须是发往 %s 的 string-array", imageLabelsWire)
	case labelsIndex > imagesIndex:
		return fmt.Errorf("Rodin 的 reference-image-labels 参数必须声明在 %s 参数之前，标记才先于图片发送", imagesWire)
	case parameter.Required || parameter.Fixed || parameter.MinItems != 0 || len(bytes.TrimSpace(parameter.Default)) > 0:
		return fmt.Errorf("Rodin 的 reference-image-labels 参数必须可省略，不得必填、固定或带默认值")
	case !parameter.AllowDuplicateValues:
		return fmt.Errorf("Rodin 的 reference-image-labels 参数必须声明 allow_duplicate_values：标记按位置对应图片，可以重复")
	case len(parameter.WireValues) > 0 || len(parameter.WireTemplate) > 0:
		return fmt.Errorf("Rodin 的 reference-image-labels 参数按原值发送，不得声明 wire_values 或 wire_template")
	case parameter.MaxItems < 1 || parameter.MaxItems != images.MaxItems:
		return fmt.Errorf("Rodin 的 reference-image-labels 参数 max_items 必须等于图片参数的 max_items，每张图才能有一个标记")
	case len(parameter.Options) == 0:
		return fmt.Errorf("Rodin 的 reference-image-labels 参数必须声明可选标记")
	}
	for _, option := range parameter.Options {
		if !slices.Contains(rodinImageLabels, option.Value) {
			return fmt.Errorf("Rodin 的 reference-image-labels 选项 %q 不是官方的 image_label 取值", option.Value)
		}
	}
	return nil
}

// ValidateInput refuses a request whose marks cannot be sent as the one array
// Rodin reads, before the step is prepared or paid. It runs on the normalized
// input and the pictures already resolved for it, and contacts nobody.
func (a *Adapter) ValidateInput(_ context.Context, request generation.InputValidationRequest) error {
	if _, declared := imageLabelsParameter(request.Offering); !declared {
		return nil
	}
	var input map[string]any
	if err := json.Unmarshal(request.Input, &input); err != nil || input == nil {
		return fail(generation.FailureInvalidInput, false, "输入不是 JSON 对象")
	}
	return checkImageLabels(request.Offering, input, pictureCount(request.Offering, request.InputAssets))
}

var _ generation.ProviderInputValidator = (*Adapter)(nil)

// pictureCount is the number of pictures the request sends: those of every
// parameter whose wire name is images.
func pictureCount(offering generation.Offering, assets map[string][]generation.InputAsset) int {
	count := 0
	for _, parameter := range offering.Parameters {
		if effectiveWireName(parameter) == imagesWire {
			count += len(assets[parameter.Name])
		}
	}
	return count
}

// checkImageLabels is the one rule ValidateInput and Start share. No marks (the
// parameter absent, null or empty) is the request without the parameter. More
// marks than pictures is what Rodin refuses. Fewer marks than pictures is
// completed with "?" (the official unknown mark) before the request is sent, so
// every picture keeps a position; each supplied mark must be one the Schema
// declares and the Gen-2.5 page lists.
func checkImageLabels(offering generation.Offering, input map[string]any, pictures int) error {
	parameter, declared := imageLabelsParameter(offering)
	if !declared || generation.ParameterOmitted(parameter, input) {
		return nil
	}
	labels, err := imageLabelValues(input[parameter.Name])
	if err != nil {
		return imageLabelsRefusal(offering, parameter, err)
	}
	if len(labels) > pictures {
		return imageLabelsRefusal(offering, parameter, fmt.Errorf("%d marks for %d pictures: more marks than pictures", len(labels), pictures))
	}
	for index, label := range labels {
		if !slices.Contains(rodinImageLabels, label) || !labelDeclared(parameter, label) {
			return imageLabelsRefusal(offering, parameter, fmt.Errorf("mark %d (%q) is not a declared mark", index+1, label))
		}
	}
	return nil
}

// padImageLabels completes a short label list with "?" so the request carries
// exactly one mark per picture. The official image_label list uses "?" for an
// unknown side; the page does not say what a short list means, so Plinth makes
// the missing positions explicit instead of sending a list that does not line
// up with the pictures.
func padImageLabels(offering generation.Offering, input map[string]any, pictures int) error {
	parameter, declared := imageLabelsParameter(offering)
	if !declared || generation.ParameterOmitted(parameter, input) {
		return nil
	}
	labels, err := imageLabelValues(input[parameter.Name])
	if err != nil {
		return imageLabelsRefusal(offering, parameter, err)
	}
	if len(labels) == 0 || len(labels) >= pictures {
		return nil
	}
	padded := make([]any, pictures)
	for index := range padded {
		padded[index] = "?"
	}
	for index, label := range labels {
		padded[index] = label
	}
	input[parameter.Name] = padded
	return nil
}

// imageLabelValues reads the marks of a normalized input.
func imageLabelValues(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("marks must be an array")
	}
	labels := make([]string, 0, len(items))
	for _, item := range items {
		label, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("a mark must be a string")
		}
		labels = append(labels, label)
	}
	return labels, nil
}

func labelDeclared(parameter generation.Parameter, label string) bool {
	return slices.ContainsFunc(parameter.Options, func(option generation.ParameterOption) bool { return option.Value == label })
}

// imageLabelsRefusal refuses the marks with the parameter's public Schema label
// (#358), the way Tripo refuses its view keys: the Provider diagnostic and the
// cause stay in the internal error chain and the text for the requester names
// neither the parameter nor the wire field.
func imageLabelsRefusal(offering generation.Offering, parameter generation.Parameter, cause error) error {
	reason, values := schema.InputRefusalParameters, schema.InputRefusalValues{}
	if strings.TrimSpace(parameter.Label) != "" {
		reason, values = schema.InputRefusalParameter, schema.InputRefusalValues{Label: parameter.Label}
	}
	return generation.ProviderInputRefusalFailure(
		&generation.Failure{Class: generation.FailureInvalidInput},
		offering, reason, values, "input parameter invalid",
		fmt.Errorf("parameter %q: %w", parameter.Name, cause))
}
