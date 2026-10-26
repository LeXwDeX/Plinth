package approvalpolicy

import "testing"

func testPolicy() Policy {
	return Policy{
		DefaultMode:                ModeManual,
		Modes:                      []string{ModeAuto, ModeManual},
		ManualApprovalCapabilities: []string{"text-to-3d", "image-to-3d", "retexture"},
		KnownCapabilities: []string{
			"text-to-image", "image-to-image", "background-removal", "text-to-3d",
			"image-to-3d", "retexture", "remesh", "rigging", "animation", "format-conversion",
		},
	}
}

func TestRequiresHumanMatrix(t *testing.T) {
	policy := testPolicy()
	manualHuman := map[string]bool{"text-to-3d": true, "image-to-3d": true, "retexture": true}
	for _, capability := range policy.KnownCapabilities {
		if got := RequiresHuman(policy, ModeAuto, capability); got {
			t.Errorf("auto %s requires human", capability)
		}
		if got := RequiresHuman(policy, ModeManual, capability); got != manualHuman[capability] {
			t.Errorf("manual %s = %v, want %v", capability, got, manualHuman[capability])
		}
	}
}

func TestRequiresHumanFailsClosed(t *testing.T) {
	policy := testPolicy()
	for _, mode := range []string{"", "AUTO", "yolo", " auto"} {
		if !RequiresHuman(policy, mode, "image-to-3d") {
			t.Errorf("unknown mode %q auto-approved a 3D step", mode)
		}
		// Unknown mode behaves as manual, so a 2D step is still automatic.
		if RequiresHuman(policy, mode, "text-to-image") {
			t.Errorf("unknown mode %q blocked a 2D step", mode)
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
