package core

import (
	"encoding/base64"
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"slices"
	"strconv"
	"strings"
)

// The remesh options of a 3D generation request (should_remesh, topology,
// target_polycount, decimation_mode) as the Text to 3D and Image to 3D pages
// (https://docs.meshy.ai/en/api/text-to-3d and .../image-to-3d, read 2026-11-12,
// #276) document them. Before that reading the fake took any value, so a game
// request with a face count outside the model's range, or quads from a Smart
// Topology model, looked fine against it.
//
//   - should_remesh is a boolean; the options below apply only when it is true
//     (the page says "for the highest-quality 3D model, send false"). A Smart
//     Topology model ignores it: no remesh runs.
//   - topology is quad ("a quad-dominant mesh") or triangle ("a decimated
//     triangle mesh", the default). Smart Topology output is triangle-only, so
//     quad with meshy-t2 or meshy-t2.5 answers 400.
//   - target_polycount is an integer, "the number of faces to aim for": 100 to
//     300000 for a remeshed standard model, 100 to 15000 for meshy-t2 and 100 to
//     30000 for meshy-t2.5 (which needs no should_remesh); out of range answers
//     400. How a quad counts is not stated, so the fake does not decide it.
//   - decimation_mode is 1 to 4 (ultra, high, medium, low) and replaces
//     target_polycount.
//
// What the pages do not say is not invented: whether quad topology works with 4K
// or 8K textures, and whether the texture of a remeshed task is generated on the
// remeshed mesh (Text to 3D remeshes in the preview and textures in the refine,
// Image to 3D does both in one task), are not modelled. The refusal text is the
// real {message} envelope with a constructed message; none of these refusals has
// been requested from the real API.

// The Smart Topology models and the target_polycount ceiling of each; a model
// type of smart-topology without an ai_model is meshy-t2.5, the page's default.
var smartTopologyCeilings = map[string]int64{"meshy-t2": 15000, "meshy-t2.5": 30000}

const (
	defaultSmartTopologyModel = "meshy-t2.5"
	standardTargetCeiling     = 300000
	targetFloor               = 100
)

// The official image bounds of Image to 3D and Multi Image to 3D
// (docs.meshy.ai/en/api/image-to-3d and .../multi-image-to-3d, read
// 2026-11-22): each side at least 32 px and at most 178,956,970 pixels in
// total. A picture outside them is refused before the task is created.
const (
	minImageSide   = 32
	maxImagePixels = 178956970
)

// ImageRequestError returns what is wrong with the data URI images of an
// image-to-3d or multi-image-to-3d request, or "". Only data URIs are checked:
// the platform inlines the pictures, and a remote URL is the upstream's to
// fetch. The refusal text is constructed (no real rejection was captured).
func ImageRequestError(request map[string]any) string {
	values := make([]any, 0, 1)
	if value, ok := request["image_url"].(string); ok {
		values = append(values, value)
	}
	if list, ok := request["image_urls"].([]any); ok {
		values = append(values, list...)
	}
	for _, value := range values {
		uri, _ := value.(string)
		if message := imageDataURIError(uri); message != "" {
			return message
		}
	}
	return ""
}

func imageDataURIError(uri string) string {
	const prefix = "data:"
	if !strings.HasPrefix(uri, prefix) {
		return ""
	}
	comma := strings.IndexByte(uri, ',')
	if comma < 0 || !strings.Contains(uri[:comma], ";base64") {
		return "image_url must be a base64 data URI"
	}
	raw, err := base64.StdEncoding.DecodeString(uri[comma+1:])
	if err != nil {
		return "image_url must be a base64 data URI"
	}
	config, _, err := image.DecodeConfig(strings.NewReader(string(raw)))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return "image_url must be a PNG or JPEG image"
	}
	if config.Width < minImageSide || config.Height < minImageSide {
		return "each image side must be at least 32 pixels"
	}
	if int64(config.Width)*int64(config.Height) > maxImagePixels {
		return "an image must not exceed 178956970 pixels"
	}
	return ""
}

// TextTo3DRequestError is the Text to 3D create validation. The official
// preview parameter table has no enable_pbr, texture_prompt or
// texture_image_url; sending them on a preview is refused. pose_mode is
// optional and "leave it out for no pose", so an explicit empty string is
// refused rather than sent.
func TextTo3DRequestError(request map[string]any) string {
	if message := GenerationRequestError(request); message != "" {
		return message
	}
	if value, present := request["pose_mode"]; present {
		if text, ok := value.(string); !ok || strings.TrimSpace(text) == "" {
			return "pose_mode must be a-pose or t-pose; leave it out for no pose"
		}
	}
	if mode, _ := request["mode"].(string); mode != "refine" {
		for _, field := range []string{"enable_pbr", "texture_prompt", "texture_image_url"} {
			if has(request, field) {
				return field + " is only valid for a refine task"
			}
		}
	}
	return ""
}

// GenerationRequestError returns what is wrong with the remesh options of a 3D
// generation request body, or "". The body was decoded with UseNumber.
func GenerationRequestError(request map[string]any) string {
	if value, present := request["pose_mode"]; present {
		if text, ok := value.(string); !ok || strings.TrimSpace(text) == "" {
			return "pose_mode must be a-pose or t-pose; leave it out for no pose"
		}
	}
	aiModel, _ := request["ai_model"].(string)
	modelType, _ := request["model_type"].(string)
	_, smartModel := smartTopologyCeilings[aiModel]
	smart := modelType == "smart-topology" || smartModel
	if smart && !smartModel {
		aiModel = defaultSmartTopologyModel
	}
	if value, present := request["should_remesh"]; present {
		if _, ok := value.(bool); !ok {
			return "should_remesh must be a boolean"
		}
	}
	remeshes := !smart && request["should_remesh"] == true
	if value, present := request["topology"]; present {
		topology, _ := value.(string)
		if !slices.Contains([]string{"quad", "triangle"}, topology) {
			return "topology must be quad or triangle"
		}
		if smart && topology == "quad" {
			return "Smart Topology output is triangle-only; topology quad is not supported with " + aiModel
		}
	}
	if value, present := request["decimation_mode"]; present {
		mode, ok := integerOf(value)
		if !ok || mode < 1 || mode > 4 {
			return "decimation_mode must be an integer from 1 to 4"
		}
	}
	if value, present := request["target_polycount"]; present {
		count, ok := integerOf(value)
		if !ok {
			return "target_polycount must be an integer"
		}
		ceiling := int64(standardTargetCeiling)
		if smart {
			ceiling = smartTopologyCeilings[aiModel]
		}
		if (smart || remeshes) && (count < targetFloor || count > ceiling) {
			return "target_polycount must be between " + strconv.Itoa(targetFloor) + " and " + strconv.FormatInt(ceiling, 10)
		}
	}
	return ""
}

func integerOf(value any) (int64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := number.Int64()
	return parsed, err == nil
}
