package core

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Request validation of the Meshy create routes, from the official pages read
// on 2026-11-24 (https://docs.meshy.ai/en/api/text-to-3d, /image-to-3d,
// /multi-image-to-3d, /retexture, /text-to-image, /image-to-image,
// /text-to-motion and /animation). Every rule below that a page states as a 400
// is answered with 400; the refusal text is the real {message} envelope with a
// constructed message, because none of these refusals has been requested from
// the real API. Rules the pages leave open are not invented: the cases where a
// page lists a value set but never says what an unlisted value does (the
// retexture ai_model, for one) are marked constructed on their own comment.
//
// Several rules exist only to catch a platform Adapter or Catalog that sends a
// combination the upstream would refuse; when the fake rejects a request the
// Adapter really builds, that is a finding about the Catalog or the Adapter,
// never a reason to loosen the fake.

// ai_model values of each endpoint family.
var (
	standardModels = []string{"meshy-6-lite", "meshy-6", "meshy-7.1", "latest", "meshy-7"}
	smartModels    = []string{"meshy-t2", "meshy-t2.5"}
	// textureModels are the retexture ai_model values (and the text-to-3d
	// refine texture models).
	retextureModels = []string{"meshy-6-lite", "meshy-6", "meshy-7", "meshy-n1", "latest"}
	// geometry2KModels and geometry4KModels are the ai_model values that run
	// 2k and 4k geometry (Text to 3D and Image to 3D pages).
	geometry2KModels = []string{"meshy-7.1", "latest", "meshy-7"}
	geometry4KModels = []string{"meshy-7.1", "latest"}
	targetFormats    = []string{"glb", "obj", "fbx", "stl", "usdz", "3mf"}
	resolutions      = []string{"2k", "4k", "8k"}
	imageModels      = []string{
		"nano-banana", "nano-banana-2", "nano-banana-2-lite", "nano-banana-2-1", "nano-banana-pro",
		"gpt-image-2", "gpt-image-2-5-flare", "gpt-image-2-5-sunburst", "gpt-image-2-6-flare", "gpt-image-2-6-sunburst",
	}
	aspectRatios = []string{"1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3"}
)

const maxPromptRunes = 800

func isNanoBanana(model string) bool { return strings.HasPrefix(model, "nano-banana") }

// stringOf returns a request field as a string.
func stringOf(request map[string]any, field string) string {
	text, _ := request[field].(string)
	return text
}

// enumError refuses a present field that is not one of the allowed strings.
func enumError(request map[string]any, field string, allowed []string) string {
	value, present := request[field]
	if !present {
		return ""
	}
	if text, ok := value.(string); ok && slices.Contains(allowed, text) {
		return ""
	}
	return field + " must be one of " + strings.Join(allowed, ", ")
}

// boolError refuses a present field that is not a boolean.
func boolError(request map[string]any, fields ...string) string {
	for _, field := range fields {
		if value, present := request[field]; present {
			if _, ok := value.(bool); !ok {
				return field + " must be a boolean"
			}
		}
	}
	return ""
}

// lengthError refuses a present text field over the documented character limit.
func lengthError(request map[string]any, field string, limit int) string {
	value, present := request[field]
	if !present {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return field + " must be a string"
	}
	if utf8.RuneCountInString(text) > limit {
		return field + " must not exceed " + strconv.Itoa(limit) + " characters"
	}
	return ""
}

// listError refuses a present array field that is not made of strings or whose
// length is outside [minimum, maximum]. A request that leaves the field out is
// the Need groups' business.
func listError(request map[string]any, field string, minimum, maximum int) string {
	value, present := request[field]
	if !present {
		return ""
	}
	list, ok := value.([]any)
	if !ok {
		return field + " must be an array"
	}
	if len(list) < minimum || len(list) > maximum {
		return field + " must hold " + strconv.Itoa(minimum) + " to " + strconv.Itoa(maximum) + " items"
	}
	for _, item := range list {
		if text, ok := item.(string); !ok || strings.TrimSpace(text) == "" {
			return field + " must hold non-empty strings"
		}
	}
	return ""
}

