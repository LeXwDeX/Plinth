package openaiv1

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

//go:embed testdata/openai-v1.candidate.json
var sampleFS embed.FS

const sampleFile = "testdata/openai-v1.candidate.json"

// Prefix is where this version is mounted.
const Prefix = "/openai/v1"

// samplePrefix is the path prefix the official API uses; the committed
// sample records it.
const samplePrefix = "/v1"

const (
	// FixedReply and FixedReasoning are the fixed contents of every text
	// completion.
	FixedReply     = "Fake upstream reply."
	FixedReasoning = "Fake upstream reasoning."
	// unauthorizedBody is the gateway's plain-text 401 (a real sample).
	unauthorizedBody = "unauthorized\n"
)

// StreamErrorMarker in the last message of a streaming request makes the fake
// answer HTTP 200 and then end the started stream with an error frame instead
// of a completion chunk, as the official 200 description allows. The shared
// scenario table has no stream-error word, so, like the tool marker, this is a
// fake-only request convention.
const StreamErrorMarker = "[[fake:stream-error]]"

// ToolMarkerPrefix triggers a tool call: a request that offers tools and whose
// last message is a user message containing [[fake:tool:NAME {json args}]] is
// answered with a call of the tool NAME. The JSON object is scanned by balanced
// braces, so nested steps/contingencies/parameters work (#251); the arguments
// are replaced with {} when they are not valid JSON. Every other request is
// answered with the fixed text; a request whose last message is a tool result
// always is.
const ToolMarkerPrefix = "[[fake:tool:"

// SystemContainsMarkerPrefix starts a fake-only request convention: a request
// whose last user message contains [[fake:system-contains TEXT]] is answered
// with "present: TEXT" when any system message contains TEXT, else
// "absent: TEXT". The official model has no such behavior; the marker exists
// so a smoke scenario can assert what the Agent was told without reading the
// server's private prompt.
const SystemContainsMarkerPrefix = "[[fake:system-contains "

var systemContainsMarker = regexp.MustCompile(`\[\[fake:system-contains ([^\]\n]+)\]\]`)

// systemContainsAsk returns the text a marker asks about.
func (r chatRequest) systemContainsAsk() (string, bool) {
	matches := systemContainsMarker.FindAllStringSubmatch(r.lastText(), -1)
	if len(matches) == 0 {
		return "", false
	}
	return strings.TrimSpace(matches[0][1]), true
}

// systemContains reports whether any system message carries the text.
func (r chatRequest) systemContains(text string) bool {
	for _, message := range r.Messages {
		if message.Role != "system" || text == "" {
			continue
		}
		var content string
		if json.Unmarshal(message.Content, &content) == nil && strings.Contains(content, text) {
			return true
		}
	}
	return false
}

// ToolMarkerFor builds the marker a test or script puts in a user message.
func ToolMarkerFor(name string, arguments string) string {
	if arguments == "" {
		return "[[fake:tool:" + name + "]]"
	}
	return "[[fake:tool:" + name + " " + arguments + "]]"
}

// docsCheckedAt is the day the constructed fault answers below were checked
// against the official documents; docsErrors is the error-code guide they use.
const (
	docsCheckedAt = "2026-11-08"
	docsErrors    = "https://developers.openai.com/api/docs/guides/error-codes"
)

// endpoints is the version's endpoint table. CheckedAt is the day the fake was
// compared with the live gateway (the samples' capture day); DocURL names the
// protocol reference the gateway claims compatibility with and was not
// re-read for this alignment.
var endpoints = []fakeupstream.Endpoint{
	{Method: "GET", Path: "/models", Evidence: fakeupstream.EvidenceReal, Sample: "GET /v1/models",
		DocURL: "https://platform.openai.com/docs/api-reference/models/list", CheckedAt: "2026-10-29",
		Note: "the model list and the gateway's text/plain 401 are real samples; the invalid-key 401 ErrorResponse is a constructed-from-docs answer built from the OpenAPI ErrorResponse and the error-code guide (" + docsCheckedAt + ")"},
	{Method: "POST", Path: "/chat/completions", Evidence: fakeupstream.EvidenceReal, Sample: "POST /v1/chat/completions",
		DocURL: "https://platform.openai.com/docs/api-reference/chat/create", CheckedAt: "2026-10-29",
		Note: "non-streaming, SSE streaming, tool_calls, reasoning_content and the model_required error are all real samples. The fault scenarios are constructed-from-docs answers (no committed sample) built from the OpenAPI and " + docsErrors + " (" + docsCheckedAt + "): invalid-key 401 ErrorResponse, forbidden 403 (text/plain JSON error text), model-not-found 404, service-unavailable 503 with Retry-After, quota-exceeded 429 with error.quota, and " + StreamErrorMarker + " ending a started stream with an error frame; the messages 400 follows the request schema's required/minItems 1. The gateway error envelope lacks the official param and carries path instead (see doc.go)"},
}

