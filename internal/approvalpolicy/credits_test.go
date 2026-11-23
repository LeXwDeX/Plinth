package approvalpolicy

import "testing"

// #490: manual mode cards a step by what it costs, not only by which capability
// it is. The amount is the frozen per-step estimate (base plus extra), the same
// number the approval card shows.
func TestManualModeCardsAStepByItsCredits(t *testing.T) {
	policy := testPolicy().WithCreditsThreshold(20)
	// A capability the manual list does not name: the expensive processing step
	// this rule exists for (rigging 25, remesh 35/40, mesh-completion 50).
	if !RequiresHumanForCredits(policy, ModeManual, "format-conversion", 50) {
		t.Error("a 50-credit step of an unlisted capability was auto-approved")
	}
	if RequiresHumanForCredits(policy, ModeManual, "format-conversion", 19) {
		t.Error("a 19-credit step of an unlisted capability was carded")
	}
	// The threshold is inclusive: the declared number itself is the number the
	// policy names.
	if !RequiresHumanForCredits(policy, ModeManual, "format-conversion", 20) {
		t.Error("a step at exactly the threshold was auto-approved")
	}
	// A capability the list names keeps its card at its own price (the 15-credit
	// text-to-3d).
	if !RequiresHumanForCredits(policy, ModeManual, "text-to-3d", 15) {
		t.Error("a listed capability lost its card")
	}
	// A free step of an unlisted capability is still automatic, and an unknown
	// capability is never auto-approved whatever it costs.
	if RequiresHumanForCredits(policy, ModeManual, "format-conversion", 0) {
		t.Error("a free step of an unlisted capability was carded")
	}
	if !RequiresHumanForCredits(policy, ModeManual, "text-to-video", 1) {
		t.Error("an unknown capability was auto-approved")
	}
}

func TestCreditsThresholdNeverOverridesAutoOrSteps(t *testing.T) {
	// The steps mode only exists when the Catalog declares a step policy; a
	// policy without one treats "steps" as manual, like every unknown mode.
	policy := testPolicy().WithCreditsThreshold(1).
		WithSteps(StepsPolicy{Counted: []string{"image-to-3d"}})
	if RequiresHumanForCredits(policy, ModeAuto, "format-conversion", 500) {
		t.Error("auto mode carded an expensive step")
	}
	if RequiresHumanForCredits(policy, ModeSteps, "format-conversion", 500) {
		t.Error("steps mode carded an expensive step")
	}
	// A mode the Schema does not offer is manual, so the cost rule applies.
	if !RequiresHumanForCredits(policy, "yolo", "format-conversion", 500) {
		t.Error("an unknown mode lost the cost rule")
	}
	policy.Modes = []string{ModeManual}
	if !RequiresHumanForCredits(policy, ModeAuto, "format-conversion", 500) {
		t.Error("auto mode accepted although the Schema does not offer it")
	}
}

func TestCreditsThresholdIsOffWithoutTheOption(t *testing.T) {
	// A policy built without WithCreditsThreshold (every Catalog written before
	// #490, which does not declare the field) decides on the capability list
	// alone, exactly as RequiresHuman does.
	policy := testPolicy()
	if RequiresHumanForCredits(policy, ModeManual, "format-conversion", 5000) {
		t.Error("a policy without a threshold carded a step by its credits")
	}
	for _, capability := range policy.KnownCapabilities {
		if got, want := RequiresHumanForCredits(policy, ModeManual, capability, 5000),
			RequiresHuman(policy, ModeManual, capability); got != want {
			t.Errorf("without a threshold manual %s = %v, want %v", capability, got, want)
		}
	}
	if got := policy.WithCreditsThreshold(-5); got.ManualCreditsThreshold != 0 {
		t.Errorf("a negative threshold = %d, want 0 (disabled)", got.ManualCreditsThreshold)
	}
}
