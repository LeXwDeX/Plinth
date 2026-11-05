package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #120 end to end: the default Catalog, the real openai-images Adapter and a
// stub of the LLMs gateway. dev-test's gateway answered a caller whose quota
// was used up with 429 quota_exceeded; read as a rate limit, the Task queued
// and resent for the whole Offering timeout. An exhausted quota or balance
// must end the Task on the first refusal, with the Schema's copy for its
// class, while a plain 429 keeps queueing.
func TestExhaustedQuotaOn429FailsTheTaskAtOnce(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		class generation.FailureClass
	}{
		// The body the gateway actually sent on dev-test (weekly quota).
		{"gateway quota_exceeded", `{"error":{"code":"quota_exceeded","message":"Quota exceeded (weekly). Limit: $100.00, used: $101.21. Resets at 2026-11-09T00:00:00Z","quota":{"dimension":"weekly","group_name":"API","limit_usd":100,"resets_at":"2026-11-09T00:00:00Z","scope":"model_group","used_usd":101.20632699999999},"type":"quota_exceeded"}}`, generation.FailureCredentialQuota},
		{"openai insufficient_quota", `{"error":{"message":"You exceeded your current quota, please check your plan and billing details.","type":"insufficient_quota","param":null,"code":"insufficient_quota"}}`, generation.FailureInsufficientBalance},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			taskCatalog, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			harness, requests, taskID := submitAgainstGatewayStub(t, taskCatalog, tc.body)

			harness.reconcile(t)
			task, err := harness.module.Get(context.Background(), "owner-acme", taskID)
			if err != nil {
				t.Fatal(err)
			}
			if task.State != generation.TaskFailed || task.RateLimitQueued {
				t.Fatalf("state=%s rate_limit_queued=%v failure=%#v, want FAILED at once", task.State, task.RateLimitQueued, task.Failure)
			}
			if task.Failure == nil || task.Failure.Class != tc.class || task.Failure.Retryable || task.Failure.Ambiguous {
				t.Fatalf("failure = %#v, want non-retryable %s", task.Failure, tc.class)
			}
			// Further passes do not resend the create.
			for range 5 {
				harness.reconcile(t)
			}
			if got := requests.Load(); got != 1 {
				t.Fatalf("upstream creates = %d, want 1", got)
			}
			events, err := harness.module.Events(context.Background(), "owner-acme", taskID, 0, 1000)
			if err != nil {
				t.Fatal(err)
			}
			for _, eventType := range []string{"attempt.rate_limit_queued", "attempt.retry_scheduled"} {
				if hasAcmeEvent(events, eventType) {
					t.Fatalf("%s recorded for an exhausted quota", eventType)
				}
			}
			if attempts := attemptCount(t, harness, "owner-acme", taskID); attempts != 1 {
				t.Fatalf("attempts = %d, want 1", attempts)
			}

			// The person and the Agent read the class's Schema copy, never
			// the upstream wording.
			application := taskCatalog.Application()
			public := application.Content.Messages["failure."+string(tc.class)]
			agent := application.Chat.Presentation.Messages.GenerationFailedByClass[string(tc.class)]
			if public == "" || agent == "" {
				t.Fatalf("Schema copy missing for %s: public=%q agent=%q", tc.class, public, agent)
			}
			if got := generationtool.FailureMessage(application.Chat, task.Offering.CreditsMetered, task.Failure); got != agent {
				t.Fatalf("Agent tool result = %q, want the Schema copy %q", got, agent)
			}
		})
	}
}

// A plain 429 in the same gateway envelope is still a rate limit: the Task
// queues without spending max_attempts.
func TestPlainRateLimitOn429StillQueues(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	harness, requests, taskID := submitAgainstGatewayStub(t, taskCatalog,
		`{"error":{"code":"rate_limit_exceeded","message":"slow down","type":"aigateway_error"}}`)
	for range 3 {
		harness.reconcile(t)
	}
	task, err := harness.module.Get(context.Background(), "owner-acme", taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskQueued || !task.RateLimitQueued || task.Failure != nil {
		t.Fatalf("state=%s rate_limit_queued=%v failure=%#v, want QUEUED", task.State, task.RateLimitQueued, task.Failure)
	}
	events, err := harness.module.Events(context.Background(), "owner-acme", taskID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !hasAcmeEvent(events, "attempt.rate_limit_queued") || hasAcmeEvent(events, "attempt.retry_scheduled") {
		t.Fatalf("events = %v, want rate_limit_queued only", events)
	}
	if got := requests.Load(); got < 2 {
		t.Fatalf("upstream creates = %d, want the queued Task to resend", got)
	}
}

// submitAgainstGatewayStub points the openai-images connection at a stub that
// answers every request with HTTP 429 and body, and submits one approved
// gpt-image-2 text-to-image Task.
func submitAgainstGatewayStub(t *testing.T, taskCatalog *catalog.Catalog, body string) (*acmeHarness, *atomic.Int64, generation.TaskID) {
	t.Helper()
	requests := &atomic.Int64{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(upstream.Close)
	harness := newAcmeHarness(t, taskCatalog, map[string]string{
		"openai_base_url": upstream.URL + "/v1",
		"openai_api_key":  "gateway-caller-key",
	})
	accepted := harness.submitApproved(t, generation.TextToImage,
		"openai/gpt-image-2/text-to-image",
		json.RawMessage(`{"prompt":"quota still"}`), "quota-e2e")
	return harness, requests, accepted.TaskID
}
