package core

import (
	"encoding/json"
	"slices"
	"strconv"
)

// Credits is the consumed_credits a task of a type reports, from the official
// Pricing page (https://docs.meshy.ai/en/api/pricing, read 2026-11-24) and the
// request's own options. It reports false for a type the page has no price for
// (the Creative Lab routes), whose polls keep the committed sample's value.
//
// What the real API showed (captured 2026-11-20 to 2026-11-23) and the price
// below agrees with: a Meshy 7.1 text-to-3d preview with 4k geometry 25, a
// refine with a 2k texture 10 and with an 8k texture 15, a 7.1 image-to-3d with
// 4k geometry and a 2k texture 35, a multi-image-to-3d with 2k geometry and a
// 2k texture 35, a retexture with an 8k texture 15, nano-banana-pro text and
// image generation 9, a remesh 5, a rigging 5, one animation 3, a resize 1 and
// a convert 1. What the page leaves open is constructed: the refine price of a
// Smart Topology preview (the page lists one refine price for every model), the
// price of an animation retargeted from a motion task (taken as one action),
// and a nano-banana request that Meshy runs on nano-banana-pro for a pose_mode
// (priced as the requested model).
func Credits(typ string, request map[string]any) (int64, bool) {
	switch typ {
	case "text-to-image":
		return imageCredits(request, false), true
	case "image-to-image":
		return imageCredits(request, true), true
	case "text-to-3d-preview":
		return meshCredits(request), true
	case "text-to-3d-refine":
		return textureCredits(request), true
	case "image-to-3d", "multi-image-to-3d":
		credits := meshCredits(request)
		if request["should_texture"] != false {
			credits += textureCredits(request)
		}
		return credits, true
	case "retexture":
		return textureCredits(request), true
	case "remesh", "uv-unwrap", "rig":
		return 5, true
	case "convert", "resize":
		return 1, true
	case "animate":
		if list, ok := request["action_ids"].([]any); ok && len(list) > 0 {
			return int64(3 * len(list)), true
		}
		return 3, true
	case "text-to-motion":
		if stringOf(request, "mode") == "swift" {
			return 3, true
		}
		return 10, true
	case "print-analyze":
		return 0, true
	case "print-multi-color":
		return 10, true
	}
	return 0, false
}

// imageCredits is the per-request image generation price: 3, 6, 3, 4 and 9 for
// the five nano-banana models, and 9 (text) or 12 (image) for a GPT Image model.
func imageCredits(request map[string]any, edit bool) int64 {
	switch stringOf(request, "ai_model") {
	case "nano-banana", "nano-banana-2-lite":
		return 3
	case "nano-banana-2":
		return 6
	case "nano-banana-2-1":
		return 4
	case "nano-banana-pro":
		return 9
	}
	if edit {
		return 12
	}
	return 9
}

// meshCredits is the mesh-only price of a generation model, plus the Ultra
// surcharge (5) of a 2k or 4k geometry_resolution; ultra_mode true is the
// deprecated 2k.
func meshCredits(request map[string]any) int64 {
	var credits int64 = 20
	switch effectiveModel(request) {
	case "meshy-6-lite", "meshy-t2", "meshy-t2.5":
		credits = 5
	}
	resolution := stringOf(request, "geometry_resolution")
	if slices.Contains([]string{"2k", "4k"}, resolution) || (resolution == "" && request["ultra_mode"] == true) {
		credits += 5
	}
	return credits
}

// textureCredits is the texture price of a refine, a retexture or a textured
// generation: 10 for a 2k or 4k texture and 15 for 8k (hd_texture true is the
// deprecated 4k). The page's texture-model overrides all price as the mesh plus
// this texture price, so the texture model does not change it.
func textureCredits(request map[string]any) int64 {
	if stringOf(request, "texture_resolution") == "8k" {
		return 15
	}
	return 10
}

// creditsNumber renders a price as the JSON number of a poll body.
func creditsNumber(credits int64) json.Number {
	return json.Number(strconv.FormatInt(credits, 10))
}
