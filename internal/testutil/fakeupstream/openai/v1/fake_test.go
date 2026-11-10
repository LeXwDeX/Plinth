package openaiv1

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

type fixture struct {
	t      *testing.T
	server *httptest.Server
}

func newFixture(t *testing.T, config fakeupstream.Config) *fixture {
	t.Helper()
	fake, err := fakeupstream.New(config, Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return &fixture{t: t, server: server}
}

func (f *fixture) send(method, path, key string, body any, headers ...string) (*http.Response, []byte) {
	f.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, f.server.URL+Prefix+path, reader)
	if err != nil {
		f.t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		f.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response, raw
}

func (f *fixture) chat(body map[string]any, headers ...string) (int, map[string]any) {
	f.t.Helper()
	response, raw := f.send("POST", "/chat/completions", "test-key", body, headers...)
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	return response.StatusCode, decoded
}

func user(text string) []map[string]any {
	return []map[string]any{{"role": "user", "content": text}}
}

var lookupTools = []map[string]any{{"type": "function", "function": map[string]any{"name": "lookup_note"}}}

func dig(t *testing.T, value any, path ...any) any {
	t.Helper()
	found, ok := fakeupstream.Dig(value, path...)
	if !ok {
		t.Fatalf("no %v in %v", path, value)
	}
	return found
}

// sseEvents reads a streamed response into JSON chunks and notes [DONE].
func sseEvents(t *testing.T, raw []byte) (chunks []map[string]any, done bool) {
	t.Helper()
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		payload, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		if payload == "[DONE]" {
			done = true
			continue
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("bad chunk %q: %v", payload, err)
		}
		chunks = append(chunks, chunk)
	}
	return chunks, done
}

func TestModelsAndAuthentication(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	response, raw := f.send("GET", "/models", "k", nil)
	var models map[string]any
	_ = json.Unmarshal(raw, &models)
	if response.StatusCode != 200 || len(dig(t, models, "data").([]any)) == 0 || dig(t, models, "data", 0, "object") != "model" {
		t.Fatalf("models = %d %s", response.StatusCode, raw)
	}
	for _, path := range []string{"/models", "/chat/completions"} {
		method := map[string]string{"/models": "GET", "/chat/completions": "POST"}[path]
		response, raw := f.send(method, path, "", map[string]any{"model": "m", "messages": user("x")})
		if response.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/plain") || len(raw) == 0 {
			t.Fatalf("%s without a key = %d %q %q", path, response.StatusCode, response.Header.Get("Content-Type"), raw)
		}
	}
}

func TestChatNonStreaming(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, body := f.chat(map[string]any{"model": "deepseek", "messages": user("hi"), "thinking": map[string]any{"type": "enabled"}, "reasoning_effort": "low"})
	if status != 200 || dig(t, body, "choices", 0, "message", "content") != FixedReply ||
		dig(t, body, "choices", 0, "message", "reasoning_content") != FixedReasoning || dig(t, body, "model") != "deepseek" ||
		dig(t, body, "choices", 0, "finish_reason") != "stop" {
		t.Fatalf("%d %v", status, body)
	}
	// Thinking not requested: reasoning_content stays present and empty.
	_, plain := f.chat(map[string]any{"model": "deepseek", "messages": user("hi")})
	if dig(t, plain, "choices", 0, "message", "reasoning_content") != "" {
		t.Fatalf("reasoning without thinking: %v", plain)
	}
	// The qwen-style flag also turns reasoning on.
	_, qwen := f.chat(map[string]any{"model": "qwen", "messages": user("hi"), "enable_thinking": true})
	if dig(t, qwen, "choices", 0, "message", "reasoning_content") != FixedReasoning {
		t.Fatalf("enable_thinking ignored: %v", qwen)
	}
	// Array content parts are accepted.
	status, _ = f.chat(map[string]any{"model": "m", "messages": []map[string]any{{"role": "user", "content": []map[string]any{{"type": "text", "text": "x"}}}}})
	if status != 200 {
		t.Fatalf("content parts = %d", status)
	}
}

