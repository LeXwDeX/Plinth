package tripov3

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

//go:embed testdata/tripo-v3-*.candidate.json
var sampleFS embed.FS

// sampleFiles are the committed candidates, one per Catalog Offering that was
// captured plus the account probes; the glob keeps newly captured files in.
var sampleFiles = func() []string {
	names, err := fs.Glob(sampleFS, "testdata/tripo-v3-*.candidate.json")
	if err != nil || len(names) == 0 {
		panic("tripo/v3: no committed samples")
	}
	return names
}()

// Prefix is where this version is mounted.
const Prefix = "/tripo/v3"

// samplePrefix is the path prefix the official API uses; the committed samples
// record it.
const samplePrefix = "/v3"

const docsRoot = "https://developers.tripo3d.ai/en/docs/"

// captured is the date the real samples were taken from the official API; for
// a real endpoint it is the check date (the fake was compared with the live
// response). researched is the date of the repository's own reading of the
// Tripo documentation (docs/3d-special-capabilities-meshy-tripo-research.md),
// the only evidence behind a constructed endpoint: the documentation site is a
// single-page app that cannot be fetched, so it was not re-read on capture day.
const (
	captured   = "2026-10-29"
	researched = "2026-09-30"
)

// Task statuses of the official API. queued, running, success and failed are
// observed in the committed samples. The task-query page also lists cancelled
// and says an unknown value may occur; the task-lifecycle page reports the
// internal banned and expired outcomes as failed, so the fake never emits
// them. cancelled and unknown have no real sample and are constructed (see
// doc.go).
const (
	statusQueued    = "queued"
	statusRunning   = "running"
	statusSuccess   = "success"
	statusFailed    = "failed"
	statusCancelled = "cancelled"
	statusUnknown   = "unknown"
)

// Tripo-local scenario words, parsed from the same X-Fake-Scenario header and
// "fake-" key prefix as the shared list. They exist because a scenario belongs
// to a connection's API key, while these faults belong to one request or one
// private child:
//
//   - task-unknown: the documented unknown poll status (fakeupstream.Scenario
//     has task-canceled but no task-unknown).
//   - convert-task-failed / convert-task-canceled / convert-task-unknown: a
//     models/convert child ends failed, cancelled, or unknown forever
//     (Billing: "Failed and cancelled tasks are not charged.", read
//     2026-11-22; a failed child reports credits_consumed 0).
//   - rate-limit-no-retry-after / concurrency-limit-no-retry-after: the same
//     two 429 answers without a Retry-After header, so the client's own
//     backoff is exercised (the rate-limits page only says to honour the
//     header when the response carries one).
//   - upload-failed: POST /files answers 500. An upload creates no task, so
//     this is a retryable transport error, never an ambiguous submission.
const (
	scenarioTaskUnknown           = "task-unknown"
	scenarioConvertTask           = "convert-task"
	scenarioRateLimitNoRetryAfter = "rate-limit-no-retry-after"
	scenarioConcurrencyNoRetry    = "concurrency-limit-no-retry-after"
	scenarioUploadFailed          = "upload-failed"
)

// convertTaskOutcome is the terminal status a convert-task-<outcome> child
// ends in; any other or missing suffix means failed.
func convertTaskOutcome(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get(fakeupstream.ScenarioHeader))
	if value == "" {
		value, _ = strings.CutPrefix(fakeupstream.BearerKey(r), fakeupstream.KeyPrefix)
	}
	rest, found := strings.CutPrefix(value, scenarioConvertTask+"-")
	if !found {
		return ""
	}
	switch rest {
	case "failed":
		return statusFailed
	case "canceled", "cancelled":
		return statusCancelled
	case "unknown":
		return statusUnknown
	}
	return statusFailed
}

// noRetryAfter answers whether the request asks for a 429 without Retry-After.
func noRetryAfter(r *http.Request) bool {
	return requestedScenario(r, scenarioRateLimitNoRetryAfter) ||
		requestedScenario(r, scenarioConcurrencyNoRetry)
}

// docsChecked is when the official error, status and upload pages behind the
// constructed responses were read.
const docsChecked = "2026-11-08"

// Fixed values. The fake promises structure, not realism.
const (
	fixedBalance   = 5000.0
	fixedTimestamp = "2026-01-01T00:00:00Z"
)

// kind describes one task-creating endpoint.
type kind struct {
	Path string
	// Type is the task type the official API reports.
	Type string
	// Base is the real task type whose poll samples give this kind its
	// structure (the kind itself when real).
	Base string
	// Required names request fields that must be present.
	Required []string
	// Real marks kinds with a captured sample of their own.
	Real    bool
	DocPage string
}

// The observed behavior behind each kind is in testdata (every kind has a real
// sample since #89); a constructed kind would borrow the structure of its Base
// type and say so in the endpoint table.
var kinds = []kind{
	{Path: "/generation/text-to-model", Type: "text_to_model", Base: "text_to_model", Required: []string{"model", "prompt"}, Real: true, DocPage: "generation-text-to-model/standard"},
	{Path: "/animations/rig-check", Type: "animate_prerigcheck", Base: "animate_prerigcheck", Required: []string{"input"}, Real: true, DocPage: "animations-rig-check"},
	{Path: "/animations/rig", Type: "animate_rig", Base: "animate_rig", Required: []string{"input", "model"}, Real: true, DocPage: "animations-rig"},
	{Path: "/animations/retarget", Type: "animate_retarget", Base: "animate_retarget", Required: []string{"input"}, Real: true, DocPage: "animations-retarget"},
	{Path: "/mesh/segment", Type: "mesh_segmentation", Base: "mesh_segmentation", Required: []string{"input", "model"}, Real: true, DocPage: "mesh-segment"},
	{Path: "/mesh/complete", Type: "mesh_completion", Base: "mesh_completion", Required: []string{"input", "model"}, Real: true, DocPage: "mesh-complete"},
	{Path: "/models/convert", Type: "convert_model", Base: "convert_model", Required: []string{"input", "format"}, Real: true, DocPage: "models-convert"},
	{Path: "/generation/image-to-model", Type: "image_to_model", Base: "image_to_model", Required: []string{"model", "input"}, Real: true, DocPage: "generation-image-to-model/standard"},
	{Path: "/generation/image-to-splat", Type: "image_to_splat", Base: "image_to_splat", Required: []string{"input"}, Real: true, DocPage: "generation-image-to-splat"},
	{Path: "/generation/image-to-multiview", Type: "image_to_multiview", Base: "image_to_multiview", Required: []string{"input"}, Real: true, DocPage: "generation-image-to-multiview"},
	{Path: "/generation/edit-multiview", Type: "edit_multiview", Base: "edit_multiview", Required: []string{"input"}, Real: true, DocPage: "generation-edit-multiview"},
	{Path: "/models/texture", Type: "texture_model", Base: "texture_model", Required: []string{"input"}, Real: true, DocPage: "models-texture"},
	// model is optional on the official page (v2.0 when omitted); decimate.go holds the rest of the request.
	{Path: "/mesh/decimate", Type: "highpoly_to_lowpoly", Base: "highpoly_to_lowpoly", Required: []string{"input"}, Real: true, DocPage: "mesh-decimate"},
	// multiview-to-model has no template sample (the real task of 2026-11-19 is evidence, not a sample
	// in sampleFS: evidence_multiview_3mf_test.go holds the fake to it): it is constructed from the
	// official documentation, borrows the image_to_model poll structure and adds what that task
	// showed (multiview.go: the request under input; artifactName: the WebP render).
	{Path: "/generation/multiview-to-model", Type: "multiview_to_model", Base: "image_to_model", Required: []string{"model"}, DocPage: "generation-multiview-to-model/standard"},
	// Smart Segmentation reports smartsegment_image or smartsegment_model by seg_type; both were captured.
	{Path: "/mesh/smartsegment", Type: "smartsegment_image", Base: "smartsegment_image", Required: []string{"input", "seg_type"}, Real: true, DocPage: "mesh-smartsegment"},
}

