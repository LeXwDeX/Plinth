package generation

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func sameParameterValue(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}
func freezeParameterAdjustments(o Offering, normalized json.RawMessage, reasons map[string]string) ([]ParameterAdjustment, error) {
	values := map[string]json.RawMessage{}
	if err := json.Unmarshal(normalized, &values); err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	result := []ParameterAdjustment{}
	for _, p := range o.Parameters {
		if p.AdjustmentPolicy != "advised" {
			continue
		}
		allowed[p.Name] = true
		value, exists := values[p.Name]
		recommended := p.Default
		baseline := map[string]any{}
		_ = json.Unmarshal(normalized, &baseline)
		var defaultValue any
		if json.Unmarshal(p.Default, &defaultValue) == nil {
			baseline[p.Name] = defaultValue
			schema.ApplyForcedValues(o.Parameters, baseline)
			recommended, _ = json.Marshal(baseline[p.Name])
		}
		if !exists || sameParameterValue(value, recommended) {
			continue
		}
		reason := strings.TrimSpace(reasons[p.Name])
		if reason == "" || len(reason) > 4096 {
			return nil, &Error{Code: ErrInvalidInput, Operation: "prepare_input", Detail: fmt.Sprintf("参数 %q 不建议更改，调整时须说明理由", p.Name)}
		}
		result = append(result, ParameterAdjustment{Name: p.Name, Label: p.Label, RecommendedValue: append(json.RawMessage(nil), recommended...), Value: append(json.RawMessage(nil), value...), Reason: reason, Advice: p.AdjustmentReason, Source: "agent"})
	}
	for name, reason := range reasons {
		if !allowed[name] || len(reason) > 4096 {
			return nil, &Error{Code: ErrInvalidInput, Operation: "prepare_input", Detail: fmt.Sprintf("参数 %q 不接受调整理由", name)}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}
