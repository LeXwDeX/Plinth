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
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
		Note: "JSON request with inline base64 pictures; 400 empty_string (missing prompt) and 401 text/plain are real samples. The picture is drawn at the requested size (an auto or omitted size is 1024x1024) in the requested output_format, and the response reports the drawn size, output_format and background (background=transparent answers an alpha PNG); the committed samples only ask for 1024x1024 png, so the size, the format and the transparent behaviors are constructed-from-docs. The documented parameter contract is enforced with constructed 400s that reuse the real envelope and OpenAI's error vocabulary: size (gpt-image-2: both edges divisible by 16, longest edge at most 3840, ratio at most 3:1, 655360 to 8294400 pixels), quality, output_format, background, moderation, n (1 to 10, integer_above_max_value / integer_below_min_value, never clamped), output_compression (0 to 100; it is documented for jpeg and webp only and is ignored with png or no output_format, since the documents do not say png refuses it), a transparent background with jpeg, and a prompt over 32000 characters; the real gateway's wording for these refusals is not recorded (free negative probes are planned). quality max is accepted: the paid real answer of 2026-11-25 (#608, openai-images-v1-quality-max-2026-11-25.candidate.json) is HTTP 200 for gpt-image-2.6-sunburst at 1024x1024 with quality max repeated and 7024 output image tokens (about 0.21 USD), and the image-generation guide read the same day lists xhigh and max for the 2.6 and 2.5 models; xhigh is accepted on that reading (not requested). gif and svg output_format are documented but not modelled (Plinth never sends them), and WebP answers the fixed 1x1 fixture whatever the size. n is documented by the official Create image reference (1-10) and returns that many distinct inline candidates. A content-policy refusal (400 moderation_blocked) and the billing and 500 faults are not observed: they reuse the real 400 envelope with the documented code or message and a null param; the billing answers are the error-code guide's 429 (credit_balance_exhausted for an empty balance, project_spend_limit_exceeded for the key-quota scenario, type insufficient_quota; the guide has no 402, checked 2026-11-25). The quota-exceeded 429 (the gateway's error.quota body), forbidden 403 (the gateway's eca_access_denied: a gateway form, the official guide has no such code) and service-unavailable 503 (documented service_unavailable_error/server_is_overloaded) are constructed-from-docs answers built from the recorded gateway refusals and the error-code guide (" + docsErrors + ", checked " + docsCheckedAt + ")"},
	{Method: "POST", Path: "/images/edits", Evidence: fakeupstream.EvidenceReal, Sample: "POST /v1/images/edits",
		DocURL: docsRoot + "createEdit", CheckedAt: captured,
		Note: "multipart request with the reference picture in the image part (Plinth sends image[], the committed sample says image because the sample key rule rejects brackets; the fake accepts both). model is optional on this route, per the official Create image edit reference (default gpt-image-2.6-sunburst, checked 2026-11-24); the same constructed-from-docs 403/429/503 faults and the same parameter contract as generations apply (the text fields are multipart strings, so n is parsed as an integer). An edit takes at most 16 pictures (array_above_max_length on image). A mask is a PNG with an alpha channel, the size of the first picture and, per the guide's \"The image to edit and mask must be of the same format and size\", its format: the formats are compared by the bytes, not the Content-Type, so a JPEG or WebP picture beside the PNG mask is refused (#582); otherwise 400 invalid_image on mask; every picture and the mask must be less than 50MB, otherwise 400 image_file_too_large (the image-generation guide's mask rules, checked 2026-11-25; the edit reference itself gives none, and the gateway's wording is not recorded). The reference also lists an application/json body (checked 2026-11-25): images[] of {image_url | file_id} (1 to 16) and mask {image_url | file_id} (exactly one); the fake reads it with the same contract (model default, n, size, enums, 16 pictures as array_above_max_length on images, 50MB, the mask rules), image_url is a base64 data URL or an http(s) address the fake can fetch, and as the fake keeps no Files API a file_id answers 400 invalid_value (No such File object). A request that is neither multipart nor JSON answers 400 Invalid Content-Type (code and param null), a JSON body that does not parse answers OpenAI's could-not-parse wording; the JSON-edit refusals are constructed, none is recorded. With a mask only the transparent pixels change and the answer keeps the picture's size, for every one of the n candidates and in the requested output_format (PNG keeps the other pixels exactly, JPEG is lossy). A missing prompt on this route answers the documented missing_required_parameter: the empty_string answer is recorded for generations only"},
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

