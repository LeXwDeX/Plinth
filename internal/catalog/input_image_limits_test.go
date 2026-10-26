package catalog

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #276: reference image limits follow each upstream's official limit
// (checked 2026-10-20, see the Issue): per image, per request after base64,
// pixels and aspect ratio.
func TestReferenceImageLimitsFollowOfficialLimits(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		offering   string
		maxBytes   int64
		totalBytes int64
		input      *schema.ImageInputConstraint
	}{
		{"openai/gpt-image-2/image-to-image", 52428800, 0, nil},
		{"jimeng/seedream-4-5/image-to-image", 30_000_000, 0,
			&schema.ImageInputConstraint{MinWidth: 15, MinHeight: 15, MaxPixels: 36_000_000, MaxAspectRatio: 16}},
		{"gemini/gemini-3-pro-image/image-to-image", 14_625_000, 19_500_000, nil},
		{"meshy/nano-banana-pro/image-to-image", 20_000_000, 0, nil},
		{"meshy/nano-banana-2/image-to-image", 20_000_000, 0, nil},
		{"meshy/meshy-7/image-to-3d", 20_000_000, 0, nil},
		{"meshy/meshy-7.1/image-to-3d", 20_000_000, 0, nil},
		{"meshy/meshy-7.1-multiview/image-to-3d", 20_000_000, 0, nil},
		{"tripo/tripo-v3.1-full/image-to-3d", 20_000_000, 0, nil},
		{"tripo/tripo-v3.1/image-to-3d", 20_000_000, 0, nil},
		{"hyper3d/rodin-gen-2.5/image-to-3d", 10485760, 0, nil},
	} {
		offering, found := c.Get(schema.OfferingID(test.offering))
		if !found {
			t.Fatalf("%s missing", test.offering)
		}
		parameter := specialParameter(t, offering, "reference_assets")
		if parameter.MaxBytes != test.maxBytes || parameter.MaxTotalEncodedBytes != test.totalBytes {
			t.Fatalf("%s: max_bytes=%d total=%d", test.offering, parameter.MaxBytes, parameter.MaxTotalEncodedBytes)
		}
		if (test.input == nil) != (parameter.ImageInput == nil) || (test.input != nil && *test.input != *parameter.ImageInput) {
			t.Fatalf("%s: image_input = %+v", test.offering, parameter.ImageInput)
		}
	}
	runtime := c.Application().Runtime.InputAssets
	if runtime.GeneratedReferenceMaxBytes <= runtime.UploadMaxBytes {
		t.Fatalf("generated reference limit %d is not above the upload limit %d",
			runtime.GeneratedReferenceMaxBytes, runtime.UploadMaxBytes)
	}
}

func TestInputImageLimitsRejectInvalidDeclarations(t *testing.T) {
	inputAssets := func(doc map[string]any) map[string]any {
		return doc["application"].(map[string]any)["runtime"].(map[string]any)["input_assets"].(map[string]any)
	}
	reference := func(doc map[string]any, id string) map[string]any {
		for _, raw := range doc["offerings"].([]any) {
			offering := raw.(map[string]any)
			if offering["id"] == id {
				for _, rawParameter := range offering["parameters"].([]any) {
					if parameter := rawParameter.(map[string]any); parameter["name"] == "reference_assets" {
						return parameter
					}
				}
			}
		}
		t.Fatalf("%s reference_assets missing", id)
		return nil
	}
	for name, mutate := range map[string]func(map[string]any){
		"generated below upload": func(doc map[string]any) { inputAssets(doc)["generated_reference_max_bytes"] = 1 },
		"offering above generated": func(doc map[string]any) {
			reference(doc, "openai/gpt-image-2/image-to-image")["max_bytes"] = 104857601
		},
		"unknown step": func(doc map[string]any) {
			inputAssets(doc)["image_fitting"].(map[string]any)["steps"] = []string{"lossless-png", "resize"}
		},
		"ascending qualities": func(doc map[string]any) {
			inputAssets(doc)["image_fitting"].(map[string]any)["jpeg_qualities"] = []int{75, 95}
		},
		"mask compressed": func(doc map[string]any) {
			inputAssets(doc)["image_fitting"].(map[string]any)["parameter_semantics"] = []string{"mask"}
		},
		"template without numbers": func(doc map[string]any) {
			inputAssets(doc)["messages"].(map[string]any)["compression_insufficient_template"] = "too large"
		},
		"total below one image": func(doc map[string]any) {
			reference(doc, "gemini/gemini-3-pro-image/image-to-image")["max_total_encoded_bytes"] = 1000
		},
		"aspect ratio below one": func(doc map[string]any) {
			reference(doc, "jimeng/seedream-4-5/image-to-image")["image_input"].(map[string]any)["max_aspect_ratio"] = 0.5
		},
	} {
		var doc map[string]any
		if err := json.Unmarshal(defaultCatalog, &doc); err != nil {
			t.Fatal(err)
		}
		mutate(doc)
		raw, _ := json.Marshal(doc)
		if _, err := Load(bytes.NewReader(raw)); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}
