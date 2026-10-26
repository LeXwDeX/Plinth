package generation_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNonIdempotent502IsNeverResubmitted(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method %s", r.Method)
		}
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, `{"message":"accepted upstream; lost response"}`)
	}))
	defer server.Close()
	adapter, err := meshy.New(meshy.Config{BaseURL: server.URL, APIKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, adapter, nil)
	accepted, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{OwnerID: "audit", Capability: generation.TextTo3D, OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"audit"}`)})
	if err != nil {
		t.Fatal(err)
	}
	h.reconcile(t)
	h.reconcile(t)
	task, err := h.module.Get(context.Background(), "audit", accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("POST count=%d expected exactly one submission", calls.Load())
	}
	if task.State != generation.TaskFailed || task.Failure == nil || task.Failure.Class != generation.FailureSubmissionUnknown {
		t.Fatalf("unexpected task %#v", task)
	}
}

type liveClock struct{}

func (liveClock) Now() time.Time { return time.Now().UTC() }

type leaseRepo struct {
	generation.Repository
	renewals   atomic.Int32
	failRenew  atomic.Bool
	renewedAt  atomic.Int64
	claimError error
	failures   atomic.Int32
}

func (r *leaseRepo) RenewLease(ctx context.Context, id generation.TaskID, token string, now time.Time, ttl time.Duration) error {
	r.renewals.Add(1)
	var err error
	if r.failRenew.Load() {
		err = generation.RepositoryConflict
	} else {
		err = r.Repository.RenewLease(ctx, id, token, now, ttl)
	}
	if err == nil {
		for {
			previous := r.renewedAt.Load()
			if now.UnixNano() <= previous || r.renewedAt.CompareAndSwap(previous, now.UnixNano()) {
				break
			}
		}
	}
	return err
}

// awaitRenewalBeyond 的上界只是防挂死护栏，不是正确性余量：租约是否有效完全由
// 注入时钟上已落库的续租读数决定，与墙钟调度延迟无关。
func awaitRenewalBeyond(t *testing.T, repo *leaseRepo, want time.Time) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for repo.renewedAt.Load() < want.UnixNano() {
		if time.Now().After(deadline) {
			t.Fatalf("lease renewal never observed injected clock %s", want.Format(time.RFC3339Nano))
		}
		time.Sleep(time.Millisecond)
	}
}
func (r *leaseRepo) ClaimReady(ctx context.Context, worker string, now time.Time, ttl time.Duration, limit int) ([]generation.Lease, error) {
	if r.claimError != nil && r.failures.Add(1) == 1 {
		return nil, r.claimError
	}
	return r.Repository.ClaimReady(ctx, worker, now, ttl, limit)
}

type coordinatorAdapter struct {
	starts   atomic.Int32
	observes atomic.Int32
	slow     chan struct{}
	release  chan struct{}
	// cancelCause, when non-nil, observes why a slow Start ended.
	cancelCause chan error
}

func (a *coordinatorAdapter) Start(ctx context.Context, r generation.StartRequest) (generation.Observation, error) {
	a.starts.Add(1)
	if strings.Contains(string(r.Input), "slow") {
		close(a.slow)
		select {
		case <-ctx.Done():
			if a.cancelCause != nil {
				select {
				case a.cancelCause <- context.Cause(ctx):
				default:
				}
			}
			return generation.Observation{}, ctx.Err()
		case <-a.release:
		}
	}
	return generation.Observation{Phase: generation.UpstreamAccepted, Job: &generation.UpstreamJob{ProviderID: "meshy", RemoteID: string(r.TaskID)}, Progress: 10, PollAfter: time.Millisecond}, nil
}
func (a *coordinatorAdapter) Observe(ctx context.Context, r generation.ObserveRequest) (generation.Observation, error) {
	a.observes.Add(1)
	return generation.Observation{Phase: generation.UpstreamRunning, Job: &r.Job, Progress: 80, PollAfter: time.Millisecond}, nil
}
func (a *coordinatorAdapter) OpenAsset(context.Context, generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	return nil, generation.AssetMetadata{}, errors.New("unexpected asset")
}
func remediationModule(t *testing.T, h *harness, repo generation.Repository, adapter generation.ProviderPort) *generation.Module {
	t.Helper()
	// remediation 模块默认用 liveClock（真实时间）：仓库租约写时钟必须与模块
	// 同一时间线，覆盖 newHarness 默认接线的 fakeClock。
	return remediationModuleWithClock(t, h, repo, adapter, liveClock{})
}

// remediationModuleWithClock 以同一注入时间线装配模块时钟与仓库租约写时钟。
// TestLongCallRenewsLeaseAcrossWorkers 与 TestCancellation… 用 fakeClock：
// 租约/就绪时间线只随测试显式 Advance 前进，墙钟调度饥饿不再能过期租约
// 或翻转 RFC3339Nano 文本序的亚毫秒就绪比较（见两处测试内注释）。
func remediationModuleWithClock(t *testing.T, h *harness, repo generation.Repository, adapter generation.ProviderPort, clock generation.Clock) *generation.Module {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	if err := registry.Register("meshy.v2", adapter); err != nil {
		t.Fatal(err)
	}
	assets, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.repository.SetClock(clock.Now)
	var taskCatalog generation.Catalog = c
	if _, ok := adapter.(*cancelAdapter); ok {
		taskCatalog = cancelCatalog{Catalog: c}
	}
	admissionRepo := preparedAdmissionRepository{Repository: repo, base: h.repository}
	m, err := generation.New(generation.Dependencies{Repository: admissionRepo, Catalog: taskCatalog, Providers: registry, Assets: assets, Clock: clock, Random: func() float64 { return 0.5 }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}
func submitRemediation(t *testing.T, m *generation.Module, repository *sqlite.Repository, clock *fakeClock, prompt string) generation.TaskID {
	t.Helper()
	fixture := &harness{module: m, repository: repository, clock: clock}
	accepted, err := fixture.submitApprovedWithModule(t, context.Background(), m, generation.SubmitCommand{OwnerID: "audit", Capability: generation.TextTo3D, OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(fmt.Sprintf(`{"prompt":%q}`, prompt))})
	if err != nil {
		t.Fatal(err)
	}
	return accepted.TaskID
}
func TestLongCallRenewsLeaseAcrossWorkers(t *testing.T) {
	adapter := &coordinatorAdapter{slow: make(chan struct{}), release: make(chan struct{})}
	h := newHarness(t, adapter, nil)
	repo := &leaseRepo{Repository: h.repository}
	m := remediationModuleWithClock(t, h, repo, adapter, h.clock)
	other := remediationModuleWithClock(t, h, repo, adapter, h.clock)
	// 注入时钟从整秒开始；SQLite 任务调度/租约时间列（next_action_at/
	// lease_until，0061）固定九位小数，TEXT 比较与时间序一致，无需再构造
	// 等长小数的墙钟读数。
	id := submitRemediation(t, m, h.repository, h.clock, "slow")
	cfg := generation.CoordinatorConfig{WorkerID: "one", LeaseFor: 150 * time.Millisecond, LeaseRenewEvery: 20 * time.Millisecond, Concurrency: 1, BatchSize: 1}
	done := make(chan error, 1)
	go func() { _, err := m.ReconcileOnce(context.Background(), cfg); done <- err }()
	// T2-4 防挂死：有界等待替代裸 <-adapter.slow（历史捕获中协调器提前退出、
	// Start 未被调用，测试在无界 channel 接收上挂死 9m55s 直到 go test 超时）。
	// done 分支同时暴露退出根因（0 leases 或 claim error）。
	select {
	case <-adapter.slow:
	case err := <-done:
		t.Fatalf("ReconcileOnce exited before provider Start was called (claim returned 0 leases or errored): %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("provider Start was not called within 30s (bounded hang guard)")
	}
	// 租约时间线 = 注入时钟，只随下面的显式 Advance 前进——墙钟调度饥饿
	// 不再可能吃掉 150ms TTL。推进半个 TTL 后等待注入时钟上的续租读数
	// 到达该时刻：成功续租把 lease_until 重臂到「注入 now + 150ms」，第二个
	// worker 的领取时刻落在租约有效期内，跨 worker 排他成为结构性保证。
	// awaitRenewalBeyond 的 30s 墙钟上界只是防挂死护栏，不是正确性余量。
	h.clock.Advance(cfg.LeaseFor / 2)
	awaitRenewalBeyond(t, repo, h.clock.Now())
	cfg.WorkerID = "two"
	count, err := other.ReconcileOnce(context.Background(), cfg)
	if err != nil || count != 0 {
		t.Fatalf("second worker claimed live task: n=%d err=%v", count, err)
	}
	close(adapter.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	task, err := m.Get(context.Background(), "audit", id)
	if err != nil || task.State != generation.TaskRunning {
		t.Fatalf("task=%#v err=%v", task, err)
	}
	if adapter.starts.Load() != 1 {
		t.Fatalf("starts=%d", adapter.starts.Load())
	}
}
func TestRenewalConflictCancelsCallWithoutResubmission(t *testing.T) {
	adapter := &coordinatorAdapter{slow: make(chan struct{}), release: make(chan struct{}), cancelCause: make(chan error, 1)}
	h := newHarness(t, adapter, nil)
	repo := &leaseRepo{Repository: h.repository}
	m := remediationModuleWithClock(t, h, repo, adapter, h.clock)
	id := submitRemediation(t, m, h.repository, h.clock, "slow")
	cfg := generation.CoordinatorConfig{WorkerID: "one", LeaseFor: time.Second, LeaseRenewEvery: 20 * time.Millisecond, Concurrency: 1, BatchSize: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type reconcileResult struct {
		count int
		err   error
	}
	done := make(chan reconcileResult, 1)
	go func() {
		count, err := m.ReconcileOnce(ctx, cfg)
		done <- reconcileResult{count, err}
	}()
	select {
	case <-adapter.slow:
	case result := <-done:
		if aggregate, loadErr := h.repository.Load(context.Background(), id); loadErr == nil {
			t.Logf("state=%s attempt=%+v starts=%d", aggregate.Task.State, aggregate.CurrentAttempt, adapter.starts.Load())
		}
		t.Fatalf("reconcile exited before provider Start: claimed=%d err=%v", result.count, result.err)
	case <-ctx.Done():
		t.Fatal("provider Start was not called before the test deadline")
	}
	// begin_attempt 与 Provider Start 都已经发生，此后才注入续租失败。
	started, err := h.repository.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if started.Task.State != generation.TaskRunning || started.CurrentAttempt == nil || started.CurrentAttempt.State != generation.AttemptSubmitting || adapter.starts.Load() != 1 {
		t.Fatalf("task not executing with a persisted submitting attempt: state=%s attempt=%+v starts=%d", started.Task.State, started.CurrentAttempt, adapter.starts.Load())
	}
	repo.failRenew.Store(true)
	var claimed int
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("first reconcile err=%v", result.err)
		}
		claimed = result.count
	case <-ctx.Done():
		t.Fatal("renewal conflict never cancelled the in-flight provider call")
	}
	if claimed != 1 {
		t.Fatalf("first reconcile claimed %d leases, want exactly 1", claimed)
	}
	select {
	case cause := <-adapter.cancelCause:
		if !errors.Is(cause, generation.RepositoryConflict) {
			t.Fatalf("in-flight call cancelled by %v, want renewal conflict", cause)
		}
	default:
		t.Fatal("in-flight call ended without a renewal conflict cancellation cause")
	}
	lost, err := h.repository.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	// 租约丢失不得落终态：冻结的 SUBMITTING 提交仍需保持可重驱。
	if lost.Task.State != generation.TaskRunning || lost.Task.Failure != nil || lost.CurrentAttempt == nil || lost.CurrentAttempt.State != generation.AttemptSubmitting {
		t.Fatalf("lease loss did not leave a redrivable submitting task: %#v attempt=%+v", lost.Task, lost.CurrentAttempt)
	}
	attemptID := lost.CurrentAttempt.ID

	// 第二个 worker 只在注入时钟越过租约后重领；非幂等 SUBMITTING 必须
	// fail closed 为 SUBMISSION_UNKNOWN，不得再次调用 Provider Start。
	repo.failRenew.Store(false)
	h.clock.Advance(2 * time.Second)
	cfg.WorkerID = "two"
	redriveCtx, cancelRedrive := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelRedrive()
	count, err := m.ReconcileOnce(redriveCtx, cfg)
	if err != nil || count != 1 {
		t.Fatalf("redrive claimed=%d err=%v", count, err)
	}
	task, err := m.Get(context.Background(), "audit", id)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskFailed || task.Failure == nil || task.Failure.Class != generation.FailureSubmissionUnknown {
		t.Fatalf("redrive task=%#v", task)
	}
	if adapter.starts.Load() != 1 {
		t.Fatalf("provider starts=%d, want exactly 1 (no resubmission)", adapter.starts.Load())
	}
	aggregate, err := h.repository.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.CurrentAttempt == nil || aggregate.CurrentAttempt.ID != attemptID || aggregate.CurrentAttempt.Ordinal != 1 {
		t.Fatalf("redrive replaced the frozen attempt: %+v", aggregate.CurrentAttempt)
	}
}
func TestSlowStartDoesNotBlockOtherTaskPollingAndShutdownDrains(t *testing.T) {
	adapter := &coordinatorAdapter{slow: make(chan struct{}), release: make(chan struct{})}
	h := newHarness(t, adapter, nil)
	m := remediationModule(t, h, h.repository, adapter)
	submitRemediation(t, m, h.repository, h.clock, "slow")
	submitRemediation(t, m, h.repository, h.clock, "fast")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- m.Run(ctx, generation.CoordinatorConfig{WorkerID: "mixed", PollEvery: time.Millisecond, LeaseFor: time.Second, LeaseRenewEvery: 50 * time.Millisecond, Concurrency: 2})
	}()
	select {
	case <-adapter.slow:
	case <-time.After(time.Second):
		t.Fatal("slow did not start")
	}
	deadline := time.Now().Add(time.Second)
	for adapter.observes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if adapter.observes.Load() == 0 {
		t.Error("slow task blocked fast task polling")
	}
	cancel()
	// 停机排水语义：在飞调用不随停机中断（已付费提交不丢），放行慢
	// 调用后 Run 应在其完成后退出，而不是先中断再失败。
	close(adapter.release)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not drain in-flight calls")
	}
}

type transientFailure struct{}

func (transientFailure) Error() string   { return "temporary disk I/O failure" }
func (transientFailure) Temporary() bool { return true }
func TestCoordinatorRecoversTransientStorageAndFailsPermanentErrors(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		t.Run(fmt.Sprint(permanent), func(t *testing.T) {
			adapter := &coordinatorAdapter{}
			h := newHarness(t, adapter, nil)
			var failure error = transientFailure{}
			if permanent {
				// SQLITE_CORRUPT: the database as a whole is unusable (#298:
				// an unclassified error no longer stops the coordinator).
				failure = coded(11)
			}
			repo := &leaseRepo{Repository: h.repository, claimError: failure}
			m := remediationModule(t, h, repo, adapter)
			submitRemediation(t, m, h.repository, h.clock, "fast")
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			err := m.Run(ctx, generation.CoordinatorConfig{Concurrency: 1, PollEvery: time.Millisecond})
			if permanent {
				if !errors.Is(err, failure) || adapter.starts.Load() != 0 {
					t.Fatalf("fatal result=%v starts=%d", err, adapter.starts.Load())
				}
			} else if !errors.Is(err, context.DeadlineExceeded) || adapter.starts.Load() != 1 {
				t.Fatalf("transient result=%v starts=%d", err, adapter.starts.Load())
			}
		})
	}
}

func (a *coordinatorAdapter) DecodeWebhook(_ context.Context, r generation.WebhookDecodeRequest) (generation.Observation, error) {
	var obs generation.Observation
	err := json.Unmarshal(r.Payload, &obs)
	return obs, err
}
func TestOutOfOrderObservationsCannotRegressProgressOrReplaceMetadata(t *testing.T) {
	adapter := &coordinatorAdapter{}
	h := newHarness(t, adapter, nil)
	m := remediationModule(t, h, h.repository, adapter)
	id := submitRemediation(t, m, h.repository, h.clock, "fast")
	if _, err := m.ReconcileOnce(context.Background(), generation.CoordinatorConfig{}); err != nil {
		t.Fatal(err)
	}
	send := func(sequence int64, phase generation.UpstreamPhase, progress int, key string) {
		t.Helper()
		payload, _ := json.Marshal(generation.Observation{Sequence: sequence, Phase: phase, Job: &generation.UpstreamJob{ProviderID: "meshy", RemoteID: string(id)}, Progress: progress, DedupeKey: key, Opaque: json.RawMessage(fmt.Sprintf(`{"key":%q}`, key))})
		if _, err := m.IngestProviderWebhook(context.Background(), generation.ProviderWebhookCommand{ProviderID: "meshy", RemoteID: string(id), EventType: "test.webhook", Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	send(2, generation.UpstreamRunning, 80, "new")
	send(1, generation.UpstreamAccepted, 10, "old")
	aggregate, err := h.repository.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.Task.Progress != 80 || aggregate.CurrentAttempt.LastObservationKey != "new" {
		t.Fatalf("old observation overwrote progress/metadata: %#v %#v", aggregate.Task, aggregate.CurrentAttempt)
	}
	send(3, generation.UpstreamCompleted, 100, "complete")
	before, _ := h.repository.Load(context.Background(), id)
	send(3, generation.UpstreamCompleted, 100, "complete")
	send(4, generation.UpstreamRunning, 85, "late-running")
	after, _ := h.repository.Load(context.Background(), id)
	if after.Task.State != generation.TaskMaterializing || after.Task.Version != before.Task.Version || after.Task.Progress < 80 {
		t.Fatalf("terminal observation was not idempotent: %#v", after.Task)
	}
}

type cancelAdapter struct {
	coordinatorAdapter
	calls   atomic.Int32
	failure error
}

func (a *cancelAdapter) CancelProviderTask(ctx context.Context, r generation.DeleteProviderRequest) error {
	a.calls.Add(1)
	return a.failure
}

type cancelCatalog struct{ generation.Catalog }

func (c cancelCatalog) Get(id generation.OfferingID) (generation.Offering, bool) {
	o, ok := c.Catalog.Get(id)
	o.Cancellation.Supported = true
	return o, ok
}
func (c cancelCatalog) Normalize(cap generation.Capability, id generation.OfferingID, input json.RawMessage) (generation.Offering, json.RawMessage, error) {
	o, v, e := c.Catalog.Normalize(cap, id, input)
	o.Cancellation.Supported = true
	return o, v, e
}
func TestCancellationCallsDeclaredProviderOnceAndRecordsOutcome(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			a := &cancelAdapter{}
			if failed {
				a.failure = errors.New("upstream unavailable")
			}
			h := newHarness(t, a, nil)
			m := remediationModuleWithClock(t, h, h.repository, a, h.clock)
			id := submitRemediation(t, m, h.repository, h.clock, "fast")
			// 结构性对策（注入时钟，分类 a 测试装置缺陷）：原实现用 liveClock
			// 单发 ReconcileOnce(空 cfg) 且忽略领取计数——RFC3339Nano 截尾零后
			// next_action_at("…12.284Z") 与 claim now("…12.284596Z") 在 SQLite
			// TEXT 比较中因 'Z' > 数字而翻转（实测负载下 0 leases，任务从未
			// Start），Cancel 对无 attempt 的 QUEUED 任务按契约走 LocalOnly
			// 零上游调用 ⇒ calls=0 假失败。fakeClock 整秒推进使全部时间戳无
			// 小数位、字典序即时序，领取确定成功；并显式断言领取数与
			// UpstreamJob 已落库，未启动就并发取消的静默前提被消除。
			h.clock.Advance(time.Second)
			n, err := m.ReconcileOnce(context.Background(), generation.CoordinatorConfig{})
			if err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Fatalf("reconcile claimed %d leases, expected the submitted task to start", n)
			}
			started, err := h.repository.Load(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if started.Task.State != generation.TaskRunning || started.CurrentAttempt == nil || started.CurrentAttempt.UpstreamJob == nil {
				t.Fatalf("task not started before concurrent cancels: state=%s attempt=%+v", started.Task.State, started.CurrentAttempt)
			}
			var wg sync.WaitGroup
			errs := make(chan error, 12)
			for range 12 {
				wg.Add(1)
				go func() { defer wg.Done(); _, err := m.Cancel(context.Background(), "audit", id); errs <- err }()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			result, err := m.Cancel(context.Background(), "audit", id)
			if err != nil {
				t.Fatal(err)
			}
			if a.calls.Load() != 1 || result.State != generation.TaskCanceled {
				t.Fatalf("calls=%d state=%s", a.calls.Load(), result.State)
			}
			expected := result.Offering.Cancellation.Confirmed
			if failed {
				expected = result.Offering.Cancellation.Failed
			}
			if result.Failure == nil || result.Failure.SafeDetail != expected {
				t.Fatalf("outcome=%#v want=%q", result.Failure, expected)
			}
		})
	}
}
func TestCancellationWithoutDeclaredProtocolWarnsAboutUpstream(t *testing.T) {
	a := &coordinatorAdapter{}
	h := newHarness(t, a, nil)
	// 结构性对策（注入时钟，分类 a 测试装置缺陷）：与
	// TestCancellationCallsDeclaredProviderOnceAndRecordsOutcome 同源的潜伏 CI
	// flake。原实现用 liveClock 单发 ReconcileOnce(空 cfg) 且忽略领取计数——
	// RFC3339Nano 截尾零后 next_action_at("…12.284Z") 与 claim now("…12.284596Z")
	// 在 SQLite TEXT 比较中因 'Z'(0x5A) > 数字而翻转（实测负载下 0 leases，任务
	// 从未 Start）。本测试断言 SafeDetail == Cancellation.Unsupported，而
	// cancellation.go 仅在 CurrentAttempt!=nil && UpstreamJob!=nil && !Supported
	// 时给出 Unsupported；0-lease 翻转令任务停在 QUEUED 无 attempt ⇒ Cancel 走
	// LocalOnly("任务已取消，尚未向上游提交") ⇒ 假失败。fakeClock 整秒推进使全部
	// 时间戳无小数位、字典序即时序，领取确定成功；并显式断言领取数 n==1 与已启动
	// 状态（RUNNING + CurrentAttempt + UpstreamJob 已落库），再做取消断言。
	// 意图不变：coordinatorAdapter 非 cancelAdapter，remediationModuleWithClock
	// 不套 cancelCatalog ⇒ Offering.Cancellation.Supported=false（无声明取消协议）
	// ⇒ 取消走 LocalOnly/warn，SafeDetail == Offering.Cancellation.Unsupported。
	m := remediationModuleWithClock(t, h, h.repository, a, h.clock)
	id := submitRemediation(t, m, h.repository, h.clock, "fast")
	h.clock.Advance(time.Second)
	n, err := m.ReconcileOnce(context.Background(), generation.CoordinatorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reconcile claimed %d leases, expected the submitted task to start", n)
	}
	started, err := h.repository.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if started.Task.State != generation.TaskRunning || started.CurrentAttempt == nil || started.CurrentAttempt.UpstreamJob == nil {
		t.Fatalf("task not started before cancellation: state=%s attempt=%+v", started.Task.State, started.CurrentAttempt)
	}
	result, err := m.Cancel(context.Background(), "audit", id)
	if err != nil {
		t.Fatal(err)
	}
	if result.Failure == nil || result.Failure.SafeDetail != result.Offering.Cancellation.Unsupported {
		t.Fatalf("unsupported outcome=%#v", result.Failure)
	}
}

func TestHistoricalCancellationStillUsesSchemaMessages(t *testing.T) {
	adapter := &coordinatorAdapter{}
	h := newHarness(t, adapter, nil)
	// 结构性对策（注入时钟，分类 a 测试装置缺陷）：与
	// TestCancellationCallsDeclaredProviderOnceAndRecordsOutcome 同源的潜伏 CI
	// flake。原实现用 liveClock 单发 ReconcileOnce(空 cfg) 且忽略领取计数——
	// RFC3339Nano 截尾零后 next_action_at("…12.284Z") 与 claim now("…12.284596Z")
	// 在 SQLite TEXT 比较中因 'Z'(0x5A) > 数字而翻转（实测负载下 0 leases，任务
	// 从未 Start）。本测试断言 SafeDetail 含 "上游可能继续执行和计费"，而
	// cancellation.go 仅在 CurrentAttempt!=nil（且 UpstreamJob!=nil）时给出
	// Unknown/Unsupported/Failed 三条含该子串的文案；0-lease 翻转令任务停在 QUEUED
	// 无 attempt ⇒ Cancel 走 LocalOnly("任务已取消，尚未向上游提交")，不含该子串
	// ⇒ 假失败。fakeClock 整秒推进使全部时间戳无小数位、字典序即时序，领取确定
	// 成功；并在历史改写前显式断言领取数 n==1 与已启动状态（RUNNING +
	// CurrentAttempt + UpstreamJob 已落库），确保历史取消路径建立在真实上游尝试上。
	// 意图不变：把 Offering 改写为已下架历史供给项 + 空 CancellationPolicy 后，
	// catalog.Get 未命中 ⇒ 回退 defaultCancellation（Supported=false）⇒ Cancel 仍
	// 用 Schema 文案（Unsupported 含 "上游可能继续执行和计费"）。
	m := remediationModuleWithClock(t, h, h.repository, adapter, h.clock)
	id := submitRemediation(t, m, h.repository, h.clock, "fast")
	h.clock.Advance(time.Second)
	n, err := m.ReconcileOnce(context.Background(), generation.CoordinatorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reconcile claimed %d leases, expected the submitted task to start", n)
	}
	aggregate, err := h.repository.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.Task.State != generation.TaskRunning || aggregate.CurrentAttempt == nil || aggregate.CurrentAttempt.UpstreamJob == nil {
		t.Fatalf("task not started before historical rewrite: state=%s attempt=%+v", aggregate.Task.State, aggregate.CurrentAttempt)
	}
	task := aggregate.Task
	task.Offering.ID = "removed/historical/offering"
	task.Offering.Cancellation = generation.CancellationPolicy{}
	task.Version++
	if err := h.repository.Commit(context.Background(), generation.Commit{ExpectedVersion: aggregate.Task.Version, Task: task}); err != nil {
		t.Fatal(err)
	}
	result, err := m.Cancel(context.Background(), "audit", id)
	if err != nil {
		t.Fatal(err)
	}
	if result.Failure == nil || !strings.Contains(result.Failure.SafeDetail, "上游可能继续执行和计费") {
		t.Fatalf("historical cancellation detail=%#v", result.Failure)
	}
}

type cappedAdapter struct {
	coordinatorAdapter
	active atomic.Int32
	peak   atomic.Int32
	calls  chan struct{}
}

func (a *cappedAdapter) Start(ctx context.Context, r generation.StartRequest) (generation.Observation, error) {
	active := a.active.Add(1)
	defer a.active.Add(-1)
	for old := a.peak.Load(); active > old; old = a.peak.Load() {
		if a.peak.CompareAndSwap(old, active) {
			break
		}
	}
	a.calls <- struct{}{}
	<-ctx.Done()
	return generation.Observation{}, ctx.Err()
}
func TestCoordinatorBackpressureBoundsAllInFlightCalls(t *testing.T) {
	adapter := &cappedAdapter{calls: make(chan struct{}, 20)}
	h := newHarness(t, adapter, nil)
	m := remediationModule(t, h, h.repository, adapter)
	for range 12 {
		submitRemediation(t, m, h.repository, h.clock, "fast")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	cfg := generation.CoordinatorConfig{Concurrency: 3, PollEvery: time.Millisecond, LeaseFor: time.Second, LeaseRenewEvery: 50 * time.Millisecond}
	go func() { done <- m.Run(ctx, cfg) }()
	for range 3 {
		select {
		case <-adapter.calls:
		case <-time.After(time.Second):
			t.Fatal("workers did not fill slots")
		}
	}
	count, err := m.ReconcileOnce(ctx, cfg)
	if err != nil || count != 0 {
		t.Fatalf("extra reconciliation exceeded cap: count=%d error=%v", count, err)
	}
	select {
	case <-adapter.calls:
		t.Fatal("backlog bypassed concurrency bound")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if adapter.peak.Load() != 3 || adapter.active.Load() != 0 {
		t.Fatalf("peak=%d active=%d", adapter.peak.Load(), adapter.active.Load())
	}
}

func TestCoordinatorRecoversRealSQLiteBusy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.db")
	first, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := first.Exec(`CREATE TABLE probe(id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	tx, err := first.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO probe VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	second, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_, busy := second.Exec(`INSERT INTO probe VALUES (2)`)
	if busy == nil {
		t.Fatal("fixture did not obtain SQLite busy")
	}
	_ = tx.Rollback()
	adapter := &coordinatorAdapter{}
	h := newHarness(t, adapter, nil)
	repo := &leaseRepo{Repository: h.repository, claimError: busy}
	m := remediationModule(t, h, repo, adapter)
	submitRemediation(t, m, h.repository, h.clock, "fast")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = m.Run(ctx, generation.CoordinatorConfig{Concurrency: 1, PollEvery: time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) || adapter.starts.Load() != 1 {
		t.Fatalf("sqlite busy stopped coordinator: err=%v starts=%d", err, adapter.starts.Load())
	}
}
