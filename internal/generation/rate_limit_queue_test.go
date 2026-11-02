package generation_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// scriptedStartAdapter 按脚本让前几次 Start 失败，之后交给 mock 正常受理。
// permit 为真时每次 Start 先取得私有派发许可再失败，用来验证结算。
type scriptedStartAdapter struct {
	mu       sync.Mutex
	script   []*generation.Failure
	calls    int
	permit   bool
	next     generation.ProviderPort
	failures []*generation.Failure
}

func newScripted(permit bool, script ...*generation.Failure) *scriptedStartAdapter {
	return &scriptedStartAdapter{script: script, permit: permit, next: mock.New()}
}

func (a *scriptedStartAdapter) Start(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	a.mu.Lock()
	index := a.calls
	a.calls++
	a.mu.Unlock()
	if index < len(a.script) {
		if a.permit {
			checkpointer, ok := generation.DispatchCheckpointerFromContext(ctx)
			if !ok {
				return generation.Observation{}, io.ErrUnexpectedEOF
			}
			if _, err := checkpointer.Checkpoint(ctx, generation.DispatchIntent{Bytes: json.RawMessage(`{"stage":"fixture.create"}`)}); err != nil {
				return generation.Observation{}, err
			}
		}
		copy := *a.script[index]
		return generation.Observation{}, &generation.ProviderError{Failure: &copy}
	}
	return a.next.Start(ctx, request)
}

func (a *scriptedStartAdapter) Observe(ctx context.Context, request generation.ObserveRequest) (generation.Observation, error) {
	return a.next.Observe(ctx, request)
}

func (a *scriptedStartAdapter) OpenAsset(ctx context.Context, request generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	return a.next.OpenAsset(ctx, request)
}

func limited(retryAfter time.Duration) *generation.Failure {
	return &generation.Failure{Class: generation.FailureRateLimited, Retryable: true, SafeDetail: "429", UpstreamStatus: 429, RetryAfter: retryAfter}
}

func unavailable() *generation.Failure {
	return &generation.Failure{Class: generation.FailureUpstreamUnavailable, Retryable: true, SafeDetail: "503"}
}

func newQueueHarness(t *testing.T, adapter generation.ProviderPort) (*harness, generation.TaskID) {
	t.Helper()
	h := newConfiguredHarness(t, adapter, nil, func(d *generation.Dependencies) {
		d.Random = func() float64 { return 1 } // 抖动系数固定为 1，退避可断言
	})
	accepted, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"castle"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, accepted.TaskID
}

// runQueue 每轮推进 5 分钟（超过 Retry.MaxBackoff），直到终态。
func (h *harness) runQueue(t *testing.T, id generation.TaskID) generation.Task {
	t.Helper()
	for range 60 {
		h.reconcile(t)
		task, err := h.module.Get(context.Background(), "owner-a", id)
		if err != nil {
			t.Fatal(err)
		}
		if task.State.Terminal() {
			return task
		}
		h.clock.Advance(5 * time.Minute)
	}
	t.Fatal("task did not reach terminal state")
	return generation.Task{}
}