// targetFormatsError checks target_formats against the six documented formats
// (Text to 3D, Image to 3D, Multi Image to 3D and Retexture pages).
func targetFormatsError(request map[string]any) string {
	value, present := request["target_formats"]
	if !present {
		return ""
	}
	list, ok := value.([]any)
	if !ok {
		return "target_formats must be an array"
	}
	for _, item := range list {
		if text, ok := item.(string); !ok || !slices.Contains(targetFormats, text) {
			return "target_formats must only list " + strings.Join(targetFormats, ", ")
		}
	}
	return ""
}

// effectiveModel is the generation model a request runs on: its ai_model, else
// the Smart Topology default when model_type asks for it, else latest (Meshy
// 7.1), as the Text to 3D and Image to 3D pages document the defaults.
func effectiveModel(request map[string]any) string {
	if model := stringOf(request, "ai_model"); model != "" {
		return model
	}
	if stringOf(request, "model_type") == "smart-topology" {
		return defaultSmartTopologyModel
	}
	return "latest"
}

// geometryResolutionError is the documented geometry_resolution rule: the
// value is standard, 2k or 4k; 2k needs meshy-7.1, latest or meshy-7, and 4k
// needs meshy-7.1 or latest; "Other models return 400". Smart Topology models
// run neither.
func geometryResolutionError(request map[string]any) string {
	if message := enumError(request, "geometry_resolution", []string{"standard", "2k", "4k"}); message != "" {
		return message
	}
	switch stringOf(request, "geometry_resolution") {
	case "2k":
		if model := effectiveModel(request); !slices.Contains(geometry2KModels, model) {
			return "geometry_resolution 2k needs ai_model meshy-7.1, latest or meshy-7, not " + model
		}
	case "4k":
		if model := effectiveModel(request); !slices.Contains(geometry4KModels, model) {
			return "geometry_resolution 4k needs ai_model meshy-7.1 or latest, not " + model
		}
	}
	return ""
}

// textTo3DCommonError is what a preview and a refine of the Text to 3D page
// share: the target formats and the boolean flags.
func textTo3DCommonError(request map[string]any) string {
	if message := targetFormatsError(request); message != "" {
		return message
	}
	if message := boolError(request, "enable_pbr", "auto_size", "alpha_thumbnail", "moderation", "ultra_mode", "hd_texture", "remove_lighting"); message != "" {
		return message
	}
	return enumError(request, "origin_at", []string{"bottom", "center"})
}

// textTo3DPreviewError is the preview create validation of the Text to 3D page
// (https://docs.meshy.ai/en/api/text-to-3d, read 2026-11-24): prompt is
// required and at most 800 characters, ai_model is one of the standard or
// Smart Topology models, and geometry_resolution follows the model.
func textTo3DPreviewError(request map[string]any) string {
	prompt, _ := request["prompt"].(string)
	if strings.TrimSpace(prompt) == "" {
		return "prompt is required for a preview task"
	}
	if message := lengthError(request, "prompt", maxPromptRunes); message != "" {
		return message
	}
	if message := enumError(request, "model_type", []string{"standard", "smart-topology"}); message != "" {
		return message
	}
	if message := enumError(request, "ai_model", append(slices.Clone(standardModels), smartModels...)); message != "" {
		return message
	}
	return geometryResolutionError(request)
}

// textTo3DRefineError is the refine create validation of the same page:
// preview_task_id is required; texture_resolution is 2k, 4k or 8k; the texture
// prompt is at most 800 characters. The refine ai_model is a texture model
// whose value set the page lists as meshy-7, latest, meshy-6, meshy-6-lite and
// meshy-n1, yet a real refine of 2026-11-20 sent meshy-7.1 and was accepted
// (202), so any generation model name passes here and the Adapter's own choice
// to send none (the field is "inherited from preview") is pinned by its test,
// not by this fake.
func textTo3DRefineError(request map[string]any) string {
	if id, _ := request["preview_task_id"].(string); strings.TrimSpace(id) == "" {
		return "preview_task_id is required for a refine task"
	}
	if message := enumError(request, "texture_resolution", resolutions); message != "" {
		return message
	}
	if message := lengthError(request, "texture_prompt", maxPromptRunes); message != "" {
		return message
	}
	known := append(append(slices.Clone(standardModels), smartModels...), "meshy-n1")
	if message := enumError(request, "ai_model", known); message != "" {
		return message
	}
	return refineTextureError(request)
}

