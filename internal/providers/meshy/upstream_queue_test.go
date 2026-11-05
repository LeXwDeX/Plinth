package meshy_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

// pendingTaskFixture is a Meshy text-to-3d task waiting in Meshy's queue
// (#131). It is constructed, not captured: status, progress, preceding_tasks
// and started_at are what a dev-test GET /openapi/v2/text-to-3d/{id}
// answered; the other fields follow the captured IN_PROGRESS sample.
func pendingTaskFixture(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("testdata/meshy-text-to-3d-pending.json")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// queueUpstream serves one preview task and one refine task whose bodies the
// test sets.
type queueUpstream struct {
	mu      sync.Mutex
	server  *httptest.Server
	preview map[string]any
	refine  map[string]any
}

func newQueueUpstream(t *testing.T) *queueUpstream {
	t.Helper()
	upstream := &queueUpstream{}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstream.mu.Lock()
		defer upstream.mu.Unlock()
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/openapi/v2/text-to-3d":
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			result := "id-1"
			if body["mode"] == "refine" {
				result = "refine-1"
			}
			_ = json.NewEncoder(writer).Encode(map[string]string{"result": result})
		case request.URL.Path == "/openapi/v2/text-to-3d/id-1" && upstream.preview != nil:
			_ = json.NewEncoder(writer).Encode(upstream.preview)
		case request.URL.Path == "/openapi/v2/text-to-3d/refine-1" && upstream.refine != nil:
			_ = json.NewEncoder(writer).Encode(upstream.refine)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func (u *queueUpstream) set(preview, refine map[string]any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.preview, u.refine = preview, refine
}

// withFields copies body with the given fields replaced (nil deletes).
func withFields(body map[string]any, fields map[string]any) map[string]any {
	copied := make(map[string]any, len(body))
	for key, value := range body {
		copied[key] = value
	}
	for key, value := range fields {
		if value == nil {
			delete(copied, key)
			continue
		}
		copied[key] = value
	}
	return copied
}

// #131: Meshy PENDING maps to the generic upstream queue through the
// connection's status_mapping (queued status words, queue_ahead_field
// preceding_tasks), in the preview stage and in the private refine stage.
func TestPendingMeshyTaskReportsItsUpstreamQueue(t *testing.T) {
	upstream := newQueueUpstream(t)
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:         upstream.server.URL,
		APIKey:          "meshy-key",
		HTTPClient:      upstream.server.Client(),
		AllowHTTPAssets: true,
	})
	offering := refineOffering(t, upstream.server)
	input := normalizedInput(t, offering, `{"prompt":"castle"}`)
	ctx := context.Background()
	started, err := adapter.Start(ctx, generation.StartRequest{
		TaskID: "task-1", AttemptID: "attempt-1", SubmissionKey: "submission-1",
		Offering: offering, Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Queue != nil {
		t.Fatalf("a create answer reports no queue: %#v", started.Queue)
	}
	request := generation.ObserveRequest{
		TaskID: "task-1", AttemptID: "attempt-1", Offering: offering,
		Job: *started.Job, Input: input,
	}
	pending := pendingTaskFixture(t)

	upstream.set(pending, nil)
	queued, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if queued.Phase != generation.UpstreamAccepted || queued.Progress != 0 ||
		queued.Queue == nil || queued.Queue.Ahead == nil || *queued.Queue.Ahead != 1313 {
		t.Fatalf("PENDING with preceding_tasks = phase %s progress %d queue %#v", queued.Phase, queued.Progress, queued.Queue)
	}

	upstream.set(withFields(pending, map[string]any{"preceding_tasks": nil}), nil)
	uncounted, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if uncounted.Queue == nil || uncounted.Queue.Ahead != nil {
		t.Fatalf("PENDING without preceding_tasks still waits in the queue: %#v", uncounted.Queue)
	}

	upstream.set(withFields(pending, map[string]any{"preceding_tasks": "many"}), nil)
	invalid, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if invalid.Queue == nil || invalid.Queue.Ahead != nil {
		t.Fatalf("an invalid count is dropped, the queue is kept: %#v", invalid.Queue)
	}

	upstream.set(withFields(pending, map[string]any{"status": "IN_PROGRESS", "progress": 20, "preceding_tasks": nil}), nil)
	running, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if running.Phase != generation.UpstreamRunning || running.Queue != nil {
		t.Fatalf("IN_PROGRESS = phase %s queue %#v", running.Phase, running.Queue)
	}

	succeeded := withFields(pending, map[string]any{
		"status": "SUCCEEDED", "progress": 100, "preceding_tasks": nil,
		"model_urls": map[string]string{"glb": upstream.server.URL + "/preview.glb"},
	})
	upstream.set(succeeded, nil)
	submitted, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if submitted.Phase != generation.UpstreamRunning || submitted.Progress != 50 || submitted.Queue != nil {
		t.Fatalf("refine submitted = phase %s progress %d queue %#v", submitted.Phase, submitted.Progress, submitted.Queue)
	}

	request.Opaque = submitted.Opaque
	upstream.set(succeeded, withFields(pending, map[string]any{"id": "refine-1", "mode": "refine", "type": "text-to-3d-refine", "preceding_tasks": 7}))
	refineQueued, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if refineQueued.Phase != generation.UpstreamRunning || refineQueued.Progress != 50 ||
		refineQueued.Queue == nil || refineQueued.Queue.Ahead == nil || *refineQueued.Queue.Ahead != 7 {
		t.Fatalf("queued refine = phase %s progress %d queue %#v", refineQueued.Phase, refineQueued.Progress, refineQueued.Queue)
	}
}
