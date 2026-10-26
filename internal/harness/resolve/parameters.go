package resolve

import (
	"encoding/json"
	"slices"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func (s Service) validateParameters(selected harness.Selection, offering generation.Offering) error {
	parameters := map[string]generation.Parameter{}
	for _, parameter := range offering.Parameters {
		parameters[parameter.Name] = parameter
	}
	for name, value := range selected.FixedParameters {
		parameter, ok := parameters[name]
		if !ok || !offering.UserParameterAllowed(parameter) {
			return invalid("parameter %q cannot be fixed", name)
		}
		if slices.Contains(selected.FillableParameters, name) {
			return invalid("parameter %q cannot be fixed and fillable", name)
		}
		if _, err := catalog.ValidateHarnessParameter(parameter, value); err != nil {
			return invalid("parameter %q: %v", name, err)
		}
	}
	for _, name := range selected.FillableParameters {
		parameter, ok := parameters[name]
		if !ok || !offering.AgentParameterAllowed(parameter) {
			return invalid("parameter %q cannot be fillable", name)
		}
	}
	return nil
}

func editable(offering generation.Offering, parameter generation.Parameter) bool {
	return offering.UserParameterAllowed(parameter)
}

func generationSourceRole(parameter generation.Parameter) (schema.ParameterSemantic, bool) {
	return schema.SourceParameterRole(parameter)
}
func hasModelSource(offering generation.Offering) bool {
	for _, parameter := range offering.Parameters {
		role, ok := generationSourceRole(parameter)
		if ok && (role == schema.SemanticSourceTask || role == schema.SemanticSourceAsset) {
			return true
		}
	}
	return false
}
func sourceParameter(offering generation.Offering, selected harness.Selection) string {
	if len(selected.Sources) > 0 && selected.Sources[0].Parameter != "*" {
		return selected.Sources[0].Parameter
	}
	for _, parameter := range offering.Parameters {
		role, ok := generationSourceRole(parameter)
		if ok && (role == schema.SemanticSourceTask || role == schema.SemanticSourceAsset) {
			return parameter.Name
		}
	}
	return ""
}
func conditionValues(offering generation.Offering, input map[string]json.RawMessage) map[string]any {
	values := map[string]any{}
	for _, parameter := range offering.Parameters {
		if len(parameter.Default) > 0 {
			var value any
			if json.Unmarshal(parameter.Default, &value) == nil {
				values[parameter.Name] = value
			}
		}
	}
	for name, raw := range input {
		var value any
		if json.Unmarshal(raw, &value) == nil {
			values[name] = value
		}
	}
	generation.ApplyForcedValues(offering.Parameters, values)
	return values
}
