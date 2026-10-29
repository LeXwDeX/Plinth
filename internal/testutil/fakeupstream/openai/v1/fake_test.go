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
		if response.StatusCode != http.StatusPaymentRequired || dig(t, body, "error", "code") != "insufficient_quota" || dig(t, body, "error", "type") != "insufficient_quota" {
			t.Fatalf("%d %v", response.StatusCode, body)
		}
	})
	t.Run("key quota", func(t *testing.T) {
		response, body := chat(newFixture(t, short), "key-quota")
		if response.StatusCode != http.StatusPaymentRequired || dig(t, body, "error", "code") != "key_credit_limit_reached" {
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
