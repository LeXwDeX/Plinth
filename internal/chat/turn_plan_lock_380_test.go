package chat

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #380 step 2: the model lock is one rule shared by the plan and the executor.
// Before ApplicableModelLock only the plan filtered the lock, so a plan quoted a
// step whose lock the executor then refused. These fix the three cases the
// single rule has to tell apart.
func TestApplicableModelLockFollowsTheCapabilitiesAnOfferingBelongsTo380(t *testing.T) {
	glb := generation.Offering{
		ID:         "meshy/meshy-7.1/text-to-3d",
		ProviderID: "meshy",
		ModelID:    "meshy-7.1",
		Capability: generation.TextTo3D,
		Enabled:    true,
	}
	rigging := generation.Offering{
		ID:         "meshy/processing/rigging",
		ProviderID: "meshy",
		ModelID:    "meshy-7.1",
		Capability: generation.Rigging,
		Enabled:    true,
	}
	offerings := []generation.Offering{glb, rigging}

	for _, test := range []struct {
		name       string
		lock       string
		capability string
		want       string
	}{
		{"the capability's own model stays a lock", "meshy-7.1", string(generation.TextTo3D), "meshy-7.1"},
		{"another capability's model is not a lock here", "meshy-7.1", string(generation.FormatConversion), ""},
		{"an empty lock is none", "  ", string(generation.TextTo3D), ""},
		{"no capability is none", "meshy-7.1", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ApplicableModelLock(offerings, test.lock, test.capability); got != test.want {
				t.Fatalf("ApplicableModelLock(%q, %q) = %q, want %q", test.lock, test.capability, got, test.want)
			}
		})
	}
}

// A lock that names a model the Catalog no longer declares stays a lock, whatever
// capability is being resolved: the step must refuse it as unavailable instead of
// silently swapping in another model (#380). Only a lock that names a *known*
// model of another capability is filtered out.
func TestApplicableModelLockKeepsAModelTheCatalogDropped380(t *testing.T) {
	offering := generation.Offering{
		ID:         "tripo/tripo-v3.1/text-to-3d",
		ProviderID: "tripo",
		ModelID:    "v3.1",
		Capability: generation.TextTo3D,
		Enabled:    true,
	}
	if got := ApplicableModelLock([]generation.Offering{offering}, "v2.0", string(generation.TextTo3D)); got != "v2.0" {
		t.Fatalf("a dropped model stopped being a lock: %q", got)
	}
	if got := ApplicableModelLock([]generation.Offering{offering}, "v2.0", string(generation.Rigging)); got != "v2.0" {
		t.Fatalf("a dropped model stopped being a lock for another capability: %q", got)
	}
	// The same *known* name bound to another capability is not this capability's lock.
	if got := ApplicableModelLock([]generation.Offering{offering}, "v3.1", string(generation.Rigging)); got != "" {
		t.Fatalf("another capability's known lock leaked into rigging: %q", got)
	}
}

// A disabled Offering of the capability still names the lock: the plan must
// refuse it as unavailable rather than re-resolve the model (#380).
func TestApplicableModelLockIgnoresTheOfferingState380(t *testing.T) {
	offering := generation.Offering{
		ID:         "tripo/tripo-v3.1/text-to-3d",
		ProviderID: "tripo",
		ModelID:    "v3.1",
		Capability: generation.TextTo3D,
		Enabled:    false,
	}
	if got := ApplicableModelLock([]generation.Offering{offering}, "v3.1", string(generation.TextTo3D)); got != "v3.1" {
		t.Fatalf("a disabled offering stopped naming the lock: %q", got)
	}
}