// Version is the OpenAI-compatible /v1 fake.
func Version() fakeupstream.Version {
	return fakeupstream.Version{
		Provider: "openai", API: "v1", Prefix: Prefix, Official: OfficialBase,
		Endpoints: endpoints,
		Handler:   newHandler,
	}
}

type handler struct {
	env     *fakeupstream.Env
	models  map[string]any
	text    map[string]any
	tool    map[string]any
	invalid map[string]any
	stream  []map[string]any
	toolSSE []map[string]any
}

func newHandler(env *fakeupstream.Env) (http.Handler, error) {
	samples, err := fakeupstream.LoadSamples(sampleFS, sampleFile)
	if err != nil {
		return nil, err
	}
	h := &handler{env: env}
	for _, exchange := range samples.Find("GET /v1/models", "") {
		if exchange.Response.Status == http.StatusOK {
			if h.models, err = fakeupstream.JSONBody(exchange); err != nil {
				return nil, err
			}
		}
	}
	for _, exchange := range samples.Find("POST /v1/chat/completions", "") {
		switch {
		case exchange.Response.Status == http.StatusBadRequest:
			h.invalid, err = fakeupstream.JSONBody(exchange)
		case exchange.Response.Status == http.StatusOK && exchange.Response.Body.Encoding == wirefixture.PayloadSSE:
			events, parseErr := streamEvents(exchange.Response.Body.SSE)
			err = parseErr
			if hasToolCall(events) {
				h.toolSSE = events
			} else {
				h.stream = events
			}
		case exchange.Response.Status == http.StatusOK && exchange.Response.Body.Encoding == wirefixture.PayloadJSON:
			var body map[string]any
			body, err = fakeupstream.JSONBody(exchange)
			if strings.Contains(string(exchange.Response.Body.JSON), `"tool_calls"`) {
				h.tool = body
			} else {
				h.text = body
			}
		}
		if err != nil {
			return nil, err
		}
	}
	if h.models == nil || h.text == nil || h.tool == nil || h.invalid == nil || len(h.stream) == 0 || len(h.toolSSE) == 0 {
		return nil, errors.New("openai/v1 sample lacks models, text, tool, stream, tool stream or error exchanges")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /models", h.listModels)
	mux.HandleFunc("POST /chat/completions", h.chat)
	return mux, nil
}

// streamEvents parses the data events of a sample SSE stream.
func streamEvents(raw string) ([]map[string]any, error) {
	var events []map[string]any
	for _, block := range strings.Split(raw, "\n\n") {
		payload, ok := strings.CutPrefix(strings.TrimSpace(block), "data: ")
		if !ok || payload == "[DONE]" {
			continue
		}
		var event map[string]any
		decoder := json.NewDecoder(strings.NewReader(payload))
		decoder.UseNumber()
		if err := decoder.Decode(&event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

func hasToolCall(events []map[string]any) bool {
	for _, event := range events {
		if _, ok := fakeupstream.Dig(event, "choices", 0, "delta", "tool_calls"); ok {
			return true
		}
	}
	return false
}

// render answers the scenarios the shared Env.Intercept owns (balance, key
// quota, rate limit and server error). Both quota answers are 429: the official
// Chat Completions API has no 402, and the recorded gateway answers its quota
// refusals with 429. The envelope stays the gateway's real one (see doc.go).
func (h *handler) render(w http.ResponseWriter, r *http.Request, scenario fakeupstream.Scenario) {
	status, kind, code, message := http.StatusInternalServerError, "server_error", "internal_error", "internal server error"
	switch scenario {
	case fakeupstream.ScenarioInsufficientBalance:
		status, kind, code, message = http.StatusTooManyRequests, "insufficient_quota", "insufficient_quota", "You exceeded your current quota, please check your plan and billing details."
	case fakeupstream.ScenarioKeyQuota:
		status, kind, code, message = http.StatusTooManyRequests, "insufficient_quota", "quota_exceeded", "API key credit limit reached"
	case fakeupstream.ScenarioRateLimit:
		status, kind, code, message = http.StatusTooManyRequests, "rate_limit_error", "rate_limit_exceeded", "rate limit exceeded"
		w.Header().Set("Retry-After", h.env.RetryAfterSeconds())
	}
	body := fakeupstream.Clone(h.invalid)
	if errorObject, ok := body["error"].(map[string]any); ok {
		errorObject["message"], errorObject["type"], errorObject["code"], errorObject["path"] = message, kind, code, samplePrefix+r.URL.Path
	}
	writeJSON(w, status, body)
}

// officialErrorBody is the ErrorResponse the official OpenAPI declares: the
// error object's required fields are type, message, param and code, and param
// and code may be null. The gateway's real envelope instead carries path and
// no param (see doc.go).
func officialErrorBody(message, kind string, param, code any) map[string]any {
	return map[string]any{"error": map[string]any{
		"message": message, "type": kind, "param": param, "code": code,
	}}
}

// quotaExceededBody is the weekly-quota refusal the dev-test gateway was
// observed to answer (the same body is recorded in
// internal/providers/httpjson/quota_test.go): HTTP 429 with error.code and
// error.type quota_exceeded and a nested error.quota object.
func quotaExceededBody() map[string]any {
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
// bodies are constructed from the official OpenAPI and error-code guide named
// in the endpoint table.
func (h *handler) documentedFault(w http.ResponseWriter, scenario fakeupstream.Scenario) bool {
	switch scenario {
	case fakeupstream.ScenarioInvalidKey:
		writeJSON(w, http.StatusUnauthorized, officialErrorBody("Incorrect API key provided", "invalid_request_error", nil, "invalid_api_key"))
	case fakeupstream.ScenarioForbidden:
		// The official 403 carries text/plain JSON-encoded error text.
		raw, _ := json.Marshal(officialErrorBody("Access to a personal API organization is blocked by the organization policy.", "invalid_request_error", nil, nil))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write(append(raw, '\n'))
	case fakeupstream.ScenarioServiceUnavailable:
		w.Header().Set("Retry-After", h.env.RetryAfterSeconds())
		writeJSON(w, http.StatusServiceUnavailable, officialErrorBody("The model is temporarily overloaded. Please retry your request after a brief delay.", "service_unavailable_error", nil, "server_is_overloaded"))
	case fakeupstream.ScenarioQuotaExceeded:
		writeJSON(w, http.StatusTooManyRequests, quotaExceededBody())
	default:
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	raw, _ := json.Marshal(body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(raw, '\n'))
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = io.WriteString(w, unauthorizedBody)
}

func (h *handler) listModels(w http.ResponseWriter, r *http.Request) {
	if fakeupstream.BearerKey(r) == "" {
		unauthorized(w)
		return
	}
	if fakeupstream.ScenarioOf(r) == fakeupstream.ScenarioInvalidKey {
		h.documentedFault(w, fakeupstream.ScenarioInvalidKey)
		return
	}
	if h.env.Intercept(w, r, fakeupstream.PhaseRead, h.render) {
		return
	}
	writeJSON(w, http.StatusOK, h.models)
}

type chatRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
	Stream        bool `json:"stream"`
	StreamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
	Tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
	Thinking       *json.RawMessage `json:"thinking"`
	EnableThinking *bool            `json:"enable_thinking"`
}

func (r chatRequest) lastText() string {
	if len(r.Messages) == 0 {
		return ""
	}
	content := r.Messages[len(r.Messages)-1].Content
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) == nil {
		var all []string
		for _, part := range parts {
			all = append(all, part.Text)
		}
		return strings.Join(all, "\n")
	}
	return ""
}

func (r chatRequest) hasTool(name string) bool {
	for _, tool := range r.Tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}

// toolCall is one marker parsed from the last user message.
type toolCall struct {
	name, arguments string
}

// calls decides the reply: one call per marker the request offers, in order.
// A marker naming a tool the request does not offer is ignored, so a request
// without a usable marker is answered with the fixed text.
func (r chatRequest) calls() []toolCall {
	if len(r.Messages) == 0 || r.Messages[len(r.Messages)-1].Role != "user" || len(r.Tools) == 0 {
		return nil
	}
	markers := parseToolMarkers(r.lastText())
	calls := make([]toolCall, 0, len(markers))
	for _, marker := range markers {
		if !r.hasTool(marker.name) {
			continue
		}
		arguments := marker.arguments
		if arguments == "" || !json.Valid([]byte(arguments)) {
			arguments = "{}"
		}
		calls = append(calls, toolCall{name: marker.name, arguments: arguments})
	}
	return calls
}

// parseToolMarkers finds every [[fake:tool:NAME ...]] marker in text. The
// optional JSON object is read with a balanced-brace scanner that respects
// strings, so nested objects and arrays are preserved. A malformed or
// unbalanced object falls back to {} like the historical single-level form.
func parseToolMarkers(text string) []toolCall {
	const prefix = ToolMarkerPrefix
	var markers []toolCall
	for offset := 0; offset < len(text); {
		start := strings.Index(text[offset:], prefix)
		if start < 0 {
			break
		}
		start += offset
		nameStart := start + len(prefix)
		nameEnd := nameStart
		for nameEnd < len(text) && isToolMarkerNameChar(text[nameEnd]) {
			nameEnd++
		}
		if nameEnd == nameStart {
			offset = nameStart
			continue
		}
		name := text[nameStart:nameEnd]
		arguments := ""
		position := nameEnd
		if position < len(text) && text[position] == ' ' {
			for position < len(text) && text[position] == ' ' {
				position++
			}
			if position < len(text) && text[position] == '{' {
				if end, ok := scanBalancedJSON(text, position); ok {
					arguments = text[position:end]
					closeAt := end
					for closeAt < len(text) && text[closeAt] == ' ' {
						closeAt++
					}
					if strings.HasPrefix(text[closeAt:], "]]") {
						markers = append(markers, toolCall{name: name, arguments: arguments})
						offset = closeAt + 2
						continue
					}
				}
			}
			// No balanced JSON object closed by ]]: consume the marker up to
			// the next ]] and answer with empty arguments, as before.
			closeRel := strings.Index(text[position:], "]]")
			if closeRel < 0 {
				offset = position
				continue
			}
			closeAt := position + closeRel
			markers = append(markers, toolCall{name: name})
			offset = closeAt + 2
			continue
		}
		if strings.HasPrefix(text[position:], "]]") {
			markers = append(markers, toolCall{name: name})
			offset = position + 2
			continue
		}
		offset = position
	}
	return markers
}

func isToolMarkerNameChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '_' || c == '.' || c == '-'
}

// scanBalancedJSON returns the end (exclusive) of the JSON object starting at
// start, or false when it never closes. Braces inside strings are ignored.
func scanBalancedJSON(text string, start int) (int, bool) {
	depth := 0
	inString, escaped := false, false
	for index := start; index < len(text); index++ {
		switch {
		case inString && escaped:
			escaped = false
		case inString && text[index] == '\\':
			escaped = true
		case inString && text[index] == '"':
			inString = false
		case inString:
		case text[index] == '"':
			inString = true
		case text[index] == '{':
			depth++
		case text[index] == '}':
			depth--
			if depth == 0 {
				return index + 1, true
			}
		}
	}
	return 0, false
}

func (r chatRequest) thinking() bool {
	if r.EnableThinking != nil && *r.EnableThinking {
		return true
	}
	if r.Thinking == nil {
		return false
	}
	var value struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(*r.Thinking, &value) == nil && value.Type == "enabled"
}

func (h *handler) chat(w http.ResponseWriter, r *http.Request) {
	if fakeupstream.BearerKey(r) == "" {
		unauthorized(w)
		return
	}
	scenario := fakeupstream.ScenarioOf(r)
	if h.documentedFault(w, scenario) {
		return
	}
	if h.env.Intercept(w, r, fakeupstream.PhaseCreate, h.render) {
		return
	}
	var request chatRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 8<<20))
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, officialErrorBody("We could not parse the JSON body of your request. The body may be malformed or contain unsupported characters.", "invalid_request_error", nil, nil))
		return
	}
	if strings.TrimSpace(request.Model) == "" {
		body := fakeupstream.Clone(h.invalid)
		if errorObject, ok := body["error"].(map[string]any); ok {
			errorObject["path"] = samplePrefix + r.URL.Path
		}
		writeJSON(w, http.StatusBadRequest, body)
		return
	}
	if len(request.Messages) == 0 {
		// The official request schema requires messages with at least one item.
		writeJSON(w, http.StatusBadRequest, officialErrorBody("Missing required parameter: 'messages'.", "invalid_request_error", "messages", "missing_required_parameter"))
		return
	}
	h.recordChatRequest(request)
	if scenario == fakeupstream.ScenarioModelNotFound {
		writeJSON(w, http.StatusNotFound, officialErrorBody(fmt.Sprintf("The model %q does not exist or you do not have access to it.", request.Model), "invalid_request_error", "model", "model_not_found"))
		return
	}
	if scenario == fakeupstream.ScenarioLostResponse {
		fakeupstream.AbortConnection(w) // the completion ran; its response is lost
		return
	}
	if request.Stream && strings.Contains(request.lastText(), StreamErrorMarker) {
		h.writeStreamError(w, request)
		return
	}
	calls := request.calls()
	reasoning := ""
	if request.thinking() {
		reasoning = FixedReasoning
	}
	reply := FixedReply
	if needle, asked := request.systemContainsAsk(); asked {
		reply = "absent: " + needle
		if request.systemContains(needle) {
			reply = "present: " + needle
		}
	}
	if request.Stream {
		h.writeStream(w, request, calls, reasoning, reply)
		return
	}
	body := fakeupstream.Clone(h.text)
	if len(calls) > 0 {
		body = fakeupstream.Clone(h.tool)
	}
	body["model"] = request.Model
	if message, ok := fakeupstream.DigMap(body, "choices", 0, "message"); ok {
		message["reasoning_content"] = reasoning
		if len(calls) == 0 {
			message["content"] = reply
		} else if sample, _ := message["tool_calls"].([]any); len(sample) > 0 {
			template, _ := sample[0].(map[string]any)
			rendered := make([]any, 0, len(calls))
			for index, call := range calls {
				rendered = append(rendered, renderToolCall(template, index, call))
			}
			message["tool_calls"] = rendered
		}
	}
	writeJSON(w, http.StatusOK, body)
}

