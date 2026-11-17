package approvalpolicy

import "testing"

func stepsPolicy() Policy {
	return testPolicy().WithSteps(StepsPolicy{
		Counted:     []string{"text-to-3d", "image-to-3d", "retexture"},
		DefaultList: StepSetting{Mode: StepModeAgent, Cap: 20},
		DefaultMCP:  StepSetting{Mode: StepModeCap, Cap: 5},
	})
}

// The steps mode exists only when application.step_policy is declared: without
// it the word is any unknown mode, and an unknown mode is the stricter manual.
func TestStepsModeExistsOnlyWhenTheStepPolicyIsDeclared(t *testing.T) {
	declared, absent := stepsPolicy(), testPolicy()
	if got := declared.NormalizeMode(ModeSteps); got != ModeSteps {
		t.Fatalf("declared: NormalizeMode(steps) = %q", got)
	}
	if got := absent.NormalizeMode(ModeSteps); got != ModeManual {
		t.Fatalf("absent: NormalizeMode(steps) = %q, want manual", got)
	}
	if !RequiresHuman(absent, ModeSteps, "image-to-3d") {
		t.Fatal("without a step policy the steps word approved a 3D step")
	}
	if absent.StepsEnabled() || !declared.StepsEnabled() {
		t.Fatalf("StepsEnabled = %v/%v", absent.StepsEnabled(), declared.StepsEnabled())
	}
}

// Steps is not a mode a person can pick: it is never offered, so the session
// approval-mode API (which asks Allows) refuses it even when it is enabled.
func TestStepsModeIsNeverOffered(t *testing.T) {
	policy := stepsPolicy()
	if policy.Allows(ModeSteps) {
		t.Fatal("the steps mode is offered to people")
	}
	for _, mode := range policy.Modes {
		if mode == ModeSteps {
			t.Fatalf("modes = %v", policy.Modes)
		}
	}
	// The three Schema modes behave as before.
	if policy.NormalizeMode(ModeAuto) != ModeAuto || policy.NormalizeMode(ModeManual) != ModeManual ||
		policy.NormalizeMode("yolo") != ModeManual {
		t.Fatal("enabling the steps mode changed another mode")
	}
}

// Nothing waits for a person under the steps mode: the 3D cap is a refusal, not
// a card, and money is not the constraint.
func TestStepsModeNeverRequiresAPerson(t *testing.T) {
	policy := stepsPolicy()
	for _, capability := range append(append([]string{}, policy.KnownCapabilities...), "", "unknown-capability") {
		if RequiresHuman(policy, ModeSteps, capability) {
			t.Errorf("steps requires a person for %q", capability)
		}
	}
	if RequiresHumanForExtraCredits(policy, ModeSteps, 40) || RequiresHumanForExtraSpend(policy, ModeSteps, true) {
		t.Error("steps asked a person to approve extra credits")
	}
	// Manual and unknown modes keep asking.
	if !RequiresHumanForExtraCredits(policy, ModeManual, 40) || !RequiresHuman(policy, ModeManual, "image-to-3d") {
		t.Error("enabling the steps mode relaxed manual")
	}
}

func TestStepsPolicyCountsOnlyItsCapabilitiesAndKnowsItsEntries(t *testing.T) {
	steps := *stepsPolicy().Steps
	for capability, want := range map[string]bool{
		"text-to-3d": true, "image-to-3d": true, "retexture": true,
		"text-to-image": false, "image-to-image": false, "": false, "unknown": false,
	} {
		if got := steps.Counts(capability); got != want {
			t.Errorf("Counts(%q) = %v", capability, got)
		}
	}
	if list, ok := steps.Default(StepEntryList); !ok || list.Mode != StepModeAgent || list.Cap != 20 {
		t.Fatalf("list default = %+v %v", list, ok)
	}
	if mcp, ok := steps.Default(StepEntryMCP); !ok || mcp.Mode != StepModeCap || mcp.Cap != 5 {
		t.Fatalf("mcp default = %+v %v", mcp, ok)
	}
	if _, ok := steps.Default("web"); ok {
		t.Fatal("an unknown entry has a default")
	}
	if !ValidStepEntry(StepEntryList) || !ValidStepEntry(StepEntryMCP) || ValidStepEntry("web") || ValidStepEntry("") {
		t.Fatal("ValidStepEntry")
	}
}

func TestWithStepsDoesNotShareTheCountedList(t *testing.T) {
	counted := []string{"text-to-3d"}
	policy := testPolicy().WithSteps(StepsPolicy{Counted: counted})
	counted[0] = "mutated"
	if policy.Steps.Counted[0] != "text-to-3d" {
		t.Fatal("WithSteps kept the caller's slice")
	}
	if testPolicy().StepsEnabled() {
		t.Fatal("WithSteps changed the receiver")
	}
}
