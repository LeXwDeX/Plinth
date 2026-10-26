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
	out := make(map[string]json.RawMessage, len(values))
	for name, raw := range values {
		p, ok := offeringParameter(o.Parameters, name)
		allowed := o.UserParameterAllowed(p)
		if agent {
			allowed = o.AgentParameterAllowed(p)
		}
		if !ok || !allowed {
			return nil, fmt.Errorf("parameter %q cannot be adjusted", name)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			out[name] = json.RawMessage("null")
			continue
		}
		normalized, err := ValidateHarnessParameter(p, raw)
		if err != nil {
			return nil, err
		}
		if agent && p.AdjustmentPolicy == "advised" {
			recommended, err := ValidateHarnessParameter(p, p.Default)
			if err != nil {
				return nil, err
			}
			if !bytes.Equal(normalized, recommended) && (strings.TrimSpace(reasons[name]) == "" || len(reasons[name]) > 4096) {
				return nil, fmt.Errorf("parameter %q requires a bounded adjustment reason", name)
			}
		}
		out[name] = normalized
	}
	for name, reason := range reasons {
		encodedReason, _ := json.Marshal(strings.TrimSpace(reason))
		p, declared := offeringParameter(o.Parameters, name)
		if _, ok := values[name]; !ok || !declared || p.AdjustmentPolicy != "advised" || len(reason) > 4096 || len(encodedReason) > 4096 {
			return nil, fmt.Errorf("unexpected adjustment reason %q", name)
		}
	}
	return out, nil
}
