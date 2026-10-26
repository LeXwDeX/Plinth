package generation_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
)

// 本文件是 F1a 机制复现（/tmp/f1-diagnosis/probe-runs/t2-summary.txt，
// mechanism_b）的确定性回归测试：M2（续租冲突 cancel 中止终态提交）与
// M3（claim 前采样时钟 → 租约出生即过期 → 提交 CAS 恒冲突）链式丢失
// SubmissionUnknown 终态。两个生产时序都用注入时钟 + 事件门钉死，
// 不依赖墙钟余量（无墙钟睡眠；所有 30s 上界只是防挂死护栏，同
// awaitRenewalBeyond 纪律）。

// terminalCommitProbeRepo 在真实 SQLite 仓库外钉死两个生产时序：
//   - ClaimReady 钩子（仅重驱 pass 武装）：调度器已采样 now 之后、写事务
//     执行之前，把注入时钟拨过租约 TTL——等价于 t2 捕获里 82.8~292.3ms
//     的 claim→commit 延迟 vs LeaseFor，制造 M3 判别条件；
//   - Commit 钩子（仅重驱 pass 武装）：把终态提交 hold 到本轮续租冲突
//     真实触发之后；若提交仍骑在挂接的 callCtx 上（修复前），继续等到
//     renew 诱发的 cancel 落地再放行，使 M2 中止成为确定性事件而非竞态。
//     修复后终态提交抵达时已脱离取消链（Done()==nil），立即放行。
//   - RenewLease 钩子（首 pass）：先等 begin_attempt 落库再放行续租冲突，
//     消除“首个 tick 抢占提交预留”的墙钟窗口。
type terminalCommitProbeRepo struct {
	*leaseRepo
	clock         *fakeClock
	stallArmed    atomic.Bool
	gateArmed     atomic.Bool
	conflictSeen  atomic.Bool
	beginOnce     sync.Once
	beginDone     chan struct{}
	lastCommitErr atomic.Value // error
	mu            sync.Mutex
	renewConflict chan struct{}
	conflictSent  bool
}

func newTerminalCommitProbeRepo(real *sqlite.Repository, clock *fakeClock) *terminalCommitProbeRepo {
	return &terminalCommitProbeRepo{
		leaseRepo:     &leaseRepo{Repository: real},
		clock:         clock,
		beginDone:     make(chan struct{}),
		renewConflict: make(chan struct{}),
	}
}

// rearmGate 在两 pass 之间（协调器完全静止时）重置重驱 pass 的事件门。
func (r *terminalCommitProbeRepo) rearmGate() {
	r.mu.Lock()
	r.renewConflict = make(chan struct{})
	r.conflictSent = false
	r.mu.Unlock()
	r.conflictSeen.Store(false)
	r.stallArmed.Store(true)
	r.gateArmed.Store(true)
}

func (r *terminalCommitProbeRepo) signalConflict() {
	r.conflictSeen.Store(true)
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.conflictSent {
		r.conflictSent = true
		close(r.renewConflict)
	}
}

func (r *terminalCommitProbeRepo) conflictChan() chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.renewConflict
}

func (r *terminalCommitProbeRepo) ClaimReady(ctx context.Context, worker string, now time.Time, ttl time.Duration, limit int) ([]generation.Lease, error) {
	if r.stallArmed.CompareAndSwap(true, false) {
		// M3 判别条件：调度器已带着陈旧 now 进入本调用；在写事务执行前把
		// 共享注入时钟拨过 TTL。修复前 lease_until=陈旧 now+TTL（出生即
		// 过期）；修复后 lease_until 由写事务时刻派生，TTL 完整。
		r.clock.Advance(ttl + 10*time.Millisecond)
	}
	return r.leaseRepo.ClaimReady(ctx, worker, now, ttl, limit)
}