// refineHighResolutionModels are the refine ai_model values that make a 4K or
// 8K texture: the page's "4k and 8k need ai_model meshy-6, meshy-7, or latest",
// plus meshy-7.1, which the page does not list as a refine model but a real
// refine of 2026-11-20 sent and had accepted; what it does at 4k or 8k is
// unknown, so the fake does not refuse it.
var refineHighResolutionModels = []string{"meshy-6", "meshy-7", "latest", "meshy-7.1"}

// refineTextureError holds the refine texture conflicts of the Text to 3D page
// (read 2026-11-25), each stated as a 400 there: texture_resolution 4k or 8k
// (or hd_texture true, which is 4k) with an explicit ai_model other than the
// models above; with meshy-n1, enable_pbr true, a texture_prompt or
// remove_lighting true; and remove_lighting with meshy-6-lite. A refine without
// ai_model textures with the preview's model and is not judged (the Adapter
// sends none, and a real 8k refine without it was accepted on 2026-11-22). The
// refusal texts are constructed.
func refineTextureError(request map[string]any) string {
	model := stringOf(request, "ai_model")
	if model == "" {
		return ""
	}
	resolution := stringOf(request, "texture_resolution")
	if resolution == "" {
		if hd, _ := request["hd_texture"].(bool); hd {
			resolution = "4k"
		}
	}
	if (resolution == "4k" || resolution == "8k") && !slices.Contains(refineHighResolutionModels, model) {
		return "texture_resolution " + resolution + " needs ai_model meshy-6, meshy-7 or latest, not " + model
	}
	switch model {
	case "meshy-n1":
		if request["enable_pbr"] == true {
			return "enable_pbr cannot be true with ai_model meshy-n1"
		}
		if has(request, "texture_prompt") {
			return "texture_prompt cannot be sent with ai_model meshy-n1"
		}
		if request["remove_lighting"] == true {
			return "remove_lighting cannot be true with ai_model meshy-n1"
		}
	case "meshy-6-lite":
		if _, present := request["remove_lighting"]; present {
			return "remove_lighting cannot be sent with ai_model meshy-6-lite"
		}
	}
	return ""
}

// textureModelValues are the texture_model values of the Image to 3D and Multi
// Image to 3D pages.
var textureModelValues = []string{"meshy-7", "meshy-n1", "meshy-6-lite", "meshy-6"}

// textureResolutionNotSupported reports the pricing page's "NS" cells: a 4K or
// 8K texture on a texture model that cannot make one (meshy-6-lite and
// meshy-n1; the default texture model of a meshy-6-lite model is meshy-6-lite).
// Constructed from the pricing table; "NS" is read as a 400.
func textureResolutionNotSupported(request map[string]any) string {
	resolution := stringOf(request, "texture_resolution")
	if resolution != "4k" && resolution != "8k" {
		if hd, _ := request["hd_texture"].(bool); !hd {
			return ""
		}
	}
	texture := stringOf(request, "texture_model")
	if texture == "" && stringOf(request, "ai_model") == "meshy-6-lite" {
		texture = "meshy-6-lite"
	}
	if texture == "meshy-6-lite" || texture == "meshy-n1" {
		return "texture_resolution 4k and 8k are not supported by the " + texture + " texture model"
	}
	return ""
}

