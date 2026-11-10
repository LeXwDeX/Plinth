package generationtool

import (
	"encoding/json"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// ReferenceViews reads the view of each reference picture of a step whose
// Offering takes several views of one object (#65), from its frozen input: the
// reference-views parameter, or the parameter's option order when the input
// leaves it out. ok is false for an Offering without that parameter.
func ReferenceViews(offering generation.Offering, input json.RawMessage) (views []string, ok bool) {
	var values map[string]json.RawMessage
	if json.Unmarshal(input, &values) != nil {
		return nil, false
	}
	var parameter *generation.Parameter
	pictures := 0
	for index := range offering.Parameters {
		switch offering.Parameters[index].Semantic {
		case generation.SemanticReferenceViews:
			parameter = &offering.Parameters[index]
		case generation.SemanticReferenceImage:
			var assets []json.RawMessage
			if json.Unmarshal(values[offering.Parameters[index].Name], &assets) == nil {
				pictures = len(assets)
			}
		}
	}
	if parameter == nil {
		// An order-only multiview Offering carries the views by position; the
		// platform's key order names them for the note and the approval card
		// (#243).
		var options struct {
			Endpoint string `json:"endpoint"`
		}
		if len(offering.ProviderOptions) == 0 ||
			json.Unmarshal(offering.ProviderOptions, &options) != nil ||
			options.Endpoint != "multi-image-to-3d" {
			return nil, false
		}
		for _, view := range defaultViewOrder {
			if len(views) >= pictures {
				break
			}
			views = append(views, view)
		}
		return views, len(views) > 0
	}
	if json.Unmarshal(values[parameter.Name], &views) == nil && len(views) > 0 {
		return views, true
	}
	for _, option := range parameter.Options {
		if len(views) == pictures {
			break
		}
		views = append(views, option.Value)
	}
	return views, true
}

// ReferenceViewsNote says which views a multiview step is made from, such as
// "由 正面 / 左侧 / 背面 视图生成", with the Offering's own view labels. Empty
// when the tool names no reference_note result or the step is not one.
func ReferenceViewsNote(tool catalog.ChatToolSchema, offering generation.Offering,
	multiview catalog.ChatThreeViewMultiviewSchema, input json.RawMessage, text func(string) string) string {
	if tool.ResultFields.ReferenceNote == "" || multiview.ViewsNote == "" {
		return ""
	}
	views, ok := ReferenceViews(offering, input)
	if !ok || len(views) == 0 {
		return ""
	}
	if text == nil {
		text = func(value string) string { return value }
	}
	labels := make([]string, 0, len(views))
	for _, view := range views {
		labels = append(labels, text(ReferenceViewLabel(offering, view)))
	}
	return strings.ReplaceAll(text(multiview.ViewsNote), "{views}", strings.Join(labels, multiview.ViewSeparator))
}

// ReferenceViewLabel is the Offering's own label of a view key, or the key.
func ReferenceViewLabel(offering generation.Offering, view string) string {
	for _, parameter := range offering.Parameters {
		if parameter.Semantic != generation.SemanticReferenceViews {
			continue
		}
		for _, option := range parameter.Options {
			if option.Value == view && option.Label != "" {
				return option.Label
			}
		}
	}
	return view
}