func TestChatRequestValidation(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for name, body := range map[string]map[string]any{"no model": {"messages": user("x")}, "empty": {}} {
		status, response := f.chat(body)
		if status != 400 || dig(t, response, "error", "code") != "model_required" || dig(t, response, "error", "type") != "invalid_request_error" {
			t.Errorf("%s: %d %v", name, status, response)
		}
	}
	// messages is required and must hold at least one item.
	for name, body := range map[string]map[string]any{"no messages": {"model": "m"}, "empty messages": {"model": "m", "messages": []map[string]any{}}} {
		status, response := f.chat(body)
		if status != http.StatusBadRequest || dig(t, response, "error", "param") != "messages" || dig(t, response, "error", "code") != "missing_required_parameter" {
			t.Errorf("%s: %d %v", name, status, response)
		}
	}
	// Malformed JSON is its own 400, not the model_required answer.
	response, raw := f.send("POST", "/chat/completions", "test-key", json.RawMessage(`{"model":`))
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	if response.StatusCode != http.StatusBadRequest || dig(t, body, "error", "type") != "invalid_request_error" ||
		!strings.Contains(dig(t, body, "error", "message").(string), "parse") || dig(t, body, "error", "param") != nil {
		t.Errorf("malformed JSON: %d %s", response.StatusCode, raw)
	}
}

func TestToolCallsFollowTheMarkerAndToolResultsEndTheTurn(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	marker := ToolMarkerFor("lookup_note", `{"topic":"cube"}`)
	status, body := f.chat(map[string]any{"model": "m", "messages": user("please " + marker), "tools": lookupTools})
	call := dig(t, body, "choices", 0, "message", "tool_calls", 0)
	if status != 200 || dig(t, call, "function", "name") != "lookup_note" || dig(t, call, "function", "arguments") != `{"topic":"cube"}` ||
		dig(t, body, "choices", 0, "finish_reason") != "tool_calls" {
		t.Fatalf("%d %v", status, body)
	}
	// Same text without tools on offer, or naming a tool that is not offered: plain text.
	for name, request := range map[string]map[string]any{
		"no tools":     {"model": "m", "messages": user(marker)},
		"unknown tool": {"model": "m", "messages": user(ToolMarkerFor("other", "")), "tools": lookupTools},
		"no marker":    {"model": "m", "messages": user("hello"), "tools": lookupTools},
	} {
		_, body := f.chat(request)
		if dig(t, body, "choices", 0, "message", "content") != FixedReply {
			t.Errorf("%s: %v", name, body)
		}
	}
	// A tool result as the last message ends the turn with text, even though
	// the earlier user message still carries the marker.
	messages := []map[string]any{
		{"role": "user", "content": marker},
		{"role": "assistant", "content": nil, "tool_calls": []any{call}},
		{"role": "tool", "tool_call_id": "call_fake_0", "content": "{}"},
	}
	_, final := f.chat(map[string]any{"model": "m", "messages": messages, "tools": lookupTools})
	if dig(t, final, "choices", 0, "message", "content") != FixedReply || fakeupstream.Clone(final) == nil {
		t.Fatalf("after the tool result: %v", final)
	}
	if _, has := fakeupstream.Dig(final, "choices", 0, "message", "tool_calls"); has {
		t.Fatalf("tool call repeated after the tool result: %v", final)
	}
	// A marker without arguments calls the tool with an empty object.
	_, empty := f.chat(map[string]any{"model": "m", "messages": user(ToolMarkerFor("lookup_note", "")), "tools": lookupTools})
	if dig(t, empty, "choices", 0, "message", "tool_calls", 0, "function", "arguments") != "{}" {
		t.Fatalf("empty arguments: %v", empty)
	}
}

