package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func defaultSelectionMaskSchema(t *testing.T) catalog.ApplicationSchema {
	t.Helper()
	defaults, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return defaults.Application()
}

// #244: the shipped Catalog declares the mask binding on every generation
// tool, binds it on refine_image and declares the mask tool and its texts.
func TestDefaultCatalogDeclaresSelectionMaskContracts(t *testing.T) {
	application := defaultSelectionMaskSchema(t)
	binds := 0
	for _, tool := range application.Chat.Tools {
		switch tool.SelectionMask {
		case "bind":
			binds++
			if tool.Name != "refine_image" {
				t.Fatalf("tool %q binds the selection mask", tool.Name)
			}
			if tool.ArgumentMappings["mask"] != "mask" || tool.OverrideMappings["mask"] != "mask" {
				t.Fatalf("refine_image mask mapping = %v/%v", tool.ArgumentMappings, tool.OverrideMappings)
			}
		case "ignore":
		default:
			t.Fatalf("tool %q declares selection_mask %q", tool.Name, tool.SelectionMask)
		}
	}
	if binds != 1 {
		t.Fatalf("selection mask bindings = %d, want refine_image only", binds)
	}
	if application.Chat.API.Fields.SelectionMask == "" ||
		application.Chat.API.SelectionMask.MaskAssetID == "" {
		t.Fatalf("chat.api.selection_mask = %q/%q", application.Chat.API.Fields.SelectionMask,
			application.Chat.API.SelectionMask.MaskAssetID)
	}
	context := application.Chat.Context
	for _, want := range []string{"{handle}", "{bbox}", "{coverage}"} {
		if !strings.Contains(context.SelectionMaskItemTemplate, want) {
			t.Fatalf("selection_mask_item_template lacks %s: %q", want, context.SelectionMaskItemTemplate)
		}
	}
	if context.SelectionMaskNote == "" || context.SelectionMaskPreviewLabel == "" ||
		context.SelectionMaskPreview.MaxPixels <= 0 || context.SelectionMaskPreview.MediaType == "" {
		t.Fatalf("selection mask context = %+v", context.SelectionMaskPreview)
	}
	mask := application.Chat.ImageMask
	if mask.Tool != "create_selection_mask" || mask.Limits.MaxBytes <= 0 || mask.Limits.MaxPixels <= 0 ||
		mask.Preview.MaxPixels <= 0 {
		t.Fatalf("image_mask = %+v", mask)
	}
	for _, mode := range mask.Modes {
		if mode != "subject" && mode != "region" && mode != "invert" {
			t.Fatalf("image_mask mode = %q", mode)
		}
	}
	for _, placeholder := range []string{"{url}", "{width}", "{height}", "{coverage}"} {
		if !strings.Contains(mask.Texts.ResultNote, placeholder) {
			t.Fatalf("image_mask result_note lacks %s", placeholder)
		}
	}
	declared := false
	for _, tool := range application.Chat.AgentTools {
		if tool.Name == mask.Tool && tool.Execution.ImplementationKey == "image.mask" &&
			len(tool.Execution.Effects) == 1 && tool.Execution.Effects[0] == "READ" {
			declared = true
		}
	}
	if !declared {
		t.Fatalf("image mask tool %q is not declared as image.mask/READ", mask.Tool)
	}
	if err := catalog.ValidateApplicationSchema(application); err != nil {
		t.Fatalf("default selection mask schema invalid: %v", err)
	}
}

// #244: the declarations are checked: a bind tool must map a declared mask
// parameter, the context texts keep their placeholders and the preview stays
// within a usable style.
func TestSelectionMaskSchemaRejectsIncompleteDeclarations(t *testing.T) {
	cases := map[string]func(*catalog.ApplicationSchema) string{
		"bind without mask mapping": func(application *catalog.ApplicationSchema) string {
			for index := range application.Chat.Tools {
				if application.Chat.Tools[index].Name == "refine_image" {
					application.Chat.Tools[index].OverrideMappings = map[string]string{}
				}
			}
			return "selection_mask bind"
		},
		"missing bbox placeholder": func(application *catalog.ApplicationSchema) string {
			application.Chat.Context.SelectionMaskItemTemplate = "mask {handle} {coverage}"
			return "bbox"
		},
		"bad preview opacity": func(application *catalog.ApplicationSchema) string {
			application.Chat.Context.SelectionMaskPreview.DimOpacity = 2
			return "dim_opacity"
		},
		"unknown mask mode": func(application *catalog.ApplicationSchema) string {
			application.Chat.ImageMask.Modes = []string{"subject", "region", "magic"}
			return "未知模式"
		},
		"mask tool missing": func(application *catalog.ApplicationSchema) string {
			application.Chat.ImageMask.Tool = "missing_tool"
			return "chat.image_mask.tool"
		},
	}
	for name, mutate := range cases {
		application := defaultSelectionMaskSchema(t)
		want := mutate(&application)
		err := catalog.ValidateApplicationSchema(application)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: err = %v, want %q", name, err, want)
		}
	}
}
