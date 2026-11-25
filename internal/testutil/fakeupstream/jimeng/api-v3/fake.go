package jimengapiv3

import (
	"bytes"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"

	_ "golang.org/x/image/webp"

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
	docsRoot      = "https://docs.volcengine.com/docs/82379/1541523"
	errorDocsRoot = "https://docs.volcengine.com/docs/82379/1299023"
	captured      = "2026-10-29"
	docsChecked   = "2026-11-25"
)

var endpoints = []fakeupstream.Endpoint{
	{Method: "POST", Path: "/images/generations", Evidence: fakeupstream.EvidenceReal, Sample: "POST /api/v3/images/generations",
		DocURL: docsRoot, CheckedAt: docsChecked,
		Note: "response_format b64_json answers an inline picture, url a signed TOS URL (both real samples); 401 AuthenticationError and 400 MissingParameter are real samples. The picture is a JPEG drawn at the requested size (an explicit WxH, the model's resolution words as squares, 2048x2048 when omitted) and data[].size reports it; the committed samples only ask for 2048x2048, so other sizes are constructed. The documented parameter contract is enforced with constructed 400 InvalidParameter answers that reuse the real envelope (param names the field; the real wording is not recorded, the free probe with size 1024x1024 is planned): size per model (Seedream 4.0 as documented: WxH with 921600 to 16777216 total pixels, or 1K, 2K or 4K; every other model keeps the 4.5 rules, which the current page no longer covers and only a secondary source gives: 3686400 to 16777216 total pixels, or 2K or 4K; both with a ratio of at most 16), response_format (url or b64_json), sequential_image_generation (auto or disabled), max_images (1 to 15, never clamped, only checked for group generation), at most 14 reference images (4.0 and 4.5), reference plus generated pictures at most 15 (the default max_images shrinks to the room left), and every readable reference picture (data URI) at least 15 pixels on each side, ratio at most 16, at most 6000x6000 pixels and 30 MB. The sequential generation fields (sequential_image_generation, sequential_image_generation_options.max_images, usage.generated_images) and the per-picture data[].error follow " + docsRoot + " (checked " + docsChecked + "); a content-policy refusal, the 403/404/408/429/5xx answers and the documented-code channel (" + UpstreamCodeHeader + ", now including the reason-suffixed content-safety, risk-detection, model-access (InvalidEndpointOrModel.ModelIDAccessDisabled), OperationDenied.ServiceOverdue and RequestBurstTooFast codes; 429 codes carry a constructed Retry-After the documents do not confirm) are constructed from " + errorDocsRoot + " (checked " + docsChecked + ") and reuse the real error envelope. " + GeneratedImagesHeader + " makes a group answer carry fewer pictures than max_images (constructed)"},
	{Method: "GET", Path: "/files/{name}", Evidence: fakeupstream.EvidenceConstructed, DocURL: docsRoot, CheckedAt: captured,
		Note: "the signed picture host is not part of the API; this route serves the fixed JPEG a response_format=url answer names. An expired URL answers the 403 JSON body the official TOS host returned for a URL past its X-Tos-Expires (observed 2026-10-29 by moving X-Tos-Date into the past)"},
}

// UpstreamCodeHeader names one documented Ark error code the request should be
// answered with, so tests can reach codes no X-Fake-Scenario word covers. It
// wins over the scenario; an unknown value answers 400 InvalidParameter.
const UpstreamCodeHeader = "X-Fake-Upstream-Code"

// GeneratedImagesHeader makes a group-generation answer carry fewer pictures
// than max_images, the way the model may stop early (the answer is still 200 and
// usage.generated_images counts the pictures made). Whether the real service
// does this, and how often, is not recorded (the group probe is planned), so the
// behavior is constructed. It never raises the count above what was asked.
const GeneratedImagesHeader = "X-Fake-Generated-Images"