// #250: the fake-only system-contains marker answers whether the system prompt
// carries the requested text, in both plain and streamed replies.
func TestSystemContainsMarkerAnswersFromTheSystemPrompt(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	ask := func(system, needle string) map[string]any {
		messages := []map[string]any{
			{"role": "system", "content": system},
			{"role": "user", "content": "check " + SystemContainsMarkerPrefix + needle + "]]"},
		}
		return map[string]any{"model": "m", "messages": messages}
	}
	status, present := f.chat(ask("<<<VISUAL_ANCHORS>>> 用户风格锁：ink and paper", "用户风格锁"))
	if status != http.StatusOK || dig(t, present, "choices", 0, "message", "content") != "present: 用户风格锁" {
		t.Fatalf("present: %d %v", status, present)
	}
	_, absent := f.chat(ask("<<<VISUAL_ANCHORS>>> 用户风格锁：关闭", "身份锚「hero」"))
	if dig(t, absent, "choices", 0, "message", "content") != "absent: 身份锚「hero」" {
		t.Fatalf("absent: %v", absent)
	}
	body := ask("system text", "system")
	body["stream"] = true
	response, raw := f.send("POST", "/chat/completions", "k", body)
	chunks, done := sseEvents(t, raw)
	if response.StatusCode != http.StatusOK || !done || len(chunks) == 0 {
		t.Fatalf("stream: %d done=%t chunks=%d", response.StatusCode, done, len(chunks))
	}
	found := false
	for _, chunk := range chunks {
		if dig(t, chunk, "choices", 0, "delta", "content") == "present: system" {
			found = true
		}
	}
	if !found {
		t.Fatalf("streamed system-contains reply missing: %v", chunks)
	}
}

func TestStreaming(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	stream := func(body map[string]any) ([]map[string]any, bool, string) {
		body["stream"] = true
		response, raw := f.send("POST", "/chat/completions", "k", body)
		chunks, done := sseEvents(t, raw)
		return chunks, done, response.Header.Get("Content-Type")
	}
	chunks, done, contentType := stream(map[string]any{"model": "deepseek", "messages": user("hi"), "thinking": map[string]any{"type": "enabled"}, "stream_options": map[string]any{"include_usage": true}})
	if !done || contentType != "text/event-stream" || len(chunks) < 4 {
		t.Fatalf("done=%t type=%q chunks=%d", done, contentType, len(chunks))
	}
	if dig(t, chunks[0], "choices", 0, "delta", "role") != "assistant" || dig(t, chunks[1], "choices", 0, "delta", "reasoning_content") != FixedReasoning {
		t.Fatalf("opening chunks: %v %v", chunks[0], chunks[1])
	}
	var text strings.Builder
	for _, chunk := range chunks {
		if content, ok := dig(t, chunk, "choices", 0, "delta", "content").(string); ok {
			text.WriteString(content)
		}
	}
	last := chunks[len(chunks)-1]
	if text.String() != FixedReply || dig(t, last, "choices", 0, "finish_reason") != "stop" || dig(t, last, "usage", "total_tokens") == nil {
		t.Fatalf("text %q last %v", text.String(), last)
	}
	// Without include_usage the usage stays null.
	chunks, _, _ = stream(map[string]any{"model": "deepseek", "messages": user("hi")})
	if dig(t, chunks[len(chunks)-1], "usage") != nil {
		t.Fatalf("usage without include_usage: %v", chunks[len(chunks)-1])
	}
	// Streamed tool call: header chunk, then the arguments, then finish.
	chunks, done, _ = stream(map[string]any{"model": "deepseek", "messages": user(ToolMarkerFor("lookup_note", `{"topic":"cube"}`)), "tools": lookupTools, "stream_options": map[string]any{"include_usage": true}})
	var name, arguments string
	for _, chunk := range chunks {
		if calls, ok := fakeupstream.Dig(chunk, "choices", 0, "delta", "tool_calls"); ok {
			function := dig(t, calls, 0, "function").(map[string]any)
			if value, _ := function["name"].(string); value != "" {
				name = value
			}
			if value, _ := function["arguments"].(string); value != "" {
				arguments += value
			}
		}
	}
	if !done || name != "lookup_note" || arguments != `{"topic":"cube"}` || dig(t, chunks[len(chunks)-1], "choices", 0, "finish_reason") != "tool_calls" {
		t.Fatalf("tool stream: name=%q args=%q done=%t", name, arguments, done)
	}
}

