package openaiv1

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"  // registers the decoder DecodeConfig reads
	_ "image/jpeg" // registers the decoder DecodeConfig reads
	_ "image/png"  // registers the decoder DecodeConfig reads
	"net/http"
	"regexp"
	"strings"
)

// Refusals of a request's pictures (#536, I27), on both wires: Chat
// Completions (image_url parts) and Responses (input_image parts, read through
// responsesRequest.chatView).
//
// Evidence, kept apart:
//   - observed (the dev-test gateway, 2026-11-23, the body the #497 tests hold
//     verbatim): a qwen model answers a picture whose height or width is 32
//     pixels or less with HTTP 400, code and type invalid_parameter_error,
//     param null, and "The image length and width do not meet the model
//     restrictions. [height:1 or width:1 must be larger than 32]";
//   - observed (the same tests, #253): a model that takes no picture input
//     answers HTTP 400 invalid_request_error "this model does not support image
//     input" (the gateway envelope: code, message, type, no param);
//   - constructed, no sample: a picture over a file-size limit, in a format the
//     model cannot read, and too many pictures in one request. They use the
//     observed envelope and wordings in the vendor's style, and exist so the
//     Adapter's classification (upstreamhttp.Refusal) has a source on the fake.
//     Which of these the dev-test gateway really answers is for the real round
//     to settle (Y in the live-matrix).
//
// The natural refusals are the model's rules (constraints.go: noImageInput,
// minImageSide). ImageRefusalMarkerPrefix forces one regardless of the model,
// for the constructed kinds that no request reaches by itself.

// ImageRefusalMarkerPrefix, [[fake:image-refusal:KIND]] in the last user
// message of a request that carries a picture, answers HTTP 400 with the
// refusal of KIND: dimensions, size, format, count or unsupported (a model
// without image input).
const ImageRefusalMarkerPrefix = "[[fake:image-refusal:"

var imageRefusalMarker = regexp.MustCompile(`\[\[fake:image-refusal:(dimensions|size|format|count|unsupported)\]\]`)

// pictureURLs are the URLs (data URIs or links) of every picture of the
// conversation, in order.
func (r chatRequest) pictureURLs() []string {
	var urls []string
	for _, message := range r.Messages {
		var parts []struct {
			Type     string          `json:"type"`
			ImageURL json.RawMessage `json:"image_url"`
		}
		if json.Unmarshal(message.Content, &parts) != nil {
			continue
		}
		for _, part := range parts {
			if part.Type != "image_url" && part.Type != "input_image" {
				continue
			}
			var object struct {
				URL string `json:"url"`
			}
			var link string
			switch {
			case json.Unmarshal(part.ImageURL, &object) == nil:
				urls = append(urls, object.URL)
			case json.Unmarshal(part.ImageURL, &link) == nil:
				urls = append(urls, link)
			default:
				urls = append(urls, "")
			}
		}
	}
	return urls
}

// pictureSize is the pixel size of a base64 data-URI picture the standard
// library can read (PNG, JPEG, GIF); ok is false for anything else, a link
// included.
func pictureSize(url string) (width, height int, ok bool) {
	header, data, found := strings.Cut(url, ",")
	if !found || !strings.HasPrefix(strings.ToLower(header), "data:image/") || !strings.HasSuffix(strings.ToLower(header), ";base64") {
		return 0, 0, false
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return 0, 0, false
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return 0, 0, false
	}
	return config.Width, config.Height, true
}

// imageRefusal is the refusal the request's pictures earn, if any: its kind
// and the vendor's wording.
func (r chatRequest) imageRefusal() (kind, message string, refused bool) {
	urls := r.pictureURLs()
	if len(urls) == 0 {
		return "", "", false
	}
	rule, constrained := constraintFor(r.Model)
	forced := imageRefusalMarker.FindStringSubmatch(r.lastText())
	switch {
	case forced != nil:
		kind = forced[1]
	case constrained && rule.noImageInput:
		kind = "unsupported"
	case constrained && rule.minImageSide > 0:
		for _, url := range urls {
			if width, height, ok := pictureSize(url); ok && (width <= rule.minImageSide || height <= rule.minImageSide) {
				return "dimensions", dimensionsMessage(height, width, rule.minImageSide), true
			}
		}
		return "", "", false
	default:
		return "", "", false
	}
	switch kind {
	case "dimensions":
		side := 32
		if rule.minImageSide > 0 {
			side = rule.minImageSide
		}
		height, width := 1, 1
		for _, url := range urls {
			if w, h, ok := pictureSize(url); ok {
				height, width = h, w
				break
			}
		}
		message = dimensionsMessage(height, width, side)
	case "size":
		message = "The image file size exceeds the model restrictions. [file size must be smaller than 10485760 bytes]"
	case "format":
		message = "The image format is not supported by the model. [supported formats: jpeg, png, webp, bmp, gif]"
	case "count":
		message = "Too many images in the request. [the number of images must not exceed 30]"
	default:
		message = "this model does not support image input"
	}
	return kind, message, true
}

func dimensionsMessage(height, width, side int) string {
	return fmt.Sprintf("The image length and width do not meet the model restrictions. "+
		"[height:%d or width:%d must be larger than %d]", height, width, side)
}

// chatImageRefusalBody is the Chat Completions body of a picture refusal: the
// gateway's own envelope (code, param, message, type) for a refused picture,
// and the shorter one (code, message, type) of a model without image input.
func chatImageRefusalBody(kind, message string) any {
	if kind == "unsupported" {
		return map[string]any{"error": orderedError{{"code", "invalid_request_error"}, {"message", message}, {"type", "invalid_request_error"}}}
	}
	return map[string]any{"error": orderedError{{"code", "invalid_parameter_error"}, {"param", nil}, {"message", message}, {"type", "invalid_parameter_error"}}}
}

// responsesImageRefusalBody is the Responses body of a picture refusal in the
// envelope the gateway answers /v1/responses 400s with (message, type, param,
// code, observed 2026-11-20); the wordings are the Chat ones. Constructed: the
// probes of that day had no refused picture.
func responsesImageRefusalBody(message string) any {
	return map[string]any{"error": orderedError{{"message", message}, {"type", "invalid_request_error"}, {"param", "input"}, {"code", "invalid_value"}}}
}

// orderedError is an error object whose members keep the order they are listed
// in, as the gateway writes them.
type orderedError []struct {
	key   string
	value any
}

func (e orderedError) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for index, member := range e {
		if index > 0 {
			out.WriteByte(',')
		}
		key, _ := json.Marshal(member.key)
		value, err := json.Marshal(member.value)
		if err != nil {
			return nil, err
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// imageRefusalStatus is the status of every picture refusal.
const imageRefusalStatus = http.StatusBadRequest
