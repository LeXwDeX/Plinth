package budget_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/budget"
)

// planPinNames maps the model names a plan stored before #426 to the Offering
// IDs the current Catalog declares for the step's capability.
var planPinNames = map[string]string{
	"image-to-3d/meshy-7.1-multiview": "meshy/meshy-7.1-multiview/image-to-3d",
	"text-to-3d/meshy-7.1":            "meshy/meshy-7.1/text-to-3d",
	"retexture/meshy-7.1":             "meshy/meshy-7.1/retexture",
}

func planPinResolver(capability, pinned string) (string, bool) {
	offeringID, found := planPinNames[capability+"/"+pinned]
	return offeringID, found
}

// #434: ResolvePins rewrites the offering pins of every step and contingency
// step the budget reads, leaves a pin it cannot resolve as stored (so the step
// stays off-plan), and never changes the plan it was given: the stored plan_json
// is not rewritten.
func TestPlanResolvePinsResolvesStepsAndContingenciesWithoutChangingTheStoredPlan(t *testing.T) {
	plan := budget.Plan{
		Title: "骑士",
		Steps: []budget.PlanStep{
			{Capability: "image-to-3d", OfferingID: "meshy-7.1-multiview", Purpose: "三视图主体",
				Estimate: budget.Legacy(40)},
			{Capability: "text-to-3d", OfferingID: "meshy-7.1", Purpose: "主体", Estimate: budget.Legacy(20),
				Check: "self_check", CandidateGroup: 2, CandidateIndex: 1},
			{Capability: "text-to-image", OfferingID: "never-declared", Purpose: "参考图",
				Estimate: budget.Legacy(1)},
			{Capability: "retexture", Purpose: "换贴图", Estimate: budget.Legacy(5)},
		},
		CorrectionCapabilities: []string{"retexture"},
		Contingencies: []budget.PlanContingency{
			{ID: "repair", Trigger: []string{"self_check"}, Step: budget.PlanStep{
				Capability: "retexture", OfferingID: "meshy-7.1", Purpose: "修补", Estimate: budget.Legacy(10)}},
		},
		Acceptance:  []budget.AcceptanceCriterion{{ID: "look", Statement: "外观符合", Check: "self_check"}},
		Checkpoints: []budget.PlanCheckpoint{{AfterCapability: "text-to-3d", Checks: []budget.PlanCheck{{Check: "watertight"}}}},
	}

	resolved := plan.ResolvePins(planPinResolver)

	for index, want := range map[int]string{
		0: "meshy/meshy-7.1-multiview/image-to-3d",
		1: "meshy/meshy-7.1/text-to-3d",
		2: "never-declared",
		3: "",
	} {
		if resolved.Steps[index].OfferingID != want {
			t.Fatalf("step %d pin = %q, want %q", index, resolved.Steps[index].OfferingID, want)
		}
	}
	if resolved.Contingencies[0].Step.OfferingID != "meshy/meshy-7.1/retexture" {
		t.Fatalf("contingency pin = %q", resolved.Contingencies[0].Step.OfferingID)
	}
	// Everything that is not a pin is carried over unchanged.
	if resolved.Steps[1].Purpose != "主体" || resolved.Steps[1].Estimate != budget.Legacy(20) ||
		resolved.Steps[1].Check != "self_check" || resolved.Steps[1].CandidateGroup != 2 ||
		resolved.Steps[1].CandidateIndex != 1 || len(resolved.CorrectionCapabilities) != 1 ||
		resolved.Acceptance[0].Statement != "外观符合" ||
		resolved.Checkpoints[0].AfterCapability != "text-to-3d" ||
		resolved.Contingencies[0].ID != "repair" || len(resolved.Contingencies[0].Trigger) != 1 {
		t.Fatalf("resolved plan lost a field: %+v", resolved)
	}
	// The stored plan keeps its bytes: only the returned copy was resolved.
	if plan.Steps[0].OfferingID != "meshy-7.1-multiview" || plan.Steps[1].OfferingID != "meshy-7.1" ||
		plan.Contingencies[0].Step.OfferingID != "meshy-7.1" {
		t.Fatalf("ResolvePins changed the receiver: %+v", plan)
	}
	// A nil resolver returns the plan as stored.
	if untouched := plan.ResolvePins(nil); untouched.Steps[0].OfferingID != "meshy-7.1-multiview" {
		t.Fatalf("nil resolver changed the plan: %+v", untouched)
	}
}

// A resolved pin makes Allows and Covers compare it the way a grant's Offering
// ID is compared; an unresolved name keeps failing both, as an off-plan step.
func TestPlanResolvePinsMakesAllowsAndCoversTrueForTheSameOffering(t *testing.T) {
	plan := budget.Plan{Title: "三视图", Steps: []budget.PlanStep{
		{Capability: "image-to-3d", OfferingID: "meshy-7.1-multiview", Estimate: budget.Legacy(40)},
		{Capability: "text-to-3d", OfferingID: "never-declared", Estimate: budget.Legacy(20)},
	}}
	resolved := plan.ResolvePins(planPinResolver)
	if !resolved.Allows("image-to-3d", "meshy/meshy-7.1-multiview/image-to-3d") ||
		!resolved.Covers("image-to-3d", "meshy/meshy-7.1-multiview/image-to-3d", 1) {
		t.Fatalf("resolved plan does not cover its own Offering: %+v", resolved)
	}
	if plan.Allows("image-to-3d", "meshy/meshy-7.1-multiview/image-to-3d") {
		t.Fatal("the stored model name must not match the Offering ID without resolution")
	}
	for _, offeringID := range []string{"meshy/meshy-7.1/text-to-3d", "meshy-7.1"} {
		if resolved.Allows("text-to-3d", offeringID) || resolved.Covers("text-to-3d", offeringID, 1) {
			t.Fatalf("unresolved pin %q covers %q", "never-declared", offeringID)
		}
	}
}
