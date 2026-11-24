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
//   - a request with pictures that the model or endpoint takes no picture
//     input for (#253): a model without image input;
//   - a request with pictures the model read and refused as pictures (#497):
//     a 400/422 that objects to the pictures' dimensions, size, format or
//     number, which is a bad picture rather than a model without image input;
//     neither picture class reads a refusal that names a field outside the
//     conversation or objects to a tool declaration (a tool named refine_image);
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
	param := rejectedParameter(refusal.Status, refusal.Body)
	if refusal.HasImages && imageRefusalStatus(refusal.Status) && mentionsImageInput(refusal.Body) &&
		pictureField(param) && !mentionsToolDeclaration(refusal.Body) {
		if imageInputUnsupported(refusal.Status, refusal.Body) {
			return errors.Join(agents.ErrImagesRejected, err)
		}
		if reasons := invalidPictureReasons(refusal.Status, refusal.Body); len(reasons) > 0 {
			return errors.Join(&agents.ImageInvalidity{Reasons: reasons}, err)
		}
	}
	if param != "" {
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

// mentionsImageInput is whether the error body talks about picture input at
// all. It is only the gate of the two classes below: what decides between
// them is a wording of the vendor's own.
func mentionsImageInput(body []byte) bool {
	text := strings.ToLower(string(body))
	for _, word := range []string{"image", "vision", "multimodal", "multi-modal"} {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

// pictureField is whether a refusal's named request field can hold a
// picture: none named, or one under the conversation (messages for Chat
// Completions, input for Responses). A refusal that names any other field — a
// tool's parameters, tool_choice, a sampling setting — is about that field
// even when its words say "image", because the Agent's tool names do
// (refine_image, create_model_3d_from_image).
func pictureField(param string) bool {
	if param == "" {
		return true
	}
	root, _, _ := strings.Cut(strings.SplitN(param, "[", 2)[0], ".")
	return root == "messages" || root == "input"
}

// toolDeclarationPhrases are the wordings of a refusal of the request's tool
// declarations. Such an error names the tool (refine_image) and the schema
// keywords it objects to (width, format, maximum), which is the picture
// vocabulary below; it is still no picture refusal.
var toolDeclarationPhrases = []string{
	"schema", "for function", "tools[", "tool_choice", "function.parameters",
}

func mentionsToolDeclaration(body []byte) bool {
	text := strings.ToLower(string(body))
	for _, phrase := range toolDeclarationPhrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

// imageInputUnsupportedPhrases are the wordings a vendor uses when its model
// or endpoint takes no picture input at all (#253): the error has to say so in
// as many words. Deliberately narrow, and deliberately not "the body mentions
// an image" — a body that objects to the picture itself (#497) mentions one
// too, and reading that as a model without image input sends the person to
// change a model that reads images fine.
var imageInputUnsupportedPhrases = []string{
	"image input is not supported",
	"image input isn't supported",
	"image input is unsupported",
	"image input not supported",
	"image input unsupported",
	"image input is not allowed",
	"image inputs are not supported",
	"image inputs are not allowed",
	"image inputs not supported",
	"image inputs unsupported",
	"does not support image input",
	"do not support image input",
	"does not support image inputs",
	"doesn't support image input",
	"don't support image input",
	"not support image input",
	"no support for image input",
	"no image input support",
	"without image input support",
	"does not support vision",
	"do not support vision",
	"doesn't support vision",
	"vision input is not supported",
	"vision input not supported",
	"vision is not supported",
	"vision not supported",
	"does not support multimodal",
	"does not support multi-modal",
	"doesn't support multimodal",
	"multimodal input is not supported",
	"multimodal input not supported",
	"multimodal content is not allowed",
	"multimodal content not allowed",
	"unknown content type image",
	"unsupported content type image",
	"invalid content type image",
	// The same wordings from a vendor that answers in Chinese.
	"不支持图片输入",
	"不支持图像输入",
	"图片输入不支持",
	"图片输入不受支持",
	"不支持读图",
	"不支持视觉输入",
	"不支持多模态输入",
}

// imageInputUnsupported is whether the refusal says the model or endpoint
// takes no picture input: HTTP 415 is the media type of the request being
// refused, and any other status needs one of the wordings above.
func imageInputUnsupported(status int, body []byte) bool {
	if status == http.StatusUnsupportedMediaType {
		return true
	}
	text := strings.ToLower(string(body))
	for _, phrase := range imageInputUnsupportedPhrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

// invalidPictureReasonVocabulary is what an upstream may object to about a
// picture it read and refused (#497): the token is the Product Schema's label
// key, the phrases are the vendor wordings that name it (English, plus the
// Chinese of a vendor that answers in Chinese). The upstream's own words never
// travel on; only the token does.
var invalidPictureReasonVocabulary = []struct {
	Token   string
	Phrases []string
}{
	{"dimensions", []string{
		"height", "width", "resolution", "dimension", "aspect ratio",
		"length and width", "尺寸", "分辨率", "宽高", "长宽",
	}},
	{"size", []string{
		"image size", "file size", "size limit", "maximum size", "max size",
		"too large", "too big", "too small", "size exceeds", "exceeds the maximum",
		"megapixel", "too many pixels", "pixel limit",
		"文件大小", "体积", "过大", "过小",
	}},
	{"format", []string{
		"format", "codec", "mime", "file type", "not a valid image",
		"格式", "编码",
	}},
	{"count", []string{
		"too many images", "number of images", "image count", "too many pictures",
		"maximum number of images", "数量", "张数",
	}},
}

// invalidPictureReasons are the reason tokens of a refusal that objects to the
// pictures themselves; nil for any other refusal. It reads a 400 or 422 only:
// other statuses are the classes above or a plain failure.
func invalidPictureReasons(status int, body []byte) []string {
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
		return nil
	}
	text := strings.ToLower(string(body))
	var reasons []string
	for _, reason := range invalidPictureReasonVocabulary {
		for _, phrase := range reason.Phrases {
			if strings.Contains(text, phrase) {
				reasons = append(reasons, reason.Token)
				break
			}
		}
	}
	return reasons
}
