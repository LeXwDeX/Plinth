package hyper3dapiv2

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

//go:embed testdata/hyper3d-api-v2-*.candidate.json
var sampleFS embed.FS

var sampleFiles = func() []string {
	names, err := fs.Glob(sampleFS, "testdata/hyper3d-api-v2-*.candidate.json")
	if err != nil || len(names) == 0 {
		panic("hyper3d/api-v2: no committed samples")
	}
	return names
}()

// Prefix is where this version is mounted; hyper3d_base_url points at
// <fake>/hyper3d/api/v2.
const Prefix = "/hyper3d/api/v2"

// samplePrefix is the path prefix the official API uses; the committed samples
// record it.
const samplePrefix = "/api/v2"

const (
	docsRoot = "https://docs.hyper3d.ai/en/api-specification/"
	captured = "2026-10-29"
)

// Job statuses of the official API.
const (
	statusWaiting    = "Waiting"
	statusGenerating = "Generating"
	statusDone       = "Done"
	statusFailed     = "Failed"
)

// tinyModelBytes is the size below which an uploaded texture model is taken for
// the unreadable one-triangle fixture (observed to end Failed).
const tinyModelBytes = 2048

// Fixed values. The fake promises structure, not realism.
const (
	fixedBalance   = 100.0
	fixedTimestamp = "2026-01-01T00:00:00Z"
)

// kind describes one task-creating route.
type kind struct {
	Path string
	// Real marks routes whose submit, status and download structure comes from a
	// committed sample.
	DocPage string
	// Need says what the form must carry: "prompt-or-images" (generation), "asset"
	// (bang: a finished task's uuid or an uploaded model) or "model-and-image".
	Need string
}

var kinds = []kind{
	{Path: "/rodin", DocPage: "rodin-gen2-5", Need: "prompt-or-images"},
	{Path: "/bang", DocPage: "bang", Need: "asset"},
	{Path: "/rodin_texture_only", DocPage: "generate-texture", Need: "model-and-image"},
}

var endpoints = buildEndpoints()

func buildEndpoints() []fakeupstream.Endpoint {
	out := []fakeupstream.Endpoint{
		{Method: "GET", Path: "/check_balance", Evidence: fakeupstream.EvidenceReal, Sample: "GET /api/v2/check_balance",
			DocURL: docsRoot + "check-balance", CheckedAt: captured},
		{Method: "POST", Path: "/status", Evidence: fakeupstream.EvidenceReal, Sample: "POST /api/v2/status",
			DocURL: docsRoot + "check-status", CheckedAt: captured,
			Note: "Waiting, Generating, Done and Failed polls are real samples. The Failed poll was captured twice (2026-10-29, phase 3): a texture request whose model is the 400-byte one-triangle fixture GLB is accepted and every job is Failed on the first poll with the top-level message \"Invalid input.\" (the fake walks its normal poll cycle first, then answers that Failed body)"},
		{Method: "POST", Path: "/download", Evidence: fakeupstream.EvidenceReal, Sample: "POST /api/v2/download",
			DocURL: docsRoot + "download-results", CheckedAt: captured,
			Note: "a download asked before the jobs finished answers an empty list (observed)"},
		{Method: "GET", Path: "/files/{task_uuid}/{name}", Evidence: fakeupstream.EvidenceConstructed,
			DocURL: docsRoot + "download-results", CheckedAt: captured,
			Note: "the official file host is not part of the API; this route serves the fixed files the download list names (content types as observed)"},
	}
	for _, k := range kinds {
		out = append(out, fakeupstream.Endpoint{
			Method: "POST", Path: k.Path, Evidence: fakeupstream.EvidenceReal, Sample: "POST " + samplePrefix + k.Path,
			DocURL: docsRoot + k.DocPage, CheckedAt: captured,
			Note: "multipart request, HTTP 201 for a handled request; a rejected request is HTTP 201 with an error body (observed)",
		})
	}
	return out
}

// Version is the Hyper3D API v2 fake.
func Version() fakeupstream.Version {
	return fakeupstream.Version{
		Provider: "hyper3d", API: "api-v2", Prefix: Prefix, Official: OfficialBase,
		Endpoints: endpoints, Handler: newHandler,
	}
}

type task struct {
	uuid     string
	key      string
	kind     kind
	jobs     []string
	scenario fakeupstream.Scenario
	// expiredIssues is how many of the first artifact URL issues expire under
	// ScenarioArtifactExpired.
	expiredIssues int
	polls         int
	// names are the files the download list offers.
	names []string
}