// realKindNotes says what the 2026-10-29 captures of the seven endpoints added in
// #89 showed (each was requested once with its cheapest option; the credits are
// what the official poll reported).
var realKindNotes = map[string]string{
	"/generation/image-to-model":     "captured: task type image_to_model, 20 credits for a geometry-only v3.1 model; the output has model_url and rendered_image_url (no generated_image_url, which only a text prompt produces); a P-series request with quad=true delivers an FBX and no GLB (observed 2026-11-12, #294, p2_quad.go)",
	"/generation/text-to-model":      "a P-series request with quad=true delivers an FBX and no GLB (observed 2026-11-12, #294, p2_quad.go); the rest is the captured text_to_model structure",
	"/generation/image-to-splat":     "captured: task type image_to_splat, 30 credits; the only output is model_url (a .splat file)",
	"/generation/image-to-multiview": "captured: task type image_to_multiview, 10 credits; the output is the object generate_multiview_image with front/left/back/right_view_url (JPEG); re-captured on 2026-11-20 through the production path (POST /v3/files then the task), where the running polls answered output {} and the task echoed input.file.object {bucket tripo-data, key} with model_version default (tripo-v3-multiview-2026-11-20.candidate.json)",
	"/generation/edit-multiview":     "captured: task type edit_multiview, 5 credits; input.original_task_id is the multiview task and the output carries only the edited views (one prompt, one front_view_url)",
	"/models/texture":                "captured: task type texture_model, 10 credits at texture_quality standard without PBR; the output has model_url and rendered_image_url",
	"/mesh/decimate":                 "captured: the task type is highpoly_to_lowpoly (not mesh_decimation as assumed), 35 credits; the output has model_url and rendered_image_url",
	"/models/convert":                "captured twice. 2026-10-29: an STL export of a segmentation result with PNG textures of 512 pixels (the task echoes its request under input, the output has only model_url). 2026-11-16 (#305, evidence-305-rig-convert.candidate.json): a GLTF and an FBX export of a v3.0 rig's GLB with PNG textures of 2048 pixels and with_animation, 10 credits each, shown already while the task runs (creation from a URL took 47 to 48 seconds; the poll names a new original_model_id); the finished GLTF and FBX tasks add output.normalize_scale and output.normalize_translation, the move that put the middle of the bounding box on the origin because pivot_to_center_bottom was not set; the GLB keeps the skin, the textures follow the request. What the fake writes for pivot_to_center_bottom, for a texture larger than the source's and for the formats the runs did not cover is constructed from the official page (convert.go). A third capture, 2026-11-19 (#386, evidence-386-convert-3mf.candidate.json): a 3MF export of a multiview model named by its task id, with nothing in the request but input and format, 5 credits shown from the queued poll on; the task echoes {format, original_model_id}, the output has only model_url (no normalize_scale or normalize_translation for a 3MF), and the delivered file is a 35,952,441-byte ZIP64 3MF package with a Tripo vendor namespace (AIGC label) that the archive isolation refused until the namespace was listed. The fake delivers a package of that structure (tripo3mf.go), not the generic 3MF fixture",
	"/animations/rig":                "the response is captured (rig_type biped, spec tripo, both v2.5 and v3.0; 25 credits on v3.0-20260909, shown while the task runs, observed 2026-11-16); the request values beyond that are constructed from the official page read on " + rigDocsChecked + " and not yet observed: rig_type (biped, quadruped, hexapod, octopod, avian, serpentine, aquatic, others), spec (tripo, mixamo, actorcore, unity, unreal, vrm), out_format, which of them exist only under v3.0, and a spec having no effect on a non-biped rig; a poll repeats spec, out_format and the rig type (named topology, as captured)",
	"/animations/retarget":           "the response is captured (preset:walk on a v3.0 biped rig; a v2 rig ended failed, code 1004); the request values beyond that are constructed from the official page read on " + rigDocsChecked + " and not yet observed: animation XOR animations (at most 5), the 11 v3.0 biped presets, the five creature presets (quadruped walk, hexapod walk, octopod walk, serpentine march, aquatic march) and the v1.0 preset:biped: names; a preset that does not fit its rig ends failed with code 1004 like the captured v2 case, which is an assumption",
	"/mesh/smartsegment":             "captured for both seg_type values: smartsegment_image (85 credits; the first request, on a shaded sphere, ended failed with error 1001 \"Segmentation pipeline failed\" and was not charged, a second picture with distinct parts succeeded) and smartsegment_model (55 credits, a 36 KB GLB uploaded to /files); the output is seg_model_url, mask_url, prompt, seg_task_id and model_task_id",
}

// multiviewChecked is when the multiview-to-model documentation was read for the fake.
const multiviewChecked = "2026-11-02"

// constructedKindNotes explains kinds that have no captured sample.
var constructedKindNotes = map[string]string{
	"/generation/multiview-to-model": "constructed from the official documentation read on " + multiviewChecked + ": inputs is an array of single-key view objects (front is required, 2-4 images) instead of input; a P-series request with quad=true delivers an FBX and no GLB like the observed text and image tasks (assumed, p2_quad.go). Checked against one real task on 2026-11-19 (#386, evidence-386-multiview-model.candidate.json, not a template sample, so the endpoint stays constructed): three views at Plinth's production defaults (texture and PBR at extreme quality, detailed geometry, 2,000,000 faces), 70 credits shown from the queued poll on. The create envelope, the task type multiview_to_model and the queued > running > success flow are as assumed; the task object shows the request's own parameters under input, the request's model as model_version and a files array of four entries (front, left, back, right; a stored object each, null for the view that was not sent), the output holds model_url (a GLB) and rendered_image_url (a WebP, not the PNG of the other generation tasks), and the real running poll shows the render from progress 99, before the model, which the fake does not (multiview.go). What the real API shows for a parameter the request left out is not known",
}

