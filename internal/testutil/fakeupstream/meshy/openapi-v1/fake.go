package meshyopenapiv1

import (
	"embed"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/core"
)

//go:embed testdata/meshy-openapi-v1-*.candidate.json
var sampleFS embed.FS

// sampleFiles are the committed candidates, one per Catalog Offering that was
// captured; the glob keeps newly captured files in.
var sampleFiles = func() []string {
	names, err := globSamples()
	if err != nil || len(names) == 0 {
		panic("meshy/openapi-v1: no committed samples")
	}
	return names
}()

// Prefix is where this version is mounted. The Meshy adapter appends
// openapi/v1/<endpoint> to meshy_base_url, so a stack points meshy_base_url at
// <fake>/meshy.
const Prefix = "/meshy/openapi/v1"

// samplePrefix is the path prefix the official API uses; the committed samples
// record it.
const samplePrefix = "/openapi/v1"

const (
	docsRoot = "https://docs.meshy.ai/en/api/"
	// captured is the date the real samples were taken from the official API.
	captured = "2026-10-29"
	// docsChecked is the date the corrected documentation links and the
	// constructed fault answers were checked.
	docsChecked = "2026-11-08"
)

// imageGenerationRequestError checks the remesh options, the official pixel
// bounds of every inlined picture (#460 B24, #487 for the texture views) and the
// option combinations the Image to 3D and Multi Image to 3D pages refuse.
func imageGenerationRequestError(multi bool) func(map[string]any) string {
	return func(request map[string]any) string {
		if message := core.GenerationRequestError(request); message != "" {
			return message
		}
		if message := core.ImageRequestError(request); message != "" {
			return message
		}
		if message := core.TextureImageListError(request); message != "" {
			return message
		}
		return core.ImageTo3DRequestError(request, multi)
	}
}

// pictureRequestError checks the pixel bounds of the inlined pictures before a
// route's own rules.
func pictureRequestError(rules func(map[string]any) string) func(map[string]any) string {
	return func(request map[string]any) string {
		if message := core.ImageRequestError(request); message != "" {
			return message
		}
		return rules(request)
	}
}

// imageSources are the task types an Image to Image or Multi Image to 3D
// input_task_id may name (the pages: a succeeded Text to Image or Image to
// Image task, multi-view included).
var imageSources = map[string][]string{"input_task_id": {"text-to-image", "image-to-image"}}

// texturedUnlessDisabled follows the should_texture default (true).
func texturedUnlessDisabled(request map[string]any) bool {
	enabled, ok := request["should_texture"].(bool)
	return !ok || enabled
}

