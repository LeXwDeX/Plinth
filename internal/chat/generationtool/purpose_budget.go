package generationtool

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The purpose budget (#129): the session brief's purpose bounds the triangle
// count and the texture size of every 3D generation and processing step, not
// only of the Offerings it names overrides for. A bounded parameter nobody
// chose takes a value within the budget at the purpose's layer, so it keeps
// the purpose's provenance; a value the person or the Agent chose is kept even
// when it is over the budget, and the step's result says so.

// budgetValues are the values the purpose budget gives an Offering's bounded
// public parameters whose baseline is over it. overrides are the purpose's
// own values; current holds values the step already has (a remesh topology,
// Tripo's smart low poly and quad switches).
//
// The baseline is first moved into the range the parameter's maximum_when
// entries give the step's combination (#139), as normalization moves a
// default: the stricter of that range and the budget wins. A purpose override
// or tool default moved that way is returned even within the budget, since
// normalization would refuse it as given; an Offering default is left to
// normalization.
func (executor *Executor) budgetValues(command chat.ToolCommand, profile catalog.ChatModel3DUsageProfile,
	offering generation.Offering, overrides, current map[string]json.RawMessage) map[string]json.RawMessage {
	usage := executor.chat.Model3DUsage
	if len(usage.BudgetLimits) == 0 {
		return nil
	}
	budget, _ := modelReviewUsageBudget(usage, profile, command.Options.ProductionBrief)
	values := map[string]json.RawMessage{}
	for _, parameter := range offering.Parameters {
		if len(parameter.Default) > 0 {
			values[parameter.Name] = parameter.Default
		}
	}
	for _, layer := range []map[string]json.RawMessage{command.Tool.DefaultParameters, overrides, current} {
		for name, value := range layer {
			values[name] = value
		}
	}
	combination := decodedValues(values)
	result := map[string]json.RawMessage{}
	for _, parameter := range offering.Parameters {
		limit, bounded := usage.BudgetLimit(parameter.Semantic)
		if !bounded || !offering.AgentParameterAllowed(parameter) {
			continue
		}
		maximum, declared := limit.Maximum(budget)
		if !declared {
			continue
		}
		baseline, fromDefault := overrides[parameter.Name], false
		if baseline == nil {
			baseline = command.Tool.DefaultParameters[parameter.Name]
		}
		if baseline == nil {
			baseline, fromDefault = parameter.Default, true
		}
		converged := convergedValue(parameter, baseline, combination)
		if value, fits := limit.Fit(offering, parameter, converged, values, maximum); fits {
			result[parameter.Name] = value
		} else if !fromDefault && !sameJSON(converged, baseline) {
			result[parameter.Name] = converged
		}
	}
	return result
}

// convergedValue moves a number into the range the parameter's maximum_when
// entries give combination; any other value is returned as it is.
func convergedValue(parameter generation.Parameter, value json.RawMessage, combination map[string]any) json.RawMessage {
	var number float64
	if parameter.Type != "number" || len(parameter.MaximumWhen) == 0 || json.Unmarshal(value, &number) != nil {
		return value
	}
	clamped := parameter.ClampNumber(number, combination)
	if clamped == number {
		return value
	}
	encoded, err := json.Marshal(clamped)
	if err != nil {
		return value
	}
	return encoded
}

// decodedValues decodes a step's values for condition matching.
func decodedValues(values map[string]json.RawMessage) map[string]any {
	decoded := make(map[string]any, len(values))
	for name, raw := range values {
		var value any
		if json.Unmarshal(raw, &value) == nil {
			decoded[name] = value
		}
	}
	return decoded
}

// sameJSON reports whether two encodings hold the same value.
func sameJSON(a, b json.RawMessage) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

// applyPurposeValues gives a processing step's unset parameters the values of
// the session purpose. A plan step cannot carry the reason an advised change
// needs, so skipAdvised leaves those at their default; the plan's note then
// reports one that is over the budget.
func (executor *Executor) applyPurposeValues(command chat.ToolCommand, offering generation.Offering,
	input map[string]json.RawMessage, skipAdvised bool) {
	_, values := executor.purposeParameterValues(command, offering, input)
	for name, value := range values {
		if _, set := input[name]; set || (skipAdvised && advisedParameter(offering, name)) {
			continue
		}
		input[name] = value
	}
}

// refreshPurposeValues chooses the purpose's values of a generation step again
// once every layer has set its values (#139). first are the values the
// purpose layer put into input; one a later layer replaced is left alone.
func (executor *Executor) refreshPurposeValues(command chat.ToolCommand, offering generation.Offering,
	input map[string]any, first map[string]json.RawMessage, chosen map[string]bool) error {
	current, err := rawInput(input)
	if err != nil {
		return err
	}
	_, final := executor.purposeParameterValues(command, offering, current)
	for name, value := range first {
		if now, present := current[name]; !present || !sameJSON(now, value) {
			continue
		}
		refreshed, kept := final[name]
		if !kept {
			delete(input, name)
			delete(chosen, name)
			continue
		}
		var decoded any
		if err := json.Unmarshal(refreshed, &decoded); err != nil {
			return err
		}
		input[name] = decoded
	}
	return nil
}

// rawInput encodes a step input built as Go values, for applyPurposeValues.
func rawInput(input map[string]any) (map[string]json.RawMessage, error) {
	raw := make(map[string]json.RawMessage, len(input))
	for name, value := range input {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		raw[name] = encoded
	}
	return raw, nil
}

// PlanStepPurposeBudgetNote checks a production plan step against the session
// purpose's budget before the plan is approved (#129). A plan step names no
// parameters, so the values are the ones the step would run with when nobody
// sets one: the Offering's defaults and fixed values, with the purpose's own
// values and budget fits. Fixed values and defaults the budget cannot bound
// are what it reports; the note is empty when they fit or the session has no
// purpose.
func (executor *Executor) PlanStepPurposeBudgetNote(command chat.ToolCommand, offering generation.Offering) string {
	_, values := executor.purposeParameterValues(command, offering, nil)
	if values == nil {
		values = map[string]json.RawMessage{}
	}
	input, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return executor.purposeBudgetText(command, executor.purposeBudgetItems(command, offering, input))
}

// purposeBudgetNote is the Schema's note naming a step's values over the
// session purpose's budget, for the tool's result key. Empty when the tool
// declares no key, the session has no purpose or every bounded value fits.
func (executor *Executor) purposeBudgetNote(command chat.ToolCommand, offering generation.Offering, input json.RawMessage) string {
	if command.Tool.ResultFields.PurposeBudgetNote == "" {
		return ""
	}
	return executor.purposeBudgetText(command, executor.purposeBudgetItems(command, offering, input))
}

// purposeBudgetItems words each bounded value of a step input that is over the
// session purpose's budget. A fixed parameter is read at its fixed value; an
// absent one at its default.
func (executor *Executor) purposeBudgetItems(command chat.ToolCommand, offering generation.Offering, input json.RawMessage) []string {
	usage := executor.chat.Model3DUsage
	if len(usage.BudgetLimits) == 0 || toolDomain(command) == string(generation.DomainImage) {
		// The triangle and texture budget belongs to 3D purposes (#239).
		return nil
	}
	profile, found := executor.briefPurpose(command)
	if !found {
		return nil
	}
	budget, _ := modelReviewUsageBudget(usage, profile, command.Options.ProductionBrief)
	values := map[string]json.RawMessage{}
	if len(input) > 0 && json.Unmarshal(input, &values) != nil {
		return nil
	}
	var items []string
	for _, parameter := range offering.Parameters {
		limit, bounded := usage.BudgetLimit(parameter.Semantic)
		if !bounded {
			continue
		}
		maximum, declared := limit.Maximum(budget)
		if !declared {
			continue
		}
		value, present := values[parameter.Name]
		if parameter.Fixed || !present {
			// An absent default is read as normalization sets it (#139).
			value = convergedValue(parameter, parameter.Default, decodedValues(values))
		}
		if len(value) == 0 || string(value) == "null" {
			continue
		}
		amount, measured := limit.Measure(offering, parameter, value, values)
		if !measured || amount <= maximum {
			continue
		}
		items = append(items, strings.NewReplacer(
			"{model}", executor.localized(command, OfferingModelName(offering)),
			"{parameter}", executor.localized(command, parameter.Label),
			"{value}", executor.budgetValueLabel(command, parameter, value),
			"{amount}", budgetNumber(amount),
			"{maximum}", budgetNumber(maximum),
			"{purpose}", executor.localized(command, profile.Label),
		).Replace(executor.localized(command, limit.ItemTemplate)))
	}
	return items
}

// purposeBudgetText wraps the items in the Schema's note.
func (executor *Executor) purposeBudgetText(command chat.ToolCommand, items []string) string {
	if len(items) == 0 {
		return ""
	}
	note := executor.chat.Model3DUsage.BudgetNote
	label := ""
	if profile, found := executor.purposeOf(command); found {
		label = executor.localized(command, profile.Label)
	}
	return strings.NewReplacer(
		"{items}", strings.Join(items, executor.localized(command, note.Separator)),
		"{purpose}", label,
	).Replace(executor.localized(command, note.Template))
}

// budgetValueLabel shows a value as the person sees it: a select option by
// its label, a number as written.
func (executor *Executor) budgetValueLabel(command chat.ToolCommand, parameter generation.Parameter, value json.RawMessage) string {
	var text string
	if json.Unmarshal(value, &text) == nil {
		for _, option := range parameter.Options {
			if option.Value == text && option.Label != "" {
				return executor.localized(command, option.Label)
			}
		}
		return text
	}
	var number float64
	if json.Unmarshal(value, &number) == nil {
		return budgetNumber(number)
	}
	return string(value)
}

func budgetNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