var endpoints = buildEndpoints()

func buildEndpoints() []fakeupstream.Endpoint {
	out := []fakeupstream.Endpoint{
		{Method: "GET", Path: "/account/balance", Evidence: fakeupstream.EvidenceReal, Sample: "GET /v3/account/balance",
			DocURL: docsRoot + "introduction", CheckedAt: captured},
		{Method: "POST", Path: "/files", Evidence: fakeupstream.EvidenceReal, Sample: "POST /v3/files",
			DocURL: docsRoot + "files", CheckedAt: captured,
			Note: "the sample's multipart reference picture is replaced by a fixed recipe; the response is real; the 400/code 2003 (empty file) and 400/code 2004 (unsupported type) refusals have no sample and are constructed from " + docsRoot + "files and " + docsRoot + "error-handling (read " + docsChecked + "); uploads never carry the credit scenarios, which the error page defines for task creation only"},
		{Method: "GET", Path: "/tasks/{task_id}", Evidence: fakeupstream.EvidenceReal, Sample: "GET /v3/tasks/{id}",
			DocURL: docsRoot + "task-query", CheckedAt: captured,
			Note: "the image_to_multiview poll was re-captured on 2026-11-20 (queued create, running with output {}, success with the four view URLs); queued, running, success and failed poll bodies are real for the real kinds; the cancelled (task-canceled) and unknown (task-unknown) terminals have no sample and are constructed from " + docsRoot + "task-lifecycle and " + docsRoot + "task-query (read " + docsChecked + ") by deriving the real failed poll without error_code/error_message; the legacy banned/expired words are never emitted because the lifecycle page reports them as failed"},
		{Method: "GET", Path: "/artifacts/{task_id}/{name}", Evidence: fakeupstream.EvidenceConstructed,
			DocURL: docsRoot + "task-query", CheckedAt: researched,
			Note: "the official artifact CDN host is not part of the API; this route serves the fixed files its task output URLs name (content type binary/octet-stream as observed)"},
	}
	for _, k := range kinds {
		endpoint := fakeupstream.Endpoint{Method: "POST", Path: k.Path, DocURL: docsRoot + k.DocPage}
		if k.Real {
			endpoint.Evidence = fakeupstream.EvidenceReal
			endpoint.Sample = "POST " + samplePrefix + k.Path
			endpoint.CheckedAt = captured
			endpoint.Note = realKindNotes[k.Path]
		} else {
			endpoint.Evidence = fakeupstream.EvidenceConstructed
			endpoint.CheckedAt = researched
			if note, ok := constructedKindNotes[k.Path]; ok {
				endpoint.CheckedAt = multiviewChecked
				endpoint.Note = note
				out = append(out, endpoint)
				continue
			}
			endpoint.Note = "no real sample (not requested to save credits): the create envelope is the real one of text-to-model; task type " + k.Type + " and the output structure of " + k.Base + " are assumptions to verify on first real use; the documentation was researched on " + researched + " and could not be fetched on " + captured
		}
		out = append(out, endpoint)
	}
	return out
}

// Version is the Tripo v3 fake.
func Version() fakeupstream.Version {
	return fakeupstream.Version{
		Provider: "tripo", API: "v3", Prefix: Prefix, Official: OfficialBase,
		Endpoints: endpoints, Handler: newHandler,
	}
}

// task is one fake task.
type task struct {
	id   string
	kind kind
	// typ is the task type the official API reports: the kind's, except that Smart
	// Segmentation reports smartsegment_image or smartsegment_model by seg_type.
	typ string
	// views and prompts are what an edit_multiview request asked for: the views to
	// edit, in request order, and the prompts as sent.
	views    []string
	prompts  []any
	scenario fakeupstream.Scenario
	// expiredIssues is how many of the first artifact URL issues expire under
	// ScenarioArtifactExpired.
	expiredIssues int
	parent        string
	model         string
	format        string
	// request is the accepted create body; a rig or retarget poll echoes parts
	// of it and a retarget is checked against the rig's.
	request map[string]any
	polls   int
	// failCode and failMessage are set when the task ends in "failed".
	failCode    int
	failMessage string
	// terminal is the non-failure terminal status a scenario forces (cancelled
	// or unknown); a failed task is described by failCode instead.
	terminal string
	// sourceFidelity is the fidelity an export inherits from the task whose
	// artifact URL it was asked to convert.
	sourceFidelity string
	// sourceTriangles is the triangle count of that task's model when it is not
	// the plain fixture (a decimation), 0 otherwise.
	sourceTriangles int
	// importedID is the id the official API gives the model a conversion fetched
	// from a URL: the poll names it original_model_id, a new id every time
	// (observed 2026-11-16, #305).
	importedID string
	// convert is the documented parameters of a convert request (convert.go),
	// echoed under input by the task poll and deciding what the export carries.
	convert map[string]any
	// done marks a task whose last poll answered a terminal status. The account
	// concurrency pool (#468) counts tasks that are not done.
	done bool
}

type handler struct {
	env     *fakeupstream.Env
	mux     *http.ServeMux
	nonce   string
	counter atomic.Int64

	mu    sync.Mutex
	tasks map[string]*task
	files map[string]bool

	balance map[string]any
	upload  map[string]any
	created map[string]any
	// polls[type][status] is a real poll body.
	polls map[string]map[string]map[string]any
	// authError and taskError are the official error envelopes.
	authError map[string]any
	taskError map[string]any
}

func newHandler(env *fakeupstream.Env) (http.Handler, error) {
	h, err := newFake(env)
	if err != nil {
		return nil, err
	}
	return h.mux, nil
}

