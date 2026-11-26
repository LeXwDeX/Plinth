package processing

import (
	"encoding/json"
	"testing"
)

// #665 third review: a step stored before agent_retries existed counts all its
// retries as the Agent's (fail closed for the Agent; the person can still retry
// from the panel). A stored count, zero included, is read as it is, and every
// step is written with the count so the two are told apart.
func TestLegacyStepWithoutAgentRetriesCountsItsRetriesAsTheAgents665(t *testing.T) {
	var legacy []Step
	if err := json.Unmarshal([]byte(`[{"capability":"remesh","offering_id":"remesh","input":{},"state":"FAILED","retry":2}]`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy[0].AgentRetries != 2 || legacy[0].Retry != 2 || legacy[0].Capability != "remesh" {
		t.Fatalf("legacy step = %+v, want its two retries counted as the Agent's", legacy[0])
	}
	var current []Step
	if err := json.Unmarshal([]byte(`[{"capability":"remesh","state":"FAILED","retry":2,"agent_retries":0}]`), &current); err != nil {
		t.Fatal(err)
	}
	if current[0].AgentRetries != 0 {
		t.Fatalf("stored zero agent retries read as %d", current[0].AgentRetries)
	}
	encoded, err := json.Marshal([]Step{{Capability: "remesh", Retry: 2}})
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip []Step
	if err := json.Unmarshal(encoded, &roundTrip); err != nil || roundTrip[0].AgentRetries != 0 {
		t.Fatalf("a step with person-only retries came back with %+v (%s) err=%v", roundTrip, encoded, err)
	}
}
