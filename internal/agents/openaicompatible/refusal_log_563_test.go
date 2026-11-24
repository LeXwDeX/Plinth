package openaicompatible_test

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	openaiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/v1"
)

// #563: the retry loop logs the refusal's error as it is; a gateway 502 that
// echoes the prompt after a long filler (the shared fake's GatewayFaultMarker)
// must leave neither the prompt nor the raw body in the retry line, nor in the
// error the turn receives at the end.
func TestRetryLogHoldsNoRawRefusalBody(t *testing.T) {
	var buffer strings.Builder
	logger := slog.New(slog.NewTextHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelInfo}))
	fake, err := fakeupstream.New(fakeupstream.Config{}, openaiv1.Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	adapter, err := openaicompatible.New(openaicompatible.Config{
		BaseURL: server.URL + openaiv1.Prefix, APIKey: "k",
		EndpointPath: "/chat/completions", AuthorizationName: "Authorization",
		AuthorizationType: "Bearer", RequestTimeout: time.Minute,
		ScannerMaxBytes: 1 << 20, ErrorBodyMaxBytes: 1 << 16,
		Client: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	retrying := agents.NewRetrying(adapter, agents.RetryPolicy{
		MaxAttempts: 2, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
		MaxRetryAfter: time.Minute, Logger: logger,
		Wait: func(context.Context, time.Duration) error { return nil },
	})
	_, err = retrying.Stream(context.Background(), agents.Request{
		Model: "configured-model", Connection: "chat-llm",
		Messages: []agents.Message{{Role: "user", Parts: []agents.ContentPart{
			{Kind: "text", Text: "secret prompt text " + openaiv1.GatewayFaultMarker},
		}}},
	}, func(agents.Delta) error { return nil })
	if err == nil {
		t.Fatal("the 502 was not reported")
	}
	logged := buffer.String()
	if !strings.Contains(logged, "Bad Gateway") {
		t.Fatalf("the retry line lost the upstream's wording: %s", logged)
	}
	for _, text := range []string{logged, err.Error()} {
		for _, secret := range []string{"secret prompt text", strings.Repeat("x", 201)} {
			if strings.Contains(text, secret) {
				t.Fatalf("carries %q: %s", secret[:min(len(secret), 40)], text)
			}
		}
	}
}
