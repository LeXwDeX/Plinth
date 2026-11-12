package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #282: the first recommendation of every input shape is what runs unless the
// person or the Agent chose another model, so the Schema must show that it
// reaches the purpose's minimum short edge. The shipped table is checked
// against the declared tiers and frame sizes, not against what the loader
// happens to accept.
func TestShippedFirstRecommendationsReachTheirMinimumShortEdge(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	usage := product.Application().Chat.Image2DUsage
	for _, profile := range usage.Profiles {
		for shape, ids := range profile.RecommendedOfferings {
			first := ids[0]
			offering, found := product.Get(generation.OfferingID(first))
			if !found {
				t.Fatalf("purpose %q shape %q: missing offering %q", profile.ID, shape, first)
			}
			short, proven := catalog.Image2DShortEdge(offering, profile.ParameterOverrides[first], usage.OutputResolutions)
			acknowledged := false
			for _, id := range profile.ResolutionUnverifiedOfferings {
				acknowledged = acknowledged || id == first
			}
			switch {
			case proven && short >= profile.Requirements.MinimumShortEdge && !acknowledged:
			case !proven && acknowledged:
				// The one accepted gap: the purpose needs a pose, which only
				// Meshy's Nano Banana can pin, and its resolution is not in the
				// Schema. inspect_image still judges the delivered picture.
				if profile.ID != "model3d_input" {
					t.Fatalf("purpose %q shape %q acknowledges an unproven first recommendation %q", profile.ID, shape, first)
				}
			default:
				t.Fatalf("purpose %q shape %q: first recommendation %q reaches %d (proven=%t, acknowledged=%t), purpose needs %d",
					profile.ID, shape, first, short, proven, acknowledged, profile.Requirements.MinimumShortEdge)
			}
		}
	}
}

// The shipped numbers behind that rule: Gemini's 4K tier reaches 3072 on the
// wide concept frame and 3547 on the portrait frame (Google's published sizes
// are 3072 and 3584); GPT Image and Seedream state their frame in pixels.
func TestShippedShortEdgesAreTheDeclaredOnes(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	usage := product.Application().Chat.Image2DUsage
	profiles := map[string]catalog.ChatImage2DUsageProfile{}
	for _, profile := range usage.Profiles {
		profiles[profile.ID] = profile
	}
	for _, test := range []struct {
		profile, offering string
		want              int
	}{
		{"concept_art", "gemini/gemini-3-pro-image/text-to-image", 3072},
		{"illustration", "gemini/gemini-3-pro-image/text-to-image", 3547},
		{"illustration", "gemini/gemini-3-pro-image/image-to-image", 3547},
		{"icon_prop", "openai/gpt-image-2/text-to-image", 2048},
		{"texture_asset", "openai/gpt-image-2/image-to-image", 2048},
		{"texture_asset", "jimeng/seedream-4-5/text-to-image", 4096},
	} {
		profile := profiles[test.profile]
		offering, found := product.Get(generation.OfferingID(test.offering))
		if !found {
			t.Fatalf("missing offering %q", test.offering)
		}
		got, proven := catalog.Image2DShortEdge(offering, profile.ParameterOverrides[test.offering], usage.OutputResolutions)
		if !proven || got != test.want {
			t.Fatalf("%s on %s = %d (proven=%t), want %d", test.offering, test.profile, got, proven, test.want)
		}
	}
	// Nano Banana has no resolution parameter: nothing in the Schema shows what
	// it delivers, so it is never counted as reaching a requirement.
	nano, _ := product.Get("meshy/nano-banana-pro/text-to-image")
	if short, proven := catalog.Image2DShortEdge(nano, profiles["concept_art"].ParameterOverrides["meshy/nano-banana-pro/text-to-image"], usage.OutputResolutions); proven {
		t.Fatalf("Nano Banana was shown to reach %d without a declaration", short)
	}
}

func TestReachingOfferingsNameTheModelsThatCanDeliver(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	usage := product.Application().Chat.Image2DUsage
	lookup := func(id string) (generation.Offering, bool) { return product.Get(generation.OfferingID(id)) }
	reaching := map[string][]string{}
	for _, profile := range usage.Profiles {
		for _, offering := range usage.ReachingOfferings(profile, lookup) {
			reaching[profile.ID] = append(reaching[profile.ID], string(offering.ID))
		}
	}
	// Nano Banana never appears; the first recommendation always does.
	if got := reaching["illustration"]; len(got) != 2 || got[0] != "gemini/gemini-3-pro-image/text-to-image" ||
		got[1] != "gemini/gemini-3-pro-image/image-to-image" {
		t.Fatalf("illustration reaching = %v", got)
	}
	for _, id := range reaching["texture_asset"] {
		if strings.HasPrefix(id, "meshy/") {
			t.Fatalf("an Offering with no resolution declaration was named: %v", reaching["texture_asset"])
		}
	}
	if len(reaching["model3d_input"]) != 0 {
		t.Fatalf("model3d_input names models the Schema cannot show: %v", reaching["model3d_input"])
	}
}