// A last user message may carry several markers: the reply names the offered
// calls in order, with unique IDs, both in one body and in the stream. A stack
// uses this to put two paid calls in one assistant completion (#189).
func TestSeveralMarkersBecomeOrderedToolCalls(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	marker := ToolMarkerFor("lookup_note", `{"topic":"one"}`) + " " + ToolMarkerFor("lookup_note", `{"topic":"two"}`)
	status, body := f.chat(map[string]any{"model": "m", "messages": user(marker), "tools": lookupTools})
	calls, _ := dig(t, body, "choices", 0, "message", "tool_calls").([]any)
	if status != http.StatusOK || len(calls) != 2 ||
		dig(t, calls, 0, "function", "arguments") != `{"topic":"one"}` ||
		dig(t, calls, 1, "function", "arguments") != `{"topic":"two"}` ||
		dig(t, calls, 0, "id") == dig(t, calls, 1, "id") {
		t.Fatalf("non-stream calls = %d %v", status, body)
	}
	response, raw := f.send("POST", "/chat/completions", "test-key", map[string]any{
		"model": "m", "messages": user(marker), "tools": lookupTools, "stream": true})
	chunks, done := sseEvents(t, raw)
	names, arguments := map[int]string{}, map[int]string{}
	for _, chunk := range chunks {
		found, ok := fakeupstream.Dig(chunk, "choices", 0, "delta", "tool_calls")
		if !ok {
			continue
		}
		for _, item := range found.([]any) {
			entry, _ := item.(map[string]any)
			index, _ := entry["index"].(float64)
			function, _ := entry["function"].(map[string]any)
			if name, _ := function["name"].(string); name != "" {
				names[int(index)] = name
			}
			if value, _ := function["arguments"].(string); value != "" {
				arguments[int(index)] += value
			}
		}
	}
	if response.StatusCode != http.StatusOK || !done || len(names) != 2 ||
		names[0] != "lookup_note" || names[1] != "lookup_note" ||
		arguments[0] != `{"topic":"one"}` || arguments[1] != `{"topic":"two"}` {
		t.Fatalf("stream calls = %d names=%v arguments=%v done=%v", response.StatusCode, names, arguments, done)
	}
}

// A marker naming a tool that is not offered is still ignored.
func TestSeveralMarkersSkipUnavailableTools(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	marker := ToolMarkerFor("other", "") + " " + ToolMarkerFor("lookup_note", `{"topic":"kept"}`)
	_, body := f.chat(map[string]any{"model": "m", "messages": user(marker), "tools": lookupTools})
	calls, _ := dig(t, body, "choices", 0, "message", "tool_calls").([]any)
	if len(calls) != 1 || dig(t, calls, 0, "function", "name") != "lookup_note" {
		t.Fatalf("calls = %v", body)
	}
}

// A streaming request whose last message carries StreamErrorMarker answers 200
// and then ends the started stream with an error frame instead of a completion
// chunk, without [DONE] (the official 200 description allows an interrupted
// stream without the marker).
func TestStreamedErrorFrame(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	response, raw := f.send("POST", "/chat/completions", "k", map[string]any{
		"model": "deepseek", "messages": user("hi " + StreamErrorMarker), "stream": true,
	})
	chunks, done := sseEvents(t, raw)
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") || done {
		t.Fatalf("%d %q done=%t", response.StatusCode, response.Header.Get("Content-Type"), done)
	}
	if len(chunks) != 2 || dig(t, chunks[0], "choices", 0, "delta", "role") != "assistant" {
		t.Fatalf("opening chunks: %v", chunks)
	}
	if dig(t, chunks[1], "error", "type") != "service_unavailable_error" || dig(t, chunks[1], "error", "code") != "server_is_overloaded" {
		t.Fatalf("error frame: %v", chunks[1])
	}
	// Without stream=true the marker changes nothing.
	status, body := f.chat(map[string]any{"model": "m", "messages": user("hi " + StreamErrorMarker)})
	if status != http.StatusOK || dig(t, body, "choices", 0, "message", "content") != FixedReply {
		t.Fatalf("non-stream: %d %v", status, body)
	}
}