// documentedCodes maps the codes of the official Ark reasoning error table
// (errorDocsRoot, checked docsChecked) to the HTTP status and error.type it
// gives them. The catalog's jimeng-images connection declares the first eight
// and the suffixed, risk-detection and model-access codes at the end; the rest
// are the authentication, refusal, timeout, unavailable and rate-limit answers
// an image request can meet.
var documentedCodes = map[string]struct {
	status int
	kind   string
}{
	"AccountOverdueError":                 {http.StatusForbidden, "Forbidden"},
	"QuotaExceeded":                       {http.StatusTooManyRequests, "TooManyRequests"},
	"SetLimitExceeded":                    {http.StatusTooManyRequests, "TooManyRequests"},
	"InputTextSensitiveContentDetected":   {http.StatusBadRequest, "BadRequest"},
	"InputImageSensitiveContentDetected":  {http.StatusBadRequest, "BadRequest"},
	"OutputTextSensitiveContentDetected":  {http.StatusBadRequest, "BadRequest"},
	"OutputImageSensitiveContentDetected": {http.StatusBadRequest, "BadRequest"},
	"SensitiveContentDetected":            {http.StatusBadRequest, "BadRequest"},
	"AuthenticationError":                 {http.StatusUnauthorized, "Unauthorized"},
	"InvalidParameter":                    {http.StatusBadRequest, "BadRequest"},
	"InvalidEndpointOrModel.NotFound":     {http.StatusNotFound, "NotFound"},
	"ModelNotOpen":                        {http.StatusNotFound, "NotFound"},
	"RequestTimeout":                      {http.StatusRequestTimeout, "RequestTimeout"},
	"OperationDenied.ServiceNotOpen":      {http.StatusForbidden, "Forbidden"},
	// An overdue account balance; the Catalog reads it like AccountOverdueError.
	"OperationDenied.ServiceOverdue":            {http.StatusForbidden, "Forbidden"},
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
	// The reason-suffixed content-safety codes, the risk-detection codes and
	// the model-access codes the Catalog's jimeng-images mapping already
	// declares (#487; the official table read 2026-11-24).
	"InputTextSensitiveContentDetected.PolicyViolation":     {http.StatusBadRequest, "BadRequest"},
	"InputTextSensitiveContentDetected.PrivacyInformation":  {http.StatusBadRequest, "BadRequest"},
	"InputImageSensitiveContentDetected.PolicyViolation":    {http.StatusBadRequest, "BadRequest"},
	"InputImageSensitiveContentDetected.PrivacyInformation": {http.StatusBadRequest, "BadRequest"},
	"OutputImageSensitiveContentDetected.PolicyViolation":   {http.StatusBadRequest, "BadRequest"},
	"OutputImageSensitiveContentDetected.DeepFake":          {http.StatusBadRequest, "BadRequest"},
	"InputImageRiskDetection":                               {http.StatusBadRequest, "BadRequest"},
	"OutputImageRiskDetection":                              {http.StatusBadRequest, "BadRequest"},
	// The risk-detection service itself failed (message "Internal error."):
	// the Catalog reads it as a retryable upstream failure.
	"ContentSecurityDetectionError": {http.StatusBadRequest, "BadRequest"},
	// The official table names the model-ID access refusal with its
	// InvalidEndpointOrModel prefix (checked 2026-11-25).
	"InvalidEndpointOrModel.ModelIDAccessDisabled": {http.StatusNotFound, "NotFound"},
	"UnsupportedModel": {http.StatusNotFound, "NotFound"},
	// The 429 family of the official table. RequestBurstTooFast (a sudden
	// request burst tripped the protection) is the newest; the others are the
	// per-account limits an image request can meet (IPM is images per minute).
	"RequestBurstTooFast":                         {http.StatusTooManyRequests, "TooManyRequests"},
	"ModelAccountTpmRateLimitExceeded":            {http.StatusTooManyRequests, "TooManyRequests"},
	"ModelAccountFlexTpmRateLimitExceeded":        {http.StatusTooManyRequests, "TooManyRequests"},
	"APIAccountRpmRateLimitExceeded":              {http.StatusTooManyRequests, "TooManyRequests"},
	"ModelAccountIpmRateLimitExceeded":            {http.StatusTooManyRequests, "TooManyRequests"},
	"ModelAccountTpdRateLimitExceeded":            {http.StatusTooManyRequests, "TooManyRequests"},
	"ModelAccountConcurrentRequestsLimitExceeded": {http.StatusTooManyRequests, "TooManyRequests"},
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
	// width, height and variant are the picture the URL names, weight the
	// byte count a test asked for (#629).
	width, height, variant int
	weight                 fakeupstream.PictureWeight
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
	// A rate-limit answer tells the caller when to come back, like the
	// rate-limit scenario does. The exhausted-quota codes are not rate limits:
	// waiting does not help, so they carry no Retry-After. The header itself is
	// constructed (the official table names none).
	if entry.status == http.StatusTooManyRequests && code != "QuotaExceeded" && code != "SetLimitExceeded" {
		w.Header().Set("Retry-After", h.env.RetryAfterSeconds())
	}
	writeJSON(w, entry.status, errorBody(h.invalid, entry.kind, code, "the documented upstream error "+code))
}

