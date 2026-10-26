package matting_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The upstream refetch path (#295): after the Task ended, the kernel reads the
// accepted batch once more with paid dispatch denied. Only the status call is
// made; the job create endpoint is never touched.
func TestMattingRefetchReadsTheAcceptedBatchWithoutCreate(t *testing.T) {
	var posts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			posts.Add(1)
			http.Error(w, "create must not be called", http.StatusTeapot)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/batches/") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"batch_status": "SUCCESS",
				"tasks": []any{map[string]any{
					"task_id": "subtask-live", "status": "SUCCEEDED", "downloadable": true,
					"download_path": "/api/v1/tasks/subtask-live/download",
				}},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	adapter := newAdapter(t, upstream, nil)
	observed, err := adapter.Observe(generation.WithDeniedDispatchCheckpointer(context.Background()), generation.ObserveRequest{
		TaskID: "failed-task", AttemptID: "failed-attempt", Offering: defaultOffering(),
		Job: generation.UpstreamJob{RemoteID: "batch-live"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Phase != generation.UpstreamCompleted || len(observed.Artifacts) != 1 || posts.Load() != 0 {
		t.Fatalf("refetch observation = %#v posts=%d", observed, posts.Load())
	}
}