// #287: a texture must tile, and lossless output comes first. The shipped
// texture purpose requires tiling, tells the Agent how to write the prompt,
// and recommends the PNG model before the one that always delivers JPEG.
func TestShippedTexturePurposeRequiresTilingAndPrefersLosslessOutput(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	usage := product.Application().Chat.Image2DUsage
	var texture catalog.ChatImage2DUsageProfile
	for _, profile := range usage.Profiles {
		if profile.Requirements.Tileable && profile.ID != "texture_asset" {
			t.Fatalf("purpose %q requires tiling", profile.ID)
		}
		if profile.ID == "texture_asset" {
			texture = profile
		}
	}
	if !texture.Requirements.Tileable {
		t.Fatalf("texture_asset does not require tiling: %+v", texture.Requirements)
	}
	for _, want := range []string{"seamless tileable", "正面平视", "均匀漫射光", "没有单一视觉焦点"} {
		if !strings.Contains(texture.PromptGuidance, want) {
			t.Fatalf("texture prompt guidance omits %q: %s", want, texture.PromptGuidance)
		}
	}
	for shape, ids := range texture.RecommendedOfferings {
		first, found := product.Get(generation.OfferingID(ids[0]))
		if !found {
			t.Fatalf("shape %q: missing %q", shape, ids[0])
		}
		var format generation.Parameter
		for _, parameter := range first.Parameters {
			if parameter.Name == "output_format" {
				format = parameter
			}
		}
		if string(format.Default) != `"png"` {
			t.Fatalf("shape %q: first texture recommendation %q delivers %s, not PNG", shape, ids[0], format.Default)
		}
		for _, forced := range format.ForcedWhen {
			if string(forced.Value) != `"png"` {
				t.Fatalf("shape %q: %q is forced to %s", shape, ids[0], forced.Value)
			}
		}
		// Seedream always delivers JPEG, so it is never the first choice.
		for index, id := range ids {
			if strings.HasPrefix(id, "jimeng/") && index != len(ids)-1 {
				t.Fatalf("shape %q: the JPEG model is not last: %v", shape, ids)
			}
		}
	}
}

func TestImage2DResolutionValidationRefusesUnprovableDeclarations(t *testing.T) {
	usage := func(chat map[string]any) map[string]any { return chat["image2d_usage"].(map[string]any) }
	profile := func(chat map[string]any, index int) map[string]any {
		return usage(chat)["profiles"].([]any)[index].(map[string]any)
	}
	shapes := func(chat map[string]any, index int) map[string]any {
		return profile(chat, index)["recommended_offerings"].(map[string]any)
	}
	resolutions := func(chat map[string]any) map[string]any {
		return usage(chat)["output_resolutions"].(map[string]any)
	}
	tests := []struct {
		name   string
		change func(chat map[string]any, document map[string]any)
		want   string
	}{
		{"a first recommendation with no resolution in the Schema", func(chat, _ map[string]any) {
			// Nano Banana first: it has no frame size and no tier table.
			shapes(chat, 0)["text-to-image"] = []any{"meshy/nano-banana-pro/text-to-image", "gemini/gemini-3-pro-image/text-to-image"}
		}, "没有可证明的分辨率"},
		{"a first recommendation that falls short", func(chat, _ map[string]any) {
			// Gemini 1K at 3:4 is 886 pixels, below the illustration minimum.
			profile(chat, 1)["parameter_overrides"].(map[string]any)["gemini/gemini-3-pro-image/text-to-image"] =
				map[string]any{"aspect_ratio": "3:4", "image_size": "1K"}
		}, "低于用途要求"},
		{"a minimum above what the tier delivers", func(chat, _ map[string]any) {
			profile(chat, 0)["requirements"].(map[string]any)["minimum_short_edge"] = json.Number("3073")
		}, "低于用途要求"},
		{"an acknowledgement of a provable recommendation", func(chat, _ map[string]any) {
			profile(chat, 0)["resolution_unverified_offerings"] = []any{"gemini/gemini-3-pro-image/text-to-image"}
		}, "不应列入"},
		{"an acknowledgement of a recommendation that is not first", func(chat, _ map[string]any) {
			profile(chat, 4)["resolution_unverified_offerings"] = []any{"meshy/nano-banana-pro/text-to-image", "meshy/nano-banana-2/text-to-image"}
		}, "不是该用途任何输入形态的首推"},
		{"the pose purpose without its acknowledgement", func(chat, _ map[string]any) {
			delete(profile(chat, 4), "resolution_unverified_offerings")
		}, "resolution_unverified_offerings"},
		{"a tier table that omits a tier", func(chat, _ map[string]any) {
			delete(resolutions(chat)["gemini/gemini-3-pro-image/text-to-image"].(map[string]any)["pixels"].(map[string]any), "2K")
		}, "缺少选项"},
		{"a tier table that names a tier the parameter lacks", func(chat, _ map[string]any) {
			resolutions(chat)["gemini/gemini-3-pro-image/text-to-image"].(map[string]any)["pixels"].(map[string]any)["8K"] = json.Number("67108864")
		}, "不是参数"},
		{"a tier table for a parameter the Offering lacks", func(chat, _ map[string]any) {
			resolutions(chat)["gemini/gemini-3-pro-image/text-to-image"].(map[string]any)["parameter"] = "resolution"
		}, "select 参数"},
		{"a tier table for a missing Offering", func(chat, _ map[string]any) {
			resolutions(chat)["gemini/no-such-model/text-to-image"] = map[string]any{
				"parameter": "image_size", "pixels": map[string]any{"1K": json.Number("1048576")},
			}
		}, "不存在"},
		{"a tier with no pixels", func(chat, _ map[string]any) {
			resolutions(chat)["gemini/gemini-3-pro-image/text-to-image"].(map[string]any)["pixels"].(map[string]any)["1K"] = json.Number("0")
		}, "正整数"},
		{"a tileable purpose with no words for the brief", func(chat, _ map[string]any) {
			delete(usage(chat)["requirements_note"].(map[string]any), "tileable_text")
		}, "tileable_text"},
		{"prompt guidance with no template", func(chat, _ map[string]any) {
			usage(chat)["requirements_note"].(map[string]any)["guidance_template"] = "提示词要点"
		}, "guidance_template"},
		{"a missing translation of the new words", func(chat map[string]any, document map[string]any) {
			localization := document["application"].(map[string]any)["localization"].(map[string]any)
			fields := localization["bundles"].(map[string]any)["en"].(map[string]any)["fields"].(map[string]any)
			delete(fields, "chat.image2d_usage.profiles[id=texture_asset].prompt_guidance")
		}, "prompt_guidance"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := loadMutatedImage2D(t, test.change)
			if err == nil {
				t.Fatal("Load accepted an unprovable resolution declaration")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want mention of %q", err, test.want)
			}
		})
	}
}

