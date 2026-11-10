package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #239: the shipped 2D purpose table declares five purposes, each with
// recommended offerings of the image domain, public overrides and checkable
// requirements. The public projection carries only ids and labels.
func TestShippedImage2DUsageDeclaresFivePurposes(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := product.Application()
	usage := app.Chat.Image2DUsage
	if len(usage.Profiles) != 5 {
		t.Fatalf("2D profiles = %d, want 5", len(usage.Profiles))
	}
	expected := []string{"concept_art", "illustration", "icon_prop", "texture_asset", "model3d_input"}
	byID := map[string]catalog.ChatImage2DUsageProfile{}
	for _, profile := range usage.Profiles {
		byID[profile.ID] = profile
	}
	for _, id := range expected {
		profile, found := byID[id]
		if !found {
			t.Fatalf("missing purpose %q", id)
		}
		if profile.Reason == "" || len(profile.RecommendedOfferings) == 0 {
			t.Fatalf("purpose %q = %+v", id, profile)
		}
		for shape, ids := range profile.RecommendedOfferings {
			if shape != "text-to-image" && shape != "image-to-image" {
				t.Fatalf("purpose %q has shape %q", id, shape)
			}
			for _, offeringID := range ids {
				offering, found := product.Get(generation.OfferingID(offeringID))
				if !found || offering.Domain != "image" || string(offering.Capability) != shape {
					t.Fatalf("purpose %q recommendation %q does not match %q", id, offeringID, shape)
				}
			}
		}
	}
	// The 3D input purpose must be expressible with a pose and a clean
	// background: its recommendations declare both, and its reason sends the
	// Agent to the 3D input checklist.
	input := byID["model3d_input"]
	if input.Requirements.Background != "solid_or_transparent" || input.Requirements.Pose != "a-pose" ||
		!strings.Contains(input.Reason, "inspect_image") {
		t.Fatalf("model3d_input = %+v", input)
	}
	public := product.Application().Public().Chat.Image2DUsage
	if public == nil || len(public.Profiles) != 5 {
		t.Fatalf("public 2D usage = %+v", public)
	}
	encoded, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "recommended_offerings") || strings.Contains(string(encoded), `"reason"`) {
		t.Fatalf("public 2D usage leaks server-only fields: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"label":"概念设定图"`) {
		t.Fatalf("public 2D usage lacks labels: %s", encoded)
	}
}

// loadMutatedImage2D loads the shipped Catalog after changing its raw
// document, because Offering and localization checks run in Load, not in
// ValidateApplicationSchema.
func loadMutatedImage2D(t *testing.T, change func(chat map[string]any, document map[string]any)) error {
	t.Helper()
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	application := document["application"].(map[string]any)
	chat := application["chat"].(map[string]any)
	change(chat, document)
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	_, err = catalog.Load(bytes.NewReader(encoded))
	return err
}

// The validator refuses 2D purposes the runtime could not honor.
func TestImage2DUsageValidationRefusesUnsafeDeclarations(t *testing.T) {
	usage := func(chat map[string]any) map[string]any {
		return chat["image2d_usage"].(map[string]any)
	}
	profiles := func(chat map[string]any) []any {
		return usage(chat)["profiles"].([]any)
	}
	profile := func(chat map[string]any, index int) map[string]any {
		return profiles(chat)[index].(map[string]any)
	}
	requirements := func(chat map[string]any, index int) map[string]any {
		return profile(chat, index)["requirements"].(map[string]any)
	}
	tests := []struct {
		name   string
		change func(chat map[string]any, document map[string]any)
		want   string
	}{
		{"duplicate purpose", func(chat map[string]any, _ map[string]any) {
			profile(chat, 1)["id"] = profile(chat, 0)["id"]
		}, "重复"},
		{"unknown default", func(chat map[string]any, _ map[string]any) {
			usage(chat)["default_profile"] = "missing"
		}, "default_profile"},
		{"unknown shape", func(chat map[string]any, _ map[string]any) {
			profile(chat, 0)["recommended_offerings"].(map[string]any)["text-to-3d"] =
				[]any{"meshy/nano-banana-pro/text-to-image"}
		}, "未知输入形态"},
		{"offering of the wrong shape", func(chat map[string]any, _ map[string]any) {
			profile(chat, 0)["recommended_offerings"].(map[string]any)["text-to-image"] =
				[]any{"meshy/nano-banana-pro/image-to-image"}
		}, "不是"},
		{"override of a hidden parameter", func(chat map[string]any, _ map[string]any) {
			profile(chat, 0)["parameter_overrides"] = map[string]any{
				"openai/gpt-image-2/text-to-image": map[string]any{"moderation": "low"},
			}
			profile(chat, 0)["recommended_offerings"].(map[string]any)["text-to-image"] =
				[]any{"openai/gpt-image-2/text-to-image"}
		}, "可公开调整"},
		{"unknown override value", func(chat map[string]any, _ map[string]any) {
			profile(chat, 0)["parameter_overrides"] = map[string]any{
				"meshy/nano-banana-pro/text-to-image": map[string]any{"aspect_ratio": "5:7"},
			}
		}, "aspect_ratio"},
		{"a transparent background no recommendation can express", func(chat map[string]any, _ map[string]any) {
			// Gemini offers neither a transparent background option nor a
			// remove_background switch.
			requirements(chat, 0)["background"] = "transparent"
		}, "transparent"},
		{"a pose no recommendation can express", func(chat map[string]any, _ map[string]any) {
			// Gemini has no pose_mode parameter.
			requirements(chat, 0)["pose"] = "a-pose"
		}, "pose_mode"},
		{"an undeclared requirement value", func(chat map[string]any, _ map[string]any) {
			requirements(chat, 0)["aspect_ratio"] = "5:7"
		}, "aspect_ratio"},
		{"a missing requirement translation", func(chat map[string]any, document map[string]any) {
			localization := document["application"].(map[string]any)["localization"].(map[string]any)
			fields := localization["bundles"].(map[string]any)["en"].(map[string]any)["fields"].(map[string]any)
			delete(fields, "chat.image2d_usage.requirements_note.subjects_texts.single")
		}, "subjects_texts"},
		{"ask_choice enum out of sync", func(chat map[string]any, _ map[string]any) {
			for _, raw := range chat["agent_tools"].([]any) {
				tool := raw.(map[string]any)
				if tool["name"] != "ask_choice" {
					continue
				}
				properties := tool["parameters"].(map[string]any)["properties"].(map[string]any)
				properties["kind"].(map[string]any)["enum"] =
					[]any{"usage_profile", "image_usage_profile", "direction_renamed"}
			}
		}, "enum"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := loadMutatedImage2D(t, test.change)
			if err == nil {
				t.Fatal("Load accepted an unsafe 2D purpose")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want mention of %q", err, test.want)
			}
		})
	}
}
