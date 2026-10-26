package generation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"sync"
	"syscall"
	"time"
)

func (m *Module) Run(ctx context.Context, cfg CoordinatorConfig) error {
	cfg = normalizeCoordinatorConfig(cfg, m.ids)
	if err := validateCoordinatorConfig(cfg); err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// 排水 ctx：停机（runCtx 取消）后停止领取新任务，但在飞的
	// Provider 调用与租约续期继续到自然完成——否则发布/重启会把
	// 已付费的同步生图提交中断成 SUBMISSION_UNKNOWN→FAILED。窗口
	// 上限为 ShutdownGrace，超时才强制中断。
	drainCtx, drainCancel := context.WithCancel(context.WithoutCancel(ctx))
	defer drainCancel()
	grace := cfg.ShutdownGrace
	if grace <= 0 {
		grace = 30 * time.Second
	}
	drainStopOnce := sync.Once{}
	go func() {
		<-runCtx.Done()
		drainStopOnce.Do(func() {
			timer := time.NewTimer(grace)
			defer timer.Stop()
			select {
			case <-timer.C:
				drainCancel()
			case <-drainCtx.Done():
			}
		})
	}()
	results := make(chan error, cfg.Concurrency)
	// Workers claim only when ready to execute. No in-memory queue holds leases
	// behind a long synchronous provider call; SQLite remains the durable backlog.
	for i := 0; i < cfg.Concurrency; i++ {
		go func(index int) {
			worker := cfg
			worker.WorkerID = fmt.Sprintf("%s:%d", cfg.WorkerID, index)
			worker.BatchSize = 1
			failures := 0
			for {
				// 领取与执行分离：领取随 runCtx 立即停止；
				// 在飞步骤随 drainCtx 排水完成。
				_, err := m.ReconcileOnceWithStepContext(runCtx, drainCtx, worker)
				if runCtx.Err() != nil {
					results <- runCtx.Err()
					return
				}
				delay := cfg.PollEvery
				if err != nil {
					if !recoverableCoordinatorError(err) {
						var persistenceError *Error
						if errors.As(err, &persistenceError) && persistenceError.Cause != nil {
							err = fmt.Errorf("generation coordinator %s: %w; cause: %v", persistenceError.Operation, err, persistenceError.Cause)
						}
						// 单个任务的错误（坏数据、唯一约束、来源暂不可读等）
						// 只记录并退避跳过，不拖垮 HTTP 与其他任务（#298：
						// 重复的上游任务 ID 曾经让整个进程退出）。只有数据库
						// 整体不可用（损坏、只读、无法打开、连接已关闭）才
						// 结束协调器并让进程退出。
						if globalPersistenceFailure(err) {
							results <- err
							return
						}
						m.logger.Error(
							"协调器步骤失败，退避跳过",
							"worker", worker.WorkerID,
							"error", err,
						)
					}
					failures++
					delay = m.jitter(exponentialDelay(cfg.PollEvery, cfg.MaxBackoff, failures))
				} else {
					failures = 0
				}
				timer := time.NewTimer(delay)
				select {
				case <-runCtx.Done():
					timer.Stop()
					results <- runCtx.Err()
					return
				case <-timer.C:
				}
			}
		}(i)
	}
	// On shutdown or a permanent error cancel all HTTP calls and drain every worker
	// before returning, so app.Run can safely close the shared repository.
	var first error
	for range cfg.Concurrency {
		err := <-results
		if first == nil {
			first = err
			cancel()
		}
	}
	return first
}

// globalPersistenceFailure reports errors after which no task can make
// progress: the database is corrupt, not a database, read-only, cannot be
// opened, denies access, or its handle is closed. Every other step error
// (constraint violations, per-row data, unreadable inputs, adapter faults,
// errors mapped to ErrUnavailable without such a cause) belongs to one task
// and must not stop the coordinator.
func globalPersistenceFailure(err error) bool {
	if errors.Is(err, sql.ErrConnDone) {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, cause := range joined.Unwrap() {
			if globalPersistenceFailure(cause) {
				return true
			}
		}
		return false
	}
	var code interface{ Code() int }
	if errors.As(err, &code) {
		switch code.Code() & 255 {
		case 3, 8, 11, 14, 23, 26: // PERM, READONLY, CORRUPT, CANTOPEN, AUTH, NOTADB
			return true
		}
	}
	return false
}

