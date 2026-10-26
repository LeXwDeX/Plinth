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
// the caller saw, rendered as "thinking" or "text:<content>".
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
		if delta.Thinking && delta.Content != "" {
			t.Fatalf("thinking delta carries content %q", delta.Content)
		}
		if delta.Thinking {
			seen = append(seen, "thinking")
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