func newFake(env *fakeupstream.Env) (*handler, error) {
	samples, err := fakeupstream.LoadSamples(sampleFS, sampleFiles...)
	if err != nil {
		return nil, err
	}
	h := &handler{
		env: env, nonce: fmt.Sprintf("%08x", uint32(env.Now().UnixMilli())), // a restarted fake never reuses an old id
		tasks: map[string]*task{}, files: map[string]bool{},
		polls: map[string]map[string]map[string]any{},
	}
	for _, exchange := range samples.Exchanges {
		if exchange.Response.Body.Encoding != wirefixture.PayloadJSON {
			continue
		}
		body, err := fakeupstream.JSONBody(exchange)
		if err != nil {
			return nil, err
		}
		key := fakeupstream.SampleKey(exchange.Request.Method, exchange.Request.Path)
		switch {
		case key == "GET /v3/account/balance" && exchange.Response.Status == http.StatusOK:
			h.balance = body
		case key == "GET /v3/account/balance" && exchange.Response.Status == http.StatusUnauthorized:
			h.authError = body
		case key == "POST /v3/files" && exchange.Response.Status == http.StatusOK:
			h.upload = body
		case key == "GET /v3/tasks/{id}" && exchange.Response.Status == http.StatusNotFound:
			h.taskError = body
		case key == "POST /v3/generation/text-to-model" && exchange.Response.Status == http.StatusOK:
			h.created = body
		case key == "GET /v3/tasks/{id}" && exchange.Response.Status == http.StatusOK:
			data, _ := fakeupstream.DigMap(body, "data")
			typ, _ := data["type"].(string)
			status, _ := data["status"].(string)
			if h.polls[typ] == nil {
				h.polls[typ] = map[string]map[string]any{}
			}
			if h.polls[typ][status] == nil {
				h.polls[typ][status] = body
			}
		}
	}
	if h.balance == nil || h.authError == nil || h.upload == nil || h.taskError == nil || h.created == nil {
		return nil, fmt.Errorf("tripo/v3 samples lack the balance, auth error, upload, task error or create exchanges")
	}
	for _, k := range kinds {
		if k.Real && h.polls[k.Type][statusSuccess] == nil {
			return nil, fmt.Errorf("tripo/v3 samples lack a success poll of real kind %s", k.Type)
		}
		if h.polls[k.Base][statusSuccess] == nil {
			return nil, fmt.Errorf("tripo/v3 samples lack the success poll of base type %s", k.Base)
		}
	}
	if h.polls["animate_retarget"][statusFailed] == nil || h.polls["animate_rig"][statusQueued] == nil || h.polls["text_to_model"][statusRunning] == nil {
		return nil, fmt.Errorf("tripo/v3 samples lack a failed, queued or running poll")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /account/balance", h.getBalance)
	mux.HandleFunc("POST /files", h.postFile)
	mux.HandleFunc("GET /tasks/{task_id}", h.getTask)
	mux.HandleFunc("GET /artifacts/{task_id}/{name}", h.getArtifact)
	for _, k := range kinds {
		mux.HandleFunc("POST "+k.Path, h.create(k))
	}
	h.mux = mux
	return h, nil
}

// pollTemplate finds the real poll body for a task of type typ in status,
// falling back to the closest real body of the same status.
func (h *handler) pollTemplate(k kind, status string) map[string]any {
	for _, typ := range []string{k.Type, k.Base} {
		if body := h.polls[typ][status]; body != nil {
			return body
		}
	}
	for _, typ := range []string{"animate_rig", "text_to_model", "animate_retarget", "mesh_segmentation", "convert_model"} {
		if body := h.polls[typ][status]; body != nil {
			return body
		}
	}
	return h.polls[k.Base][statusSuccess]
}

// ---- error bodies ----

func (h *handler) envelope(base map[string]any, code int, message, suggestion string) map[string]any {
	body := fakeupstream.Clone(base)
	body["code"], body["message"], body["status"] = json.Number(fmt.Sprint(code)), message, "error"
	if suggestion != "" {
		body["suggestion"] = suggestion
	}
	if _, has := body["request_id"]; has {
		body["request_id"] = "00000000-0000-4000-8000-000000000000"
	}
	return body
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	raw, _ := json.Marshal(body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// render writes the Tripo-shaped error of an injected scenario.
func (h *handler) render(w http.ResponseWriter, r *http.Request, scenario fakeupstream.Scenario) {
	status, code, message := http.StatusInternalServerError, 1000, "Internal server error"
	switch scenario {
	case fakeupstream.ScenarioInsufficientBalance:
		status, code, message = http.StatusForbidden, 2010, "You need more credits to complete this task"
	case fakeupstream.ScenarioKeyQuota:
		status, code, message = http.StatusForbidden, 2010, "API key credit limit reached"
	case fakeupstream.ScenarioRateLimit:
		// Request-rate limiting is 1007; a full concurrency pool is the separate
		// 2000 answer (rate-limits and error-handling pages, read docsChecked).
		status, code, message = http.StatusTooManyRequests, 1007, "Rate limit exceeded, you've generated too many requests in a short amount of time"
		if !noRetryAfter(r) {
			w.Header().Set("Retry-After", h.env.RetryAfterSeconds())
			w.Header().Set("X-RateLimit-Limit", "100")
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", h.env.RetryAfterSeconds())
		}
	case fakeupstream.ScenarioConcurrencyLimit:
		status, code, message = http.StatusTooManyRequests, 2000, "You have exceeded the limit of generation"
		if !noRetryAfter(r) {
			w.Header().Set("Retry-After", h.env.RetryAfterSeconds())
		}
	}
	writeJSON(w, status, h.envelope(h.taskError, code, message, "Retry later or check the account"))
}

// renderLocalRateLimit answers the header form of the two 429 words that ask
// for no Retry-After. ScenarioOf does not know them (the shared list holds only
// rate-limit and concurrency-limit), so the shared Intercept lets the request
// through and this handler answers it. The key form ("fake-rate-limit-no-
// retry-after") resolves to the shared word and is answered by Intercept, whose
// render omits the header for it.
func (h *handler) renderLocalRateLimit(w http.ResponseWriter, r *http.Request) bool {
	value := strings.TrimSpace(r.Header.Get(fakeupstream.ScenarioHeader))
	switch value {
	case scenarioRateLimitNoRetryAfter:
		h.render(w, r, fakeupstream.ScenarioRateLimit)
		return true
	case scenarioConcurrencyNoRetry:
		h.render(w, r, fakeupstream.ScenarioConcurrencyLimit)
		return true
	}
	return false
}

// auth answers 401 unless the request carries a bearer key.
func (h *handler) auth(w http.ResponseWriter, r *http.Request) bool {
	if fakeupstream.BearerKey(r) != "" {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, h.authError)
	return false
}

// ---- endpoints ----

func (h *handler) getBalance(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) || h.env.Intercept(w, r, fakeupstream.PhaseRead, h.render) {
		return
	}
	body := fakeupstream.Clone(h.balance)
	if data, ok := fakeupstream.DigMap(body, "data"); ok {
		data["balance"] = fixedBalance
		data["frozen"] = 0.0
	}
	writeJSON(w, http.StatusOK, body)
}

func (h *handler) postFile(w http.ResponseWriter, r *http.Request) {
	// An upload consumes no credits and the files page defines no credit
	// refusal: it is a read-phase request, so insufficient-balance and
	// key-quota stay with task creation (architecture.md). Request-level
	// failures (rate limit, server error, timeout) still apply.
	if !h.auth(w, r) {
		return
	}
	if requestedScenario(r, scenarioUploadFailed) {
		// A free upload creates no task: the official files page defines no
		// credit refusal, so this is a plain transport failure.
		writeJSON(w, http.StatusInternalServerError, h.envelope(h.taskError, 1000, "Internal server error", "Retry later"))
		return
	}
	if h.env.Intercept(w, r, fakeupstream.PhaseRead, h.render) {
		return
	}
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		writeJSON(w, http.StatusBadRequest, h.envelope(h.taskError, 1004, "file is required", "Refer to the API documentation for parameter requirements"))
		return
	}
	reader, err := r.MultipartReader()
	if err == nil {
		for {
			part, nextErr := reader.NextPart()
			if nextErr != nil {
				err = nextErr
				break
			}
			if part.FormName() != "file" || part.FileName() == "" {
				continue
			}
			if !uploadTypeAllowed(part.Header.Get("Content-Type"), part.FileName()) {
				writeJSON(w, http.StatusBadRequest, h.envelope(h.taskError, 2004, "The file type is unsupported", "Refer to the API documentation for the supported file types"))
				return
			}
			// Drain the whole part: the official limit for a model is 150 MB, so a
			// fixed read cap would truncate a valid upload. The fake only checks
			// emptiness, not the per-type size limits (20 MB pictures, 150 MB models).
			written, copyErr := io.Copy(io.Discard, part)
			if copyErr != nil {
				err = copyErr
				break
			}
			if written == 0 {
				writeJSON(w, http.StatusBadRequest, h.envelope(h.taskError, 2003, "The image file is empty", "Please check if you passed the file, or it may be rejected by our firewall"))
				return
			}
			err = nil
			break
		}
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, h.envelope(h.taskError, 1004, "file is required", "Refer to the API documentation for parameter requirements"))
		return
	}
	token := fmt.Sprintf("file_%s-0000-4000-8000-%012d", h.nonce, h.counter.Add(1))
	h.mu.Lock()
	h.files[token] = true
	h.mu.Unlock()
	body := fakeupstream.Clone(h.upload)
	if data, ok := fakeupstream.DigMap(body, "data"); ok {
		data["file_token"] = token
	}
	writeJSON(w, http.StatusOK, body)
}

