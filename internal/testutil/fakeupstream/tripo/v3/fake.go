package tripov3

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"sync/atomic"

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

// Task statuses of the official API (observed: queued, running, success and
// failed; the Catalog's status_mapping also knows cancelled, banned, expired).
const (
	statusQueued  = "queued"
	statusRunning = "running"
	statusSuccess = "success"
	statusFailed  = "failed"
)

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
	{Path: "/animations/retarget", Type: "animate_retarget", Base: "animate_retarget", Required: []string{"input", "animation"}, Real: true, DocPage: "animations-retarget"},
	{Path: "/mesh/segment", Type: "mesh_segmentation", Base: "mesh_segmentation", Required: []string{"input", "model"}, Real: true, DocPage: "mesh-segment"},
	{Path: "/mesh/complete", Type: "mesh_completion", Base: "mesh_completion", Required: []string{"input", "model"}, Real: true, DocPage: "mesh-complete"},
	{Path: "/models/convert", Type: "convert_model", Base: "convert_model", Required: []string{"input", "format"}, Real: true, DocPage: "models-convert"},
	{Path: "/generation/image-to-model", Type: "image_to_model", Base: "image_to_model", Required: []string{"model", "input"}, Real: true, DocPage: "generation-image-to-model/standard"},
	{Path: "/generation/image-to-splat", Type: "image_to_splat", Base: "image_to_splat", Required: []string{"input"}, Real: true, DocPage: "generation-image-to-splat"},
	{Path: "/generation/image-to-multiview", Type: "image_to_multiview", Base: "image_to_multiview", Required: []string{"input"}, Real: true, DocPage: "generation-image-to-multiview"},
	{Path: "/generation/edit-multiview", Type: "edit_multiview", Base: "edit_multiview", Required: []string{"input"}, Real: true, DocPage: "generation-edit-multiview"},
	{Path: "/models/texture", Type: "texture_model", Base: "texture_model", Required: []string{"input"}, Real: true, DocPage: "models-texture"},
	{Path: "/mesh/decimate", Type: "highpoly_to_lowpoly", Base: "highpoly_to_lowpoly", Required: []string{"input", "model"}, Real: true, DocPage: "mesh-decimate"},
	// Smart Segmentation reports smartsegment_image or smartsegment_model by seg_type; both were captured.
	{Path: "/mesh/smartsegment", Type: "smartsegment_image", Base: "smartsegment_image", Required: []string{"input", "seg_type"}, Real: true, DocPage: "mesh-smartsegment"},
}

// realKindNotes says what the 2026-10-29 captures of the seven endpoints added in
// #89 showed (each was requested once with its cheapest option; the credits are
// what the official poll reported).
var realKindNotes = map[string]string{
	"/generation/image-to-model":     "captured: task type image_to_model, 20 credits for a geometry-only v3.1 model; the output has model_url and rendered_image_url (no generated_image_url, which only a text prompt produces)",
	"/generation/image-to-splat":     "captured: task type image_to_splat, 30 credits; the only output is model_url (a .splat file)",
	"/generation/image-to-multiview": "captured: task type image_to_multiview, 10 credits; the output is the object generate_multiview_image with front/left/back/right_view_url (JPEG)",
	"/generation/edit-multiview":     "captured: task type edit_multiview, 5 credits; input.original_task_id is the multiview task and the output carries only the edited views (one prompt, one front_view_url)",
	"/models/texture":                "captured: task type texture_model, 10 credits at texture_quality standard without PBR; the output has model_url and rendered_image_url",
	"/mesh/decimate":                 "captured: the task type is highpoly_to_lowpoly (not mesh_decimation as assumed), 35 credits; the output has model_url and rendered_image_url",
	"/mesh/smartsegment":             "captured for both seg_type values: smartsegment_image (85 credits; the first request, on a shaded sphere, ended failed with error 1001 \"Segmentation pipeline failed\" and was not charged, a second picture with distinct parts succeeded) and smartsegment_model (55 credits, a 36 KB GLB uploaded to /files); the output is seg_model_url, mask_url, prompt, seg_task_id and model_task_id",
}

var endpoints = buildEndpoints()

