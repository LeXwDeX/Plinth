package openaicompatible_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	openaiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/v1"
)

// The #509 regressions run against the fake of the OpenAI-compatible gateway:
// a stream cut short, an error frame, a plain answer to a streaming request,
// the transient refusals and a stuck upstream are its fault scenarios
// (fakeupstream.Scenario* through a fake-<scenario> key, or the fake-only
// markers of openai/v1).

func resilienceFake(t *testing.T, config fakeupstream.Config) string {
	t.Helper()
	fake, err := fakeupstream.New(config, openaiv1.Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return server.URL + openaiv1.Prefix
}

func resilienceAdapter(t *testing.T, baseURL, key string, mutate func(*openaicompatible.Config)) *openaicompatible.Adapter {
	t.Helper()
	config := openaicompatible.Config{
		BaseURL: baseURL, APIKey: key, EndpointPath: "/chat/completions",
		AuthorizationName: "Authorization", AuthorizationType: "Bearer",
		RequestTimeout: 10 * time.Second, ScannerMaxBytes: 1 << 20, ErrorBodyMaxBytes: 1 << 16,
	}
	if mutate != nil {
		mutate(&config)
	}
	adapter, err := openaicompatible.New(config)
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func ask(text string) agents.Request {
	return agents.Request{Model: "deepseek", Messages: []agents.Message{{Role: "user", Content: text}}}
}

// A stream that ends before its last chunk, one that reports an error and a
// plain answer to a streaming request are transient failures, never a reply:
// the Responses Adapter already fails a stream without its terminal event.
func TestAStreamWithoutItsEndIsATransientFailure(t *testing.T) {
	adapter := resilienceAdapter(t, resilienceFake(t, fakeupstream.Config{}), "test-key", nil)
	for name, marker := range map[string]string{
		"cut":         openaiv1.StreamCutMarker,
		"error frame": openaiv1.StreamErrorMarker,
		"not SSE":     openaiv1.StreamPlainMarker,
	} {
		var content strings.Builder
		completion, err := adapter.Stream(context.Background(), ask("hi "+marker), func(delta agents.Delta) error {
			content.WriteString(delta.Content)
			return nil
		})
		if !errors.Is(err, agents.ErrTransient) {
			t.Errorf("%s: completion %+v, err %v; want a transient failure", name, completion, err)
		}
		var status *agents.StatusError
		if errors.As(err, &status) {
			t.Errorf("%s: an answered stream carries no HTTP status: %v", name, err)
		}
		if name == "cut" && content.String() != openaiv1.StreamCutPart {
			t.Errorf("cut: the part before the cut was emitted as %q", content.String())
		}
	}
	// A complete stream still is a reply.
	completion, err := adapter.Stream(context.Background(), ask("hi"), nil)
	if err != nil || completion.Content != openaiv1.FixedReply {
		t.Fatalf("complete stream: %+v %v", completion, err)
	}
}

// An error frame of a used-up quota or a content-safety refusal keeps its class
// (as on a refusal and in the Responses Adapter): it is not retried.
func TestAStreamErrorFrameKeepsItsClass(t *testing.T) {
	adapter := resilienceAdapter(t, resilienceFake(t, fakeupstream.Config{}), "test-key", func(config *openaicompatible.Config) {
		config.ExhaustedQuota = func(_ int, body []byte) (string, bool) {
			return "upstream_overloaded_as_quota", strings.Contains(string(body), "server_is_overloaded")
		}
	})
	_, err := adapter.Stream(context.Background(), ask("hi "+openaiv1.StreamErrorMarker), nil)
	if !errors.Is(err, agents.ErrQuotaExhausted) || errors.Is(err, agents.ErrTransient) {
		t.Fatalf("error frame read as quota: %v", err)
	}
}

// HTTP 429 that is not a used-up quota, 5xx and a broken connection are
// transient; the 429 and the 503 carry the upstream's Retry-After. A quota the
// Schema maps, a refused request field (#401) and an unknown model are not.
func TestTransientRefusalsAndBrokenConnections(t *testing.T) {
	baseURL := resilienceFake(t, fakeupstream.Config{RetryAfter: 3 * time.Second})
	quota := func(_ int, body []byte) (string, bool) {
		return "quota_exceeded", strings.Contains(string(body), `"quota_exceeded"`)
	}
	for _, scenario := range []struct {
		key        string
		transient  bool
		retryAfter time.Duration
	}{
		{"fake-rate-limit", true, 3 * time.Second},
		{"fake-service-unavailable", true, 3 * time.Second},
		{"fake-server-error", true, 0},
		{"fake-lost-response", true, 0},
		{"fake-quota-exceeded", false, 0},
		{"fake-model-not-found", false, 0},
		{"fake-invalid-key", false, 0},
	} {
		adapter := resilienceAdapter(t, baseURL, scenario.key, func(config *openaicompatible.Config) {
			config.ExhaustedQuota = quota
		})
		for _, path := range []string{"stream", "complete"} {
			var err error
			if path == "stream" {
				_, err = adapter.Stream(context.Background(), ask("hi"), nil)
			} else {
				_, err = adapter.Complete(context.Background(), ask("hi"))
			}
			retryAfter, transient := agents.TransientRetryAfter(err)
			if err == nil || transient != scenario.transient || retryAfter != scenario.retryAfter {
				t.Errorf("%s %s: transient=%t retry-after=%s err=%v", scenario.key, path, transient, retryAfter, err)
			}
		}
	}
	// #401: a field the model refuses stays a request rejection.
	adapter := resilienceAdapter(t, baseURL, "test-key", nil)
	request := ask("hi")
	request.Model = "gpt-astra"
	request.Temperature = 0.2
	_, err := adapter.Stream(context.Background(), request, nil)
	if !errors.Is(err, agents.ErrRequestRejected) || errors.Is(err, agents.ErrTransient) {
		t.Fatalf("refused field: %v", err)
	}
}

// An upstream that sends nothing fails after the first-byte timeout, as a
// transient failure, long before the request timeout.
func TestAStuckUpstreamFailsAtTheFirstByteTimeout(t *testing.T) {
	// The fake holds the request for a second (it reads no body first, so it
	// does not see the client leave); the call must not wait for that.
	baseURL := resilienceFake(t, fakeupstream.Config{TimeoutHold: time.Second})
	adapter := resilienceAdapter(t, baseURL, "fake-timeout", func(config *openaicompatible.Config) {
		config.FirstByteTimeout = 150 * time.Millisecond
	})
	started := time.Now()
	_, err := adapter.Stream(context.Background(), ask("hi"), nil)
	if elapsed := time.Since(started); elapsed > 800*time.Millisecond {
		t.Fatalf("stuck call took %s", elapsed)
	}
	if !errors.Is(err, agents.ErrTransient) || !strings.Contains(err.Error(), "first byte") {
		t.Fatalf("stuck call: %v", err)
	}
	// A caller that gives up is not a transient failure.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := adapter.Stream(ctx, ask("hi"), nil); err == nil || errors.Is(err, agents.ErrTransient) {
		t.Fatalf("canceled call: %v", err)
	}
}