// uploadExtensions and uploadMediaTypes are the file types the official upload
// endpoint takes (files page, read docsChecked): JPEG or PNG pictures and GLB,
// GLTF, FBX, OBJ or STL models. WebP pictures are included because the presign
// page lists them and Plinth uploads WebP references; the other presign-only
// types (BMP, TIFF, 3MF, USDZ) are not, because the multipart endpoint does
// not document them.
var (
	uploadExtensions = map[string]bool{
		".png": true, ".jpg": true, ".jpeg": true, ".webp": true,
		".glb": true, ".gltf": true, ".fbx": true, ".obj": true, ".stl": true,
	}
	uploadMediaTypes = map[string]bool{
		"image/png": true, "image/jpeg": true, "image/webp": true,
		"model/gltf-binary": true, "model/gltf+json": true, "model/fbx": true, "model/stl": true,
	}
)

// uploadTypeAllowed accepts a part when either its filename extension or its
// declared media type is one the official upload documents.
func uploadTypeAllowed(mediaType, filename string) bool {
	if uploadExtensions[strings.ToLower(path.Ext(filename))] {
		return true
	}
	parsed, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return false
	}
	return uploadMediaTypes[strings.ToLower(parsed)]
}

func (h *handler) newID() string {
	return fmt.Sprintf("%s-0000-4000-8000-%012d", h.nonce, h.counter.Add(1))
}

