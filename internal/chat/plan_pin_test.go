package chat_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// planPinOfferings are the Offerings a legacy plan's model-name pins are
// resolved against (#434): one model ID in the shapes the Catalog declares,
// one model ID declared for another capability, two Offerings that share a
// model ID in the same capability (ambiguous), and a disabled one.
func planPinOfferings() []generation.Offering {
	return []generation.Offering{
		{ID: "meshy/meshy-7.1/text-to-3d", ProviderID: "meshy", ModelID: "meshy-7.1",
			Capability: generation.TextTo3D, Enabled: true},
		{ID: "meshy/meshy-7.1/image-to-3d", ProviderID: "meshy", ModelID: "meshy-7.1",
			Capability: generation.ImageTo3D, Enabled: true},
		{ID: "meshy/meshy-7.1-multiview/image-to-3d", ProviderID: "meshy", ModelID: "meshy-7.1-multiview",
			Capability: generation.ImageTo3D, Enabled: true},
		{ID: "tripo/tripo-v3.1/image-to-3d", ProviderID: "tripo", ModelID: "tripo-v3.1",
			Capability: generation.ImageTo3D, Enabled: true},
		{ID: "vendor/twin-a/image-to-3d", ProviderID: "vendor", ModelID: "twin",
			Capability: generation.ImageTo3D, Enabled: true},
		{ID: "vendor/twin-b/image-to-3d", ProviderID: "vendor", ModelID: "twin",
			Capability: generation.ImageTo3D, Enabled: true},
		{ID: "vendor/retired/text-to-3d", ProviderID: "vendor", ModelID: "retired-model",
			Capability: generation.TextTo3D, Enabled: false},
	}
}

// #434: a plan saved before #426 pinned the Agent's model name. The pin
// resolves to the Offering ID only when the name selects exactly one enabled
// Offering of the pin's capability; every other name keeps its stored value so
// the step stays off-plan.
func TestResolvePlanPinResolvesOnlyTheOneOfferingOfItsCapability(t *testing.T) {
	offerings := planPinOfferings()
	for _, tc := range []struct {
		name       string
		capability string
		pinned     string
		want       string
		found      bool
	}{
		{"model ID", "image-to-3d", "meshy-7.1-multiview", "meshy/meshy-7.1-multiview/image-to-3d", true},
		{"model ID of the other shape", "text-to-3d", "meshy-7.1", "meshy/meshy-7.1/text-to-3d", true},
		{"provider and model ID pair", "image-to-3d", "meshy/meshy-7.1-multiview", "meshy/meshy-7.1-multiview/image-to-3d", true},
		{"stored Offering ID", "image-to-3d", "tripo/tripo-v3.1/image-to-3d", "tripo/tripo-v3.1/image-to-3d", true},
		{"unknown name", "image-to-3d", "never-declared", "", false},
		{"empty pin", "image-to-3d", "", "", false},
		{"empty capability", "", "meshy-7.1-multiview", "", false},
		{"ambiguous model ID", "image-to-3d", "twin", "", false},
		{"disabled Offering", "text-to-3d", "retired-model", "", false},
		// The same model ID exists in another capability: the pin's
		// capability decides, so the two never resolve into each other.
		{"capability scopes the match", "image-to-3d", "meshy-7.1", "meshy/meshy-7.1/image-to-3d", true},
		{"capability without the model", "text-to-3d", "meshy-7.1-multiview", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, found := chat.ResolvePlanPin(offerings, tc.capability, tc.pinned)
			if got != tc.want || found != tc.found {
				t.Fatalf("ResolvePlanPin(%q, %q) = %q, %v; want %q, %v",
					tc.capability, tc.pinned, got, found, tc.want, tc.found)
			}
		})
	}
}