// kinds are the task routes of this version, in the order of the Catalog's
// Offerings. Every one has a captured sample of its own.
var kinds = []core.Kind{
	{Path: "/text-to-image", Type: "text-to-image", Need: [][]string{{"prompt"}}, Real: true,
		Validate: core.TextToImageRequestError},
	{Path: "/image-to-image", Type: "image-to-image", Need: [][]string{{"prompt"}, {"reference_image_urls", "input_task_id"}}, Sources: []string{"input_task_id"},
		SourceTypes: imageSources, Real: true, Validate: pictureRequestError(core.ImageToImageRequestError)},
	{Path: "/image-to-3d", Type: "image-to-3d", Need: [][]string{{"image_url", "input_task_id"}}, Sources: []string{"input_task_id"}, Real: true,
		Textured: texturedUnlessDisabled, Validate: imageGenerationRequestError(false), EchoResolution: true},
	{Path: "/multi-image-to-3d", Type: "multi-image-to-3d", Need: [][]string{{"image_urls", "input_task_id"}}, Sources: []string{"input_task_id"},
		SourceTypes: imageSources, Real: true, Textured: texturedUnlessDisabled, Validate: imageGenerationRequestError(true), EchoResolution: true},
	{Path: "/remesh", Type: "remesh", Need: [][]string{{"input_task_id", "model_url"}}, Sources: []string{"input_task_id"}, Real: true,
		Validate: core.RemeshRequestError},
	{Path: "/convert", Type: "convert", Need: [][]string{{"input_task_id", "model_url"}, {"target_formats"}}, Sources: []string{"input_task_id"}, Real: true},
	{Path: "/retexture", Type: "retexture", Need: [][]string{{"input_task_id", "model_url"}, {"text_style_prompt", "image_style_url", "multiview_image_urls"}},
		Sources: []string{"input_task_id"}, Real: true, Textured: func(map[string]any) bool { return true },
		Validate: pictureRequestError(core.RetextureRequestError)},
	{Path: "/rigging", Type: "rig", Need: [][]string{{"input_task_id", "model_url"}}, Sources: []string{"input_task_id"}, Fidelity: "skeleton", Real: true,
		Validate: riggingRequestError, ValidateSource: riggingSourceError, ForbiddenMessage: processingForbiddenMessage},
	{Path: "/animations", Type: "animate", Need: [][]string{{"rig_task_id"}, {"action_id", "action_ids", "motion_task_id"}}, Sources: []string{"rig_task_id", "motion_task_id"}, Fidelity: "animation", Real: true,
		Validate: core.AnimationRequestError, ValidateSourceRequest: core.AnimationSourceError, ForbiddenMessage: processingForbiddenMessage},
	{Path: "/uv-unwrap", Type: "uv-unwrap", Need: [][]string{{"input_task_id", "model_url"}}, Sources: []string{"input_task_id"}, Real: true},
	{Path: "/resize", Type: "resize", Need: [][]string{{"input_task_id", "model_url"}}, Sources: []string{"input_task_id"}, Real: true,
		Validate: resizeRequestError},
	{Path: "/print/analyze", Type: "print-analyze", Need: [][]string{{"input_task_id", "model_url"}}, Sources: []string{"input_task_id"}, Real: true,
		Patch: patchPrintAnalyze},
	{Path: "/print/multi-color", Type: "print-multi-color", Need: [][]string{{"input_task_id", "model_url"}}, Sources: []string{"input_task_id"}, Real: true},
	{Path: "/text-to-motion", Type: "text-to-motion", Need: [][]string{{"prompt"}, {"duration"}}, Fidelity: "animation", Real: true,
		Validate: core.TextToMotionRequestError},
}

// processingForbiddenMessage is the 403 text the Rigging and Animation pages
// give (read 2026-11-25): "This model type does not support this operation",
// for a bas-relief input. No real 403 was captured; the other routes keep the
// Errors page's generic text.
const processingForbiddenMessage = "This model type does not support this operation"

// rigSkeletonTemplates are the skeleton_template values of the Rigging page
// (https://docs.meshy.ai/en/api/rigging-and-animation, read 2026-11-12); meshyrig is
// the default.
var rigSkeletonTemplates = []string{
	"meshyrig", "mixamo", "ue5_mannequin", "max_biped", "unity_humanoid", "maya_human_ik",
	"blender_rigify_basic_human", "cc5_hd_neutral_base", "daz_genesis9",
}

// resizeRequestError is the one rule the Resize page states about its sizing
// modes (https://docs.meshy.ai/en/api/resize, read 2026-11-23): "Send exactly
// one of resize_height, resize_longest_side, or auto_size. Sending none of
// them, or more than one, returns 400." The fake used to create the task for
// any body, so a projection that dropped auto_size still looked fine against
// it (#487). The refusal text is constructed; no real rejection was captured.
func resizeRequestError(request map[string]any) string {
	modes := 0
	for _, field := range []string{"resize_height", "resize_longest_side", "auto_size"} {
		if _, present := request[field]; present {
			modes++
		}
	}
	if modes != 1 {
		return "send exactly one of resize_height, resize_longest_side or auto_size"
	}
	// "The model's new height in meters, greater than 0" and the same for the
	// longest side; the page names a negative value as a 400 cause, and 0 is
	// not greater than 0 (read 2026-11-25; constructed refusal text).
	for _, field := range []string{"resize_height", "resize_longest_side"} {
		if value, present := request[field]; present {
			number, isNumber := value.(json.Number)
			size, err := number.Float64()
			if !isNumber || err != nil || size <= 0 {
				return field + " must be a number greater than 0"
			}
		}
	}
	return ""
}

