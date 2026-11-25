package tripov3

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// The request and source-task contracts of the processing endpoints, read from the
// official pages on processingDocsChecked (#541):
//
//   - https://developers.tripo3d.ai/en/docs/models-texture (model, texture_quality,
//     texture_prompt: text, image and images are mutually exclusive, style_image goes
//     with text; fast needs the v3.5 texture model and is 1004 with v2.5)
//   - https://developers.tripo3d.ai/en/docs/mesh-segment (model v1.0-20250506 or
//     v2.0-20260430; segmentation_granularity simple, balanced or detailed;
//     split_by_connectivity; ref_image)
//   - https://developers.tripo3d.ai/en/docs/mesh-complete (input is the task id of a
//     mesh/segment task; model v1.0-20250506; completion_mode ai_completion or
//     quick_cap; part_names)
//   - https://developers.tripo3d.ai/en/docs/mesh-smartsegment (seg_type image or model;
//     granularity coarse, medium or fine; hint; transform of 16 floats for the model
//     pipeline; a task id input has to have succeeded, 2007, and to hold a usable
//     picture or GLB, 2006)
//   - https://developers.tripo3d.ai/en/docs/generation-edit-multiview (input is the task
//     id of a successful Image to Multiview task; one to four prompts, each for a
//     distinct view, each a non-empty text of at most 1024 characters)
//   - https://developers.tripo3d.ai/en/docs/animations-rig-check and .../animations-rig
//     (input is a text, image or multiview generation task, a file token or a URL)
//   - https://developers.tripo3d.ai/en/docs/animations-retarget (input is "the rigging task
//     itself, or a later task run on the rigged model")
//   - https://developers.tripo3d.ai/en/docs/models-convert (input is a text, image or
//     multiview generation task, a file token or a URL)
//
// A refusal is the real create envelope (400/code 1004), except the 2006 and 2007 of
// Smart Segmentation, which are the codes that page names; their HTTP status (400, like
// every other 2xxx code of the error page except 2001 and 2010) and message text are
// constructed. The pages state no refusal behaviour for most of these values, so every
// one of them is constructed from the page's list, not observed; the message of each
// refusal is the fake's own.
//
// Retarget and later tasks: the retarget page names texture, smart UV and segmentation
// as examples of a later task run on the rigged model. The fake follows the chain of
// source tasks (a texture, a decimation, a segmentation or a completion of a rig) back
// to the rig; a smart UV is a decimation request here. The texture and segmentation
// pages themselves list only generation tasks as sources, which conflicts with that
// sentence; the fake accepts any task for those two endpoints rather than guess.
const processingDocsChecked = "2026-11-24"

var (
	segmentModels      = []string{"v1.0-20250506", "v2.0-20260430"}
	segmentGranularity = []string{"simple", "balanced", "detailed"}
	completeModels     = []string{"v1.0-20250506"}
	completionModes    = []string{"ai_completion", "quick_cap"}
	smartSegmentLevels = []string{"coarse", "medium", "fine"}
	textureModels      = []string{"v3.5-20260815", "v3.0-20250812", textureV25}
	editMultiviewViews = []string{"front", "left", "back", "right"}
)

// editMultiviewPromptLimit is the length of one edit instruction, in characters.
const editMultiviewPromptLimit = 1024

// problemMessage turns an optional refusal into the message the older string-based
// checks return ("" for none).
func problemMessage(problems ...*refusal) string {
	for _, problem := range problems {
		if problem != nil {
			return problem.message
		}
	}
	return ""
}

// stringList reports whether a field is an array of strings.
func stringList(body map[string]any, name string) *refusal {
	raw, present := body[name]
	if !present {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return invalid("%s must be an array of strings", name)
	}
	for _, item := range items {
		if _, isText := item.(string); !isText {
			return invalid("%s must be an array of strings", name)
		}
	}
	return nil
}

// textureRequestError checks a POST /models/texture body.
func textureRequestError(body map[string]any) string {
	if problem := problemMessage(
		optionalChoice(body, "model", textureModels),
		optionalChoice(body, "texture_quality", textureQualities),
		optionalChoice(body, "texture_alignment", textureAlignments),
		optionalChoice(body, "compress", compressModes),
		optionalBool(body, "pbr"), optionalBool(body, "bake"), optionalBool(body, "delight"),
		stringList(body, "part_names"),
	); problem != "" {
		return problem
	}
	if quality, _ := body["texture_quality"].(string); quality == textureQualityFast {
		if model, _ := body["model"].(string); model == textureV25 {
			return "texture_quality fast requires the v3.5 texture model"
		}
	}
	raw, present := body["texture_prompt"]
	if !present {
		return ""
	}
	prompt, ok := raw.(map[string]any)
	if !ok {
		return "texture_prompt must be an object"
	}
	sources := 0
	for _, name := range []string{"text", "image", "images"} {
		if _, has := prompt[name]; has {
			sources++
		}
	}
	if sources > 1 {
		return "texture_prompt text, image and images are mutually exclusive"
	}
	if _, style := prompt["style_image"]; style {
		if _, hasText := prompt["text"]; !hasText {
			return "texture_prompt style_image is only used with text"
		}
	}
	return ""
}

