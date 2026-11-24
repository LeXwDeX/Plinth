package agents_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	openaiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/v1"
)

// The retry of #509 runs against the fake of the OpenAI-compatible gateway
// through the Chat Completions Adapter: the fault scenarios fail a set number
// of requests (fakeupstream.Config.FailTimes) and then answer normally.

type retryStack struct {
	t        *testing.T
	baseURL  string
	mu       sync.Mutex
	requests int
	waits    []time.Duration
}

func newRetryStack(t *testing.T, config fakeupstream.Config) *retryStack {
	t.Helper()
	fake, err := fakeupstream.New(config, openaiv1.Version())
	if err != nil {
		t.Fatal(err)
	}
	stack := &retryStack{t: t}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stack.mu.Lock()
		stack.requests++
		stack.mu.Unlock()
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	stack.baseURL = server.URL + openaiv1.Prefix
	return stack
}

func (stack *retryStack) count() int {
	stack.mu.Lock()
	defer stack.mu.Unlock()
	return stack.requests
}

// adapter is the retried Chat Completions Adapter with the key (the fake's
// scenario); the waits between attempts are recorded instead of slept.
func (stack *retryStack) adapter(key string, policy agents.RetryPolicy, firstByte time.Duration) agents.Adapter {
	stack.t.Helper()
	adapter, err := openaicompatible.New(openaicompatible.Config{
		BaseURL: stack.baseURL, APIKey: key, EndpointPath: "/chat/completions",
		AuthorizationName: "Authorization", AuthorizationType: "Bearer",
		RequestTimeout: 10 * time.Second, ScannerMaxBytes: 1 << 20, ErrorBodyMaxBytes: 1 << 16,
		FirstByteTimeout: firstByte,
		ExhaustedQuota: func(_ int, body []byte) (string, bool) {
			return "quota_exceeded", strings.Contains(string(body), `"quota_exceeded"`)
		},
	})
	if err != nil {
		stack.t.Fatal(err)
	}
	if policy.Wait == nil {
		policy.Wait = func(ctx context.Context, wait time.Duration) error {
			stack.mu.Lock()
			stack.waits = append(stack.waits, wait)
			stack.mu.Unlock()
			return ctx.Err()
		}
	}
	return agents.NewRetrying(adapter, policy)
}

var threeAttempts = agents.RetryPolicy{
	MaxAttempts: 3, InitialBackoff: 100 * time.Millisecond, MaxBackoff: time.Second, MaxRetryAfter: 10 * time.Second,
}

func hello(text string) agents.Request {
	return agents.Request{Model: "deepseek", Messages: []agents.Message{{Role: "user", Content: text}}}
}

// A 5xx, a 429 that is no quota and a broken connection are retried with
// backoff; the 429's Retry-After sets the wait. Complete is sent once.
func TestTransientFailuresAreRetried(t *testing.T) {
	for _, scenario := range []struct {
		key   string
		waits []time.Duration
	}{
		{"fake-server-error", []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}},
		{"fake-rate-limit", []time.Duration{2 * time.Second, 2 * time.Second}},
	} {
		stack := newRetryStack(t, fakeupstream.Config{FailTimes: 2, RetryAfter: 2 * time.Second})
		adapter := stack.adapter(scenario.key, threeAttempts, 0)
		completion, err := adapter.Stream(context.Background(), hello("hi"), nil)
		if err != nil || completion.Content != openaiv1.FixedReply || stack.count() != 3 {
			t.Fatalf("%s: %d requests, %+v %v", scenario.key, stack.count(), completion, err)
		}
		if len(stack.waits) != 2 || stack.waits[0] != scenario.waits[0] || stack.waits[1] != scenario.waits[1] {
			t.Fatalf("%s: waits %v, want %v", scenario.key, stack.waits, scenario.waits)
		}
		once := newRetryStack(t, fakeupstream.Config{FailTimes: 2})
		if _, err := once.adapter(scenario.key, threeAttempts, 0).Complete(context.Background(), hello("hi")); !errors.Is(err, agents.ErrTransient) || once.count() != 1 {
			t.Fatalf("%s complete: %d requests, %v", scenario.key, once.count(), err)
		}
	}
	// A connection that breaks every time is tried MaxAttempts times and
	// returned as the transient failure it is.
	stack := newRetryStack(t, fakeupstream.Config{})
	_, err := stack.adapter("fake-lost-response", threeAttempts, 0).Stream(context.Background(), hello("hi"), nil)
	if !errors.Is(err, agents.ErrTransient) || stack.count() != 3 {
		t.Fatalf("lost response: %d requests, %v", stack.count(), err)
	}
}