func validateCoordinatorConfig(cfg CoordinatorConfig) error {
	if cfg.LeaseRenewEvery <= 0 || cfg.LeaseRenewEvery >= cfg.LeaseFor {
		return errors.New("generation: lease renewal interval must be positive and shorter than lease duration")
	}
	return nil
}

func (m *Module) ReconcileOnce(ctx context.Context, cfg CoordinatorConfig) (int, error) {
	return m.ReconcileOnceWithStepContext(ctx, ctx, cfg)
}

// ReconcileOnceWithStepContext 分离领取与执行上下文：claim 随 ctx 停止，
// 已领取的步骤随 stepCtx 排水完成（停机语义由 Run 装配）。
func (m *Module) ReconcileOnceWithStepContext(
	ctx context.Context,
	stepCtx context.Context,
	cfg CoordinatorConfig,
) (int, error) {
	cfg = normalizeCoordinatorConfig(cfg, m.ids)
	if err := validateCoordinatorConfig(cfg); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.coordinatorMu.Lock()
	available := max(0, cfg.Concurrency-m.activeSteps)
	reserved := min(cfg.BatchSize, available)
	m.activeSteps += reserved
	m.coordinatorMu.Unlock()
	if reserved == 0 {
		return 0, nil
	}
	defer func() { m.coordinatorMu.Lock(); m.activeSteps -= reserved; m.coordinatorMu.Unlock() }()
	leases, err := m.repository.ClaimReady(ctx, cfg.WorkerID, m.clock.Now(), cfg.LeaseFor, reserved)
	if err != nil {
		return 0, mapRepositoryError("claim_work", err)
	}
	results := make(chan error, len(leases))
	var wg sync.WaitGroup
	for _, lease := range leases {
		wg.Add(1)
		go func(lease Lease) {
			defer wg.Done()
			defer func() {
				// 步骤级 panic 隔离：一行坏数据/适配器缺陷不得终止进程，
				// 转为步骤错误由租约超时后的下一次认领接管。
				if r := recover(); r != nil {
					results <- fmt.Errorf(
						"generation step panic (task %s): %v",
						lease.Task.ID, r,
					)
				}
			}()
			if err := m.processRenewingLease(stepCtx, lease, cfg); err != nil {
				results <- fmt.Errorf("task %s: %w", lease.Task.ID, err)
				return
			}
			results <- nil
		}(lease)
	}
	wg.Wait()
	close(results)
	var combined error
	for err := range results {
		if err != nil && !IsCode(err, ErrConflict) && !IsCode(err, ErrNotFound) {
			combined = errors.Join(combined, err)
		}
	}
	return len(leases), combined
}

func (m *Module) processRenewingLease(ctx context.Context, lease Lease, cfg CoordinatorConfig) error {
	callCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(cfg.LeaseRenewEvery)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-callCtx.Done():
				return
			case <-ticker.C:
				if err := m.repository.RenewLease(callCtx, lease.Task.ID, lease.Token, m.clock.Now(), cfg.LeaseFor); err != nil {
					cancel(mapRepositoryError("renew_lease", err))
					return
				}
			}
		}
	}()
	err := m.processLease(callCtx, lease)
	close(done)
	<-stopped
	if err != nil && context.Cause(callCtx) != nil {
		return context.Cause(callCtx)
	}
	return err
}

func recoverableCoordinatorError(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, cause := range joined.Unwrap() {
			if !recoverableCoordinatorError(cause) {
				return false
			}
		}
		return true
	}
	var code interface{ Code() int }
	if errors.As(err, &code) {
		switch code.Code() & 255 {
		case 5, 6, 10, 13:
			return true
		default:
			return false
		}
	}
	var temporary interface{ Temporary() bool }
	if errors.As(err, &temporary) && temporary.Temporary() {
		return true
	}
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EIO) || errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, io.ErrUnexpectedEOF)
}

func (m *Module) jitter(delay time.Duration) time.Duration {
	m.randomMu.Lock()
	random := m.random()
	m.randomMu.Unlock()
	if random < 0 {
		random = 0
	}
	if random > 1 {
		random = 1
	}
	return max(time.Nanosecond, time.Duration(float64(delay)*(0.5+random*0.5)))
}

func (m *Module) taskRetryDelay(policy RetryPolicy, ordinal int, requested time.Duration) time.Duration {
	maximum := policy.MaxBackoff
	if maximum <= 0 {
		maximum = time.Minute
	}
	return min(maximum, max(requested, m.jitter(retryDelay(policy, ordinal, 0))))
}