// errorBody clones the real 400 envelope with another status text. param is a
// string, or nil for an answer about no one parameter: the official Error
// declares param nullable (OpenAPI components.schemas.Error), and no recorded
// answer of this gateway has an empty-string param.
func (h *handler) errorBody(endpoint, kind, code, message string, param any) map[string]any {
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

// render answers the scenarios the shared Env.Intercept owns. The two billing
// answers are the error-code guide's (checked 2026-11-25): it has no 402, and a
// used-up balance or spend limit is HTTP 429 whose error.code names the cause
// while error.type stays insufficient_quota. An empty balance is
// credit_balance_exhausted; the key-quota scenario (a limit on the caller's
// spending, not the account's balance) is project_spend_limit_exceeded, the
// nearest documented limit, as OpenAI has no per-key spend limit. The messages
// paraphrase the guide's causes; its wording of the body is not given.
func (h *handler) render(endpoint string) fakeupstream.ErrorRenderer {
	return func(w http.ResponseWriter, _ *http.Request, scenario fakeupstream.Scenario) {
		status, kind, code, message := http.StatusInternalServerError, "server_error", "internal_error", "The server had an error while processing your request."
		switch scenario {
		case fakeupstream.ScenarioInsufficientBalance:
			status, kind, code, message = http.StatusTooManyRequests, "insufficient_quota", "credit_balance_exhausted", "Your organization has no prepaid credits remaining."
		case fakeupstream.ScenarioKeyQuota:
			status, kind, code, message = http.StatusTooManyRequests, "insufficient_quota", "project_spend_limit_exceeded", "Your project reached its enforced spend limit."
		case fakeupstream.ScenarioRateLimit:
			status, kind, code, message = http.StatusTooManyRequests, "rate_limit_error", "rate_limit_exceeded", "rate limit exceeded"
			w.Header().Set("Retry-After", h.env.RetryAfterSeconds())
		}
		writeJSON(w, status, h.errorBody(endpoint, kind, code, message, nil))
	}
}

// request is what the fake reads out of a JSON or multipart request.
type request struct {
	prompt string
	model  string
	// form holds every text value as a string (the multipart fields, and the
	// JSON strings); raw holds the typed JSON values, so a number sent as a
	// string (or the other way round) is told apart by validate.
	form map[string]string
	raw  map[string]any
	// multipart is true for a multipart edit: its text fields are always
	// strings. jsonEdit is true for the application/json edit (#582), whose
	// pictures are images[] references; imageParam names the pictures'
	// parameter in a refusal (image for multipart, images for JSON).
	multipart  bool
	jsonEdit   bool
	imageParam string
	hasImg     bool
	// imageCount counts the reference pictures (image and image[] parts, or
	// the images[] entries of a JSON edit).
	imageCount int
	// refs is the first refusal of a JSON edit's picture references (a wrong
	// shape, a file_id, a URL that does not answer a picture); it is answered
	// after the parameter contract, as a multipart picture is only read then.
	refs *refusal
	// imageData and maskData keep the first reference picture and the mask of
	// an edit request (#244), so the fake can compose only the masked region
	// and leave every other pixel of the picture byte for byte equal.
	imageData []byte
	maskData  []byte
	// oversized names the first picture part (image or mask) of an edit whose
	// file is not under maxEditFileBytes; its bytes are not kept.
	oversized string
}

// maxEditFileBytes bounds each picture of an edit: the image-generation guide
// (checked 2026-11-25) says "The image to edit and mask must be of the same
// format and size (less than 50MB in size)".
const maxEditFileBytes = 50 << 20

// readEditFile reads one picture part. A part of maxEditFileBytes or more is
// drained, not kept, and reported as too large.
func readEditFile(part io.Reader) (data []byte, tooLarge bool) {
	data, _ = io.ReadAll(io.LimitReader(part, maxEditFileBytes))
	if len(data) < maxEditFileBytes {
		return data, false
	}
	_, _ = io.Copy(io.Discard, part)
	return nil, true
}

// readFailure says why a request body could not be read.
type readFailure int

const (
	readOK readFailure = iota
	// readBadContentType is an edit that is neither multipart nor JSON.
	readBadContentType
	// readBadJSON is a JSON body that does not parse as an object.
	readBadJSON
)

// maxJSONEditBytes bounds a JSON edit body: sixteen inline pictures and a mask
// may each be just under 50MB, and base64 grows them by a third. A larger
// body is cut, then does not parse.
const maxJSONEditBytes = (maxReferences+1)*(maxEditFileBytes/3*4) + (1 << 20)

func readJSONObject(r *http.Request, limit int64, out *request) bool {
	var body map[string]any
	if json.NewDecoder(io.LimitReader(r.Body, limit)).Decode(&body) != nil || body == nil {
		return false
	}
	for key, value := range body {
		out.raw[key] = value
		if typed, ok := value.(string); ok {
			out.form[key] = typed
		}
	}
	return true
}

func readRequest(r *http.Request, endpoint string) (request, readFailure) {
	out := request{form: map[string]string{}, raw: map[string]any{}, imageParam: "image"}
	if endpoint == "/images/generations" {
		if !readJSONObject(r, 8<<20, &out) {
			return out, readBadJSON
		}
	} else {
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err == nil && mediaType == "application/json" {
			// #582: the Create image edit reference (checked 2026-11-25) also
			// takes a JSON body: images[] of {image_url | file_id} (1 to 16)
			// and mask {image_url | file_id}. The same contract applies.
			out.jsonEdit, out.imageParam = true, "images"
			if !readJSONObject(r, maxJSONEditBytes, &out) {
				return out, readBadJSON
			}
			out.readJSONPictures()
			out.prompt, out.model = strings.TrimSpace(out.form["prompt"]), strings.TrimSpace(out.form["model"])
			return out, readOK
		}
		if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
			return out, readBadContentType
		}
		out.multipart = true
		// The body bound leaves room for a picture over the per-file limit, so
		// that refusal can be told apart from a truncated body.
		reader := multipart.NewReader(io.LimitReader(r.Body, 2*maxEditFileBytes+(64<<20)), params["boundary"])
		for {
			part, err := reader.NextPart()
			if err != nil {
				break
			}
			switch name := part.FormName(); {
			case name == "image" || name == "image[]" || name == "mask":
				data, tooLarge := readEditFile(part)
				if tooLarge && out.oversized == "" {
					out.oversized = strings.TrimSuffix(name, "[]")
				}
				switch {
				case name == "mask":
					out.maskData = data
				case len(data) > 0 || tooLarge:
					out.hasImg = true
					out.imageCount++
					if out.imageData == nil {
						out.imageData = data
					}
				}
			case part.FileName() == "":
				data, _ := io.ReadAll(part)
				out.form[name] = string(data)
				out.raw[name] = string(data)
			default:
				_, _ = io.Copy(io.Discard, part)
			}
		}
	}
	out.prompt, out.model = strings.TrimSpace(out.form["prompt"]), strings.TrimSpace(out.form["model"])
	return out, readOK
}

