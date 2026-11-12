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
	"slices"
	"strconv"
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
	// formDocs is when the value rules of the Gen-2.5 form (rodin-gen2-5) were
	// last checked. They are constructed from that page, like the error bodies:
	// no wire sample covers a rejected value (see validateGenerationForm).
	formDocs = "2026-11-12"
	// errorDocs is when the error bodies without a wire sample were last
	// checked against docsRoot+"errors". They are constructed from that page:
	// application failures are HTTP 201 with the reason in error and the
	// detail in message, while 400, 401 and 429 are transport failures. The
	// status and download NO_SUCH_TASK bodies ({error} only) also match the
	// real observation recorded in
	// internal/providers/hyper3d/charge_evidence_test.go (2026-10-21).
	errorDocs = "2026-11-08"
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
			Note: "Waiting, Generating, Done and Failed polls are real samples. The Failed poll was captured twice (2026-10-29, phase 3): a texture request whose model is the 400-byte one-triangle fixture GLB is accepted and every job is Failed on the first poll with the top-level message \"Invalid input.\" (the fake walks its normal poll cycle first, then answers that Failed body). An unknown subscription_key answers HTTP 201 {\"error\":\"NO_SUCH_TASK\"} (real observation 2026-10-21, charge_evidence_test.go, not committed as a sample); a malformed body keeps the 400 envelope"},
		{Method: "POST", Path: "/download", Evidence: fakeupstream.EvidenceReal, Sample: "POST /api/v2/download",
			DocURL: docsRoot + "download-results", CheckedAt: captured,
			Note: "a download asked before the jobs finished answers an empty list (observed); an unknown task_uuid answers HTTP 201 {\"error\":\"NO_SUCH_TASK\"} (real observation 2026-10-21, charge_evidence_test.go, not committed as a sample)"},
		{Method: "GET", Path: "/files/{task_uuid}/{name}", Evidence: fakeupstream.EvidenceConstructed,
			DocURL: docsRoot + "download-results", CheckedAt: captured,
			Note: "the official file host is not part of the API; this route serves the fixed files the download list names (content types as observed)"},
	}
	for _, k := range kinds {
		note := "multipart request, HTTP 201 for a handled request; a rejected request is HTTP 201 with an error body (observed). API_NO_ACTIVE_SUBSCRIPTION was observed on 2026-11-12 (#294): a real submission was refused with it and the product recorded no HTTP status for the refusal (upstream_status 0), which is what the HTTP 201 error body sent here gives; the message text of the real body was not kept, so the message stays the documented description. The other documented submission codes without a wire sample — API_PARALLELISM_LIMIT_REACHED, PERMISSION_DENIED, API_SUBSCRIPTION_PLAN_TOO_LOW, API_OBJECT_NOT_FOUND_ON_IMAGE, IMAGE_CONTENT_VIOLATION, IMAGE_LABEL_LENGTH_TOO_LONG, API_UNKNOWN and the unknown-asset NO_SUCH_TASK — are constructed from " + docsRoot + "errors (checked 2026-11-08)"
		if k.Need == "prompt-or-images" {
			note += ". The value rules of the Gen-2.5 form (seed, mesh_mode, quality, texture_mode, quality_override with its Quad and tier ceilings, and the TAPose and quad_normal switches) are constructed from " + docsRoot + k.DocPage + " (checked " + formDocs + "): a value outside them is answered INVALID_REQUEST, the documented code for invalid values. The real service may answer a schema-level violation with its HTTP 400 validator envelope instead; neither the status nor the message text of a rejected value is observed"
		}
		out = append(out, fakeupstream.Endpoint{
			Method: "POST", Path: k.Path, Evidence: fakeupstream.EvidenceReal, Sample: "POST " + samplePrefix + k.Path,
			DocURL: docsRoot + k.DocPage, CheckedAt: captured, Note: note,
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

// auth answers 401 unless the request carries a bearer key. ScenarioInvalidKey
// rejects even a supplied key: 401 is the documented transport-level failure
// for missing or invalid authentication (docsRoot+"errors", checked errorDocs).
func (h *handler) auth(w http.ResponseWriter, r *http.Request) bool {
	if fakeupstream.BearerKey(r) == "" || fakeupstream.ScenarioOf(r) == fakeupstream.ScenarioInvalidKey {
		writeJSON(w, http.StatusUnauthorized, h.authError)
		return false
	}
	return true
}

// render writes the Hyper3D-shaped error of an injected scenario. Rodin reports
// a rejected request with HTTP 201 and an error code (observed for
// INVALID_REQUEST); the codes follow the Catalog's error mapping.
func (h *handler) render(w http.ResponseWriter, _ *http.Request, scenario fakeupstream.Scenario) {
	switch scenario {
	case fakeupstream.ScenarioInsufficientBalance, fakeupstream.ScenarioKeyQuota:
		// A per-key credit limit surfaces as the documented insufficient-funds
		// code: the table has no separate per-key quota code.
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

// renderSubmissionScenario writes the documented submission errors that
// Env.Intercept does not route. Their bodies are constructed from
// docsRoot+"errors" (checked errorDocs): HTTP 201 with the documented
// description as message. No wire sample covers them (API_NO_ACTIVE_SUBSCRIPTION
// was observed once, 2026-11-12, #294, as a refusal the product recorded with no
// HTTP status; the shape here is that of the other HTTP 201 refusals and the
// message is still the documented description), and the FailTimes counter that
// Env.Intercept applies to its own scenarios does not reach these codes.
func (h *handler) renderSubmissionScenario(w http.ResponseWriter, r *http.Request) bool {
	switch fakeupstream.ScenarioOf(r) {
	case fakeupstream.ScenarioConcurrencyLimit:
		h.reject(w, "API_PARALLELISM_LIMIT_REACHED", "Too many of the account's tasks are already running. Retry once one finishes.")
	case fakeupstream.ScenarioPermissionDenied:
		h.reject(w, "PERMISSION_DENIED", "The account may not perform this action.")
	case fakeupstream.ScenarioNoActiveSubscription:
		h.reject(w, "API_NO_ACTIVE_SUBSCRIPTION", "The account lacks the API access required by the endpoint or Rodin tier.")
	case fakeupstream.ScenarioPlanTooLow:
		h.reject(w, "API_SUBSCRIPTION_PLAN_TOO_LOW", "The subscription plan does not allow API access.")
	case fakeupstream.ScenarioObjectNotFoundOnImage:
		h.reject(w, "API_OBJECT_NOT_FOUND_ON_IMAGE", "No object was detected in the input image, or the image is unsupported.")
	case fakeupstream.ScenarioContentViolation:
		h.reject(w, "IMAGE_CONTENT_VIOLATION", "The input image is not allowed in your region. Try another image.")
	case fakeupstream.ScenarioAPIUnknown:
		h.reject(w, "API_UNKNOWN", "Unexpected server-side error.")
	default:
		return false
	}
	return true
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

// formData is a parsed multipart submission.
type formData struct {
	fields map[string]string
	files  map[string]int
	sizes  map[string]int
	// labels counts the image_label fields: the documented
	// IMAGE_LABEL_LENGTH_TOO_LONG rejection compares them with the uploaded
	// images.
	labels int
}

func readForm(r *http.Request) (formData, error) {
	mediaType, params, parseErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if parseErr != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		return formData{}, fmt.Errorf("not multipart")
	}
	reader, err := r.MultipartReader()
	if err != nil {
		return formData{}, err
	}
	parsed := formData{fields: map[string]string{}, files: map[string]int{}, sizes: map[string]int{}}
	for {
		part, nextErr := reader.NextPart()
		if nextErr == io.EOF {
			return parsed, nil
		}
		if nextErr != nil {
			return formData{}, nextErr
		}
		raw, _ := io.ReadAll(io.LimitReader(part, 128<<20))
		name := part.FormName()
		if part.FileName() != "" {
			parsed.files[name]++
			parsed.sizes[name] = len(raw)
			continue
		}
		if name == "image_label" {
			parsed.labels++
		}
		parsed.fields[name] = string(raw)
	}
}

// reject writes a documented application-level error as HTTP 201 with
// {"error": code} and, when not empty, the message (docsRoot+"errors", checked
// errorDocs). An empty message leaves only error, which is the observed shape
// of the status and download NO_SUCH_TASK bodies.
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

// Value rules of the Gen-2.5 form, constructed from the official page
// (docsRoot+"rodin-gen2-5", checked formDocs). A value outside them is answered
// like the other invalid requests, HTTP 201 with INVALID_REQUEST, the documented
// code for invalid values; the message text is the fake's own. Only the fields
// whose values the page restricts and Plinth can send are checked; the rest of
// the form is accepted as before.
const (
	maxSeed                 = 65535
	minQualityOverride      = 500
	maxQualityOverride      = 2000000
	maxQualityOverrideLower = 1000000 // every Gen-2.5 tier below High
	maxQualityOverrideQuad  = 200000  // the additional ceiling of Quad mode
)

var (
	generationMeshModes      = []string{"Raw", "Quad"}
	generationQualityPresets = []string{"high", "medium", "low", "extra-low"}
	// generationTextureModes are the documented texture_mode values; the
	// resolution each gives is the page's texture resolution table (2K for
	// extreme-low to high, 6K for extreme-high, without HighPack), which the
	// fake does not need to model.
	generationTextureModes = []string{"legacy", "extreme-low", "low", "medium", "high", "extreme-high"}
	generationHighTiers    = []string{"Gen-2.5-High", "Gen-2.5-Extreme-High"}
	// generationSwitches are the documented boolean fields.
	generationSwitches = []string{
		"TAPose", "quad_normal", "use_original_alpha", "preview_render", "hd_texture",
		"is_micro", "texture_delight", "uhd_texture", "soft",
	}
)

// validateGenerationForm returns the rejection message of the first field that
// breaks a rule, or "" when the form is valid.
func validateGenerationForm(fields map[string]string) string {
	if value, present := fields["seed"]; present {
		if number, ok := wholeNumber(value); !ok || number < 0 || number > maxSeed {
			return fmt.Sprintf("seed must be an integer between 0 and %d.", maxSeed)
		}
	}
	mode, hasMode := fields["mesh_mode"]
	if hasMode && !slices.Contains(generationMeshModes, mode) {
		return "mesh_mode must be one of Raw, Quad."
	}
	if quality, present := fields["quality"]; present && !slices.Contains(generationQualityPresets, quality) {
		return "quality must be one of high, medium, low, extra-low."
	}
	if texture, present := fields["texture_mode"]; present && !slices.Contains(generationTextureModes, texture) {
		return "texture_mode must be one of legacy, extreme-low, low, medium, high, extreme-high."
	}
	for _, name := range generationSwitches {
		if value, present := fields[name]; present && value != "true" && value != "false" {
			return name + " must be a boolean."
		}
	}
	value, present := fields["quality_override"]
	if !present {
		return ""
	}
	number, ok := wholeNumber(value)
	if !ok || number < minQualityOverride || number > maxQualityOverride {
		return fmt.Sprintf("quality_override must be an integer between %d and %d.", minQualityOverride, maxQualityOverride)
	}
	tier := fields["tier"]
	if strings.HasPrefix(tier, "Gen-2.5-") && !slices.Contains(generationHighTiers, tier) && number > maxQualityOverrideLower {
		return fmt.Sprintf("quality_override must not exceed %d for tier %s.", maxQualityOverrideLower, tier)
	}
	// Gen-2.5 tiers default to Raw; every other tier (an omitted tier is
	// Regular) defaults to Quad.
	if !hasMode {
		mode = "Quad"
		if strings.HasPrefix(tier, "Gen-2.5-") {
			mode = "Raw"
		}
	}
	if mode == "Quad" && number > maxQualityOverrideQuad {
		return fmt.Sprintf("quality_override must not exceed %d in Quad mode.", maxQualityOverrideQuad)
	}
	return ""
}

// wholeNumber parses a form value that must be an integer.
func wholeNumber(value string) (int64, bool) {
	number, err := strconv.ParseInt(value, 10, 64)
	return number, err == nil
}

func (h *handler) submitHandler(k kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.auth(w, r) || h.env.Intercept(w, r, fakeupstream.PhaseCreate, h.render) {
			return
		}
		if h.renderSubmissionScenario(w, r) {
			return
		}
		form, err := readForm(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, h.badBody)
			return
		}
		switch k.Need {
		case "prompt-or-images":
			// IMAGE_LABEL_LENGTH_TOO_LONG is documented as more image_label
			// entries than uploaded images (docsRoot+"errors", checked
			// errorDocs); constructed, no wire sample covers the rejection.
			if form.labels > form.files["images"] {
				h.reject(w, "IMAGE_LABEL_LENGTH_TOO_LONG", "More image_label entries were sent than uploaded images.")
				return
			}
			// The official Rodin page documents 1-5 reference images. The upper
			// bound is constructed from those docs: no live rejection sample
			// exists for it. Zero images stays valid for text-to-3D when the
			// form carries a prompt.
			if form.files["images"] > 5 {
				h.reject(w, "INVALID_REQUEST", "Please upload between 1 and 5 images.")
				return
			}
			if strings.TrimSpace(form.fields["prompt"]) == "" && form.files["images"] == 0 {
				h.reject(w, "INVALID_REQUEST", "Please upload at least one image or give a prompt.")
				return
			}
			if message := validateGenerationForm(form.fields); message != "" {
				h.reject(w, "INVALID_REQUEST", message)
				return
			}
		case "asset":
			if id := form.fields["asset_id"]; id != "" {
				// A task id that names no task is the documented NO_SUCH_TASK
				// ("The referenced task does not exist or has expired"); only an
				// existing task whose state does not allow the operation is
				// ASSET_NOT_AVAILABLE (observed: an unfinished one). The unknown
				// branch is constructed from docsRoot+"errors" (checked
				// errorDocs), no wire sample covers it.
				h.mu.Lock()
				source := h.tasks[id]
				h.mu.Unlock()
				if source == nil {
					h.reject(w, "NO_SUCH_TASK", "The referenced task does not exist or has expired.")
					return
				}
				if !h.finished(source) {
					h.reject(w, "ASSET_NOT_AVAILABLE", "")
					return
				}
			} else if form.files["model"] == 0 {
				h.reject(w, "INVALID_REQUEST", "Please upload a model or give an asset id.")
				return
			}
		case "model-and-image":
			if form.files["model"] == 0 || form.files["image"] == 0 {
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
		if k.Need == "model-and-image" && form.sizes["model"] < tinyModelBytes && t.scenario == fakeupstream.ScenarioNone {
			t.scenario = fakeupstream.ScenarioTaskFailed
		}
		t.key = "fake-subscription-" + t.uuid
		for i := 0; i < count; i++ {
			t.jobs = append(t.jobs, h.newUUID())
		}
		t.names = h.downloadNames(k, form.fields)
		h.mu.Lock()
		h.tasks[t.uuid], h.byKey[t.key] = t, t
		h.mu.Unlock()
		if t.scenario == fakeupstream.ScenarioLostResponse {
			fakeupstream.AbortConnection(w)
			return
		}
		body := fakeupstream.Clone(sample)
		body["uuid"], body["submit_time"], body["consumed"] = t.uuid, fixedTimestamp, 0.5
		if prompt, has := body["prompt"]; has && form.fields["prompt"] != "" {
			_ = prompt
			body["prompt"] = form.fields["prompt"]
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
		// A supplied key that names no task is an application error with HTTP
		// 201, not the 400 malformed-body envelope: real observation 2026-10-21
		// (charge_evidence_test.go), docsRoot+"errors" checked errorDocs.
		h.reject(w, "NO_SUCH_TASK", "")
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
	if t == nil {
		// An unknown, expired or unowned task_uuid is an application error with
		// HTTP 201: real observation 2026-10-21 (charge_evidence_test.go),
		// docsRoot+"errors" checked errorDocs. An existing task that has no
		// files yet (unfinished or failed) keeps the observed empty list: the
		// real texture-failed sample answers {"list":[]}, so the documented
		// no-files INVALID_REQUEST has no distinguishable trigger here.
		h.reject(w, "NO_SUCH_TASK", "")
		return
	}
	template := h.download[t.kind.Path]
	body := fakeupstream.Clone(template)
	list := []any{}
	// Asked before the jobs finish, the official API answers an empty list.
	if h.finished(t) {
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
