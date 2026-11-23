package openaiv1

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #401: the two bodies below are the gateway's observed refusals of
// 2026-11-20 (byte for byte, apart from the trailing newline the fake adds).
const (
	observedToolsEffortRefusal = `{"error":{"message":"Function tools with reasoning_effort are not supported for gpt-6.2-astra in /v1/chat/completions. To use function tools, use /v1/responses or set reasoning_effort to 'none'.","type":"invalid_request_error","param":"reasoning_effort","code":null}}`
	observedTemperatureRefusal = `{"error":{"message":"Unsupported value: 'temperature' does not support 0 with this model. Only the default (1) value is supported.","type":"invalid_request_error","param":"temperature","code":"unsupported_value"}}`
	// Constructed from the OpenAI documents (read 2026-11-20), not observed.
	constructedNoneRefusal = `{"error":{"message":"Unsupported value: 'reasoning_effort' does not support 'none' with this model.","type":"invalid_request_error","param":"reasoning_effort","code":"unsupported_value"}}`
)

func (f *fixture) refuse(body map[string]any) (int, string) {
	f.t.Helper()
	response, raw := f.send("POST", "/chat/completions", "test-key", body)
	if response.StatusCode == http.StatusBadRequest && !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		f.t.Fatalf("a refusal is JSON, not %q", response.Header.Get("Content-Type"))
	}
	return response.StatusCode, strings.TrimSpace(string(raw))
}

// Every Agent request of Plinth for gpt-astra carried tools, a thinking level
// and temperature 0, and failed with these exact answers (#401).
func TestAstraRefusesTheRequestShapesTheGatewayRefused(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for _, model := range []string{"gpt-astra", "gpt-6.2-astra", "GPT-Astra"} {
		for name, test := range map[string]struct {
			body map[string]any
			want string
		}{
			// The Agent turn as it was sent: all three at once. The tools
			// refusal is the observed answer to it.
			"agent turn": {map[string]any{"messages": user("hi"), "tools": lookupTools,
				"reasoning_effort": "high", "temperature": 0}, observedToolsEffortRefusal},
			"tools with a level": {map[string]any{"messages": user("hi"), "tools": lookupTools,
				"reasoning_effort": "low"}, observedToolsEffortRefusal},
			// The title request as it was sent: a level, no tools, temperature 0.
			"title request": {map[string]any{"messages": user("hi"), "reasoning_effort": "high",
				"temperature": 0}, observedTemperatureRefusal},
			"temperature without a level": {map[string]any{"messages": user("hi"), "temperature": 0}, observedTemperatureRefusal},
			// The documents say none is rejected: tools cannot be rescued by it.
			"none": {map[string]any{"messages": user("hi"), "reasoning_effort": "none"}, constructedNoneRefusal},
			"none with tools": {map[string]any{"messages": user("hi"), "tools": lookupTools,
				"reasoning_effort": "none"}, constructedNoneRefusal},
			// The model's own default effort is not none, so tools still fail.
			"tools without a level": {map[string]any{"messages": user("hi"), "tools": lookupTools}, observedToolsEffortRefusal},
		} {
			body := map[string]any{"model": model}
			for key, value := range test.body {
				body[key] = value
			}
			status, raw := f.refuse(body)
			if status != http.StatusBadRequest || raw != test.want {
				t.Errorf("%s / %s: %d %s\nwant 400 %s", model, name, status, raw, test.want)
			}
		}
	}
}

// What the model accepts: no temperature (or its default 1) and a level, with
// no tools; streamed or not.
func TestAstraAcceptsTheRequestShapeThatOmitsWhatItRefuses(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for _, model := range []string{"gpt-astra", "gpt-6.2-astra"} {
		for name, extra := range map[string]map[string]any{
			"a level":                {"reasoning_effort": "high"},
			"no level":               {},
			"the default":            {"temperature": 1, "reasoning_effort": "max"},
			"the default as a float": {"temperature": 1.0},
			"a null temperature":     {"temperature": nil},
		} {
			for _, stream := range []bool{false, true} {
				body := map[string]any{"model": model, "messages": user("hi"), "stream": stream}
				for key, value := range extra {
					body[key] = value
				}
				response, raw := f.send("POST", "/chat/completions", "test-key", body)
				if response.StatusCode != http.StatusOK || !strings.Contains(string(raw), FixedReply) {
					t.Errorf("%s / %s / stream=%t: %d %s", model, name, stream, response.StatusCode, raw)
				}
			}
		}
	}
}

