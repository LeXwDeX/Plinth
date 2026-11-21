package tripo_test

// #468: the upstream concurrency pool as the Tripo Adapter uses it. The fake
// upstream answers 429 code 2000 (the account's shared pool is full) when more
// than its limit of jobs are in flight, so these tests prove that the Adapter
// queues instead of failing, never leaves a dispatching remnant, and never
// spends the per-format retry budget on a full pool.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"github.com/LeXwDeX/Plinth/internal/providers/upstreampool"
)

// poolUpstream is a minimal Tripo v3 upstream with an account-wide concurrency
// limit shared by every create request. It answers 429 code 2000 with
// Retry-After when the limit is reached, like the official rate-limits page.
type poolUpstream struct {
	server *httptest.Server

	mu          sync.Mutex
	limit       int
	inflight    int
	maxInflight int
	posts       []string
	polls       map[string]int
	counter     int
	refuseOnce  map[string]bool
	retryAfter  string
}

func newPoolUpstream(t *testing.T, limit int) *poolUpstream {
	t.Helper()
	f := &poolUpstream{limit: limit, polls: map[string]int{}, refuseOnce: map[string]bool{}, retryAfter: "1"}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/models/convert"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			format, _ := body["format"].(string)
			f.mu.Lock()
			if f.inflight >= f.limit || f.refuseOnce[format] {
				refused := f.refuseOnce[format]
				delete(f.refuseOnce, format)
				f.mu.Unlock()
				_ = refused
				w.Header().Set("Retry-After", f.retryAfter)
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = fmt.Fprint(w, `{"code":2000,"message":"You have exceeded the limit of generation"}`)
				return
			}
			f.inflight++
			if f.inflight > f.maxInflight {
				f.maxInflight = f.inflight
			}
			f.posts = append(f.posts, format)
			f.counter++
			id := fmt.Sprintf("child-%d", f.counter)
			f.polls[id] = 0
			f.mu.Unlock()
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"task_id": id}})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks/"):
			id := strings.TrimPrefix(r.URL.Path, "/tasks/")
			if id == "base" {
				writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
					"task_id": "base", "status": "success", "progress": 100,
					"output": map[string]any{"model_url": f.server.URL + "/base.glb"},
				}})
				return
			}
			f.mu.Lock()
			f.polls[id]++
			n := f.polls[id]
			status := "success"
			switch n {
			case 1:
				status = "queued"
			case 2:
				status = "running"
			}
			if status == "success" {
				f.inflight--
			}
			f.mu.Unlock()
			data := map[string]any{"task_id": id, "status": status, "progress": 0}
			if status == "queued" {
				data["progress"] = 0
			} else if status == "running" {
				data["progress"] = 50
			} else {
				data["progress"] = 100
				data["output"] = map[string]any{"model_url": f.server.URL + "/artifacts/" + id + "/model.glb"}
			}
			writeJSON(w, map[string]any{"code": 0, "data": data})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *poolUpstream) snapshot() (posts []string, maxInflight int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.posts...), f.maxInflight
}

