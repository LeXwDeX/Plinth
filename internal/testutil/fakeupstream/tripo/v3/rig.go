package tripov3

import (
	"fmt"
	"slices"
	"strings"
)

// The rig and retarget request contracts (#277). The committed samples hold the
// one request each endpoint was captured with (rig_type biped, spec tripo,
// preset:walk); everything else here is constructed from the official pages
// read on rigDocsChecked and has NOT been observed against the real API:
//
//   - https://developers.tripo3d.ai/en/docs/animations-rig (model, rig_type,
//     spec and out_format values; which values exist only under v3.0; spec
//     applies only to a biped rig)
//   - https://developers.tripo3d.ai/en/docs/animations-retarget (animation XOR
//     animations of up to 5 presets, the presets of each rig, out_format and
//     the boolean switches)
//   - https://developers.tripo3d.ai/en/docs/animations-rig-check (the seven
//     rig types a check can recommend)
//
// The refusals of an unknown value are modelled on the create envelope the
// samples hold (400, code 1004). How the real API fails a preset on a rig it
// does not fit is unverified; the fake reuses the one failure that was captured
// (a retarget of a v2 rig ends "failed", code 1004).
const rigDocsChecked = "2026-11-12"

const (
	rigModelV1  = "v1.0-20240301"
	rigModelV25 = "v2.5-20260210"
	rigModelV3  = "v3.0-20260909"
)

var (
	rigModels = []string{rigModelV1, rigModelV25, rigModelV3}
	// rigTypes are the documented rig_type values; "others" is documented only
	// under v3.0.
	rigTypes = []string{"biped", "quadruped", "hexapod", "octopod", "avian", "serpentine", "aquatic", "others"}
	// rigSpecs are the documented spec values; actorcore, unity, unreal and vrm
	// are documented only under v3.0.
	rigSpecs        = []string{"tripo", "mixamo", "actorcore", "unity", "unreal", "vrm"}
	rigSpecsV3Only  = []string{"actorcore", "unity", "unreal", "vrm"}
	rigOutFormats   = []string{"glb", "fbx"}
	maxRetargetList = 5
)

// retargetBipedV3 are the presets of a v3.0 biped rig.
var retargetBipedV3 = []string{
	"preset:idle", "preset:walk", "preset:run", "preset:dive", "preset:climb", "preset:jump",
	"preset:slash", "preset:shoot", "preset:hurt", "preset:fall", "preset:turn",
}

// retargetCreatures maps each preset of a non-biped rig (v2.5 or v3.0) to the
// rig_type it needs.
var retargetCreatures = map[string]string{
	"preset:quadruped:walk":   "quadruped",
	"preset:hexapod:walk":     "hexapod",
	"preset:octopod:walk":     "octopod",
	"preset:serpentine:march": "serpentine",
	"preset:aquatic:march":    "aquatic",
}

// retargetBipedV1 are the names after the "preset:biped:" prefix of a v1.0
// biped rig.
var retargetBipedV1 = []string{
	"afraid", "agree", "angry_01", "angry_02", "angry_03", "basketball_shot", "bow", "box_01", "box_02", "box_03",
	"cast_a_spell", "cheer", "chop", "clap", "climb", "complain_01", "complain_02", "cross_body_crunch",
	"crossover_dribble", "cry", "dance_01", "dance_02", "dance_03", "dance_04", "dance_05", "dance_06",
	"defeat_02", "defeat_03", "depressed", "dig", "dive", "dribble", "fall", "fire", "flee_01", "flee_02", "flip",
	"fold_arms", "football_catch", "football_save", "football_pass", "freaky", "frightened", "front_kick_01",
	"front_kick_02", "frustrated_01", "frustrated_02", "golf", "greet_01", "greet_02", "greet_03", "greet_04",
	"heart_pose", "hit_to_body_01", "hit_to_body_02", "hit_to_head", "hit_to_side", "hit_to_stomach", "hug", "hurt",
	"idle", "jump_down", "jump", "jump_rope_01", "jump_rope_02", "laugh_01", "laugh_02", "lift_heavy", "look_around",
	"make_a_call_01", "make_a_call_02", "pitch_baseball", "play_mobile_game", "play_video_game", "press-up",
	"run_upstairs", "run", "scared_01", "scared_02", "scratch", "shoot", "shovel", "sing_01", "sing_02", "sing_03",
	"sing_04", "sit", "slash", "sob", "standing_relax", "surf", "swagger", "swim", "turn", "victory_celebration",
	"volleyball", "wait", "walk", "warm_up", "wave_goodbye_01", "wave_goodbye_02",
}

func choice(value string, allowed []string) bool { return slices.Contains(allowed, value) }

// optionalString reads a string field that may be absent. present is false for
// an absent field; ok is false for a field that is not a non-empty string.
func optionalString(body map[string]any, name string) (value string, present, ok bool) {
	raw, exists := body[name]
	if !exists {
		return "", false, true
	}
	text, isString := raw.(string)
	return text, true, isString && strings.TrimSpace(text) != ""
}

// rigModelOf is the model a rig request names; the fake requires it.
func rigModelOf(body map[string]any) string {
	model, _ := body["model"].(string)
	return model
}

// rigTypeOf is the rig_type of a rig request, defaulting to the documented
// biped.
func rigTypeOf(body map[string]any) string {
	if value, present, ok := optionalString(body, "rig_type"); present && ok {
		return value
	}
	return "biped"
}

