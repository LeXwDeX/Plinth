package openaiimagesv1

import (
	"bytes"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

//go:embed testdata/openai-images-v1-*.candidate.json
var sampleFS embed.FS

var sampleFiles = func() []string {
	names, err := fs.Glob(sampleFS, "testdata/openai-images-v1-*.candidate.json")
	if err != nil || len(names) == 0 {
		panic("openai/images-v1: no committed samples")
	}
	return names
}()

// Prefix is where this version is mounted: the stack's OPENAI_BASE_URL points
// at <fake>/openai/images/v1.
const Prefix = "/openai/images/v1"

// samplePrefix is the path prefix the official gateway uses; the committed
// samples record it.
const samplePrefix = "/v1"

const (
	docsRoot = "https://platform.openai.com/docs/api-reference/images/"
	captured = "2026-11-09"
	// docsCheckedAt is the day the constructed fault answers were checked
	// against the official documents; docsErrors is the error-code guide.
	docsCheckedAt = "2026-11-08"
	docsErrors    = "https://developers.openai.com/api/docs/guides/error-codes"
)

// unauthorizedBody is the gateway's plain-text 401 (observed).
const unauthorizedBody = "unauthorized\n"

var endpoints = []fakeupstream.Endpoint{
	{Method: "POST", Path: "/images/generations", Evidence: fakeupstream.EvidenceReal, Sample: "POST /v1/images/generations",
		DocURL: docsRoot + "create", CheckedAt: captured,
		Note: "JSON request with inline base64 pictures; 400 missing_required_parameter and 401 text/plain are real samples. n is documented by the official Create image reference (1–10) and returns that many distinct inline candidates; the committed samples ask for n=1, so n>1 is constructed-from-docs. A content-policy refusal (400 moderation_blocked) and the 402/500 faults are not observed: they reuse the real 400 envelope with the documented code or message. The quota-exceeded 429 (the gateway's error.quota body), forbidden 403 (the gateway's eca_access_denied) and service-unavailable 503 (documented service_unavailable_error/server_is_overloaded) are constructed-from-docs answers built from the recorded gateway refusals and the error-code guide (" + docsErrors + ", checked " + docsCheckedAt + ")"},
	{Method: "POST", Path: "/images/edits", Evidence: fakeupstream.EvidenceReal, Sample: "POST /v1/images/edits",
		DocURL: docsRoot + "createEdit", CheckedAt: captured,
		Note: "multipart request with the reference picture in the image part (Plinth sends image[], the committed sample says image because the sample key rule rejects brackets; the fake accepts both). model is optional on this route, per the official Create image edit reference (default gpt-image-2.5-sunburst, checked " + docsCheckedAt + "); the same constructed-from-docs 403/429/503 faults as generations apply"},
}

// Version is the OpenAI-compatible images /v1 fake.
func Version() fakeupstream.Version {
	return fakeupstream.Version{
		Provider: "openai", API: "images-v1", Prefix: Prefix, Official: OfficialBase,
		Endpoints: endpoints, Handler: newHandler,
	}
}

type handler struct {
	env     *fakeupstream.Env
	ok      map[string]map[string]any // endpoint -> success body
	invalid map[string]map[string]any // endpoint -> 400 body
}

func newHandler(env *fakeupstream.Env) (http.Handler, error) {
	samples, err := fakeupstream.LoadSamples(sampleFS, sampleFiles...)
	if err != nil {
		return nil, err
	}
	h := &handler{env: env, ok: map[string]map[string]any{}, invalid: map[string]map[string]any{}}
	for _, endpoint := range []string{"/images/generations", "/images/edits"} {
		for _, exchange := range samples.Find("POST "+samplePrefix+endpoint, "") {
			if exchange.Response.Body.Encoding != "json" {
				continue
			}
			body, err := fakeupstream.JSONBody(exchange)
			if err != nil {
				return nil, err
			}
			switch exchange.Response.Status {
			case http.StatusOK:
				h.ok[endpoint] = body
			case http.StatusBadRequest:
				h.invalid[endpoint] = body
			}
		}
		if h.ok[endpoint] == nil || h.invalid[endpoint] == nil {
			return nil, errors.New("openai/images-v1 sample lacks a success or a 400 exchange of " + endpoint)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /images/generations", func(w http.ResponseWriter, r *http.Request) { h.create(w, r, "/images/generations") })
	mux.HandleFunc("POST /images/edits", func(w http.ResponseWriter, r *http.Request) { h.create(w, r, "/images/edits") })
	return mux, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	raw, _ := json.Marshal(body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(raw, '\n'))
}

// errorBody clones the real 400 envelope with another status text.
func (h *handler) errorBody(endpoint, kind, code, message, param string) map[string]any {
	body := fakeupstream.Clone(h.invalid[endpoint])
	if object, ok := body["error"].(map[string]any); ok {
		object["type"], object["code"], object["message"], object["param"] = kind, code, message, param
	}
	return body
}

// officialErrorBody is the ErrorResponse the official OpenAPI declares: the
// error object's required fields are type, message, param and code, and param
// and code may be null.
func officialErrorBody(message, kind string, param, code any) map[string]any {
	return map[string]any{"error": map[string]any{
		"message": message, "type": kind, "param": param, "code": code,
	}}
}

// gatewayQuotaBody is the weekly-quota refusal the dev-test gateway was
// observed to answer (the same body is recorded in
// internal/providers/httpjson/quota_test.go): HTTP 429 with error.code and
// error.type quota_exceeded and a nested error.quota object.
func gatewayQuotaBody() map[string]any {
	return map[string]any{"error": map[string]any{
		"code": "quota_exceeded", "type": "quota_exceeded",
		"message": "Quota exceeded (weekly). Limit: $100.00, used: $101.21. Resets at 2026-11-09T00:00:00Z",
		"quota": map[string]any{
			"dimension": "weekly", "group_name": "API", "limit_usd": 100,
			"resets_at": "2026-11-09T00:00:00Z", "scope": "model_group", "used_usd": 101.20632699999999,
		},
	}}
}

// documentedFault answers the fault scenarios the shared Intercept table does
// not cover, all opt-in through X-Fake-Scenario or a fake-<scenario> key. The
// 403 and 429 bodies are the gateway refusals recorded in the repository; the
// 503 body is constructed from the official error-code guide.
func (h *handler) documentedFault(w http.ResponseWriter, scenario fakeupstream.Scenario) bool {
	switch scenario {
	case fakeupstream.ScenarioQuotaExceeded:
		writeJSON(w, http.StatusTooManyRequests, gatewayQuotaBody())
	case fakeupstream.ScenarioForbidden:
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]any{
			"code": "eca_access_denied", "message": "Eca IP not in whitelist",
		}})
	case fakeupstream.ScenarioServiceUnavailable:
		w.Header().Set("Retry-After", h.env.RetryAfterSeconds())
		writeJSON(w, http.StatusServiceUnavailable, officialErrorBody("The model is temporarily overloaded. Please retry your request after a brief delay.", "service_unavailable_error", nil, "server_is_overloaded"))
	default:
		return false
	}
	return true
}

