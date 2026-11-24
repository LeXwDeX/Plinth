package openairesponses_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openairesponses"
)

// #382: the Responses Adapter leaves the same one line per model call as the
// Chat Completions one: model, thinking level, duration, first byte, tokens and
// the upstream's request id. Nothing of the request's own content is logged.
func TestAdapterLogsOneLinePerStreamedCall(t *testing.T) {
	var buffer strings.Builder
	logger := slog.New(slog.NewTextHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug}))
	body := frames(
		"response.created", `{"type":"response.created","response":{"id":"resp-1"}}`,
		"response.output_text.delta", `{"type":"response.output_text.delta","delta":"painted a cat"}`,
		"response.completed", `{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":9,"output_tokens":2}}}`,
	)
	adapter, _ := scriptedConfigured(t, 200, "text/event-stream", body, func(config *openairesponses.Config) {
		config.Logger = logger
	})
	if _, err := adapter.Stream(context.Background(), agents.Request{
		Model:           "gpt-astra",
		Connection:      "chat-llm",
		ReasoningEffort: "low",
		Messages: []agents.Message{
			{Role: "user", Content: "secret prompt text"},
		},
	}, func(agents.Delta) error { return nil }); err != nil {
		t.Fatal(err)
	}
	logged := buffer.String()
	for _, field := range []string{
		"model=gpt-astra", "connection=chat-llm", "effort=low", "stream=true",
		"elapsed_ms=", "first_byte_ms=", "prompt_tokens=9", "completion_tokens=2",
		"request_bytes=",
	} {
		if !strings.Contains(logged, field) {
			t.Fatalf("log line misses %q: %s", field, logged)
		}
	}
	for _, secret := range []string{"secret prompt text", "painted a cat", "test-key"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("log line carries %q: %s", secret, logged)
		}
	}
}

// A model whose profile offers no level logs no effort, and a call without a
// logger writes nothing at all.
func TestAdapterLogsNoEffortForAModelWithoutLevels(t *testing.T) {
	var buffer strings.Builder
	logger := slog.New(slog.NewTextHandler(&buffer, nil))
	body := frames(
		"response.created", `{"type":"response.created"}`,
		"response.output_text.delta", `{"type":"response.output_text.delta","delta":"hi"}`,
		"response.completed", `{"type":"response.completed","response":{"status":"completed"}}`,
	)
	adapter, _ := scriptedConfigured(t, 200, "text/event-stream", body, func(config *openairesponses.Config) {
		config.OptionsFor = nil
		config.Logger = logger
	})
	if _, err := adapter.Stream(context.Background(), agents.Request{
		Model: "plain-model", Messages: []agents.Message{{Role: "user", Content: "hi"}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buffer.String(), "effort=") {
		t.Fatalf("a model without levels logged an effort: %s", buffer.String())
	}
	silent, _ := scriptedConfigured(t, 200, "text/event-stream", body, nil)
	if _, err := silent.Stream(context.Background(), agents.Request{
		Model: "plain-model", Messages: []agents.Message{{Role: "user", Content: "hi"}},
	}, nil); err != nil {
		t.Fatal(err)
	}
}

// A failed call is a WARN line with its status, a request id (the fake sends
// none, so the local sequence number), the request's size and pictures, and
// the sanitized head of the upstream's answer.
func TestAdapterLogsAFailedCallWithStatusRequestIDAndExcerpt(t *testing.T) {
	var buffer strings.Builder
	logger := slog.New(slog.NewTextHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelInfo}))
	s := newStack(t)
	adapter := s.adapter(func(config *openairesponses.Config) {
		config.APIKey = "fake-server-error"
		config.Logger = logger
	})
	_, err := adapter.Stream(context.Background(), agents.Request{
		Model: "gpt-astra", Connection: "chat-llm",
		Messages: []agents.Message{{Role: "user", Parts: []agents.ContentPart{
			{Kind: "text", Text: "secret prompt text"},
			{Kind: "image", URL: "data:image/png;base64,AAAA"},
		}}},
	}, nil)
	if err == nil {
		t.Fatal("the server error was not reported")
	}
	logged := buffer.String()
	for _, field := range []string{
		"level=WARN", "status=500", "request_id=1", "model=gpt-astra", "connection=chat-llm",
		"elapsed_ms=", "request_bytes=", "images=1", "response_excerpt=",
		"internal server error",
	} {
		if !strings.Contains(logged, field) {
			t.Fatalf("failed call log misses %q: %s", field, logged)
		}
	}
	for _, secret := range []string{"secret prompt text", "AAAA", "fake-server-error"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("failed call log carries %q: %s", secret, logged)
		}
	}
}