// readJSONPictures resolves the images[] and mask references of a JSON edit
// into the same bytes a multipart edit carries, so both forms share one
// contract. An absent images leaves hasImg false (a missing parameter); more
// than sixteen entries are only counted (validate refuses them, nothing is
// fetched). Every refusal is constructed in OpenAI's vocabulary: the reference
// gives the shapes, not the gateway's wording.
func (r *request) readJSONPictures() {
	raw, present := r.raw["images"]
	if !present || raw == nil {
		return
	}
	r.hasImg = true
	list, isList := raw.([]any)
	switch {
	case !isList:
		r.refs = &refusal{"invalid_type", fmt.Sprintf("Invalid type for 'images': expected an array, but got %s instead.", kindOf(raw)), "images"}
		return
	case len(list) == 0:
		r.refs = &refusal{"empty_array", "Invalid 'images': empty array. Expected an array with minimum length 1, but got an empty array instead.", "images"}
		return
	}
	r.imageCount = len(list)
	if len(list) > maxReferences {
		return
	}
	for index, item := range list {
		data, bad := resolveReference(fmt.Sprintf("images[%d]", index), item, false)
		if bad != nil {
			r.refs = bad
			return
		}
		if data == nil {
			// The picture is 50MB or more; the oversized answer names it.
			if r.oversized == "" {
				r.oversized = fmt.Sprintf("images[%d]", index)
			}
			continue
		}
		if r.imageData == nil {
			r.imageData = data
		}
	}
	if mask, present := r.raw["mask"]; present && mask != nil {
		data, bad := resolveReference("mask", mask, true)
		switch {
		case bad != nil:
			r.refs = bad
		case data == nil:
			if r.oversized == "" {
				r.oversized = "mask"
			}
		default:
			r.maskData = data
		}
	}
}

// fetchClient fetches an image_url that is an http(s) address. The fake reads
// only what its tests and the fake stack serve; the timeout keeps an address
// that never answers from holding the request.
var fetchClient = &http.Client{Timeout: 30 * time.Second}

