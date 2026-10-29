package hyper3d_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/hyper3d"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	hyper3dapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/hyper3d/api-v2"
)

// statusRecorder records the HTTP status the fake answered with.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// exchange is one request the Adapter made to the fake and the status it got.
type exchange struct {
	path   string
	status int
}

// Rodin reports a rejected request with HTTP 201 and {"error": ...} (observed
// in #96). The Adapter must fail such an exchange with the Schema's class and
// never read the body as an accepted job or poll it. This runs against the #96
// Hyper3D fake, which serves the same status/body combination.
func TestRodinHTTP201ErrorBodyFailsWithMappedClassAgainstFake(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var errorMapping *generation.ErrorMapping
	for _, connection := range product.Application().Runtime.ProviderConnections {
		if connection.ID == "hyper3d" {
			errorMapping = connection.ErrorMapping
		}
	}
	if errorMapping == nil {
		t.Fatal("hyper3d connection has no error mapping")
	}
	fake, err := fakeupstream.New(fakeupstream.Config{}, hyper3dapiv2.Version())
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var exchanges []exchange
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		fake.ServeHTTP(recorder, r)
		mu.Lock()
		exchanges = append(exchanges, exchange{path: r.URL.Path, status: recorder.status})
		mu.Unlock()
	}))
	t.Cleanup(server.Close)
	newPort := func(key string) *hyper3d.Adapter {
		port, err := hyper3d.New(hyper3d.Config{
			BaseURL: server.URL + hyper3dapiv2.Prefix, APIKey: key, StatusMapping: mapping(),
			ErrorMapping: errorMapping, HTTPClient: server.Client(), AllowHTTPAssets: true, InputAssets: imageInputs{},
		})
		if err != nil {
			t.Fatal(err)
		}
		return port
	}
	reset := func() {
		mu.Lock()
		exchanges = nil
		mu.Unlock()
	}
	failureOf := func(err error) *generation.Failure {
		t.Helper()
		var providerErr *generation.ProviderError
		if !errors.As(err, &providerErr) || providerErr.Failure == nil {
			t.Fatalf("a 201 body error did not fail the submission: %v", err)
		}
		return providerErr.Failure
	}
	assertSingleCreate := func(wantPath string) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if len(exchanges) != 1 || exchanges[0].path != wantPath || exchanges[0].status != http.StatusCreated {
			t.Fatalf("exchanges = %+v, want one HTTP 201 create to %s (no polling, no replay)", exchanges, wantPath)
		}
	}

	for _, scenario := range []fakeupstream.Scenario{fakeupstream.ScenarioInsufficientBalance, fakeupstream.ScenarioKeyQuota} {
		t.Run(string(scenario), func(t *testing.T) {
			reset()
			_, err := newPort(fakeupstream.KeyPrefix+string(scenario)).Start(context.Background(), simpleHyperRequest("rodin-text", "a chair", "unused"))
			failure := failureOf(err)
			if failure.Class != generation.FailureInsufficientBalance || failure.Retryable || failure.Ambiguous || failure.UpstreamCode != "API_INSUFFICIENT_FUNDS" {
				t.Fatalf("failure = %+v", failure)
			}
			assertSingleCreate(hyper3dapiv2.Prefix + "/rodin")
		})
	}

	t.Run("source not available", func(t *testing.T) {
		reset()
		port := newPort("synthetic-key")
		accepted, err := port.Start(context.Background(), simpleHyperRequest("rodin-text", "a chair", "unused"))
		if err != nil || accepted.Job == nil {
			t.Fatalf("control submission: %+v %v", accepted, err)
		}
		reset()
		// The source job has not finished, so the fake answers 201 with
		// ASSET_NOT_AVAILABLE, like the official API.
		_, err = port.Start(context.Background(), simpleHyperRequest("bang-native", "", accepted.Job.RemoteID))
		failure := failureOf(err)
		if failure.Class != generation.FailureRejected || failure.Retryable || failure.Ambiguous || failure.UpstreamCode != "ASSET_NOT_AVAILABLE" {
			t.Fatalf("failure = %+v", failure)
		}
		assertSingleCreate(hyper3dapiv2.Prefix + "/bang")
	})
}
