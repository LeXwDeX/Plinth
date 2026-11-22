package generation_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/hyper3d"
)

// #460 C25: a 5xx answer to a Hyper3D create is retried when the connection
// declares it. The first create fails, the second is accepted, and the task
// never becomes a terminal failure.
func TestHyper3DSubmit5xxIsRetriedWhenDeclared(t *testing.T) {
	var creates atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hyper3d/api/v2/rodin" {
			http.NotFound(w, r)
			return
		}
		if creates.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":"Internal Server Error","statusCode":500,"message":["Internal server error"]}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"message":"Submitted.","uuid":"11111111-1111-4111-8111-111111111111","jobs":{"uuids":["22222222-2222-4222-8222-222222222222"],"subscription_key":"sub-key"},"consumed":0.5}`)
	}))
	t.Cleanup(server.Close)

	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var mapping *generation.StatusMapping
	for _, connection := range taskCatalog.Application().Runtime.ProviderConnections {
		if connection.AdapterKey == "hyper3d.rodin.v1" {
			mapping = connection.StatusMapping
		}
	}
	if mapping == nil {
		t.Fatal("default Catalog has no hyper3d.rodin.v1 connection")
	}
	adapter, err := hyper3d.New(hyper3d.Config{
		BaseURL: server.URL + "/hyper3d/api/v2", APIKey: "test",
		StatusMapping: mapping, HTTPClient: server.Client(),
		RetrySubmitServerError: true,
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
	h.reconcile(t)
	task, err := h.module.Get(context.Background(), "owner-a", accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskQueued {
		t.Fatalf("state = %s, want queued for the retry (failure %#v)", task.State, task.Failure)
	}
	h.reconcile(t)
	task, err = h.module.Get(context.Background(), "owner-a", accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State == generation.TaskFailed {
		t.Fatalf("task failed instead of retrying: %#v", task.Failure)
	}
	if got := creates.Load(); got != 2 {
		t.Fatalf("creates = %d, want the failed create and its retry", got)
	}
}
