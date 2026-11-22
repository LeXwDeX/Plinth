package generation

import (
	"encoding/json"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// ResolveConditionalOutputs pins input-dependent success gates into the Task
// snapshot, so later observations and restart recovery never consult live Schema.
// Submit calls it on the Offering it is about to freeze; tests call it on a
// Catalog Offering to read the same decision.
func ResolveConditionalOutputs(offering Offering, input json.RawMessage) (Offering, error) {
	return resolveConditionalOutputs(offering, input)
}

// resolveConditionalOutputs pins input-dependent success gates into the Task
// snapshot. Later observations and restart recovery never consult live Schema.
//
// A required_when slot the input does not select keeps its identity but drops to
// minimum zero (an absent required slot is allowed, and bytes supplied for it
// are still checked). A requested_when slot the input does not select is a slot
// the request never asked for (#462): it is removed from the frozen Offering, so
// it takes no part in the success gate, the resource-state projection or the
// Provider's post-success work list. Both decisions are then facts of the
// snapshot rather than rules every later reader has to re-evaluate.
func resolveConditionalOutputs(offering Offering, input json.RawMessage) (Offering, error) {
	conditional := false
	for _, output := range offering.Outputs {
		if output.RequiredWhen != nil || output.RequestedWhen != nil {
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
	kept := make([]AssetRequirement, 0, len(offering.Outputs))
	for index, output := range offering.Outputs {
		if output.RequestedWhen != nil {
			if schema.ConditionMatches(*output.RequestedWhen, values) {
				kept = append(kept, output)
			}
			continue
		}
		if output.RequiredWhen != nil && !schema.ConditionMatches(*output.RequiredWhen, values) {
			zero := 0
			offering.Outputs[index].Minimum = &zero
		}
		kept = append(kept, offering.Outputs[index])
	}
	offering.Outputs = kept
	return offering, nil
}