// ImageTo3DRequestError is the create validation of Image to 3D and (multi
// true) Multi Image to 3D, on top of the remesh and image checks the routes
// already ran. Pages: https://docs.meshy.ai/en/api/image-to-3d and
// /multi-image-to-3d, read 2026-11-24; the credit page for the texture-model
// cells.
func ImageTo3DRequestError(request map[string]any, multi bool) string {
	if message := targetFormatsError(request); message != "" {
		return message
	}
	if message := boolError(request, "should_texture", "enable_pbr", "auto_size", "alpha_thumbnail", "moderation",
		"ultra_mode", "hd_texture", "remove_lighting", "image_enhancement", "save_pre_remeshed_model"); message != "" {
		return message
	}
	models := slices.Clone(standardModels)
	if !multi {
		models = append(models, smartModels...)
	}
	if message := enumError(request, "ai_model", models); message != "" {
		return message
	}
	if !multi {
		if message := enumError(request, "model_type", []string{"standard", "smart-topology"}); message != "" {
			return message
		}
	}
	if message := geometryResolutionError(request); message != "" {
		return message
	}
	if multi && stringOf(request, "geometry_resolution") == "4k" {
		return "geometry_resolution 4k is not supported on this endpoint"
	}
	if message := enumError(request, "texture_resolution", resolutions); message != "" {
		return message
	}
	if message := lengthError(request, "texture_prompt", maxPromptRunes); message != "" {
		return message
	}
	// An unknown texture_model is refused even when should_texture is false.
	allowedTexture := textureModelValues
	if multi {
		allowedTexture = []string{"meshy-7", "meshy-n1"}
	}
	if message := enumError(request, "texture_model", allowedTexture); message != "" {
		return message
	}
	textured := request["should_texture"] != false
	// Single image only: "true needs should_texture: true, and returns 400 with
	// should_texture: false" (Image to 3D page, read 2026-11-25). The message is
	// the real answer of the #581 probe (2026-11-25, no credits spent). The Multi
	// Image to 3D page states no such rule, so the fake accepts the pair there.
	if !multi && !textured && request["enable_pbr"] == true {
		return "enable_pbr is only supported when should_texture is true"
	}
	texture := stringOf(request, "texture_model")
	if textured {
		if texture == "meshy-6" && stringOf(request, "ai_model") != "meshy-6" {
			return "texture_model meshy-6 is only accepted with ai_model meshy-6"
		}
		if message := textureResolutionNotSupported(request); message != "" {
			return message
		}
		if request["enable_pbr"] == true && texture == "meshy-n1" {
			return "enable_pbr cannot be true with texture_model meshy-n1"
		}
	}
	if message := textureOptionError(request, texture, !multi); message != "" {
		return message
	}
	if multi {
		return multiImageError(request, textured)
	}
	// surface_mode is a single-image option: "Accepted values are organic and
	// hard. Any other value returns 400." (Image to 3D page, read 2026-11-25).
	return enumError(request, "surface_mode", []string{"organic", "hard"})
}

// smartTopologyRequest reports a request that runs a Smart Topology model
// (model_type smart-topology, or a meshy-t2 / meshy-t2.5 ai_model).
func smartTopologyRequest(request map[string]any) bool {
	return stringOf(request, "model_type") == "smart-topology" || slices.Contains(smartModels, stringOf(request, "ai_model"))
}

// textureOptionError holds the texture-option conflicts the Image to 3D and
// Multi Image to 3D pages (read 2026-11-25) both state as a 400:
// image_enhancement with meshy-6-lite (and, single image only, with Smart
// Topology); with texture_model meshy-n1, a texture_prompt, remove_lighting
// true, or topology quad on a remeshed model ("which meshy-6-lite does by
// default"); and remove_lighting with meshy-6-lite and its default texture model
// (or, single image only, with Smart Topology). The refusal texts are
// constructed. enable_pbr with should_texture false is checked in
// ImageTo3DRequestError (#581).
func textureOptionError(request map[string]any, texture string, single bool) string {
	model := stringOf(request, "ai_model")
	smart := single && smartTopologyRequest(request)
	if has(request, "image_enhancement") {
		if model == "meshy-6-lite" {
			return "image_enhancement is not supported with ai_model meshy-6-lite"
		}
		if smart {
			return "image_enhancement is not supported with model_type smart-topology"
		}
	}
	if texture == "meshy-n1" {
		if has(request, "texture_prompt") {
			return "texture_prompt cannot be sent with texture_model meshy-n1"
		}
		if request["remove_lighting"] == true {
			return "remove_lighting cannot be true with texture_model meshy-n1"
		}
		remeshed := request["should_remesh"] == true || (model == "meshy-6-lite" && request["should_remesh"] != false)
		if stringOf(request, "topology") == "quad" && remeshed {
			return "topology quad is not supported with texture_model meshy-n1 on a remeshed model"
		}
	}
	if _, present := request["remove_lighting"]; present {
		if smart {
			return "remove_lighting is not supported with model_type smart-topology"
		}
		if model == "meshy-6-lite" && texture == "" {
			return "remove_lighting is not supported with ai_model meshy-6-lite and its default texture model"
		}
	}
	return ""
}