// resolveReference reads one {image_url | file_id} object. data is nil (and
// bad nil) for a picture of 50MB or more. The mask must name exactly one of
// the two (the reference says so for mask; for an images[] entry it lists both
// as optional, so the fake only refuses an entry with neither). The fake keeps
// no Files API, so no file_id resolves: it answers what an unknown File
// object answers, constructed.
func resolveReference(param string, value any, exactlyOne bool) ([]byte, *refusal) {
	object, isObject := value.(map[string]any)
	if !isObject {
		return nil, &refusal{"invalid_type", fmt.Sprintf("Invalid type for '%s': expected an object, but got %s instead.", param, kindOf(value)), param}
	}
	imageURL, hasURL := object["image_url"]
	fileID, hasFile := object["file_id"]
	hasURL, hasFile = hasURL && imageURL != nil, hasFile && fileID != nil
	switch {
	case !hasURL && !hasFile:
		return nil, &refusal{"missing_required_parameter", fmt.Sprintf("Invalid '%s': provide exactly one of image_url or file_id.", param), param}
	case exactlyOne && hasURL && hasFile:
		return nil, &refusal{"invalid_value", fmt.Sprintf("Invalid '%s': provide exactly one of image_url or file_id.", param), param}
	case hasFile:
		id, isString := fileID.(string)
		if !isString {
			return nil, &refusal{"invalid_type", fmt.Sprintf("Invalid type for '%s.file_id': expected a string, but got %s instead.", param, kindOf(fileID)), param + ".file_id"}
		}
		return nil, &refusal{"invalid_value", fmt.Sprintf("Invalid '%s.file_id': No such File object: %s.", param, id), param + ".file_id"}
	}
	address, isString := imageURL.(string)
	if !isString {
		return nil, &refusal{"invalid_type", fmt.Sprintf("Invalid type for '%s.image_url': expected a string, but got %s instead.", param, kindOf(imageURL)), param + ".image_url"}
	}
	field := param + ".image_url"
	if strings.HasPrefix(address, "data:") {
		comma := strings.IndexByte(address, ',')
		if comma < 0 || !strings.HasSuffix(address[:comma], ";base64") {
			return nil, &refusal{"invalid_value", fmt.Sprintf("Invalid '%s': expected a base64-encoded data URL.", field), field}
		}
		decoded, err := base64.StdEncoding.DecodeString(address[comma+1:])
		if err != nil {
			return nil, &refusal{"invalid_value", fmt.Sprintf("Invalid '%s': expected a base64-encoded data URL.", field), field}
		}
		if len(decoded) >= maxEditFileBytes {
			return nil, nil
		}
		return decoded, nil
	}
	if !strings.HasPrefix(address, "http://") && !strings.HasPrefix(address, "https://") {
		return nil, &refusal{"invalid_value", fmt.Sprintf("Invalid '%s': expected a fully qualified URL or a base64-encoded data URL.", field), field}
	}
	response, err := fetchClient.Get(address)
	if err != nil {
		return nil, &refusal{"invalid_value", fmt.Sprintf("Invalid '%s': the image could not be downloaded.", field), field}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &refusal{"invalid_value", fmt.Sprintf("Invalid '%s': the image could not be downloaded (HTTP %d).", field, response.StatusCode), field}
	}
	data, tooLarge := readEditFile(response.Body)
	if tooLarge {
		return nil, nil
	}
	if len(data) == 0 {
		return nil, &refusal{"invalid_value", fmt.Sprintf("Invalid '%s': the image could not be downloaded.", field), field}
	}
	return data, nil
}

// refusal is a constructed official-shaped 400 for a parameter value the
// documented contract rejects. The real gateway's wording for these is not
// recorded (the free negative probes are planned, see the endpoint table), so
// the codes follow OpenAI's own vocabulary and every message is the fake's.
type refusal struct{ code, message, param string }

const (
	// The limits below are the official gpt-image-2 contract (Create image
	// reference, checked 2026-11-24): a custom size has both edges divisible by
	// 16, a longest edge of at most 3840, a long:short ratio of at most 3:1 and
	// a total of 655,360 to 8,294,400 pixels; n is 1 to 10; an edit takes up to
	// 16 pictures. The Catalog declares the same numbers; the schemaimages
	// contract test keeps the two in step.
	sizeMultiple    = 16
	sizeMaxEdge     = 3840
	sizeMaxRatio    = 3
	sizeMinPixels   = 655_360
	sizeMaxPixels   = 8_294_400
	maxCandidates   = 10
	maxReferences   = 16
	maxPromptLength = 32_000
	// autoSize is the picture an omitted or auto size answers.
	autoSize = "1024x1024"
)

// The enums are the official ones. quality also lists xhigh and max for every
// model: the gateway accepts max on gpt-image-2.6-sunburst (the paid real
// answer of 2026-11-25, #608: HTTP 200 for 1024x1024 quality max, the response
// repeats quality max, 7024 output image tokens; sample
// openai-images-v1-quality-max-2026-11-25), and the image-generation guide read
// the same day says the 2.6 and 2.5 Sunburst and Flare models support xhigh
// and max. xhigh on 2.6 is accepted on the same reading (not requested).
var (
	qualityValues      = []string{"auto", "low", "medium", "high", "xhigh", "max"}
	outputFormatValues = []string{"png", "jpeg", "webp"}
	backgroundValues   = []string{"auto", "opaque", "transparent"}
	moderationValues   = []string{"auto", "low"}
)

