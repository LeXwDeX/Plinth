package httpjson

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestSubmittingServerErrorsRemainAmbiguous(t *testing.T) {
	for _, code := range []int{408, 500, 502, 503, 504} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
			defer server.Close()
			client, _ := New(server.URL, "key", server.Client())
			err := client.DoJSON(context.Background(), "POST", "tasks", map[string]string{"prompt": "test"}, "key", nil, true)
			var pe *generation.ProviderError
			if !errors.As(err, &pe) || !pe.Failure.Ambiguous || pe.Failure.Retryable {
				t.Fatalf("failure=%#v", err)
			}
		})
	}
}

func TestDialFailureIsDefinitelyNotSent(t *testing.T) {
	if !definitelyNotSent(&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}) {
		t.Fatal("dial failure treated as ambiguous")
	}
}

type brokenInput struct{}

func (brokenInput) Read([]byte) (int, error) { return 0, errors.New("local image unavailable") }
func TestLocalMultipartReadFailsBeforeSubmission(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = io.Copy(io.Discard, r.Body) }))
	defer server.Close()
	client, _ := New(server.URL, "key", server.Client())
	err := client.Do(context.Background(), "POST", "tasks", brokenInput{}, "multipart/form-data; boundary=x", "", nil, true)
	var pe *generation.ProviderError
	if !errors.As(err, &pe) || pe.Failure.Ambiguous || !pe.Failure.Retryable {
		t.Fatalf("failure=%#v", err)
	}
	if calls != 0 {
		t.Fatalf("HTTP calls=%d", calls)
	}
}

func TestTLSHandshakeFailureIsSafeToRetry(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted TLS request was sent") }))
	defer server.Close()
	client, _ := New(server.URL, "key", &http.Client{})
	err := client.DoJSON(context.Background(), http.MethodPost, "tasks", nil, "", nil, true)
	var pe *generation.ProviderError
	if !errors.As(err, &pe) || pe.Failure.Ambiguous || !pe.Failure.Retryable {
		t.Fatalf("TLS failure=%#v", err)
	}
}

func TestCancelUnblocksMultipartPreparation(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	client, _ := New("http://unused.invalid", "key", &http.Client{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- client.Do(ctx, http.MethodPost, "tasks", reader, "multipart/form-data; boundary=test", "", nil, true)
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(250 * time.Millisecond):
		_ = reader.Close()
		<-done
		t.Fatal("canceled multipart copy blocked on pipe.Read")
	}
}