func (h *handler) create(k kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.auth(w, r) {
			return
		}
		if h.renderLocalRateLimit(w, r) {
			return
		}
		if h.env.Intercept(w, r, fakeupstream.PhaseCreate, h.render) {
			return
		}
		// A full concurrency pool refuses task creation only; the shared
		// Intercept does not know this scenario yet, so it is applied here.
		if fakeupstream.ScenarioOf(r) == fakeupstream.ScenarioConcurrencyLimit {
			h.render(w, r, fakeupstream.ScenarioConcurrencyLimit)
			return
		}
		var body map[string]any
		decoder := json.NewDecoder(io.LimitReader(r.Body, 8<<20))
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			// A broken body is 1003; an empty body names no parameter and stays
			// 1004 (error-handling page, read docsChecked).
			if errors.Is(err, io.EOF) {
				writeJSON(w, http.StatusBadRequest, h.envelope(h.taskError, 1004, "request body must be a JSON object", "Refer to the API documentation for parameter requirements"))
			} else {
				writeJSON(w, http.StatusBadRequest, h.envelope(h.taskError, 1003, "The request body is malformed", "Refer to the API documentation for request schema"))
			}
			return
		}
		if body == nil {
			writeJSON(w, http.StatusBadRequest, h.envelope(h.taskError, 1003, "The request body is malformed", "Refer to the API documentation for request schema"))
			return
		}
		for _, field := range k.Required {
			if value, ok := body[field].(string); !ok || strings.TrimSpace(value) == "" {
				writeJSON(w, http.StatusBadRequest, h.envelope(h.taskError, 1004, field+" is required", "Refer to the API documentation for parameter requirements"))
				return
			}
		}
		if k.Path == "/generation/multiview-to-model" {
			if message := multiviewInputsError(body); message != "" {
				writeJSON(w, http.StatusBadRequest, h.envelope(h.taskError, 1004, message, "Refer to the API documentation for parameter requirements"))
				return
			}
		}
		// The rig, retarget, convert and decimate values are constructed from the
		// official pages (rig.go, convert.go, decimate.go).
		message := ""
		switch k.Path {
		case "/animations/rig":
			message = rigRequestError(body)
		case "/animations/retarget":
			message = retargetRequestError(body)
		case "/models/convert":
			message = convertRequestError(body)
		case "/mesh/decimate":
			message = decimateRequestError(body)
		}
		if message != "" {
			writeJSON(w, http.StatusBadRequest, h.envelope(h.taskError, 1004, message, "Refer to the API documentation for parameter requirements"))
			return
		}
		t := &task{id: h.newID(), kind: k, typ: k.Type, request: body, scenario: fakeupstream.ScenarioOf(r), expiredIssues: fakeupstream.ExpiredIssues(r)}
		if k.Path == "/models/convert" {
			t.convert = convertEcho(body)
		}
		t.terminal = terminalStatus(r)
		if k.Path == "/models/convert" {
			// A convert child can be made to end failed, cancelled or unknown
			// forever (convert-task-<outcome>): the format's own retry path
			// then runs against it.
			switch outcome := convertTaskOutcome(r); outcome {
			case statusFailed:
				if t.failCode == 0 {
					t.failCode, t.failMessage = 3000, "Fake injected convert failure"
				}
			case statusCancelled:
				t.terminal = statusCancelled
			case statusUnknown:
				t.terminal = statusUnknown
			}
		}
		if k.Path == "/mesh/smartsegment" {
			switch segType, _ := body["seg_type"].(string); segType {
			case "image":
			case "model":
				t.typ = "smartsegment_model"
			default:
				writeJSON(w, http.StatusBadRequest, h.envelope(h.taskError, 1004, "seg_type must be image or model", "Refer to the API documentation for parameter requirements"))
				return
			}
		}
		if k.Path == "/generation/edit-multiview" {
			prompts, _ := body["prompts"].([]any)
			for _, entry := range prompts {
				if prompt, _ := entry.(map[string]any); prompt != nil {
					view, _ := prompt["view"].(string)
					if slices.Contains([]string{"front", "left", "back", "right"}, view) && !slices.Contains(t.views, view) {
						t.views = append(t.views, view)
					}
				}
			}
			if len(t.views) == 0 {
				writeJSON(w, http.StatusBadRequest, h.envelope(h.taskError, 1004, "prompts must name at least one view", "Refer to the API documentation for parameter requirements"))
				return
			}
			t.prompts = prompts
		}
		t.model, _ = body["model"].(string)
		t.format, _ = body["format"].(string)
		if source, _ := body["input"].(string); k.Path == "/models/convert" {
			if from := h.urlSource(source); from != nil {
				t.sourceFidelity, t.sourceTriangles = from.fidelity(), from.modelTriangles()
			}
			if isHTTP(source) {
				// A URL is fetched before the answer: the real API took 47 to 48
				// seconds to return the task id (observed 2026-11-16, #305). The
				// model it fetched is a new model with a new id.
				t.importedID = h.newID()
				if delay := h.env.ConvertURLDelay; delay > 0 {
					select {
					case <-time.After(delay):
					case <-r.Context().Done():
						return
					}
				}
			}
		}
		// input is a task id, except for the kinds that take an uploaded file (image-to-*,
		// Smart Segmentation) and Convert, which takes a URL. A file token and a public
		// URL pass through; any other value names a task and must exist, or the official
		// API answers 2001 (error-handling page: the task does not exist or is not the
		// caller's; checked docsChecked).
		if parent, _ := body["input"].(string); parent != "" && k.Path != "/models/convert" {
			switch {
			case strings.HasPrefix(parent, "file_"), isHTTP(parent):
			default:
				t.parent = parent
				source := h.lookup(parent)
				if source == nil {
					writeJSON(w, http.StatusNotFound, h.envelope(h.taskError, 2001, "The task is not found", "Check if the task is committed by you or whether the format is correct"))
					return
				}
				if !h.acceptsSource(k, source) {
					writeJSON(w, http.StatusBadRequest, h.envelope(h.taskError, 1004, "Invalid input parameter", "Refer to the API documentation for parameter requirements"))
					return
				}
				// Observed once (2026-10-29): retargeting a rig built with a v2 rig
				// model ends "failed", error 1004 "Invalid input parameter". The other
				// pairings of preset and rig are constructed from the retarget page
				// the same way (retargetFits).
				if presets, _ := retargetPresets(body); k.Type == "animate_retarget" && !retargetFits(source, presets) {
					t.failCode, t.failMessage = 1004, "Invalid input parameter"
				}
			}
		}
		if t.scenario == fakeupstream.ScenarioTaskFailed && t.failCode == 0 {
			t.failCode, t.failMessage = 3000, "Fake injected task failure"
		}
		h.mu.Lock()
		// The rate-limits page's pools are per task category: a category that is
		// full answers 429 code 2000 with Retry-After (#468), and the other
		// categories keep their own, unaffected pool (checked 2026-11-23).
		category := concurrencyCategory(t)
		if limit := h.concurrencyLimit(category); limit > 0 && h.inflightLocked(category) >= limit {
			h.mu.Unlock()
			h.render(w, r, fakeupstream.ScenarioConcurrencyLimit)
			return
		}
		h.tasks[t.id] = t
		h.mu.Unlock()
		if t.scenario == fakeupstream.ScenarioLostResponse {
			fakeupstream.AbortConnection(w)
			return
		}
		response := fakeupstream.Clone(h.created)
		if data, ok := fakeupstream.DigMap(response, "data"); ok {
			data["task_id"], data["type"] = t.id, t.typ
		}
		writeJSON(w, http.StatusOK, response)
	}
}

// terminalStatus is the non-failure terminal status the creating request asks
// for: the shared task-canceled scenario or the Tripo-local task-unknown word.
func terminalStatus(r *http.Request) string {
	switch {
	case requestedScenario(r, string(fakeupstream.ScenarioTaskCanceled)):
		return statusCancelled
	case requestedScenario(r, scenarioTaskUnknown):
		return statusUnknown
	}
	return ""
}

// requestedScenario answers whether r selects a scenario by the shared
// X-Fake-Scenario header or by a "fake-<name>[-suffix]" API key. It mirrors
// fakeupstream.ScenarioOf for a word the shared scenario list may not hold.
func requestedScenario(r *http.Request, name string) bool {
	if value := strings.TrimSpace(r.Header.Get(fakeupstream.ScenarioHeader)); value != "" {
		return value == name
	}
	rest, ok := strings.CutPrefix(fakeupstream.BearerKey(r), fakeupstream.KeyPrefix)
	if !ok {
		return false
	}
	return rest == name || strings.HasPrefix(rest, name+"-") || strings.HasPrefix(rest, name+"_")
}

// isHTTP reports whether raw is a public http(s) URL, which the create
// endpoints take as an input in place of a task id or file token.
func isHTTP(raw string) bool {
	return strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://")
}

// urlSource finds the task one of this fake's artifact URLs belongs to, nil for
// any other URL.
func (h *handler) urlSource(raw string) *task {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i, part := range parts {
		if part == "artifacts" && i+1 < len(parts) {
			if source := h.lookup(parts[i+1]); source != nil {
				return source
			}
		}
	}
	return nil
}

// inflightLocked counts the tasks of one category that have not reached a
// terminal poll. The caller holds h.mu.
func (h *handler) inflightLocked(category string) int {
	count := 0
	for _, t := range h.tasks {
		if !t.done && concurrencyCategory(t) == category {
			count++
		}
	}
	return count
}

// concurrencyLimit is the account-level ceiling of one category: the official
// per-category default (categoryConcurrency), or AccountConcurrencyLimit when a
// test pins every category to the same number. A negative AccountConcurrencyLimit
// disables the pools (returns 0, so create never refuses), for tests that are not
// about concurrency.
func (h *handler) concurrencyLimit(category string) int {
	if h.env.AccountConcurrencyLimit > 0 {
		return h.env.AccountConcurrencyLimit
	}
	if h.env.AccountConcurrencyLimit < 0 {
		return 0
	}
	return categoryConcurrency[category]
}

// Concurrency categories of the rate-limits page (checked 2026-11-23). Each is an
// independent account-level pool, so one category filling up never blocks another.
const (
	categoryGenerationH     = "generation-h"
	categoryGenerationP     = "generation-p"
	categoryImage           = "image-generation"
	categoryAnimation       = "animation"
	categoryModelProcessing = "model-processing"
	categoryMeshOperations  = "mesh-operations"
)

