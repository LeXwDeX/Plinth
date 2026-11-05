package openaicompatible_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"
)

// The LLMs gateway's 429 when the caller's weekly quota ran out (#120).
const gatewayQuotaRefusal = `{"error":{"code":"quota_exceeded","message":"Quota exceeded (weekly). Limit: $100.00, used: $101.21. Resets at 2026-11-09T00:00:00Z","quota":{"dimension":"weekly","group_name":"API","limit_usd":100,"resets_at":"2026-11-09T00:00:00Z","scope":"model_group","used_usd":101.20632699999999},"type":"quota_exceeded"}}`

// #120: a refusal the configured classifier (the chat connection's Schema
// error_mapping) reads as a used-up quota is an agents.ErrQuotaExhausted that
// still carries its status; the classifier sees the JSON of a streamed
// refusal too. Any other refusal keeps its plain status error.
func TestAdapterMarksDeclaredQuotaExhaustion(t *testing.T) {
	request := agents.Request{Model: "m", Messages: []agents.Message{{Role: "user", Content: "hello"}}}
	for _, test := range []struct {
		name      string
		status    int
		body      string
		exhausted bool
	}{
		{"gateway quota_exceeded", http.StatusTooManyRequests, gatewayQuotaRefusal, true},
		{"streamed quota refusal", http.StatusTooManyRequests, "data: " + gatewayQuotaRefusal, true},
		{"plain rate limit", http.StatusTooManyRequests, `{"error":{"code":"rate_limit_exceeded","message":"slow down"}}`, false},
		{"other refusal", http.StatusBadGateway, `{"error":{"code":"quota_exceeded"}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var seen []string
			classify := func(status int, body []byte) (string, bool) {
				seen = append(seen, string(body))
				// Stands in for the Schema mapping: only a 429 that names the
				// declared code counts.
				if status == http.StatusTooManyRequests && strings.Contains(string(body), `"code":"quota_exceeded"`) {
					return "credential_quota", strings.HasPrefix(string(body), "{")
				}
				return "", false
			}
			adapter := refusingAdapter(t, test.status, test.body, classify)
			for _, call := range []func() error{
				func() error { _, err := adapter.Complete(context.Background(), request); return err },
				func() error { _, err := adapter.Stream(context.Background(), request, nil); return err },
			} {
				err := call()
				var status *agents.StatusError
				if !errors.As(err, &status) || status.Status != test.status {
					t.Fatalf("status lost: %v", err)
				}
				var exhaustion *agents.QuotaExhaustion
				if got := errors.Is(err, agents.ErrQuotaExhausted); got != test.exhausted {
					t.Fatalf("quota exhausted = %v, want %v: %v", got, test.exhausted, err)
				}
				if test.exhausted && (!errors.As(err, &exhaustion) || exhaustion.Class != "credential_quota") {
					t.Fatalf("exhaustion = %#v", exhaustion)
				}
				if errors.Is(err, agents.ErrContentRejected) || errors.Is(err, agents.ErrImagesRejected) {
					t.Fatalf("misread refusal: %v", err)
				}
			}
			if len(seen) != 2 || strings.HasPrefix(seen[0], "data:") || strings.HasPrefix(seen[1], "data:") {
				t.Fatalf("classifier saw %q, want the JSON of each refusal", seen)
			}
		})
	}
}

// Without a classifier, as for a connection that declares no error_mapping,
// no refusal reads as a used-up quota.
func TestAdapterWithoutQuotaClassifierKeepsTheStatusError(t *testing.T) {
	adapter := refusingAdapter(t, http.StatusTooManyRequests, gatewayQuotaRefusal, nil)
	_, err := adapter.Complete(context.Background(), agents.Request{Model: "m", Messages: []agents.Message{{Role: "user", Content: "hello"}}})
	var status *agents.StatusError
	if errors.Is(err, agents.ErrQuotaExhausted) || !errors.As(err, &status) || status.Status != http.StatusTooManyRequests {
		t.Fatalf("err = %v", err)
	}
}

func refusingAdapter(t *testing.T, status int, body string, classify func(int, []byte) (string, bool)) *openaicompatible.Adapter {
	t.Helper()
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	adapter, err := openaicompatible.New(openaicompatible.Config{
		BaseURL: "https://configured-agent.test/v1", APIKey: "k", EndpointPath: "/chat/completions",
		AuthorizationName: "Authorization", AuthorizationType: "Bearer", RequestTimeout: time.Minute,
		ScannerMaxBytes: 1 << 20, ErrorBodyMaxBytes: 1 << 16, Client: &http.Client{Transport: transport},
		ExhaustedQuota: classify,
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}
