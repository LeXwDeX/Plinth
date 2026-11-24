package upstreamhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/moderation"
)

// Refusal is a request the upstream refused with a non-2xx status.
type Refusal struct {
	Status int
	// Body is what the upstream said, already cut to the Adapter's error body
	// bound.
	Body []byte
	// HasImages is whether the request carried pictures: a refusal is read as
	// "no image input" only then.
	HasImages bool
	// ExhaustedQuota, when set, reports whether the refusal is a used-up
	// account balance or key quota and names its failure class. The
	// application derives it from the chat connection's Schema error_mapping
	// (#120); nil: no refusal is read as one.
	ExhaustedQuota func(status int, body []byte) (class string, exhausted bool)
	// RetryAfter is the wait the response's Retry-After header asked for
	// (RetryAfter); a transient refusal carries it (#509).
	RetryAfter time.Duration
}

// Err is the error an Adapter returns for the refusal. The status travels as
// agents.StatusError (#322), so a caller can classify the refusal without
// reading the body, and the upstream's wording stays in the text for the log.
// A refusal that one of the classes below owns carries that class's mark too,
// checked in this order:
//   - content moderation (#35): a vendor content-safety refusal names its own
//     error code and comes first, because its message may mention image data;
//   - a used-up balance or key quota (#120), as the Schema declares it;
//   - a request with pictures refused as invalid input whose error names
//     image, vision or multimodal content (#253): a model without image input;
//   - a request field the upstream refuses (#401): an invalid_request_error
//     that names the field it refuses, with the field's name;
//   - a status that may not meet the same request again (TransientStatus:
//     408, 429, 500, 502, 503, 504), with its Retry-After (#509).
//
// Any other refusal (context too long, an unknown model) stays the plain
// status error.
func (refusal Refusal) Err() error {
	err := fmt.Errorf(
		"%w: %s",
		&agents.StatusError{Status: refusal.Status},
		strings.TrimSpace(string(refusal.Body)),
	)
	if code := ModerationCode(refusal.Body); refusal.Status >= 400 && refusal.Status < 500 && code != "" {
		reasons := moderation.Reasons(string(refusal.Body))
		if len(reasons) == 0 {
			reasons = moderation.Tokens(code)
		}
		return errors.Join(&agents.ContentRejection{Reasons: reasons}, err)
	}
	if refusal.ExhaustedQuota != nil {
		if class, exhausted := refusal.ExhaustedQuota(refusal.Status, JSON(refusal.Body)); exhausted {
			return errors.Join(&agents.QuotaExhaustion{Class: class}, err)
		}
	}
	if imageRefusalStatus(refusal.Status) && refusal.HasImages && mentionsImageInput(refusal.Body) {
		return errors.Join(agents.ErrImagesRejected, err)
	}
	if param := rejectedParameter(refusal.Status, refusal.Body); param != "" {
		return errors.Join(&agents.RequestRejection{Param: param}, err)
	}
	if TransientStatus(refusal.Status) {
		return agents.Transient(err, refusal.RetryAfter)
	}
	return err
}

// RequestHasImages reports whether any message of the request carries a picture.
func RequestHasImages(request agents.Request) bool {
	for _, message := range request.Messages {
		for _, part := range message.Parts {
			if part.URL != "" {
				return true
			}
		}
	}
	return false
}

// safeParameterName bounds what an upstream error may put in front of a
// person: a request field path such as reasoning_effort, reasoning.effort or
// messages[2].content, never free text.
var safeParameterName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.\[\]-]{0,63}$`)

// rejectedParameter is the request field an HTTP 400/422 invalid_request_error
// names in its param member; "" for any other refusal, an unnamed field or a
// name that is not a plain field path. It reads a plain JSON body or one SSE
// "data:" line.
func rejectedParameter(status int, body []byte) string {
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
		return ""
	}
	var payload struct {
		Error *struct {
			Type  string `json:"type"`
			Param any    `json:"param"`
		} `json:"error"`
	}
	if json.Unmarshal(JSON(body), &payload) != nil || payload.Error == nil ||
		payload.Error.Type != "invalid_request_error" {
		return ""
	}
	param, _ := payload.Error.Param.(string)
	if !safeParameterName.MatchString(param) {
		return ""
	}
	return param
}

// moderationCodes are the error codes vendors give a content-safety
// refusal: DashScope's compatible (data_inspection_failed) and native
// (DataInspectionFailed) endpoints, Azure OpenAI (content_filter) and OpenAI
// (content_policy_violation).
var moderationCodes = map[string]bool{
	"data_inspection_failed":   true,
	"datainspectionfailed":     true,
	"content_filter":           true,
	"content_policy_violation": true,
}

// ModerationCode is the content-safety code of an error body, either a plain
// JSON object or one SSE "data:" line as a streamed refusal sends it; "" for
// any other refusal.
func ModerationCode(body []byte) string {
	text := JSON(body)
	var payload struct {
		Code  any `json:"code"`
		Error *struct {
			Code any    `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(text, &payload) != nil {
		return ""
	}
	codes := []any{payload.Code}
	if payload.Error != nil {
		codes = append(codes, payload.Error.Code, payload.Error.Type)
	}
	for _, code := range codes {
		if name, ok := code.(string); ok && moderationCodes[strings.ToLower(name)] {
			return name
		}
	}
	return ""
}

// JSON is the JSON of an error body, either a plain JSON object or one SSE
// "data:" line as a streamed refusal sends it.
func JSON(body []byte) []byte {
	text := strings.TrimSpace(string(body))
	return []byte(strings.TrimSpace(strings.TrimPrefix(text, "data:")))
}

func imageRefusalStatus(status int) bool {
	switch status {
	case http.StatusBadRequest, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
		return true
	}
	return false
}

func mentionsImageInput(body []byte) bool {
	text := strings.ToLower(string(body))
	for _, word := range []string{"image", "vision", "multimodal", "multi-modal"} {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}
