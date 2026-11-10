package budget

import "testing"

func TestPlanCoversAnExplicitSeriesAtThePinnedOffering(t *testing.T) {
	plan := Plan{Steps: []PlanStep{
		{Capability: "image-to-image", OfferingID: "image-offering", Estimate: Legacy(4)},
		{Capability: "image-to-image", OfferingID: "image-offering", Estimate: Legacy(4)},
		{Capability: "image-to-3d", OfferingID: "views-offering", Estimate: Legacy(20)},
	}}
	if !plan.Covers("image-to-image", "image-offering", 2) {
		t.Fatal("the planned image series is not covered")
	}
	if plan.Covers("image-to-image", "image-offering", 3) {
		t.Fatal("more steps than the plan reserves were covered")
	}
	if plan.Covers("image-to-image", "another-offering", 1) {
		t.Fatal("a different Offering was covered by a pinned step")
	}
	if plan.Covers("image-to-3d", "views-offering", 1) == false {
		t.Fatal("the 3D step is not covered")
	}
	if plan.Covers("image-to-3d", "views-offering", 2) {
		t.Fatal("one planned 3D step covered two")
	}
	if plan.Covers("", "image-offering", 1) || plan.Covers("image-to-image", "image-offering", 0) {
		t.Fatal("an empty capability or count was covered")
	}
}

func TestPlanCoversUnpinnedStepsForAnyOffering(t *testing.T) {
	plan := Plan{Steps: []PlanStep{{Capability: "image-to-image", Estimate: Legacy(4)}}}
	if !plan.Covers("image-to-image", "any-offering", 1) {
		t.Fatal("an unpinned plan step did not cover the capability")
	}
}