func quoted(values []string) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = "'" + value + "'"
	}
	return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func kindOf(value any) string {
	switch typed := value.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		if typed != float64(int64(typed)) {
			return "decimal number"
		}
		return "integer"
	case nil:
		return "null"
	default:
		return "object"
	}
}

// enumParam reads an enum parameter. ok is false when the parameter is absent.
func (r request) enumParam(name string, allowed []string) (value string, present bool, bad *refusal) {
	raw, exists := r.raw[name]
	if !exists || raw == nil {
		return "", false, nil
	}
	text, isString := raw.(string)
	if !isString {
		return "", true, &refusal{"invalid_type", fmt.Sprintf("Invalid type for '%s': expected a string, but got %s instead.", name, kindOf(raw)), name}
	}
	if !contains(allowed, text) {
		return "", true, &refusal{"invalid_value", fmt.Sprintf("Invalid value: '%s'. Supported values are: %s.", text, quoted(allowed)), name}
	}
	return text, true, nil
}

// intParam reads an integer parameter bounded to min..max. A JSON number must
// be integral; a multipart text value must parse as an integer.
func (r request) intParam(name string, minimum, maximum int) (value int, present bool, bad *refusal) {
	raw, exists := r.raw[name]
	if !exists || raw == nil {
		return 0, false, nil
	}
	switch typed := raw.(type) {
	case float64:
		if typed != float64(int64(typed)) {
			return 0, true, &refusal{"invalid_type", fmt.Sprintf("Invalid type for '%s': expected an integer, but got a decimal number instead.", name), name}
		}
		value = int(typed)
	case string:
		if !r.multipart {
			return 0, true, &refusal{"invalid_type", fmt.Sprintf("Invalid type for '%s': expected an integer, but got a string instead.", name), name}
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0, true, &refusal{"invalid_type", fmt.Sprintf("Invalid type for '%s': expected an integer, but got a string instead.", name), name}
		}
		value = parsed
	default:
		return 0, true, &refusal{"invalid_type", fmt.Sprintf("Invalid type for '%s': expected an integer, but got %s instead.", name, kindOf(raw)), name}
	}
	if value > maximum {
		return 0, true, &refusal{"integer_above_max_value", fmt.Sprintf("Invalid '%s': integer above maximum value. Expected a value <= %d, but got %d instead.", name, maximum, value), name}
	}
	if value < minimum {
		return 0, true, &refusal{"integer_below_min_value", fmt.Sprintf("Invalid '%s': integer below minimum value. Expected a value >= %d, but got %d instead.", name, minimum, value), name}
	}
	return value, true, nil
}

// sizeRefusal checks a custom WIDTHxHEIGHT against the gpt-image-2 rules.
func sizeRefusal(value string) *refusal {
	width, height, ok := fakeupstream.ParseSize(value)
	if !ok {
		return &refusal{"invalid_value", fmt.Sprintf("Invalid value: '%s'. Expected 'auto' or a size in the form WIDTHxHEIGHT.", value), "size"}
	}
	switch {
	case width%sizeMultiple != 0 || height%sizeMultiple != 0:
		return &refusal{"invalid_value", fmt.Sprintf("Invalid size '%s': width and height must both be divisible by %d.", value, sizeMultiple), "size"}
	case width > sizeMaxEdge || height > sizeMaxEdge:
		return &refusal{"invalid_value", fmt.Sprintf("Invalid size '%s': the maximum edge is %d pixels.", value, sizeMaxEdge), "size"}
	case width > sizeMaxRatio*height || height > sizeMaxRatio*width:
		return &refusal{"invalid_value", fmt.Sprintf("Invalid size '%s': the aspect ratio must be between 1:%d and %d:1.", value, sizeMaxRatio, sizeMaxRatio), "size"}
	case width*height < sizeMinPixels || width*height > sizeMaxPixels:
		return &refusal{"invalid_value", fmt.Sprintf("Invalid size '%s': the total pixels must be between %d and %d.", value, sizeMinPixels, sizeMaxPixels), "size"}
	}
	return nil
}