type handler struct {
	env     *fakeupstream.Env
	mux     *http.ServeMux
	nonce   string
	counter atomic.Int64

	mu    sync.Mutex
	tasks map[string]*task
	byKey map[string]*task

	balance   map[string]any
	submit    map[string]map[string]any // by route path
	status    map[string]any
	download  map[string]map[string]any // by route path
	failed    map[string]any            // a status poll whose jobs ended Failed (real sample)
	authError map[string]any
	badBody   map[string]any // the 400 envelope
	invalid   map[string]any // INVALID_REQUEST
}

func newHandler(env *fakeupstream.Env) (http.Handler, error) {
	h, err := newFake(env)
	if err != nil {
		return nil, err
	}
	return h.mux, nil
}

func newFake(env *fakeupstream.Env) (*handler, error) {
	h := &handler{
		env: env, nonce: fmt.Sprintf("%08x", uint32(env.Now().UnixMilli())),
		tasks: map[string]*task{}, byKey: map[string]*task{},
		submit: map[string]map[string]any{}, download: map[string]map[string]any{},
	}
	// Each committed sample file holds one route's exchanges; the submit
	// exchange names the route its status and download belong to.
	for _, file := range sampleFiles {
		samples, err := fakeupstream.LoadSamples(sampleFS, file)
		if err != nil {
			return nil, err
		}
		if err := h.index(samples.Exchanges); err != nil {
			return nil, err
		}
	}
	for _, k := range kinds {
		if h.submit[k.Path] == nil {
			return nil, fmt.Errorf("hyper3d/api-v2 samples lack the submit response of %s", k.Path)
		}
		if h.download[k.Path] == nil {
			return nil, fmt.Errorf("hyper3d/api-v2 samples lack the download list of %s", k.Path)
		}
	}
	if h.balance == nil || h.authError == nil || h.badBody == nil || h.invalid == nil || h.status == nil || h.failed == nil {
		return nil, fmt.Errorf("hyper3d/api-v2 samples lack the balance, error, status or Failed status exchanges")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /check_balance", h.getBalance)
	mux.HandleFunc("POST /status", h.postStatus)
	mux.HandleFunc("POST /download", h.postDownload)
	mux.HandleFunc("GET /files/{task_uuid}/{name}", h.getFile)
	for _, k := range kinds {
		mux.HandleFunc("POST "+k.Path, h.submitHandler(k))
	}
	h.mux = mux
	return h, nil
}

func (h *handler) index(exchanges []wirefixture.Exchange) error {
	route := ""
	for _, exchange := range exchanges {
		if exchange.Request.Method != http.MethodPost || exchange.Response.Body.Encoding != wirefixture.PayloadJSON ||
			exchange.Response.Status != http.StatusCreated {
			continue
		}
		body, err := fakeupstream.JSONBody(exchange)
		if err != nil {
			return err
		}
		if jobs, _ := body["jobs"].(map[string]any); jobs != nil && body["uuid"] != nil {
			route = strings.TrimPrefix(exchange.Request.Path, samplePrefix)
			break
		}
	}
	for _, exchange := range exchanges {
		if exchange.Response.Body.Encoding != wirefixture.PayloadJSON {
			continue
		}
		body, err := fakeupstream.JSONBody(exchange)
		if err != nil {
			return err
		}
		apiPath := strings.TrimPrefix(exchange.Request.Path, samplePrefix)
		switch {
		case apiPath == "/check_balance" && exchange.Response.Status == http.StatusOK:
			h.balance = body
		case exchange.Response.Status == http.StatusUnauthorized:
			h.authError = body
		case exchange.Response.Status == http.StatusBadRequest:
			h.badBody = body
		case body["error"] == "INVALID_REQUEST":
			h.invalid = body
		case apiPath == "/status" && hasFailedJob(body):
			h.failed = body
		case apiPath == "/status" && body["jobs"] != nil:
			if h.status == nil {
				h.status = body
			}
		case apiPath == "/download":
			if list, _ := body["list"].([]any); len(list) > 0 && route != "" && h.download[route] == nil {
				h.download[route] = body
			}
		case body["jobs"] != nil && body["uuid"] != nil && h.submit[apiPath] == nil:
			h.submit[apiPath] = body
		}
	}
	return nil
}

// hasFailedJob reports whether a status body has a job that ended Failed.
func hasFailedJob(body map[string]any) bool {
	jobs, _ := body["jobs"].([]any)
	for _, job := range jobs {
		if entry, _ := job.(map[string]any); entry != nil && entry["status"] == statusFailed {
			return true
		}
	}
	return false
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, body any) {
	raw, _ := json.Marshal(body)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func (h *handler) newUUID() string {
	return fmt.Sprintf("%s-0000-4000-8000-%012d", h.nonce, h.counter.Add(1))
}

// auth answers 401 unless the request carries a bearer key.
func (h *handler) auth(w http.ResponseWriter, r *http.Request) bool {
	if fakeupstream.BearerKey(r) != "" {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, h.authError)
	return false
}

// render writes the Hyper3D-shaped error of an injected scenario. Rodin reports
// a rejected request with HTTP 201 and an error code (observed for
// INVALID_REQUEST); the codes follow the Catalog's error mapping.
func (h *handler) render(w http.ResponseWriter, _ *http.Request, scenario fakeupstream.Scenario) {
	switch scenario {
	case fakeupstream.ScenarioInsufficientBalance, fakeupstream.ScenarioKeyQuota:
		body := fakeupstream.Clone(h.invalid)
		body["error"], body["message"] = "API_INSUFFICIENT_FUNDS", "Insufficient credits."
		writeJSON(w, http.StatusCreated, body)
	case fakeupstream.ScenarioRateLimit:
		w.Header().Set("Retry-After", h.env.RetryAfterSeconds())
		body := fakeupstream.Clone(h.badBody)
		body["error"], body["statusCode"], body["message"] = "Too Many Requests", json.Number("429"), []any{"Too many requests"}
		writeJSON(w, http.StatusTooManyRequests, body)
	default:
		body := fakeupstream.Clone(h.badBody)
		body["error"], body["statusCode"], body["message"] = "Internal Server Error", json.Number("500"), []any{"Internal server error"}
		writeJSON(w, http.StatusInternalServerError, body)
	}
}

// ---- endpoints ----

func (h *handler) getBalance(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) || h.env.Intercept(w, r, fakeupstream.PhaseRead, h.render) {
		return
	}
	body := fakeupstream.Clone(h.balance)
	body["balance"] = fixedBalance
	writeJSON(w, http.StatusOK, body)
}

func formFields(r *http.Request) (fields map[string]string, files map[string]int, sizes map[string]int, err error) {
	mediaType, params, parseErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if parseErr != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		return nil, nil, nil, fmt.Errorf("not multipart")
	}
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, nil, nil, err
	}
	fields, files, sizes = map[string]string{}, map[string]int{}, map[string]int{}
	for {
		part, nextErr := reader.NextPart()
		if nextErr == io.EOF {
			return fields, files, sizes, nil
		}
		if nextErr != nil {
			return nil, nil, nil, nextErr
		}
		data, _ := io.ReadAll(io.LimitReader(part, 128<<20))
		if part.FileName() != "" {
			files[part.FormName()]++
			sizes[part.FormName()] = len(data)
		} else {
			fields[part.FormName()] = string(data)
		}
	}
}

