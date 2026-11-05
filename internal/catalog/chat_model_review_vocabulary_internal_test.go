package catalog

import (
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

// TestModelReviewVocabularyMatchesRenderer keeps the rendering vocabulary
// Catalog uses to validate product declarations equal to what the renderer can
// draw. Catalog cannot import internal/modelinspect (architecture boundary),
// so the copies need this guard.
func TestModelReviewVocabularyMatchesRenderer(t *testing.T) {
	views := []string{
		string(modelinspect.ViewFront),
		string(modelinspect.ViewBack),
		string(modelinspect.ViewLeft),
		string(modelinspect.ViewRight),
		string(modelinspect.ViewTop),
		string(modelinspect.ViewThreeQuarter),
	}
	if !slices.Equal(modelReviewViews, views) {
		t.Fatalf("catalog views = %v, renderer views = %v", modelReviewViews, views)
	}
	modes := []string{
		string(modelinspect.ModeTextured),
		string(modelinspect.ModeClay),
		string(modelinspect.ModeNormals),
		string(modelinspect.ModeWireframe),
	}
	if !slices.Equal(modelReviewModes, modes) {
		t.Fatalf("catalog modes = %v, renderer modes = %v", modelReviewModes, modes)
	}
	if modelReviewMaxRenderDimension != modelinspect.MaxRenderDimension {
		t.Fatalf("catalog ceiling = %d, renderer ceiling = %d",
			modelReviewMaxRenderDimension, modelinspect.MaxRenderDimension)
	}
}
