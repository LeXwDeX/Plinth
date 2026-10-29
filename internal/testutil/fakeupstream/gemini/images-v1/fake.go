package geminiimagesv1

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
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
	docsRoot = "https://ai.google.dev/gemini-api/docs/image-generation"
	captured = "2026-10-29"
)

// FixedReasoning is the fixed reasoning text of every picture response.
const FixedReasoning = "Fake upstream reasoning."

const unauthorizedBody = "unauthorized\n"

var endpoints = []fakeupstream.Endpoint{
	{Method: "POST", Path: "/chat/completions", Evidence: fakeupstream.EvidenceReal, Sample: "POST /v1/chat/completions",
		DocURL: docsRoot, CheckedAt: captured,
		Note: "the gateway's OpenAI-compatible chat completion with modalities [text,image]: the picture comes back as a data URI in choices[0].message.content[].image_url.url. The real text-to-image sample is a JPEG, the image-to-image one a PNG; the fake always sends a PNG. 400 invalid_text_request and 401 text/plain are real samples; a content-policy refusal and 402/429/5xx are not observed and reuse the real 400 envelope"},
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

func (h *handler) render(w http.ResponseWriter, _ *http.Request, scenario fakeupstream.Scenario) {
	status, code, message := http.StatusInternalServerError, "internal_error", "internal server error"
	switch scenario {
	case fakeupstream.ScenarioInsufficientBalance:
		status, code, message = http.StatusPaymentRequired, "insufficient_quota", "insufficient balance"
	case fakeupstream.ScenarioKeyQuota:
		status, code, message = http.StatusPaymentRequired, "insufficient_quota", "API key credit limit reached"
	case fakeupstream.ScenarioRateLimit:
		status, code, message = http.StatusTooManyRequests, "rate_limit_exceeded", "rate limit exceeded"
		w.Header().Set("Retry-After", h.env.RetryAfterSeconds())
	}
	writeJSON(w, status, h.errorBody(code, message))
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
	if fakeupstream.BearerKey(r) == "" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, unauthorizedBody)
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
	case fakeupstream.ScenarioLostResponse:
		fakeupstream.AbortConnection(w)
		return
	case fakeupstream.ScenarioTaskFailed:
		writeJSON(w, http.StatusBadRequest, h.errorBody("content_policy_violation", "The request was blocked by the safety system."))
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
	template := h.ok
	if request.hasReference() {
		template = h.edit
	}
	body := fakeupstream.Clone(template)
	body["model"] = request.Model
	if message, ok := fakeupstream.DigMap(body, "choices", 0, "message"); ok {
		message["reasoning_content"] = FixedReasoning
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