func (h *handler) reject(w http.ResponseWriter, code, message string) {
	body := fakeupstream.Clone(h.invalid)
	body["error"] = code
	if message == "" {
		delete(body, "message")
	} else {
		body["message"] = message
	}
	writeJSON(w, http.StatusCreated, body)
}

func (h *handler) submitHandler(k kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.auth(w, r) || h.env.Intercept(w, r, fakeupstream.PhaseCreate, h.render) {
			return
		}
		fields, files, sizes, err := formFields(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, h.badBody)
			return
		}
		switch k.Need {
		case "prompt-or-images":
			if strings.TrimSpace(fields["prompt"]) == "" && files["images"] == 0 {
				h.reject(w, "INVALID_REQUEST", "Please upload at least one image or give a prompt.")
				return
			}
		case "asset":
			if id := fields["asset_id"]; id != "" {
				// The asset must be a finished task of this fake (observed: an
				// unfinished one answers ASSET_NOT_AVAILABLE).
				h.mu.Lock()
				source := h.tasks[id]
				h.mu.Unlock()
				if source == nil || !h.finished(source) {
					h.reject(w, "ASSET_NOT_AVAILABLE", "")
					return
				}
			} else if files["model"] == 0 {
				h.reject(w, "INVALID_REQUEST", "Please upload a model or give an asset id.")
				return
			}
		case "model-and-image":
			if files["model"] == 0 || files["image"] == 0 {
				h.reject(w, "INVALID_REQUEST", "Please upload one model and one image.")
				return
			}
		}
		sample := h.submit[k.Path]
		uuids, _ := fakeupstream.Dig(sample, "jobs", "uuids")
		count := len(uuids.([]any))
		t := &task{uuid: h.newUUID(), kind: k, scenario: fakeupstream.ScenarioOf(r), expiredIssues: fakeupstream.ExpiredIssues(r)}
		// Observed on the official service: a texture request whose model is the tiny
		// one-triangle fixture GLB is accepted and then ends with every job Failed
		// ("Invalid input."), twice in a row (see the alignment report).
		if k.Need == "model-and-image" && sizes["model"] < tinyModelBytes && t.scenario == fakeupstream.ScenarioNone {
			t.scenario = fakeupstream.ScenarioTaskFailed
		}
		t.key = "fake-subscription-" + t.uuid
		for i := 0; i < count; i++ {
			t.jobs = append(t.jobs, h.newUUID())
		}
		t.names = h.downloadNames(k, fields)
		h.mu.Lock()
		h.tasks[t.uuid], h.byKey[t.key] = t, t
		h.mu.Unlock()
		if t.scenario == fakeupstream.ScenarioLostResponse {
			fakeupstream.AbortConnection(w)
			return
		}
		body := fakeupstream.Clone(sample)
		body["uuid"], body["submit_time"], body["consumed"] = t.uuid, fixedTimestamp, 0.5
		if prompt, has := body["prompt"]; has && fields["prompt"] != "" {
			_ = prompt
			body["prompt"] = fields["prompt"]
		}
		jobs, _ := fakeupstream.DigMap(body, "jobs")
		list := make([]any, len(t.jobs))
		for i, id := range t.jobs {
			list[i] = id
		}
		jobs["uuids"], jobs["subscription_key"] = list, t.key
		writeJSON(w, http.StatusCreated, body)
	}
}

