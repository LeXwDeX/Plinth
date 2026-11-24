package tripov3

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// The request contract of the three generation endpoints (POST /generation/text-to-model,
// /image-to-model and /multiview-to-model), read from the official pages on
// generationDocsChecked (#541):
//
//   - https://developers.tripo3d.ai/en/docs/generation-text-to-model/standard and /p
//   - https://developers.tripo3d.ai/en/docs/generation-image-to-model/standard and /p
//   - https://developers.tripo3d.ai/en/docs/generation-multiview-to-model/standard and /p
//   - https://developers.tripo3d.ai/en/docs/changelog (the P1/P2 notes)
//   - https://developers.tripo3d.ai/en/docs/error-handling (codes 1004 and 2015)
//
// An H and a P series request share the endpoint; the model names the family. Before
// #541 the fake only required model and prompt/input, so a combination the real API
// refuses (a P1 quad request, a v2.5 geometry_quality, a face_limit over the tier's
// ceiling, parts with a texture) looked fine against it.
//
// What the pages state, and what the fake does with it:
//
//   - model: H is v3.1-20260211, v3.0-20250812 or v2.5-20250123; P is P2-20260801 or
//     P1-20260311. Any other name is 400/code 1004.
//   - texture_quality is fast, standard, detailed or extreme; texture_version is
//     v3.5-20260815, v3.0-20250812 or v2.5-20250123; geometry_quality is standard or
//     detailed; orientation (image and multiview) is default or align_image;
//     texture_alignment is original_image or geometry; export_orientation is +x, -x, -y
//     or +y; compress is geometry. Each is 1004 outside its list.
//   - texture_quality=fast with a v2.5 texture model (an explicit texture_version of
//     v2.5, or a v2.5 geometry model without texture_version) is 1004, "rather than
//     silently falling back to standard" (official).
//   - geometry_quality, auto_size, quad, smart_low_poly, generate_parts and compress
//     "require model >= v3.0" (image page); the fake answers 1004 for a v2.5 model.
//     texture_quality is listed in the same group on the multiview page, but its only
//     documented refusal is fast (above), so the other values pass on v2.5.
//   - face_limit ceilings: v3.1 1,500,000 standard or 2,000,000 with
//     geometry_quality=detailed (Ultra); v3.0 1,000,000 or 2,000,000; v2.5 500,000;
//     quad 150,000; smart_low_poly 500-20,000 triangles or 500-10,000 quads. P1 50-20,000;
//     P2 48-50,000, or 48-25,000 with quad. The page names no floor for the H series;
//     the fake only requires a positive integer.
//   - quad is P2 only among the P series ("Sending quad=true with P1 returns 400",
//     changelog); P1 also does not support geometry_quality, smart_low_poly or
//     generate_parts (changelog).
//   - generate_parts needs texture=false and pbr=false; omitting texture counts as true
//     and is 1004 (official). The fake accepts an omitted pbr next to texture=false (the
//     page does not say whether the default pbr=true then conflicts) and refuses pbr=true.
//   - prompt is at most 1024 characters, negative_prompt at most 255 (official).
//
// Constructed, not stated: that the P2 model refuses geometry_quality, smart_low_poly,
// generate_parts and compress (its pages do not list them; P1's changelog line says it
// does not support them, and the task asked for the P series to refuse the H-only
// parameters); which model names answer 2015 (the error page says only "the version has
// been deprecated"; the names below are the retired generations of the earlier public
// API); the message text of each refusal; and that a v2.5 model refuses auto_size and
// compress with 1004 rather than ignoring them. None of it has been requested from the
// real API; the zero-cost negative probes that settle it are listed in the issue.
const generationDocsChecked = "2026-11-24"

const (
	textureQualityFast = "fast"
	textureV25         = "v2.5-20250123"
)

var (
	generationModelsH = []string{"v3.1-20260211", "v3.0-20250812", textureV25}
	generationModelsP = []string{"P2-20260801", "P1-20260311"}
	// deprecatedModels are the model names of the retired generations: the fake
	// answers 2015 for them. The real list is not published (constructed).
	deprecatedModels = []string{"v1.3-20240522", "v1.4-20240625", "v2.0-20240919", "Turbo-v1.0-20250506"}

	textureQualities   = []string{"fast", "standard", "detailed", "extreme"}
	textureVersions    = []string{"v3.5-20260815", "v3.0-20250812", textureV25}
	geometryQualities  = []string{"standard", "detailed"}
	orientations       = []string{"default", "align_image"}
	textureAlignments  = []string{"original_image", "geometry"}
	exportOrientations = []string{"+x", "-x", "-y", "+y"}
	compressModes      = []string{"geometry"}
)

// refusal is one answer of the create envelope: 400 with a business code.
type refusal struct {
	code    int
	message string
}

func invalid(format string, args ...any) *refusal {
	return &refusal{code: 1004, message: fmt.Sprintf(format, args...)}
}

// isGenerationPath reports one of the three endpoints this file checks.
func isGenerationPath(path string) bool {
	switch path {
	case "/generation/text-to-model", "/generation/image-to-model", "/generation/multiview-to-model":
		return true
	}
	return false
}

// modelFamily classifies a model name: "h", "p", "deprecated" or "" for a name no
// family has.
func modelFamily(model string) string {
	switch {
	case slices.Contains(generationModelsH, model):
		return "h"
	case slices.Contains(generationModelsP, model):
		return "p"
	case slices.Contains(deprecatedModels, model):
		return "deprecated"
	}
	return ""
}

// optionalChoice checks an optional string field against its list. A field that is
// absent is fine.
func optionalChoice(body map[string]any, name string, allowed []string) *refusal {
	raw, present := body[name]
	if !present {
		return nil
	}
	text, ok := raw.(string)
	if !ok || !slices.Contains(allowed, text) {
		return invalid("%s must be one of %s", name, strings.Join(allowed, ", "))
	}
	return nil
}

