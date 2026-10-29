package app

import (
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #150: the approval policy the shipped Product Schema declares. In manual
// mode only the listed capabilities need a person; Remesh, Rigging and
// Animation are approved by policy in both modes, so the Agent must never
// tell a person to switch a mode to "get a confirmation" for them.
func TestDefaultSchemaManualModeAutoApprovesRemeshRiggingAnimation(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	policy := approvalPolicyFromSchema(product.Application())
	if policy.DefaultMode != approvalpolicy.ModeManual {
		t.Fatalf("default approval mode = %q, want manual", policy.DefaultMode)
	}
	for _, capability := range []string{"remesh", "rigging", "animation"} {
		if !slices.Contains(policy.KnownCapabilities, capability) {
			t.Fatalf("capability %q is not declared by the Schema", capability)
		}
		for _, mode := range []string{approvalpolicy.ModeAuto, approvalpolicy.ModeManual} {
			if approvalpolicy.RequiresHuman(policy, mode, capability) {
				t.Errorf("%s needs a person in %s mode", capability, mode)
			}
		}
	}
	for _, capability := range []string{"text-to-3d", "image-to-3d", "image-to-splat", "retexture"} {
		if !approvalpolicy.RequiresHuman(policy, approvalpolicy.ModeManual, capability) {
			t.Errorf("%s is auto-approved in manual mode", capability)
		}
		if approvalpolicy.RequiresHuman(policy, approvalpolicy.ModeAuto, capability) {
			t.Errorf("%s needs a person in auto mode", capability)
		}
	}
}