// riggingRequestError checks the documented rigging fields (#277). The page
// read on 2026-11-12 says: height_meters below zero answers 400; skeleton_template
// takes one of the nine values above and any other answers 400; include_fingers is a
// boolean, and true together with texture_image_url answers 400. Both options also
// need a biped rig, which the fake cannot judge (no pose estimation), and the two 422
// refusals for a model that can only be rigged with the default skeleton or without
// fingers are not modelled. None of this has been requested from the real API.
func riggingRequestError(request map[string]any) string {
	if value, present := request["height_meters"]; present {
		number, isNumber := value.(json.Number)
		height, err := number.Float64()
		if !isNumber || err != nil {
			return "height_meters must be a number"
		}
		if height < 0 {
			return "height_meters must not be negative"
		}
	}
	if value, present := request["skeleton_template"]; present {
		template, isString := value.(string)
		if !isString || !slices.Contains(rigSkeletonTemplates, template) {
			return "skeleton_template must be one of " + strings.Join(rigSkeletonTemplates, ", ")
		}
	}
	if value, present := request["include_fingers"]; present {
		fingers, isBool := value.(bool)
		if !isBool {
			return "include_fingers must be a boolean"
		}
		if texture, _ := request["texture_image_url"].(string); fingers && strings.TrimSpace(texture) != "" {
			return "include_fingers cannot be combined with texture_image_url"
		}
	}
	// The page read on 2026-11-25: animation_type is biped (the default) or
	// quadruped and "Any other value returns 400"; with a quadruped rig,
	// "Sending skeleton_template, or include_fingers set to true, returns 400";
	// texture_image_url "must be a PNG file ... Another image format returns
	// 400". The fake judges the format from a data URI's media type or a URL
	// path's extension and lets a URL without one pass. Constructed refusal
	// texts.
	if message := rigAnimationTypeError(request); message != "" {
		return message
	}
	if texture, _ := request["texture_image_url"].(string); strings.TrimSpace(texture) != "" && !pngLocator(texture) {
		return "The texture_image_url image isn't a PNG file."
	}
	return ""
}

// rigAnimationTypeError checks animation_type and the options a quadruped rig
// refuses.
func rigAnimationTypeError(request map[string]any) string {
	value, present := request["animation_type"]
	if !present {
		return ""
	}
	kind, _ := value.(string)
	switch kind {
	case "biped":
		return ""
	case "quadruped":
		if _, sent := request["skeleton_template"]; sent {
			return "skeleton_template works only with a biped rig"
		}
		if request["include_fingers"] == true {
			return "include_fingers works only with a biped rig"
		}
		return ""
	}
	return "animation_type must be biped or quadruped"
}

// pngLocator reports whether a texture locator can be a PNG: a data URI of
// image/png, or a URL whose path has no extension or the .png one.
func pngLocator(locator string) bool {
	locator = strings.TrimSpace(locator)
	if rest, isData := strings.CutPrefix(locator, "data:"); isData {
		mediaType, _, _ := strings.Cut(rest, ";")
		mediaType, _, _ = strings.Cut(mediaType, ",")
		return strings.EqualFold(strings.TrimSpace(mediaType), "image/png")
	}
	parsed, err := url.Parse(locator)
	if err != nil {
		return true
	}
	extension := strings.ToLower(path.Ext(parsed.Path))
	return extension == "" || extension == ".png"
}

// riggingFingersQuadMessage is the beginning of the real refusal of include_fingers
// for a model made with quad topology: HTTP 400 with a message-only body, observed
// 2026-11-12 on a rigging of a quad-topology model (#294). The record that
// survived ends the sentence with an ellipsis, so the fake sends the words that
// are known and no more.
const riggingFingersQuadMessage = "include_fingers is not available for a quad-topology model. Set include_fingers to false"

// riggingSourceError is the one refusal of a rigging request that depends on the
// source task: finger bones for a model made with quad topology (real, #294). The
// topology is the one the source task's own request, or the request chain behind
// it, names (core.Store.topologyOf). A source whose requests name none, or a
// model_url upload, is accepted: the fake cannot tell its topology.
func riggingSourceError(request map[string]any, sourceTopology string) string {
	if fingers, _ := request["include_fingers"].(bool); fingers && sourceTopology == "quad" {
		return riggingFingersQuadMessage
	}
	return ""
}