type generationRequest struct {
	Model                            string          `json:"model"`
	Prompt                           string          `json:"prompt"`
	ResponseFormat                   string          `json:"response_format"`
	Size                             string          `json:"size"`
	Image                            json.RawMessage `json:"image"`
	SequentialImageGeneration        string          `json:"sequential_image_generation"`
	SequentialImageGenerationOptions struct {
		MaxImages *int `json:"max_images"`
	} `json:"sequential_image_generation_options"`
}

const (
	// defaultMaxImages and maxImagesLimit are the documented default and upper
	// bound of sequential_image_generation_options.max_images (range [1, 15]).
	defaultMaxImages = 15
	maxImagesLimit   = 15
	// Reference pictures plus generated pictures total at most 15; the limit of
	// reference pictures alone depends on the model (modelLimits).
	maxTotalImages = 15
	// The documented upper bounds of a WxH size, shared by Seedream 4.0 and
	// 4.5: total pixels at most 16777216 (4096x4096) and an aspect ratio
	// between 1/16 and 16. The lower bound and the resolution words depend on
	// the model (modelLimits); the model picks the aspect ratio of a word
	// itself, which the fake draws as a square.
	maxSizePixels = 16_777_216
	maxSizeRatio  = 16
	defaultSize   = "2048x2048"
	// The documented bounds of a reference picture: each side above 14 pixels,
	// aspect ratio 1/16 to 16, at most 6000x6000 pixels, at most 30 MB.
	minReferenceSide = 15
	maxReferencePix  = 36_000_000
	maxReferenceSize = 30_000_000
)

// refusal is a constructed 400 InvalidParameter for a value the documented
// contract rejects. The real answer's wording for these is not recorded (the
// free probe with size 1024x1024 is planned); the envelope is the real 400's.
type refusal struct{ param, detail string }

// modelLimits are the size and reference rules that differ between the
// Seedream models the Catalog registers. The official page (docsRoot, checked
// docsChecked) gives Seedream 4.0 the words 1K, 2K and 4K, a WxH of at least
// 921600 pixels (1280x720) and at most 14 reference pictures. The page no
// longer covers 4.5: its rules (the words 2K and 4K, at least 3686400 pixels,
// that is 2560x1440, and 14 reference pictures) come from a secondary source
// only, and they stay the fake's rules for every model that is not 4.0.
type modelLimits struct {
	// words maps each resolution word to the side of the square the fake draws.
	words              map[string]int
	wordList           string
	minSizePixels      int
	maxReferenceImages int
}

var (
	seedream40Limits = modelLimits{
		words: map[string]int{"1K": 1024, "2K": 2048, "4K": 4096}, wordList: "1K, 2K, 4K",
		minSizePixels: 921_600, maxReferenceImages: 14,
	}
	seedream45Limits = modelLimits{
		words: map[string]int{"2K": 2048, "4K": 4096}, wordList: "2K, 4K",
		minSizePixels: 3_686_400, maxReferenceImages: 14,
	}
)

