package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"regexp"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

var providerHan = regexp.MustCompile(`\p{Han}`)

// overriddenOfferingCatalog serves one modified Offering so a real Provider
// input check can be made to refuse a small uploaded image.
type overriddenOfferingCatalog struct {
	generation.Catalog
	offering generation.Offering
}

func (c overriddenOfferingCatalog) Get(id generation.OfferingID) (generation.Offering, bool) {
	if id == c.offering.ID {
		return c.offering, true
	}
	return c.Catalog.Get(id)
}

func (c overriddenOfferingCatalog) Normalize(cap generation.Capability, id generation.OfferingID,
	raw json.RawMessage) (generation.Offering, json.RawMessage, error) {
	_, normalized, err := c.Catalog.Normalize(cap, id, raw)
	if err != nil || id != c.offering.ID {
		return generation.Offering{}, normalized, err
	}
	return c.offering, normalized, nil
}

func TestProviderValidateInputRefusalReachesTheKernel(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, mock.New(), nil)
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get("tripo/geometry/smartsegment-image")
	if !ok {
		t.Fatal("Smart Segmentation offering missing")
	}
	offering.ExecutionMessages = product.Application().Runtime.GenerationMessages
	registry := providers.NewRegistry()
	adapter, err := tripo.New(tripo.Config{
		BaseURL: "https://tripo.invalid", APIKey: "fixture-key", InputAssets: h.inputs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("tripo.v3", adapter); err != nil {
		t.Fatal(err)
	}
	if offering.ExecutionMessages.InputSourceDimensions == "" {
		t.Fatalf("offering has no dimensions text: %+v", offering.ExecutionMessages)
	}
	if refusal := offering.ExecutionMessages.InputRefusal(schema.InputRefusalDimensions, schema.InputRefusalValues{
		Width: 4096, Height: 4096, MinWidth: 1, MinHeight: 1, MaxPixels: 16000000,
	}); refusal == nil {
		t.Fatal("direct Schema refusal is nil")
	}
	module, err := generation.New(generation.Dependencies{
		Repository: h.repository, Catalog: overriddenOfferingCatalog{Catalog: product, offering: offering},
		Providers: registry, Assets: h.assets, Inputs: h.inputs, Clock: h.clock, SchemaVersion: "input-refusal-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Close() })

	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewGray(image.Rect(0, 0, 4096, 4096))); err != nil {
		t.Fatal(err)
	}
	asset, err := h.inputs.Upload(ctx, "owner-a", &imageBytes, "image/png", 20_000_000)
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{
		"image": string(asset.ID), "seg_type": "image", "granularity": "medium", "hint": "test",
		"output_formats": []string{"FBX", "OBJ", "STL", "USDZ", "3MF"}, "authorize_extra_credits": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	directErr := adapter.ValidateInput(ctx, generation.InputValidationRequest{
		OwnerID: "owner-a", Offering: offering, Input: input,
		InputAssets: map[string][]generation.InputAsset{"image": {asset}},
	})
	if refusal := generation.UserFacingError(directErr); refusal == nil {
		t.Logf("type=%T value=%+v", directErr, directErr)
		t.Fatalf("direct provider refusal has no UserFacing text: %v", directErr)
	}
	err = module.ValidateOfferingInput(ctx, "owner-a", offering.ID, offering.Revision, input)
	refusal := generation.UserFacingError(err)
	if refusal == nil {
		t.Fatalf("error = %v", err)
	}
	if refusal.DetailTemplate != product.Application().Runtime.GenerationMessages.InputSourcePixels {
		t.Fatalf("template = %q", refusal.DetailTemplate)
	}
	texts, localizerErr := catalog.NewTextLocalizer(product.Application())
	if localizerErr != nil {
		t.Fatal(localizerErr)
	}
	english := refusal.LocalizedDetail(func(text string) string { return texts.Text("en", text) })
	if providerHan.MatchString(english) || !strings.Contains(english, "The source image is") {
		t.Fatalf("English refusal = %q", english)
	}
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil ||
		!strings.Contains(provider.Failure.SafeDetail, "超过 Tripo Smart Segmentation 的上限") {
		t.Fatalf("Provider diagnostic lost: %v", err)
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(input, &values) != nil || string(values["image"]) == "" {
		t.Fatalf("input changed: %s", input)
	}
}
