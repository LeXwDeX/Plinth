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
	"reflect"
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

// #301: a dev-test stack reaches the matting fake through the adapter the
// application registers, with the fixed fake key and no test header, and the
// pictures a person (or a smoke script) uploads are stored as uploaded. So a
// picture that carries a fault marker in its own bytes must still carry it after
// the input store, and the registered adapter must hand it to the fake unchanged:
// the paths of #284 (the notice naming the pictures that were not matted) and
// #299 (a picture the GPU cannot hold) then follow from the pictures alone, with
// no branch in the production code for it.
func TestUploadedFaultMarkersReachTheFakeThroughTheRegisteredAdapter(t *testing.T) {
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
	untouched := func(_ string, base http.RoundTripper) http.RoundTripper { return base }
	if err := registerProvidersWithTransport(Config{ProviderMode: "live", RuntimeSettings: settings},
		product, providerRegistry, inputs, blobs, discardLogger(), untouched); err != nil {
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

	upload := func(t *testing.T, width, height int, faults ...string) generation.InputAsset {
		t.Helper()
		var picture bytes.Buffer
		if err := png.Encode(&picture, image.NewGray(image.Rect(0, 0, width, height))); err != nil {
			t.Fatal(err)
		}
		data := picture.Bytes()
		if len(faults) > 0 {
			if data, err = mattingapiv1.MarkPNG(data, faults...); err != nil {
				t.Fatal(err)
			}
		}
		asset, err := inputs.Upload(context.Background(), "owner", bytes.NewReader(data), "image/png", 1<<26)
		if err != nil {
			t.Fatal(err)
		}
		return asset
	}
	observe := func(t *testing.T, assets ...generation.InputAsset) generation.Observation {
		t.Helper()
		ids := make([]generation.AssetID, len(assets))
		for index, asset := range assets {
			ids[index] = asset.ID
		}
		input, err := json.Marshal(map[string]any{"source_assets": ids})
		if err != nil {
			t.Fatal(err)
		}
		started, err := port.Start(context.Background(), generation.StartRequest{
			OwnerID: "owner", TaskID: "task-301", AttemptID: "attempt-301", SubmissionKey: "submission-301",
			Offering: offering, Input: input, InputAssets: map[string][]generation.InputAsset{"source_assets": assets},
		})
		if err != nil || started.Job == nil {
			t.Fatalf("the fake refused the batch: %+v %v", started, err)
		}
		var observed generation.Observation
		for range 8 {
			observed, err = port.Observe(context.Background(), generation.ObserveRequest{
				AttemptID: "attempt-301", Offering: offering, Job: *started.Job,
			})
			if err != nil {
				t.Fatal(err)
			}
			if observed.Phase == generation.UpstreamCompleted || observed.Phase == generation.UpstreamFailed {
				return observed
			}
		}
		t.Fatalf("the batch never finished: %+v", observed)
		return observed
	}

	t.Run("the pictures that were not matted are named", func(t *testing.T) {
		completed := observe(t,
			upload(t, 16, 16), upload(t, 16, 16, mattingapiv1.FaultFailed), upload(t, 16, 16), upload(t, 16, 16, mattingapiv1.FaultRejected))
		want := []generation.TaskNotice{{Code: generation.NoticePartialInputs, Inputs: []int{2, 4}}}
		if completed.Phase != generation.UpstreamCompleted || len(completed.Artifacts) != 2 || !reflect.DeepEqual(completed.Notices, want) {
			t.Fatalf("observation = %+v, want a completion with two pictures and the notice %+v", completed, want)
		}
	})

	t.Run("a batch whose only picture ran out of memory is a capacity failure that is not retried", func(t *testing.T) {
		failed := observe(t, upload(t, 2048, 2048, mattingapiv1.FaultGPUOutOfMemory))
		failure := failed.Failure
		if failed.Phase != generation.UpstreamFailed || failure == nil {
			t.Fatalf("observation = %+v, want a failed one", failed)
		}
		if failure.Class != generation.FailureUpstreamUnavailable || failure.Retryable || failure.Ambiguous {
			t.Fatalf("failure = %+v, want a definite upstream_unavailable that is not retried", failure)
		}
		if want := mattingapiv1.OutOfMemoryMessage(2048, 2048); failure.SafeDetail != want {
			t.Fatalf("detail = %q, want the service's own text %q", failure.SafeDetail, want)
		}
	})

	t.Run("a picture without a marker is matted and says nothing", func(t *testing.T) {
		completed := observe(t, upload(t, 16, 16), upload(t, 16, 16))
		if completed.Phase != generation.UpstreamCompleted || len(completed.Artifacts) != 2 || len(completed.Notices) != 0 {
			t.Fatalf("observation = %+v", completed)
		}
	})
}