// #282/#284/#287: the thresholds and words of the purpose checks are Schema;
// the loader refuses a Catalog that declares purposes without them or with
// words that would leak a placeholder to the Agent.
func TestImageUsageReviewValidationRefusesIncompleteChecks(t *testing.T) {
	usage := func(chat map[string]any) map[string]any {
		return chat["image_review"].(map[string]any)["usage"].(map[string]any)
	}
	hints := func(chat map[string]any) map[string]any { return usage(chat)["hints"].(map[string]any) }
	tests := []struct {
		name   string
		change func(chat map[string]any, document map[string]any)
		want   string
	}{
		{"purposes without purpose checks", func(chat, _ map[string]any) {
			delete(chat["image_review"].(map[string]any), "usage")
		}, "chat.image_review.usage 必须声明"},
		{"a missing hint", func(chat, _ map[string]any) { delete(hints(chat), "edge_fringe") }, "hints.edge_fringe"},
		{"an unknown check", func(chat, _ map[string]any) { hints(chat)["sharpness"] = "x" }, "未知检查"},
		{"an unknown placeholder", func(chat, _ map[string]any) {
			hints(chat)["alpha"] = "没有透明通道：{actual} {colour}"
		}, "未知占位符"},
		{"a resolution hint that names no model", func(chat, _ map[string]any) {
			hints(chat)["short_edge"] = "分辨率不够：{actual}，要求 {minimum}。"
		}, "{models}"},
		{"a models template without the list", func(chat, _ map[string]any) {
			usage(chat)["models_template"] = "没有模型"
		}, "models_template"},
		{"a seam ratio that cannot flag anything", func(chat, _ map[string]any) {
			usage(chat)["tiling"].(map[string]any)["max_seam_ratio"] = json.Number("1")
		}, "usage.tiling"},
		{"a negative transparent ratio", func(chat, _ map[string]any) {
			usage(chat)["alpha"].(map[string]any)["min_transparent_ratio"] = json.Number("-0.1")
		}, "usage.alpha"},
		{"an edge band that covers the picture", func(chat, _ map[string]any) {
			usage(chat)["edge_fringe"].(map[string]any)["band_ratio"] = json.Number("0.5")
		}, "usage.edge_fringe"},
		{"a purpose note without the purpose", func(chat, _ map[string]any) {
			usage(chat)["brief_purpose_note"] = "已核对"
		}, "{purpose}"},
		{"a transparent field that is not a brief boolean", func(chat, _ map[string]any) {
			usage(chat)["transparent_field"] = "image_use"
		}, "transparent_field"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := loadMutatedImage2D(t, test.change)
			if err == nil {
				t.Fatal("Load accepted incomplete purpose checks")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want mention of %q", err, test.want)
			}
		})
	}
}

func TestShippedImageUsageReviewIsDeclared(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	usage := product.Application().Chat.ImageReview.Usage
	if usage == nil || usage.TransparentField != "image_transparent" {
		t.Fatalf("image_review.usage = %+v", usage)
	}
	for _, name := range []string{"short_edge", "seamless", "alpha", "edge_fringe"} {
		if strings.TrimSpace(usage.Hints[name]) == "" {
			t.Fatalf("no hint for %q", name)
		}
	}
	if usage.Tiling.Segments != 8 || usage.Tiling.MaxSeamRatio != 2.5 || usage.Tiling.MinSeamDelta != 6 {
		t.Fatalf("tiling thresholds = %+v", usage.Tiling)
	}
}