func TestFaultScenarios(t *testing.T) {
	short := fakeupstream.Config{TimeoutHold: 50 * time.Millisecond}
	chat := func(f *fixture, scenario string) (*http.Response, map[string]any) {
		response, raw := f.send("POST", "/chat/completions", "test-key", map[string]any{"model": "m", "messages": user("x")}, "X-Fake-Scenario", scenario)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		return response, body
	}
	t.Run("insufficient balance", func(t *testing.T) {
		response, body := chat(newFixture(t, short), "insufficient-balance")
		if response.StatusCode != http.StatusTooManyRequests || dig(t, body, "error", "code") != "insufficient_quota" || dig(t, body, "error", "type") != "insufficient_quota" {
			t.Fatalf("%d %v", response.StatusCode, body)
		}
	})
	t.Run("key quota", func(t *testing.T) {
		response, body := chat(newFixture(t, short), "key-quota")
		if response.StatusCode != http.StatusTooManyRequests || dig(t, body, "error", "code") != "quota_exceeded" || dig(t, body, "error", "type") != "insufficient_quota" {
			t.Fatalf("%d %v", response.StatusCode, body)
		}
	})
	t.Run("quota exceeded carries the gateway quota object", func(t *testing.T) {
		response, body := chat(newFixture(t, short), "quota-exceeded")
		if response.StatusCode != http.StatusTooManyRequests || dig(t, body, "error", "code") != "quota_exceeded" ||
			dig(t, body, "error", "type") != "quota_exceeded" || dig(t, body, "error", "quota", "dimension") != "weekly" {
			t.Fatalf("%d %v", response.StatusCode, body)
		}
	})
	t.Run("rate limit has Retry-After", func(t *testing.T) {
		response, body := chat(newFixture(t, short), "rate-limit")
		if response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") != "1" || dig(t, body, "error", "code") != "rate_limit_exceeded" {
			t.Fatalf("%d %v", response.StatusCode, body)
		}
	})
	t.Run("server error", func(t *testing.T) {
		response, body := chat(newFixture(t, short), "server-error")
		if response.StatusCode != http.StatusInternalServerError || dig(t, body, "error", "type") != "server_error" {
			t.Fatalf("%d %v", response.StatusCode, body)
		}
	})
	t.Run("invalid key answers the official 401 ErrorResponse", func(t *testing.T) {
		f := newFixture(t, short)
		response, body := chat(f, "invalid-key")
		if response.StatusCode != http.StatusUnauthorized || dig(t, body, "error", "code") != "invalid_api_key" ||
			dig(t, body, "error", "type") != "invalid_request_error" {
			t.Fatalf("%d %v", response.StatusCode, body)
		}
		// A key prefix selects it too, and the read route answers alike.
		response, raw := f.send("GET", "/models", "fake-invalid-key-1", nil)
		var models map[string]any
		_ = json.Unmarshal(raw, &models)
		if response.StatusCode != http.StatusUnauthorized || dig(t, models, "error", "code") != "invalid_api_key" {
			t.Fatalf("models: %d %s", response.StatusCode, raw)
		}
	})
	t.Run("forbidden is the official text/plain JSON error text", func(t *testing.T) {
		response, body := chat(newFixture(t, short), "forbidden")
		if response.StatusCode != http.StatusForbidden || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/plain") ||
			dig(t, body, "error", "type") != "invalid_request_error" || dig(t, body, "error", "param") != nil {
			t.Fatalf("%d %q %v", response.StatusCode, response.Header.Get("Content-Type"), body)
		}
	})
	t.Run("model not found names the model", func(t *testing.T) {
		f := newFixture(t, short)
		response, raw := f.send("POST", "/chat/completions", "test-key", map[string]any{"model": "no-such-model", "messages": user("x")}, "X-Fake-Scenario", "model-not-found")
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if response.StatusCode != http.StatusNotFound || dig(t, body, "error", "code") != "model_not_found" ||
			dig(t, body, "error", "param") != "model" || !strings.Contains(dig(t, body, "error", "message").(string), "no-such-model") {
			t.Fatalf("%d %v", response.StatusCode, body)
		}
	})
	t.Run("service unavailable carries the official body and Retry-After", func(t *testing.T) {
		response, body := chat(newFixture(t, short), "service-unavailable")
		if response.StatusCode != http.StatusServiceUnavailable || response.Header.Get("Retry-After") != "1" ||
			dig(t, body, "error", "type") != "service_unavailable_error" || dig(t, body, "error", "code") != "server_is_overloaded" {
			t.Fatalf("%d %v", response.StatusCode, body)
		}
	})
	t.Run("timeout drops the connection after the hold", func(t *testing.T) {
		f := newFixture(t, short)
		start := time.Now()
		request, _ := http.NewRequest("POST", f.server.URL+Prefix+"/chat/completions", strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"x"}]}`))
		request.Header.Set("Authorization", "Bearer k")
		request.Header.Set("X-Fake-Scenario", "timeout")
		if response, err := http.DefaultClient.Do(request); err == nil {
			response.Body.Close()
			t.Fatalf("got a response: %d", response.StatusCode)
		}
		if time.Since(start) < 40*time.Millisecond {
			t.Fatal("dropped before the hold")
		}
	})
	t.Run("lost response", func(t *testing.T) {
		f := newFixture(t, short)
		request, _ := http.NewRequest("POST", f.server.URL+Prefix+"/chat/completions", strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"x"}]}`))
		request.Header.Set("Authorization", "Bearer k")
		request.Header.Set("X-Fake-Scenario", "lost-response")
		if response, err := http.DefaultClient.Do(request); err == nil {
			response.Body.Close()
			t.Fatalf("got a response: %d", response.StatusCode)
		}
	})
	t.Run("key prefix selects the scenario", func(t *testing.T) {
		f := newFixture(t, short)
		response, _ := f.send("POST", "/chat/completions", "fake-rate-limit-1", map[string]any{"model": "m", "messages": user("x")})
		if response.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("status = %d", response.StatusCode)
		}
	})
	t.Run("task scenarios do not touch chat", func(t *testing.T) {
		response, _ := chat(newFixture(t, short), "task-failed")
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", response.StatusCode)
		}
	})
}

