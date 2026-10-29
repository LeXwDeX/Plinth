package jimengapiv3

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

//go:embed testdata/jimeng-api-v3-*.candidate.json
var sampleFS embed.FS

var sampleFiles = func() []string {
	names, err := fs.Glob(sampleFS, "testdata/jimeng-api-v3-*.candidate.json")
	if err != nil || len(names) == 0 {
		panic("jimeng/api-v3: no committed samples")
	}
	return names
}()

// Prefix is where this version is mounted: the stack's JIMENG_BASE_URL points
// at <fake>/jimeng (Plinth appends api/v3/images/generations).
const Prefix = "/jimeng/api/v3"

// samplePrefix is the path prefix the official API uses; the committed samples
// record it.
const samplePrefix = "/api/v3"

const (
	docsRoot = "https://www.volcengine.com/docs/82379/1541523"
	captured = "2026-10-29"
)

var endpoints = []fakeupstream.Endpoint{
	{Method: "POST", Path: "/images/generations", Evidence: fakeupstream.EvidenceReal, Sample: "POST /api/v3/images/generations",
		DocURL: docsRoot, CheckedAt: captured,
		Note: "response_format b64_json answers an inline picture, url a signed TOS URL (both real samples); 401 AuthenticationError and 400 MissingParameter are real samples. A content-policy refusal, 402/429/5xx are not observed and reuse the real error envelope"},
	{Method: "GET", Path: "/files/{name}", Evidence: fakeupstream.EvidenceConstructed, DocURL: docsRoot, CheckedAt: captured,
		Note: "the signed picture host is not part of the API; this route serves the fixed JPEG a response_format=url answer names. An expired URL answers the 403 JSON body the official TOS host returned for a URL past its X-Tos-Expires (observed 2026-10-29 by moving X-Tos-Date into the past)"},
}

// Version is the Jimeng (Volcengine Ark) api/v3 fake.
func Version() fakeupstream.Version {
	return fakeupstream.Version{
		Provider: "jimeng", API: "api-v3", Prefix: Prefix, Official: OfficialBase,
		Endpoints: endpoints, Handler: newHandler,
	}
}

// fileState is what a response_format=url answer remembers about its picture.
type fileState struct {
	scenario      fakeupstream.Scenario
	expiredIssues int
}

type handler struct {
	env     *fakeupstream.Env
	ok      map[string]any
	url     map[string]any
	invalid map[string]any
	denied  map[string]any

	mu    sync.Mutex
	files map[string]fileState
}

func newHandler(env *fakeupstream.Env) (http.Handler, error) {
	samples, err := fakeupstream.LoadSamples(sampleFS, sampleFiles...)
	if err != nil {
		return nil, err
	}
	h := &handler{env: env, files: map[string]fileState{}}
	for _, exchange := range samples.Find("POST "+samplePrefix+"/images/generations", "") {
		body, err := fakeupstream.JSONBody(exchange)
		if err != nil {
			return nil, err
		}
		switch exchange.Response.Status {
		case http.StatusOK:
			if strings.Contains(string(exchange.Response.Body.JSON), `"url"`) {
				h.url = body
			} else {
				h.ok = body
			}
		case http.StatusBadRequest:
			h.invalid = body
		case http.StatusUnauthorized:
			h.denied = body
		}
	}
	if h.ok == nil || h.url == nil || h.invalid == nil || h.denied == nil {
		return nil, errors.New("jimeng/api-v3 sample lacks an inline, a URL, a 400 or a 401 exchange")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /images/generations", h.generate)
	mux.HandleFunc("GET /files/{name}", h.getFile)
	return mux, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	raw, _ := json.Marshal(body)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(raw, '\n'))
}

// errorBody clones the real error envelope with another code and message.
func errorBody(template map[string]any, kind, code, message string) map[string]any {
	body := fakeupstream.Clone(template)
	if object, ok := body["error"].(map[string]any); ok {
		object["code"], object["message"], object["type"] = code, message, kind
	}
	return body
}