func (h *handler) render(endpoint string) fakeupstream.ErrorRenderer {
	return func(w http.ResponseWriter, _ *http.Request, scenario fakeupstream.Scenario) {
		status, kind, code, message := http.StatusInternalServerError, "server_error", "internal_error", "The server had an error while processing your request."
		switch scenario {
		case fakeupstream.ScenarioInsufficientBalance:
			status, kind, code, message = http.StatusPaymentRequired, "insufficient_quota", "insufficient_quota", "insufficient balance"
		case fakeupstream.ScenarioKeyQuota:
			status, kind, code, message = http.StatusPaymentRequired, "insufficient_quota", "insufficient_quota", "API key credit limit reached"
		case fakeupstream.ScenarioRateLimit:
			status, kind, code, message = http.StatusTooManyRequests, "rate_limit_error", "rate_limit_exceeded", "rate limit exceeded"
			w.Header().Set("Retry-After", h.env.RetryAfterSeconds())
		}
		writeJSON(w, status, h.errorBody(endpoint, kind, code, message, ""))
	}
}

// request is what the fake reads out of a JSON or multipart request.
type request struct {
	prompt string
	model  string
	form   map[string]string
	hasImg bool
	// count is the requested candidate count (n): 1..10. The recorded samples
	// only ever ask for one, so n>1 is a constructed-from-docs behavior.
	count int
}

