package app

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #103 review C4: an explicit 3D model lock may only name an enabled Offering
// of the 3D domain. The picker sends model_id; the Offering id and the
// provider/model pair are accepted because the generation tools match them.
func TestExplicitModel3DValidNamesAnEnabled3DOfferingOnly(t *testing.T) {
	offerings := []generation.Offering{
		{
			ID: "meshy/meshy-7.1/text-to-3d", ProviderID: "meshy",
			ModelID: "meshy-7.1", Domain: generation.DomainModel3D, Enabled: true,
		},
		{
			ID: "tripo/tripo-v3.1/image-to-3d", ProviderID: "tripo",
			ModelID: "tripo-v3.1", Domain: generation.DomainModel3D, Enabled: false,
		},
		{
			ID: "openai/gpt-image-2/text-to-image", ProviderID: "openai",
			ModelID: "gpt-image-2.5-sunburst", Domain: generation.DomainImage, Enabled: true,
		},
	}
	for _, model := range []string{
		"meshy-7.1",
		"meshy/meshy-7.1/text-to-3d",
		"meshy/meshy-7.1",
	} {
		if !explicitModel3DValid(offerings, model) {
			t.Fatalf("a valid 3D pick %q was refused", model)
		}
	}
	for _, model := range []string{
		"",
		"   ",
		"unknown-model",
		"gpt-image-2.5-sunburst",
		"openai/gpt-image-2/text-to-image",
		"tripo-v3.1",
	} {
		if explicitModel3DValid(offerings, model) {
			t.Fatalf("an invalid 3D pick %q was accepted", model)
		}
	}
}
