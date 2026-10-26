package schema

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
)

// ParameterOmitted applies wire omission independently of UI disabled state.
func ParameterOmitted(parameter Parameter, values map[string]any) bool {
	for _, condition := range parameter.OmitWhen {
		if ConditionMatches(condition, values) {
			return true
		}
	}
	return false
}

// ConditionMatches 按声明算子评估单个参数条件。
func ConditionMatches(condition ParameterCondition, values map[string]any) bool {
	value, exists := values[condition.Parameter]
	// Normalize numeric representations used by JSON decoders and typed callers.
	if encoded, err := json.Marshal(value); err == nil {
		_ = json.Unmarshal(encoded, &value)
	}
	present := exists && value != nil && value != ""
	if s, ok := value.(string); ok {
		present = exists && strings.TrimSpace(s) != ""
	}
	var expected any
	_ = json.Unmarshal(condition.Value, &expected)
	equal := reflect.DeepEqual(value, expected)
	truthy := present && value != false && value != float64(0)
	switch condition.Operator {
	case "equals":
		return equal
	case "not-equals":
		return !equal
	case "present":
		return present
	case "absent":
		return !present
	case "truthy":
		return truthy
	case "falsy":
		return !truthy
	}
	return false
}

// ApplyForcedValues 在服务端规范化阶段执行 forced_when 声明：任一条件
// 命中即以声明值覆盖当前取值。返回是否发生了覆盖。
func ApplyForcedValues(parameters []Parameter, values map[string]any) bool {
	changed := ApplyLegacyValues(parameters, values)
	for _, parameter := range parameters {
		for _, forced := range parameter.ForcedWhen {
			if !ConditionMatches(forced.When, values) {
				continue
			}
			var forcedValue any
			decoder := json.NewDecoder(bytes.NewReader(forced.Value))
			decoder.UseNumber()
			if err := decoder.Decode(&forcedValue); err != nil {
				continue
			}
			values[parameter.Name] = forcedValue
			changed = true
			break
		}
	}
	return changed
}

// ApplyLegacyValues replaces a retired value with the value its parameter
// declares for it in legacy_values.
func ApplyLegacyValues(parameters []Parameter, values map[string]any) bool {
	changed := false
	for _, parameter := range parameters {
		text, ok := values[parameter.Name].(string)
		if !ok {
			continue
		}
		raw, retired := parameter.LegacyValues[text]
		if !retired {
			continue
		}
		var replacement any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&replacement) != nil {
			continue
		}
		values[parameter.Name] = replacement
		changed = true
	}
	return changed
}

// WireInput 返回交给 Provider 的输入：去掉声明了 prompt_delivery 的参数。
// 这些参数的取值在规范化时已写进提示词，不再上线；没有这类参数时原样返回。
func WireInput(offering Offering, input json.RawMessage) json.RawMessage {
	delivered := false
	for _, parameter := range offering.Parameters {
		if parameter.PromptDelivery != nil {
			delivered = true
			break
		}
	}
	if !delivered || len(input) == 0 {
		return input
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(input, &values); err != nil {
		return input
	}
	for _, parameter := range offering.Parameters {
		if parameter.PromptDelivery != nil {
			delete(values, parameter.Name)
		}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return input
	}
	return encoded
}
