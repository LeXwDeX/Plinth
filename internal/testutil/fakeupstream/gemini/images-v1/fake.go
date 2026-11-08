package geminiimagesv1

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

//go:embed testdata/gemini-images-v1-*.candidate.json
var sampleFS embed.FS

var sampleFiles = func() []string {
	names, err := fs.Glob(sampleFS, "testdata/gemini-images-v1-*.candidate.json")
	if err != nil || len(names) == 0 {
		panic("gemini/images-v1: no committed samples")
	}
	return names
}()

// Prefix is where this version is mounted: the stack's GEMINI_BASE_URL points
// at <fake>/gemini/images/v1.
const Prefix = "/gemini/images/v1"

// samplePrefix is the path prefix the official gateway uses; the committed
// samples record it.
const samplePrefix = "/v1"

const (
	// docsRoot is the gateway vendor's page for this OpenAI-compatible contract
	// (chat completions plus eca_image_config). It is login-gated, so it is a
	// pointer rather than a public reference; see the endpoint Note.
	docsRoot = "https://doc.model-store.ai/ai-gateway/model/api-detail?code=api-chat-completions1"
	// docsChecked is the date the documentation links and the public Gemini API
	// errors page were checked.
	docsChecked = "2026-11-08"
	// errorsURL is the public page the fault answers' error vocabulary follows.
	errorsURL = "https://ai.google.dev/gemini-api/docs/api-errors"
)

// Test-only error selectors. X-Fake-Error-Code makes the next request answer
// one documented upstream error code; X-Fake-Error-Status overrides its HTTP
// status (400 when absent). A stack whose requests cannot carry extra headers
// selects the same pair with the API key "fake-error-[<status>-]<code>".
const (
	ErrorCodeHeader   = "X-Fake-Error-Code"
	ErrorStatusHeader = "X-Fake-Error-Status"
	errorKeyPrefix    = fakeupstream.KeyPrefix + "error-"
)

// FixedReasoning is the fixed reasoning text of every picture response.
const FixedReasoning = "Fake upstream reasoning."

const unauthorizedBody = "unauthorized\n"

var endpoints = []fakeupstream.Endpoint{
	{Method: "POST", Path: "/chat/completions", Evidence: fakeupstream.EvidenceReal, Sample: "POST /v1/chat/completions",
		DocURL: docsRoot, CheckedAt: docsChecked,
		Note: "the gateway's OpenAI-compatible chat completion with modalities [text,image]: the picture comes back as a data URI in choices[0].message.content[].image_url.url. The real text-to-image sample is a JPEG, the image-to-image one a PNG; the fake always sends a PNG. 400 invalid_text_request and 401 text/plain are real samples. No public official documentation exists for this gateway contract (the vendor page at DocURL is login-gated and redirects to its catalog); the only structural evidence is the committed real gateway samples. The fault answers (quota-exceeded, forbidden, permission-denied, model-not-found, service-unavailable, content-filtered, the 402/500 vocabulary and the " + ErrorCodeHeader + "/" + ErrorStatusHeader + " override) are constructed-from-docs from " + errorsURL + " (checked " + docsChecked + ") and the real gateway envelope; none was observed on the gateway"},
}

// Version is the Gemini image (OpenAI-compatible gateway) /v1 fake.
func Version() fakeupstream.Version {
	return fakeupstream.Version{
		Provider: "gemini", API: "images-v1", Prefix: Prefix, Official: OfficialBase,
		Endpoints: endpoints, Handler: newHandler,
	}
}

type handler struct {
	env *fakeupstream.Env
	// ok is the text-to-image success body, edit the success body of a request
	// that carries a reference picture (it also reports service_tier and the
	// picture's prompt tokens).
	ok, edit map[string]any
	invalid  map[string]any
}

