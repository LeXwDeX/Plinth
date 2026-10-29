package openaiv1

import (
	"embed"
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
	// FixedModel is reported when a request names no model the fake knows.
	unauthorizedBody = "unauthorized\n"
)

// ToolMarker triggers a tool call: a request that offers tools and whose last
// message is a user message containing [[fake:tool:NAME {json args}]] is
// answered with a call of the tool NAME. Every other request is answered with
// the fixed text; a request whose last message is a tool result always is.
var toolMarker = regexp.MustCompile(`\[\[fake:tool:([A-Za-z0-9_.\-]+)(?:\s+(\{.*?\}))?\]\]`)

// ToolMarkerFor builds the marker a test or script puts in a user message.
func ToolMarkerFor(name string, arguments string) string {
	if arguments == "" {
		return "[[fake:tool:" + name + "]]"
	}
	return "[[fake:tool:" + name + " " + arguments + "]]"
}

// endpoints is the version's endpoint table. CheckedAt is the day the fake was
// compared with the live gateway (the samples' capture day); DocURL names the
// protocol reference the gateway claims compatibility with and was not
// re-read for this alignment.
var endpoints = []fakeupstream.Endpoint{
	{Method: "GET", Path: "/models", Evidence: fakeupstream.EvidenceReal, Sample: "GET /v1/models",
		DocURL: "https://platform.openai.com/docs/api-reference/models/list", CheckedAt: "2026-10-29"},
	{Method: "POST", Path: "/chat/completions", Evidence: fakeupstream.EvidenceReal, Sample: "POST /v1/chat/completions",
		DocURL: "https://platform.openai.com/docs/api-reference/chat/create", CheckedAt: "2026-10-29",
		Note: "non-streaming, SSE streaming, tool_calls, reasoning_content and the model_required error are all real samples"},
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

func (h *handler) render(w http.ResponseWriter, r *http.Request, scenario fakeupstream.Scenario) {
	status, kind, code, message := http.StatusInternalServerError, "server_error", "internal_error", "internal server error"
	switch scenario {
	case fakeupstream.ScenarioInsufficientBalance:
		status, kind, code, message = http.StatusPaymentRequired, "insufficient_quota", "insufficient_quota", "insufficient balance"
	case fakeupstream.ScenarioKeyQuota:
		status, kind, code, message = http.StatusPaymentRequired, "insufficient_quota", "key_credit_limit_reached", "API key credit limit reached"
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

// call decides the reply: a tool call (name, arguments) or "" for text.
func (r chatRequest) call() (name, arguments string) {
	if len(r.Messages) == 0 || r.Messages[len(r.Messages)-1].Role != "user" || len(r.Tools) == 0 {
		return "", ""
	}
	match := toolMarker.FindStringSubmatch(r.lastText())
	if match == nil || !r.hasTool(match[1]) {
		return "", ""
	}
	arguments = match[2]
	if arguments == "" || !json.Valid([]byte(arguments)) {
		arguments = "{}"
	}
	return match[1], arguments
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
	if h.env.Intercept(w, r, fakeupstream.PhaseCreate, h.render) {
		return
	}
	var request chatRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 8<<20))
	if err := decoder.Decode(&request); err != nil || strings.TrimSpace(request.Model) == "" {
		body := fakeupstream.Clone(h.invalid)
		if errorObject, ok := body["error"].(map[string]any); ok {
			errorObject["path"] = samplePrefix + r.URL.Path
		}
		writeJSON(w, http.StatusBadRequest, body)
		return
	}
	if fakeupstream.ScenarioOf(r) == fakeupstream.ScenarioLostResponse {
		fakeupstream.AbortConnection(w) // the completion ran; its response is lost
		return
	}
	name, arguments := request.call()
	reasoning := ""
	if request.thinking() {
		reasoning = FixedReasoning
	}
	if request.Stream {
		h.writeStream(w, request, name, arguments, reasoning)
		return
	}
	body := fakeupstream.Clone(h.text)
	if name != "" {
		body = fakeupstream.Clone(h.tool)
	}
	body["model"] = request.Model
	if message, ok := fakeupstream.DigMap(body, "choices", 0, "message"); ok {
		message["reasoning_content"] = reasoning
		if name == "" {
			message["content"] = FixedReply
		} else if calls, _ := message["tool_calls"].([]any); len(calls) > 0 {
			call, _ := calls[0].(map[string]any)
			function, _ := call["function"].(map[string]any)
			function["name"], function["arguments"] = name, arguments
		}
	}
	writeJSON(w, http.StatusOK, body)
}

// writeStream emits the chunk pattern the official gateway streams (observed
// 2026-10-29): a role chunk, a reasoning chunk, then either the answer text or
// a tool call split into its header and its arguments, and a final chunk
// carrying finish_reason and usage. The envelope fields and the final usage
// object come from the committed sample.
func (h *handler) writeStream(w http.ResponseWriter, request chatRequest, name, arguments, reasoning string) {
	sample := h.stream
	if name != "" {
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
	if name == "" {
		events = append(events, chunk(map[string]any{"content": FixedReply, "reasoning_content": nil}, nil, nil))
	} else {
		finish = "tool_calls"
		events = append(events,
			chunk(map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": "call_fake_0", "type": "function", "function": map[string]any{"name": name, "arguments": ""},
			}}}, nil, nil),
			chunk(map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "function": map[string]any{"arguments": arguments},
			}}}, nil, nil))
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
