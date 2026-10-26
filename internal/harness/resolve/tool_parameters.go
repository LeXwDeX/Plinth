package resolve

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
)

// conversationHandlePattern is the shape of a chat object handle (@img2,
// @selected); the executor resolves it against this session's history.
const conversationHandlePattern = `^@\S+$`

// toolParameters freezes only the model-visible parameter contract. Wire names,
// mappings, prompt-delivery policy and credentials can never enter this object.
//
// A conversation input (ConversationParameter) is offered as the handle of an
// image of this chat; conversationHint, from the Schema, tells the model so.
func toolParameters(selected harness.Selection, offering generation.Offering, conversationHint string) (json.RawMessage, error) {
	properties := map[string]any{}
	required := []string{}
	values := conditionValues(offering, selected.FixedParameters)
	for _, p := range offering.Parameters {
		if ConversationParameter(p) {
			if _, locked := selected.FixedParameters[p.Name]; locked {
				continue
			}
			handle := map[string]any{"type": "string", "pattern": conversationHandlePattern}
			field := map[string]any{"description": strings.TrimSpace(p.Description + " " + conversationHint)}
			if p.Type == "asset-array" {
				field["type"] = "array"
				field["items"] = handle
				field["uniqueItems"] = true
				if p.MinItems > 0 {
					field["minItems"] = p.MinItems
				}
				if p.MaxItems > 0 {
					field["maxItems"] = p.MaxItems
				}
			} else {
				field["type"] = "string"
				field["pattern"] = conversationHandlePattern
			}
			if p.Required && !generation.ParameterOmitted(p, values) {
				required = append(required, p.Name)
			}
			properties[p.Name] = field
			continue
		}
		fixed, locked := selected.FixedParameters[p.Name]
		if !locked && !slices.Contains(selected.FillableParameters, p.Name) {
			continue
		}
		field := map[string]any{"description": p.Description}
		switch p.Type {
		case "text", "select":
			field["type"] = "string"
			if p.MaxLength > 0 {
				field["maxLength"] = p.MaxLength
			}
		case "boolean":
			field["type"] = "boolean"
		case "number":
			field["type"] = "number"
			if p.Integer {
				field["type"] = "integer"
			}
			if p.Minimum != nil {
				field["minimum"] = *p.Minimum
			}
			if p.Maximum != nil {
				field["maximum"] = *p.Maximum
			}
		case "string-array":
			field["type"] = "array"
			field["items"] = map[string]any{"type": "string"}
			field["uniqueItems"] = true
			if p.MinItems > 0 {
				field["minItems"] = p.MinItems
			}
			if p.MaxItems > 0 {
				field["maxItems"] = p.MaxItems
			}
		default:
			return nil, fmt.Errorf("harness tool parameter %q has unsupported type", p.Name)
		}
		if len(p.Options) > 0 {
			options := make([]string, 0, len(p.Options))
			for _, o := range p.Options {
				options = append(options, o.Value)
			}
			if p.Type == "string-array" {
				field["items"].(map[string]any)["enum"] = options
			} else {
				field["enum"] = options
			}
		}
		if locked {
			field["const"] = fixed
		} else if len(p.Default) > 0 {
			field["default"] = p.Default
		}
		if p.Required && !locked && !generation.ParameterOmitted(p, values) {
			if value, ok := values[p.Name]; !ok || value == nil || value == "" {
				required = append(required, p.Name)
			}
		}
		properties[p.Name] = field
	}
	return json.Marshal(map[string]any{"type": "object", "properties": map[string]any{
		"offering_id":       map[string]any{"type": "string", "const": selected.OfferingID},
		"parameter_reasons": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string", "maxLength": 4096}},
		"parameters":        map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false},
	}, "additionalProperties": false})
}
