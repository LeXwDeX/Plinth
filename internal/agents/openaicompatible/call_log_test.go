package openaicompatible_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"
)

// #382: every model call leaves one line with its model, thinking level,
// duration, first byte, tokens and the upstream's request id - and nothing of
// the request's own content: no prompt, no picture, no key.
func TestAdapterLogsOneLinePerCallWithNoRequestContent(t *testing.T) {
	var buffer strings.Builder
	logger := slog.New(slog.NewTextHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug}))
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type":   []string{"application/json"},
				"X-Request-Id":   []string{"upstream-7"},
				"Content-Length": []string{"2"},
			},
			Body: io.NopCloser(strings.NewReader(
				`{"choices":[{"message":{"content":"painted a cat"}}],` +
					`"usage":{"prompt_tokens":11,"completion_tokens":3}}`,
			)),
		}, nil
	})
	adapter, err := openaicompatible.New(openaicompatible.Config{
		BaseURL:           "https://configured-agent.test/v1/",
		APIKey:            "configured-secret",
		EndpointPath:      "/chat/completions",
		AuthorizationName: "Configured-Authorization",
		AuthorizationType: "Configured",
		RequestTimeout:    time.Minute,
		ScannerMaxBytes:   1 << 20,
		ErrorBodyMaxBytes: 1 << 16,
		// A model profile that offers the "low" level, so the request really
		// carries it on the wire and the log may state it. A profile-less model
		// sends no effort and the line omits it, as the openairesponses sibling
		// test asserts; without this the fixture would log no effort at all.
		Thinking: &openaicompatible.ThinkingOptions{
			EffortField:   "reasoning_effort",
			EffortLevels:  []string{"low", "medium", "high"},
			DefaultEffort: "medium",
		},
		Client: &http.Client{Transport: transport},
		Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Complete(context.Background(), agents.Request{
		Model:           "configured-model",
		Connection:      "chat-llm",
		ReasoningEffort: "low",
		Messages: []agents.Message{
			{Role: "user", Parts: []agents.ContentPart{
				{Kind: "text", Text: "secret prompt text"},
				{Kind: "image", URL: "data:image/png;base64,AAAA"},
			}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	logged := buffer.String()
	for _, field := range []string{
		"model=configured-model", "connection=chat-llm", "effort=low",
		"stream=false", "elapsed_ms=", "first_byte_ms=",
		"prompt_tokens=11", "completion_tokens=3", "request_id=upstream-7",
		"request_bytes=",
	} {
		if !strings.Contains(logged, field) {
			t.Fatalf("log line misses %q: %s", field, logged)
		}
	}
	for _, secret := range []string{"secret prompt text", "painted a cat", "configured-secret", "AAAA"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("log line carries %q: %s", secret, logged)
		}
	}
}

// A refusal is logged too, as the same line with its error.
func TestAdapterLogsARefusedCall(t *testing.T) {
	var buffer strings.Builder
	logger := slog.New(slog.NewTextHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelInfo}))
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"temperature is not supported"}}`)),
		}, nil
	})
	adapter, err := openaicompatible.New(openaicompatible.Config{
		BaseURL: "https://configured-agent.test/v1/", APIKey: "k",
		EndpointPath: "/chat/completions", AuthorizationName: "Authorization",
		AuthorizationType: "Bearer", RequestTimeout: time.Minute,
		ScannerMaxBytes: 1 << 20, ErrorBodyMaxBytes: 1 << 16,
		Client: &http.Client{Transport: transport}, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Complete(context.Background(), agents.Request{
		Model: "configured-model", Messages: []agents.Message{{Role: "user", Content: "hi"}},
	}); err == nil {
		t.Fatal("the refusal was not reported")
	}
	logged := buffer.String()
	if !strings.Contains(logged, "level=WARN") || !strings.Contains(logged, "elapsed_ms=") {
		t.Fatalf("refused call log = %s", logged)
	}
}

// marshalRequest is what the size in the log counts; the log itself never
// carries the body.
func TestAdapterLogIsJSON(t *testing.T) {
	var buffer strings.Builder
	logger := slog.New(slog.NewJSONHandler(&buffer, nil))
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)),
		}, nil
	})
	adapter, err := openaicompatible.New(openaicompatible.Config{
		BaseURL: "https://configured-agent.test/v1/", APIKey: "k",
		EndpointPath: "/chat/completions", AuthorizationName: "Authorization",
		AuthorizationType: "Bearer", RequestTimeout: time.Minute,
		ScannerMaxBytes: 1 << 20, ErrorBodyMaxBytes: 1 << 16,
		Client: &http.Client{Transport: transport}, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Complete(context.Background(), agents.Request{
		Model: "configured-model", Messages: []agents.Message{{Role: "user", Content: "hi"}},
	}); err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(buffer.String()), &record); err != nil {
		t.Fatalf("log line is not one JSON object: %v (%s)", err, buffer.String())
	}
	if record["model"] != "configured-model" {
		t.Fatalf("record = %v", record)
	}
}
