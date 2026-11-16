package matting_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/matting"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	mattingapiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/matting/api-v1"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

// markedPNG is a real PNG of the given size whose own bytes ask the matting fake
// for the given faults (#301); with no fault it is a plain picture.
func markedPNG(t *testing.T, width, height int, faults ...string) []byte {
	t.Helper()
	data := pngOf(t, width, height)
	if len(faults) == 0 {
		return data
	}
	marked, err := mattingapiv1.MarkPNG(data, faults...)
	if err != nil {
		t.Fatal(err)
	}
	return marked
}

// observePictures runs the pictures through the real adapter the way a deployed
// dev-test stack does: the request carries no X-Fake-File-Faults header and no
// scenario, the key is the fixed fake key, and the adapter is the shipped one with
// the shipped Offering and the shipped error vocabulary. Whatever becomes of a
// picture comes from the picture's own bytes.
func observePictures(t *testing.T, pictures ...[]byte) generation.Observation {
	t.Helper()
	_, mapping := shippedMapping(t)
	f := testkit.New(t, fakeupstream.Config{}, mattingapiv1.Version())
	inputs := sizedPictures{}
	ids := make([]string, len(pictures))
	assets := make([]generation.InputAsset, len(pictures))
	for index, data := range pictures {
		id := generation.AssetID(fmt.Sprintf("asset-%d", index+1))
		inputs[id] = data
		ids[index] = string(id)
		assets[index] = generation.InputAsset{ID: id, MediaType: "image/png"}
	}
	adapter, err := matting.New(matting.Config{
		BaseURL: f.Server.URL + "/matting", APIKey: "fake-test-key",
		InputAssets: inputs, ErrorMapping: mapping,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, offering := shippedOffering(t)
	input, err := json.Marshal(map[string]any{"source_assets": ids})
	if err != nil {
		t.Fatal(err)
	}
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		AttemptID: "attempt-301", SubmissionKey: "submission-301", Offering: offering,
		Input: input, InputAssets: map[string][]generation.InputAsset{"source_assets": assets},
	})
	if err != nil || started.Job == nil {
		t.Fatalf("the fake refused the batch: %+v %v", started, err)
	}
	var observed generation.Observation
	for range 8 {
		observed, err = adapter.Observe(context.Background(), generation.ObserveRequest{
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

// #301: the paths of #284 (the notice that names the pictures that were not
// matted) and #299 (a picture the GPU cannot hold) are reachable with nothing
// but the pictures, through the adapter exactly as it ships: no header, no
// scenario, no branch of the adapter for tests.
func TestMarkedPicturesChooseTheOutcomeThroughTheShippedAdapter(t *testing.T) {
	partial := func(inputs ...int) []generation.TaskNotice {
		return []generation.TaskNotice{{Code: generation.NoticePartialInputs, Inputs: inputs}}
	}
	for _, test := range []struct {
		name      string
		pictures  [][]byte
		artifacts int
		notices   []generation.TaskNotice
	}{
		{"a picture that fails and one that is refused are named", [][]byte{
			markedPNG(t, 16, 16), markedPNG(t, 16, 16, mattingapiv1.FaultFailed), markedPNG(t, 16, 16), markedPNG(t, 16, 16, mattingapiv1.FaultRejected),
		}, 2, partial(2, 4)},
		{"the batch of the real run: a 2048x2048 picture that ran out of memory next to a matted one", [][]byte{
			markedPNG(t, 2048, 2048, mattingapiv1.FaultGPUOutOfMemory), markedPNG(t, 16, 16),
		}, 1, partial(1)},
		{"pictures without a marker are matted without a notice", [][]byte{markedPNG(t, 16, 16), markedPNG(t, 16, 16)}, 2, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			completed := observePictures(t, test.pictures...)
			if completed.Phase != generation.UpstreamCompleted || completed.Failure != nil || len(completed.Artifacts) != test.artifacts {
				t.Fatalf("observation = %+v", completed)
			}
			if !reflect.DeepEqual(completed.Notices, test.notices) {
				t.Fatalf("notices = %+v, want %+v", completed.Notices, test.notices)
			}
		})
	}
}

// A batch whose only picture ran out of memory is the Task's failure, the
// Schema's capacity class that is not retried, and the service's own text is
// what the failure keeps (#299).
func TestMarkedPictureThatRanOutOfMemoryFailsTheTaskAsACapacityFailure(t *testing.T) {
	observed := observePictures(t, markedPNG(t, 2048, 2048, mattingapiv1.FaultGPUOutOfMemory))
	failure := observed.Failure
	if observed.Phase != generation.UpstreamFailed || failure == nil {
		t.Fatalf("observation = %+v, want a failed one", observed)
	}
	if failure.Class != generation.FailureUpstreamUnavailable || failure.Retryable || failure.Ambiguous {
		t.Fatalf("failure = %+v, want a definite upstream_unavailable that is not retried", failure)
	}
	if failure.SafeDetail != realOutOfMemory {
		t.Fatalf("detail = %q, want %q", failure.SafeDetail, realOutOfMemory)
	}
}
