package httpjson

import (
	"net/http"
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
