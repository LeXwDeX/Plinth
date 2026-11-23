package tripo

// #468: the native export ledger has the same rule as pre-completion: a convert
// child that ended failed or cancelled enters the documented backoff (1, 2, 4…
// seconds) before its format is sent again. Entering the backoff also records an
// attempt, so the "an outcome happened, dispatch nothing" return always fires
// with it: the backoff has to be applied there, or the next round dispatches the
// retry immediately.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestNativeConversionFailedChildRoundCarriesItsBackoff(t *testing.T) {
	r := nativeDeliveryFixture(generation.Rigging)
	posts := 0
	failNext := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.Method == http.MethodPost {
			if strings.HasSuffix(q.URL.Path, "models/convert") {
				posts++
				id := "child_2"
				if failNext {
					failNext = false
					id = "child_1"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"task_id": id}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"task_id": "task_processed"}})
			return
		}
		id := strings.TrimPrefix(q.URL.Path, "/v3/tasks/")
		data := map[string]any{"task_id": id, "status": "success", "progress": 100}
		switch {
		case id == "task_processed":
			data["output"] = map[string]any{"model_url": "https://assets.example/processed.glb"}
		case id == "child_1":
			data = map[string]any{"task_id": id, "status": "failed", "progress": 0, "credits_consumed": 0, "error_code": 1001}
		default:
			data["output"] = map[string]any{"model_url": "https://assets.example/" + id + ".glb"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer server.Close()
	statusMapping := &generation.StatusMapping{UpstreamStatus: map[string]generation.StatusRule{
		"success": {Phase: "completed"},
		"running": {Phase: "running"},
		"failed":  {Phase: "failed"},
	}}
	factory := func() *Adapter {
		a, err := New(Config{
			BaseURL:       server.URL + "/v3",
			APIKey:        "test",
			HTTPClient:    server.Client(),
			// #487: whether a failed child may be resent is decided by the
			// connection's task-level vocabulary, not by the Adapter.
			ErrorMapping:  taskErrorMapping(t),
			StatusMapping: statusMapping,
		})
		if err != nil {
			t.Fatal(err)
		}
		a.nativeSourceClient = nativeDeliveryTestSourceClient(nativeTestGLB, nil)
		return a
	}
	p := &nativeDeliveryPermit{}
	ctx := generation.WithDispatchCheckpointer(context.Background(), p)
	accepted, err := factory().Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	q := rigObserve(r, accepted)
	// The first round starts the FBX child.
	first, err := factory().Observe(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	q.Opaque = first.Opaque
	if posts != 1 {
		t.Fatalf("posts = %d, want the FBX child", posts)
	}
	// The next round polls the child: it failed, so the format is waiting out
	// its backoff.
	second, err := factory().Observe(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	q.Opaque = second.Opaque
	var state nativeProcessingState
	if err := json.Unmarshal(second.Opaque, &state); err != nil {
		t.Fatal(err)
	}
	item := state.Conversions["FBX"]
	if item.Status != conversionItemRetryPending || item.RetryWaitSeconds <= 0 {
		t.Fatalf("FBX = %s/%d, want retry_pending with its backoff", item.Status, item.RetryWaitSeconds)
	}
	if second.PollAfter != time.Duration(item.RetryWaitSeconds)*time.Second {
		t.Fatalf("PollAfter = %v, want the format's backoff %ds", second.PollAfter, item.RetryWaitSeconds)
	}
	if posts != 1 {
		t.Fatalf("the round that entered the backoff also dispatched: %d posts", posts)
	}
	// The wait is a delay, not a dead end: the next round resends FBX.
	if _, err := factory().Observe(ctx, q); err != nil {
		t.Fatal(err)
	}
	if posts != 2 {
		t.Fatalf("posts after the backoff = %d, want FBX resent", posts)
	}
}