// recordChatRequest keeps the bounded #247 record of what Plinth sent: roles,
// short text and each picture part's detail, data-URI marker and byte count.
// Picture bytes are never stored.
func (h *handler) recordChatRequest(request chatRequest) {
	record := fakeupstream.ChatRequestRecord{Model: request.Model}
	for _, message := range request.Messages {
		entry := fakeupstream.ChatMessageRecord{Role: message.Role}
		var text string
		if json.Unmarshal(message.Content, &text) == nil {
			entry.Text = summarizeChatText(text)
		} else {
			var parts []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL struct {
					URL    string `json:"url"`
					Detail string `json:"detail"`
				} `json:"image_url"`
			}
			if json.Unmarshal(message.Content, &parts) == nil {
				var texts []string
				for _, part := range parts {
					if part.Text != "" {
						texts = append(texts, part.Text)
						// The text right after a picture is its label.
						if len(entry.Images) > 0 && entry.Images[len(entry.Images)-1].Label == "" &&
							!strings.HasPrefix(part.Text, "以下是") && strings.TrimSpace(part.Text) != "" {
							entry.Images[len(entry.Images)-1].Label = summarizeChatText(part.Text)
						}
					}
					if part.ImageURL.URL != "" || part.Type == "image_url" {
						entry.Images = append(entry.Images, chatImageRecord(part.ImageURL.URL, part.ImageURL.Detail))
					}
				}
				entry.Text = summarizeChatText(strings.Join(texts, "\n"))
			}
		}
		record.Messages = append(record.Messages, entry)
	}
	h.env.RecordChatRequest(record)
}