// The refusal names the temperature as the request wrote it.
func TestAstraTemperatureRefusalNamesTheValueSent(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, raw := f.refuse(map[string]any{"model": "gpt-astra", "messages": user("hi"), "temperature": 0.7})
	if status != http.StatusBadRequest || !strings.Contains(raw, "'temperature' does not support 0.7 with this model") ||
		!strings.Contains(raw, `"param":"temperature"`) {
		t.Fatalf("%d %s", status, raw)
	}
}

// Another model keeps the unconstrained fake: tools, a level and temperature 0
// together are served, so only a model with declared rules can fail on them.
func TestOtherModelsAreNotConstrained(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	marker := ToolMarkerFor("lookup_note", `{"topic":"cube"}`)
	for _, model := range []string{"deepseek", "gpt-6-astra", "gpt-6.1-sol", "qwen-max", "m"} {
		status, body := f.chat(map[string]any{"model": model, "messages": user(marker), "tools": lookupTools,
			"reasoning_effort": "high", "temperature": 0})
		if status != http.StatusOK || dig(t, body, "choices", 0, "finish_reason") != "tool_calls" {
			t.Errorf("%s: %d %v", model, status, body)
		}
		status, _ = f.chat(map[string]any{"model": model, "messages": user("hi"), "reasoning_effort": "none"})
		if status != http.StatusOK {
			t.Errorf("%s with none: %d", model, status)
		}
	}
}

// The refusal does not replace the other validations: a missing model or
// messages keeps its own answer, and an unknown-model scenario is judged first.
func TestConstraintRefusalComesAfterTheRequestValidation(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, response := f.chat(map[string]any{"model": "gpt-astra", "temperature": 0})
	if status != http.StatusBadRequest || dig(t, response, "error", "param") != "messages" {
		t.Fatalf("no messages: %d %v", status, response)
	}
	status, response = f.chat(map[string]any{"model": "gpt-astra", "messages": user("x"), "temperature": 0}, "X-Fake-Scenario", "model-not-found")
	if status != http.StatusNotFound || dig(t, response, "error", "code") != "model_not_found" {
		t.Fatalf("model-not-found scenario: %d %v", status, response)
	}
}

// #377: the thinking mode of DeepSeek requires the reasoning_content of an
// assistant message that calls tools back in the next round of the same tool
// loop; a request that drops the field is refused, and one that carries it as
// the empty string (a round that produced no reasoning at all) is served.
func TestDeepSeekRequiresTheReasoningOfAToolCallMessage(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	call := map[string]any{"role": "assistant", "content": "",
		"tool_calls": []any{map[string]any{"id": "call-a", "type": "function",
			"function": map[string]any{"name": "lookup_note", "arguments": "{}"}}}}
	without := []any{user("go"), call}
	with := []any{user("go"), map[string]any{"role": "assistant", "content": "", "reasoning_content": "",
		"tool_calls": call["tool_calls"]}, map[string]any{"role": "tool", "tool_call_id": "call-a", "content": "[]"}}
	status, raw := f.refuse(map[string]any{"model": "deepseek", "messages": without, "tools": lookupTools})
	if status != http.StatusBadRequest || !strings.Contains(raw, `"param":"messages"`) ||
		!strings.Contains(raw, "reasoning_content") {
		t.Fatalf("a tool-call message without reasoning: %d %s", status, raw)
	}
	// The empty string the thinking mode sends for a round without reasoning is
	// a value, not a missing field: the same request is served.
	response, body := f.send("POST", "/chat/completions", "test-key",
		map[string]any{"model": "deepseek", "messages": with, "tools": lookupTools})
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), FixedReply) {
		t.Errorf("with an empty reasoning: %d %s", response.StatusCode, body)
	}
	// A request without an assistant tool-call message is not judged by the
	// rule, whatever else it carries.
	response, body = f.send("POST", "/chat/completions", "test-key",
		map[string]any{"model": "deepseek", "messages": user("hi"), "tools": lookupTools})
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), FixedReply) {
		t.Errorf("a first round: %d %s", response.StatusCode, body)
	}
	// Another model keeps the field optional: only a declared rule refuses.
	response, body = f.send("POST", "/chat/completions", "test-key",
		map[string]any{"model": "gpt-6", "messages": without, "tools": lookupTools})
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), FixedReply) {
		t.Errorf("another model: %d %s", response.StatusCode, body)
	}
}