func newHandler(env *fakeupstream.Env) (http.Handler, error) {
	samples, err := fakeupstream.LoadSamples(sampleFS, sampleFiles...)
	if err != nil {
		return nil, err
	}
	h := &handler{env: env}
	for _, exchange := range samples.Find("POST "+samplePrefix+"/chat/completions", "") {
		if exchange.Response.Body.Encoding != "json" {
			continue
		}
		body, err := fakeupstream.JSONBody(exchange)
		if err != nil {
			return nil, err
		}
		switch exchange.Response.Status {
		case http.StatusOK:
			if strings.Contains(string(exchange.Request.Body.JSON), `"image_url"`) {
				h.edit = body
			} else {
				h.ok = body
			}
		case http.StatusBadRequest:
			h.invalid = body
		}
	}
	if h.ok == nil || h.edit == nil || h.invalid == nil {
		return nil, errors.New("gemini/images-v1 sample lacks a text success, an edit success or a 400 exchange")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /chat/completions", h.complete)
	return mux, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	raw, _ := json.Marshal(body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(raw, '\n'))
}

func (h *handler) errorBody(code, message string) map[string]any {
	body := fakeupstream.Clone(h.invalid)
	if object, ok := body["error"].(map[string]any); ok {
		object["code"], object["message"] = code, message
	}
	return body
}

// errorMessages are the documented descriptions of the codes the fake answers
// (https://ai.google.dev/gemini-api/docs/api-errors, checked 2026-11-08);
// insufficient_quota uses OpenAI's wording because the gateway is
// OpenAI-compatible. Other codes get a fixed message of the fake's own.
var errorMessages = map[string]string{
	"api_error":           "Unexpected server-side error.",
	"deadline_exceeded":   "Request did not finish within its deadline.",
	"insufficient_quota":  "You exceeded your current quota, please check your plan and billing details.",
	"payment_required":    "Prepay credit balance is exhausted.",
	"permission_denied":   "Your API key lacks permission for this resource, or your project is restricted or requires verification or billing changes.",
	"quota_exceeded":      "You have exceeded your daily quota.",
	"rate_limit_exceeded": "You have exceeded the per-minute or per-second request or token limit.",
	"service_unavailable": "Service temporarily overloaded or down.",
}

func errorMessage(code string) string {
	if message, ok := errorMessages[code]; ok {
		return message
	}
	return "The upstream answered the documented error " + code + "."
}

// quotaExceededBody mirrors the gateway's committed weekly-quota sample
// (internal/providers/httpjson/quota_test.go, gatewayWeeklyQuotaSample): a 429
// whose error.type is quota_exceeded and which carries the quota object. It is
// not a rate limit, so it has no Retry-After. The values are fixed like every
// fake value; the answer itself is constructed and was not observed on the
// gateway.
func quotaExceededBody() map[string]any {
	return map[string]any{"error": map[string]any{
		"code":    "quota_exceeded",
		"message": "Quota exceeded (weekly). Limit: $100.00, used: $101.21. Resets at 2026-11-09T00:00:00Z",
		"quota": map[string]any{
			"dimension":  "weekly",
			"group_name": "API",
			"limit_usd":  100,
			"resets_at":  "2026-11-09T00:00:00Z",
			"scope":      "model_group",
			"used_usd":   101.20632699999999,
		},
		"type": "quota_exceeded",
	}}
}

// render writes a constructed gateway error for the scenarios Intercept
// handles. The codes follow the public Gemini API errors page; the envelope is
// the real 400 sample's.
func (h *handler) render(w http.ResponseWriter, _ *http.Request, scenario fakeupstream.Scenario) {
	status, code := http.StatusInternalServerError, "api_error"
	switch scenario {
	case fakeupstream.ScenarioInsufficientBalance:
		status, code = http.StatusPaymentRequired, "payment_required"
	case fakeupstream.ScenarioKeyQuota:
		status, code = http.StatusPaymentRequired, "insufficient_quota"
	case fakeupstream.ScenarioRateLimit:
		status, code = http.StatusTooManyRequests, "rate_limit_exceeded"
		w.Header().Set("Retry-After", h.env.RetryAfterSeconds())
	}
	writeJSON(w, status, h.errorBody(code, errorMessage(code)))
}

// requestedError reads the test-only error selector: the X-Fake-Error-Code
// header (with an optional X-Fake-Error-Status) or the API key
// "fake-error-[<status>-]<code>". It is answered like a fault, right after
// authentication.
func requestedError(r *http.Request) (code string, status int, ok bool) {
	if code = strings.TrimSpace(r.Header.Get(ErrorCodeHeader)); code != "" {
		return code, errorStatus(r.Header.Get(ErrorStatusHeader)), true
	}
	rest, found := strings.CutPrefix(fakeupstream.BearerKey(r), errorKeyPrefix)
	if !found || rest == "" {
		return "", 0, false
	}
	if len(rest) > 4 && rest[3] == '-' && isDigits(rest[:3]) {
		if parsed, err := strconv.Atoi(rest[:3]); err == nil && parsed >= 400 && parsed <= 599 && rest[4:] != "" {
			return rest[4:], parsed, true
		}
	}
	return rest, http.StatusBadRequest, true
}

func errorStatus(raw string) int {
	if parsed, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && parsed >= 400 && parsed <= 599 {
		return parsed
	}
	return http.StatusBadRequest
}

func isDigits(value string) bool {
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return value != ""
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = io.WriteString(w, unauthorizedBody)
}

type completionRequest struct {
	Model    string            `json:"model"`
	Messages []json.RawMessage `json:"messages"`
}

// hasReference reports whether a message carries a picture part.
func (r completionRequest) hasReference() bool {
	for _, message := range r.Messages {
		if strings.Contains(string(message), `"image_url"`) {
			return true
		}
	}
	return false
}

func (h *handler) complete(w http.ResponseWriter, r *http.Request) {
	if fakeupstream.BearerKey(r) == "" || fakeupstream.ScenarioOf(r) == fakeupstream.ScenarioInvalidKey {
		unauthorized(w)
		return
	}
	if code, status, ok := requestedError(r); ok {
		writeJSON(w, status, h.errorBody(code, errorMessage(code)))
		return
	}
	if h.env.Intercept(w, r, fakeupstream.PhaseCreate, h.render) {
		return
	}
	var request completionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&request); err != nil || len(request.Messages) == 0 || strings.TrimSpace(request.Model) == "" {
		writeJSON(w, http.StatusBadRequest, h.invalid)
		return
	}
	scenario := fakeupstream.ScenarioOf(r)
	switch scenario {
	case fakeupstream.ScenarioQuotaExceeded:
		writeJSON(w, http.StatusTooManyRequests, quotaExceededBody())
		return
	case fakeupstream.ScenarioForbidden:
		writeJSON(w, http.StatusForbidden, h.errorBody("forbidden", errorMessage("forbidden")))
		return
	case fakeupstream.ScenarioPermissionDenied:
		writeJSON(w, http.StatusForbidden, h.errorBody("permission_denied", errorMessage("permission_denied")))
		return
	case fakeupstream.ScenarioModelNotFound:
		writeJSON(w, http.StatusNotFound, h.errorBody("model_not_found", errorMessage("model_not_found")))
		return
	case fakeupstream.ScenarioServiceUnavailable:
		writeJSON(w, http.StatusServiceUnavailable, h.errorBody("service_unavailable", errorMessage("service_unavailable")))
		return
	case fakeupstream.ScenarioLostResponse:
		fakeupstream.AbortConnection(w)
		return
	case fakeupstream.ScenarioTaskFailed:
		writeJSON(w, http.StatusBadRequest, h.errorBody("content_policy_violation", "The request was blocked by the safety system."))
		return
	}
	template := h.ok
	if request.hasReference() {
		template = h.edit
	}
	body := fakeupstream.Clone(template)
	body["model"] = request.Model
	message, hasMessage := fakeupstream.DigMap(body, "choices", 0, "message")
	if hasMessage {
		message["reasoning_content"] = FixedReasoning
	}
	// A filtered answer is a successful completion without a picture: the
	// gateway reports it in finish_reason (OpenAI vocabulary) instead of an
	// error status (#139).
	if scenario == fakeupstream.ScenarioContentFiltered {
		if choice, ok := fakeupstream.DigMap(body, "choices", 0); ok {
			choice["finish_reason"] = "content_filter"
		}
		if hasMessage {
			message["content"] = []any{}
		}
		writeJSON(w, http.StatusOK, body)
		return
	}
	file, err := fakeupstream.ImageFile("png")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := file.Data
	if scenario == fakeupstream.ScenarioArtifactCorrupt {
		data = fakeupstream.Corrupt(data)
	}
	if hasMessage {
		if content, ok := message["content"].([]any); ok && len(content) > 0 {
			if part, ok := content[0].(map[string]any); ok {
				if image, ok := part["image_url"].(map[string]any); ok {
					image["url"] = "data:" + file.MediaType + ";base64," + base64.StdEncoding.EncodeToString(data)
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, body)
}
