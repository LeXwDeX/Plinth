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

// #322: a refusal carries its HTTP status as agents.StatusError, so the
// administrator's connection test can classify it without the body.
func TestAdapterRefusalCarriesItsStatus(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusTooManyRequests, http.StatusBadGateway} {
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{"error":"upstream detail"}`))}, nil
		})
		adapter, err := openaicompatible.New(openaicompatible.Config{
			BaseURL: "https://configured-agent.test/v1", APIKey: "configured-secret", EndpointPath: "/chat/completions",
			AuthorizationName: "Authorization", AuthorizationType: "Bearer", RequestTimeout: time.Minute,
			ScannerMaxBytes: 1 << 20, ErrorBodyMaxBytes: 1 << 16, Client: &http.Client{Transport: transport},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = adapter.Complete(context.Background(), agents.Request{Model: "m", Messages: []agents.Message{{Role: "user", Content: "hi"}}})
		var refused *agents.StatusError
		if !errors.As(err, &refused) || refused.Status != status {
			t.Fatalf("status %d: %v", status, err)
		}
	}
}