func buildEndpoints() []fakeupstream.Endpoint {
	out := []fakeupstream.Endpoint{
		{Method: "GET", Path: "/account/balance", Evidence: fakeupstream.EvidenceReal, Sample: "GET /v3/account/balance",
			DocURL: docsRoot + "introduction", CheckedAt: captured},
		{Method: "POST", Path: "/files", Evidence: fakeupstream.EvidenceReal, Sample: "POST /v3/files",
			DocURL: docsRoot + "files", CheckedAt: captured,
			Note: "the sample's multipart reference picture is replaced by a fixed recipe; the response is real"},
		{Method: "GET", Path: "/tasks/{task_id}", Evidence: fakeupstream.EvidenceReal, Sample: "GET /v3/tasks/{id}",
			DocURL: docsRoot + "task-query", CheckedAt: captured,
			Note: "queued, running, success and failed poll bodies are real for the real kinds"},
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
	typ      string
	scenario fakeupstream.Scenario
	// expiredIssues is how many of the first artifact URL issues expire under
	// ScenarioArtifactExpired.
	expiredIssues int
	parent        string
	model         string
	format        string
	polls         int
	// failCode and failMessage are set when the task ends in "failed".
	failCode    int
	failMessage string
	// sourceFidelity is the fidelity an export inherits from the task whose
	// artifact URL it was asked to convert.
	sourceFidelity string
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
func (h *handler) render(w http.ResponseWriter, _ *http.Request, scenario fakeupstream.Scenario) {
	status, code, message := http.StatusInternalServerError, 1000, "Internal server error"
	switch scenario {
	case fakeupstream.ScenarioInsufficientBalance:
		status, code, message = http.StatusForbidden, 2010, "You need more credits to complete this task"
	case fakeupstream.ScenarioKeyQuota:
		status, code, message = http.StatusForbidden, 2010, "API key credit limit reached"
	case fakeupstream.ScenarioRateLimit:
		status, code, message = http.StatusTooManyRequests, 2000, "Too many requests"
		w.Header().Set("Retry-After", h.env.RetryAfterSeconds())
	}
	writeJSON(w, status, h.envelope(h.taskError, code, message, "Retry later or check the account"))
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
	if !h.auth(w, r) || h.env.Intercept(w, r, fakeupstream.PhaseCreate, h.render) {
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
			if part.FormName() == "file" && part.FileName() != "" {
				_, _ = io.Copy(io.Discard, io.LimitReader(part, 64<<20))
				err = nil
				break
			}
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

func (h *handler) newID() string {
	return fmt.Sprintf("%s-0000-4000-8000-%012d", h.nonce, h.counter.Add(1))
}

func (h *handler) create(k kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.auth(w, r) || h.env.Intercept(w, r, fakeupstream.PhaseCreate, h.render) {
			return
		}
		var body map[string]any
		decoder := json.NewDecoder(io.LimitReader(r.Body, 8<<20))
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil || body == nil {
			writeJSON(w, http.StatusBadRequest, h.envelope(h.taskError, 1004, "request body must be a JSON object", "Refer to the API documentation for parameter requirements"))
			return
		}
		for _, field := range k.Required {
			if value, ok := body[field].(string); !ok || strings.TrimSpace(value) == "" {
				writeJSON(w, http.StatusBadRequest, h.envelope(h.taskError, 1004, field+" is required", "Refer to the API documentation for parameter requirements"))
				return
			}
		}
		t := &task{id: h.newID(), kind: k, typ: k.Type, scenario: fakeupstream.ScenarioOf(r), expiredIssues: fakeupstream.ExpiredIssues(r)}
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
		t.model, _ = body["model"].(string)
		t.format, _ = body["format"].(string)
		if source, _ := body["input"].(string); k.Path == "/models/convert" {
			t.sourceFidelity = h.urlFidelity(source)
		}
		// input is a task id, except for the kinds that take an uploaded file (image-to-*,
		// Smart Segmentation) and Convert, which takes a URL.
		if parent, _ := body["input"].(string); parent != "" && k.Path != "/models/convert" && !strings.HasPrefix(parent, "file_") {
			t.parent = parent
			if source := h.lookup(parent); source != nil {
				if !h.acceptsSource(k, source) {
					writeJSON(w, http.StatusBadRequest, h.envelope(h.taskError, 1004, "Invalid input parameter", "Refer to the API documentation for parameter requirements"))
					return
				}
				// Observed once (2026-10-29): retargeting a rig built with a v2 rig
				// model ends "failed", error 1004 "Invalid input parameter".
				if k.Type == "animate_retarget" && strings.HasPrefix(source.model, "v2") {
					t.failCode, t.failMessage = 1004, "Invalid input parameter"
				}
			} else if strings.Contains(parent, "-0000-4000-8000-") {
				writeJSON(w, http.StatusNotFound, h.envelope(h.taskError, 2001, "The task is not found", "Check if the task is committed by you or whether the format is correct"))
				return
			}
		}
		if t.scenario == fakeupstream.ScenarioTaskFailed && t.failCode == 0 {
			t.failCode, t.failMessage = 3000, "Fake injected task failure"
		}
		h.mu.Lock()
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

// urlFidelity reads which task one of this fake's artifact URLs belongs to.
func (h *handler) urlFidelity(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i, part := range parts {
		if part == "artifacts" && i+1 < len(parts) {
			if source := h.lookup(parts[i+1]); source != nil {
				return source.fidelity()
			}
		}
	}
	return ""
}

func (h *handler) lookup(id string) *task {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tasks[id]
}

// acceptsSource refuses a source task that cannot feed the kind: a retarget
// needs a rig, a rig check or rig needs a generated model.
func (h *handler) acceptsSource(k kind, source *task) bool {
	switch k.Type {
	case "animate_retarget":
		return source.kind.Type == "animate_rig"
	case "animate_rig", "animate_prerigcheck":
		return source.kind.Type == "text_to_model" || source.kind.Type == "image_to_model"
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
	h.mu.Unlock()
	status := statusSuccess
	switch {
	case n <= h.env.QueuedPolls:
		status = statusQueued
	case n <= h.env.QueuedPolls+h.env.RunningPolls:
		status = statusRunning
	case t.failCode != 0:
		status = statusFailed
	}
	writeJSON(w, http.StatusOK, h.pollBody(r, t, status))
}

func (h *handler) pollBody(r *http.Request, t *task, status string) map[string]any {
	template := t.kind
	template.Type, template.Base = t.typ, t.typ // the sample of the task's own type (seg_type picks one of two)
	body := fakeupstream.Clone(h.pollTemplate(template, status))
	data, _ := fakeupstream.DigMap(body, "data")
	data["task_id"], data["type"], data["status"] = t.id, t.typ, status
	for _, key := range []string{"created_at", "completed_at"} {
		if _, has := data[key]; has {
			data[key] = fixedTimestamp
		}
	}
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
		for _, field := range []string{"original_model_id", "original_task_id"} {
			if _, has := input[field]; has {
				input[field] = t.parentID()
			}
		}
	}
	if status == statusFailed {
		data["error_code"], data["error_message"] = json.Number(fmt.Sprint(t.failCode)), t.failMessage
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
		render(output)
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
	return "00000000-0000-4000-8000-000000000000"
}

// artifactName names an output file. The extension is what Plinth keys the
// media type on, so it follows the delivered format.
func (t *task) artifactName(field string) string {
	switch field {
	case "rendered_image":
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
		switch strings.ToUpper(t.format) {
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
			return "model.gltf"
		}
	}
	return "model.glb"
}

// artifactFile returns the fixed bytes of an artifact name.
func artifactFile(name, fidelity string) (fakeupstream.File, error) {
	model := func(format string) (fakeupstream.File, error) {
		if fidelity != "" && (format == "glb" || format == "fbx") {
			return fakeupstream.ModelFile(format + ":" + fidelity)
		}
		return fakeupstream.ModelFile(format)
	}
	switch path.Ext(name) {
	case ".glb":
		return model("glb")
	case ".gltf":
		return fakeupstream.ModelFile("gltf")
	case ".fbx":
		return model("fbx")
	case ".zip":
		return fakeupstream.ModelFile("obj")
	case ".stl":
		return fakeupstream.ModelFile("stl")
	case ".usdz":
		return fakeupstream.ModelFile("usdz")
	case ".3mf":
		return fakeupstream.ModelFile("3mf")
	case ".splat":
		return fakeupstream.SplatFile()
	case ".png":
		return fakeupstream.ModelFile("png")
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
	file, err := artifactFile(r.PathValue("name"), t.fidelity())
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
