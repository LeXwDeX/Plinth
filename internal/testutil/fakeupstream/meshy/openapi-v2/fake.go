package meshyopenapiv2

import (
	"embed"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/core"
)

//go:embed testdata/meshy-openapi-v2*.candidate.json
var sampleFS embed.FS

var sampleFiles = []string{"testdata/meshy-openapi-v2.candidate.json"}

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
	// The remesh options of the preview, as the Text to 3D page documents them
	// (core/generation.go).
	Validate: core.GenerationRequestError,
}

var endpoints = []fakeupstream.Endpoint{
	{Method: "POST", Path: "/text-to-3d", Evidence: fakeupstream.EvidenceReal, Sample: "POST /openapi/v2/text-to-3d",
		DocURL: docsRoot + "text-to-3d", CheckedAt: captured,
		Note: "preview and refine are one route; the task type follows mode; the forbidden scenario answers the documented 403 {message} envelope (constructed from docs, checked " + docsChecked + ")"},
	{Method: "GET", Path: "/text-to-3d/{id}", Evidence: fakeupstream.EvidenceReal, Sample: "GET /openapi/v2/text-to-3d/{id}",
		DocURL: docsRoot + "text-to-3d", CheckedAt: captured,
		Note: "IN_PROGRESS and SUCCEEDED polls are real for both types; PENDING is constructed from the field docs (started_at/finished_at 0, preceding_tasks while queued); FAILED is constructed from the in-progress structure (a failed one carries the task_error object and no expires_at); CANCELED is constructed (task-canceled scenario); texture_urls follow the request's enable_pbr in a refine (constructed from docs); geometry_resolution/ultra_mode are not echoed because the Adapter does not read them (side evidence of #203)"},
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
// extension. glb and png were observed in the v2 alignment run; fbx, stl, 3mf,
// obj, mtl and usdz follow the v1 alignment's observations on the same CDN
// (assets.meshy.ai / cdn.meshy.ai) and the Meshy adapter's notes, because the
// v2 run requested glb only while the Catalog's default offering requires all
// six model formats.
var ContentTypes = map[string]string{
	".glb": "model/gltf-binary", ".png": "image/png", ".fbx": "application/octet-stream",
	".stl": "model/stl", ".3mf": "model/3mf", ".obj": "application/x-tgif",
	".mtl": "text/plain", ".usdz": "model/vnd.usdz+zip",
}
