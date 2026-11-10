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
			ModelID: "gpt-image-2.6-sunburst", Domain: generation.DomainImage, Enabled: true,
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
		"gpt-image-2.6-sunburst",
		"openai/gpt-image-2/text-to-image",
		"tripo-v3.1",
	} {
		if explicitModel3DValid(offerings, model) {
			t.Fatalf("an invalid 3D pick %q was accepted", model)
		}
	}
}

// #239: an explicit 2D model lock may only name an enabled Offering of the
// image domain; a 3D model is refused so the image tools do not all fail.
func TestExplicitModelImageValidNamesAnEnabledImageOfferingOnly(t *testing.T) {
	offerings := []generation.Offering{
		{
			ID: "meshy/nano-banana-pro/text-to-image", ProviderID: "meshy",
			ModelID: "nano-banana-pro", Domain: generation.DomainImage, Enabled: true,
		},
		{
			ID: "openai/gpt-image-2/text-to-image", ProviderID: "openai",
			ModelID: "gpt-image-2.6-sunburst", Domain: generation.DomainImage, Enabled: false,
		},
		{
			ID: "meshy/meshy-7.1/text-to-3d", ProviderID: "meshy",
			ModelID: "meshy-7.1", Domain: generation.DomainModel3D, Enabled: true,
		},
	}
	for _, model := range []string{
		"nano-banana-pro",
		"meshy/nano-banana-pro/text-to-image",
		"meshy/nano-banana-pro",
	} {
		if !explicitModelImageValid(offerings, model) {
			t.Fatalf("a valid image pick %q was refused", model)
		}
	}
	for _, model := range []string{
		"",
		"   ",
		"unknown-model",
		"meshy-7.1",
		"meshy/meshy-7.1/text-to-3d",
		"gpt-image-2.6-sunburst",
	} {
		if explicitModelImageValid(offerings, model) {
			t.Fatalf("an invalid image pick %q was accepted", model)
		}
	}
}
