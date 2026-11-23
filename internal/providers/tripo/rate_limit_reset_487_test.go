package tripo_test

// rate_limit_reset_487_test.go 锁定 #487：限流响应带 X-RateLimit-Reset 时按
// 它等待，只有 Retry-After 时才用 Retry-After（rate-limits 页：
// https://developers.tripo3d.ai/en/docs/rate-limits，读取 2026-11-23）。

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

// rateLimitRefusal 是一个只回 429 的上游：它设置 Retry-After、X-RateLimit-Reset
// 或两者都不设，取决于测试。
func rateLimitRefusal(t *testing.T, headers map[string]string) *generation.Failure {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for name, value := range headers {
			w.Header().Set(name, value)
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":1007,"message":"Rate limit exceeded"}`))
	}))
	t.Cleanup(upstream.Close)
	adapter, err := tripo.New(tripo.Config{
		BaseURL: upstream.URL, APIKey: "tripo-key", HTTPClient: upstream.Client(),
		ErrorMapping: func() *generation.ErrorMapping {
			mapping, _ := declaredTripoMappings(t)
			return mapping
		}(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Start(t.Context(), generation.StartRequest{
		TaskID: "task", AttemptID: "attempt", OwnerID: "owner", SubmissionKey: "submission",
		Offering: tripoOffering(generation.TextTo3D), Input: []byte(`{"prompt":"ship"}`),
	})
	if err == nil {
		t.Fatal("a 429 refusal must fail the dispatch")
	}
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil {
		t.Fatalf("err = %v, want a provider failure", err)
	}
	return provider.Failure
}

func TestRateLimitResetIsUsedWhenRetryAfterIsAbsent(t *testing.T) {
	reset := time.Now().Add(7 * time.Second).Unix()
	failure := rateLimitRefusal(t, map[string]string{
		"X-RateLimit-Limit":     "100",
		"X-RateLimit-Remaining": "0",
		"X-RateLimit-Reset":     strconv.FormatInt(reset, 10),
	})
	if failure.Class != generation.FailureRateLimited {
		t.Fatalf("class = %q, want rate_limited", failure.Class)
	}
	if failure.RetryAfter < 5*time.Second || failure.RetryAfter > 9*time.Second {
		t.Fatalf("RetryAfter = %v, want the wait until the window resets", failure.RetryAfter)
	}
}

func TestRetryAfterWinsOverTheResetHeader(t *testing.T) {
	reset := time.Now().Add(30 * time.Second).Unix()
	failure := rateLimitRefusal(t, map[string]string{
		"Retry-After":       "3",
		"X-RateLimit-Reset": strconv.FormatInt(reset, 10),
	})
	if failure.RetryAfter != 3*time.Second {
		t.Fatalf("RetryAfter = %v, want the Retry-After seconds", failure.RetryAfter)
	}
}

// Without either header the failure keeps no wait and the caller's own backoff
// applies.
func TestNoRateLimitHeaderKeepsTheCallersBackoff(t *testing.T) {
	failure := rateLimitRefusal(t, nil)
	if failure.RetryAfter != 0 {
		t.Fatalf("RetryAfter = %v, want zero", failure.RetryAfter)
	}
}
