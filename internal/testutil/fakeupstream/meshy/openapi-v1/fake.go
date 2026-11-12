package meshyopenapiv1

import (
	"embed"
	"encoding/json"
	"net/http"
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

// texturedUnlessDisabled follows the should_texture default (true).
func texturedUnlessDisabled(request map[string]any) bool {
	enabled, ok := request["should_texture"].(bool)
	return !ok || enabled
}

// kinds are the task routes of this version, in the order of the Catalog's
// Offerings. Every one has a captured sample of its own.
var kinds = []core.Kind{
	{Path: "/text-to-image", Type: "text-to-image", Need: [][]string{{"prompt"}}, Real: true},
	{Path: "/image-to-image", Type: "image-to-image", Need: [][]string{{"prompt"}, {"reference_image_urls"}}, Real: true},
	{Path: "/image-to-3d", Type: "image-to-3d", Need: [][]string{{"image_url", "input_task_id"}}, Sources: []string{"input_task_id"}, Real: true,
		Textured: texturedUnlessDisabled},
	{Path: "/multi-image-to-3d", Type: "multi-image-to-3d", Need: [][]string{{"image_urls", "input_task_id"}}, Sources: []string{"input_task_id"}, Real: true,
		Textured: texturedUnlessDisabled},
	{Path: "/remesh", Type: "remesh", Need: [][]string{{"input_task_id", "model_url"}}, Sources: []string{"input_task_id"}, Real: true},
	{Path: "/convert", Type: "convert", Need: [][]string{{"input_task_id", "model_url"}, {"target_formats"}}, Sources: []string{"input_task_id"}, Real: true},
	{Path: "/retexture", Type: "retexture", Need: [][]string{{"input_task_id", "model_url"}}, Sources: []string{"input_task_id"}, Real: true,
		Textured: func(map[string]any) bool { return true }},
	{Path: "/rigging", Type: "rig", Need: [][]string{{"input_task_id", "model_url"}}, Sources: []string{"input_task_id"}, Fidelity: "skeleton", Real: true,
		Validate: riggingRequestError},
	{Path: "/animations", Type: "animate", Need: [][]string{{"rig_task_id"}, {"action_id", "action_ids", "motion_task_id"}}, Sources: []string{"rig_task_id", "motion_task_id"}, Fidelity: "animation", Real: true},
	{Path: "/uv-unwrap", Type: "uv-unwrap", Need: [][]string{{"input_task_id", "model_url"}}, Sources: []string{"input_task_id"}, Real: true},
	{Path: "/resize", Type: "resize", Need: [][]string{{"input_task_id", "model_url"}}, Sources: []string{"input_task_id"}, Real: true},
	{Path: "/print/analyze", Type: "print-analyze", Need: [][]string{{"input_task_id", "model_url"}}, Sources: []string{"input_task_id"}, Real: true,
		Patch: patchPrintAnalyze},
	{Path: "/print/multi-color", Type: "print-multi-color", Need: [][]string{{"input_task_id", "model_url"}}, Sources: []string{"input_task_id"}, Real: true},
	{Path: "/text-to-motion", Type: "text-to-motion", Need: [][]string{{"prompt"}, {"duration"}}, Fidelity: "animation", Real: true},
}

// rigSkeletonTemplates are the skeleton_template values of the Rigging page
// (https://docs.meshy.ai/en/api/rigging-and-animation, read 2026-11-12); meshyrig is
// the default.
var rigSkeletonTemplates = []string{
	"meshyrig", "mixamo", "ue5_mannequin", "max_biped", "unity_humanoid", "maya_human_ik",
	"blender_rigify_basic_human", "cc5_hd_neutral_base", "daz_genesis9",
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
			Note: "the official CDN host is not part of the API; this route serves the fixed files a task's URLs name. Content types of glb, png, fbx, stl and 3mf are as observed; obj, mtl and usdz were not requested in the run and follow the Meshy adapter's notes"},
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
		case "/rigging", "/animations", "/print/analyze", "/print/multi-color":
			checkedAt = docsChecked
		}
		note := ""
		switch kind.Path {
		case "/rigging":
			note = "an input over 320000 faces is accepted (202) and the task ends FAILED with an invalid_input task_error (observed with a 300000-face quad remesh); the documentation says 400, the official API did not answer it at submit; the rig-non-humanoid scenario answers the documented 422 (constructed from docs); the request values skeleton_template (nine templates), include_fingers (not with texture_image_url) and height_meters (not negative) are checked as the page read on 2026-11-12 documents them, which has not been requested from the real API (riggingRequestError)"
		case "/print/analyze":
			note = "the report's printability._version key cannot be held by the sample sanitizer; the fake adds it back"
		case "/image-to-3d", "/multi-image-to-3d", "/retexture":
			note = "the samples were captured with the cheapest texture options; texture_urls and the PBR maps a default Catalog request asks for are constructed from the documented texture fields (docs checked " + docsChecked + ")"
		}
		if note != "" {
			note += "; "
		}
		note += "the forbidden scenario answers the documented 403 {message} envelope (constructed from docs, checked " + docsChecked + ")"
		out = append(out,
			fakeupstream.Endpoint{Method: "POST", Path: kind.Path, Evidence: fakeupstream.EvidenceReal,
				Sample: "POST " + samplePrefix + kind.Path, DocURL: page, CheckedAt: checkedAt,
				Note: note},
			fakeupstream.Endpoint{Method: "GET", Path: kind.Path + "/{id}", Evidence: fakeupstream.EvidenceReal,
				Sample: "GET " + samplePrefix + kind.Path + "/{id}", DocURL: page, CheckedAt: checkedAt,
				Note: "IN_PROGRESS and SUCCEEDED polls are real; PENDING is constructed from the field docs (start/finish timestamps absent, preceding_tasks while queued); FAILED is real for rigging and constructed elsewhere (the terminal timestamps are added); CANCELED is constructed (task-canceled scenario)"},
			fakeupstream.Endpoint{Method: "DELETE", Path: kind.Path + "/{id}", Evidence: fakeupstream.EvidenceReal,
				Sample: "DELETE " + samplePrefix + kind.Path + "/{id}", DocURL: page, CheckedAt: checkedAt,
				Note: "a sample exists for the routes the alignment run deleted (text-to-image, image-to-3d, remesh, text-to-motion); the others answer the same"},
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
// alignment run, by file extension.
var ContentTypes = map[string]string{
	".glb": "model/gltf-binary", ".png": "image/png", ".fbx": "application/octet-stream",
	".stl": "model/stl", ".3mf": "model/3mf",
	// Not requested in the run; the Meshy adapter documents the CDN labelling.
	".obj": "application/x-tgif", ".mtl": "text/plain", ".usdz": "model/vnd.usdz+zip",
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
