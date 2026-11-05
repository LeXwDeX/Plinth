package httpjson

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #120: the LLMs gateway answers a caller whose quota is used up with HTTP 429
// and error code quota_exceeded, in 0 ms. Read as a rate limit, the create was
// queued and resent until the Offering timeout (200+ refusals in ten minutes).
// An exhausted quota or balance does not come back within that window, so the
// connection's declared code ends the create as a definite, non-retryable
// refusal; a plain 429 stays a retryable rate limit.
func TestGatewayQuotaExhaustionOn429IsNotARateLimit(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		class     generation.FailureClass
		retryable bool
	}{
		{"gateway quota_exceeded", `{"error":{"code":"quota_exceeded","message":"caller quota exceeded","type":"aigateway_error"}}`, generation.FailureCredentialQuota, false},
		{"openai insufficient_quota", `{"error":{"message":"You exceeded your current quota, please check your plan and billing details.","type":"insufficient_quota","param":null,"code":"insufficient_quota"}}`, generation.FailureInsufficientBalance, false},
		{"rate_limit_exceeded", `{"error":{"code":"rate_limit_exceeded","message":"slow down","type":"aigateway_error"}}`, generation.FailureRateLimited, true},
		{"429 without a body", ``, generation.FailureRateLimited, true},
	}
	// Both image connections the gateway serves on dev-test.
	for _, connection := range []string{"openai-images", "gemini-images"} {
		for _, tc := range cases {
			t.Run(connection+"/"+tc.name, func(t *testing.T) {
				failure := submitAgainst(t, connectionMapping(t, connection), http.StatusTooManyRequests, tc.body)
				if failure.Class != tc.class || failure.Retryable != tc.retryable || failure.Ambiguous {
					t.Fatalf("class %s retryable %v (%+v), want class %s retryable %v", failure.Class, failure.Retryable, failure, tc.class, tc.retryable)
				}
				if tc.retryable && failure.RetryAfter != 7*time.Second {
					t.Fatalf("a rate limit lost Retry-After: %v", failure.RetryAfter)
				}
				if !tc.retryable && failure.RetryAfter != 0 {
					t.Fatalf("a definite refusal carries Retry-After %v", failure.RetryAfter)
				}
				if failure.UpstreamStatus != http.StatusTooManyRequests {
					t.Fatalf("upstream status = %d", failure.UpstreamStatus)
				}
			})
		}
	}
}

// gatewayWeeklyQuotaSample is the 429 body the LLMs gateway sent on dev-test
// (#120) when the caller's weekly model-group quota ran out. Plinth read it as
// class=rate_limited, retryable=true, upstream_code=quota_exceeded. It holds no
// credentials; the amounts and the reset time are kept as observed.
const gatewayWeeklyQuotaSample = `{"error":{"code":"quota_exceeded","message":"Quota exceeded (weekly). Limit: $100.00, used: $101.21. Resets at 2026-11-09T00:00:00Z","quota":{"dimension":"weekly","group_name":"API","limit_usd":100,"resets_at":"2026-11-09T00:00:00Z","scope":"model_group","used_usd":101.20632699999999},"type":"quota_exceeded"}}`

// The real gateway sample ends the gpt-image-2 create as the connection's
// declared credential_quota: not retryable, no Retry-After, and the same
// upstream code evidence as before.
func TestGatewayWeeklyQuotaSampleIsACredentialQuota(t *testing.T) {
	failure := submitAgainst(t, connectionMapping(t, "openai-images"), http.StatusTooManyRequests, gatewayWeeklyQuotaSample)
	if failure.Class != generation.FailureCredentialQuota || failure.Retryable || failure.Ambiguous || failure.RetryAfter != 0 {
		t.Fatalf("class %s retryable %v ambiguous %v retry-after %v, want a definite credential_quota",
			failure.Class, failure.Retryable, failure.Ambiguous, failure.RetryAfter)
	}
	if failure.UpstreamStatus != http.StatusTooManyRequests || failure.UpstreamCode != "quota_exceeded" {
		t.Fatalf("evidence = status %d code %q", failure.UpstreamStatus, failure.UpstreamCode)
	}
}

// A code the connection declares as an exhausted balance or key quota outranks
// its HTTP status ranges, like the balance codes always did: a connection that
// declares 429 as a retryable rate limit must still end a 429 quota_exceeded,
// and a broad 403 range must not hide one either.
func TestDeclaredQuotaExhaustionOutranksStatusRanges(t *testing.T) {
	mapping := &generation.ErrorMapping{
		HTTPStatusRanges: []generation.HTTPStatusClass{
			{Min: 403, Max: 403, Class: string(generation.FailureUpstreamDenied)},
			{Min: 429, Max: 429, Class: string(generation.FailureRateLimited), Retryable: true},
		},
		UpstreamCodeMap: map[string]generation.ErrorClassRule{
			"quota_exceeded":     {Class: string(generation.FailureCredentialQuota)},
			"insufficient_quota": {Class: string(generation.FailureInsufficientBalance)},
		},
	}
	cases := []struct {
		name      string
		status    int
		body      string
		class     generation.FailureClass
		retryable bool
	}{
		{"429 quota_exceeded", 429, `{"error":{"code":"quota_exceeded"}}`, generation.FailureCredentialQuota, false},
		{"429 insufficient_quota", 429, `{"error":{"code":"insufficient_quota"}}`, generation.FailureInsufficientBalance, false},
		{"403 quota_exceeded", 403, `{"error":{"code":"quota_exceeded"}}`, generation.FailureCredentialQuota, false},
		{"429 other code keeps the declared range", 429, `{"error":{"code":"rate_limit_exceeded"}}`, generation.FailureRateLimited, true},
		{"403 other code keeps the declared range", 403, `{"error":{"code":"eca_access_denied"}}`, generation.FailureUpstreamDenied, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failure := classify(t, mapping, tc.status, tc.body)
			if failure.Class != tc.class || failure.Retryable != tc.retryable {
				t.Fatalf("class %s retryable %v (%+v), want class %s retryable %v", failure.Class, failure.Retryable, failure, tc.class, tc.retryable)
			}
		})
	}
}

// submitAgainst sends one create (submitting) through the client configured
// with mapping, against an upstream that answers status and body.
func submitAgainst(t *testing.T, mapping *generation.ErrorMapping, status int, body string) *generation.Failure {
	t.Helper()
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(upstream.Close)
	client, err := NewWithConfig(Config{BaseURL: upstream.URL, APIKey: "key", ErrorMapping: mapping, HTTPClient: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	err = client.DoJSON(context.Background(), http.MethodPost, "/images/generations", map[string]string{"prompt": "p"}, "request", nil, true)
	var providerErr *generation.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Failure == nil {
		t.Fatalf("not a provider failure: %v", err)
	}
	if requests != 1 {
		t.Fatalf("upstream requests = %d, want 1", requests)
	}
	return providerErr.Failure
}
