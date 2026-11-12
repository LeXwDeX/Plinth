package matting_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #284: a batch that ends PARTIAL_SUCCESS is a completed Task that claims only
// the pictures that were matted. The provider records name the others: the
// position each picture had among the submitted ones, read back from the upload
// filename the service echoes, and whether it produced an artifact.
func TestPartialSuccessNamesThePicturesThatWereNotMatted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"batch_id":     "batch-9",
			"batch_status": "PARTIAL_SUCCESS",
			"tasks": []any{
				map[string]any{
					"task_id": "t-1", "status": "SUCCEEDED", "downloadable": true,
					"source_name": "input_1.png", "download_path": "/api/v1/tasks/t-1/download",
				},
				map[string]any{
					"task_id": "t-2", "status": "REJECTED", "downloadable": false,
					"source_name": "input_2.jpg", "invalid_reason": "unreadable image",
				},
				map[string]any{
					"task_id": "t-3", "status": "SUCCEEDED", "downloadable": true,
					"source_name": "input_3.png", "download_path": "/api/v1/tasks/t-3/download",
				},
				map[string]any{
					"task_id": "t-4", "status": "FAILED", "downloadable": false,
					"source_name": "renamed-by-the-service.png", "error_message": "out of memory",
				},
			},
		})
	}))
	defer upstream.Close()
	adapter := newAdapter(t, upstream, nil)
	offering := generation.Offering{
		ProviderID:   "matting",
		Capability:   generation.BackgroundRemove,
		AdapterKey:   "matting.v1",
		PollInterval: time.Second,
		Parameters: []generation.Parameter{{
			Name: "source_assets", Type: "asset-array", Required: true, MinItems: 1, MaxItems: 20,
			Semantic: generation.SemanticReferenceImage,
		}},
		ProviderOptions: defaultProviderOptions(),
	}
	completed, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		AttemptID: "attempt-9", Offering: offering,
		Job: generation.UpstreamJob{ProviderID: "matting", RemoteID: "batch-9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The Task still completes, and only the matted pictures are claimed.
	if completed.Phase != generation.UpstreamCompleted || len(completed.Artifacts) != 2 ||
		completed.Artifacts[0].Variant != "input_1.png" || completed.Artifacts[1].Variant != "input_3.png" {
		t.Fatalf("completed = %#v", completed)
	}
	var records []struct {
		Identifier   string `json:"identifier"`
		Status       string `json:"status"`
		InputOrdinal int    `json:"input_ordinal"`
		Matted       bool   `json:"matted"`
	}
	if err := json.Unmarshal(completed.Opaque, &records); err != nil {
		t.Fatalf("opaque = %s: %v", completed.Opaque, err)
	}
	want := []struct {
		identifier string
		ordinal    int
		matted     bool
	}{{"t-1", 1, true}, {"t-2", 2, false}, {"t-3", 3, true}, {"t-4", 0, false}}
	if len(records) != len(want) {
		t.Fatalf("records = %+v", records)
	}
	for index, expected := range want {
		got := records[index]
		if got.Identifier != expected.identifier || got.InputOrdinal != expected.ordinal || got.Matted != expected.matted {
			t.Fatalf("record %d = %+v, want %+v", index, got, expected)
		}
	}
	// Only the pictures that were matted say so: a service that does not echo
	// the upload name leaves the position unknown instead of guessing it.
	if records[1].Status == "SUCCEEDED" || records[3].Status != "FAILED" {
		t.Fatalf("statuses = %+v", records)
	}
}
