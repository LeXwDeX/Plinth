package tripov3

import (
	"encoding/json"
	"strconv"
)

// What a task is billed (#305). The committed samples hold credits_consumed as a
// placeholder, so the prices below are from the paid run of 2026-11-16 and from
// the pricing page; the task kinds that are not listed report the sample's value
// as before.
//
//   - A rig check is free and a rig on v3.0-20260909 costs 25 credits (observed).
//   - A conversion costs 5 credits, 10 when any of quad, face_limit,
//     flatten_bottom, flatten_bottom_threshold, texture_size, texture_format,
//     pivot_to_center_bottom or scale_factor leaves the page's default (texture
//     size 4096, texture format JPEG, the rest off). 10 is observed, with PNG
//     textures of 2048 pixels; fbx_preset and bake are not among the options
//     that raise it.
//   - A decimation costs 35 credits, 40 for a quad mesh on v3.0 (the page; 35
//     observed for a triangle mesh).
//
// Every poll of a task reports the whole price as credits_consumed: the credits are
// frozen when the task is created, and the poll shows the frozen amount from the
// queued poll on (observed: the 3MF conversion (5) and the multiview model (70) of
// 2026-11-19, #386, and the queued polls of four conversions of 2026-11-24, 10 each,
// #577). A task that fails or is cancelled is not charged (the pricing page), so it
// reports 0. The account balance stays the fixed value of the fake: only the task
// object is billed.
const (
	creditsRig             = 25
	creditsConvertBasic    = 5
	creditsConvertAdvanced = 10
	creditsDecimate        = 35
	creditsDecimateQuad    = 40
)

// Defaults of the convert page whose change makes a conversion advanced.
const (
	convertDefaultTextureSize   = 4096
	convertDefaultTextureFormat = "JPEG"
)

// The rest of the pricing page (https://developers.tripo3d.ai/en/pricing, read
// 2026-11-24, #541), which Plinth's native_pricing follows for the generation tasks:
//
//   - generation, H series: base 10 credits for text and 20 for image and multiview;
//     a texture is +10 (standard, and fast at the same price), +10 more for the HD
//     texture, +20 more for the ultra (8K) one; geometry_quality=detailed is +20, quad
//     +5, smart low poly +10, generate parts +20. The 70 credits of the multiview run of
//     2026-11-19 (extreme texture, detailed geometry) are 20 + 10 + 20 + 20.
//   - generation, P2: 100 credits without a texture and 110, 120, 130 for the standard,
//     HD and ultra textures (changelog); quad adds nothing. The pricing page lists no P
//     Series price, so the P2 figures are the changelog's and P1 is not priced at all.
//   - texture 10 (fast and standard), 20 (HD), 30 (8K); segmentation 40; completion 50,
//     30 for quick_cap; Smart Segmentation 85 (image) or 55 (model), as captured;
//     retarget 10 per animation; image to multiview 10; edit multiview 5 per edited
//     image. Image to splat has no published price: 30 credits is what the real task
//     of 2026-11-24 reported from its first poll on (#577).
//
// The real round of 2026-11-24 (#577) reported every one of these for the requests it
// sent: text 10 (geometry only) and 60 (extreme PBR texture, detailed geometry), image
// and multiview 20, texture 30 (extreme), segmentation 40, completion 50 and quick_cap
// 30, Smart Segmentation of a model 55, rig 25 on v2.5 and on v3.0, retarget 10, image
// to multiview 10, edit multiview 5, decimation 35, conversion 5 and 10.
const (
	creditsGenerationText       = 10
	creditsGenerationImage      = 20
	creditsGenerationP2         = 100
	creditsTexture              = 10
	creditsTextureHD            = 10
	creditsTextureUltra         = 20
	creditsGeometryDetailed     = 20
	creditsGenerationQuad       = 5
	creditsGenerationSmall      = 10
	creditsGenerationParts      = 20
	creditsSegment              = 40
	creditsComplete             = 50
	creditsQuickCap             = 30
	creditsSmartSegmentImage    = 85
	creditsSmartSegmentModel    = 55
	creditsRetargetPerAnimation = 10
	creditsImageToMultiview     = 10
	creditsEditPerView          = 5
	creditsImageToSplat         = 30
)