func (r *terminalCommitProbeRepo) RenewLease(ctx context.Context, id generation.TaskID, token string, now time.Time, ttl time.Duration) error {
	if !r.gateArmed.Load() {
		// 首 pass：begin_attempt 落库前不许续租冲突抢占（事件排序，无墙钟竞态）。
		select {
		case <-r.beginDone:
		case <-time.After(30 * time.Second):
			return errors.New("regression gate: begin_attempt never committed before renewal")
		}
	}
	err := r.leaseRepo.RenewLease(ctx, id, token, now, ttl)
	if err != nil && r.gateArmed.Load() {
		r.signalConflict()
	}
	return err
}

func (r *terminalCommitProbeRepo) Commit(ctx context.Context, commit generation.Commit) error {
	if commit.Attempt != nil && commit.Attempt.State == generation.AttemptSubmitting && !commit.ClearLease {
		err := r.leaseRepo.Commit(ctx, commit)
		if err == nil {
			r.beginOnce.Do(func() { close(r.beginDone) })
		}
		return err
	}
	if r.gateArmed.Load() && commit.ClearLease && commit.Task.State == generation.TaskFailed &&
		commit.Attempt != nil && commit.Attempt.State == generation.AttemptSubmissionUnknown {
		// 重驱 pass 的 fail-closed 终态提交：先等本轮续租冲突真实触发。
		select {
		case <-r.conflictChan():
		case <-time.After(30 * time.Second):
			return errors.New("regression gate: renewal conflict never fired before the terminal commit")
		}
		// 修复前：ctx 就是被 renew 冲突 cancel 的 callCtx——等 cancel 落地，
		// 使 M2 中止确定性发生。修复后：ctx 已按 cancellation.go:72/:85 的
		// WithoutCancel 纪律脱离取消链（Done()==nil），立即放行，cancel 无法
		// 中止它。
		if ctx.Done() != nil {
			select {
			case <-ctx.Done():
			case <-time.After(30 * time.Second):
				return errors.New("regression gate: attached commit ctx was never cancelled after renewal conflict")
			}
		}
	}
	err := r.leaseRepo.Commit(ctx, commit)
	if err != nil && r.gateArmed.Load() && commit.ClearLease && commit.Task.State == generation.TaskFailed {
		r.lastCommitErr.Store(err)
	}
	return err
}

