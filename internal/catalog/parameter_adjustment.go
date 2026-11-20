package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// ValidateParameterPatch validates a settings mutation without applying defaults
// or granting execution. Cross-field rules still run when preparing the input.
func ValidateParameterPatch(o schema.Offering, values map[string]json.RawMessage, agent bool, reasons map[string]string) (map[string]json.RawMessage, error) {
	out, _, err := NormalizeParameterPatch(o, values, agent, reasons)
	return out, err
}

// NormalizeParameterPatch is ValidateParameterPatch that also returns the
// adjustment reasons the patch keeps: those of advised parameters. A reason for
// a parameter that needs none (a normal setting) is ignored rather than
// refused (#402): it harms nothing, and refusing it lost the whole call and a
// round to a resend. A caller that stores the reasons stores these, never the
// ones it was given, because a stored reason for a parameter that is not
// advised would later fail the generation that freezes the input. A reason
// that names an undeclared parameter, has no value in the same patch or is over
// the bound is still refused, whatever the parameter's policy.
func NormalizeParameterPatch(o schema.Offering, values map[string]json.RawMessage, agent bool, reasons map[string]string) (map[string]json.RawMessage, map[string]string, error) {
	out := make(map[string]json.RawMessage, len(values))
	for name, raw := range values {
		p, ok := offeringParameter(o.Parameters, name)
		allowed := o.UserParameterAllowed(p)
		if agent {
			allowed = o.AgentParameterAllowed(p)
		}
		if !ok || !allowed {
			return nil, nil, fmt.Errorf("parameter %q cannot be adjusted", name)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			out[name] = json.RawMessage("null")
			continue
		}
		normalized, err := ValidateHarnessParameter(p, raw)
		if err != nil {
			return nil, nil, err
		}
		if agent && p.AdjustmentPolicy == "advised" {
			// A parameter without a default is never sent unless the Agent
			// chooses it; every supplied value is then an adjustment.
			adjusted := true
			if len(bytes.TrimSpace(p.Default)) > 0 {
				recommended, err := ValidateHarnessParameter(p, p.Default)
				if err != nil {
					return nil, nil, err
				}
				adjusted = !bytes.Equal(normalized, recommended)
			}
			if adjusted && (strings.TrimSpace(reasons[name]) == "" || len(reasons[name]) > 4096) {
				return nil, nil, fmt.Errorf("parameter %q requires a bounded adjustment reason", name)
			}
		}
		out[name] = normalized
	}
	var kept map[string]string
	for name, reason := range reasons {
		encodedReason, _ := json.Marshal(strings.TrimSpace(reason))
		p, declared := offeringParameter(o.Parameters, name)
		if _, ok := values[name]; !ok || !declared || len(reason) > 4096 || len(encodedReason) > 4096 {
			return nil, nil, fmt.Errorf("unexpected adjustment reason %q", name)
		}
		if p.AdjustmentPolicy != "advised" {
			continue
		}
		if kept == nil {
			kept = make(map[string]string, len(reasons))
		}
		kept[name] = reason
	}
	return out, kept, nil
}
