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
)

// textTo3D is the route of this version. A request's mode decides the task
// type: preview or refine.
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
}

var endpoints = []fakeupstream.Endpoint{
	{Method: "POST", Path: "/text-to-3d", Evidence: fakeupstream.EvidenceReal, Sample: "POST /openapi/v2/text-to-3d",
		DocURL: docsRoot + "text-to-3d", CheckedAt: captured,
		Note: "preview and refine are one route; the task type follows mode"},
	{Method: "GET", Path: "/text-to-3d/{id}", Evidence: fakeupstream.EvidenceReal, Sample: "GET /openapi/v2/text-to-3d/{id}",
		DocURL: docsRoot + "text-to-3d", CheckedAt: captured,
		Note: "IN_PROGRESS and SUCCEEDED polls are real for both types; PENDING and FAILED reuse the in-progress structure (a failed one carries the task_error object observed on a failed rigging task)"},
	{Method: "DELETE", Path: "/text-to-3d/{id}", Evidence: fakeupstream.EvidenceReal, Sample: "DELETE /openapi/v2/text-to-3d/{id}",
		DocURL: docsRoot + "text-to-3d", CheckedAt: captured},
	{Method: "GET", Path: "/assets/{task_id}/{name}", Evidence: fakeupstream.EvidenceConstructed,
		DocURL: docsRoot + "text-to-3d", CheckedAt: captured,
		Note: "the official CDN host is not part of the API; this route serves the fixed files the task's model_urls and thumbnail_url name (content types as observed)"},
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
	core.SetContentTypes(ContentTypes)
	return core.New(core.Config{
		Env: env, Samples: samples, SamplePrefix: samplePrefix, Prefix: Prefix,
		Kinds: []core.Kind{textTo3D}, Store: core.SharedStore(env),
	})
}

// ContentTypes are the content types the official asset CDN served in the
// alignment run, by file extension.
var ContentTypes = map[string]string{".glb": "model/gltf-binary", ".png": "image/png"}
