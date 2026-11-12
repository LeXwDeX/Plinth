package generationtool_test

import (
	"slices"
	"strings"
	"testing"
)

// #278: the purpose of the session decides the formats an Agent-started Tripo
// task freezes. The format set is a purpose override (provenance
// "profile:<id>"); every export option a purpose does not name stays at its
// Offering default, which is the upstream default, so such a task is requested
// and priced as it was before the options existed.

func frozenStrings(t *testing.T, value any) []string {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("not a list: %v", value)
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.(string))
	}
	return result
}

func TestPrintPurposeFreezesTheGLBStlAnd3MFSetOnTheBasicExport(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	h.gateway.normalize = true // the real Catalog normalization injects the defaults
	result := h.runLocked3D("call-print-formats", purposeBrief(t, map[string]any{"purpose": "print"}), nil)
	if len(h.gateway.frozen) != 1 || result.NoSubmission || h.gateway.frozen[0].OfferingID != "tripo/tripo-v3.1-full/text-to-3d" {
		t.Fatalf("step was not frozen: %+v", result)
	}
	input, frozen := h.frozenInput(0), h.gateway.frozen[0]
	if formats := frozenStrings(t, input["output_formats"]); !slices.Equal(formats, []string{"GLTF", "STL", "3MF"}) {
		t.Fatalf("output_formats = %v, want the print set", formats)
	}
	if frozen.ParameterSources["output_formats"] != "profile:print" || frozen.ParameterReasons["output_formats"] == "" ||
		!strings.Contains(frozen.ParameterReasons["output_formats"], "STL") {
		t.Fatalf("the format set carries %q / %q, want the print purpose and its reason", frozen.ParameterSources["output_formats"], frozen.ParameterReasons["output_formats"])
	}
	// Print names no export option: its STL and 3MF carry no texture, so the
	// conversions stay on the upstream defaults (the basic price) and nothing
	// about them is attributed to the purpose.
	for name, want := range map[string]any{
		"export_texture_format": "JPEG", "export_texture_size": "4k", "export_pivot_bottom_center": false, "export_fbx_preset": "blender",
	} {
		if input[name] != want || frozen.ParameterSources[name] != "" {
			t.Errorf("%s = %v from %q, want the default %v", name, input[name], frozen.ParameterSources[name], want)
		}
	}
	if _, reported := h.content(result)[budgetNoteKey]; reported {
		t.Fatalf("values within the budget were reported: %s", result.AgentContent)
	}
}

// A model the person locked outside the purpose's recommendations does not take
// the purpose's format set, which is declared per recommended Offering: here a
// locked Tripo v3.1 under a game purpose keeps the display set and the plain
// export.
func TestLockedModelUnderAGamePurposeKeepsItsFormatsAndExport(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	h.gateway.normalize = true
	h.runLocked3D("call-game-formats", purposeBrief(t, map[string]any{"purpose": "game_prop"}), nil)
	if len(h.gateway.frozen) != 1 {
		t.Fatalf("step was not frozen: %+v", h.gateway.frozen)
	}
	input, frozen := h.frozenInput(0), h.gateway.frozen[0]
	if formats := frozenStrings(t, input["output_formats"]); len(formats) != 6 {
		t.Fatalf("output_formats = %v, want the default six for a model the purpose does not recommend", formats)
	}
	if input["export_texture_format"] != "JPEG" || input["export_texture_size"] != "4k" || input["export_pivot_bottom_center"] != false {
		t.Fatalf("export = %v / %v / %v, want the upstream defaults", input["export_texture_format"], input["export_texture_size"], input["export_pivot_bottom_center"])
	}
	for _, name := range []string{"output_formats", "export_texture_format", "export_texture_size", "export_pivot_bottom_center"} {
		if frozen.ParameterSources[name] != "" {
			t.Errorf("%s is attributed to %q although the purpose does not recommend this model", name, frozen.ParameterSources[name])
		}
	}
}

// With no purpose nothing moves: the display set on the upstream defaults.
func TestWithoutAPurposeTheExportsKeepTheUpstreamDefaults(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	h.gateway.normalize = true
	h.runLocked3D("call-no-purpose", nil, nil)
	input := h.frozenInput(0)
	if formats := frozenStrings(t, input["output_formats"]); len(formats) != 6 || input["export_texture_format"] != "JPEG" ||
		input["export_texture_size"] != "4k" || input["export_pivot_bottom_center"] != false || input["export_fbx_preset"] != "blender" {
		t.Fatalf("defaults = %v / %v / %v / %v / %v", input["output_formats"], input["export_texture_format"], input["export_texture_size"],
			input["export_pivot_bottom_center"], input["export_fbx_preset"])
	}
}
