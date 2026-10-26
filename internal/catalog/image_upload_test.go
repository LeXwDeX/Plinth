package catalog_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestImageUploadConversionOutputIsAcceptedByEveryImageToImageOffering(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range c.Application().Runtime.Frontend.ImageUpload.Conversions {
		for _, offering := range c.List(generation.ImageToImage) {
			for _, parameter := range offering.Parameters {
				if parameter.Semantic == generation.SemanticReferenceImage &&
					len(parameter.MediaTypes) > 0 &&
					!slices.Contains(parameter.MediaTypes, "image/*") &&
					!slices.Contains(parameter.MediaTypes, rule.TargetMediaType) {
					t.Errorf("%s rejects converted upload %s", offering.ID, rule.TargetMediaType)
				}
			}
		}
	}
}

func TestImageUploadSchemaIsValidatedAndDefensivelyCloned(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	public := app.Public()
	public.Runtime.Frontend.ImageUpload.Conversions[0].SourceMediaTypes[0] = "mutated"
	public.Runtime.Frontend.ImageUpload.Conversions[0].SourceExtensions[0] = "mutated"
	if app.Runtime.Frontend.ImageUpload.Conversions[0].SourceMediaTypes[0] == "mutated" ||
		app.Runtime.Frontend.ImageUpload.Conversions[0].SourceExtensions[0] == "mutated" {
		t.Fatal("public upload configuration aliases its source")
	}
	app.Runtime.Frontend.ImageUpload.Conversions[0].TargetMediaType = "mutated"
	if c.Application().Runtime.Frontend.ImageUpload.Conversions[0].TargetMediaType == "mutated" {
		t.Fatal("upload configuration aliases catalog")
	}
	for _, mimeOnly := range []bool{true, false} {
		app := c.Application()
		if mimeOnly {
			app.Runtime.Frontend.ImageUpload.Conversions[0].SourceExtensions = nil
		} else {
			app.Runtime.Frontend.ImageUpload.Conversions[0].SourceMediaTypes = nil
		}
		if err := catalog.ValidateApplicationSchema(app); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(app.Public().Runtime.Frontend.ImageUpload)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Conversions []struct {
				MediaTypes []string `json:"source_media_types"`
				Extensions []string `json:"source_extensions"`
			} `json:"conversions"`
		}
		if err := json.Unmarshal(encoded, &wire); err != nil {
			t.Fatal(err)
		}
		if wire.Conversions[0].MediaTypes == nil || wire.Conversions[0].Extensions == nil {
			t.Fatalf("public conversion lists must be arrays: %s", encoded)
		}
	}
	for name, mutate := range map[string]func(*catalog.ImageUploadSchema){
		"zero pixel budget":   func(s *catalog.ImageUploadSchema) { s.MaximumPixels = 0 },
		"unsupported encoder": func(s *catalog.ImageUploadSchema) { s.Conversions[0].TargetMediaType = "image/bmp" },
		"empty source formats": func(s *catalog.ImageUploadSchema) {
			s.Conversions[0].SourceMediaTypes = nil
			s.Conversions[0].SourceExtensions = nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			app := c.Application()
			mutate(&app.Runtime.Frontend.ImageUpload)
			if err := catalog.ValidateApplicationSchema(app); err == nil {
				t.Fatal("invalid upload configuration accepted")
			}
		})
	}
}
