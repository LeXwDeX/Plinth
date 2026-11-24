package openairesponses_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openairesponses"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	openaiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/v1"
)

// #509: the failures the same request may not meet again are transient, as in
// the Chat Completions Adapter: a stream without its terminal event, a dropped
// connection, an error event the upstream sent after accepting the request, a
// plain answer to a streaming request, HTTP 429/5xx and a stuck upstream.
func TestTransientFailuresOfTheResponsesWire(t *testing.T) {
	cut, _ := scripted(t, http.StatusOK, "text/event-stream",
		frames("response.output_text.delta", `{"type":"response.output_text.delta","output_index":0,"delta":"half"}`))
	plain, _ := scripted(t, http.StatusOK, "application/json", `{"status":"completed","output":[]}`)
	s := newStack(t)
	for name, run := range map[string]func() error{
		"cut stream": func() error {
			_, err := cut.Stream(ctx, agents.Request{Model: "m", Messages: userHello}, nil)
			return err
		},
		"not SSE": func() error {
			_, err := plain.Stream(ctx, agents.Request{Model: "m", Messages: userHello}, nil)
			return err
		},
		"dropped": func() error {
			_, err := newAdapter(t, newBrokenStream(t), &recorder{}, nil).Stream(ctx, agents.Request{Model: "m", Messages: userHello}, nil)
			return err
		},
		"error event": func() error {
			_, err := s.adapter(nil).Stream(ctx, agents.Request{Model: "gpt-astra", Messages: []agents.Message{userMessage("go " + openaiv1.StreamErrorMarker)}}, nil)
			return err
		},
	} {
		if err := run(); !errors.Is(err, agents.ErrTransient) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A failed response is the model's own end, not a transient failure.
	_, err := s.adapter(nil).Stream(ctx, agents.Request{Model: "gpt-astra", Messages: []agents.Message{userMessage("go " + openaiv1.FailedMarker)}}, nil)
	if err == nil || errors.Is(err, agents.ErrTransient) {
		t.Errorf("response.failed: %v", err)
	}
	for key, want := range map[string]time.Duration{"fake-rate-limit": time.Second, "fake-server-error": 0} {
		adapter := s.adapter(func(config *openairesponses.Config) { config.APIKey = key })
		_, err := adapter.Stream(ctx, agents.Request{Model: "gpt-astra", Messages: userHello}, nil)
		if retryAfter, transient := agents.TransientRetryAfter(err); !transient || retryAfter != want {
			t.Errorf("%s: transient=%t retry-after=%s %v", key, transient, retryAfter, err)
		}
	}
}

// A stuck upstream ends at the first-byte timeout.
func TestTheResponsesWireHasAFirstByteTimeout(t *testing.T) {
	fake, err := fakeupstream.New(fakeupstream.Config{TimeoutHold: time.Second}, openaiv1.Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	adapter := newAdapter(t, server.URL+openaiv1.Prefix, &recorder{}, func(config *openairesponses.Config) {
		config.APIKey = "fake-timeout"
		config.FirstByteTimeout = 150 * time.Millisecond
	})
	started := time.Now()
	_, err = adapter.Stream(context.Background(), agents.Request{Model: "gpt-astra", Messages: userHello}, nil)
	if elapsed := time.Since(started); elapsed > 800*time.Millisecond || !errors.Is(err, agents.ErrTransient) ||
		!strings.Contains(err.Error(), "first byte") {
		t.Fatalf("stuck call after %s: %v", elapsed, err)
	}
}