// generationPrice prices a text, image or multiview generation request; false for the
// P1 model, whose price the pages do not list.
func (t *task) generationPrice() (float64, bool) {
	model, _ := t.request["model"].(string)
	var total float64
	switch {
	case model == "P1-20260311":
		return 0, false
	case t.isPSeries():
		total = creditsGenerationP2
	case t.kind.Type == "text_to_model":
		total = creditsGenerationText
	default:
		total = creditsGenerationImage
	}
	// Plinth's native_pricing counts the texture when texture is on; pbr=true forces it on.
	if flag(t.request, "texture", true) || flag(t.request, "pbr", false) {
		total += creditsTexture
		switch t.request["texture_quality"] {
		case "detailed":
			total += creditsTextureHD
		case "extreme":
			total += creditsTextureUltra
		}
	}
	if t.isPSeries() {
		return total, true
	}
	if t.request["geometry_quality"] == "detailed" {
		total += creditsGeometryDetailed
	}
	if flag(t.request, "quad", false) {
		total += creditsGenerationQuad
	}
	if flag(t.request, "smart_low_poly", false) {
		total += creditsGenerationSmall
	}
	if flag(t.request, "generate_parts", false) {
		total += creditsGenerationParts
	}
	return total, true
}

// retargetAnimations is how many animations a retarget request names.
func (t *task) retargetAnimations() int {
	if many, ok := t.request["animations"].([]any); ok {
		return len(many)
	}
	return 1
}

// price is what the task costs, and whether the fake prices its kind.
func (t *task) price() (float64, bool) {
	switch t.kind.Type {
	case "text_to_model", "image_to_model", "multiview_to_model":
		return t.generationPrice()
	case "image_to_multiview":
		return creditsImageToMultiview, true
	case "image_to_splat":
		return creditsImageToSplat, true
	case "edit_multiview":
		return float64(creditsEditPerView * len(t.views)), true
	case "texture_model":
		switch t.request["texture_quality"] {
		case "detailed":
			return creditsTexture + creditsTextureHD, true
		case "extreme":
			return creditsTexture + creditsTextureUltra, true
		}
		return creditsTexture, true
	case "mesh_segmentation":
		return creditsSegment, true
	case "mesh_completion":
		if t.request["completion_mode"] == "quick_cap" {
			return creditsQuickCap, true
		}
		return creditsComplete, true
	case "smartsegment_image":
		// The kind reports smartsegment_image for both pipelines; the task's own type
		// follows seg_type.
		if t.typ == "smartsegment_model" {
			return creditsSmartSegmentModel, true
		}
		return creditsSmartSegmentImage, true
	case "animate_retarget":
		return float64(creditsRetargetPerAnimation * t.retargetAnimations()), true
	case "animate_prerigcheck":
		return 0, true
	case "animate_rig":
		return creditsRig, true
	case "convert_model":
		if convertAdvanced(t.convert) {
			return creditsConvertAdvanced, true
		}
		return creditsConvertBasic, true
	case "highpoly_to_lowpoly":
		if t.decimatesToQuad() && stringOr(t.request["model"], "") == "v3.0" {
			return creditsDecimateQuad, true
		}
		return creditsDecimate, true
	}
	return 0, false
}

// convertAdvanced reports whether a convert request is billed as an advanced
// conversion.
func convertAdvanced(options map[string]any) bool {
	for _, name := range []string{"quad", "flatten_bottom", "pivot_to_center_bottom"} {
		if on, _ := options[name].(bool); on {
			return true
		}
	}
	for _, name := range []string{"face_limit", "flatten_bottom_threshold"} {
		if _, present := options[name]; present {
			return true
		}
	}
	if size, present := options["texture_size"].(json.Number); present {
		if edge, err := size.Int64(); err != nil || edge != convertDefaultTextureSize {
			return true
		}
	}
	if format, present := options["texture_format"].(string); present && format != convertDefaultTextureFormat {
		return true
	}
	if scale, present := options["scale_factor"].(json.Number); present {
		if factor, err := scale.Float64(); err != nil || factor != 1 {
			return true
		}
	}
	return false
}

// bill writes credits_consumed into a poll body: the frozen price from the queued
// poll on and when the task succeeds, nothing for a task that ended without a result.
func (t *task) bill(data map[string]any, status string) {
	price, priced := t.price()
	if _, shown := data["credits_consumed"]; !priced || !shown {
		return
	}
	switch status {
	case statusQueued, statusRunning, statusSuccess:
		data["credits_consumed"] = json.Number(strconv.FormatFloat(price, 'f', -1, 64))
	case statusFailed, statusCancelled, statusUnknown:
		data["credits_consumed"] = json.Number("0")
	}
}