func (h *harness) eventsOf(t *testing.T, id generation.TaskID) []generation.Event {
	t.Helper()
	events, err := h.module.Events(context.Background(), "owner-a", id, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestRateLimitedStartQueuesWithoutSpendingMaxAttempts(t *testing.T) {
	h, id := newQueueHarness(t, newScripted(false, limited(0), limited(0), limited(0), limited(0), limited(0)))
	task := h.runQueue(t, id)
	events := h.eventsOf(t, id)
	if task.State != generation.TaskSucceeded {
		t.Fatalf("state=%s failure=%#v events=%v", task.State, task.Failure, eventTypes(events))
	}
	if countEvents(events, "attempt.rate_limit_queued") != 5 || countEvents(events, "attempt.retry_scheduled") != 0 ||
		countEvents(events, "attempt.started") != 6 {
		t.Fatalf("events=%v", eventTypes(events))
	}
}

func TestRateLimitQueueKeepsTaskQueuedWithPublicReasonAndBackoff(t *testing.T) {
	h, id := newQueueHarness(t, newScripted(false, limited(0), limited(0)))
	start := h.clock.Now()
	h.reconcile(t) // 注意 reconcile 之后时钟前进 31 秒
	task, err := h.module.Get(context.Background(), "owner-a", id)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskQueued || !task.RateLimitQueued || task.Failure != nil {
		t.Fatalf("task=%s queued=%v failure=%v", task.State, task.RateLimitQueued, task.Failure)
	}
	if want := start.Add(30 * time.Second); !task.NextActionAt.Equal(want) {
		t.Fatalf("next=%v want %v（初始退避 30 秒）", task.NextActionAt, want)
	}
	h.reconcile(t) // 第二次 429，退避翻倍
	if events := h.eventsOf(t, id); countEvents(events, "attempt.rate_limit_queued") != 2 {
		t.Fatalf("events=%v", eventTypes(events))
	}
	task, _ = h.module.Get(context.Background(), "owner-a", id)
	if want := start.Add(31*time.Second + 60*time.Second); !task.NextActionAt.Equal(want) {
		t.Fatalf("second next=%v want %v", task.NextActionAt, want)
	}
	if final := h.runQueue(t, id); final.State != generation.TaskSucceeded || final.RateLimitQueued {
		t.Fatalf("final=%s queued=%v", final.State, final.RateLimitQueued)
	}
}

func TestRateLimitQueueHonoursRetryAfterAndMaxBackoff(t *testing.T) {
	h, id := newQueueHarness(t, newScripted(false, limited(100*time.Second), limited(time.Hour)))
	start := h.clock.Now()
	h.reconcile(t)
	task, _ := h.module.Get(context.Background(), "owner-a", id)
	if want := start.Add(100 * time.Second); !task.NextActionAt.Equal(want) {
		t.Fatalf("next=%v want %v", task.NextActionAt, want)
	}
	h.clock.Advance(100 * time.Second)
	now := h.clock.Now()
	h.reconcile(t)
	task, _ = h.module.Get(context.Background(), "owner-a", id)
	if want := now.Add(5 * time.Minute); !task.NextActionAt.Equal(want) {
		t.Fatalf("capped next=%v want %v（封顶 Retry.MaxBackoff）", task.NextActionAt, want)
	}
}

func TestRateLimitQueueStopsAtOfferingTimeoutAndFallsBackToMaxAttempts(t *testing.T) {
	h, id := newQueueHarness(t, newScripted(false, limited(0), limited(0), limited(0)))
	h.clock.Advance(3590 * time.Second) // 距 Offering.Timeout(3600s) 只剩 10 秒，30 秒退避放不下
	task := h.runQueue(t, id)
	events := h.eventsOf(t, id)
	if task.State != generation.TaskFailed || task.Failure == nil || task.Failure.Class != generation.FailureRateLimited {
		t.Fatalf("state=%s failure=%#v", task.State, task.Failure)
	}
	if countEvents(events, "attempt.rate_limit_queued") != 0 || countEvents(events, "attempt.retry_scheduled") != 1 ||
		countEvents(events, "attempt.started") != 2 {
		t.Fatalf("events=%v", eventTypes(events))
	}
}

func TestRateLimitQueueDoesNotExtendOtherFailureAttempts(t *testing.T) {
	h, id := newQueueHarness(t, newScripted(false, limited(0), limited(0), unavailable(), unavailable(), unavailable()))
	task := h.runQueue(t, id)
	events := h.eventsOf(t, id)
	if task.State != generation.TaskFailed || task.Failure.Class != generation.FailureUpstreamUnavailable {
		t.Fatalf("state=%s failure=%#v", task.State, task.Failure)
	}
	// 2 次排队不计数；503 仍只允许 max_attempts=2：1 次重试后失败。
	if countEvents(events, "attempt.rate_limit_queued") != 2 || countEvents(events, "attempt.retry_scheduled") != 1 ||
		countEvents(events, "attempt.started") != 4 {
		t.Fatalf("events=%v", eventTypes(events))
	}
}

func TestAmbiguousRateLimitNeverQueues(t *testing.T) {
	for name, failure := range map[string]*generation.Failure{
		"ambiguous":          {Class: generation.FailureRateLimited, Retryable: true, Ambiguous: true},
		"submission_unknown": {Class: generation.FailureSubmissionUnknown, Retryable: true},
	} {
		t.Run(name, func(t *testing.T) {
			h, id := newQueueHarness(t, newScripted(false, failure))
			task := h.runQueue(t, id)
			events := h.eventsOf(t, id)
			if task.State != generation.TaskFailed || task.Failure.Class != generation.FailureSubmissionUnknown {
				t.Fatalf("state=%s failure=%#v", task.State, task.Failure)
			}
			if hasEvent(events, "attempt.rate_limit_queued") || countEvents(events, "attempt.started") != 1 {
				t.Fatalf("events=%v", eventTypes(events))
			}
		})
	}
}

func TestRateLimitedStartSettlesPermitAsNotDispatched(t *testing.T) {
	h, id := newQueueHarness(t, newScripted(true, limited(0)))
	h.reconcile(t)
	db, err := sql.Open("sqlite", "file:"+h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var state sql.NullString
	if err := db.QueryRow(`SELECT dispatch_checkpoint_json FROM generation_attempts WHERE task_id = ? AND ordinal = 1`, id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if !state.Valid || !jsonHas(state.String, `"state":"idle"`) {
		t.Fatalf("checkpoint=%q; 429 拒绝必须按未派发清回 idle", state.String)
	}
	if final := h.runQueue(t, id); final.State != generation.TaskSucceeded {
		t.Fatalf("final=%s", final.State)
	}
}

func jsonHas(raw, fragment string) bool {
	var compact map[string]any
	if json.Unmarshal([]byte(raw), &compact) != nil {
		return false
	}
	encoded, _ := json.Marshal(compact)
	return string(encoded) != "" && containsFold(string(encoded), fragment)
}

func containsFold(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