// patchPrintAnalyze adds the keys the sanitizer cannot hold in a committed
// sample (see OmittedKeys): the official report carries printability._version.
func patchPrintAnalyze(body map[string]any) {
	if report, ok := body["printability"].(map[string]any); ok {
		report["_version"] = "v1"
	}
}

var endpoints = buildEndpoints()

func buildEndpoints() []fakeupstream.Endpoint {
	out := []fakeupstream.Endpoint{
		{Method: "GET", Path: "/balance", Evidence: fakeupstream.EvidenceReal, Sample: "GET /openapi/v1/balance",
			DocURL: docsRoot + "balance", CheckedAt: captured},
		{Method: "GET", Path: "/assets/{task_id}/{name}", Evidence: fakeupstream.EvidenceConstructed,
			DocURL: docsRoot + "remesh", CheckedAt: captured,
			Note: "the official CDN host is not part of the API; this route serves the fixed files a task's URLs name. The content types of glb, png, fbx, stl, 3mf, obj, mtl and usdz are as the 2026-11-20 window observed them (obj is model/obj, usdz application/octet-stream)"},
	}
	for _, kind := range kinds {
		page := docsRoot + kind.Path[1:]
		switch kind.Path {
		case "/rigging":
			page = docsRoot + "rigging"
		case "/animations":
			page = docsRoot + "animation"
		case "/print/analyze":
			page = docsRoot + "analyze-printability"
		case "/print/multi-color":
			page = docsRoot + "multi-color-print"
		}
		checkedAt := captured
		switch kind.Path {
		case "/rigging", "/animations", "/print/analyze", "/print/multi-color", "/resize":
			checkedAt = docsChecked
		}
		var parts []string
		switch kind.Path {
		case "/rigging":
			parts = append(parts, "an input over 320000 faces is accepted (202) and the task ends FAILED with an invalid_input task_error (observed with a 300000-face quad remesh); the documentation says 400, the official API did not answer it at submit; the rig-non-humanoid scenario answers the documented 422 (constructed from docs); the request values skeleton_template (nine templates), include_fingers (not with texture_image_url) and height_meters (not negative) are checked as the page read on 2026-11-12 documents them, which has not been requested from the real API (riggingRequestError); include_fingers on a source task made with quad topology answers the real HTTP 400 {message} observed 2026-11-12 (#294, riggingSourceError)")
		case "/print/analyze":
			parts = append(parts, "the report's printability._version key cannot be held by a sample (a policy key must start with a letter or a digit), so the draft drops it and the fake adds it back")
		case "/resize":
			parts = append(parts, "a request that names none of resize_height, resize_longest_side and auto_size, or more than one, answers the documented 400 (resizeRequestError; constructed from docs, checked "+docsChecked+")")
		case "/image-to-3d", "/multi-image-to-3d", "/retexture":
			parts = append(parts, "the 2026-10-29 samples were captured with the cheapest texture options, so texture_urls and the PBR maps were constructed from the documented texture fields (docs checked "+docsChecked+"); the 2026-11-20 window re-captured image-to-3d and retexture with the Catalog defaults (textures and PBR on) and their PBR maps are real, as are multi-image-to-3d's since 2026-11-23 (meshy-openapi-v1-multi-image-to-3d-2026-11-23.candidate.json)")
		}
		switch kind.Path {
		case "/text-to-image", "/image-to-image":
			parts = append(parts, "the create is checked as the pages read on 2026-11-24 document it (#542): ai_model required and one of ten, aspect_ratio one of seven with 3:2 and 2:3 for the GPT Image models only, generate_multi_view and aspect_ratio conflict, and image-to-image takes 1 to 5 reference_image_urls or an input_task_id of a succeeded text-to-image or image-to-image task (which wins); none of the refusals was requested from the real API (constructed)")
		case "/image-to-3d", "/multi-image-to-3d":
			parts = append(parts, "the option combinations of the pages read on 2026-11-24 answer 400 (#542, core.ImageTo3DRequestError): geometry_resolution 2k needs meshy-7.1, latest or meshy-7 and 4k needs meshy-7.1 or latest (multi-image takes no 4k), texture_model, texture_resolution 4k/8k with meshy-6-lite or meshy-n1 (the pricing page's NS cells, read as 400), PBR with meshy-n1, and for multi-image 1 to 4 image_urls and texture_image_urls with their conflicts; the refusal texts are constructed. Single-image enable_pbr true with should_texture false answers the 400 of the #581 real probe (2026-11-25, its message verbatim, meshy-openapi-v1-image-to-3d-untextured-pbr-2026-11-25.candidate.json). The success bodies of the 1.9 real round (2026-11-24, #568: a meshy-t2.5 image-to-3d with a 2k PBR texture and a meshy-7.1 multi-image-to-3d of two views with 2k geometry and an accepted 8K PBR texture) are the newest samples (meshy-openapi-v1-real-round-2026-11-24.candidate.json); their creates carried inlined pictures over the recorder's 1 MiB limit and were not kept")
		case "/retexture":
			parts = append(parts, "the create is checked as the page read on 2026-11-24 documents it (#542, core.RetextureRequestError): one of text_style_prompt, image_style_url or multiview_image_urls is required, multiview excludes the other two and needs an explicit meshy-7 or meshy-n1, the prompt is at most 800 characters, and the meshy-6-lite and meshy-n1 conflicts; the page's ai_model enum has no meshy-7.1 and the fake answers 400 for it, which is constructed because the page does not say what an unlisted value does")
		case "/animations", "/text-to-motion":
			parts = append(parts, "the create is checked as the page read on 2026-11-24 documents it (#542): animations take exactly one of action_id, action_ids (at most ten, no repeats) or motion_task_id, text-to-motion a prompt of at most 400 characters, mode prime or swift and a duration of 2 to 10 seconds in half steps (constructed refusal texts)")
		}
		// #579: the rules of the pages read on 2026-11-25, all constructed.
		switch kind.Path {
		case "/image-to-3d", "/multi-image-to-3d":
			parts = append(parts, "the texture-option conflicts of the pages read on 2026-11-25 answer 400 (#579, core textureOptionError): image_enhancement with meshy-6-lite (single image also with Smart Topology), texture_prompt, remove_lighting true or a remeshed quad topology with texture_model meshy-n1, and remove_lighting with meshy-6-lite's default texture model (single image also with Smart Topology); single-image surface_mode is organic or hard. enable_pbr true with should_texture false (documented 400) is not checked because the Catalog sends its enable_pbr default either way (register gap)")
		case "/rigging":
			parts = append(parts, "the page read on 2026-11-25 adds (#579): animation_type biped or quadruped, a quadruped rig refuses skeleton_template and include_fingers true, and a texture_image_url that is not a PNG (data URI media type or URL extension) answers 400; the forbidden scenario answers the page's 403 text")
		case "/animations":
			parts = append(parts, "the page read on 2026-11-25 adds (#579): post_process.fps is 24, 25, 30 or 60, and motion_task_id on a quadruped rig (the rig request's animation_type) answers 400; whether an action matches the rig's rig_type is not modelled; the forbidden scenario answers the page's 403 text")
		case "/remesh":
			parts = append(parts, "the create is checked as the page read on 2026-11-25 documents it (#579, core.RemeshRequestError): target_formats from seven formats (blend included), topology quad or triangle, target_polycount 100 to 300000, at most one of resize_height, resize_longest_side and auto_size true, and convert_format_only true with an empty or null target_formats answer 400 (constructed refusal texts)")
		case "/resize":
			parts = append(parts, "resize_height and resize_longest_side must be numbers greater than 0 (page read 2026-11-25, #579; constructed refusal text)")
		}
		parts = append(parts,
			"consumed_credits follows the request from the Pricing page read on 2026-11-24 (#542, core.Credits): the price is reported from the first poll, a failed task reports 0, and the prices a real capture of 2026-11-20 to 2026-11-23 showed are asserted as such")
		// The newest multi-image-to-3d sample (2026-11-24) repeats
		// geometry_resolution and ultra_mode, so the kind answers both keys even
		// when the request names no geometry option (#497).
		parts = append(parts,
			"the 2026-11-20 window re-captured the processing chain: a successful rig answers result.basic_animations with six clips and the OBJ of a textured model comes with an MTL",
			"the resolution keys follow the request, not the template (core shapeResolution, #568): a 2k or 4k geometry_resolution is repeated with ultra_mode true (image-to-3d 2026-11-20, multi-image-to-3d 2026-11-23 and 2026-11-24), and texture_resolution with hd_texture true only for a 4k or 8k texture (8k repeated by retexture 2026-11-20 and multi-image-to-3d 2026-11-24; every real 2k task, the 2026-11-24 retexture among them, carried neither key; 4k never sent)",
			"the forbidden scenario answers the documented 403 {message} envelope (constructed from docs, checked "+docsChecked+")")
		note := strings.Join(parts, "; ")
		out = append(out,
			fakeupstream.Endpoint{Method: "POST", Path: kind.Path, Evidence: fakeupstream.EvidenceReal,
				Sample: "POST " + samplePrefix + kind.Path, DocURL: page, CheckedAt: checkedAt,
				Note: note},
			fakeupstream.Endpoint{Method: "GET", Path: kind.Path + "/{id}", Evidence: fakeupstream.EvidenceReal,
				Sample: "GET " + samplePrefix + kind.Path + "/{id}", DocURL: page, CheckedAt: checkedAt,
				Note: "IN_PROGRESS and SUCCEEDED polls are real (the engine templates from the newest capture of the type: 2026-11-24 for image-to-3d, multi-image-to-3d and retexture, 2026-11-20 or earlier for the others, whose 2026-11-24 polls matched them in structure and were not committed again); PENDING is constructed from the field docs (start/finish timestamps absent, preceding_tasks while queued); FAILED is real for rigging and constructed elsewhere (the terminal timestamps are added); CANCELED is constructed (task-canceled scenario)"},
			fakeupstream.Endpoint{Method: "DELETE", Path: kind.Path + "/{id}", Evidence: fakeupstream.EvidenceReal,
				Sample: "DELETE " + samplePrefix + kind.Path + "/{id}", DocURL: page, CheckedAt: checkedAt,
				Note: "a sample exists for the routes the alignment run deleted (text-to-image, image-to-3d, remesh, text-to-motion); the others answer the same; deleting an IN_PROGRESS task answers 409 with the Text to 3D page's text \"Task is IN_PROGRESS and cannot be deleted.\" (read 2026-11-25, constructed)"},
		)
	}
	return out
}