func (h *handler) render(w http.ResponseWriter, _ *http.Request, scenario fakeupstream.Scenario) {
	status, kind, code, message := http.StatusInternalServerError, "InternalServiceError", "InternalServiceError", "internal service error"
	switch scenario {
	case fakeupstream.ScenarioInsufficientBalance:
		status, kind, code, message = http.StatusForbidden, "Forbidden", "AccountOverdueError", "the account is in arrears; recharge it before calling the service"
	case fakeupstream.ScenarioKeyQuota:
		status, kind, code, message = http.StatusTooManyRequests, "TooManyRequests", "QuotaExceeded", "the API key quota is exhausted"
	case fakeupstream.ScenarioRateLimit:
		status, kind, code, message = http.StatusTooManyRequests, "TooManyRequests", "RateLimitExceeded", "the request rate limit is exceeded"
		w.Header().Set("Retry-After", h.env.RetryAfterSeconds())
	}
	writeJSON(w, status, errorBody(h.invalid, kind, code, message))
}

type generationRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	ResponseFormat string `json:"response_format"`
	Size           string `json:"size"`
}

func (h *handler) generate(w http.ResponseWriter, r *http.Request) {
	if fakeupstream.BearerKey(r) == "" {
		writeJSON(w, http.StatusUnauthorized, h.denied)
		return
	}
	if h.env.Intercept(w, r, fakeupstream.PhaseCreate, h.render) {
		return
	}
	var request generationRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&request); err != nil || strings.TrimSpace(request.Prompt) == "" {
		writeJSON(w, http.StatusBadRequest, h.invalid)
		return
	}
	scenario := fakeupstream.ScenarioOf(r)
	switch scenario {
	case fakeupstream.ScenarioLostResponse:
		fakeupstream.AbortConnection(w)
		return
	case fakeupstream.ScenarioTaskFailed:
		writeJSON(w, http.StatusBadRequest, errorBody(h.invalid, "BadRequest", "OutputImageSensitiveContentDetected",
			"the generated image may contain sensitive information and has been blocked"))
		return
	}
	file, err := fakeupstream.ImageFile("jpeg")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	template := h.ok
	if request.ResponseFormat == "url" {
		template = h.url
	}
	body := fakeupstream.Clone(template)
	if request.Model != "" {
		body["model"] = request.Model
	}
	if list, ok := body["data"].([]any); ok && len(list) > 0 {
		item, _ := list[0].(map[string]any)
		if request.Size != "" {
			item["size"] = request.Size
		}
		if request.ResponseFormat == "url" {
			h.mu.Lock()
			id := fmt.Sprintf("fake-image-%d", len(h.files)+1)
			h.files[id] = fileState{scenario: scenario, expiredIssues: fakeupstream.ExpiredIssues(r)}
			h.mu.Unlock()
			item["url"] = h.env.SignArtifactURL(
				h.env.ArtifactOriginFor(r)+Prefix+"/files/"+id+".jpeg?X-Tos-Expires=86400", scenario, h.env.IssueArtifactURLs(id))
		} else {
			data := file.Data
			if scenario == fakeupstream.ScenarioArtifactCorrupt {
				data = fakeupstream.Corrupt(data)
			}
			item["b64_json"] = base64.StdEncoding.EncodeToString(data)
		}
	}
	writeJSON(w, http.StatusOK, body)
}

// expiredBody is the body the official TOS host answered for a signed URL past
// its lifetime (403, observed 2026-10-29); the identifiers are fixed.
const expiredBody = `{"Code":"AccessDenied","RequestId":"fake-request","HostId":"fake-host","Message":"Request has expired","EC":"0002-00000032","ExpiresSeconds":86400,"Expires":"2026-01-02T00:00:00Z","ServerTime":"2026-01-03T00:00:00Z"}`

func (h *handler) getFile(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(r.PathValue("name"), ".jpeg")
	h.mu.Lock()
	state, known := h.files[id]
	h.mu.Unlock()
	scenario := state.scenario
	if !known || scenario == fakeupstream.ScenarioArtifactNotFound {
		http.NotFound(w, r)
		return
	}
	if fakeupstream.ArtifactURLExpired(r, scenario, state.expiredIssues) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, expiredBody)
		return
	}
	file, err := fakeupstream.ImageFile("jpeg")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := file.Data
	if scenario == fakeupstream.ScenarioArtifactCorrupt {
		data = fakeupstream.Corrupt(data)
	}
	w.Header().Set("Content-Type", file.MediaType)
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	_, _ = w.Write(data)
}