// validate applies the documented parameter contract. It returns the first
// violated rule, in the order the official reference lists the parameters.
func (r request) validate() *refusal {
	size, present, bad := r.freeText("size")
	if bad != nil {
		return bad
	}
	// The custom-size rules are gpt-image-2's; an edit may omit the model, and
	// then the default model (the same family) applies.
	if present && size != "auto" && (r.model == "" || strings.HasPrefix(r.model, "gpt-image-2")) {
		if bad := sizeRefusal(size); bad != nil {
			return bad
		}
	}
	if _, _, bad := r.enumParam("quality", qualityValues); bad != nil {
		return bad
	}
	if _, _, bad := r.intParam("n", 1, maxCandidates); bad != nil {
		return bad
	}
	format, _, bad := r.enumParam("output_format", outputFormatValues)
	if bad != nil {
		return bad
	}
	// output_compression is "for jpeg or webp output" (Create image reference,
	// checked 2026-11-25); the documents do not say that png refuses it, and
	// the fake ignores it there (no recorded answer either way). The 0-100
	// range is checked for every format.
	if _, _, bad := r.intParam("output_compression", 0, 100); bad != nil {
		return bad
	}
	background, _, bad := r.enumParam("background", backgroundValues)
	if bad != nil {
		return bad
	}
	if background == "transparent" && format == "jpeg" {
		return &refusal{"invalid_value", "Invalid 'background': a transparent background requires the png or webp output format.", "background"}
	}
	if _, _, bad := r.enumParam("moderation", moderationValues); bad != nil {
		return bad
	}
	if (r.multipart || r.jsonEdit) && r.imageCount > maxReferences {
		return &refusal{"array_above_max_length", fmt.Sprintf("Invalid '%s': array too long. Expected an array with maximum length %d, but got an array with length %d instead.", r.imageParam, maxReferences, r.imageCount), r.imageParam}
	}
	if utf8.RuneCountInString(r.prompt) > maxPromptLength {
		return &refusal{"string_above_max_length", fmt.Sprintf("Invalid 'prompt': string too long. Expected a string with maximum length %d, but got a string with length %d instead.", maxPromptLength, utf8.RuneCountInString(r.prompt)), "prompt"}
	}
	return nil
}

