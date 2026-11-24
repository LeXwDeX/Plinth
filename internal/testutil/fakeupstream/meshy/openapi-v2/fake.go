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
	// A refine takes a succeeded preview of this account, and every other
	// answer is a 400 ("invalid, not yours, or not ready").
	SourceTypes:          map[string][]string{"preview_task_id": {"text-to-3d-preview"}},
	SourceNotFoundStatus: http.StatusBadRequest,
	Textured: func(request map[string]any) bool {
		mode, _ := request["mode"].(string)
		return mode == "refine"
	},
	// The remesh options of the preview and the stage-specific fields, as the
	// Text to 3D page documents them (core/generation.go).
	Validate: core.TextTo3DRequestError,
	// A preview repeats a 2k or 4k geometry_resolution (2026-11-20, 2026-11-22);
	// a refine sends none, so its object keeps the template's (none).
	EchoResolution: true,
}

var endpoints = []fakeupstream.Endpoint{
	{Method: "POST", Path: "/text-to-3d", Evidence: fakeupstream.EvidenceReal, Sample: "POST /openapi/v2/text-to-3d",
		DocURL: docsRoot + "text-to-3d", CheckedAt: captured,
		Note: "the create is checked as the page read on 2026-11-24 documents it (#542): a preview needs a prompt of at most 800 characters, a refine a preview_task_id that names a succeeded preview of the account (anything else is a 400, not a 404), geometry_resolution 2k needs meshy-7.1, latest or meshy-7 and 4k needs meshy-7.1 or latest, and the model, mode and format enums are enforced; a refine carries no ai_model (the Adapter sends none) yet any model name passes, since the real refine of 2026-11-20 sent meshy-7.1 and was accepted; consumed_credits follows the Pricing page (a preview 20 or 5, plus 5 for 2k or 4k geometry; a refine 10, or 15 at 8k) and the real prices of 2026-11-20, 2026-11-22 and 2026-11-24 (t2.5 preview 5, its 2k refine 10) are asserted; the refusal texts are constructed. preview and refine are one route; the task type follows mode; the forbidden scenario answers the documented 403 {message} envelope (constructed from docs, checked " + docsChecked + "). The 2026-11-20 window shows the create answering the whole queued task object (status PENDING, the task's own id and result, expires_at/started_at/finished_at null, preceding_tasks) with the accepted request echoed under input, which the fake reproduces; the 2026-10-29 sample answered only {result}"},
	{Method: "GET", Path: "/text-to-3d/{id}", Evidence: fakeupstream.EvidenceReal, Sample: "GET /openapi/v2/text-to-3d/{id}",
		DocURL: docsRoot + "text-to-3d", CheckedAt: captured,
		Note: "IN_PROGRESS and SUCCEEDED polls are real for both types; since the 2026-11-20 capture the object adds input (the request as sent), expires_at null and an output object with model_urls/seed/texture_urls/thumbnail_url, and the 1.9 real round of 2026-11-24 (#568, a meshy-t2.5 Smart Topology preview of 15000 faces, 5 credits, and its 2k PBR refine, 10 credits) is the newest. The resolution keys follow the request, not the template: a preview repeats a 2k or 4k geometry_resolution with ultra_mode true (2026-11-20, 2026-11-22) and a t2.5 preview, which names none, carries neither; a refine repeats texture_resolution with hd_texture true only for a 4k or 8k texture (8k on 2026-11-22; the 2k refines of 2026-11-20 and 2026-11-24 carried neither). PENDING is constructed from the field docs and the observed poll (started_at/finished_at 0, preceding_tasks while queued; the create object has them null instead); FAILED is constructed from the in-progress structure (a failed one carries the task_error object and expires_at null); CANCELED is constructed (task-canceled scenario); texture_urls follow the request's enable_pbr in a refine, and a preview keeps the captured normal map when enable_pbr was asked for"},
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