// Nothing is retried that a repeat cannot fix or that would repeat output:
// a used-up quota, a refused field (#401), an unknown model, a Retry-After over
// the bound, and a stream that broke after the caller received output.
func TestWhatIsNotRetried(t *testing.T) {
	for name, run := range map[string]func(stack *retryStack) error{
		"quota": func(stack *retryStack) error {
			_, err := stack.adapter("fake-quota-exceeded", threeAttempts, 0).Stream(context.Background(), hello("hi"), nil)
			return err
		},
		"model not found": func(stack *retryStack) error {
			_, err := stack.adapter("fake-model-not-found", threeAttempts, 0).Stream(context.Background(), hello("hi"), nil)
			return err
		},
		"refused field": func(stack *retryStack) error {
			request := hello("hi")
			request.Model, request.Temperature = "gpt-astra", 0.2
			_, err := stack.adapter("test-key", threeAttempts, 0).Stream(context.Background(), request, nil)
			return err
		},
		"retry-after over the bound": func(stack *retryStack) error {
			policy := threeAttempts
			policy.MaxRetryAfter = time.Second
			_, err := stack.adapter("fake-rate-limit", policy, 0).Stream(context.Background(), hello("hi"), nil)
			return err
		},
		"cut after output": func(stack *retryStack) error {
			var content strings.Builder
			_, err := stack.adapter("test-key", threeAttempts, 0).Stream(context.Background(), hello("hi "+openaiv1.StreamCutMarker),
				func(delta agents.Delta) error { content.WriteString(delta.Content); return nil })
			if content.String() != openaiv1.StreamCutPart {
				t.Errorf("cut after output: the person saw %q", content.String())
			}
			return err
		},
	} {
		stack := newRetryStack(t, fakeupstream.Config{RetryAfter: 5 * time.Second})
		if err := run(stack); err == nil || stack.count() != 1 || len(stack.waits) != 0 {
			t.Errorf("%s: %d requests, waits %v, %v", name, stack.count(), stack.waits, err)
		}
	}
}

// A stream that broke before any output is replayed: the error frame and the
// plain answer reach the caller with nothing emitted, so each attempt is a
// fresh one.
func TestAStreamThatBrokeBeforeOutputIsReplayed(t *testing.T) {
	for _, marker := range []string{openaiv1.StreamErrorMarker, openaiv1.StreamPlainMarker} {
		stack := newRetryStack(t, fakeupstream.Config{})
		emitted := 0
		_, err := stack.adapter("test-key", threeAttempts, 0).Stream(context.Background(), hello("hi "+marker),
			func(agents.Delta) error { emitted++; return nil })
		if !errors.Is(err, agents.ErrTransient) || stack.count() != 3 || emitted != 0 {
			t.Errorf("%s: %d requests, %d deltas, %v", marker, stack.count(), emitted, err)
		}
	}
}

// A stuck first attempt ends at the first-byte timeout and the next one
// answers; a round that stays stuck ends at the round timeout, as transient.
func TestAStuckUpstreamIsRetriedWithinTheRound(t *testing.T) {
	stack := newRetryStack(t, fakeupstream.Config{FailTimes: 1, TimeoutHold: time.Second})
	completion, err := stack.adapter("fake-timeout", threeAttempts, 100*time.Millisecond).Stream(context.Background(), hello("hi"), nil)
	if err != nil || completion.Content != openaiv1.FixedReply || stack.count() != 2 {
		t.Fatalf("stuck once: %d requests, %+v %v", stack.count(), completion, err)
	}

	stuck := newRetryStack(t, fakeupstream.Config{TimeoutHold: time.Second})
	policy := threeAttempts
	policy.RoundTimeout = 150 * time.Millisecond
	policy.Wait = func(ctx context.Context, wait time.Duration) error {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
	started := time.Now()
	_, err = stuck.adapter("fake-timeout", policy, 0).Stream(context.Background(), hello("hi"), nil)
	if elapsed := time.Since(started); elapsed > 800*time.Millisecond || !errors.Is(err, agents.ErrTransient) ||
		!strings.Contains(err.Error(), "round") {
		t.Fatalf("stuck round after %s: %v", elapsed, err)
	}

	// The caller's own end is never retried nor turned transient.
	canceled := newRetryStack(t, fakeupstream.Config{TimeoutHold: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = canceled.adapter("fake-timeout", threeAttempts, 0).Stream(ctx, hello("hi"), nil)
	if err == nil || errors.Is(err, agents.ErrTransient) || canceled.count() != 1 {
		t.Fatalf("canceled: %d requests, %v", canceled.count(), err)
	}
}

// A policy of one attempt and no round bound leaves the Adapter as it is.
func TestNoPolicyIsTheAdapterItself(t *testing.T) {
	adapter, err := openaicompatible.New(openaicompatible.Config{
		BaseURL: "http://agent.test/v1", EndpointPath: "/chat/completions", AuthorizationName: "Authorization",
		AuthorizationType: "Bearer", RequestTimeout: time.Second, ScannerMaxBytes: 1, ErrorBodyMaxBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if wrapped := agents.NewRetrying(adapter, agents.RetryPolicy{MaxAttempts: 1}); wrapped != agents.Adapter(adapter) {
		t.Fatalf("wrapped: %#v", wrapped)
	}
}
