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
	docsRoot      = "https://www.volcengine.com/docs/82379/1541523"
	errorDocsRoot = "https://docs.volcengine.com/docs/82379/1299023"
	captured      = "2026-10-29"
	docsChecked   = "2026-11-08"
)

var endpoints = []fakeupstream.Endpoint{
	{Method: "POST", Path: "/images/generations", Evidence: fakeupstream.EvidenceReal, Sample: "POST /api/v3/images/generations",
		DocURL: docsRoot, CheckedAt: docsChecked,
		Note: "response_format b64_json answers an inline picture, url a signed TOS URL (both real samples); 401 AuthenticationError and 400 MissingParameter are real samples. The sequential generation fields (sequential_image_generation, sequential_image_generation_options.max_images, usage.generated_images) and the per-picture data[].error follow " + docsRoot + " (checked " + docsChecked + "); a content-policy refusal, the 403/404/408/429/5xx answers and the documented-code channel (" + UpstreamCodeHeader + ") are constructed from " + errorDocsRoot + " (checked " + docsChecked + ") and reuse the real error envelope"},
	{Method: "GET", Path: "/files/{name}", Evidence: fakeupstream.EvidenceConstructed, DocURL: docsRoot, CheckedAt: captured,
		Note: "the signed picture host is not part of the API; this route serves the fixed JPEG a response_format=url answer names. An expired URL answers the 403 JSON body the official TOS host returned for a URL past its X-Tos-Expires (observed 2026-10-29 by moving X-Tos-Date into the past)"},
}

// UpstreamCodeHeader names one documented Ark error code the request should be
// answered with, so tests can reach codes no X-Fake-Scenario word covers. It
// wins over the scenario; an unknown value answers 400 InvalidParameter.
const UpstreamCodeHeader = "X-Fake-Upstream-Code"

// documentedCodes maps the codes of the official Ark reasoning error table
// (errorDocsRoot, checked docsChecked) to the HTTP status and error.type it
// gives them. The catalog's jimeng-images connection declares the first eight;
// the rest are the authentication, refusal, timeout and unavailable answers an
// image request can meet.
var documentedCodes = map[string]struct {
	status int
	kind   string
}{
	"AccountOverdueError":                       {http.StatusForbidden, "Forbidden"},
	"QuotaExceeded":                             {http.StatusTooManyRequests, "TooManyRequests"},
	"SetLimitExceeded":                          {http.StatusTooManyRequests, "TooManyRequests"},
	"InputTextSensitiveContentDetected":         {http.StatusBadRequest, "BadRequest"},
	"InputImageSensitiveContentDetected":        {http.StatusBadRequest, "BadRequest"},
	"OutputTextSensitiveContentDetected":        {http.StatusBadRequest, "BadRequest"},
	"OutputImageSensitiveContentDetected":       {http.StatusBadRequest, "BadRequest"},
	"SensitiveContentDetected":                  {http.StatusBadRequest, "BadRequest"},
	"AuthenticationError":                       {http.StatusUnauthorized, "Unauthorized"},
	"InvalidParameter":                          {http.StatusBadRequest, "BadRequest"},
	"InvalidEndpointOrModel.NotFound":           {http.StatusNotFound, "NotFound"},
	"ModelNotOpen":                              {http.StatusNotFound, "NotFound"},
	"RequestTimeout":                            {http.StatusRequestTimeout, "RequestTimeout"},
	"OperationDenied.ServiceNotOpen":            {http.StatusForbidden, "Forbidden"},
	"AccessDenied":                              {http.StatusForbidden, "Forbidden"},
	"ServiceConnectionRefused":                  {http.StatusServiceUnavailable, "ServiceUnavailable"},
	"NoAvailableService":                        {http.StatusServiceUnavailable, "ServiceUnavailable"},
	"InternalServiceError":                      {http.StatusInternalServerError, "InternalServerError"},
	"RateLimitExceeded.EndpointRPMExceeded":     {http.StatusTooManyRequests, "TooManyRequests"},
	"RateLimitExceeded.EndpointTPMExceeded":     {http.StatusTooManyRequests, "TooManyRequests"},
	"RateLimitExceeded.EndpointFlexTPMExceeded": {http.StatusTooManyRequests, "TooManyRequests"},
	"AccountRateLimitExceeded":                  {http.StatusTooManyRequests, "TooManyRequests"},
	"ModelAccountRpmRateLimitExceeded":          {http.StatusTooManyRequests, "TooManyRequests"},
	"ServerOverloaded":                          {http.StatusTooManyRequests, "TooManyRequests"},
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
	status, kind, code, message := http.StatusInternalServerError, "InternalServerError", "InternalServiceError", "internal service error"
	switch scenario {
	case fakeupstream.ScenarioInsufficientBalance:
		status, kind, code, message = http.StatusForbidden, "Forbidden", "AccountOverdueError", "the account is in arrears; recharge it before calling the service"
	case fakeupstream.ScenarioKeyQuota:
		status, kind, code, message = http.StatusTooManyRequests, "TooManyRequests", "QuotaExceeded", "the API key quota is exhausted"
	case fakeupstream.ScenarioQuotaExceeded:
		status, kind, code, message = http.StatusTooManyRequests, "TooManyRequests", "SetLimitExceeded", "the account has reached its set usage limit"
	case fakeupstream.ScenarioRateLimit:
		status, kind, code, message = http.StatusTooManyRequests, "TooManyRequests", "RateLimitExceeded.EndpointRPMExceeded", "the request rate limit is exceeded"
		w.Header().Set("Retry-After", h.env.RetryAfterSeconds())
	case fakeupstream.ScenarioForbidden:
		status, kind, code, message = http.StatusForbidden, "Forbidden", "AccessDenied", "the request is denied access to the requested resource"
	case fakeupstream.ScenarioModelNotFound:
		status, kind, code, message = http.StatusNotFound, "NotFound", "InvalidEndpointOrModel.NotFound", "the model or endpoint does not exist or is not accessible"
	case fakeupstream.ScenarioServiceUnavailable:
		status, kind, code, message = http.StatusServiceUnavailable, "ServiceUnavailable", "NoAvailableService", "no available service"
	}
	writeJSON(w, status, errorBody(h.invalid, kind, code, message))
}

