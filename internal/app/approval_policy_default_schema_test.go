package app

import (
	"encoding/json"
	"slices"
	"strings"
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

// Tripo Rigging and decimation declare fixed additional export formats with extra
// credits, so manual mode still asks a person for those steps even though the
// capability itself is approved by policy. The Agent text states that exception.
func TestDefaultSchemaTripoRiggingExtraCreditsNeedAPersonInManualMode(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	policy := approvalPolicyFromSchema(product.Application())
	// The default input carries fixed additional exports: Normalize refuses it until the extra spend is
	// consented to, which is the extra-credit exception the Agent text names.
	_, _, err = product.Normalize("rigging", "tripo/processing/rigging", json.RawMessage(`{"source_task":"task-1"}`))
	if err == nil || !strings.Contains(err.Error(), "附加转换费用") {
		t.Fatalf("tripo/processing/rigging does not declare fixed extra credits: %v", err)
	}
	const extra = 1
	if approvalpolicy.RequiresHuman(policy, approvalpolicy.ModeManual, "rigging") {
		t.Fatal("rigging itself needs a person")
	}
	if !approvalpolicy.RequiresHumanForExtraCredits(policy, approvalpolicy.ModeManual, extra) {
		t.Fatal("manual mode approves Tripo rigging extra credits without a person")
	}
	if approvalpolicy.RequiresHumanForExtraCredits(policy, approvalpolicy.ModeAuto, extra) {
		t.Fatal("auto mode asks a person for extra credits")
	}
}