// downloadNames are the files a finished task offers. Plinth selects the model
// by the "_pbr.glb" suffix and the preview by name, so a PBR request names its
// model like the official PBR download.
func (h *handler) downloadNames(k kind, fields map[string]string) []string {
	sample, _ := h.download[k.Path]["list"].([]any)
	var names []string
	for _, item := range sample {
		entry, _ := item.(map[string]any)
		name, _ := entry["name"].(string)
		if name == "" {
			continue
		}
		if k.Path == "/bang" && fields["material"] != "None" && !strings.Contains(name, "_pbr") {
			name = "base_" + strings.ToLower(orDefault(fields["resolution"], "basic")) + "_pbr.glb"
		}
		names = append(names, name)
	}
	return names
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// finished reports whether the task's jobs have all been polled to Done.
func (h *handler) finished(t *task) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return t.scenario != fakeupstream.ScenarioTaskFailed && t.polls > h.pollsBeforeDone(t)
}

func (h *handler) postStatus(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) || h.env.Intercept(w, r, fakeupstream.PhaseRead, h.render) {
		return
	}
	var request map[string]any
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request)
	key, _ := request["subscription_key"].(string)
	if key == "" {
		body := fakeupstream.Clone(h.badBody)
		body["message"] = []any{"subscription_key must be a string"}
		writeJSON(w, http.StatusBadRequest, body)
		return
	}
	h.mu.Lock()
	t := h.byKey[key]
	if t != nil {
		t.polls++
	}
	var n int
	if t != nil {
		n = t.polls
	}
	h.mu.Unlock()
	if t == nil {
		body := fakeupstream.Clone(h.badBody)
		body["message"] = []any{"subscription_key is not valid"}
		writeJSON(w, http.StatusBadRequest, body)
		return
	}
	body := fakeupstream.Clone(h.status)
	if t.scenario == fakeupstream.ScenarioTaskFailed && n > h.pollsBeforeDone(t) {
		body = fakeupstream.Clone(h.failed) // real sample: every job Failed and a top-level message
	}
	list := make([]any, len(t.jobs))
	for i, id := range t.jobs {
		list[i] = map[string]any{"uuid": id, "status": h.jobStatus(t, i, n)}
	}
	body["jobs"] = list
	if first, _ := list[0].(map[string]any); first != nil && first["status"] == statusWaiting {
		first["queue_length"] = json.Number("0")
	}
	if t.scenario == fakeupstream.ScenarioTaskFailed && n > h.pollsBeforeDone(t) {
		body["message"] = "Invalid input."
	}
	writeJSON(w, http.StatusCreated, body)
}

