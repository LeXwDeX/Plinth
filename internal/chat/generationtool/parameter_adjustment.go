package generationtool

import (
	"encoding/json"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func (e *Executor) parameterReasons(command chat.ToolCommand, o generation.Offering, input json.RawMessage) (map[string]string, error) {
	reasons := map[string]string{}
	values := map[string]json.RawMessage{}
	if err := json.Unmarshal(input, &values); err != nil {
		return nil, err
	}
	if !activeHarness(command) && command.Tool.Processing == nil && e.offeringParameterReasons != nil {
		for name, entry := range e.offeringParameterReasons(command.OwnerID, command.SessionID, string(o.ID)) {
			if sameHarnessJSON(values[name], entry.Value, command) {
				reasons[name] = entry.Reason
			}
		}
	}
	var args struct {
		Reasons map[string]string `json:"parameter_reasons"`
	}
	if len(command.Call.Arguments) > 0 {
		if err := json.Unmarshal(command.Call.Arguments, &args); err != nil {
			return nil, fmt.Errorf("invalid parameter reasons: %w", err)
		}
	}
	for k, v := range args.Reasons {
		reasons[k] = v
	}
	return reasons, nil
}

// Ephemeral turn input still accepts declared input assets (including legacy
// mask parameters without a semantic). They are resolved with owner checks;
// neither the Agent settings store nor set_params may persist these sources.
func turnParameterAllowed(o generation.Offering, p generation.Parameter) bool {
	if o.UserParameterAllowed(p) {
		return true
	}
	if p.Type != "asset" && p.Type != "asset-array" {
		return false
	}
	if p.Fixed || p.AdjustmentPolicy != "" || o.ExtraConsentParameter() == p.Name {
		return false
	}
	if p.Presentation != nil && (p.Presentation.Surface == schema.SurfaceHidden || p.Presentation.Surface == schema.SurfaceReadOnly) {
		return false
	}
	_, source := schema.SourceParameterRole(p)
	return !source
}
