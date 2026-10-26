package openaicompatible_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"
)

// #322 wire contract. Expected values come from the vendors' documents, not
// from the Schema under test:
//   - DeepSeek https://api-docs.deepseek.com/api/create-chat-completion
//     (2026-10-22): reasoning_effort is a top-level string (none/low/high/max);
//     stream_options.include_usage is only valid with stream=true (400
//     otherwise); the last chunk before [DONE] carries usage with
//     prompt_tokens and completion_tokens, and the chunk may still carry one
//     choice with no new content.
//   - Qwen (OpenAI compatible) documents include_usage false by default and
//     an empty choices array on the usage chunk.

type capturedRequest struct {
	body map[string]any
}

func effortAdapter(t *testing.T, reply func(stream bool) string, thinkingFor func(string) *openaicompatible.ThinkingOptions, include bool) (*openaicompatible.Adapter, *[]capturedRequest) {
	t.Helper()
	var captured []capturedRequest
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return nil, err
		}
		captured = append(captured, capturedRequest{body: body})
		stream, _ := body["stream"].(bool)
		contentType := "application/json"
		if stream {
			contentType = "text/event-stream"
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{contentType}},
			Body:       io.NopCloser(strings.NewReader(reply(stream))),
		}, nil
	})
	adapter, err := openaicompatible.New(openaicompatible.Config{
		BaseURL:            "https://configured-agent.test/v1/",
		APIKey:             "configured-secret",
		EndpointPath:       "/chat/completions",
		AuthorizationName:  "Authorization",
		AuthorizationType:  "Bearer",
		RequestTimeout:     time.Minute,
		ScannerMaxBytes:    1 << 20,
		ErrorBodyMaxBytes:  1 << 16,
		Client:             &http.Client{Transport: transport},
		ThinkingFor:        thinkingFor,
		StreamIncludeUsage: include,
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter, &captured
}

func deepseekOptions(model string) *openaicompatible.ThinkingOptions {
	if !strings.HasPrefix(strings.ToLower(model), "deepseek") {
		return nil
	}
	return &openaicompatible.ThinkingOptions{
		RequestFields:      map[string]json.RawMessage{"thinking": json.RawMessage(`{"type":"enabled"}`)},
		EffortField:        "reasoning_effort",
		EffortLevels:       []string{"low", "high", "max"},
		DefaultEffort:      "low",
		StreamIncludeUsage: true,
	}
}

const deepseekStream = "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":356000,\"completion_tokens\":120,\"total_tokens\":356120}}\n\n" +
	"data: [DONE]\n\n"

func TestAdapterWritesTheChosenEffortForTheRequestModel(t *testing.T) {
	adapter, captured := effortAdapter(t, func(bool) string { return deepseekStream }, deepseekOptions, false)
	for _, testCase := range []struct {
		model, chosen string
		want          any
	}{
		{"deepseek", "high", "high"},
		{"DeepSeek-Flash", "max", "max"},
		// A level this model does not offer (chosen for another model) falls
		// back to the profile default.
		{"deepseek", "xhigh", "low"},
		{"deepseek", "", "low"},
		// A model without a profile sends no thinking fields at all.
		{"gpt-4o", "high", nil},
	} {
		if _, err := adapter.Stream(context.Background(), agents.Request{
			Model: testCase.model, ReasoningEffort: testCase.chosen,
			Messages: []agents.Message{{Role: "user", Content: "hi"}},
		}, nil); err != nil {
			t.Fatal(err)
		}
		body := (*captured)[len(*captured)-1].body
		if body["reasoning_effort"] != testCase.want {
			t.Fatalf("%s/%s: reasoning_effort = %#v, want %#v", testCase.model, testCase.chosen, body["reasoning_effort"], testCase.want)
		}
		_, thinking := body["thinking"]
		if thinking != (testCase.want != nil) {
			t.Fatalf("%s: thinking field present = %v", testCase.model, thinking)
		}
	}
}

func TestAdapterAsksForStreamUsageOnlyOnStreamedRequests(t *testing.T) {
	reply := func(stream bool) string {
		if stream {
			return deepseekStream
		}
		return `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`
	}
	adapter, captured := effortAdapter(t, reply, deepseekOptions, false)
	completion, err := adapter.Stream(context.Background(), agents.Request{Model: "deepseek", Messages: []agents.Message{{Role: "user", Content: "hi"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	options, _ := (*captured)[0].body["stream_options"].(map[string]any)
	if options["include_usage"] != true {
		t.Fatalf("stream_options = %#v", (*captured)[0].body["stream_options"])
	}
	if completion.Usage == nil || completion.Usage.PromptTokens != 356000 || completion.Usage.CompletionTokens != 120 {
		t.Fatalf("stream usage = %#v", completion.Usage)
	}
	// DeepSeek returns 400 for stream_options without stream: a non-streamed
	// request never carries it, and its usage is read from the body.
	completion, err = adapter.Complete(context.Background(), agents.Request{Model: "deepseek", Messages: []agents.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, present := (*captured)[1].body["stream_options"]; present {
		t.Fatalf("non-streamed request carries stream_options: %#v", (*captured)[1].body)
	}
	if completion.Usage == nil || completion.Usage.PromptTokens != 10 || completion.Usage.CompletionTokens != 2 {
		t.Fatalf("completion usage = %#v", completion.Usage)
	}
	// A model without a profile follows chat.agent.stream_include_usage (off here).
	if _, err := adapter.Stream(context.Background(), agents.Request{Model: "gpt-4o", Messages: []agents.Message{{Role: "user", Content: "hi"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, present := (*captured)[2].body["stream_options"]; present {
		t.Fatalf("profile-less request asked for usage with the agent switch off: %#v", (*captured)[2].body)
	}
}

func TestAdapterReadsUsageFromAnEmptyChoicesChunkAndReportsNoneWhenAbsent(t *testing.T) {
	// The OpenAI/Qwen shape: a final chunk with empty choices carries usage.
	qwen := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}],\"usage\":null}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1234,\"completion_tokens\":56,\"total_tokens\":1290}}\n\n" +
		"data: [DONE]\n\n"
	none := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"
	for name, testCase := range map[string]struct {
		stream string
		want   *agents.Usage
	}{
		"empty choices": {qwen, &agents.Usage{PromptTokens: 1234, CompletionTokens: 56}},
		"absent":        {none, nil},
	} {
		adapter, captured := effortAdapter(t, func(bool) string { return testCase.stream }, nil, true)
		completion, err := adapter.Stream(context.Background(), agents.Request{Model: "qwen-plus", Messages: []agents.Message{{Role: "user", Content: "hi"}}}, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if completion.Content != "hi" {
			t.Fatalf("%s: content = %q", name, completion.Content)
		}
		if (completion.Usage == nil) != (testCase.want == nil) || testCase.want != nil && *completion.Usage != *testCase.want {
			t.Fatalf("%s: usage = %#v, want %#v", name, completion.Usage, testCase.want)
		}
		// The agent-level switch is on and no profile matched.
		options, _ := (*captured)[0].body["stream_options"].(map[string]any)
		if options["include_usage"] != true {
			t.Fatalf("%s: stream_options = %#v", name, (*captured)[0].body["stream_options"])
		}
	}
}

func TestAdapterProfileCanTurnStreamUsageOff(t *testing.T) {
	glm := func(string) *openaicompatible.ThinkingOptions {
		return &openaicompatible.ThinkingOptions{
			RequestFields: map[string]json.RawMessage{"thinking": json.RawMessage(`{"type":"enabled"}`)},
			EffortField:   "reasoning_effort", EffortLevels: []string{"low", "high", "max"}, DefaultEffort: "max",
			StreamIncludeUsage: false,
		}
	}
	adapter, captured := effortAdapter(t, func(bool) string { return deepseekStream }, glm, true)
	completion, err := adapter.Stream(context.Background(), agents.Request{Model: "glm-5.3", Messages: []agents.Message{{Role: "user", Content: "hi"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := (*captured)[0].body
	if _, present := body["stream_options"]; present || body["reasoning_effort"] != "max" {
		t.Fatalf("glm body = %#v", body)
	}
	// Usage the vendor sends anyway is still read.
	if completion.Usage == nil || completion.Usage.PromptTokens != 356000 {
		t.Fatalf("usage = %#v", completion.Usage)
	}
}
