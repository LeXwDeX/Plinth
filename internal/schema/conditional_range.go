package schema

import "encoding/json"

// Conditional number ranges (#139): maximum_when entries narrow a number
// parameter's legal range for one combination of the other values, such as
// Tripo's face limit once smart low poly or a quad mesh is on. The range is
// computed here once; normalization, the frozen adjustment baseline and the
// chat tools' purpose values all read it.

// Matches reports whether the entry applies to values: its When condition and
// every AlsoWhen condition match.
func (limit ConditionalMaximum) Matches(values map[string]any) bool {
	if !ConditionMatches(limit.When, values) {
		return false
	}
	for _, condition := range limit.AlsoWhen {
		if !ConditionMatches(condition, values) {
			return false
		}
	}
	return true
}

// NumberRange is the legal range of a number parameter under values: its
// static minimum, and its static maximum narrowed by every matching
// maximum_when entry. A nil bound is open.
func (p Parameter) NumberRange(values map[string]any) (minimum, maximum *float64) {
	minimum, maximum = p.Minimum, p.Maximum
	for _, limit := range p.MaximumWhen {
		if !limit.Matches(values) {
			continue
		}
		if maximum == nil || limit.Maximum < *maximum {
			bound := limit.Maximum
			maximum = &bound
		}
	}
	return minimum, maximum
}

// ClampNumber moves number into the parameter's range under values.
func (p Parameter) ClampNumber(number float64, values map[string]any) float64 {
	minimum, maximum := p.NumberRange(values)
	if maximum != nil && number > *maximum {
		number = *maximum
	}
	if minimum != nil && number < *minimum {
		number = *minimum
	}
	return number
}

// ConvergeDefaults moves each number value the Schema supplied into the range
// its maximum_when entries give under the other values (#139). A default is
// the Schema's own choice, so it follows the combination the caller chose
// (Tripo's two-million face default becomes 20000 with smart low poly); a
// value the caller gave is not defaulted and is validated instead. It reports
// whether a value moved.
func ConvergeDefaults(parameters []Parameter, values map[string]any, defaulted func(string) bool) bool {
	changed := false
	for _, parameter := range parameters {
		if parameter.Type != "number" || len(parameter.MaximumWhen) == 0 || !defaulted(parameter.Name) {
			continue
		}
		number, ok := conditionNumber(values[parameter.Name])
		if !ok {
			continue
		}
		if clamped := parameter.ClampNumber(number, values); clamped != number {
			values[parameter.Name] = clamped
			changed = true
		}
	}
	return changed
}

func conditionNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	}
	return 0, false
}