func chatImageRecord(url, detail string) fakeupstream.ChatImageRecord {
	record := fakeupstream.ChatImageRecord{Detail: detail}
	lower := strings.ToLower(url)
	if strings.HasPrefix(lower, "data:image/") {
		record.DataURI = true
		if comma := strings.Index(url, ","); comma >= 0 {
			if data, err := base64.StdEncoding.DecodeString(url[comma+1:]); err == nil {
				record.Bytes = len(data)
				return record
			}
			record.Bytes = len(url) - comma - 1
			return record
		}
	}
	record.Bytes = len(url)
	return record
}

// summarizeChatText keeps a short, bounded summary; the record is a fixture,
// not a transcript.
func summarizeChatText(text string) string {
	const limit = 200
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= limit {
		return text
	}
	return text[:limit]
}

// renderToolCall clones the sample's first call for one parsed marker. The
// single-call reply keeps the sample's shape; later calls get unique IDs.
func renderToolCall(template map[string]any, index int, call toolCall) map[string]any {
	clone := fakeupstream.Clone(template)
	clone["id"] = fmt.Sprintf("call_fake_%d", index)
	if function, ok := clone["function"].(map[string]any); ok {
		function["name"], function["arguments"] = call.name, call.arguments
	}
	return clone
}

// writeStreamError starts a normal 200 stream (one opening role chunk) and
// then sends a data frame carrying an error object instead of a completion
// chunk, as the official 200 description allows. The interrupted stream ends
// without [DONE].
func (h *handler) writeStreamError(w http.ResponseWriter, request chatRequest) {
	opening := fakeupstream.Clone(h.stream[0])
	opening["model"] = request.Model
	if choice, ok := fakeupstream.DigMap(opening, "choices", 0); ok {
		choice["delta"] = map[string]any{"role": "assistant", "content": nil, "reasoning_content": ""}
		choice["finish_reason"] = nil
	}
	opening["usage"] = nil
	frame := officialErrorBody("The model is temporarily overloaded. Please retry your request after a brief delay.", "service_unavailable_error", nil, "server_is_overloaded")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for _, event := range []map[string]any{opening, frame} {
		raw, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// writeStream emits the chunk pattern the official gateway streams (observed
// 2026-10-29): a role chunk, a reasoning chunk, then either the answer text or
// a tool call split into its header and its arguments, and a final chunk
// carrying finish_reason and usage. The envelope fields and the final usage
// object come from the committed sample.
func (h *handler) writeStream(w http.ResponseWriter, request chatRequest, calls []toolCall, reasoning, reply string) {
	sample := h.stream
	if len(calls) > 0 {
		sample = h.toolSSE
	}
	envelope, last := sample[0], sample[len(sample)-1]
	chunk := func(delta map[string]any, finish any, usage any) map[string]any {
		event := fakeupstream.Clone(envelope)
		event["model"] = request.Model
		if choice, ok := fakeupstream.DigMap(event, "choices", 0); ok {
			choice["delta"], choice["finish_reason"] = delta, finish
		}
		event["usage"] = usage
		return event
	}
	var usage any
	if request.StreamOptions.IncludeUsage {
		usage, _ = fakeupstream.Dig(fakeupstream.Clone(last), "usage")
	}
	events := []map[string]any{
		chunk(map[string]any{"role": "assistant", "content": nil, "reasoning_content": ""}, nil, nil),
		chunk(map[string]any{"content": nil, "reasoning_content": reasoning}, nil, nil),
	}
	finish := "stop"
	if len(calls) == 0 {
		events = append(events, chunk(map[string]any{"content": reply, "reasoning_content": nil}, nil, nil))
	} else {
		finish = "tool_calls"
		for index, call := range calls {
			events = append(events,
				chunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": index, "id": fmt.Sprintf("call_fake_%d", index), "type": "function",
					"function": map[string]any{"name": call.name, "arguments": ""},
				}}}, nil, nil))
		}
		for index, call := range calls {
			events = append(events,
				chunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": index, "function": map[string]any{"arguments": call.arguments},
				}}}, nil, nil))
		}
	}
	events = append(events, chunk(map[string]any{"content": "", "reasoning_content": nil}, finish, usage))
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for _, event := range events {
		raw, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		if flusher != nil {
			flusher.Flush()
		}
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}
