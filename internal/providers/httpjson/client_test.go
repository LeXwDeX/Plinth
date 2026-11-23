package httpjson

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewWithConfigDefaultsClientTimeout(t *testing.T) {
	client, err := NewWithConfig(Config{
		BaseURL: "http://upstream.example",
		APIKey:  "key",
	})
	if err != nil {
		t.Fatalf("NewWithConfig failed: %v", err)
	}
	if client.HTTPClient == nil {
		t.Fatal("expected default HTTPClient to be constructed")
	}
	if client.HTTPClient.Timeout != DefaultRequestTimeout {
		t.Fatalf(
			"default HTTPClient timeout = %v, want %v",
			client.HTTPClient.Timeout,
			DefaultRequestTimeout,
		)
	}
	if DefaultRequestTimeout < 30*time.Minute {
		t.Fatalf(
			"DefaultRequestTimeout = %v, must cover sync image generation exceeding 2 minutes",
			DefaultRequestTimeout,
		)
	}
}

func TestNewWithConfigPreservesInjectedClient(t *testing.T) {
	injected := &http.Client{Timeout: time.Second}
	client, err := NewWithConfig(Config{
		BaseURL:    "http://upstream.example",
		APIKey:     "key",
		HTTPClient: injected,
	})
	if err != nil {
		t.Fatalf("NewWithConfig failed: %v", err)
	}
	if client.HTTPClient != injected {
		t.Fatal("expected injected HTTPClient to be preserved")
	}
}

// #487: a status poll is bounded by the declared poll timeout (Meshy's
// official GET guidance is 30 s) even though the connection timeout also has
// to let an artifact download run long. A creation request keeps its own
// submit timeout.
func TestPollTimeoutBoundsStatusRequestsOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(400 * time.Millisecond)
		_, _ = writer.Write([]byte(`{"status":"SUCCEEDED"}`))
	}))
	defer server.Close()
	client, err := NewWithConfig(Config{
		BaseURL:       server.URL,
		APIKey:        "key",
		HTTPClient:    server.Client(),
		PollTimeout:   50 * time.Millisecond,
		SubmitTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	var target map[string]any
	if err := client.DoJSON(context.Background(), http.MethodGet, "/task-1", nil, "req-1", &target, false); err == nil {
		t.Fatal("a poll longer than the poll timeout was accepted")
	}
	// A slow answer is fine while the caller's own context allows it: the
	// submit path keeps the connection timeout.
	if err := client.DoJSON(context.Background(), http.MethodPost, "/task", map[string]any{"prompt": "p"}, "req-2", &target, true); err != nil {
		t.Fatalf("a create inside the submit timeout failed: %v", err)
	}
}
