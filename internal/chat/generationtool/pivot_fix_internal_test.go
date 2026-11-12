package generationtool

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// The pivot fix the usage check names is one the shipped Catalog can perform:
// Meshy Resize with origin_at=bottom is enabled, while the Tripo conversion
// that would also move the pivot is a disabled declaration, so the hint never
// names it (#278; the reasons it stays disabled are in docs/architecture.md).
func TestShippedCatalogOffersAnEnabledPivotFix(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var fix *catalog.ChatModelInspectFixSchema
	for _, candidate := range product.Application().Chat.ModelReview.Inspect.Production.Fixes {
		if candidate.Check == catalog.ModelReviewUsageCheckPivotBottomCenter {
			fix = &candidate
		}
	}
	if fix == nil || len(fix.Options) != 2 {
		t.Fatalf("the pivot fix declares %+v, want the resize and the conversion options", fix)
	}
	// Catalog.List returns the enabled Offerings only.
	enabled := func(option catalog.ChatModelInspectFixCapabilitySchema) bool {
		for _, offering := range product.List("") {
			if string(offering.Capability) == option.Capability && modelReviewOfferingSupportsFix(offering, option) {
				return true
			}
		}
		return false
	}
	if !enabled(fix.Options[0].Capabilities[0]) {
		t.Errorf("%+v has no enabled Offering that takes the declared value", fix.Options[0].Capabilities[0])
	}
	if enabled(fix.Options[1].Capabilities[0]) {
		t.Errorf("%+v is enabled; the hint would name a capability the document says is disabled", fix.Options[1].Capabilities[0])
	}
}