// The committed alignment report must show this version aligned.
func TestAlignmentReportIsCommittedAndClean(t *testing.T) {
	raw, err := os.ReadFile("testdata/alignment-report.json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Official  string `json:"official_base"`
		CheckedAt string `json:"checked_at"`
		Requests  int    `json:"official_requests"`
		Diffs     []struct {
			Note string `json:"note"`
		} `json:"diffs"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.Official != OfficialBase || report.CheckedAt == "" || report.Requests == 0 {
		t.Fatalf("report header incomplete: %+v", report)
	}
	for _, diff := range report.Diffs {
		if strings.TrimSpace(diff.Note) == "" {
			t.Fatal("report holds an unexplained difference")
		}
	}
}

// #247: the fake records what each chat request carried, so the fake stack can
// assert details without reading picture bytes.
func TestChatRequestsAreRecordedForTheVisionBudget(t *testing.T) {
	fake, err := fakeupstream.New(fakeupstream.Config{}, Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	defer server.Close()
	body := map[string]any{
		"model": "fake-vision-model",
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "text", "text": "look at these"},
				map[string]any{"type": "image_url", "image_url": map[string]any{
					"url": "data:image/png;base64,AAAA", "detail": "high",
				}},
				map[string]any{"type": "image_url", "image_url": map[string]any{
					"url": "data:image/png;base64,BBBB", "detail": "low",
				}},
			},
		}},
	}
	raw, _ := json.Marshal(body)
	request, err := http.NewRequest(http.MethodPost, server.URL+Prefix+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer fake-test-key")
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()

	records := fake.Env().ChatRequests()
	if len(records) != 1 || records[0].Model != "fake-vision-model" || len(records[0].Messages) != 1 {
		t.Fatalf("records = %+v", records)
	}
	message := records[0].Messages[0]
	if message.Role != "user" || !strings.Contains(message.Text, "look at these") || len(message.Images) != 2 {
		t.Fatalf("message record = %+v", message)
	}
	if message.Images[0].Detail != "high" || message.Images[0].Detail == message.Images[1].Detail ||
		!message.Images[0].DataURI || message.Images[0].Bytes != 3 {
		t.Fatalf("image records = %+v", message.Images)
	}

	// The control route is readable and clearable outside every version prefix.
	control, err := http.Get(server.URL + fakeupstream.ChatRequestsPath)
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Requests []fakeupstream.ChatRequestRecord `json:"requests"`
	}
	if err := json.NewDecoder(control.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	_ = control.Body.Close()
	if len(listed.Requests) != 1 {
		t.Fatalf("control requests = %+v", listed.Requests)
	}
	clear, err := http.NewRequest(http.MethodDelete, server.URL+fakeupstream.ChatRequestsPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := http.DefaultClient.Do(clear)
	if err != nil {
		t.Fatal(err)
	}
	_ = deleted.Body.Close()
	if deleted.StatusCode != http.StatusNoContent || len(fake.Env().ChatRequests()) != 0 {
		t.Fatalf("clear: %d, records=%+v", deleted.StatusCode, fake.Env().ChatRequests())
	}
}

// #251: nested steps/contingencies/parameters must survive the marker parser;
// the historical single-level and multi-marker forms keep their meaning.
func TestToolMarkerParsesNestedJSON(t *testing.T) {
	nested := `{"source_task_id":"task-1","steps":[{"capability":"remesh","parameters":{"target_polycount":20000,"nested":{"a":[1,2]}}}]}`
	markers := parseToolMarkers("do it [[fake:tool:plan_model_processing " + nested +
		"]] then [[fake:tool:get_processing_plan {\"plan_id\":\"p1\"}]]")
	if len(markers) != 2 {
		t.Fatalf("markers = %+v", markers)
	}
	if markers[0].name != "plan_model_processing" || markers[0].arguments != nested {
		t.Fatalf("nested arguments = %q", markers[0].arguments)
	}
	if markers[1].name != "get_processing_plan" || markers[1].arguments != `{"plan_id":"p1"}` {
		t.Fatalf("second marker = %+v", markers[1])
	}
	// A brace inside a string does not end the object.
	quoted := `{"prompt":"close } and { braces"}`
	markers = parseToolMarkers("[[fake:tool:process_model " + quoted + "]]")
	if len(markers) != 1 || markers[0].arguments != quoted {
		t.Fatalf("quoted braces = %+v", markers)
	}
	// A malformed object falls back to empty arguments, not a lost marker.
	markers = parseToolMarkers(`[[fake:tool:no_args]] [[fake:tool:bad {"x":}]]`)
	if len(markers) != 2 || markers[0].name != "no_args" || markers[0].arguments != "" ||
		markers[1].name != "bad" || markers[1].arguments != `{"x":}` {
		t.Fatalf("fallback markers = %+v", markers)
	}
}

func TestModelFixtureEndpointServesAGLB(t *testing.T) {
	fake, err := fakeupstream.New(fakeupstream.Config{}, Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	defer server.Close()
	response, err := http.Get(server.URL + fakeupstream.ModelFixturePath)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "model/gltf-binary" {
		t.Fatalf("fixture response = %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	raw, _ := io.ReadAll(response.Body)
	if len(raw) < 12 || string(raw[:4]) != "glTF" {
		t.Fatalf("fixture is not a GLB: %d bytes %q", len(raw), raw[:min(4, len(raw))])
	}
}