func newPoolAdapter(t *testing.T, upstream *poolUpstream, limit int, offerings ...generation.Offering) (*tripo.Adapter, *upstreampool.Manager) {
	t.Helper()
	errorMapping, statusMapping := declaredTripoMappings(t)
	pool := upstreampool.New(upstreampool.Options{WaitHint: 2 * time.Second})
	pool.Register("tripo.v3", tripo.TripoPoolName, upstreampool.PoolConfig{Limit: limit})
	adapter, err := tripo.New(tripo.Config{
		BaseURL:         upstream.server.URL,
		APIKey:          "tripo-key",
		HTTPClient:      upstream.server.Client(),
		AllowHTTPAssets: true,
		OfferingRoutes:  offerings,
		ErrorMapping:    errorMapping,
		StatusMapping:   statusMapping,
		Pool:            pool,
		PoolID:          tripo.TripoPoolName,
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter, pool
}

type poolTask struct {
	request generation.ObserveRequest
	permit  *conversionPermit
	done    bool
	last    generation.Observation
}

func poolRequests(t *testing.T, count int) []poolTask {
	t.Helper()
	base := fullFormatRequest(t)
	tasks := make([]poolTask, 0, count)
	for i := 0; i < count; i++ {
		request := base
		request.AttemptID = generation.AttemptID(fmt.Sprintf("attempt-%d", i))
		tasks = append(tasks, poolTask{request: request, permit: &conversionPermit{}})
	}
	return tasks
}

// drivePoolObservations polls every unfinished task once per round, in order,
// until all are terminal or the round bound is reached.
func drivePoolObservations(
	t *testing.T,
	adapter *tripo.Adapter,
	tasks []poolTask,
	maxRounds int,
) []generation.Observation {
	t.Helper()
	var all []generation.Observation
	for round := 0; round < maxRounds; round++ {
		progressed := false
		for i := range tasks {
			task := &tasks[i]
			if task.done {
				continue
			}
			progressed = true
			ctx := generation.WithDispatchCheckpointer(context.Background(), task.permit)
			observation, err := adapter.Observe(ctx, task.request)
			if err != nil {
				t.Fatalf("task %d round %d: %v", i, round, err)
			}
			task.permit.settle(t, observation)
			task.last = observation
			task.request.Opaque = observation.Opaque
			all = append(all, observation)
			if poolTerminal(observation.Phase) {
				task.done = true
			}
		}
		if !progressed {
			break
		}
		done := true
		for i := range tasks {
			if !tasks[i].done {
				done = false
			}
		}
		if done {
			break
		}
	}
	return all
}

func TestUpstreamPoolQueuesAcrossTasks(t *testing.T) {
	upstream := newPoolUpstream(t, 100) // the fake is not the limit; the pool is
	adapter, _ := newPoolAdapter(t, upstream, 3)
	tasks := poolRequests(t, 5)
	observations := drivePoolObservations(t, adapter, tasks, 200)
	for i := range tasks {
		if !tasks[i].done {
			t.Fatalf("task %d did not finish: %+v", i, tasks[i].last)
		}
		if tasks[i].last.Phase != generation.UpstreamCompleted {
			t.Fatalf("task %d phase = %s: %+v", i, tasks[i].last.Phase, tasks[i].last.Failure)
		}
	}
	posts, maxInflight := upstream.snapshot()
	if len(posts) != 30 {
		t.Fatalf("posts = %d, want 30 (5 tasks x 6 formats): %v", len(posts), posts)
	}
	if maxInflight > 3 {
		t.Fatalf("max in flight = %d, want at most the pool limit 3", maxInflight)
	}
	queued := 0
	for _, observation := range observations {
		if observation.Queue != nil {
			queued++
		}
	}
	if queued == 0 {
		t.Fatal("no observation carried the upstream queue clock")
	}
}

func TestUpstreamPoolQueuedObservationHasNoDispatchingRemnant(t *testing.T) {
	upstream := newPoolUpstream(t, 100)
	adapter, _ := newPoolAdapter(t, upstream, 1)
	tasks := poolRequests(t, 2)
	// One round: the first task may dispatch, the second must queue. Neither
	// queued observation may leave a dispatching entry behind (C1).
	ctx := generation.WithDispatchCheckpointer(context.Background(), tasks[0].permit)
	first, err := adapter.Observe(ctx, tasks[0].request)
	if err != nil {
		t.Fatal(err)
	}
	tasks[0].permit.settle(t, first)
	tasks[0].request.Opaque = first.Opaque
	ctx = generation.WithDispatchCheckpointer(context.Background(), tasks[1].permit)
	second, err := adapter.Observe(ctx, tasks[1].request)
	if err != nil {
		t.Fatal(err)
	}
	tasks[1].permit.settle(t, second)
	if second.Queue == nil {
		t.Fatalf("second task did not queue: %+v", second)
	}
	assertNoDispatching(t, second.Opaque)
	assertNoDispatching(t, first.Opaque)
	// The queued task must not be abandoned on the next round (C1).
	tasks[1].request.Opaque = second.Opaque
	ctx = generation.WithDispatchCheckpointer(context.Background(), tasks[1].permit)
	next, err := adapter.Observe(ctx, tasks[1].request)
	if err != nil {
		t.Fatal(err)
	}
	tasks[1].permit.settle(t, next)
	if next.Phase == generation.UpstreamFailed {
		t.Fatalf("queued task failed on the next round: %+v", next.Failure)
	}
	assertNoDispatching(t, next.Opaque)
}

func TestUpstreamPoolFullRefusalQueuesWithoutSpendingBudget(t *testing.T) {
	// The fake allows one job at a time while the pool allows two: the second
	// task's child is refused with code 2000. That must release the slot and
	// queue, not spend the per-format rate-limit budget (C4).
	upstream := newPoolUpstream(t, 1)
	upstream.refuseOnce["GLTF"] = true
	adapter, _ := newPoolAdapter(t, upstream, 5)
	tasks := poolRequests(t, 1)
	// A single task cannot overflow the fake (pool concurrency is one child).
	// Drive it and make sure it finishes with no rate-limit retry spent.
	observations := drivePoolObservations(t, adapter, tasks, 100)
	if !tasks[0].done || tasks[0].last.Phase != generation.UpstreamCompleted {
		t.Fatalf("task did not finish: %+v", tasks[0].last)
	}
	for _, observation := range observations {
		state := parsePoolState(t, observation.Opaque)
		for format, item := range state {
			if item.RateLimitRetries != 0 {
				t.Fatalf("format %s spent %d rate-limit retries on a full pool", format, item.RateLimitRetries)
			}
		}
	}
}

func TestUpstreamPoolFullRefusalAcrossTasksKeepsBudget(t *testing.T) {
	upstream := newPoolUpstream(t, 1)
	upstream.refuseOnce["GLTF"] = true
	adapter, _ := newPoolAdapter(t, upstream, 5)
	tasks := poolRequests(t, 2)
	observations := drivePoolObservations(t, adapter, tasks, 300)
	for i := range tasks {
		if !tasks[i].done || tasks[i].last.Phase != generation.UpstreamCompleted {
			t.Fatalf("task %d did not finish: %+v", i, tasks[i].last)
		}
	}
	for _, observation := range observations {
		state := parsePoolState(t, observation.Opaque)
		for format, item := range state {
			if item.RateLimitRetries != 0 {
				t.Fatalf("format %s spent %d rate-limit retries on a full pool", format, item.RateLimitRetries)
			}
		}
	}
}

type poolConversionState struct {
	Conversions map[string]struct {
		Status           string `json:"status"`
		RateLimitRetries int    `json:"rate_limit_retries"`
	} `json:"conversions"`
}

func parsePoolState(t *testing.T, opaque json.RawMessage) map[string]struct {
	Status           string `json:"status"`
	RateLimitRetries int    `json:"rate_limit_retries"`
} {
	t.Helper()
	var state poolConversionState
	if len(opaque) == 0 {
		return nil
	}
	if err := json.Unmarshal(opaque, &state); err != nil {
		t.Fatalf("decode opaque: %v", err)
	}
	return state.Conversions
}

func assertNoDispatching(t *testing.T, opaque json.RawMessage) {
	t.Helper()
	for format, item := range parsePoolState(t, opaque) {
		if item.Status == "dispatching" {
			t.Fatalf("format %s left a dispatching remnant", format)
		}
	}
}

func poolTerminal(phase generation.UpstreamPhase) bool {
	switch phase {
	case generation.UpstreamCompleted, generation.UpstreamFailed, generation.UpstreamCanceled:
		return true
	default:
		return false
	}
}

// #468: after a restart the pool is empty, but a child that was already
// submitted is still running upstream. The first Observe re-registers it
// (Readopt, allowed to exceed the limit for one cycle) instead of queueing it
// behind another attempt that took the only slot.
func TestUpstreamPoolReadoptAfterRestart(t *testing.T) {
	upstream := newPoolUpstream(t, 100)
	adapter, _ := newPoolAdapter(t, upstream, 1)
	tasks := poolRequests(t, 1)
	ctx := generation.WithDispatchCheckpointer(context.Background(), tasks[0].permit)
	first, err := adapter.Observe(ctx, tasks[0].request)
	if err != nil {
		t.Fatal(err)
	}
	tasks[0].permit.settle(t, first)
	if first.Queue != nil || first.Dispatch == nil {
		t.Fatalf("first round did not dispatch: %+v", first)
	}
	// Restart: a new pool and adapter against the same upstream. Another
	// attempt takes the only slot before the original task is observed again.
	restarted, pool := newPoolAdapter(t, upstream, 1)
	other := upstreampool.Holder{Connection: "tripo.v3", Pool: tripo.TripoPoolName, Attempt: "other"}
	if _, ok, _ := pool.Acquire(context.Background(), other, 0); !ok {
		t.Fatal("other attempt should fit the empty pool")
	}
	tasks[0].request.Opaque = first.Opaque
	next, err := restarted.Observe(ctx, tasks[0].request)
	if err != nil {
		t.Fatal(err)
	}
	tasks[0].permit.settle(t, next)
	if next.Queue != nil {
		t.Fatalf("submitted child was queued after restart: %+v", next)
	}
	if next.Phase != generation.UpstreamRunning {
		t.Fatalf("submitted child after restart phase = %s: %+v", next.Phase, next)
	}
	if stats := pool.Stats(); stats.TotalHeld != 2 {
		t.Fatalf("readopt did not exceed the limit for the running child: %+v", stats)
	}
}
