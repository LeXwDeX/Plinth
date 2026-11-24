package agenttools

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #549: ask_choice reads an option's step as one object; a list is read whole,
// so the service can refuse an option that does more than one thing instead of
// silently keeping the first step.
func TestChoiceOptionStepsReadsEveryNamedStep_549(t *testing.T) {
	cases := []struct {
		raw   string
		want  []chat.ChoiceStep
		valid bool
	}{
		{"", nil, true},
		{"null", nil, true},
		{`{"tool":"process_model","capability":"remesh"}`, []chat.ChoiceStep{{Tool: "process_model", Capability: "remesh"}}, true},
		{`[{"tool":"process_model","capability":"remesh"},{"tool":"retexture_model"}]`,
			[]chat.ChoiceStep{{Tool: "process_model", Capability: "remesh"}, {Tool: "retexture_model"}}, true},
		{`"process_model"`, nil, false},
		{`[1]`, nil, false},
	}
	for _, tc := range cases {
		steps, valid := choiceOptionSteps(json.RawMessage(tc.raw))
		if valid != tc.valid || len(steps) != len(tc.want) {
			t.Fatalf("%s = %+v valid=%v", tc.raw, steps, valid)
		}
		for index := range steps {
			if steps[index] != tc.want[index] {
				t.Fatalf("%s step %d = %+v", tc.raw, index, steps[index])
			}
		}
	}
}
