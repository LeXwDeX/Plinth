package catalog

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// Image fitting steps (#276). Width and height never change; there is no
// resize step.
const (
	ImageFittingLosslessPNG = "lossless-png"
	ImageFittingLossy       = "lossy"
)

// validateInputImageLimits checks the generated-reference copy limit, the
// image fitting policy and the refusal texts (#276).
func validateInputImageLimits(schema InputAssetRuntimeSchema) error {
	if schema.GeneratedReferenceMaxBytes < schema.UploadMaxBytes {
		return errors.New("generated_reference_max_bytes 不能小于 upload_max_bytes")
	}
	fitting := schema.ImageFitting
	if len(fitting.ParameterSemantics) == 0 || len(fitting.Steps) == 0 || fitting.MaxDecodePixels <= 0 {
		return errors.New("image_fitting 必须声明参数语义、步骤和解码像素上限")
	}
	for _, semantic := range fitting.ParameterSemantics {
		if semantic == string(schemacontract.SemanticMask) || !schemacontract.ParameterSemantic(semantic).Valid() {
			return fmt.Errorf("image_fitting 参数语义 %q 无效或不可压缩", semantic)
		}
	}
	seen := map[string]bool{}
	for _, step := range fitting.Steps {
		if (step != ImageFittingLosslessPNG && step != ImageFittingLossy) || seen[step] {
			return fmt.Errorf("image_fitting 步骤 %q 无效或重复", step)
		}
		seen[step] = true
	}
	if seen[ImageFittingLossy] != (len(fitting.JPEGQualities) > 0) {
		return errors.New("image_fitting 的 lossy 步骤与 jpeg_qualities 必须同时声明")
	}
	for index, quality := range fitting.JPEGQualities {
		if quality < 1 || quality > 100 || (index > 0 && quality >= fitting.JPEGQualities[index-1]) {
			return errors.New("image_fitting.jpeg_qualities 必须在 1–100 之间并从高到低")
		}
	}
	messages := schema.Messages
	for name, check := range map[string]struct {
		value        string
		placeholders []string
	}{
		"generated_reference_too_large_template": {messages.GeneratedReferenceTooLargeTemplate, []string{"{size}", "{limit}"}},
		"parameter_too_large_template":           {messages.ParameterTooLargeTemplate, []string{"{label}", "{size}", "{limit}"}},
		"compression_insufficient_template":      {messages.CompressionInsufficientTemplate, []string{"{label}", "{size}", "{compressed}", "{limit}"}},
		"transparency_kept_template":             {messages.TransparencyKeptTemplate, []string{"{label}", "{size}", "{compressed}", "{limit}"}},
		"total_too_large_template":               {messages.TotalTooLargeTemplate, []string{"{label}", "{count}", "{size}", "{limit}"}},
		"too_many_pixels_template":               {messages.TooManyPixelsTemplate, []string{"{label}", "{width}", "{height}", "{max_pixels}"}},
		"too_small_template":                     {messages.TooSmallTemplate, []string{"{label}", "{width}", "{height}", "{min_width}", "{min_height}"}},
		"aspect_ratio_template":                  {messages.AspectRatioTemplate, []string{"{label}", "{width}", "{height}", "{max_ratio}"}},
		"unreadable_image_template":              {messages.UnreadableImageTemplate, []string{"{label}"}},
	} {
		if strings.TrimSpace(check.value) == "" {
			return fmt.Errorf("input_assets.messages.%s 不能为空", name)
		}
		for _, placeholder := range check.placeholders {
			if !strings.Contains(check.value, placeholder) {
				return fmt.Errorf("input_assets.messages.%s 缺少 %s", name, placeholder)
			}
		}
	}
	return nil
}

// validateGeneratedReferenceCoverage keeps the copy limit for generated
// references from being stricter than an Offering: every reference image an
// Offering accepts must also be copyable (#276).
func validateGeneratedReferenceCoverage(offering schemacontract.Offering, schema InputAssetRuntimeSchema) error {
	for _, parameter := range offering.Parameters {
		if parameter.Semantic != schemacontract.SemanticReferenceImage ||
			(parameter.Type != "asset" && parameter.Type != "asset-array") {
			continue
		}
		if parameter.MaxBytes > schema.GeneratedReferenceMaxBytes {
			return fmt.Errorf("参数 %q 的 max_bytes 超过 input_assets.generated_reference_max_bytes", parameter.Name)
		}
	}
	return nil
}

// ImageFittingApplies reports whether images of a parameter may be compressed
// under the Schema's image_fitting policy.
func ImageFittingApplies(fitting ImageFittingSchema, parameter schemacontract.Parameter) bool {
	return (parameter.Type == "asset" || parameter.Type == "asset-array") &&
		slices.Contains(fitting.ParameterSemantics, string(parameter.Semantic))
}
