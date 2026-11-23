package app

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #490: the per-step credit threshold declared by application.chat.approval_policy
// reaches the pure policy the runtime decides with. The processing capabilities
// (model-split among them) are not in the manual capability list, so only the
// threshold can card them; a cheap 2D step stays automatic and the 15-credit
// text-to-3d keeps the card the capability list already gave it.
func TestShippedSchemaCardsAStepByItsCredits(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	declared := application.Chat.ApprovalPolicy.ManualCreditsThreshold
	if declared <= 0 {
		t.Fatalf("declared manual_credits_threshold = %d, want a positive default", declared)
	}
	policy := approvalPolicyFromSchema(application)
	if got := policy.ManualCreditsThreshold; got != declared {
		t.Fatalf("assembled threshold = %d, declared = %d", got, declared)
	}
	// The expensive processing step this rule exists for: carded in manual mode
	// although its capability is not listed, and left alone in auto mode.
	if !approvalpolicy.RequiresHumanForCredits(policy, approvalpolicy.ModeManual, "model-split", 50) {
		t.Error("manual mode auto-approved a 50-credit processing step")
	}
	if approvalpolicy.RequiresHumanForCredits(policy, approvalpolicy.ModeAuto, "model-split", 50) {
		t.Error("auto mode carded a processing step")
	}
	// Below the threshold nothing new is carded: a cheap 2D step of an unlisted
	// capability stays automatic.
	if approvalpolicy.RequiresHumanForCredits(policy, approvalpolicy.ModeManual, "image-to-image", 5) {
		t.Error("manual mode carded a cheap 2D step")
	}
	// The capability list is unchanged: the 15-credit text-to-3d keeps its card,
	// and a capability the Schema does not know is still never auto-approved.
	if !approvalpolicy.RequiresHumanForCredits(policy, approvalpolicy.ModeManual, "text-to-3d", 15) {
		t.Error("text-to-3d lost the card the capability list gives it")
	}
	if !approvalpolicy.RequiresHumanForCredits(policy, approvalpolicy.ModeManual, "text-to-video", 1) {
		t.Error("manual mode auto-approved a capability the Schema does not know")
	}
}
