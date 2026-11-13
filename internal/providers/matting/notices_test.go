package matting_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/matting"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	mattingapiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/matting/api-v1"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

// requestHeaders adds the fake's per-request controls (a scenario, or the faults
// of individual files) to every request the adapter sends. The fake keeps what
// the creating request asked for, so the later polls need no help.
type requestHeaders struct {
	next    http.RoundTripper
	headers map[string]string
}

func (transport requestHeaders) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	for name, value := range transport.headers {
		clone.Header.Set(name, value)
	}
	return transport.next.RoundTrip(clone)
}

// faults asks the shared matting fake for what becomes of individual pictures:
// "2:rejected,4:failed" refuses the second and fails the fourth, "3:renamed"
// has the service answer the third under a name of its own.
func faults(spec string) map[string]string {
	return map[string]string{mattingapiv1.FileFaultsHeader: spec}
}

// observeBatch submits the given number of pictures through the real adapter to
// the shared matting fake, asked for the given headers, and returns the
// completion the adapter observes once the batch has finished.
func observeBatch(t *testing.T, pictures int, headers map[string]string) generation.Observation {
	t.Helper()
	f := testkit.New(t, fakeupstream.Config{}, mattingapiv1.Version())
	adapter, err := matting.New(matting.Config{
		BaseURL: f.Server.URL + "/matting", APIKey: "matting-key",
		HTTPClient:  &http.Client{Transport: requestHeaders{next: http.DefaultTransport, headers: headers}},
		InputAssets: memoryInputs{content: []byte("picture")},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, offering := shippedOffering(t)
	mediaTypes := []string{"image/png", "image/jpeg", "image/webp"}
	ids, assets := make([]string, pictures), make([]generation.InputAsset, pictures)
	for index := range ids {
		ids[index] = fmt.Sprintf("asset-%d", index+1)
		assets[index] = generation.InputAsset{ID: generation.AssetID(ids[index]), MediaType: mediaTypes[index%len(mediaTypes)]}
	}
	input, err := json.Marshal(map[string]any{"source_assets": ids})
	if err != nil {
		t.Fatal(err)
	}
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		AttemptID: "attempt-notices", SubmissionKey: "submission-notices", Offering: offering,
		Input: input, InputAssets: map[string][]generation.InputAsset{"source_assets": assets},
	})
	if err != nil || started.Job == nil {
		t.Fatalf("the fake refused the batch: %+v %v", started, err)
	}
	var observed generation.Observation
	for range 8 {
		observed, err = adapter.Observe(context.Background(), generation.ObserveRequest{
			AttemptID: "attempt-notices", Offering: offering, Job: *started.Job,
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

// #284: a completed batch that holds fewer results than pictures says which
// pictures are missing, by the position each had among the submitted ones. The
// batch is the shared matting fake's: pictures 2 and 4 of four are missing, one
// refused by the service and one failed while processing, and the two between
// them were matted.
func TestPartialSuccessNoticeNamesEveryPictureThatWasNotMatted(t *testing.T) {
	completed := observeBatch(t, 4, faults("2:rejected,4:failed"))
	if completed.Phase != generation.UpstreamCompleted || len(completed.Artifacts) != 2 {
		t.Fatalf("completed = %+v", completed)
	}
	want := []generation.TaskNotice{{Code: generation.NoticePartialInputs, Inputs: []int{2, 4}}}
	if !reflect.DeepEqual(completed.Notices, want) {
		t.Fatalf("notices = %+v, want %+v", completed.Notices, want)
	}
}

// A picture the service did not echo a position for cannot be named, and a
// list that left it out would be untrue, so the notice names none. Here picture
// 2 was refused under the name it was uploaded with and picture 3 failed under
// a name of the service's own.
func TestPartialSuccessNoticeDoesNotNameSomeWhenOneCannotBeNamed(t *testing.T) {
	completed := observeBatch(t, 3, faults("2:rejected,3:failed,3:renamed"))
	if completed.Phase != generation.UpstreamCompleted || len(completed.Artifacts) != 1 {
		t.Fatalf("completed = %+v", completed)
	}
	want := []generation.TaskNotice{{Code: generation.NoticePartialInputs}}
	if !reflect.DeepEqual(completed.Notices, want) {
		t.Fatalf("notices = %+v, want %+v", completed.Notices, want)
	}
}

// A batch in which every picture was matted says nothing; a batch in which
// none was is a failure, not a success with a notice, whether the service
// refused the pictures or failed them.
func TestNoNoticeWhenEveryPictureWasMattedOrNoneWas(t *testing.T) {
	all := observeBatch(t, 2, nil)
	if all.Phase != generation.UpstreamCompleted || len(all.Artifacts) != 2 || len(all.Notices) != 0 {
		t.Fatalf("a full success = %+v", all)
	}
	// A renamed picture that was matted is not a missing one.
	renamed := observeBatch(t, 2, faults("2:renamed"))
	if renamed.Phase != generation.UpstreamCompleted || len(renamed.Artifacts) != 2 || len(renamed.Notices) != 0 {
		t.Fatalf("a full success under another name = %+v", renamed)
	}
	for name, spec := range map[string]string{"refused": "1:rejected,2:rejected", "failed": "1:failed,2:failed"} {
		none := observeBatch(t, 2, faults(spec))
		if none.Phase != generation.UpstreamFailed || none.Failure == nil || len(none.Artifacts) != 0 || len(none.Notices) != 0 {
			t.Fatalf("a batch with no result (%s) = %+v", name, none)
		}
	}
}

// Against the fake's partial-images scenario, which accepts the first picture
// and refuses the rest, the adapter's completion names exactly the refused
// positions, read back from the filenames the fake echoes.
func TestPartialSuccessNoticeAgainstTheFake(t *testing.T) {
	completed := observeBatch(t, 3, map[string]string{fakeupstream.ScenarioHeader: string(fakeupstream.ScenarioPartialImages)})
	if completed.Phase != generation.UpstreamCompleted || len(completed.Artifacts) != 1 {
		t.Fatalf("completed = %+v", completed)
	}
	want := []generation.TaskNotice{{Code: generation.NoticePartialInputs, Inputs: []int{2, 3}}}
	if !reflect.DeepEqual(completed.Notices, want) {
		t.Fatalf("notices = %+v, want %+v", completed.Notices, want)
	}
}
