package tripo

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// referenceWireMultiviewInputs is the Schema-declared provider_options
// reference_wire value that selects Tripo's multiview-to-model image field:
// `inputs` holds one view-key object per image. Adapter code never keys off an
// Offering ID.
const referenceWireMultiviewInputs = "multiview_inputs"

const multiviewViewsParameter = "reference_views"

// multiviewInputsWire reports whether the Offering declares the multiview
// `inputs` wire.
func (a *Adapter) multiviewInputsWire(offering generation.Offering) (bool, error) {
	options, err := a.optionsFor(offering)
	if err != nil {
		return false, err
	}
	return options.ReferenceWire == referenceWireMultiviewInputs, nil
}

// multiviewViews resolves the view key of each reference image. It is a pure
// check of the normalized input, so Start and ValidateInput refuse bad views
// before any upload or paid dispatch.
func multiviewViews(offering generation.Offering, input map[string]any, assetCount int) ([]string, error) {
	if assetCount < 2 || assetCount > len(multiviewWireOrder) {
		return nil, failure(generation.FailureInvalidInput, false, "Tripo 多视图需要 2–4 张参考图")
	}
	views := slices.Clone(multiviewWireOrder[:assetCount])
	if raw, present := input[multiviewViewsParameter]; present && raw != nil {
		declared, err := stringList(raw)
		if err != nil {
			return nil, viewsRefusal(offering, err)
		}
		views = declared
	}
	switch {
	case len(views) != assetCount:
		return nil, viewsRefusal(offering, fmt.Errorf("%d views for %d images", len(views), assetCount))
	case !slices.Contains(views, "front"):
		return nil, viewsRefusal(offering, errors.New("front view missing"))
	}
	seen := make(map[string]struct{}, len(views))
	for _, view := range views {
		if !slices.Contains(multiviewWireOrder, view) {
			return nil, viewsRefusal(offering, fmt.Errorf("unknown view %q", view))
		}
		if _, duplicate := seen[view]; duplicate {
			return nil, viewsRefusal(offering, fmt.Errorf("duplicate view %q", view))
		}
		seen[view] = struct{}{}
	}
	return views, nil
}

func viewsRefusal(offering generation.Offering, cause error) error {
	for _, parameter := range offering.Parameters {
		if parameter.Name == multiviewViewsParameter {
			return inputParameterRefusal(offering, parameter, cause)
		}
	}
	return inputParametersRefusal(offering, cause)
}

func stringList(raw any) ([]string, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, errors.New("views must be an array")
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, errors.New("view must be a string")
		}
		result = append(result, text)
	}
	return result, nil
}

// multiviewInputsBody pairs uploaded file tokens with their views as the
// documented view-key array: [{"front":"<token>"}, {"left":"<token>"}, ...].
func multiviewInputsBody(views, tokens []string) []map[string]string {
	inputs := make([]map[string]string, 0, len(tokens))
	for i, token := range tokens {
		inputs = append(inputs, map[string]string{views[i]: token})
	}
	return inputs
}

// validateMultiviewInputsRequest is the read-only pre-admission check.
func (a *Adapter) validateMultiviewInputsRequest(request generation.InputValidationRequest) error {
	wire, err := a.multiviewInputsWire(request.Offering)
	if err != nil || !wire {
		return nil
	}
	var input map[string]any
	if err := json.Unmarshal(request.Input, &input); err != nil {
		return failure(generation.FailureInvalidInput, false, "规范化输入不是 JSON 对象")
	}
	_, err = multiviewViews(request.Offering, input, len(request.InputAssets["reference_assets"]))
	return err
}
