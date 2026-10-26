package httpapi

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestLegacyInputApplicabilityRejectsSuppliedUnsupportedFields(t *testing.T) {
	t.Parallel()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id     generation.OfferingID
		fields []string
	}{
		{"tripo/processing/rigging", []string{"model_url", "height_meters", "texture_image_url"}},
		{"tripo/processing/texture", []string{"model_url", "target_formats", "enable_pbr", "resolution"}},
		{"tripo/processing/animation", []string{"action_id"}},
	} {
		o, ok := c.Get(tc.id)
		if !ok {
			t.Fatal(tc.id)
		}
		var legacy *catalog.LegacyTaskAPISchema
		for _, cap := range c.Application().Capabilities {
			if cap.ID == o.Capability {
				legacy = cap.LegacyAPI
			}
		}
		for _, field := range tc.fields {
			for _, value := range []any{"", false, float64(0)} {
				input := map[string]any{field: value, "source_task": "source"}
				before := map[string]any{field: value, "source_task": "source"}
				if err := applyLegacyFieldForwarding(input, o, legacy.Fields); err == nil {
					t.Fatalf("%s accepted %s=%v", tc.id, field, value)
				}
				if !reflect.DeepEqual(input, before) {
					t.Fatalf("rejected input mutated: %v", input)
				}
			}
		}
	}
	texture, _ := c.Get("tripo/processing/texture")
	var legacy *catalog.LegacyTaskAPISchema
	for _, cap := range c.Application().Capabilities {
		if cap.ID == texture.Capability {
			legacy = cap.LegacyAPI
		}
	}
	input := map[string]any{"text_style_prompt": "silver"}
	if err := applyLegacyFieldForwarding(input, texture, legacy.Fields); err != nil || input["prompt"] != "silver" {
		t.Fatalf("valid texture prompt: %v %v", input, err)
	}
	input = map[string]any{"text_style_prompt": "silver", "prompt": "gold"}
	if err := applyLegacyFieldForwarding(input, texture, legacy.Fields); err == nil {
		t.Fatal("conflicting alias and target accepted")
	}
	// Excluded image input fails before persistence, including empty input.
	for _, alias := range legacy.AssetAliases {
		if err := (&Server{}).persistLegacyInlineAssets(context.Background(), "owner", map[string]any{alias.Name: ""}, texture, legacy.AssetAliases); err == nil {
			t.Fatal("unsupported asset alias accepted")
		}
	}
	meshy, _ := c.Get("meshy/processing/retexture")
	input = map[string]any{"model_url": "https://model.invalid/source.glb", "text_style_prompt": "gold"}
	if err := applyLegacyFieldForwarding(input, meshy, legacy.Fields); err != nil || input["source_url"] != "https://model.invalid/source.glb" || input["prompt"] != "gold" {
		t.Fatalf("Meshy mapping changed: %v %v", input, err)
	}
}

func TestLegacyNativeRigAliasesRequireExactOfferingAndVariant(t *testing.T) {
	t.Parallel()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var legacy catalog.LegacyTaskAPISchema
	for _, cap := range c.Application().Capabilities {
		if cap.ID == generation.Rigging {
			legacy = *cap.LegacyAPI
		}
	}
	task := generation.Task{ID: "task", Offering: generation.Offering{ID: "tripo/processing/rigging"}, Assets: []generation.Asset{
		{ID: "fbx", Role: "model", Variant: "fbx", MediaType: "model/fbx"},
		{ID: "glb", Role: "model", Variant: "glb", MediaType: "model/gltf-binary"},
	}}
	response := map[string]any{}
	projectLegacyResultAssets(response, task, legacy, configuredTaskAssetRoute(t))
	result, ok := response[legacy.ResultField].(map[string]any)
	if !ok {
		t.Fatalf("missing result: %v", response)
	}
	for _, alias := range legacy.ResultAssets {
		variant := alias.OfferingVariants[task.Offering.ID]
		if result[alias.Name] != "/configured/tasks/task/files/"+variant {
			t.Fatalf("alias %s = %v", alias.Name, result[alias.Name])
		}
	}
	for _, variant := range []string{"", "other", "rigged_character_glb_url"} {
		task.Assets = []generation.Asset{{ID: "wrong", Role: "model", Variant: variant, MediaType: "model/gltf-binary"}}
		response = map[string]any{}
		projectLegacyResultAssets(response, task, legacy, configuredTaskAssetRoute(t))
		if result, ok := response[legacy.ResultField].(map[string]any); ok && len(result) > 0 {
			t.Fatalf("MIME fallback admitted native wrong slot %q: %v", variant, response)
		}
	}
	task.Offering.ID = "tripo/processing/animation"
	task.Assets = []generation.Asset{{ID: "wrong", Role: "model", Variant: "glb", MediaType: "model/gltf-binary"}}
	response = map[string]any{}
	projectLegacyResultAssets(response, task, legacy, configuredTaskAssetRoute(t))
	if result, ok := response[legacy.ResultField].(map[string]any); ok && len(result) > 0 {
		t.Fatalf("wrong offering got Rig aliases: %v", response)
	}
}

func TestHarnessEstimatePreservesExplicitAndOptionalSelection(t *testing.T) {
	t.Parallel()
	o := generation.Offering{Cost: &generation.CostDeclaration{SelectionParameter: "formats"}, Parameters: []generation.Parameter{{Name: "formats", Fixed: true, Default: []byte(`["FBX"]`)}}}
	input := map[string]json.RawMessage{"formats": json.RawMessage(`[]`)}
	if got := harnessEstimateInput(o, input); string(got["formats"]) != "[]" {
		t.Fatal("explicit selection changed")
	}
	o.Parameters[0].Fixed = false
	if got := harnessEstimateInput(o, map[string]json.RawMessage{}); len(got) != 0 {
		t.Fatal("optional selection defaulted")
	}
}
