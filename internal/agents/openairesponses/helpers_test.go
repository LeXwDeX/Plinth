package openairesponses_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openairesponses"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	openaiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/v1"
)

// The Adapter is tested against the fake of the OpenAI-compatible gateway
// (internal/testutil/fakeupstream/openai/v1, whose /v1/responses is aligned to
// the real probe of 2026-11-20): the same stack the application's fake
// deployment runs, with the answers it gives for gpt-astra. A test that needs a
// wire event a faithful fake never sends (an unknown event type, a stream cut
// short, an oversized line other than the two the fake plays since
// formal-test's #638 rounds, fake_observed_1126_test.go) serves it from a
// scripted handler instead (scripted).

// recorder keeps the bytes of every request the Adapter sends.
type recorder struct {
	mu     sync.Mutex
	bodies [][]byte
}

type recordingTransport struct {
	next     http.RoundTripper
	recorder *recorder
}

func (transport recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Body != nil {
		raw, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		_ = request.Body.Close()
		request.Body = io.NopCloser(bytes.NewReader(raw))
		transport.recorder.mu.Lock()
		transport.recorder.bodies = append(transport.recorder.bodies, raw)
		transport.recorder.mu.Unlock()
	}
	return transport.next.RoundTrip(request)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

// raw is the bytes of the n-th last request (0 is the last).
func (r *recorder) raw(back int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.bodies[len(r.bodies)-1-back])
}

func (r *recorder) body(t *testing.T, back int) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(r.raw(back)), &decoded); err != nil {
		t.Fatalf("request is not JSON: %v", err)
	}
	return decoded
}

// astraOptions are the options the shipped Catalog gives gpt-astra (its
// profile: wire_api responses, levels low to max, default high, no
// temperature); any other model has no profile.
func astraOptions(model string) *openairesponses.Options {
	if model == "gpt-astra" {
		return &openairesponses.Options{
			EffortLevels: []string{"low", "medium", "high", "xhigh", "max"}, DefaultEffort: "high", OmitTemperature: true,
		}
	}
	return nil
}

type stack struct {
	t        *testing.T
	fake     *fakeupstream.Server
	server   *httptest.Server
	recorder *recorder
}

// newStack starts the fake of the OpenAI-compatible gateway.
func newStack(t *testing.T) *stack {
	t.Helper()
	fake, err := fakeupstream.New(fakeupstream.Config{}, openaiv1.Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return &stack{t: t, fake: fake, server: server, recorder: &recorder{}}
}

// adapter is an Adapter on the fake; mutate edits its config first.
func (s *stack) adapter(mutate func(*openairesponses.Config)) *openairesponses.Adapter {
	s.t.Helper()
	return newAdapter(s.t, s.server.URL+openaiv1.Prefix, s.recorder, mutate)
}

func newAdapter(t *testing.T, baseURL string, rec *recorder, mutate func(*openairesponses.Config)) *openairesponses.Adapter {
	t.Helper()
	config := openairesponses.Config{
		BaseURL:           baseURL,
		APIKey:            "test-key",
		EndpointPath:      "/responses",
		AuthorizationName: "Authorization",
		AuthorizationType: "Bearer",
		RequestTimeout:    time.Minute,
		ScannerMaxBytes:   1 << 20,
		ErrorBodyMaxBytes: 1 << 16,
		Client:            &http.Client{Transport: recordingTransport{next: http.DefaultTransport, recorder: rec}},
		OptionsFor:        astraOptions,
	}
	if mutate != nil {
		mutate(&config)
	}
	adapter, err := openairesponses.New(config)
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

// scripted serves one fixed answer to every request, for the wire events the
// fake never sends.
func scripted(t *testing.T, status int, contentType, body string) (*openairesponses.Adapter, *recorder) {
	t.Helper()
	return scriptedConfigured(t, status, contentType, body, nil)
}

func scriptedConfigured(t *testing.T, status int, contentType, body string, mutate func(*openairesponses.Config)) (*openairesponses.Adapter, *recorder) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	rec := &recorder{}
	return newAdapter(t, server.URL, rec, mutate), rec
}

// frames builds an SSE body: each pair is an event name and its JSON data.
func frames(pairs ...string) string {
	var out bytes.Buffer
	for index := 0; index+1 < len(pairs); index += 2 {
		out.WriteString("event: " + pairs[index] + "\ndata: " + pairs[index+1] + "\n\n")
	}
	return out.String()
}

var (
	userHello = []agents.Message{{Role: "user", Content: "hello"}}
	lookup    = []agents.Tool{{Name: "lookup_note", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`)}}
	ctx       = context.Background()
)

func userMessage(text string) agents.Message { return agents.Message{Role: "user", Content: text} }

// collect runs a streamed request and returns the deltas it produced.
func collect(adapter *openairesponses.Adapter, request agents.Request) (agents.Completion, []agents.Delta, error) {
	var deltas []agents.Delta
	completion, err := adapter.Stream(ctx, request, func(delta agents.Delta) error {
		deltas = append(deltas, delta)
		return nil
	})
	return completion, deltas, err
}

func texts(deltas []agents.Delta) string {
	var out string
	for _, delta := range deltas {
		out += delta.Content
	}
	return out
}

// newBrokenStream serves the start of a stream and then drops the connection.
func newBrokenStream(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"delta\":\"x\"}\n\n")
		w.(http.Flusher).Flush()
		connection, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = connection.Close()
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// reasoningItems is what the orchestrator records on the assistant message of
// the same tool loop (#377): one item marked with the source of the request it
// answers, so a test that sends the request again gets its own item back.
func reasoningItems(request agents.Request, completion agents.Completion) []agents.ReasoningItem {
	if completion.ReasoningContent == "" {
		return nil
	}
	source := agents.ForRequest(request)
	return []agents.ReasoningItem{{
		Connection: source.Connection, Model: source.Model, Wire: openairesponses.Wire, Kind: openairesponses.ReasoningKind,
		Content: completion.ReasoningContent,
	}}
}