func readRequest(r *http.Request, endpoint string) (request, bool) {
	out := request{form: map[string]string{}}
	if endpoint == "/images/generations" {
		var body map[string]any
		if json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&body) != nil {
			return out, false
		}
		for key, value := range body {
			switch typed := value.(type) {
			case string:
				out.form[key] = typed
			case float64:
				if key == "n" {
					out.count = int(typed)
				}
			}
		}
	} else {
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
			return out, false
		}
		reader := multipart.NewReader(io.LimitReader(r.Body, 64<<20), params["boundary"])
		for {
			part, err := reader.NextPart()
			if err != nil {
				break
			}
			data, _ := io.ReadAll(part)
			switch name := part.FormName(); {
			case name == "image" || name == "image[]":
				out.hasImg = out.hasImg || len(data) > 0
			case part.FileName() == "":
				out.form[name] = string(data)
			}
		}
	}
	if out.count == 0 {
		if parsed, err := strconv.Atoi(strings.TrimSpace(out.form["n"])); err == nil {
			out.count = parsed
		}
	}
	out.prompt, out.model = strings.TrimSpace(out.form["prompt"]), strings.TrimSpace(out.form["model"])
	return out, true
}

func (h *handler) create(w http.ResponseWriter, r *http.Request, endpoint string) {
	if fakeupstream.BearerKey(r) == "" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, unauthorizedBody)
		return
	}
	if h.documentedFault(w, fakeupstream.ScenarioOf(r)) {
		return
	}
	if h.env.Intercept(w, r, fakeupstream.PhaseCreate, h.render(endpoint)) {
		return
	}
	req, parsed := readRequest(r, endpoint)
	switch {
	case !parsed || (endpoint == "/images/generations" && req.model == ""):
		// The official Create image edit reference makes model optional there.
		writeJSON(w, http.StatusBadRequest, h.errorBody(endpoint, "invalid_request_error", "missing_required_parameter", "Missing required parameter: 'model'.", "model"))
		return
	case endpoint == "/images/edits" && !req.hasImg:
		writeJSON(w, http.StatusBadRequest, h.invalid[endpoint])
		return
	case req.prompt == "":
		writeJSON(w, http.StatusBadRequest, h.errorBody(endpoint, "invalid_request_error", "missing_required_parameter", "Missing required parameter: 'prompt'.", "prompt"))
		return
	}
	scenario := fakeupstream.ScenarioOf(r)
	switch scenario {
	case fakeupstream.ScenarioLostResponse:
		fakeupstream.AbortConnection(w) // the picture was made; the response is lost
		return
	case fakeupstream.ScenarioTaskFailed:
		writeJSON(w, http.StatusBadRequest, h.errorBody(endpoint, "image_generation_user_error", "moderation_blocked",
			"Your request was rejected by the safety system. If you believe this is an error, contact us.", ""))
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
	body := fakeupstream.Clone(h.ok[endpoint])
	if list, ok := body["data"].([]any); ok && len(list) > 0 {
		if template, ok := list[0].(map[string]any); ok {
			// n is documented by the official Create image reference. The
			// committed samples only ask for one picture, so returning n
			// distinct pictures is constructed-from-docs.
			count := req.count
			if count < 1 {
				count = 1
			}
			if count > 10 {
				count = 10
			}
			items := make([]any, 0, count)
			for index := 0; index < count; index++ {
				item := fakeupstream.Clone(template)
				candidate := data
				if count > 1 {
					candidate = candidatePNG(index)
				}
				if scenario == fakeupstream.ScenarioArtifactCorrupt {
					candidate = fakeupstream.Corrupt(candidate)
				}
				item["b64_json"] = base64.StdEncoding.EncodeToString(candidate)
				if _, exists := item["generation_id"]; exists {
					item["generation_id"] = fmt.Sprintf("id-%d", index+1)
				}
				items = append(items, item)
			}
			body["data"] = items
		}
	}
	for _, field := range []string{"size", "quality", "output_format", "background"} {
		if value := req.form[field]; value != "" && value != "auto" {
			body[field] = value
		}
	}
	writeJSON(w, http.StatusOK, body)
}

// candidatePNG returns a deterministic solid-colour picture whose bytes differ
// per candidate. It is fake fixture data, not a Provider response.
func candidatePNG(index int) []byte {
	picture := image.NewRGBA(image.Rect(0, 0, 256, 256))
	shade := uint8(40 + index*18)
	draw.Draw(picture, picture.Bounds(), &image.Uniform{
		C: color.RGBA{R: shade, G: uint8(180 - index*12), B: uint8(90 + index*8), A: 255},
	}, image.Point{}, draw.Src)
	var encoded bytes.Buffer
	_ = png.Encode(&encoded, picture)
	return encoded.Bytes()
}
