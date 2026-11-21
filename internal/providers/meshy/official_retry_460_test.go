package meshy_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	meshyopenapiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/openapi-v1"
	meshyopenapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/openapi-v2"
)

// #460 B20: the Meshy create request uses the official 60 s (120 s above
// 100 MB) instead of the connection's 1800 s. A delayed create must end as an
// ambiguous transport failure; a poll keeps the connection timeout.
func TestMeshyCreateHonoursSubmitTimeout(t *testing.T) {
	upstream := newMeshyFakeServer(t, fakeupstream.Config{}, 100)
	taskCatalog, offering := upgradeOffering(t, "meshy/meshy-7.1/text-to-3d", upstream)
	input := normalizedOfferingInput(t, taskCatalog, offering, `{"prompt":"a small robot"}`)
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL: upstream.URL + "/meshy", APIKey: "test", HTTPClient: upstream.Client(),
		SubmitTimeout: 10 * time.Millisecond,
	})
	_, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering: offering,
		Input:    input,
	})
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil ||
		provider.Failure.Class != generation.FailureTransport || !provider.Failure.Ambiguous {
		t.Fatalf("error = %v, want an ambiguous transport failure", err)
	}
}

// #460 B21: a non-terminal poll uses the Retry-After header (7 s here) rather
// than the Offering's 5 s interval.
func TestMeshyNonTerminalPollHonoursRetryAfter(t *testing.T) {
	upstream := newMeshyFakeServer(t, fakeupstream.Config{PollRetryAfter: 7 * time.Second})
	taskCatalog, offering := upgradeOffering(t, "meshy/meshy-7.1/text-to-3d", upstream)
	input := normalizedOfferingInput(t, taskCatalog, offering, `{"prompt":"a small robot"}`)
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL: upstream.URL + "/meshy", APIKey: "test", HTTPClient: upstream.Client(),
	})
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering: offering,
		Input:    input,
	})
	if err != nil || started.Job == nil {
		t.Fatalf("start = %+v, %v", started, err)
	}
	observed, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		TaskID: "task", AttemptID: "attempt", Offering: offering, Job: *started.Job,
		Opaque: started.Opaque,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.PollAfter != 7*time.Second {
		t.Fatalf("PollAfter = %v, want the Retry-After 7s", observed.PollAfter)
	}
}

func normalizedOfferingInput(t *testing.T, taskCatalog *catalog.Catalog, offering generation.Offering, raw string) []byte {
	t.Helper()
	_, normalized, err := taskCatalog.Normalize(offering.Capability, offering.ID, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return normalized
}

// newMeshyFakeServer mounts both Meshy route versions and adds a create delay
// when the test asks for one.
func newMeshyFakeServer(t *testing.T, config fakeupstream.Config, delayMillis ...int) *httptest.Server {
	t.Helper()
	fake, err := fakeupstream.New(config, meshyopenapiv1.Version(), meshyopenapiv2.Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(delayMillis) > 0 && r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/text-to-3d") {
			r.Header.Set(fakeupstream.DelayHeader, strconv.Itoa(delayMillis[0]))
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}
