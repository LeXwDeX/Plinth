package tripo_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	tripov3 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/tripo/v3"
)

// #563: the primary model of a mixed Offering finished (Tripo billed it) and
// the pre-completion conversion POST reached Tripo, but its response was lost
// (the v3 fake keeps the task and drops the connection). The next observation
// reads only the dispatch intent: the outcome is unknown, so the task ends as
// an ambiguous submission (counted as possibly charged, never retried), not a
// rejection, and the conversion is not sent again.
func TestPreCompletionConversionWithLostResponseEndsAmbiguous563(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	fake, err := fakeupstream.New(fakeupstream.Config{}, tripov3.Version())
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	converts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/models/convert") {
			mu.Lock()
			converts++
			if converts == 1 {
				r.Header.Set(fakeupstream.ScenarioHeader, string(fakeupstream.ScenarioLostResponse))
			}
			mu.Unlock()
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	convertCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return converts
	}
	adapter := exportAdapter(t, server)
	offering, _ := c.Get(exportOfferingID)
	input := exportInput(t, c, exportOfferingID, gamePurposeExport)
	checkpoints := &recordingCheckpointer{}
	ctx := generation.WithDispatchCheckpointer(context.Background(), checkpoints)
	request := generation.ObserveRequest{
		AttemptID: "attempt-563", Offering: offering, Input: input,
		Job: generation.UpstreamJob{ProviderID: "tripo", RemoteID: createBaseTask(t, server)},
	}
	lost := false
	for range 60 {
		observation, err := adapter.Observe(ctx, request)
		if convertCount() > 0 {
			if err == nil {
				t.Fatalf("a lost conversion response must not read as submitted: %+v", observation)
			}
			lost = true
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		request.Opaque = observation.Opaque
	}
	if !lost {
		t.Fatal("the pre-completion conversion was never sent")
	}

	request.Opaque = json.RawMessage(checkpoints.intent)
	observation, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Phase != generation.UpstreamFailed || observation.Failure == nil {
		t.Fatalf("observation = phase %s failure %+v", observation.Phase, observation.Failure)
	}
	if !observation.Failure.Ambiguous || observation.Failure.Retryable ||
		observation.Failure.Class != generation.FailureSubmissionUnknown {
		t.Fatalf("an unknown conversion submission must be ambiguous: %+v", *observation.Failure)
	}
	if got := convertCount(); got != 1 {
		t.Fatalf("convert posts = %d, the conversion was sent again", got)
	}
}
