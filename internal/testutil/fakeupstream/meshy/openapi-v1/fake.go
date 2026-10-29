package meshyopenapiv1

import (
	"embed"
	"net/http"
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
)

// kinds are the task routes of this version, in the order of the Catalog's
// Offerings. Every one has a captured sample of its own.
var kinds = []core.Kind{
	{Path: "/text-to-image", Type: "text-to-image", Need: [][]string{{"prompt"}}, Real: true},
	{Path: "/image-to-image", Type: "image-to-image", Need: [][]string{{"prompt"}, {"reference_image_urls"}}, Real: true},
	{Path: "/image-to-3d", Type: "image-to-3d", Need: [][]string{{"image_url", "input_task_id"}}, Sources: []string{"input_task_id"}, Real: true},
	{Path: "/multi-image-to-3d", Type: "multi-image-to-3d", Need: [][]string{{"image_urls", "input_task_id"}}, Sources: []string{"input_task_id"}, Real: true},
	{Path: "/remesh", Type: "remesh", Need: [][]string{{"input_task_id", "model_url"}}, Sources: []string{"input_task_id"}, Real: true},
	{Path: "/convert", Type: "convert", Need: [][]string{{"input_task_id", "model_url"}, {"target_formats"}}, Sources: []string{"input_task_id"}, Real: true},
	{Path: "/retexture", Type: "retexture", Need: [][]string{{"input_task_id", "model_url"}}, Sources: []string{"input_task_id"}, Real: true},
	{Path: "/rigging", Type: "rig", Need: [][]string{{"input_task_id", "model_url"}}, Sources: []string{"input_task_id"}, Fidelity: "skeleton", Real: true},
	{Path: "/animations", Type: "animate", Need: [][]string{{"rig_task_id"}, {"action_id", "action_ids", "motion_task_id"}}, Sources: []string{"rig_task_id", "motion_task_id"}, Fidelity: "animation", Real: true},
	{Path: "/uv-unwrap", Type: "uv-unwrap", Need: [][]string{{"input_task_id", "model_url"}}, Sources: []string{"input_task_id"}, Real: true},
	{Path: "/resize", Type: "resize", Need: [][]string{{"input_task_id", "model_url"}}, Sources: []string{"input_task_id"}, Real: true},
	{Path: "/print/analyze", Type: "print-analyze", Need: [][]string{{"input_task_id", "model_url"}}, Sources: []string{"input_task_id"}, Real: true,
		Patch: patchPrintAnalyze},
	{Path: "/print/multi-color", Type: "print-multi-color", Need: [][]string{{"input_task_id", "model_url"}}, Sources: []string{"input_task_id"}, Real: true},
	{Path: "/text-to-motion", Type: "text-to-motion", Need: [][]string{{"prompt"}, {"duration"}}, Fidelity: "animation", Real: true},
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
		case "/rigging", "/animations":
			page = docsRoot + "rigging-and-animation"
		case "/print/analyze", "/print/multi-color":
			page = docsRoot + "print"
		}
		note := ""
		switch kind.Path {
		case "/rigging":
			note = "an input over 320000 faces is accepted (202) and the task ends FAILED with an invalid_input task_error (observed with a 300000-face quad remesh); the documentation says 400, the official API did not answer it at submit"
		case "/print/analyze":
			note = "the report's printability._version key cannot be held by the sample sanitizer; the fake adds it back"
		}
		out = append(out,
			fakeupstream.Endpoint{Method: "POST", Path: kind.Path, Evidence: fakeupstream.EvidenceReal,
				Sample: "POST " + samplePrefix + kind.Path, DocURL: page, CheckedAt: captured, Note: note},
			fakeupstream.Endpoint{Method: "GET", Path: kind.Path + "/{id}", Evidence: fakeupstream.EvidenceReal,
				Sample: "GET " + samplePrefix + kind.Path + "/{id}", DocURL: page, CheckedAt: captured,
				Note: "IN_PROGRESS and SUCCEEDED polls are real; PENDING reuses the in-progress structure; FAILED is real for rigging and reuses it elsewhere"},
			fakeupstream.Endpoint{Method: "DELETE", Path: kind.Path + "/{id}", Evidence: fakeupstream.EvidenceReal,
				Sample: "DELETE " + samplePrefix + kind.Path + "/{id}", DocURL: page, CheckedAt: captured,
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
	core.SetContentTypes(ContentTypes)
	return core.New(core.Config{
		Env: env, Samples: samples, SamplePrefix: samplePrefix, Prefix: Prefix,
		Kinds: kinds, Balance: true, Store: core.SharedStore(env),
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
