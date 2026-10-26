package generation

import (
	"encoding/json"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// resolveConditionalOutputs pins input-dependent success gates into the Task
// snapshot. Later observations and restart recovery never consult live Schema.
func resolveConditionalOutputs(offering Offering, input json.RawMessage) (Offering, error) {
	conditional := false
	for _, output := range offering.Outputs {
		if output.RequiredWhen != nil {
			conditional = true
			break
		}
	}
	if !conditional {
		return offering, nil
	}
	var values map[string]any
	if err := json.Unmarshal(input, &values); err != nil || values == nil {
		return Offering{}, fmt.Errorf("conditional outputs require normalized object input")
	}
	offering.Outputs = append([]AssetRequirement(nil), offering.Outputs...)
	for index, output := range offering.Outputs {
		if output.RequiredWhen == nil {
			continue
		}
		if !schema.ConditionMatches(*output.RequiredWhen, values) {
			zero := 0
			offering.Outputs[index].Minimum = &zero
		}
	}
	return offering, nil
}