// limitsFor picks the rules of the requested model: Seedream 4.0 by its model
// ID (doubao-seedream-4-0-<date>), the 4.5 rules for every other model.
func limitsFor(model string) modelLimits {
	if strings.Contains(model, "seedream-4-0") {
		return seedream40Limits
	}
	return seedream45Limits
}

// resolveSize turns the size parameter into the pixel size of the picture: an
// explicit WxH, a resolution word of the model (a square) or the documented
// default.
func (limits modelLimits) resolveSize(value string) (width, height int, bad *refusal) {
	value = strings.TrimSpace(value)
	if value == "" {
		width, height, _ = fakeupstream.ParseSize(defaultSize)
		return width, height, nil
	}
	if side, ok := limits.words[value]; ok {
		return side, side, nil
	}
	width, height, ok := fakeupstream.ParseSize(value)
	switch {
	case !ok:
		return 0, 0, &refusal{"size", "size must be one of " + limits.wordList + " or WIDTHxHEIGHT in pixels"}
	case width*height < limits.minSizePixels || width*height > maxSizePixels:
		return 0, 0, &refusal{"size", fmt.Sprintf("the total pixels of %dx%d must be between %d and %d", width, height, limits.minSizePixels, maxSizePixels)}
	case width > maxSizeRatio*height || height > maxSizeRatio*width:
		return 0, 0, &refusal{"size", fmt.Sprintf("the aspect ratio of %dx%d must be between 1/%d and %d", width, height, maxSizeRatio, maxSizeRatio)}
	}
	return width, height, nil
}

// referenceImages reads the image parameter (a string or an array of strings)
// and applies the documented limits to every reference picture it can read.
// A URL is not fetched; a data URI is decoded far enough to read its size. A
// picture whose format the fake cannot read is accepted as it is.
func referenceImages(raw json.RawMessage, maxReferenceImages int) (count int, bad *refusal) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return 0, nil
	}
	var values []string
	var single string
	switch {
	case json.Unmarshal(raw, &values) == nil:
	case json.Unmarshal(raw, &single) == nil:
		values = []string{single}
	default:
		return 0, &refusal{"image", "image must be a string or an array of strings"}
	}
	if len(values) > maxReferenceImages {
		return 0, &refusal{"image", fmt.Sprintf("at most %d reference images are accepted, got %d", maxReferenceImages, len(values))}
	}
	for index, value := range values {
		if bad := checkReferenceImage(index, value); bad != nil {
			return 0, bad
		}
	}
	return len(values), nil
}

func checkReferenceImage(index int, value string) *refusal {
	label := fmt.Sprintf("image[%d]", index)
	switch {
	case strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://"):
		return nil
	case !strings.HasPrefix(value, "data:image/"):
		return &refusal{"image", label + " must be an http(s) URL or a data:image/<format>;base64,<data> URI"}
	}
	_, encoded, found := strings.Cut(value, ";base64,")
	if !found {
		return &refusal{"image", label + " must be a base64 data URI"}
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return &refusal{"image", label + " is not valid base64"}
	}
	if len(data) > maxReferenceSize {
		return &refusal{"image", fmt.Sprintf("%s is larger than %d bytes", label, maxReferenceSize)}
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		if errors.Is(err, image.ErrFormat) {
			return nil // a format the fake cannot read: bmp, tiff, heic...
		}
		return &refusal{"image", label + " could not be decoded"}
	}
	switch {
	case config.Width < minReferenceSide || config.Height < minReferenceSide:
		return &refusal{"image", fmt.Sprintf("%s is %dx%d: each side must be greater than 14 pixels", label, config.Width, config.Height)}
	case config.Width > maxSizeRatio*config.Height || config.Height > maxSizeRatio*config.Width:
		return &refusal{"image", fmt.Sprintf("%s is %dx%d: the aspect ratio must be between 1/16 and 16", label, config.Width, config.Height)}
	case config.Width*config.Height > maxReferencePix:
		return &refusal{"image", fmt.Sprintf("%s is %dx%d: at most %d pixels are accepted", label, config.Width, config.Height, maxReferencePix)}
	}
	return nil
}