// rigRequestError checks the documented fields of POST /animations/rig (model
// is required by this fake because Plinth always sends it). An empty answer
// means the request is valid.
func rigRequestError(body map[string]any) string {
	model := rigModelOf(body)
	if !choice(model, rigModels) {
		return fmt.Sprintf("model must be one of %s", strings.Join(rigModels, ", "))
	}
	v3 := strings.HasPrefix(model, "v3")
	rigType, present, ok := optionalString(body, "rig_type")
	if present && (!ok || !choice(rigType, rigTypes)) {
		return fmt.Sprintf("rig_type must be one of %s", strings.Join(rigTypes, ", "))
	}
	if !present {
		rigType = "biped"
	}
	if rigType == "others" && !v3 {
		return "rig_type others is only available with " + rigModelV3
	}
	if strings.HasPrefix(model, "v1") && rigType != "biped" {
		return "model " + rigModelV1 + " rigs biped characters only"
	}
	spec, present, ok := optionalString(body, "spec")
	if present && (!ok || !choice(spec, rigSpecs)) {
		return fmt.Sprintf("spec must be one of %s", strings.Join(rigSpecs, ", "))
	}
	if present && choice(spec, rigSpecsV3Only) && !v3 {
		return "spec " + spec + " is only available with " + rigModelV3
	}
	if format, present, ok := optionalString(body, "out_format"); present && (!ok || !choice(format, rigOutFormats)) {
		return "out_format must be glb or fbx"
	}
	return ""
}

// retargetPresets lists the presets of a retarget request: animation XOR
// animations (at most five). A second return of false names the problem.
func retargetPresets(body map[string]any) ([]string, string) {
	single, hasSingle := body["animation"]
	many, hasMany := body["animations"]
	switch {
	case hasSingle && hasMany:
		return nil, "animation and animations are mutually exclusive"
	case !hasSingle && !hasMany:
		return nil, "animation is required"
	case hasSingle:
		preset, ok := single.(string)
		if !ok || strings.TrimSpace(preset) == "" {
			return nil, "animation must be a preset"
		}
		return []string{preset}, ""
	}
	items, ok := many.([]any)
	if !ok || len(items) == 0 {
		return nil, "animations must list presets"
	}
	if len(items) > maxRetargetList {
		return nil, fmt.Sprintf("animations accepts at most %d presets", maxRetargetList)
	}
	presets := make([]string, 0, len(items))
	for _, item := range items {
		preset, ok := item.(string)
		if !ok || strings.TrimSpace(preset) == "" {
			return nil, "animations must list presets"
		}
		presets = append(presets, preset)
	}
	return presets, ""
}

// knownRetargetPreset reports whether a preset exists on any documented rig.
func knownRetargetPreset(preset string) bool {
	if choice(preset, retargetBipedV3) {
		return true
	}
	if _, creature := retargetCreatures[preset]; creature {
		return true
	}
	name, v1 := strings.CutPrefix(preset, "preset:biped:")
	return v1 && choice(name, retargetBipedV1)
}

// retargetRequestError checks the documented fields of POST /animations/retarget.
func retargetRequestError(body map[string]any) string {
	presets, problem := retargetPresets(body)
	if problem != "" {
		return problem
	}
	for _, preset := range presets {
		if !knownRetargetPreset(preset) {
			return "animation " + preset + " is not a documented preset"
		}
	}
	if format, present, ok := optionalString(body, "out_format"); present && (!ok || !choice(format, rigOutFormats)) {
		return "out_format must be glb or fbx"
	}
	for _, name := range []string{"export_with_geometry", "animate_in_place", "bake_animation"} {
		if value, present := body[name]; present {
			if _, isBool := value.(bool); !isBool {
				return name + " must be a boolean"
			}
		}
	}
	return ""
}

// retargetFits reports whether a rig built by the source task takes every
// preset: the v3.0 biped presets need a v3.0 biped rig (a v2 rig ends "failed",
// captured 2026-10-29), the creature presets a rig of their own type built with
// v2.5 or v3.0, and the "preset:biped:" names a v1.0 biped rig.
func retargetFits(source *task, presets []string) bool {
	model, rigType := rigModelOf(source.request), rigTypeOf(source.request)
	for _, preset := range presets {
		switch {
		case choice(preset, retargetBipedV3):
			if rigType != "biped" || !strings.HasPrefix(model, "v3") {
				return false
			}
		case strings.HasPrefix(preset, "preset:biped:"):
			if rigType != "biped" || !strings.HasPrefix(model, "v1") {
				return false
			}
		default:
			needs, creature := retargetCreatures[preset]
			if !creature || rigType != needs || strings.HasPrefix(model, "v1") {
				return false
			}
		}
	}
	return true
}

// echoedInput is what a poll of a rig or retarget task repeats of its request in
// data.input. The real rig poll names the rig type "topology" (captured); only
// the keys the committed sample's input already holds are written, so the shape
// stays the sampled one.
func (t *task) echoedInput(input map[string]any) {
	set := func(key string, value any) {
		if _, has := input[key]; has && value != nil {
			input[key] = value
		}
	}
	switch t.kind.Type {
	case "animate_rig":
		set("spec", stringOr(t.request["spec"], "tripo"))
		set("topology", rigTypeOf(t.request))
		set("out_format", stringOr(t.request["out_format"], "glb"))
	case "animate_retarget":
		set("out_format", stringOr(t.request["out_format"], "glb"))
		set("export_with_geometry", boolOr(t.request["export_with_geometry"], true))
		set("animate_in_place", boolOr(t.request["animate_in_place"], false))
		if many, hasMany := t.request["animations"]; hasMany {
			delete(input, "animation")
			input["animations"] = many
		} else {
			set("animation", t.request["animation"])
		}
	}
}

func stringOr(value any, fallback string) any {
	if text, ok := value.(string); ok && text != "" {
		return text
	}
	return fallback
}

func boolOr(value any, fallback bool) any {
	if flag, ok := value.(bool); ok {
		return flag
	}
	return fallback
}
