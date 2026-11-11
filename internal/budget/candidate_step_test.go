package budget_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/budget"
)

// #242: candidate identity is stored with the step and older plan JSON stays
// readable (both fields are absent there).
func TestPlanStepCandidateIdentityRoundTripsAndLegacyJSONStaysReadable(t *testing.T) {
	step := budget.PlanStep{Capability: "image-to-3d", Purpose: "candidate",
		Estimate: budget.Legacy(7), CandidateGroup: 2, CandidateIndex: 3}
	encoded, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	var decoded budget.PlanStep
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.CandidateGroup != 2 || decoded.CandidateIndex != 3 || decoded.Estimate != budget.Legacy(7) {
		t.Fatalf("round trip = %+v", decoded)
	}
	var legacy budget.PlanStep
	if err := json.Unmarshal([]byte(`{"capability":"image-to-3d","estimated_credits":5}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.CandidateGroup != 0 || legacy.CandidateIndex != 0 || legacy.Estimate != budget.Legacy(5) {
		t.Fatalf("legacy = %+v", legacy)
	}
}
