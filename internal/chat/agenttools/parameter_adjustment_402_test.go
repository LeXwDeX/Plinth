package agenttools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #402: a set_params call that carries a reason for a parameter that needs
// none succeeds at once, and the reason is not remembered. The generation tool
// reads the remembered reasons back when it freezes the input, and
// generation.freezeParameterAdjustments refuses a reason that no advised
// parameter takes, so storing it would only move the failure from set_params
// to the generation.
func TestSetParamsIgnoresAReasonForAParameterThatNeedsNone_402(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	const offeringID = "openai/gpt-image-2/text-to-image"
	params := agentparams.NewPersistentParameterStore(nil, nil)
	generate := &fakeGenerate{}
	e, err := New(Config{Params: params, Generate: generate, Offerings: func() []generation.Offering { return product.List("") }})
	if err != nil {
		t.Fatal(err)
	}
	execute := func(arguments string) chat.ToolResult {
		t.Helper()
		result, err := e.Execute(context.Background(), chat.ToolCommand{
			OwnerID: testOwner, SessionID: "s1",
			Call: agents.ToolCall{Name: ToolSetParams, Arguments: json.RawMessage(arguments)},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	// background is a normal setting: the Issue's own example.
	result := execute(`{"offering_id":"` + offeringID + `","params":{"background":"opaque"},"reasons":{"background":"白底更干净"}}`)
	if result.Error != "" || result.AgentContent != "parameters updated" {
		t.Fatalf("an extra reason failed the call: %+v", result)
	}
	state := params.OfferingStates(testScope, offeringID)["background"]
	if state.Source != "agent" || string(state.Value) != `"opaque"` || state.Reason != "" {
		t.Fatalf("background state = %+v: the value is kept, the extra reason is not", state)
	}

	// An advised parameter keeps its reason next to a normal one that has none
	// to keep.
	result = execute(`{"offering_id":"` + offeringID + `","params":{"background":"transparent","quality":"low"},"reasons":{"background":"透明底","quality":"draft"}}`)
	if result.Error != "" {
		t.Fatalf("a mixed call failed: %+v", result)
	}
	states := params.OfferingStates(testScope, offeringID)
	if states["quality"].Reason != "draft" || states["background"].Reason != "" || string(states["background"].Value) != `"transparent"` {
		t.Fatalf("states = %+v", states)
	}

	// What the generation tool is handed: the same reading the application
	// wires into generationtool (agent-set values that carry a reason). No
	// entry may name a parameter that is not advised.
	for name, state := range states {
		if state.Source != "agent" || state.Reason == "" {
			continue
		}
		if !offeringAdvises(product, offeringID, name) {
			t.Fatalf("a reason for the unadvised parameter %q reaches the frozen input", name)
		}
	}

	// A reason that still has to be refused fails as it did, and stores nothing.
	before := params.OfferingStates(testScope, offeringID)["background"]
	result = execute(`{"offering_id":"` + offeringID + `","params":{"background":"auto"},"reasons":{"invented":"没有这个参数"}}`)
	if result.Error == "" || !result.NoSubmission {
		t.Fatalf("a reason for an undeclared parameter was accepted: %+v", result)
	}
	if after := params.OfferingStates(testScope, offeringID)["background"]; string(after.Value) != string(before.Value) {
		t.Fatalf("a refused call changed background: %+v -> %+v", before, after)
	}
	if generate.callCount != 0 {
		t.Fatalf("set_params reached generation %d times", generate.callCount)
	}
}

func offeringAdvises(product *catalog.Catalog, offeringID, parameter string) bool {
	offering, ok := product.Get(generation.OfferingID(offeringID))
	return ok && offering.AdvisedParameter(parameter)
}