// categoryConcurrency is the official default pool of each category (3D
// generation H 10 / P 5, image generation 1, animation 10, model processing 5,
// mesh operations 10).
var categoryConcurrency = map[string]int{
	categoryGenerationH:     10,
	categoryGenerationP:     5,
	categoryImage:           1,
	categoryAnimation:       10,
	categoryModelProcessing: 5,
	categoryMeshOperations:  10,
}

// concurrencyCategory is the rate-limit category a creation request falls under.
// Generation splits H (10) and P (5) by the request's model name (isPSeries); the
// other kinds map to the category the page lists them in.
func concurrencyCategory(t *task) string {
	switch t.kind.Type {
	case "image_to_multiview", "edit_multiview":
		return categoryImage
	case "animate_prerigcheck", "animate_rig", "animate_retarget":
		return categoryAnimation
	case "texture_model", "convert_model":
		return categoryModelProcessing
	case "mesh_segmentation", "mesh_completion", "highpoly_to_lowpoly", "smartsegment_image":
		return categoryMeshOperations
	}
	// text_to_model, image_to_model, multiview_to_model and image_to_splat are
	// 3D generation; the page gives the H series 10 and the P series 5.
	if t.isPSeries() {
		return categoryGenerationP
	}
	return categoryGenerationH
}

func (h *handler) lookup(id string) *task {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tasks[id]
}

// acceptsSource refuses a source task that cannot feed the kind: a retarget
// needs a rig, a rig check or rig needs a generated model, a decimation needs
// a text, image or multiview generation task (decimate.go).
func (h *handler) acceptsSource(k kind, source *task) bool {
	switch k.Type {
	case "animate_retarget":
		return source.kind.Type == "animate_rig"
	case "animate_rig", "animate_prerigcheck":
		return source.kind.Type == "text_to_model" || source.kind.Type == "image_to_model"
	case "highpoly_to_lowpoly":
		return acceptsDecimateSource(source)
	}
	return true
}

func (h *handler) getTask(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) || h.env.Intercept(w, r, fakeupstream.PhaseRead, h.render) {
		return
	}
	t := h.lookup(r.PathValue("task_id"))
	if t == nil {
		writeJSON(w, http.StatusNotFound, h.envelope(h.taskError, 2001, "The task is not found", "Check if the task is committed by you or whether the format is correct"))
		return
	}
	h.mu.Lock()
	t.polls++
	n := t.polls
	status := statusSuccess
	switch {
	case n <= h.env.QueuedPolls:
		status = statusQueued
	case n <= h.env.QueuedPolls+h.env.RunningPolls:
		status = statusRunning
	case t.terminal != "":
		status = t.terminal
	case t.failCode != 0:
		status = statusFailed
	}
	if status == statusSuccess || status == statusFailed || status == statusCancelled || status == statusUnknown {
		t.done = true
	}
	h.mu.Unlock()
	writeJSON(w, http.StatusOK, h.pollBody(r, t, status))
}

func (h *handler) pollBody(r *http.Request, t *task, status string) map[string]any {
	template := t.kind
	template.Type = t.typ
	if t.typ != t.kind.Type {
		template.Base = t.typ // the sample of the task's own type (seg_type picks one of two)
	}
	// cancelled and unknown have no real poll sample: their structure is the
	// real failed poll of the same task type without the failure fields (see
	// doc.go). The status word in the body is set below.
	templateStatus := status
	if status == statusCancelled || status == statusUnknown {
		templateStatus = statusFailed
	}
	body := fakeupstream.Clone(h.pollTemplate(template, templateStatus))
	data, _ := fakeupstream.DigMap(body, "data")
	data["task_id"], data["type"], data["status"] = t.id, t.typ, status
	for _, key := range []string{"created_at", "completed_at"} {
		if _, has := data[key]; has {
			data[key] = fixedTimestamp
		}
	}
	t.bill(data, status)
	// Some task types omit progress in some states (edit_multiview while running,
	// smartsegment_image while queued or failed): the sample decides.
	if _, has := data["progress"]; has {
		switch status {
		case statusQueued:
			data["progress"] = json.Number("0")
		case statusRunning:
			data["progress"] = json.Number("50")
		default:
			data["progress"] = json.Number("100")
		}
	}
	if input, ok := fakeupstream.DigMap(data, "input"); ok {
		if _, has := input["prompts"]; has && t.prompts != nil {
			input["prompts"] = t.prompts
		}
		for _, field := range []string{"original_model_id", "original_task_id"} {
			if _, has := input[field]; has {
				input[field] = t.parentID()
			}
		}
		t.echoedInput(input)
		// A convert task shows the request it ran: the committed sample lists the
		// request's format, texture_format, texture_size, bake, fbx_preset and
		// with_animation next to original_model_id, and nothing else.
		if t.convert != nil {
			for name := range input {
				if name != "original_model_id" {
					delete(input, name)
				}
			}
			for name, value := range t.convert {
				input[name] = value
			}
		}
	}
	if status == statusFailed {
		data["error_code"], data["error_message"] = json.Number(fmt.Sprint(t.failCode)), t.failMessage
		return body
	}
	if status == statusCancelled || status == statusUnknown {
		// The failed template carries the failure fields; a cancelled or unknown
		// task has none (task-lifecycle and task-query pages, read docsChecked).
		delete(data, "error_code")
		delete(data, "error_message")
		return body
	}
	if output, ok := fakeupstream.DigMap(data, "output"); ok && status == statusSuccess {
		issue := h.env.IssueArtifactURLs(t.id)
		var render func(node map[string]any)
		render = func(node map[string]any) {
			for key, value := range node {
				if nested, ok := value.(map[string]any); ok {
					render(nested) // generate_multiview_image holds the four view URLs
					continue
				}
				if name, isURL := strings.CutSuffix(key, "_url"); isURL {
					node[key] = h.env.SignArtifactURL(
						h.env.ArtifactOriginFor(r)+Prefix+"/artifacts/"+t.id+"/"+t.artifactName(name), t.scenario, issue)
				}
			}
		}
		if t.typ == "edit_multiview" {
			// An edit returns only the views it was asked to edit (captured: one prompt, one view).
			if views, ok := output["generate_multiview_image"].(map[string]any); ok {
				sample := ""
				for _, value := range views {
					sample, _ = value.(string)
					break
				}
				edited := map[string]any{}
				for _, view := range t.views {
					edited[view+"_view_url"] = sample
				}
				output["generate_multiview_image"] = edited
			}
		}
		render(output)
		t.normalize(output)
	}
	return body
}

// fidelity is the structure the task's model files must carry: a rig has a
// skeleton, a retargeted rig keyframes, and an export keeps what its source had.
func (t *task) fidelity() string {
	switch t.kind.Type {
	case "animate_rig":
		return "skeleton"
	case "animate_retarget":
		return "animation"
	}
	return t.sourceFidelity
}

