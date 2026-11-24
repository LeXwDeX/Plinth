package tripo_test

// #541: the business errors and rate-limit answers of the Tripo error-handling and
// rate-limits pages (read 2026-11-24), raised by the Tripo v3 fake, are classified as
// the Catalog's error_mapping declares. The fake words are in fakeupstream/tripo/v3
// (fake.go businessErrors, rate-limit-reset-only).

import (
	"context"
	"errors"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	tripov3 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/tripo/v3"
)

// fakeP2Adapter is the shared adapter over a fake, authenticating with apiKey (a
// "fake-<scenario>" key selects the injected fault) and the P2 text Offering whose frozen
// input the fake accepts.
func fakeP2Adapter(t *testing.T, apiKey string, config fakeupstream.Config) (*tripo.Adapter, generation.Offering, []byte) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, found := product.Get(schema.OfferingID("tripo/tripo-p2/text-to-3d"))
	if !found {
		t.Fatal("P2 text Offering missing")
	}
	fake, err := fakeupstream.New(config, tripov3.Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	errorMapping, statusMapping := declaredTripoMappings(t)
	adapter, err := tripo.New(tripo.Config{
		BaseURL: server.URL + tripov3.Prefix, APIKey: apiKey, HTTPClient: server.Client(),
		AllowHTTPAssets: true, OfferingRoutes: []generation.Offering{offering},
		ErrorMapping: errorMapping, StatusMapping: statusMapping,
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter, offering, p2FrozenInput(t, offering, map[string]any{"prompt": "a game-ready knight", "face_limit": 10000})
}

// startFailure starts a P2 text task against the fake and returns the refusal.
func startFailure(t *testing.T, apiKey string, config fakeupstream.Config) *generation.Failure {
	t.Helper()
	adapter, offering, input := fakeP2Adapter(t, apiKey, config)
	_, err := adapter.Start(context.Background(), generation.StartRequest{
		AttemptID: "attempt-541", SubmissionKey: "submit-541", Offering: offering, Input: input,
	})
	if err == nil {
		t.Fatalf("%s: the fake accepted a faulted creation", apiKey)
	}
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil {
		t.Fatalf("%s: err = %v, want a provider failure", apiKey, err)
	}
	return provider.Failure
}

func TestTripoBusinessErrorsOfTheFakeAreClassifiedBySchema(t *testing.T) {
	mapping, _ := declaredTripoMappings(t)
	for _, tc := range []struct {
		word      string
		code      string
		class     generation.FailureClass
		retryable bool
		// declared is false for 4001, which the Catalog does not list: the HTTP status
		// classifies it.
		declared bool
	}{
		{"content-policy", "2008", generation.FailureRejected, false, true},
		{"version-deprecated", "2015", generation.FailureInvalidInput, false, true},
		{"model-too-complex", "2018", generation.FailureRejected, false, true},
		{"task-type-unsupported", "2002", generation.FailureInvalidInput, false, true},
		{"endpoint-not-found", "4001", generation.FailureRejected, false, false},
		{"insufficient-balance", "2010", generation.FailureInsufficientBalance, false, true},
	} {
		t.Run(tc.word, func(t *testing.T) {
			if _, declared := mapping.UpstreamCodeMap[tc.code]; declared != tc.declared {
				t.Fatalf("Catalog declares %s = %v, want %v: the fake word and the mapping disagree", tc.code, declared, tc.declared)
			}
			failure := startFailure(t, "fake-"+tc.word, fakeupstream.Config{})
			if failure.Class != tc.class || failure.Retryable != tc.retryable || failure.UpstreamCode != tc.code {
				t.Fatalf("failure = class %q retryable %v code %q, want %q %v %q",
					failure.Class, failure.Retryable, failure.UpstreamCode, tc.class, tc.retryable, tc.code)
			}
		})
	}
}

// A request-rate refusal carries a wait: Retry-After when the answer has it, the window's
// end (X-RateLimit-Reset, a Unix timestamp) when that is all it has, nothing when the
// answer has neither, so the caller's own backoff applies. The reset path (#487) is only
// reachable against the fake because the fake now writes a timestamp, not relative seconds.
func TestTripoRateLimitWaitsFollowTheHeadersOfTheFake(t *testing.T) {
	config := fakeupstream.Config{RetryAfter: 8 * time.Second}
	for _, tc := range []struct {
		word     string
		min, max time.Duration
	}{
		{"rate-limit", 8 * time.Second, 8 * time.Second},
		{"rate-limit-reset-only", 5 * time.Second, 9 * time.Second},
		{"rate-limit-no-retry-after", 0, 0},
		{"concurrency-limit", 8 * time.Second, 8 * time.Second},
	} {
		t.Run(tc.word, func(t *testing.T) {
			failure := startFailure(t, "fake-"+tc.word, config)
			if failure.Class != generation.FailureRateLimited || !failure.Retryable {
				t.Fatalf("failure = %q retryable %v, want a retryable rate limit", failure.Class, failure.Retryable)
			}
			if failure.RetryAfter < tc.min || failure.RetryAfter > tc.max {
				t.Fatalf("RetryAfter = %v, want %v to %v", failure.RetryAfter, tc.min, tc.max)
			}
			wantCode := "1007"
			if tc.word == "concurrency-limit" {
				wantCode = "2000"
			}
			if failure.UpstreamCode != wantCode {
				t.Fatalf("code = %q, want %s", failure.UpstreamCode, wantCode)
			}
		})
	}
}

// A task that ends failed with a business code keeps the code as evidence and is not
// reclassified from the request-level table (#487): the task-level table declares only
// the two server errors.
func TestTripoTaskLevelBusinessErrorsKeepTheirCodeAsEvidence(t *testing.T) {
	for word, code := range map[string]int{"content-policy": 2008, "model-too-complex": 2018} {
		t.Run(word, func(t *testing.T) {
			adapter, offering, input := fakeP2Adapter(t, "fake-task-failed-"+word, fakeupstream.Config{AccountConcurrencyLimit: -1})
			started, err := adapter.Start(context.Background(), generation.StartRequest{
				AttemptID: "attempt-541", SubmissionKey: "submit-541", Offering: offering, Input: input,
			})
			if err != nil || started.Job == nil {
				t.Fatalf("the creation of a task that fails later was refused: %v", err)
			}
			request := generation.ObserveRequest{TaskID: "task-541", AttemptID: "attempt-541", Offering: offering, Job: *started.Job, Input: input}
			for range 10 {
				observed, err := adapter.Observe(context.Background(), request)
				if err != nil {
					t.Fatal(err)
				}
				request.Opaque = observed.Opaque
				if observed.Phase != generation.UpstreamFailed {
					continue
				}
				failure := observed.Failure
				if failure == nil || failure.UpstreamCode != strconv.Itoa(code) || failure.Class != generation.FailureRejected || failure.Retryable {
					t.Fatalf("failure = %+v, want a non-retryable rejection carrying code %d", failure, code)
				}
				return
			}
			t.Fatal("the task never failed")
		})
	}
}