// pollsBeforeDone is how many polls answer a non-final state: the Queued polls
// with every job Waiting, then the jobs run one after another, each with a
// Waiting poll (except the first) and a Generating poll.
func (h *handler) pollsBeforeDone(t *task) int {
	return h.env.QueuedPolls + h.env.RunningPolls*(2*len(t.jobs)-1)
}

// jobStatus is the status of job i at poll n, as observed: jobs run in order,
// the finished ones are Done, the running one Generating, the rest Waiting. The
// task-failed scenario ends with every job Failed.
func (h *handler) jobStatus(t *task, i, n int) string {
	if n > h.pollsBeforeDone(t) {
		if t.scenario == fakeupstream.ScenarioTaskFailed {
			return statusFailed
		}
		return statusDone
	}
	if n <= h.env.QueuedPolls {
		return statusWaiting
	}
	// phase 0 is the first job Generating; phase 2k-1 job k Waiting, 2k Generating.
	phase := (n - h.env.QueuedPolls - 1) / h.env.RunningPolls
	running, generating := (phase+1)/2, phase%2 == 0
	switch {
	case i < running:
		return statusDone
	case i == running && generating:
		return statusGenerating
	}
	return statusWaiting
}

func (h *handler) postDownload(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) || h.env.Intercept(w, r, fakeupstream.PhaseRead, h.render) {
		return
	}
	var request map[string]any
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request)
	id, _ := request["task_uuid"].(string)
	if id == "" {
		body := fakeupstream.Clone(h.badBody)
		body["message"] = []any{"task_uuid must be a string"}
		writeJSON(w, http.StatusBadRequest, body)
		return
	}
	h.mu.Lock()
	t := h.tasks[id]
	h.mu.Unlock()
	template := h.download["/rodin"]
	if t != nil {
		template = h.download[t.kind.Path]
	}
	body := fakeupstream.Clone(template)
	list := []any{}
	// Asked before the jobs finish, the official API answers an empty list.
	if t != nil && h.finished(t) {
		issue := h.env.IssueArtifactURLs(t.uuid)
		for _, name := range t.names {
			list = append(list, map[string]any{
				"name": name, "url": h.env.SignArtifactURL(
					h.env.ArtifactOriginFor(r)+Prefix+"/files/"+t.uuid+"/"+name, t.scenario, issue),
			})
		}
	}
	body["list"] = list
	writeJSON(w, http.StatusCreated, body)
}

func (h *handler) getFile(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	t := h.tasks[r.PathValue("task_uuid")]
	h.mu.Unlock()
	if t == nil || t.scenario == fakeupstream.ScenarioArtifactNotFound {
		http.NotFound(w, r)
		return
	}
	if fakeupstream.ArtifactURLExpired(r, t.scenario, t.expiredIssues) {
		w.Header().Set("Content-Type", expiredContentType)
		w.WriteHeader(expiredStatus)
		_, _ = w.Write([]byte(expiredBody))
		return
	}
	name := r.PathValue("name")
	var file fakeupstream.File
	var err error
	switch path.Ext(name) {
	case ".glb":
		file, err = fakeupstream.ModelFile("glb")
	case ".webp":
		file, err = fakeupstream.ImageFile("webp")
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := file.Data
	if t.scenario == fakeupstream.ScenarioArtifactCorrupt {
		data = fakeupstream.Corrupt(data)
	}
	w.Header().Set("Content-Type", file.MediaType)
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	_, _ = w.Write(data)
}

// An expired signed file URL is refused with 403 and the file host's JSON
// error: file.hyper3d.com is a Volcengine TOS host (X-Tos-* query parameters,
// seven days), which checks the lifetime before the signature. Observed
// 2026-10-29 by moving X-Tos-Date into the past; the identifiers are fixed.
const (
	expiredStatus      = http.StatusForbidden
	expiredContentType = "application/json"
	expiredBody        = `{"Code":"AccessDenied","RequestId":"fake-request","HostId":"fake-host","Message":"Request has expired","EC":"0002-00000032","ExpiresSeconds":604800,"Expires":"2026-01-08T00:00:00Z","ServerTime":"2026-01-09T00:00:00Z"}`
)
