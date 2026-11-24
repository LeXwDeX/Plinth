package generation_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/hyper3d"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	hyper3dv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/hyper3d/api-v2"
)

// hyper3dRetryHarness runs a Hyper3D text-to-3D task against the Hyper3D fake
// with the default Catalog's connection declaration (retry_submit_server_error)
// and the fake scenario the API key selects; creates counts the creates the
// fake received.
func hyper3dRetryHarness(t *testing.T, apiKey string, failTimes int) (*harness, generation.TaskID, *atomic.Int32) {
	t.Helper()
	fake, err := fakeupstream.New(fakeupstream.Config{FailTimes: failTimes}, hyper3dv2.Version())
	if err != nil {
		t.Fatal(err)
	}
	var creates atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/rodin") {
			creates.Add(1)
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var mapping *generation.StatusMapping
	declared := false
	for _, connection := range taskCatalog.Application().Runtime.ProviderConnections {
		if connection.AdapterKey == "hyper3d.rodin.v1" {
			mapping, declared = connection.StatusMapping, connection.RetrySubmitServerError
		}
	}
	if mapping == nil || !declared {
		t.Fatal("default Catalog has no hyper3d.rodin.v1 connection declaring retry_submit_server_error")
	}
	adapter, err := hyper3d.New(hyper3d.Config{
		BaseURL: server.URL + hyper3dv2.Prefix, APIKey: apiKey,
		StatusMapping: mapping, HTTPClient: server.Client(),
		RetrySubmitServerError: declared,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := newConfiguredHarness(t, adapter, nil, nil)
	accepted, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "hyper3d/rodin-gen-2.5/text-to-3d", Input: []byte(`{"prompt":"castle","authorize_extra_credits":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, accepted.TaskID, &creates
}

func hyper3dRetryTask(t *testing.T, h *harness, id generation.TaskID) generation.Task {
	t.Helper()
	task, err := h.module.Get(context.Background(), "owner-a", id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// #460 C25, narrowed by #563: the declared connection resends a create only
// on the upstream's explicit "not now", a 503 with Retry-After. The first
// create is refused, the second is accepted, and the task never fails.
func TestHyper3DSubmit503WithRetryAfterIsRetriedWhenDeclared(t *testing.T) {
	h, id, creates := hyper3dRetryHarness(t, "fake-service-unavailable", 1)
	h.reconcile(t)
	if task := hyper3dRetryTask(t, h, id); task.State != generation.TaskQueued {
		t.Fatalf("state = %s, want queued for the retry (failure %#v)", task.State, task.Failure)
	}
	h.reconcile(t)
	if task := hyper3dRetryTask(t, h, id); task.State == generation.TaskFailed {
		t.Fatalf("task failed instead of retrying: %#v", task.Failure)
	}
	if got := creates.Load(); got != 2 {
		t.Fatalf("creates = %d, want the refused create and its retry", got)
	}
}

// #563: a 503 refusal on every create runs out of attempts. The last failure
// is ended as an ambiguous submission: counted as possibly charged, offered
// no retry.
func TestHyper3DSubmit503RetriesExhaustedEndAmbiguous(t *testing.T) {
	h, id, creates := hyper3dRetryHarness(t, "fake-service-unavailable", 0)
	h.reconcile(t)
	h.reconcile(t)
	task := hyper3dRetryTask(t, h, id)
	if task.State != generation.TaskFailed || task.Failure == nil {
		t.Fatalf("state = %s failure %#v, want failed after the declared attempts", task.State, task.Failure)
	}
	if !task.Failure.Ambiguous || task.Failure.Retryable || task.Failure.Class != generation.FailureSubmissionUnknown {
		t.Fatalf("exhausted retries must end ambiguous: %#v", task.Failure)
	}
	if task.Failure.UpstreamStatus != http.StatusServiceUnavailable {
		t.Fatalf("the upstream evidence was lost: %#v", task.Failure)
	}
	if got := creates.Load(); got != 2 {
		t.Fatalf("creates = %d, want max_attempts", got)
	}
}

// #563: Rodin accepted (and billed) the create, then the gateway answered 502.
// A 5xx is not a refusal: the task ends at once as an ambiguous submission and
// the create is never sent again.
func TestHyper3DSubmitAcceptedThen502IsNeverResent(t *testing.T) {
	h, id, creates := hyper3dRetryHarness(t, "fake-accepted-502", 0)
	h.reconcile(t)
	h.reconcile(t)
	task := hyper3dRetryTask(t, h, id)
	if task.State != generation.TaskFailed || task.Failure == nil ||
		!task.Failure.Ambiguous || task.Failure.Retryable || task.Failure.Class != generation.FailureSubmissionUnknown {
		t.Fatalf("state = %s failure %#v, want an ambiguous submission", task.State, task.Failure)
	}
	if got := creates.Load(); got != 1 {
		t.Fatalf("creates = %d, an accepted create was sent again", got)
	}
}