// segmentRequestError checks a POST /mesh/segment body.
func segmentRequestError(body map[string]any) string {
	return problemMessage(
		optionalChoice(body, "model", segmentModels),
		optionalChoice(body, "segmentation_granularity", segmentGranularity),
		optionalBool(body, "split_by_connectivity"),
		optionalText(body, "ref_image", 4096),
	)
}

// completeRequestError checks a POST /mesh/complete body.
func completeRequestError(body map[string]any) string {
	return problemMessage(
		optionalChoice(body, "model", completeModels),
		optionalChoice(body, "completion_mode", completionModes),
		stringList(body, "part_names"),
	)
}

// smartsegmentRequestError checks a POST /mesh/smartsegment body (seg_type itself is
// checked by the create handler).
func smartsegmentRequestError(body map[string]any) string {
	if problem := problemMessage(
		optionalChoice(body, "granularity", smartSegmentLevels),
		optionalText(body, "hint", 4096),
	); problem != "" {
		return problem
	}
	if body["seg_type"] != "model" {
		return ""
	}
	transform, ok := body["transform"].([]any)
	if !ok || len(transform) != 16 {
		return "transform must be 16 numbers (a column-major 4x4 matrix) when seg_type is model"
	}
	for _, value := range transform {
		if _, isNumber := value.(json.Number); !isNumber {
			return "transform must be 16 numbers (a column-major 4x4 matrix) when seg_type is model"
		}
	}
	return ""
}

// editMultiviewFailureMessage is the error_message of the one real failed
// edit_multiview task (2026-11-25, #625, error_code 1001); the task-failed scenario
// ends an edit with it.
const editMultiviewFailureMessage = "Failed to generate image"

// editMultiviewPromptsError checks the prompts of POST /generation/edit-multiview.
func editMultiviewPromptsError(body map[string]any) string {
	prompts, ok := body["prompts"].([]any)
	if !ok || len(prompts) < 1 || len(prompts) > 4 {
		return "prompts must hold one to four edit instructions"
	}
	seen := map[string]bool{}
	for _, entry := range prompts {
		item, ok := entry.(map[string]any)
		if !ok {
			return "each prompts item needs a view and a prompt"
		}
		view, _ := item["view"].(string)
		if !slices.Contains(editMultiviewViews, view) {
			return fmt.Sprintf("view must be one of %s", strings.Join(editMultiviewViews, ", "))
		}
		if seen[view] {
			return "each prompts item must be for a distinct view"
		}
		seen[view] = true
		text, isText := item["prompt"].(string)
		if !isText || strings.TrimSpace(text) == "" {
			return "prompt must be a non-empty string"
		}
		if problem := optionalText(item, "prompt", editMultiviewPromptLimit); problem != nil {
			return problem.message
		}
	}
	return ""
}

// isGenerationTaskType reports one of the task types the pages list as the 3D generation
// tasks a rig check, a rig or a conversion takes a task id from.
func isGenerationTaskType(typ string) bool { return slices.Contains(decimateSourceTypes, typ) }

// riggedLineage reports whether a source task is a rigging task or a later task run on
// the rigged model: the chain of source tasks leads back to an animate_rig task through
// tasks that keep the skeleton.
func (h *handler) riggedLineage(source *task) bool {
	for depth := 0; source != nil && depth < 8; depth++ {
		switch source.kind.Type {
		case "animate_rig":
			return true
		case "texture_model", "highpoly_to_lowpoly", "mesh_segmentation", "mesh_completion":
			source = h.lookup(source.parent)
		default:
			return false
		}
	}
	return false
}

// smartSegmentSourceRefusal checks a task id input of Smart Segmentation: the task has
// to have succeeded (2007) and hold a usable picture or GLB (2006). A task that was
// never polled to its end has not succeeded from the caller's point of view.
func smartSegmentSourceRefusal(segType string, source *task) *refusal {
	if source.final != statusSuccess {
		return &refusal{code: 2007, message: "The task has not succeeded"}
	}
	usable := true
	switch segType {
	case "model":
		// The model pipeline takes a GLB: pictures, a splat, a rig check and a quad
		// FBX have none.
		switch source.kind.Type {
		case "image_to_multiview", "edit_multiview", "image_to_splat", "animate_prerigcheck":
			usable = false
		default:
			usable = strings.HasSuffix(source.artifactName("model"), ".glb")
		}
	default:
		switch source.kind.Type {
		case "image_to_splat", "animate_prerigcheck", "convert_model":
			usable = false
		}
	}
	if !usable {
		return &refusal{code: 2006, message: "The task has no usable image or GLB model"}
	}
	return nil
}
