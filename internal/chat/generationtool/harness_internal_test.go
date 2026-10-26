package generationtool

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
)

func TestHarnessForcedValueCannotOverrideHumanFixedParameter(t *testing.T) {
	selection := harness.DefaultSelection("owner", "session")
	selection.FixedParameters = map[string]json.RawMessage{"quality": json.RawMessage(`"high"`)}
	command := chat.ToolCommand{OwnerID: "owner", SessionID: "session", Options: chat.TurnOptions{Harness: &resolve.Snapshot{Selection: selection}}}
	offering := generation.Offering{Parameters: []generation.Parameter{{Name: "trigger", Default: json.RawMessage(`true`)}, {Name: "quality", ForcedWhen: []generation.ForcedValue{{When: generation.ParameterCondition{Parameter: "trigger", Operator: "truthy"}, Value: json.RawMessage(`"low"`)}}}}}
	if forcedHarnessInputMatches(command, offering, json.RawMessage(`{"quality":"high"}`)) {
		t.Fatal("forced value silently changed human fixed parameter")
	}
	offering.Parameters[1].ForcedWhen[0].Value = json.RawMessage(`"high"`)
	if !forcedHarnessInputMatches(command, offering, json.RawMessage(`{"quality":"high"}`)) {
		t.Fatal("same fixed forced value rejected")
	}
}
