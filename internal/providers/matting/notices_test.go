package matting_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/matting"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	mattingapiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/matting/api-v1"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

// observeBatch serves one batch document of the given tasks
// and returns the completion the adapter observes from it.
func observeBatch(t *testing.T, batchStatus string, tasks []map[string]any) generation.Observation {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"batch_id": "batch-1", "batch_status": batchStatus, "tasks": tasks})
	}))
	defer upstream.Close()
	observed, err := newAdapter(t, upstream, nil).Observe(context.Background(), generation.ObserveRequest{
		AttemptID: "attempt-1", Offering: defaultOffering(),
		Job: generation.UpstreamJob{ProviderID: "matting", RemoteID: "batch-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return observed
}

func succeeded(id, name string) map[string]any {
	return map[string]any{
		"task_id": id, "status": "SUCCEEDED", "downloadable": true, "source_name": name,
		"download_path": "/api/v1/tasks/" + id + "/download",
	}
}

func notMatted(id, status, name string) map[string]any {
	return map[string]any{"task_id": id, "status": status, "downloadable": false, "source_name": name, "invalid_reason": "refused"}
}

// #284: a completed batch that holds fewer results than pictures says which
// pictures are missing, by the position each had among the submitted ones.
func TestPartialSuccessNoticeNamesEveryPictureThatWasNotMatted(t *testing.T) {
	completed := observeBatch(t, "PARTIAL_SUCCESS", []map[string]any{
		succeeded("t-1", "input_1.png"),
		notMatted("t-2", "REJECTED", "input_2.jpg"),
		succeeded("t-3", "input_3.png"),
		notMatted("t-4", "FAILED", "input_4.png"),
	})
	if completed.Phase != generation.UpstreamCompleted || len(completed.Artifacts) != 2 {
		t.Fatalf("completed = %+v", completed)
	}
	want := []generation.TaskNotice{{Code: generation.NoticePartialInputs, Inputs: []int{2, 4}}}
	if !reflect.DeepEqual(completed.Notices, want) {
		t.Fatalf("notices = %+v, want %+v", completed.Notices, want)
	}
}

// A picture the service did not echo a position for cannot be named, and a
// list that left it out would be untrue, so the notice names none.
func TestPartialSuccessNoticeDoesNotNameSomeWhenOneCannotBeNamed(t *testing.T) {
	completed := observeBatch(t, "PARTIAL_SUCCESS", []map[string]any{
		succeeded("t-1", "input_1.png"),
		notMatted("t-2", "REJECTED", "input_2.jpg"),
		notMatted("t-3", "FAILED", "renamed-by-the-service.png"),
	})
	want := []generation.TaskNotice{{Code: generation.NoticePartialInputs}}
	if !reflect.DeepEqual(completed.Notices, want) {
		t.Fatalf("notices = %+v, want %+v", completed.Notices, want)
	}
}

// A batch in which every picture was matted says nothing; a batch in which
// none was is a failure, not a success with a notice.
func TestNoNoticeWhenEveryPictureWasMattedOrNoneWas(t *testing.T) {
	all := observeBatch(t, "SUCCESS", []map[string]any{succeeded("t-1", "input_1.png"), succeeded("t-2", "input_2.png")})
	if all.Phase != generation.UpstreamCompleted || len(all.Notices) != 0 {
		t.Fatalf("a full success = %+v", all)
	}
	none := observeBatch(t, "PARTIAL_SUCCESS", []map[string]any{
		notMatted("t-1", "REJECTED", "input_1.png"), notMatted("t-2", "REJECTED", "input_2.png"),
	})
	if none.Phase != generation.UpstreamFailed || none.Failure == nil || len(none.Notices) != 0 {
		t.Fatalf("a batch with no result = %+v", none)
	}
}

// scenarioTransport asks the fake for one of its scenarios on every request.
type scenarioTransport struct {
	next     http.RoundTripper
	scenario string
}

func (transport scenarioTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header.Set("X-Fake-Scenario", transport.scenario)
	return transport.next.RoundTrip(clone)
}

// Against the fake's partial-images scenario, which accepts the first picture
// and refuses the rest, the adapter's completion names exactly the refused
// positions, read back from the filenames the fake echoes.
func TestPartialSuccessNoticeAgainstTheFake(t *testing.T) {
	f := testkit.New(t, fakeupstream.Config{}, mattingapiv1.Version())
	adapter, err := matting.New(matting.Config{
		BaseURL: f.Server.URL + "/matting", APIKey: "matting-key",
		HTTPClient:  &http.Client{Transport: scenarioTransport{next: http.DefaultTransport, scenario: "partial-images"}},
		InputAssets: memoryInputs{content: []byte("picture")},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, offering := shippedOffering(t)
	assets := []generation.InputAsset{
		{ID: "asset-1", MediaType: "image/png"}, {ID: "asset-2", MediaType: "image/jpeg"}, {ID: "asset-3", MediaType: "image/webp"},
	}
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		AttemptID: "attempt-fake", SubmissionKey: "submission-fake", Offering: offering,
		Input:       json.RawMessage(`{"source_assets":["asset-1","asset-2","asset-3"]}`),
		InputAssets: map[string][]generation.InputAsset{"source_assets": assets},
	})
	if err != nil {
		t.Fatal(err)
	}
	var completed generation.Observation
	for range 8 {
		completed, err = adapter.Observe(context.Background(), generation.ObserveRequest{
			AttemptID: "attempt-fake", Offering: offering, Job: *started.Job,
		})
		if err != nil {
			t.Fatal(err)
		}
		if completed.Phase == generation.UpstreamCompleted {
			break
		}
	}
	if completed.Phase != generation.UpstreamCompleted || len(completed.Artifacts) != 1 {
		t.Fatalf("completed = %+v", completed)
	}
	want := []generation.TaskNotice{{Code: generation.NoticePartialInputs, Inputs: []int{2, 3}}}
	if !reflect.DeepEqual(completed.Notices, want) {
		t.Fatalf("notices = %+v, want %+v", completed.Notices, want)
	}
}
