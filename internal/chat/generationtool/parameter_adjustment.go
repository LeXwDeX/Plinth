package generationtool

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func (e *Executor) parameterReasons(command chat.ToolCommand, o generation.Offering, input json.RawMessage) (map[string]string, map[string]string, error) {
	reasons := map[string]string{}
	sources := map[string]string{}
	values := map[string]json.RawMessage{}
	if err := json.Unmarshal(input, &values); err != nil {
		return nil, nil, err
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
			return nil, nil, fmt.Errorf("invalid parameter reasons: %w", err)
		}
	}
	for k, v := range args.Reasons {
		reasons[k] = v
	}
	// #103: the session production purpose explains the values it contributed,
	// unless the Agent gave its own reason for the same parameter. The frozen
	// adjustment then reads "profile:<id>" instead of "agent". Every step
	// chose its budget values against its own final input (#129, #139), so the
	// same input decides them here.
	if profile, overrides := e.purposeParameterValues(command, o, values); profile.ID != "" {
		// A value a layer above the purpose set in this call stays that layer's
		// even when it happens to equal the purpose value (#103).
		overridden := e.laterParameterNames(command, o)
		for name, value := range overrides {
			if _, explained := reasons[name]; explained {
				continue
			}
			if overridden[name] {
				continue
			}
			// Only an advised change is frozen as an adjustment. A purpose may
			// also set a public parameter the Offering does not mark advised;
			// that value needs no reason, and freezeParameterAdjustments would
			// refuse one (#103).
			if !advisedParameter(o, name) {
				continue
			}
			if current, exists := values[name]; exists && sameHarnessJSON(current, value, command) {
				reasons[name] = profile.Reason
				sources[name] = "profile:" + profile.ID
			}
		}
	}
	return reasons, sources, nil
}

// advisedParameter reports whether the Offering freezes this parameter's
// non-default value as an adjustment that needs a reason.
func advisedParameter(o generation.Offering, name string) bool {
	for _, parameter := range o.Parameters {
		if parameter.Name == name {
			return parameter.AdjustmentPolicy == "advised"
		}
	}
	return false
}

// laterParameterNames are the parameters a layer above the production purpose
// set for this call: the person's panel, the Agent's explicit arguments or a
// remembered Agent setting. The purpose keeps no credit for a value one of
// them supplied, even when the values are equal.
func (e *Executor) laterParameterNames(command chat.ToolCommand, o generation.Offering) map[string]bool {
	names := map[string]bool{}
	for name := range command.Options.Parameters {
		names[name] = true
	}
	for name := range command.ExplicitParameters {
		names[name] = true
	}
	if e.offeringParameters != nil {
		for name := range e.offeringParameters(command.OwnerID, command.SessionID, string(o.ID)) {
			names[name] = true
		}
	}
	// The person's panel can carry a mapped override (pose, size) instead of a
	// parameter entry.
	overrideValues := map[string]string{
		"aspect_ratio": command.Options.AspectRatio,
		"pose_mode":    command.Options.PoseMode,
		"size":         command.Options.Size,
		"image_size":   command.Options.ImageSize,
	}
	for semantic, parameter := range command.Tool.OverrideMappings {
		if strings.TrimSpace(overrideValues[semantic]) != "" {
			names[parameter] = true
		}
	}
	// The Agent can also pass a mapped generation argument directly in the
	// call instead of through the parameter panel.
	var arguments map[string]json.RawMessage
	if len(command.Call.Arguments) > 0 {
		_ = json.Unmarshal(command.Call.Arguments, &arguments)
	}
	for argument, semantic := range command.Tool.ArgumentMappings {
		if _, given := arguments[argument]; !given {
			continue
		}
		if parameter, mapped := command.Tool.OverrideMappings[semantic]; mapped {
			names[parameter] = true
		}
	}
	// A processing tool passes the Agent's values through its declared
	// parameter arguments (#129).
	if processing := command.Tool.Processing; processing != nil {
		for argument, parameter := range processing.ParameterArguments {
			if _, given := arguments[argument]; given {
				names[parameter] = true
			}
		}
	}
	return names
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
