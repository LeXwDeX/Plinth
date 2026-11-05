package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func conditionalFaceLimit() schema.Parameter {
	minimum, maximum := 1000.0, 2000000.0
	quad := schema.ParameterCondition{Parameter: "quad", Operator: "truthy"}
	smart := schema.ParameterCondition{Parameter: "smart_low_poly", Operator: "truthy"}
	return schema.Parameter{Name: "face_limit", Type: "number", Minimum: &minimum, Maximum: &maximum,
		Default: json.RawMessage(`2000000`), MaximumWhen: []schema.ConditionalMaximum{
			{When: smart, Maximum: 20000},
			{When: smart, AlsoWhen: []schema.ParameterCondition{quad}, Maximum: 10000},
		}}
}

// #139: an also_when entry applies only when every condition matches.
func TestNumberRangeNeedsEveryConditionOfAnEntry(t *testing.T) {
	parameter := conditionalFaceLimit()
	for _, test := range []struct {
		values map[string]any
		want   float64
	}{
		{map[string]any{}, 2000000},
		{map[string]any{"quad": true}, 2000000},
		{map[string]any{"smart_low_poly": true}, 20000},
		{map[string]any{"smart_low_poly": true, "quad": true}, 10000},
		{map[string]any{"smart_low_poly": false, "quad": true}, 2000000},
	} {
		minimum, maximum := parameter.NumberRange(test.values)
		if minimum == nil || *minimum != 1000 || maximum == nil || *maximum != test.want {
			t.Fatalf("%v: range = %v–%v, want 1000–%v", test.values, minimum, maximum, test.want)
		}
	}
}

// Only a value the Schema supplied moves; a given value is left for the
// validation to refuse.
func TestConvergeDefaultsMovesOnlyDefaultedValues(t *testing.T) {
	parameters := []schema.Parameter{conditionalFaceLimit()}
	values := map[string]any{"face_limit": json.Number("2000000"), "smart_low_poly": true, "quad": true}
	if !schema.ConvergeDefaults(parameters, values, func(string) bool { return true }) || values["face_limit"] != float64(10000) {
		t.Fatalf("defaulted face_limit = %v, want 10000", values["face_limit"])
	}
	given := map[string]any{"face_limit": 2000000.0, "smart_low_poly": true}
	if schema.ConvergeDefaults(parameters, given, func(string) bool { return false }) || given["face_limit"] != 2000000.0 {
		t.Fatalf("a given face_limit was rewritten: %v", given["face_limit"])
	}
}

func TestParameterRangeRefusalNamesTheRange(t *testing.T) {
	messages := schema.ExecutionMessages{InputParameterRange: "{label}={value} [{minimum},{maximum}]"}
	refusal := messages.InputRefusal(schema.InputRefusalParameterRange, schema.InputRefusalValues{
		Label: "面数上限", Value: "2000000", RangeMinimum: "1000", RangeMaximum: "20000"})
	if refusal == nil || !refusal.UserFacing || refusal.Detail != "面数上限=2000000 [1000,20000]" || refusal.Code != schema.ErrInvalidInput {
		t.Fatalf("refusal = %#v", refusal)
	}
	if messages.InputRefusal(schema.InputRefusalParameterRange, schema.InputRefusalValues{Label: "面数上限"}) != nil {
		t.Fatal("a refusal without its values was produced")
	}
}