// multiImageError is what only the Multi Image to 3D page states.
func multiImageError(request map[string]any, textured bool) string {
	if message := listError(request, "image_urls", 1, 4); message != "" {
		return message
	}
	if message := listError(request, "texture_image_urls", 1, 4); message != "" {
		return message
	}
	texture := stringOf(request, "texture_model")
	if has(request, "texture_image_urls") {
		for _, conflict := range []string{"texture_image_url", "texture_prompt"} {
			if has(request, conflict) {
				return "texture_image_urls cannot be sent with " + conflict
			}
		}
		if !textured {
			return "texture_image_urls cannot be sent with should_texture: false"
		}
		// "Requires ai_model of meshy-7.1, latest or meshy-7, or texture_model of
		// meshy-7 or meshy-n1": the default model is latest.
		if effective := effectiveModel(request); !slices.Contains(geometry2KModels, effective) && texture != "meshy-7" && texture != "meshy-n1" {
			return "texture_image_urls needs ai_model meshy-7.1, latest or meshy-7, or texture_model meshy-7 or meshy-n1"
		}
	}
	return ""
}

// RetextureRequestError is the create validation of the Retexture page
// (https://docs.meshy.ai/en/api/retexture, read 2026-11-24): exactly one style
// input group, the 800-character prompt, and the texture-model conflicts. The
// ai_model enum is the page's list (meshy-6-lite, meshy-6, meshy-7, meshy-n1,
// latest, where latest is Meshy 7). The page never says what an unlisted value
// such as meshy-7.1 does; the fake answers 400 (constructed), and a probe of
// the real API settles it.
func RetextureRequestError(request map[string]any) string {
	if message := enumError(request, "ai_model", retextureModels); message != "" {
		return message
	}
	if message := targetFormatsError(request); message != "" {
		return message
	}
	if message := boolError(request, "enable_pbr", "enable_original_uv", "hd_texture", "remove_lighting", "alpha_thumbnail"); message != "" {
		return message
	}
	if message := enumError(request, "texture_resolution", resolutions); message != "" {
		return message
	}
	if message := lengthError(request, "text_style_prompt", maxPromptRunes); message != "" {
		return message
	}
	multiview := has(request, "multiview_image_urls")
	if multiview {
		if has(request, "text_style_prompt") || has(request, "image_style_url") {
			return "multiview_image_urls cannot be sent with text_style_prompt or image_style_url"
		}
		if message := listError(request, "multiview_image_urls", 1, 4); message != "" {
			return message
		}
		if model := stringOf(request, "ai_model"); model != "meshy-7" && model != "meshy-n1" {
			return "multiview_image_urls needs an explicit ai_model of meshy-7 or meshy-n1"
		}
	} else if value, present := request["multiview_image_urls"]; present {
		// An empty array (has is false for it) or a wrong type.
		if _, isList := value.([]any); !isList {
			return "multiview_image_urls must be an array"
		}
		return "multiview_image_urls must hold 1 to 4 items"
	}
	model := stringOf(request, "ai_model")
	switch model {
	case "meshy-n1":
		if request["enable_pbr"] == true {
			return "enable_pbr cannot be true with ai_model meshy-n1"
		}
		if _, present := request["remove_lighting"]; present {
			return "remove_lighting cannot be sent with ai_model meshy-n1"
		}
		if resolution := stringOf(request, "texture_resolution"); resolution == "4k" || resolution == "8k" {
			return "texture_resolution " + resolution + " is not supported with ai_model meshy-n1"
		}
		if hd, _ := request["hd_texture"].(bool); hd && !has(request, "texture_resolution") {
			return "hd_texture cannot be true with ai_model meshy-n1"
		}
		if has(request, "text_style_prompt") && !has(request, "image_style_url") && !multiview {
			return "ai_model meshy-n1 needs image_style_url, not text_style_prompt"
		}
	case "meshy-6-lite":
		if _, present := request["remove_lighting"]; present {
			return "remove_lighting cannot be sent with ai_model meshy-6-lite"
		}
		if resolution := stringOf(request, "texture_resolution"); resolution == "4k" || resolution == "8k" {
			return "texture_resolution " + resolution + " is not supported with ai_model meshy-6-lite"
		}
	}
	return ""
}

