package openaicompatible_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"
)

// streamDeltas runs one stream over the given SSE body and returns the deltas
// the caller saw, rendered as "thinking", "tool_calling" or "text:<content>".
func streamDeltas(t *testing.T, thinking *openaicompatible.ThinkingOptions, body string) ([]string, agents.Completion) {
	t.Helper()
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	adapter, err := openaicompatible.New(openaicompatible.Config{
		BaseURL:           "https://configured-agent.test/v1/",
		EndpointPath:      "/chat/completions",
		AuthorizationName: "Configured-Authorization",
		AuthorizationType: "Configured",
		RequestTimeout:    time.Minute,
		ScannerMaxBytes:   1 << 20,
		ErrorBodyMaxBytes: 1 << 16,
		Client:            &http.Client{Transport: transport},
		Thinking:          thinking,
	})
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	completion, err := adapter.Stream(context.Background(), agents.Request{
		Model:    "configured-model",
		Messages: []agents.Message{{Role: "configured-user", Content: "hello"}},
	}, func(delta agents.Delta) error {
		if (delta.Thinking || delta.ToolCalling) && delta.Content != "" {
			t.Fatalf("signal delta carries content %q", delta.Content)
		}
		if delta.Thinking {
			seen = append(seen, "thinking")
		}
		if delta.ToolCalling {
			seen = append(seen, "tool_calling")
		}
		if delta.Content != "" {
			seen = append(seen, "text:"+delta.Content)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return seen, completion
}

func reasoningChunk(text string) string {
	encoded, _ := json.Marshal(text)
	return fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"reasoning_content\":%s}}]}\n\n", encoded)
}

func contentChunk(text string) string {
	encoded, _ := json.Marshal(text)
	return fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"content\":%s}}]}\n\n", encoded)
}

func toolCallChunk(index int, id, name, arguments string) string {
	encoded, _ := json.Marshal(arguments)
	return fmt.Sprintf(
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":%d,\"id\":%q,\"function\":{\"name\":%q,\"arguments\":%s}}]}}]}\n\n",
		index, id, name, encoded,
	)
}

// TestAdapterReportsReasoningAsContentFreeThinkingSignal pins #285: a stretch
// of reasoning fragments yields one Thinking delta and no reasoning text;
// reasoning after visible content yields another one. This holds with or
// without a thinking profile (passback only decides what the completion keeps).
func TestAdapterReportsReasoningAsContentFreeThinkingSignal(t *testing.T) {
	body := reasoningChunk("secret step one") +
		reasoningChunk("secret step two") +
		contentChunk("hello ") +
		reasoningChunk("secret step three") +
		contentChunk("world") +
		"data: [DONE]\n\n"
	for name, thinking := range map[string]*openaicompatible.ThinkingOptions{
		"no profile": nil,
		"passback":   {ReasoningPassback: true},
	} {
		t.Run(name, func(t *testing.T) {
			seen, completion := streamDeltas(t, thinking, body)
			want := "thinking|text:hello |thinking|text:world"
			if strings.Join(seen, "|") != want {
				t.Fatalf("deltas = %v, want %s", seen, want)
			}
			if strings.Contains(strings.Join(seen, ""), "secret") || completion.Content != "hello world" {
				t.Fatalf("reasoning leaked: deltas=%v completion=%#v", seen, completion)
			}
			if thinking == nil && completion.ReasoningContent != "" {
				t.Fatalf("reasoning kept without passback: %q", completion.ReasoningContent)
			}
		})
	}
}

// TestAdapterSendsNoThinkingSignalWithoutReasoning pins that a model that
// sends no reasoning streams only its text.
func TestAdapterSendsNoThinkingSignalWithoutReasoning(t *testing.T) {
	seen, _ := streamDeltas(t, nil, contentChunk("hello")+"data: [DONE]\n\n")
	if strings.Join(seen, "|") != "text:hello" {
		t.Fatalf("deltas = %v", seen)
	}
}

// TestAdapterReportsTheFirstToolCallFragmentAsContentFreeSignal pins #375: the
// model writes a tool call's arguments for a long time before the call can run,
// so the first call fragment of a completion yields one content-free
// ToolCalling delta, however many fragments or calls follow. The signal comes
// after earlier reasoning and text, never repeats, and leaves the assembled
// calls untouched.
func TestAdapterReportsTheFirstToolCallFragmentAsContentFreeSignal(t *testing.T) {
	body := reasoningChunk("secret step") +
		contentChunk("plan ") +
		toolCallChunk(0, "call-a", "configured_tool", `{"prompt":`) +
		toolCallChunk(0, "", "", `"paint"}`) +
		toolCallChunk(1, "call-b", "configured_tool", `{}`) +
		"data: [DONE]\n\n"
	seen, completion := streamDeltas(t, nil, body)
	want := "thinking|text:plan |tool_calling"
	if strings.Join(seen, "|") != want {
		t.Fatalf("deltas = %v, want %s", seen, want)
	}
	if len(completion.ToolCalls) != 2 ||
		completion.ToolCalls[0].ID != "call-a" ||
		string(completion.ToolCalls[0].Arguments) != `{"prompt":"paint"}` ||
		completion.ToolCalls[1].ID != "call-b" {
		t.Fatalf("tool calls = %#v", completion.ToolCalls)
	}
}

// TestAdapterSignalsAToolCallOnlyStream pins the case behind #375: a model
// that streams nothing but a tool call still signals it once.
func TestAdapterSignalsAToolCallOnlyStream(t *testing.T) {
	seen, completion := streamDeltas(t, nil,
		toolCallChunk(0, "call-a", "configured_tool", `{}`)+"data: [DONE]\n\n")
	if strings.Join(seen, "|") != "tool_calling" || len(completion.ToolCalls) != 1 {
		t.Fatalf("deltas = %v, calls = %#v", seen, completion.ToolCalls)
	}
}
