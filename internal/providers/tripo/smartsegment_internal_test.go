package tripo

import (
	"os"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestSmartSegmentWebPConversionRejectsPNGOverUploadLimit(t *testing.T) {
	raw, err := os.ReadFile("../../../public/gpt-skills/_placeholder.webp")
	if err != nil {
		t.Fatal(err)
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get("tripo/geometry/smartsegment-image")
	if !ok {
		t.Fatal("Smart Segmentation offering missing")
	}
	offering.ExecutionMessages = product.Application().Runtime.GenerationMessages
	png, err := smartSegmentPNGFromWebP(offering, raw, smartSegmentMaxUploadImageBytes)
	if err != nil || len(png) < 32 {
		t.Fatalf("baseline conversion: %v", err)
	}
	_, err = smartSegmentPNGFromWebP(offering, raw, int64(len(png)-1))
	refusal := generation.UserFacingError(err)
	if refusal == nil || refusal.DetailTemplate != offering.ExecutionMessages.InputSourceTooLarge {
		t.Fatalf("error = %v", err)
	}
	if refusal.Code != generation.ErrInvalidInput {
		t.Fatalf("code = %v", refusal.Code)
	}
}