// TextToImageRequestError is the create validation of the Text to Image page
// (https://docs.meshy.ai/en/api/text-to-image, read 2026-11-24): ai_model and
// prompt are required; aspect_ratio is one of seven and the two 3:2 values are
// for the GPT Image models only; aspect_ratio and generate_multi_view conflict.
// ImageToImageRequestError adds the reference images.
func TextToImageRequestError(request map[string]any) string {
	model := stringOf(request, "ai_model")
	if strings.TrimSpace(model) == "" {
		return "ai_model is required"
	}
	if message := enumError(request, "ai_model", imageModels); message != "" {
		return message
	}
	if strings.TrimSpace(stringOf(request, "prompt")) == "" {
		return "prompt is required"
	}
	if message := boolError(request, "generate_multi_view", "remove_background"); message != "" {
		return message
	}
	if message := enumError(request, "pose_mode", []string{"a-pose", "t-pose"}); message != "" {
		return message
	}
	if message := enumError(request, "aspect_ratio", aspectRatios); message != "" {
		return message
	}
	ratio := stringOf(request, "aspect_ratio")
	if has(request, "aspect_ratio") && request["generate_multi_view"] == true {
		return "aspect_ratio cannot be sent with generate_multi_view: true"
	}
	if (ratio == "3:2" || ratio == "2:3") && isNanoBanana(model) {
		return "aspect_ratio " + ratio + " is not supported with " + model
	}
	return ""
}

// ImageToImageRequestError is the create validation of the Image to Image page
// (https://docs.meshy.ai/en/api/image-to-image, read 2026-11-24): the Text to
// Image rules, with 1 to 5 reference_image_urls unless input_task_id is sent
// (which wins when both are).
func ImageToImageRequestError(request map[string]any) string {
	if message := TextToImageRequestError(request); message != "" {
		return message
	}
	if has(request, "input_task_id") {
		return ""
	}
	if value, present := request["reference_image_urls"]; present {
		if list, ok := value.([]any); ok && len(list) == 0 {
			return "reference_image_urls must hold 1 to 5 items"
		}
	}
	return listError(request, "reference_image_urls", 1, 5)
}

// TextToMotionRequestError is the create validation of the Text to Motion page
// (https://docs.meshy.ai/en/api/text-to-motion, read 2026-11-24): prompt up to
// 400 characters, mode prime or swift, duration 2 to 10 seconds in half steps.
func TextToMotionRequestError(request map[string]any) string {
	if strings.TrimSpace(stringOf(request, "prompt")) == "" {
		return "prompt is required"
	}
	if message := lengthError(request, "prompt", 400); message != "" {
		return message
	}
	if message := enumError(request, "mode", []string{"prime", "swift"}); message != "" {
		return message
	}
	number, ok := request["duration"].(json.Number)
	if !ok {
		return "duration must be a number from 2 to 10 seconds in 0.5 steps"
	}
	seconds, err := number.Float64()
	if err != nil || seconds < 2 || seconds > 10 || seconds*2 != float64(int64(seconds*2)) {
		return "duration must be a number from 2 to 10 seconds in 0.5 steps"
	}
	return ""
}