// TestSubmissionUnknownTerminalCommitSurvivesRenewalCancelAndStaleLease 钉死
// F1a mechanism_b 的完整链条：非幂等提交冻结（AttemptSubmitting 已落库）→
// 租约过期 → 重驱 claim→commit 延迟 > LeaseFor（注入时钟）→ 重驱期间续租
// 冲突 cancel（failRenew）→ 断言 SubmissionUnknown 终态被记录且不重发。
// 修复前：终态提交被 context canceled（M2）或 repository: conflict（M3）
// 丢弃，任务停留 State:QUEUED Failure:nil Version:2（非收敛重驱）。
func TestSubmissionUnknownTerminalCommitSurvivesRenewalCancelAndStaleLease(t *testing.T) {
	adapter := &coordinatorAdapter{slow: make(chan struct{}), release: make(chan struct{})}
	// 非零纳秒起点：formatTime(RFC3339Nano) 去掉尾零，0 纳秒与小数秒混比时
	// 字典序 != 时间序（"…00.1Z" < "…00Z"）；固定 9 位小数保证 CAS 字符串
	// 比较与时间序一致。生产 liveClock 天然纳秒精度，不受此影响。
	clock := &fakeClock{now: time.Date(2026, 7, 26, 12, 0, 0, 123456789, time.UTC)}
	real, err := sqlite.Open(filepath.Join(t.TempDir(), "regression.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = real.Close() })
	// 单一时间线：模块时钟 == 仓库租约写时钟（合成时间线下二者必须一致）。
	real.SetClock(clock.Now)
	repo := newTerminalCommitProbeRepo(real, clock)
	repo.failRenew.Store(true)

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
	admissionRepo := preparedAdmissionRepository{Repository: repo, base: real}
	m, err := generation.New(generation.Dependencies{Repository: admissionRepo, Catalog: c, Providers: registry, Assets: assets, Clock: clock, Random: func() float64 { return 0.5 }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })

	id := submitRemediation(t, m, real, clock, "slow")
	cfg := generation.CoordinatorConfig{WorkerID: "one", LeaseFor: 100 * time.Millisecond, LeaseRenewEvery: 50 * time.Millisecond, Concurrency: 1, BatchSize: 1}

	// ── pass 1：真实非幂等提交在途时续租冲突 → 调用被取消。按租约契约，
	// 丢失租约的 worker 不得落终态（同 test2 :216）：任务必须停留
	// RUNNING/Version:2（领取即 RUNNING，begin_attempt 持久化），
	// AttemptSubmitting 预留已持久化，等待重驱。
	if _, err := m.ReconcileOnce(context.Background(), cfg); err != nil {
		t.Fatalf("pass 1: %v", err)
	}
	task, err := m.Get(context.Background(), "audit", id)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskRunning || task.Failure != nil || task.Version != 2 {
		t.Fatalf("pass 1 must leave the frozen submission re-drivable per the lease-loss contract: State:%q Failure:%v Version:%d", task.State, task.Failure, task.Version)
	}
	aggregate, err := repo.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.CurrentAttempt == nil || aggregate.CurrentAttempt.State != generation.AttemptSubmitting {
		t.Fatalf("pass 1 must durably reserve the non-idempotent submission: %#v", aggregate.CurrentAttempt)
	}
	if adapter.starts.Load() != 1 {
		t.Fatalf("pass 1 starts=%d want 1", adapter.starts.Load())
	}

	// 注入时钟上确定性地让 pass 1 租约过期（替代 test2 的墙钟 sleep）。
	clock.Advance(cfg.LeaseFor + 10*time.Millisecond)
	repo.rearmGate()

	// ── pass 2（重驱）：claim→commit 延迟已被拨过 LeaseFor（M3），且提交
	// 被 hold 到续租冲突 cancel 落地之后（M2）。fail-closed 终态必须记录。
	if _, err := m.ReconcileOnce(context.Background(), cfg); err != nil {
		t.Fatalf("re-drive: %v", err)
	}
	if !repo.conflictSeen.Load() {
		t.Error("renewal conflict never fired during the re-drive — M2 race was not exercised")
	}
	task, err = m.Get(context.Background(), "audit", id)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskFailed || task.Failure == nil || task.Failure.Class != generation.FailureSubmissionUnknown {
		commitErr, _ := repo.lastCommitErr.Load().(error)
		t.Fatalf("terminal SubmissionUnknown state LOST (M2/M3 chain): State:%q Failure:%v Version:%d starts=%d terminalCommitErr=%v",
			task.State, task.Failure, task.Version, adapter.starts.Load(), commitErr)
	}
	if task.Version != 3 {
		t.Fatalf("terminal version=%d want 3", task.Version)
	}
	if adapter.starts.Load() != 1 {
		t.Fatalf("non-idempotent submission was re-driven to the provider: starts=%d want 1", adapter.starts.Load())
	}
	aggregate, err = repo.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.CurrentAttempt == nil || aggregate.CurrentAttempt.State != generation.AttemptSubmissionUnknown {
		t.Fatalf("attempt must be fail-closed SubmissionUnknown: %#v", aggregate.CurrentAttempt)
	}

	// 收敛性：终态不可再领取，非收敛重驱循环必须终结。
	count, err := m.ReconcileOnce(context.Background(), cfg)
	if err != nil || count != 0 {
		t.Fatalf("terminal task was re-claimable: n=%d err=%v", count, err)
	}
	after, err := m.Get(context.Background(), "audit", id)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != generation.TaskFailed || after.Version != 3 || after.Failure == nil || after.Failure.Class != generation.FailureSubmissionUnknown {
		t.Fatalf("terminal state regressed: %#v", after)
	}
}