// optionalBool checks an optional boolean field.
func optionalBool(body map[string]any, name string) *refusal {
	if raw, present := body[name]; present {
		if _, ok := raw.(bool); !ok {
			return invalid("%s must be a boolean", name)
		}
	}
	return nil
}

// optionalInteger checks an optional integer field and returns its value.
func optionalInteger(body map[string]any, name string) (value int64, present bool, problem *refusal) {
	raw, exists := body[name]
	if !exists {
		return 0, false, nil
	}
	number, ok := raw.(json.Number)
	if !ok {
		return 0, true, invalid("%s must be an integer", name)
	}
	parsed, err := number.Int64()
	if err != nil {
		return 0, true, invalid("%s must be an integer", name)
	}
	return parsed, true, nil
}

// optionalText checks an optional string field against a length in characters.
func optionalText(body map[string]any, name string, limit int) *refusal {
	raw, present := body[name]
	if !present {
		return nil
	}
	text, ok := raw.(string)
	if !ok {
		return invalid("%s must be a string", name)
	}
	if utf8.RuneCountInString(text) > limit {
		return invalid("%s must be at most %d characters", name, limit)
	}
	return nil
}

// flag reads a boolean field, def when it is absent.
func flag(body map[string]any, name string, def bool) bool {
	if value, ok := body[name].(bool); ok {
		return value
	}
	return def
}

// modelAtLeastV3 reports an H model of v3.0 or later: the names that sort after
// v2.5 in the version number.
func modelAtLeastV3(model string) bool { return strings.HasPrefix(model, "v3") }

// generationRequestError returns what is wrong with a generation request body, or
// nil. The body was decoded with UseNumber.
func generationRequestError(body map[string]any) *refusal {
	model, _ := body["model"].(string)
	family := modelFamily(model)
	switch family {
	case "":
		return invalid("model must be one of %s", strings.Join(append(slices.Clone(generationModelsH), generationModelsP...), ", "))
	case "deprecated":
		return &refusal{code: 2015, message: "The version has been deprecated, please try higher version"}
	}
	isP := family == "p"

	if problem := optionalText(body, "prompt", 1024); problem != nil {
		return problem
	}
	if problem := optionalText(body, "negative_prompt", 255); problem != nil {
		return problem
	}
	for name, allowed := range map[string][]string{
		"texture_quality": textureQualities, "texture_version": textureVersions, "geometry_quality": geometryQualities,
		"orientation": orientations, "texture_alignment": textureAlignments, "export_orientation": exportOrientations,
		"compress": compressModes,
	} {
		if problem := optionalChoice(body, name, allowed); problem != nil {
			return problem
		}
	}
	for _, name := range []string{
		"texture", "pbr", "auto_size", "quad", "smart_low_poly", "generate_parts", "export_uv", "delight", "enable_image_autofix",
	} {
		if problem := optionalBool(body, name); problem != nil {
			return problem
		}
	}
	for _, name := range []string{"model_seed", "image_seed", "texture_seed"} {
		if _, _, problem := optionalInteger(body, name); problem != nil {
			return problem
		}
	}
	faces, hasFaces, problem := optionalInteger(body, "face_limit")
	if problem != nil {
		return problem
	}

	quad := flag(body, "quad", false)
	smart := flag(body, "smart_low_poly", false)

	// The H-only parameters of the P series and the v3.0-or-later parameters of H2.5.
	if isP {
		if model == "P1-20260311" && quad {
			return invalid("quad is only supported by P2-20260801")
		}
		for _, name := range []string{"geometry_quality", "smart_low_poly", "generate_parts", "compress"} {
			if _, present := body[name]; present {
				return invalid("%s is not supported by the P series", name)
			}
		}
	} else if !modelAtLeastV3(model) {
		for _, name := range []string{"geometry_quality", "auto_size", "quad", "smart_low_poly", "generate_parts", "compress"} {
			if _, present := body[name]; present {
				return invalid("%s requires a model of v3.0 or later", name)
			}
		}
	}

	if quality, _ := body["texture_quality"].(string); quality == textureQualityFast {
		version, explicit := body["texture_version"].(string)
		if !explicit && model == textureV25 {
			version = textureV25
		}
		if version == textureV25 {
			return invalid("texture_quality fast requires texture_version v3.5-20260815")
		}
	}

	if flag(body, "generate_parts", false) {
		if flag(body, "texture", true) || flag(body, "pbr", false) {
			return invalid("generate_parts requires texture=false and pbr=false")
		}
	}

	if hasFaces {
		detailed := body["geometry_quality"] == "detailed"
		low, high := faceLimitRange(model, isP, quad, smart, detailed)
		if faces < low || faces > high {
			return invalid("face_limit must be between %d and %d for this request, got %d", low, high, faces)
		}
	}
	return nil
}

// faceLimitRange is the face_limit range the pages list for a model and its options.
// detailed is geometry_quality=detailed (Ultra), which lifts the H3.x ceiling to
// 2,000,000.
func faceLimitRange(model string, isP, quad, smart, detailed bool) (low, high int64) {
	if isP {
		if model == "P1-20260311" {
			return 50, 20000
		}
		if quad {
			return 48, 25000
		}
		return 48, 50000
	}
	switch {
	case smart && quad:
		return 500, 10000
	case smart:
		return 500, 20000
	case quad:
		return 1, 150000
	case model == textureV25:
		return 1, 500000
	case detailed:
		return 1, 2000000
	case strings.HasPrefix(model, "v3.1"):
		return 1, 1500000
	}
	return 1, 1000000
}
