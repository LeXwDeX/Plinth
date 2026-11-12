package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #294: a source input rule refuses a source Task when its frozen input and the
// request's input meet the rule's conditions together.
func TestSourceInputRuleRefusesOnlyTheCombination(t *testing.T) {
	var rule schema.SourceInputRule
	const declared = `{
		"source_conditions": [{"parameter": "topology", "operator": "equals", "value": "quad"}],
		"request_conditions": [{"parameter": "include_fingers", "operator": "truthy"}],
		"message": "reason"
	}`
	if err := json.Unmarshal([]byte(declared), &rule); err != nil {
		t.Fatal(err)
	}
	if rule.Message != "reason" || len(rule.SourceConditions) != 1 || len(rule.RequestConditions) != 1 {
		t.Fatalf("decoded rule = %+v", rule)
	}
	quad := map[string]any{"topology": "quad", "target_polycount": float64(8000)}
	triangle := map[string]any{"topology": "triangle"}
	for name, test := range map[string]struct {
		source, request map[string]any
		want            bool
	}{
		"both hold":                      {quad, map[string]any{"include_fingers": true}, true},
		"only the source holds":          {quad, map[string]any{"include_fingers": false}, false},
		"the request leaves it out":      {quad, map[string]any{}, false},
		"only the request holds":         {triangle, map[string]any{"include_fingers": true}, false},
		"a source with no topology":      {map[string]any{"prompt": "a robot"}, map[string]any{"include_fingers": true}, false},
		"a source with no input at all":  {nil, map[string]any{"include_fingers": true}, false},
		"a request with no input at all": {quad, nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := rule.Refuses(test.source, test.request); got != test.want {
				t.Fatalf("Refuses = %v, want %v", got, test.want)
			}
		})
	}
}

// The request conditions are optional (the source alone may decide), and a rule
// without source conditions refuses nothing: that would refuse every source.
func TestSourceInputRuleNeedsSourceConditions(t *testing.T) {
	quad := schema.ParameterCondition{Parameter: "topology", Operator: "equals", Value: json.RawMessage(`"quad"`)}
	fingers := schema.ParameterCondition{Parameter: "include_fingers", Operator: "truthy"}

	sourceAlone := schema.SourceInputRule{SourceConditions: []schema.ParameterCondition{quad}, Message: "reason"}
	if !sourceAlone.Refuses(map[string]any{"topology": "quad"}, nil) || sourceAlone.Refuses(map[string]any{"topology": "triangle"}, nil) {
		t.Fatal("a rule with source conditions only must follow the source")
	}
	requestOnly := schema.SourceInputRule{RequestConditions: []schema.ParameterCondition{fingers}, Message: "reason"}
	if requestOnly.Refuses(map[string]any{"topology": "quad"}, map[string]any{"include_fingers": true}) {
		t.Fatal("a rule without source conditions must refuse nothing")
	}
	// Every condition has to hold, not one of them.
	both := schema.SourceInputRule{
		SourceConditions: []schema.ParameterCondition{quad, {Parameter: "should_remesh", Operator: "truthy"}},
		Message:          "reason",
	}
	if both.Refuses(map[string]any{"topology": "quad"}, nil) || !both.Refuses(map[string]any{"topology": "quad", "should_remesh": true}, nil) {
		t.Fatal("a rule must need all of its source conditions")
	}
}