// validate applies the documented parameter contract and returns the pixel
// size to draw and the number of pictures to answer.
func (request generationRequest) validate() (width, height, count int, bad *refusal) {
	if request.ResponseFormat != "" && request.ResponseFormat != "url" && request.ResponseFormat != "b64_json" {
		return 0, 0, 0, &refusal{"response_format", "response_format must be url or b64_json"}
	}
	if mode := request.SequentialImageGeneration; mode != "" && mode != "auto" && mode != "disabled" {
		return 0, 0, 0, &refusal{"sequential_image_generation", "sequential_image_generation must be auto or disabled"}
	}
	limits := limitsFor(request.Model)
	width, height, bad = limits.resolveSize(request.Size)
	if bad != nil {
		return 0, 0, 0, bad
	}
	references, bad := referenceImages(request.Image, limits.maxReferenceImages)
	if bad != nil {
		return 0, 0, 0, bad
	}
	if request.SequentialImageGeneration != "auto" {
		return width, height, 1, nil
	}
	// max_images only applies to group generation; the documented default
	// (15) leaves room for the reference pictures, which count against the
	// total of 15.
	count = min(defaultMaxImages, maxTotalImages-references)
	if explicit := request.SequentialImageGenerationOptions.MaxImages; explicit != nil {
		if *explicit < 1 || *explicit > maxImagesLimit {
			return 0, 0, 0, &refusal{"sequential_image_generation_options.max_images", fmt.Sprintf("max_images must be between 1 and %d", maxImagesLimit)}
		}
		if references+*explicit > maxTotalImages {
			return 0, 0, 0, &refusal{"sequential_image_generation_options.max_images", fmt.Sprintf("%d reference images plus %d generated images exceed the total of %d", references, *explicit, maxTotalImages)}
		}
		count = *explicit
	}
	return width, height, count, nil
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
	width, height, count, bad := request.validate()
	if bad != nil {
		refused := errorBody(h.invalid, "BadRequest", "InvalidParameter",
			"The parameter `"+bad.param+"` specified in the request is not valid: "+bad.detail)
		if object, ok := refused["error"].(map[string]any); ok {
			object["param"] = bad.param
		}
		writeJSON(w, http.StatusBadRequest, refused)
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
	if request.SequentialImageGeneration == "auto" {
		if limit, err := strconv.Atoi(strings.TrimSpace(r.Header.Get(GeneratedImagesHeader))); err == nil && limit >= 1 {
			count = min(count, limit)
		}
	}
	items := make([]any, 0, maxImagesLimit)
	generated := 0
	weight := h.env.PictureWeightOf(r, request.Prompt, width, height)
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
		// The answer reports the size that was drawn (the 2K and 4K words are
		// resolved to pixels, as the real service does).
		item["size"] = fmt.Sprintf("%dx%d", width, height)
		if request.ResponseFormat == "url" {
			h.mu.Lock()
			id := fmt.Sprintf("fake-image-%d", len(h.files)+1)
			h.files[id] = fileState{scenario: scenario, expiredIssues: fakeupstream.ExpiredIssues(r), width: width, height: height, variant: index, weight: weight}
			h.mu.Unlock()
			item["url"] = h.env.SignArtifactURL(
				h.env.ArtifactOriginFor(r)+Prefix+"/files/"+id+".jpeg?X-Tos-Expires=86400", scenario, h.env.IssueArtifactURLs(id))
		} else {
			file, err := fakeupstream.WeightedPicture(fakeupstream.PictureSpec{Format: "jpeg", Width: width, Height: height, Variant: index}, weight)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
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
	file, err := fakeupstream.WeightedPicture(fakeupstream.PictureSpec{Format: "jpeg", Width: state.width, Height: state.height, Variant: state.variant}, state.weight)
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
