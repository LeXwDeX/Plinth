package meshyopenapiv2

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/core"
)

//go:embed testdata/meshy-openapi-v2*.candidate.json
var sampleFS embed.FS

// sampleFiles are the committed candidates; the glob keeps a re-capture in. The
// engine templates from the newest sample of each key.
var sampleFiles = func() []string {
	names, err := fs.Glob(sampleFS, "testdata/meshy-openapi-v2*.candidate.json")
	if err != nil || len(names) == 0 {
		panic("meshy/openapi-v2: no committed samples")
	}
	return names
}()

// Prefix is where this version is mounted. The Meshy adapter appends
// openapi/v2/<endpoint> to meshy_base_url, so a stack points meshy_base_url at
// <fake>/meshy.
const Prefix = "/meshy/openapi/v2"

// samplePrefix is the path prefix the official API uses; the committed samples
// record it.
const samplePrefix = "/openapi/v2"

const (
	docsRoot = "https://docs.meshy.ai/en/api/"
	// captured is the date the real samples were taken from the official API.
	captured = "2026-10-29"
	// docsChecked is the date the constructed fault answers were checked
	// against the official documentation.
	docsChecked = "2026-11-08"
)

// textTo3D is the route of this version. A request's mode decides the task
// type: preview or refine. Only refine is textured (the preview is a mesh-only
// task), so the OBJ MTL and texture_urls follow the mode.
var textTo3D = core.Kind{
	Path: "/text-to-3d", Type: "text-to-3d-preview", Real: true,
	TypeOf: func(request map[string]any) string {
		if mode, _ := request["mode"].(string); mode == "refine" {
			return "text-to-3d-refine"
		}
		return "text-to-3d-preview"
	},
	Need:    [][]string{{"mode"}},
	Sources: []string{"preview_task_id"},
	Textured: func(request map[string]any) bool {
		mode, _ := request["mode"].(string)
		return mode == "refine"
	},
	// The remesh options of the preview and the stage-specific fields, as the
	// Text to 3D page documents them (core/generation.go).
	Validate: core.TextTo3DRequestError,
}

var endpoints = []fakeupstream.Endpoint{
	{Method: "POST", Path: "/text-to-3d", Evidence: fakeupstream.EvidenceReal, Sample: "POST /openapi/v2/text-to-3d",
		DocURL: docsRoot + "text-to-3d", CheckedAt: captured,
		Note: "preview and refine are one route; the task type follows mode; the forbidden scenario answers the documented 403 {message} envelope (constructed from docs, checked " + docsChecked + "). The 2026-11-20 window shows the create answering the whole queued task object (status PENDING, the task's own id and result, expires_at/started_at/finished_at null, preceding_tasks) with the accepted request echoed under input, which the fake reproduces; the 2026-10-29 sample answered only {result}"},
	{Method: "GET", Path: "/text-to-3d/{id}", Evidence: fakeupstream.EvidenceReal, Sample: "GET /openapi/v2/text-to-3d/{id}",
		DocURL: docsRoot + "text-to-3d", CheckedAt: captured,
		Note: "IN_PROGRESS and SUCCEEDED polls are real for both types and the 2026-11-20 capture is the newest: its object adds input (the request as sent), expires_at null, geometry_resolution/texture_resolution as sent, ultra_mode/hd_texture and an output object with model_urls/seed/texture_urls/thumbnail_url. PENDING is constructed from the field docs and the observed poll (started_at/finished_at 0, preceding_tasks while queued; the create object has them null instead); FAILED is constructed from the in-progress structure (a failed one carries the task_error object and expires_at null); CANCELED is constructed (task-canceled scenario); texture_urls follow the request's enable_pbr in a refine, and a preview keeps the captured normal map when enable_pbr was asked for"},
	{Method: "DELETE", Path: "/text-to-3d/{id}", Evidence: fakeupstream.EvidenceReal, Sample: "DELETE /openapi/v2/text-to-3d/{id}",
		DocURL: docsRoot + "text-to-3d", CheckedAt: captured},
	{Method: "GET", Path: "/assets/{task_id}/{name}", Evidence: fakeupstream.EvidenceConstructed,
		DocURL: docsRoot + "text-to-3d", CheckedAt: captured,
		Note: "the official CDN host is not part of the API; this route serves the fixed files the task's model_urls and thumbnail_url name. The content types of the six Catalog formats follow the v1 alignment's CDN observations and the Meshy adapter's notes (the v2 run requested glb only)"},
}

// Version is the Meshy OpenAPI v2 fake.
func Version() fakeupstream.Version {
	return fakeupstream.Version{
		Provider: "meshy", API: "openapi-v2", Prefix: Prefix, Official: OfficialBase,
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
		Kinds: []core.Kind{textTo3D}, Store: core.SharedStore(env), ContentTypes: ContentTypes,
		Shape: core.ShapeV2TextTo3D,
	})
}

// ContentTypes are the content types the official asset CDN serves, by file
// extension. The 2026-11-20 window requested every format the Catalog needs
// and confirmed glb, png, fbx, stl, 3mf, obj, mtl and usdz as below.
var ContentTypes = map[string]string{
	".glb": "model/gltf-binary", ".png": "image/png", ".fbx": "application/octet-stream",
	".stl": "model/stl", ".3mf": "model/3mf", ".obj": "model/obj",
	".mtl": "text/plain", ".usdz": "application/octet-stream",
}