// renderDocumentedCode answers one code of the official error table with the
// status and type the table gives it.
func (h *handler) renderDocumentedCode(w http.ResponseWriter, code string) {
	entry, ok := documentedCodes[code]
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorBody(h.invalid, "BadRequest", "InvalidParameter",
			"the fake does not know the documented upstream code "+code))
		return
	}
	writeJSON(w, entry.status, errorBody(h.invalid, entry.kind, code, "the documented upstream error "+code))
}

type generationRequest struct {
	Model                            string `json:"model"`
	Prompt                           string `json:"prompt"`
	ResponseFormat                   string `json:"response_format"`
	Size                             string `json:"size"`
	SequentialImageGeneration        string `json:"sequential_image_generation"`
	SequentialImageGenerationOptions struct {
		MaxImages int `json:"max_images"`
	} `json:"sequential_image_generation_options"`
}

const (
	// defaultMaxImages and maxImagesLimit are the documented default and upper
	// bound of sequential_image_generation_options.max_images.
	defaultMaxImages = 15
	maxImagesLimit   = 15
)

// imageCount is how many data items the answer carries: one unless the request
// asks for sequential generation, in which case max_images wins (the documented
// default and limit when the request gives none or too many).
func imageCount(request generationRequest) int {
	if request.SequentialImageGeneration != "auto" {
		return 1
	}
	count := request.SequentialImageGenerationOptions.MaxImages
	if count <= 0 {
		count = defaultMaxImages
	}
	if count > maxImagesLimit {
		count = maxImagesLimit
	}
	return count
}

func (h *handler) generate(w http.ResponseWriter, r *http.Request) {
	scenario := fakeupstream.ScenarioOf(r)
	if scenario == fakeupstream.ScenarioInvalidKey {
		writeJSON(w, http.StatusUnauthorized, h.denied)
		return
	}
	if fakeupstream.BearerKey(r) == "" {
		writeJSON(w, http.StatusUnauthorized, h.denied)
		return
	}
	if code := strings.TrimSpace(r.Header.Get(UpstreamCodeHeader)); code != "" {
		h.renderDocumentedCode(w, code)
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
	switch scenario {
	case fakeupstream.ScenarioLostResponse:
		fakeupstream.AbortConnection(w)
		return
	case fakeupstream.ScenarioTaskFailed:
		writeJSON(w, http.StatusBadRequest, errorBody(h.invalid, "BadRequest", "OutputImageSensitiveContentDetected",
			"the generated image may contain sensitive information and has been blocked"))
		return
	case fakeupstream.ScenarioForbidden, fakeupstream.ScenarioModelNotFound,
		fakeupstream.ScenarioServiceUnavailable, fakeupstream.ScenarioQuotaExceeded:
		h.render(w, r, scenario)
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
	list, ok := body["data"].([]any)
	if !ok || len(list) == 0 {
		writeJSON(w, http.StatusOK, body)
		return
	}
	prototype, ok := list[0].(map[string]any)
	if !ok {
		writeJSON(w, http.StatusOK, body)
		return
	}
	// ScenarioPartialImages fails one picture of a sequential answer; a request
	// for fewer than two pictures still answers them whole. The slice capacity
	// is the documented maximum, never the request value, so the allocation
	// cannot scale with user input (CodeQL go/overflowing-allocation).
	count := imageCount(request)
	items := make([]any, 0, maxImagesLimit)
	generated := 0
	for index := 0; index < count; index++ {
		if scenario == fakeupstream.ScenarioPartialImages && index == 1 {
			// The per-picture error of a partly failed sequential request is
			// documented (code and message only) but not observed; the other
			// pictures keep the real sample's shape.
			items = append(items, map[string]any{"error": map[string]any{
				"code":    "OutputImageSensitiveContentDetected",
				"message": "the generated image may contain sensitive information and has been blocked",
			}})
			continue
		}
		item := fakeupstream.Clone(prototype)
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
		items = append(items, item)
		generated++
	}
	body["data"] = items
	if request.SequentialImageGeneration == "auto" {
		if usage, ok := body["usage"].(map[string]any); ok {
			usage["generated_images"] = generated
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
