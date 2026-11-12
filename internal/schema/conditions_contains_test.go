package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// contains holds when a multi-select parameter holds the expected string; it is
// what ties a delivered format's output slot to the frozen format selection.
func TestContainsMatchesAMemberOfAStringArray(t *testing.T) {
	condition := schema.ParameterCondition{Parameter: "output_formats", Operator: "contains", Value: json.RawMessage(`"FBX"`)}
	for name, test := range map[string]struct {
		values map[string]any
		want   bool
	}{
		"a member":                      {map[string]any{"output_formats": []string{"GLTF", "FBX"}}, true},
		"a member of a decoded array":   {map[string]any{"output_formats": []any{"GLTF", "FBX"}}, true},
		"not a member":                  {map[string]any{"output_formats": []string{"GLTF", "STL"}}, false},
		"a case difference":             {map[string]any{"output_formats": []string{"fbx"}}, false},
		"an empty selection":            {map[string]any{"output_formats": []string{}}, false},
		"an absent parameter":           {map[string]any{}, false},
		"a text value is not an array":  {map[string]any{"output_formats": "FBX"}, false},
		"a nested value is not a match": {map[string]any{"output_formats": []any{[]any{"FBX"}}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := schema.ConditionMatches(condition, test.values); got != test.want {
				t.Fatalf("contains = %v, want %v", got, test.want)
			}
		})
	}
}
