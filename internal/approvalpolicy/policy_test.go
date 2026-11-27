package approvalpolicy

import "testing"

func testPolicy() Policy {
	return Policy{
		DefaultMode:                ModeManual,
		Modes:                      []string{ModeAuto, ModeBudget, ModeManual},
		ManualApprovalCapabilities: []string{"text-to-3d", "image-to-3d", "retexture"},
		KnownCapabilities: []string{
			"text-to-image", "image-to-image", "background-removal", "text-to-3d",
			"image-to-3d", "retexture", "remesh", "rigging", "animation", "format-conversion",
		},
	}
}

func TestRequiresHumanMatrix(t *testing.T) {
	policy := testPolicy()
	budgetHuman := map[string]bool{"text-to-3d": true, "image-to-3d": true, "retexture": true}
	for _, capability := range policy.KnownCapabilities {
		if got := RequiresHuman(policy, ModeAuto, capability); got {
			t.Errorf("auto %s requires human", capability)
		}
		// #702: strict asks for every step, 2D included.
		if !RequiresHuman(policy, ModeManual, capability) {
			t.Errorf("manual %s did not require a human", capability)
		}
		if got := RequiresHuman(policy, ModeBudget, capability); got != budgetHuman[capability] {
			t.Errorf("budget %s = %v, want %v", capability, got, budgetHuman[capability])
		}
	}
}

func TestRequiresHumanFailsClosed(t *testing.T) {
	policy := testPolicy()
	for _, mode := range []string{"", "AUTO", "yolo", " auto"} {
		if !RequiresHuman(policy, mode, "image-to-3d") {
			t.Errorf("unknown mode %q auto-approved a 3D step", mode)
		}
		// Unknown mode behaves as manual, which asks for a 2D step too (#702).
		if !RequiresHuman(policy, mode, "text-to-image") {
			t.Errorf("unknown mode %q auto-approved a 2D step", mode)
		}
	}
	for _, capability := range []string{"", "unknown", "text-to-video"} {
		if !RequiresHuman(policy, ModeManual, capability) {
			t.Errorf("manual mode auto-approved unknown capability %q", capability)
		}
	}
}

func TestAutoModeIsOnlyHonoredWhenSchemaOffersIt(t *testing.T) {
	policy := testPolicy()
	policy.Modes = []string{ModeManual}
	if !RequiresHuman(policy, ModeAuto, "image-to-3d") {
		t.Fatal("auto mode accepted although the Schema does not offer it")
	}
	if got := policy.NormalizeMode(ModeAuto); got != ModeManual {
		t.Fatalf("NormalizeMode(auto) = %q", got)
	}
}

func TestRequiresHumanForExtraCredits(t *testing.T) {
	policy := testPolicy()
	for _, mode := range []string{ModeManual, "", "yolo", " auto"} {
		if !RequiresHumanForExtraCredits(policy, mode, 5) {
			t.Errorf("mode %q approved extra credits without a person", mode)
		}
		if RequiresHumanForExtraCredits(policy, mode, 0) {
			t.Errorf("mode %q asked for a person although nothing extra is spent", mode)
		}
	}
	if RequiresHumanForExtraCredits(policy, ModeAuto, 30) {
		t.Error("auto mode asked a person to approve extra credits")
	}
	policy.Modes = []string{ModeManual}
	if !RequiresHumanForExtraCredits(policy, ModeAuto, 5) {
		t.Error("auto accepted although the Schema does not offer it")
	}
}

// #702: auto and budget ask a person once whenever the session's approved
// steps reach a multiple of the mode's window; strict and steps never do.
func TestCheckpointDue(t *testing.T) {
	policy := testPolicy().WithCheckpoints(map[string]int{ModeAuto: 100, ModeBudget: 100})
	for _, mode := range []string{ModeAuto, ModeBudget} {
		for approved, want := range map[int]bool{0: false, 1: false, 99: false, 100: true, 101: false, 199: false, 200: true} {
			if got := policy.CheckpointDue(mode, approved); got != want {
				t.Errorf("%s CheckpointDue(%d) = %v, want %v", mode, approved, got, want)
			}
		}
	}
	for _, mode := range []string{ModeManual, ModeSteps, "yolo"} {
		if policy.CheckpointDue(mode, 100) {
			t.Errorf("%s reached a checkpoint", mode)
		}
	}
	if testPolicy().CheckpointDue(ModeAuto, 100) {
		t.Error("a policy without windows reached a checkpoint")
	}
}

// #702: strict spares the person's own click on a known 2D step only; the
// card list, a counted 3D step and an unknown capability still ask.
func TestStrictSparesPersonClick(t *testing.T) {
	policy := testPolicy().WithSteps(StepsPolicy{Counted: []string{"text-to-3d", "image-to-3d", "retexture", "remesh"}})
	for capability, want := range map[string]bool{
		"text-to-image": true, "image-to-image": true, "background-removal": true,
		"text-to-3d": false, "retexture": false, "remesh": false, "mystery": false, "": false,
	} {
		if got := policy.StrictSparesPersonClick(capability); got != want {
			t.Fatalf("StrictSparesPersonClick(%q) = %v, want %v", capability, got, want)
		}
	}
}