// Version is the Meshy OpenAPI v1 fake.
func Version() fakeupstream.Version {
	return fakeupstream.Version{
		Provider: "meshy", API: "openapi-v1", Prefix: Prefix, Official: OfficialBase,
		Endpoints: endpoints, Handler: newHandler,
	}
}

func newHandler(env *fakeupstream.Env) (http.Handler, error) {
	samples, err := fakeupstream.LoadSamples(sampleFS, sampleFiles...)
	if err != nil {
		return nil, err
	}
	return core.New(core.Config{
		Env: env, Samples: samples, SamplePrefix: samplePrefix, Prefix: Prefix,
		Kinds: kinds, Balance: true, Store: core.SharedStore(env), ContentTypes: ContentTypes,
	})
}

// ContentTypes are the content types the official asset CDN served in the
// alignment run, by file extension. The 2026-11-20 window requested every
// format the Catalog needs and confirmed glb, png, fbx, stl, 3mf, obj, mtl and
// usdz as below.
var ContentTypes = map[string]string{
	".glb": "model/gltf-binary", ".png": "image/png", ".fbx": "application/octet-stream",
	".stl": "model/stl", ".3mf": "model/3mf",
	".obj": "model/obj", ".mtl": "text/plain", ".usdz": "application/octet-stream",
}

func globSamples() ([]string, error) {
	entries, err := sampleFS.ReadDir("testdata")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".candidate.json") {
			names = append(names, "testdata/"+entry.Name())
		}
	}
	return names, nil
}
