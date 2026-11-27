package openairesponses_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openairesponses"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	openaiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/v1"
)

// The two gpt-astra answers formal-test showed on 2026-11-26 (#638), served by
// the fake (openaiv1.SelfImageMarker, openaiv1.EmptyCompletionMarker) instead
// of a scripted handler, so the fake and the Adapter's contract check each
// other: the fake's lines are over the 1 MiB line bound of the tests' Config,
// as the real terminal lines were over the shipped Schema's.

// headerTransport adds fixed headers to every request.
type headerTransport struct {
	next    http.RoundTripper
	headers map[string]string
}

func (transport headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	for name, value := range transport.headers {
		request.Header.Set(name, value)
	}
	return transport.next.RoundTrip(request)
}

// withHeaders sends the stack's requests with headers, still recorded.
func (s *stack) withHeaders(headers map[string]string) func(*openairesponses.Config) {
	return func(config *openairesponses.Config) {
		config.Client = &http.Client{Transport: headerTransport{
			next: recordingTransport{next: http.DefaultTransport, recorder: s.recorder}, headers: headers,
		}}
	}
}

// retrying is the round policy of the shipped Schema's shape (three attempts,
// one empty retry) without its waits.
func retrying(next agents.Adapter) agents.Adapter {
	return agents.NewRetrying(next, agents.RetryPolicy{
		MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond, EmptyRetries: 1,
		Wait: func(context.Context, time.Duration) error { return nil },
	})
}

// A picture the model drew itself is ErrInlineImageOutput, sent once even
// through the retrying round, and the stream is cut at its first sign: the
// fake holds the rest for five seconds, the call returns long before.
func TestTheFakesSelfDrawnPictureIsInlineImageOutputAndNeverRetried(t *testing.T) {
	s := newStack(t)
	request := agents.Request{Model: "gpt-astra", Messages: []agents.Message{userMessage("make the poster [[fake:self-image:5000]]")}}
	started := time.Now()
	_, err := retrying(s.adapter(nil)).Stream(ctx, request, nil)
	if !errors.Is(err, agents.ErrInlineImageOutput) {
		t.Fatalf("err = %v, want ErrInlineImageOutput", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("returned after %s: the stream was read past the picture's first sign", elapsed)
	}
	if sent := s.recorder.count(); sent != 1 {
		t.Fatalf("requests = %d, want 1: a drawn picture is never sent again", sent)
	}
	// Without the hold, the whole answer of about 8 MB ends the same way.
	_, err = s.adapter(nil).Stream(ctx, agents.Request{Model: "gpt-astra", Messages: []agents.Message{userMessage("again " + openaiv1.SelfImageMarker)}}, nil)
	if !errors.Is(err, agents.ErrInlineImageOutput) {
		t.Fatalf("err = %v, want ErrInlineImageOutput", err)
	}
}

// A round that only reasoned, its terminal line over the bound, is an empty
// completion without a usage; the retrying round sends it once more and then
// returns it empty (the orchestrator ends the turn with empty_response). When
// the second answer is text, the retry recovers.
func TestTheFakesReasoningOnlyRoundIsEmptyAndRetriedOnce(t *testing.T) {
	s := newStack(t)
	request := agents.Request{Model: "gpt-astra", Messages: []agents.Message{userMessage("continue " + openaiv1.EmptyCompletionMarker)}}
	completion, err := s.adapter(nil).Stream(ctx, request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !completion.Empty() || completion.Usage != nil || completion.ReasoningContent == "" {
		t.Fatalf("completion = content %q, %d calls, usage %v, reasoning %d bytes",
			completion.Content, len(completion.ToolCalls), completion.Usage, len(completion.ReasoningContent))
	}
	before := s.recorder.count()
	completion, err = retrying(s.adapter(nil)).Stream(ctx, request, nil)
	if err != nil || !completion.Empty() {
		t.Fatalf("retried = %+v, %v; want empty", completion, err)
	}
	if sent := s.recorder.count() - before; sent != 2 {
		t.Fatalf("requests = %d, want 2: one empty retry", sent)
	}

	recovering := newStack(t)
	adapter := recovering.adapter(recovering.withHeaders(map[string]string{fakeupstream.FailTimesHeader: "1"}))
	completion, err = retrying(adapter).Stream(ctx, request, nil)
	if err != nil || completion.Content != openaiv1.FixedReply {
		t.Fatalf("recovered = %q, %v; want the fixed reply", completion.Content, err)
	}
	if sent := recovering.recorder.count(); sent != 2 {
		t.Fatalf("requests = %d, want 2", sent)
	}
}

// #640 on the Responses wire: without the connection's quota memory the
// gateway's 429 is a used-up quota only when ExhaustedQuota says so, and its
// own 503s that follow are transient; internal/app's test drives the memory
// that reads them as the quota.
func TestTheFakesGatewayMuteRefusalsReachTheResponsesAdapter(t *testing.T) {
	s := newStack(t)
	read := func(status int, body []byte) (string, bool) {
		return "credential_quota", status == http.StatusTooManyRequests
	}
	adapter := s.adapter(func(config *openairesponses.Config) { config.ExhaustedQuota = read })
	request := agents.Request{Model: "gpt-astra", Messages: []agents.Message{userMessage("hello " + openaiv1.GatewayMuteMarker)}}
	if _, err := adapter.Stream(ctx, request, nil); !errors.Is(err, agents.ErrQuotaExhausted) {
		t.Fatalf("429 = %v, want ErrQuotaExhausted", err)
	}
	for _, answer := range []string{"muted", "recovering"} {
		_, err := adapter.Stream(ctx, request, nil)
		var status *agents.StatusError
		if !errors.Is(err, agents.ErrTransient) || !errors.As(err, &status) || status.Status != http.StatusServiceUnavailable {
			t.Fatalf("%s = %v, want a transient 503", answer, err)
		}
	}
}