func (t *task) parentID() string {
	if t.parent != "" {
		return t.parent
	}
	if t.importedID != "" {
		return t.importedID
	}
	return "00000000-0000-4000-8000-000000000000"
}

// artifactName names an output file. The extension is what Plinth keys the
// media type on, so it follows the delivered format.
func (t *task) artifactName(field string) string {
	switch field {
	case "rendered_image":
		if t.typ == "multiview_to_model" {
			// The multiview model's render is a WebP (observed 2026-11-19, #386: the
			// bytes of a real task, a textured PBR model). The text, image, texture and
			// decimation tasks of 2026-10-29 delivered PNG bytes.
			return "rendered_image.webp"
		}
		return "rendered_image.png"
	case "generated_image":
		return "generated_image.jpg"
	case "front_view", "left_view", "back_view", "right_view":
		return field + ".jpg" // the multiview pictures are JPEG
	case "mask":
		return "mask.png"
	case "seg_model":
		return "seg_model.glb"
	}
	if t.typ == "image_to_splat" {
		return "model.splat"
	}
	if t.kind.Type == "convert_model" {
		switch strings.ToUpper(t.convertFormat()) {
		case "FBX":
			return "model.fbx"
		case "OBJ":
			return "model.zip"
		case "STL":
			return "model.stl"
		case "USDZ":
			return "model.usdz"
		case "3MF":
			return "model.3mf"
		case "GLTF":
			// The real GLTF export is a binary GLB, not a glTF JSON: the dev-test
			// check of 2026-11-12 (#293) read the converted.gltf file and the task's
			// model, which is that file, as the same bytes. The fake delivers the
			// same fixed GLB the generation does.
			return "model.glb"
		}
	}
	if t.decimatesToQuad() || t.generatesQuadFBX() {
		return "model.fbx"
	}
	return "model.glb"
}

// artifactFile returns the fixed bytes of an artifact name. texture names the
// image a converted FBX embeds ("png" or "jpeg", convert.go); a rig or animation
// export keeps its fidelity fixture and no texture.
func artifactFile(name, fidelity, texture string) (fakeupstream.File, error) {
	model := func(format string) (fakeupstream.File, error) {
		if fidelity != "" && (format == "glb" || format == "fbx") {
			return fakeupstream.ModelFile(format + ":" + fidelity)
		}
		if format == "fbx" && texture != "" {
			return fakeupstream.ModelFile("fbx:texture-" + texture)
		}
		return fakeupstream.ModelFile(format)
	}
	switch path.Ext(name) {
	case ".glb":
		return model("glb")
	case ".fbx":
		return model("fbx")
	case ".zip":
		return fakeupstream.ModelFile("obj")
	case ".stl":
		return fakeupstream.ModelFile("stl")
	case ".usdz":
		return fakeupstream.ModelFile("usdz")
	case ".3mf":
		// The package Tripo's own converter writes, not the generic fixture: its
		// vendor namespace and ZIP64 framing are what a real delivery has (#386).
		return fakeupstream.ModelFile("3mf:tripo")
	case ".splat":
		return fakeupstream.SplatFile()
	case ".png":
		return fakeupstream.ModelFile("png")
	case ".webp":
		return fakeupstream.ImageFile("webp")
	case ".jpg":
		return fakeupstream.ImageFile("jpeg")
	}
	return fakeupstream.File{}, fmt.Errorf("unknown artifact %q", name)
}

// ArtifactContentType is the content type the official CDN serves model and
// picture files with (observed on every artifact of the alignment run).
const ArtifactContentType = "binary/octet-stream"

// artifactContentType is the content type the CDN served an artifact with: the
// generic binary/octet-stream for models and renders (and for the pictures of a
// multiview task), but the real picture types for a segmentation mask and for the
// views of an edit_multiview task (captured 2026-10-29).
func (t *task) artifactContentType(name string) string {
	switch {
	case t.scenario == fakeupstream.ScenarioArtifactMislabeled && path.Ext(name) == ".glb":
		// The host mime table calls some model files application/x-tgif (#418).
		// The bytes stay a valid GLB; only the label is wrong.
		return "application/x-tgif"
	case name == "mask.png":
		return "image/png"
	case t.typ == "edit_multiview":
		return "image/jpeg"
	}
	return ArtifactContentType
}

func (h *handler) getArtifact(w http.ResponseWriter, r *http.Request) {
	t := h.lookup(r.PathValue("task_id"))
	if t == nil || t.scenario == fakeupstream.ScenarioArtifactNotFound {
		http.NotFound(w, r)
		return
	}
	if fakeupstream.ArtifactURLExpired(r, t.scenario, t.expiredIssues) {
		writeExpired(w)
		return
	}
	file, err := t.artifact(r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data := file.Data
	if t.scenario == fakeupstream.ScenarioArtifactCorrupt {
		data = fakeupstream.Corrupt(data)
	}
	w.Header().Set("Content-Type", t.artifactContentType(r.PathValue("name")))
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	_, _ = w.Write(data)
}

// writeExpired answers a signed artifact URL whose lifetime has passed. The
// official artifact CDN is CloudFront with a custom-policy signed URL (Policy,
// Signature, Key-Pair-Id): a URL it refuses, expired or not, gets 403 with the
// XML access-denied error. Observed 2026-10-29 by setting the Policy's
// DateLessThan into the past: CloudFront cannot tell an expired policy from a
// bad signature, and does not try to.
func writeExpired(w http.ResponseWriter) {
	w.Header().Set("Content-Type", expiredContentType)
	w.WriteHeader(expiredStatus)
	_, _ = w.Write([]byte(expiredBody))
}

const (
	expiredStatus      = http.StatusForbidden
	expiredContentType = "text/xml"
	expiredBody        = `<?xml version="1.0" encoding="UTF-8"?><Error><Code>AccessDenied</Code><Message>Access denied</Message></Error>`
)

// multiviewInputsError checks the documented multiview-to-model inputs: 2-4 single-key
// view objects with unique keys front/left/back/right and a front view, each a file token.
func multiviewInputsError(body map[string]any) string {
	if _, legacy := body["input"]; legacy {
		return "input is not accepted; use inputs"
	}
	inputs, ok := body["inputs"].([]any)
	if !ok || len(inputs) < 2 || len(inputs) > 4 {
		return "inputs must contain 2 to 4 views"
	}
	seen := map[string]bool{}
	for _, entry := range inputs {
		view, ok := entry.(map[string]any)
		if !ok || len(view) != 1 {
			return "each inputs item must hold exactly one view key"
		}
		for key, value := range view {
			token, _ := value.(string)
			if !slices.Contains([]string{"front", "left", "back", "right"}, key) || strings.TrimSpace(token) == "" || seen[key] {
				return "inputs view keys must be unique front/left/back/right with a file token"
			}
			seen[key] = true
		}
	}
	if !seen["front"] {
		return "inputs must include the front view"
	}
	return ""
}