// freeText reads a free-text parameter (size) that is not an enum.
func (r request) freeText(name string) (value string, present bool, bad *refusal) {
	raw, exists := r.raw[name]
	if !exists || raw == nil {
		return "", false, nil
	}
	text, isString := raw.(string)
	if !isString {
		return "", true, &refusal{"invalid_type", fmt.Sprintf("Invalid type for '%s': expected a string, but got %s instead.", name, kindOf(raw)), name}
	}
	return strings.TrimSpace(text), true, nil
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
	req, failure := readRequest(r, endpoint)
	switch {
	case failure == readBadContentType:
		// An edit that is neither a multipart form nor JSON, the two bodies the
		// Create image edit reference lists (checked 2026-11-25). The wording is
		// constructed in OpenAI's style and code and param are null (no
		// parameter is at fault); no answer is recorded.
		body := h.errorBody(endpoint, "invalid_request_error", "",
			fmt.Sprintf("Invalid Content-Type header (%s), expected multipart/form-data or application/json.", contentTypeOf(r)), nil)
		body["error"].(map[string]any)["code"] = nil
		writeJSON(w, http.StatusBadRequest, body)
		return
	case failure == readBadJSON && req.jsonEdit:
		// A JSON edit whose body is no JSON object. OpenAI's wording for an
		// unparsable body, constructed (not recorded on this gateway).
		body := h.errorBody(endpoint, "invalid_request_error", "", "We could not parse the JSON body of your request.", nil)
		body["error"].(map[string]any)["code"] = nil
		writeJSON(w, http.StatusBadRequest, body)
		return
	case failure != readOK || (endpoint == "/images/generations" && req.model == ""):
		// The official Create image edit reference makes model optional there.
		writeJSON(w, http.StatusBadRequest, h.errorBody(endpoint, "invalid_request_error", "missing_required_parameter", "Missing required parameter: 'model'.", "model"))
		return
	case req.jsonEdit && !req.hasImg:
		// images is required in the JSON body; the recorded missing-image
		// answer is the multipart one, so this one is constructed.
		writeJSON(w, http.StatusBadRequest, h.errorBody(endpoint, "invalid_request_error", "missing_required_parameter", "Missing required parameter: 'images'.", "images"))
		return
	case endpoint == "/images/edits" && !req.hasImg:
		writeJSON(w, http.StatusBadRequest, h.invalid[endpoint])
		return
	case req.prompt == "":
		// The real gateway answers a request without a prompt with empty_string
		// (recorded sample, 2026-11-09): the field is read as an empty string, not
		// as a missing parameter. The edit route's answer is not recorded, so it
		// keeps OpenAI's documented missing_required_parameter.
		if endpoint == "/images/generations" {
			writeJSON(w, http.StatusBadRequest, h.invalid[endpoint])
		} else {
			writeJSON(w, http.StatusBadRequest, h.errorBody(endpoint, "invalid_request_error", "missing_required_parameter", "Missing required parameter: 'prompt'.", "prompt"))
		}
		return
	}
	if bad := req.validate(); bad != nil {
		writeJSON(w, http.StatusBadRequest, h.errorBody(endpoint, "invalid_request_error", bad.code, bad.message, bad.param))
		return
	}
	if bad := req.refs; bad != nil {
		writeJSON(w, http.StatusBadRequest, h.errorBody(endpoint, "invalid_request_error", bad.code, bad.message, bad.param))
		return
	}
	if req.oversized != "" {
		// The guide's 50MB limit; the code and wording are constructed (OpenAI's
		// own name for a picture file over a limit, from the Responses
		// ResponseError enum), no answer is recorded.
		writeJSON(w, http.StatusBadRequest, h.errorBody(endpoint, "invalid_request_error", "image_file_too_large",
			fmt.Sprintf("Invalid '%s': the file must be less than 50MB.", req.oversized), req.oversized))
		return
	}
	var editor *maskedEditor
	if endpoint == "/images/edits" && len(req.maskData) > 0 {
		// #244: an edit with a mask changes only the mask's transparent pixels
		// and copies every other pixel of the reference picture byte for byte.
		built, bad := newMaskedEditor(req.imageData, req.maskData)
		if bad != nil {
			writeJSON(w, http.StatusBadRequest, h.errorBody(endpoint, "invalid_request_error", bad.code, bad.message, bad.param))
			return
		}
		editor = built
	}
	scenario := fakeupstream.ScenarioOf(r)
	switch scenario {
	case fakeupstream.ScenarioLostResponse:
		fakeupstream.AbortConnection(w) // the picture was made; the response is lost
		return
	case fakeupstream.ScenarioTaskFailed:
		writeJSON(w, http.StatusBadRequest, h.errorBody(endpoint, "image_generation_user_error", "moderation_blocked",
			"Your request was rejected by the safety system. If you believe this is an error, contact us.", nil))
		return
	}
	format := req.form["output_format"]
	if format == "" {
		format = "png"
	}
	transparent := req.form["background"] == "transparent"
	width, height := outputSize(req.form["size"])
	if editor != nil {
		// A masked edit keeps the reference picture's pixels, so it keeps its size.
		width, height = editor.width, editor.height
	}
	body := fakeupstream.Clone(h.ok[endpoint])
	if list, ok := body["data"].([]any); ok && len(list) > 0 {
		if template, ok := list[0].(map[string]any); ok {
			// n is documented by the official Create image reference (1-10) and
			// validate has already refused anything else. The committed samples
			// only ask for one picture, so returning n distinct pictures is
			// constructed-from-docs.
			count := 1
			if n, present, _ := req.intParam("n", 1, maxCandidates); present {
				count = n
			}
			// The capacity is a constant so the allocation never depends on the
			// request's n; count only decides how many entries are appended.
			items := make([]any, 0, maxCandidates)
			for index := 0; index < count; index++ {
				item := fakeupstream.Clone(template)
				candidate, err := h.candidate(editor, format, width, height, transparent, index)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
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
	// The answer reports what was made, not what was asked: an auto size is the
	// size actually drawn, an auto background is opaque (the recorded sample).
	body["size"] = fmt.Sprintf("%dx%d", width, height)
	body["output_format"] = format
	if transparent {
		body["background"] = "transparent"
	} else {
		body["background"] = "opaque"
	}
	if quality := req.form["quality"]; quality != "" && quality != "auto" {
		body["quality"] = quality
	}
	writeJSON(w, http.StatusOK, body)
}

// contentTypeOf is the request's media type for an error message, without
// its parameters.
func contentTypeOf(r *http.Request) string {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType == "" {
		return "none"
	}
	return mediaType
}

// outputSize is the size of the picture drawn for a request's size value:
// the requested WxH, or 1024x1024 for auto or an omitted size.
func outputSize(value string) (width, height int) {
	if width, height, ok := fakeupstream.ParseSize(value); ok {
		return width, height
	}
	width, height, _ = fakeupstream.ParseSize(autoSize)
	return width, height
}

// candidate draws picture number index: the masked composition of the
// reference when an edit carries a mask, else the fake subject picture at the
// requested size and format (an alpha PNG for background=transparent). Every
// index shades the picture differently, so n>1 answers distinct bytes.
func (h *handler) candidate(editor *maskedEditor, format string, width, height int, transparent bool, index int) ([]byte, error) {
	if editor != nil {
		return editor.encode(format, index)
	}
	file, err := fakeupstream.Picture(fakeupstream.PictureSpec{
		Format: format, Width: width, Height: height, Transparent: transparent && format == "png", Variant: index,
	})
	if err != nil {
		return nil, err
	}
	return file.Data, nil
}

// maskedEditor composes the edit of a reference picture and its mask.
type maskedEditor struct {
	source        image.Image
	mask          image.Image
	width, height int
}

// sniffFormat names a picture's format from its leading bytes (never from a
// Content-Type header or a file name): png, jpeg, webp or gif, else "".
func sniffFormat(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return "jpeg"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "webp"
	case bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a")):
		return "gif"
	}
	return ""
}

// newMaskedEditor decodes the first reference picture and the mask and applies
// the image-generation guide's mask rules (checked 2026-11-25): "The image to
// edit and mask must be of the same format and size", and the mask has an
// alpha channel; the fake also keeps the mask a PNG. The formats are compared
// by the bytes (#582), so a JPEG or WebP picture beside the PNG mask is
// refused on mask; the wording is constructed. A reference the fake cannot
// decode is refused too, as the real service would; the edit without a mask
// never decodes it.
func newMaskedEditor(reference, mask []byte) (*maskedEditor, *refusal) {
	config, format, err := image.DecodeConfig(bytes.NewReader(mask))
	if err != nil {
		return nil, &refusal{"invalid_image", "The reference image or the mask could not be decoded.", "mask"}
	}
	if format != "png" {
		return nil, &refusal{"invalid_image", "Invalid mask image format: the mask must be a PNG file with an alpha channel.", "mask"}
	}
	switch config.ColorModel {
	case color.RGBAModel, color.GrayModel, color.Gray16Model, color.RGBA64Model:
		return nil, &refusal{"invalid_image", "Invalid mask image format: the mask must contain an alpha channel.", "mask"}
	}
	referenceFormat := sniffFormat(reference)
	if referenceFormat == "" {
		return nil, &refusal{"invalid_image", "The reference image or the mask could not be decoded.", "image"}
	}
	if referenceFormat != format {
		return nil, &refusal{"invalid_image", fmt.Sprintf("Invalid mask image: the mask format %s does not match the image format %s. The image to edit and the mask must be of the same format.", format, referenceFormat), "mask"}
	}
	source, _, err := image.Decode(bytes.NewReader(reference))
	if err != nil {
		return nil, &refusal{"invalid_image", "The reference image or the mask could not be decoded.", "image"}
	}
	decodedMask, _, err := image.Decode(bytes.NewReader(mask))
	if err != nil {
		return nil, &refusal{"invalid_image", "The reference image or the mask could not be decoded.", "mask"}
	}
	bounds := source.Bounds()
	if config.Width != bounds.Dx() || config.Height != bounds.Dy() {
		return nil, &refusal{"invalid_image", fmt.Sprintf("Invalid mask image: the mask size %dx%d does not match the image size %dx%d.", config.Width, config.Height, bounds.Dx(), bounds.Dy()), "mask"}
	}
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return nil, &refusal{"invalid_image", "The reference image has no pixels.", "image"}
	}
	return &maskedEditor{source: source, mask: decodedMask, width: bounds.Dx(), height: bounds.Dy()}, nil
}

// compose copies the reference picture and repaints only the pixels whose mask
// alpha is below 128 (the editable region, #244). The repaint is a
// deterministic diagonal gradient (shifted per variant, so n>1 candidates
// differ), so a test can compare every pixel outside the mask with the source
// and see that the inside changed. The output keeps the reference picture's
// dimensions.
func (e *maskedEditor) compose(variant int) *image.RGBA {
	bounds := e.source.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, e.width, e.height))
	draw.Draw(out, out.Bounds(), e.source, bounds.Min, draw.Src)
	maskBounds := e.mask.Bounds()
	for y := 0; y < e.height; y++ {
		maskY := maskBounds.Min.Y + y*maskBounds.Dy()/e.height
		for x := 0; x < e.width; x++ {
			maskX := maskBounds.Min.X + x*maskBounds.Dx()/e.width
			_, _, _, alpha := e.mask.At(maskX, maskY).RGBA()
			if alpha>>8 >= 128 {
				continue
			}
			out.SetRGBA(x, y, color.RGBA{
				R: uint8((x*7 + variant*41) % 256),
				G: uint8((y*11 + variant*23) % 256),
				B: uint8((x + y + variant*59) % 256),
				A: 255,
			})
		}
	}
	return out
}

// encode writes the composition in the requested output format. PNG keeps
// every pixel outside the mask exactly; JPEG is lossy like the real one;
// WebP answers the fixed fixture (see fakeupstream.PictureSpec).
func (e *maskedEditor) encode(format string, variant int) ([]byte, error) {
	composed := e.compose(variant)
	var encoded bytes.Buffer
	switch format {
	case "jpeg":
		if err := jpeg.Encode(&encoded, composed, &jpeg.Options{Quality: 90}); err != nil {
			return nil, err
		}
	case "webp":
		file, err := fakeupstream.ImageFile("webp")
		if err != nil {
			return nil, err
		}
		return file.Data, nil
	default:
		if err := png.Encode(&encoded, composed); err != nil {
			return nil, err
		}
	}
	return encoded.Bytes(), nil
}