// AnimationRequestError is the one-of rule of the Animation page
// (https://docs.meshy.ai/en/api/animation, read 2026-11-24): exactly one of
// action_id, action_ids and motion_task_id; action_ids holds at most ten
// distinct integers; post_process.operation_type is change_fps, fbx2usdz or
// extract_armature.
func AnimationRequestError(request map[string]any) string {
	chosen := 0
	for _, field := range []string{"action_id", "action_ids", "motion_task_id"} {
		if has(request, field) {
			chosen++
		}
	}
	if chosen != 1 {
		return "send exactly one of action_id, action_ids or motion_task_id"
	}
	if list, ok := request["action_ids"].([]any); ok {
		if len(list) > 10 {
			return "action_ids must hold at most 10 actions"
		}
		seen := map[string]bool{}
		for _, item := range list {
			id, isNumber := item.(json.Number)
			if !isNumber {
				return "action_ids must hold integers"
			}
			if seen[id.String()] {
				return "action_ids must not repeat an action"
			}
			seen[id.String()] = true
		}
	}
	if process, ok := request["post_process"].(map[string]any); ok {
		if message := enumError(process, "operation_type", []string{"change_fps", "fbx2usdz", "extract_armature"}); message != "" {
			return "post_process." + message
		}
		// "The frame rate to change to, one of 24, 25, 30, or 60 ... Any other
		// value returns 400" (read 2026-11-25). The page says fps "applies only
		// when operation_type is change_fps", so beside another operation it is
		// ignored, not refused; its value is still checked (constructed).
		if value, present := process["fps"]; present {
			number, isNumber := value.(json.Number)
			if !isNumber || !slices.Contains([]string{"24", "25", "30", "60"}, number.String()) {
				return "post_process.fps must be one of 24, 25, 30, 60"
			}
		}
	}
	return ""
}

// AnimationSourceError is the Animation page's "Unsupported rig" 400 (read
// 2026-11-25): "motion_task_id needs a biped rig, and Meshy rejects a quadruped
// rig." The rig's kind is the animation_type of the rigging request behind
// rig_task_id (default biped). Whether an action_id's rig_type matches the rig
// needs the animation library, which the fake does not hold, so that 400 is not
// modelled. The refusal text is constructed.
func AnimationSourceError(request map[string]any, field string, source map[string]any) string {
	if field == "rig_task_id" && has(request, "motion_task_id") && stringOf(source, "animation_type") == "quadruped" {
		return "motion_task_id needs a biped rig; the rig_task_id task is a quadruped rig"
	}
	return ""
}

// remeshTargetFormats are the target_formats of the Remesh page, which also
// lists blend (read 2026-11-25); the generation pages list the six above.
var remeshTargetFormats = append(slices.Clone(targetFormats), "blend")

// RemeshRequestError is the create validation of the Remesh page
// (https://docs.meshy.ai/en/api/remesh, read 2026-11-25), each rule stated as a
// 400 there: target_formats from its seven formats; topology quad or triangle
// ("Invalid topology"); target_polycount from 100 to 300000 ("A value outside
// the range returns 400"); more than one of resize_height, resize_longest_side
// and auto_size ("Mutually exclusive parameters"); and, with
// convert_format_only true, an empty or null target_formats. auto_size counts
// as sent only when true and the two sizes when present, so a body naming
// auto_size false beside a size is not refused (the page does not say how it
// reads an explicit false). The refusal texts are constructed.
func RemeshRequestError(request map[string]any) string {
	if value, present := request["target_formats"]; present && value != nil {
		list, ok := value.([]any)
		if !ok {
			return "target_formats must be an array"
		}
		for _, item := range list {
			if text, ok := item.(string); !ok || !slices.Contains(remeshTargetFormats, text) {
				return "target_formats must only list " + strings.Join(remeshTargetFormats, ", ")
			}
		}
	}
	if message := enumError(request, "topology", []string{"quad", "triangle"}); message != "" {
		return message
	}
	if value, present := request["target_polycount"]; present {
		count, ok := integerOf(value)
		if !ok || count < targetFloor || count > standardTargetCeiling {
			return "target_polycount must be an integer between " + strconv.Itoa(targetFloor) + " and " + strconv.Itoa(standardTargetCeiling)
		}
	}
	if message := boolError(request, "auto_size", "convert_format_only"); message != "" {
		return message
	}
	modes := 0
	for _, field := range []string{"resize_height", "resize_longest_side"} {
		if _, present := request[field]; present {
			modes++
		}
	}
	if request["auto_size"] == true {
		modes++
	}
	if modes > 1 {
		return "send at most one of resize_height, resize_longest_side and auto_size"
	}
	if request["convert_format_only"] == true {
		if list, _ := request["target_formats"].([]any); len(list) == 0 {
			if _, present := request["target_formats"]; present {
				return "target_formats must list at least one format when convert_format_only is true"
			}
		}
	}
	return ""
}
