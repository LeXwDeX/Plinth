package app

import (
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/imagefit"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// The Schema's image_fitting step names are the ones the encoder runs, and the
// default policy never resizes (#276).
func TestInputImageFittingMapsSchemaPolicy(t *testing.T) {
	if catalog.ImageFittingLosslessPNG != imagefit.StepLosslessPNG || catalog.ImageFittingLossy != imagefit.StepLossy {
		t.Fatal("Schema step names differ from the encoder's")
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	runtime := product.Application().Runtime.InputAssets
	fitting := inputImageFitting(runtime)
	if !slices.Equal(fitting.Policy.Steps, []string{imagefit.StepLosslessPNG, imagefit.StepLossy}) ||
		len(fitting.Policy.JPEGQualities) == 0 || !slices.Equal(fitting.ParameterSemantics, []string{"reference-image"}) {
		t.Fatalf("fitting = %+v", fitting)
	}
	messages := inputImageMessages(runtime)
	for name, text := range map[string]string{
		"parameter_too_large": messages.ParameterTooLarge, "compression_insufficient": messages.CompressionInsufficient, "transparency_kept": messages.TransparencyKept,
		"total_too_large": messages.TotalTooLarge, "too_many_pixels": messages.TooManyPixels,
		"too_small": messages.TooSmall, "aspect_ratio": messages.AspectRatio, "unreadable": messages.UnreadableImage,
	} {
		if text == "" {
			t.Fatalf("%s text not wired", name)
		}
	}
}
