package httpjson

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #460 B20: the connection's request timeout covers every request (polls and
// downloads included), so the official creation timeout is applied to the
// create request alone.
func TestSubmitTimeoutBoundsOnlyCreationRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	client, err := NewWithConfig(Config{
		BaseURL: server.URL, APIKey: "k", HTTPClient: server.Client(),
		SubmitTimeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = client.DoJSON(context.Background(), http.MethodPost, "/x", map[string]string{"a": "b"}, "id", nil, true)
	assertProviderFailure(t, err, generation.FailureTransport, false, true)
	if err := client.DoJSON(context.Background(), http.MethodGet, "/x", nil, "id", nil, false); err != nil {
		t.Fatalf("a non-creation request must keep the connection timeout: %v", err)
	}
}

// #460 B20: a body over the threshold takes the longer creation timeout.
func TestSubmitTimeoutUsesLargeTimeoutAboveThreshold(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(80 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	client, err := NewWithConfig(Config{
		BaseURL: server.URL, APIKey: "k", HTTPClient: server.Client(),
		SubmitTimeout: 10 * time.Millisecond, LargeSubmitTimeout: time.Second,
		LargeSubmitBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DoJSON(context.Background(), http.MethodPost, "/x", map[string]string{"a": "b"}, "id", nil, true); err == nil {
		t.Fatal("a small body must time out at SubmitTimeout")
	}
	large := map[string]string{"a": strings.Repeat("x", 2048)}
	if err := client.DoJSON(context.Background(), http.MethodPost, "/x", large, "id", nil, true); err != nil {
		t.Fatalf("a body over the threshold must use LargeSubmitTimeout: %v", err)
	}
}

// #460 C25: a 5xx answer to a create is retried only when the connection
// declares it; the default stays "unknown, never resend".
func TestSubmitServerErrorRetryFollowsConnectionDeclaration(t *testing.T) {
	for _, test := range []struct {
		name      string
		declared  bool
		status    int
		wantClass generation.FailureClass
		retryable bool
		ambiguous bool
	}{
		{"declared 5xx retries", true, http.StatusInternalServerError, generation.FailureUpstreamUnavailable, true, false},
		{"undeclared 5xx stays unknown", false, http.StatusInternalServerError, generation.FailureSubmissionUnknown, false, true},
		{"declared 408 stays unknown", true, http.StatusRequestTimeout, generation.FailureSubmissionUnknown, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(`{"message":"upstream failed"}`))
			}))
			t.Cleanup(server.Close)
			client, err := NewWithConfig(Config{
				BaseURL: server.URL, APIKey: "k", HTTPClient: server.Client(),
				RetrySubmitServerError: test.declared,
			})
			if err != nil {
				t.Fatal(err)
			}
			err = client.DoJSON(context.Background(), http.MethodPost, "/x", map[string]string{"a": "b"}, "id", nil, true)
			assertProviderFailure(t, err, test.wantClass, test.retryable, test.ambiguous)
		})
	}
}

// #460 B21: a non-terminal poll uses Retry-After when present, else the
// fallback.
func TestPollAfterHonoursRetryAfterHeader(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "7")
	if got := PollAfter(header, 5*time.Second); got != 7*time.Second {
		t.Fatalf("PollAfter = %v, want 7s", got)
	}
	if got := PollAfter(http.Header{}, 5*time.Second); got != 5*time.Second {
		t.Fatalf("PollAfter fallback = %v, want 5s", got)
	}
	invalid := http.Header{}
	invalid.Set("Retry-After", "soon")
	if got := PollAfter(invalid, 5*time.Second); got != 5*time.Second {
		t.Fatalf("PollAfter malformed = %v, want 5s", got)
	}
}

func assertProviderFailure(t *testing.T, err error, class generation.FailureClass, retryable, ambiguous bool) {
	t.Helper()
	if err == nil {
		t.Fatal("want a provider failure, got nil")
	}
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil {
		t.Fatalf("error = %T %v, want *generation.ProviderError", err, err)
	}
	if provider.Failure.Class != class || provider.Failure.Retryable != retryable || provider.Failure.Ambiguous != ambiguous {
		t.Fatalf("failure = %#v, want class=%s retryable=%v ambiguous=%v", provider.Failure, class, retryable, ambiguous)
	}
}
