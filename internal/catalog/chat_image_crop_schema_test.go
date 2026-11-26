package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func defaultImageCropSchema(t *testing.T) catalog.ApplicationSchema {
	t.Helper()
	defaults, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return defaults.Application()
}

// #331: the shipped Catalog declares the read-only crop tool, its parameters
// and its bilingual refusal copy.
func TestDefaultCatalogDeclaresImageCropContracts(t *testing.T) {
	application := defaultImageCropSchema(t)
	crop := application.Chat.ImageCrop
	if crop.Tool != "crop_image_regions" {
		t.Fatalf("image_crop.tool = %q", crop.Tool)
	}
	for code, text := range map[string]string{
		"invalid_arguments":  crop.Texts.InvalidArguments,
		"invalid_region":     crop.Texts.InvalidRegion,
		"image_too_small":    crop.Texts.ImageTooSmall,
		"image_too_large":    crop.Texts.ImageTooLarge,
		"crop_limit_reached": crop.Texts.CropLimitReached,
		"image_not_found":    crop.Texts.ImageNotFound,
		"image_unavailable":  crop.Texts.ImageUnavailable,
		"presented_note":     crop.Texts.PresentedNote,
		"not_presentable":    crop.Texts.NotPresentable,
		"not_presented":      crop.Texts.NotPresented,
	} {
		if strings.TrimSpace(text) == "" {
			t.Fatalf("image_crop.texts.%s is empty", code)
		}
	}
	var tool *catalog.ChatAgentToolSchema
	for index := range application.Chat.AgentTools {
		if application.Chat.AgentTools[index].Name == crop.Tool {
			tool = &application.Chat.AgentTools[index]
		}
	}
	if tool == nil {
		t.Fatalf("agent tool %q is not declared", crop.Tool)
	}
	if tool.Execution.ImplementationKey != "image.crop" ||
		len(tool.Execution.Effects) != 1 || tool.Execution.Effects[0] != "READ" ||
		tool.Execution.AgentScope != "utility" ||
		len(tool.Execution.Domains) != 1 || tool.Execution.Domains[0] != "image" {
		t.Fatalf("crop tool execution = %+v", tool.Execution)
	}
	var parameters struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"image", "regions"} {
		if _, exists := parameters.Properties[name]; !exists {
			t.Fatalf("crop tool does not declare parameter %q", name)
		}
	}
	if strings.Join(parameters.Required, ",") != "image,regions" {
		t.Fatalf("crop tool required = %v", parameters.Required)
	}
	if !strings.Contains(tool.Description, "@projectN") {
		t.Fatal("crop description does not refuse @projectN")
	}
	if strings.Contains(tool.Description, "24") {
		t.Fatal("crop description hard-codes the crop limit")
	}
	// The English bundle pairs every Chinese refusal and the coverage entry
	// enforces it.
	covered := false
	for _, path := range application.Localization.Coverage {
		covered = covered || path == "chat.image_crop"
	}
	if !covered {
		t.Fatal("localization coverage does not declare chat.image_crop")
	}
	english := application.Localization.Bundles["en"].Fields
	for _, code := range []string{
		"invalid_arguments", "invalid_region", "image_too_small", "image_too_large",
		"crop_limit_reached", "image_not_found", "image_unavailable",
		"presented_note", "not_presentable", "not_presented",
	} {
		key := "chat.image_crop.texts." + code
		text, exists := english[key]
		if !exists || strings.TrimSpace(text) == "" {
			t.Fatalf("English bundle lacks %s", key)
		}
	}
	// #642: the tool cuts a whole 4096x4096 board; the decompose crop keeps
	// its own smaller cap.
	if crop.Limits.MaximumRegionPixels < 4096*4096 || crop.Limits.MaximumPNGBytes <= 0 ||
		application.AssetList.Decompose.Crop.MaximumRegionPixels >= crop.Limits.MaximumRegionPixels {
		t.Fatalf("image_crop.limits = %+v, decompose crop = %+v",
			crop.Limits, application.AssetList.Decompose.Crop)
	}
	if err := catalog.ValidateApplicationSchema(application); err != nil {
		t.Fatalf("default image crop schema invalid: %v", err)
	}
	// An older Catalog without the section still loads; the tool then keeps
	// the decompose crop limits.
	application.Chat.ImageCrop.Limits = catalog.ChatImageCropLimits{}
	if err := catalog.ValidateApplicationSchema(application); err != nil {
		t.Fatalf("image crop schema without limits invalid: %v", err)
	}
}

// #331: the declaration is checked: the tool must exist as image.crop/READ and
// every error code keeps its Schema copy and placeholders.
func TestImageCropSchemaRejectsIncompleteDeclarations(t *testing.T) {
	cases := map[string]func(*catalog.ApplicationSchema) string{
		"missing tool": func(application *catalog.ApplicationSchema) string {
			application.Chat.ImageCrop.Tool = "missing_tool"
			return "chat.image_crop.tool"
		},
		"empty copy": func(application *catalog.ApplicationSchema) string {
			application.Chat.ImageCrop.Texts.ImageUnavailable = ""
			return "image_unavailable"
		},
		"missing detail placeholder": func(application *catalog.ApplicationSchema) string {
			application.Chat.ImageCrop.Texts.InvalidRegion = "bad region"
			return "{detail}"
		},
		"missing limit placeholder": func(application *catalog.ApplicationSchema) string {
			application.Chat.ImageCrop.Texts.CropLimitReached = "no more crops"
			return "{limit}"
		},
		// #552: the presented note names how many crops the person sees, and
		// both presentation refusals say why.
		"missing count placeholder": func(application *catalog.ApplicationSchema) string {
			application.Chat.ImageCrop.Texts.PresentedNote = "crops are shown"
			return "{count}"
		},
		"empty not presentable copy": func(application *catalog.ApplicationSchema) string {
			application.Chat.ImageCrop.Texts.NotPresentable = ""
			return "not_presentable"
		},
		"missing not presented detail": func(application *catalog.ApplicationSchema) string {
			application.Chat.ImageCrop.Texts.NotPresented = "not shown"
			return "{detail}"
		},
		// #642: the tool's own crop caps stay within the source decode bound
		// and the input-asset storage cap.
		"half-declared caps": func(application *catalog.ApplicationSchema) string {
			application.Chat.ImageCrop.Limits.MaximumRegionPixels = 0
			return "chat.image_crop.limits.maximum_region_pixels"
		},
		"region cap over the source decode bound": func(application *catalog.ApplicationSchema) string {
			application.Chat.ImageCrop.Limits.MaximumRegionPixels = application.Chat.ImageMask.Limits.MaxPixels + 1
			return "chat.image_crop.limits.maximum_region_pixels"
		},
		"PNG cap over the upload cap": func(application *catalog.ApplicationSchema) string {
			application.Chat.ImageCrop.Limits.MaximumPNGBytes = application.Runtime.InputAssets.UploadMaxBytes + 1
			return "chat.image_crop.limits.maximum_png_bytes"
		},
	}
	for name, mutate := range cases {
		application := defaultImageCropSchema(t)
		want := mutate(&application)
		err := catalog.ValidateApplicationSchema(application)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: err = %v, want %q", name, err, want)
		}
	}
}
