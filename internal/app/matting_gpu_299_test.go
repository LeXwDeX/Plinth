package app

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	mattingapiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/matting/api-v1"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/registry"
)

// requestHeader adds one header to every request that passes: the fake's own
// per-request controls, which a deployed stack never sends.
type requestHeader struct {
	next        http.RoundTripper
	name, value string
}

func (transport requestHeader) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header.Set(transport.name, transport.value)
	return transport.next.RoundTrip(clone)
}

// #299: the failure vocabulary the Catalog declares for the matting connection
// reaches the adapter the application builds from it. The service runs out of
// GPU memory on a 2048x2048 picture (the shared matting fake does, for the file
// asked to), and the Task's failure is the Schema's capacity class that is not
// retried, not the refusal it was: the words live in the Schema and only the
// application's registration can hand them to the adapter.
func TestRegisteredMattingAdapterClassifiesGPUOutOfMemoryFromTheCatalog(t *testing.T) {
	fake, err := registry.New(fakeupstream.Config{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	repository, err := sqlitetest.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	blobs, err := local.New(filepath.Join(root, "cas"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blobs.Close() })
	inputs, err := inputassets.New(repository, blobs)
	if err != nil {
		t.Fatal(err)
	}
	providerRegistry := providers.NewRegistry()
	settings := map[string]string{"matting_base_url": server.URL + "/matting", "matting_api_key": fakeUpstreamKeyPrefix + "test-key"}
	withFault := func(connectionID string, base http.RoundTripper) http.RoundTripper {
		if connectionID != "matting" {
			return base
		}
		return requestHeader{next: base, name: mattingapiv1.FileFaultsHeader, value: "1:gpu_oom"}
	}
	if err := registerProvidersWithTransport(Config{ProviderMode: "live", RuntimeSettings: settings},
		product, providerRegistry, inputs, blobs, discardLogger(), withFault); err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get("matting/default/background-removal")
	if !ok {
		t.Fatal("background-removal offering missing")
	}
	port, ok := providerRegistry.Provider(offering)
	if !ok {
		t.Fatal("the matting adapter is not registered")
	}

	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewGray(image.Rect(0, 0, 2048, 2048))); err != nil {
		t.Fatal(err)
	}
	asset, err := inputs.Upload(context.Background(), "owner", &picture, "image/png", 1<<26)
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{"source_assets": []generation.AssetID{asset.ID}})
	if err != nil {
		t.Fatal(err)
	}
	started, err := port.Start(context.Background(), generation.StartRequest{
		OwnerID: "owner", TaskID: "task-299", AttemptID: "attempt-299", SubmissionKey: "submission-299",
		Offering: offering, Input: input, InputAssets: map[string][]generation.InputAsset{"source_assets": {asset}},
	})
	if err != nil || started.Job == nil {
		t.Fatalf("the fake refused the batch: %+v %v", started, err)
	}
	var observed generation.Observation
	for range 8 {
		observed, err = port.Observe(context.Background(), generation.ObserveRequest{
			AttemptID: "attempt-299", Offering: offering, Job: *started.Job,
		})
		if err != nil {
			t.Fatal(err)
		}
		if observed.Phase == generation.UpstreamFailed || observed.Phase == generation.UpstreamCompleted {
			break
		}
	}
	failure := observed.Failure
	if observed.Phase != generation.UpstreamFailed || failure == nil {
		t.Fatalf("observation = %+v, want a failed one", observed)
	}
	if failure.Class != generation.FailureUpstreamUnavailable || failure.Retryable || failure.Ambiguous {
		t.Fatalf("failure = %+v, want a definite upstream_unavailable that is not retried", failure)
	}
	if want := mattingapiv1.OutOfMemoryMessage(2048, 2048); failure.SafeDetail != want {
		t.Fatalf("detail = %q, want the service's own text %q", failure.SafeDetail, want)
	}
}
