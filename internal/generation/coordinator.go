package generation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/cachemanagement"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type CoordinatorConfig struct {
	WorkerID        string
	PollEvery       time.Duration
	LeaseFor        time.Duration
	BatchSize       int
	IdleBackoff     time.Duration
	Concurrency     int
	LeaseRenewEvery time.Duration
	MaxBackoff      time.Duration
	// ShutdownGrace 界定停机排水窗口；<=0 视为未声明，按默认值处理。
	ShutdownGrace time.Duration
}

func normalizeCoordinatorConfig(cfg CoordinatorConfig, ids IDGenerator) CoordinatorConfig {
	if cfg.WorkerID == "" {
		cfg.WorkerID = "worker_" + ids.Token()
	}
	if cfg.PollEvery <= 0 {
		cfg.PollEvery = 500 * time.Millisecond
	}
	if cfg.LeaseFor <= 0 {
		cfg.LeaseFor = 2 * time.Minute
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 10
	}
	if cfg.BatchSize > 100 {
		cfg.BatchSize = 100
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 10
	}
	if cfg.Concurrency > 100 {
		cfg.Concurrency = 100
	}
	if cfg.LeaseRenewEvery <= 0 {
		cfg.LeaseRenewEvery = cfg.LeaseFor / 3
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 30 * time.Second
	}
	return cfg
}

func (m *Module) processLease(ctx context.Context, lease Lease) error {
	task := lease.Task
	// 成功后观察入口（§6.2 触点 2）：独立于通用终态释放——仅当声明
	// observe 且存在 0022 资源态时进入；否则维持既有终态 releaseLease。
	if task.State == TaskSucceeded {
		if postSuccessObservationActive(task) {
			return m.observePostSuccess(ctx, lease)
		}
		return m.releaseLease(ctx, lease, nil)
	}
	if task.State.Terminal() {
		return m.releaseLease(ctx, lease, nil)
	}

	switch task.State {
	case TaskQueued:
		return m.startAttempt(ctx, lease)
	case TaskRunning:
		// begin_attempt 在调用 Start 前把任务置 RUNNING 并落库；若此后租约
		// 丢失或进程崩溃，尝试仍是 SUBMITTING（尚无任何观察）。重驱必须走
		// startAttempt 的幂等闸与 M2 fail-close 恢复路径，与 QUEUED 冻结提交
		// 同构，不得进入观察流程。首次观察落地后尝试状态推进，恢复常规轮询。
		if lease.CurrentAttempt != nil && lease.CurrentAttempt.State == AttemptSubmitting {
			return m.startAttempt(ctx, lease)
		}
		if lease.CurrentAttempt != nil && lease.CurrentAttempt.LastObservationPhase == UpstreamPreparing && lease.CurrentAttempt.UpstreamJob == nil {
			return m.resumePreparation(ctx, lease)
		}
		return m.observeAttempt(ctx, lease)
	case TaskMaterializing:
		return m.materialize(ctx, lease)
	default:
		failure := &Failure{
			Class:      FailureInternal,
			SafeDetail: fmt.Sprintf("任务处于未知状态 %q", task.State),
		}
		return m.finishFailed(ctx, lease, lease.CurrentAttempt, failure, "task.invalid_state", nil)
	}
}

func (m *Module) startAttempt(ctx context.Context, lease Lease) error {
	// A refetch Task never calls Start: it reads its source's accepted job.
	if binding, refetch, err := m.upstreamRefetchOf(ctx, lease.Task.ID); err != nil {
		return err
	} else if refetch {
		return m.startRefetch(ctx, lease, binding)
	}
	task := lease.Task
	attempt := lease.CurrentAttempt
	fresh := false

	switch {
	case attempt == nil:
		attempt = m.newAttempt(task, 1)
		fresh = true
	case attempt.State == AttemptFailed:
		attempt = m.newAttempt(task, attempt.Ordinal+1)
		fresh = true
	case attempt.State == AttemptSubmitting:
		if !task.Offering.IdempotentSubmission {
			failure := &Failure{
				Class:      FailureSubmissionUnknown,
				Ambiguous:  true,
				SafeDetail: "上次提交可能已被上游接受，已停止自动重发以避免重复扣费",
			}
			attempt.State = AttemptSubmissionUnknown
			attempt.Failure = failure
			// M2：重驱冻结提交的 fail-closed 终态不得被续租 cancel 中止。
			// 续租失败会 cancel 同一个 callCtx（scheduler.go renew_lease 路径），
			// 挂接 ctx 上的 BeginTx 以 context canceled 中止，SubmissionUnknown
			// 终态丢失，任务停留可重领取状态非收敛重驱。按 cancellation.go:72/:85
			// 的既有纪律用 context.WithoutCancel 脱离取消链落库；所有权仍由
			// Commit CAS（version+lease_token+lease_until）裁决，租约真丢失时
			// 该提交照样冲突，不绕过任何并发防护。
			return m.finishFailed(context.WithoutCancel(ctx), lease, attempt, failure, "attempt.submission_unknown", nil)
		}
	default:
		failure := &Failure{
			Class:      FailureInternal,
			SafeDetail: fmt.Sprintf("排队任务的当前尝试状态非法: %s", attempt.State),
		}
		return m.finishFailed(ctx, lease, attempt, failure, "attempt.invalid_state", nil)
	}

	// Read admission before persisting SUBMITTING. A transient read failure
	// must not make a non-idempotent task look like an uncertain paid dispatch.
	taskDependencies, dependencyErr := m.resolveTaskDependencies(ctx, task.OwnerID, task.Offering, task.Input, task.SessionID)
	if dependencyErr != nil && !IsCode(dependencyErr, ErrInvalidInput) {
		return dependencyErr
	}
	var admissionErr error
	if dependencyErr == nil {
		admissionErr = m.verifyAdmittedDependencies(ctx, task, taskDependencies)
		if admissionErr != nil && !IsCode(admissionErr, ErrConflict) && !IsCode(admissionErr, ErrInvalidInput) {
			return admissionErr
		}
	}

	if fresh {
		now := m.clock.Now()
		task.CurrentAttemptID = attempt.ID
		// 领取租约即进入执行：summary 必须在首条 Observation 前投影
		// RUNNING，否则同步任务在上游回包前（可能数分钟）一直显示
		// QUEUED（排队中），托盘与任务事实不一致。转换表允许
		// QUEUED→RUNNING；可重试失败路径会经 RUNNING→QUEUED 回队。
		if task.State == TaskQueued {
			task.State = TaskRunning
		}
		task.UpdatedAt = now
		task.Version++
		if err := m.repository.Commit(ctx, Commit{
			LeaseToken:      lease.Token,
			ExpectedVersion: lease.Task.Version,
			Task:            task,
			Attempt:         attempt,
			Events: []Event{{
				TaskID:    task.ID,
				Type:      "attempt.started",
				Data:      eventData(map[string]any{"attempt_id": attempt.ID, "ordinal": attempt.Ordinal}),
				CreatedAt: now,
			}},
		}); err != nil {
			return mapRepositoryError("begin_attempt", err)
		}
		lease.Task = task
		lease.CurrentAttempt = attempt
	}

	port, ok := m.providers.Provider(task.Offering)
	if !ok {
		failure := &Failure{
			Class:      FailureInternal,
			SafeDetail: fmt.Sprintf("适配器 %q 未注册", task.Offering.AdapterKey),
		}
		return m.finishFailed(ctx, lease, attempt, failure, "attempt.adapter_missing", nil)
	}
	inputAssets, err := m.resolveAcceptedInputAssets(ctx, task)
	if err != nil {
		failure := &Failure{
			Class:      FailureInvalidInput,
			SafeDetail: "任务输入资产不可用或不属于当前用户",
		}
		return m.finishFailed(ctx, lease, attempt, failure, "attempt.input_asset_missing", nil)
	}
	if dependencyErr != nil {
		class := FailureInvalidInput
		if errors.Is(dependencyErr, ErrSourceExpired) {
			class = FailureSourceExpired
		}
		return m.finishFailed(ctx, lease, attempt, &Failure{
			Class: class, SafeDetail: dependencyErr.Error(),
		}, "attempt.task_dependency_missing", nil)
	}

	if admissionErr != nil {
		return m.finishFailed(ctx, lease, attempt, &Failure{
			Class:      FailureInvalidInput,
			SafeDetail: "任务依赖已变化或无法核验，请重新准备并确认执行",
		}, "attempt.task_dependency_changed", nil)
	}

	callCtx, cancel := m.withOfferingTimeout(ctx, task.Offering.Timeout)
	defer cancel()
	// 私有派发检查点在 Start 入口可用（§4.2）：绑定本租约与当前 Attempt。
	checkpoints := m.dispatchCheckpointerFor(&lease, attempt)
	if checkpoints != nil {
		callCtx = WithDispatchCheckpointer(callCtx, checkpoints)
	}
	obs, err := port.Start(callCtx, StartRequest{
		SessionID:        task.SessionID,
		OwnerID:          task.OwnerID,
		TaskID:           task.ID,
		AttemptID:        attempt.ID,
		SubmissionKey:    attempt.SubmissionKey,
		Offering:         task.Offering,
		Input:            schema.WireInput(task.Offering, task.Input),
		InputAssets:      inputAssets,
		TaskDependencies: taskDependencies,
	})
	if err != nil {
		failure := providerFailure(err, true)
		return m.handleAttemptFailure(ctx, lease, attempt, failure, nil)
	}
	if err := validateObservation(obs); err != nil {
		return m.handleAttemptFailure(ctx, lease, attempt, &Failure{
			Class:      FailureProtocol,
			SafeDetail: "上游返回了不可识别的任务状态",
		}, nil)
	}
	if obs.Phase == UpstreamPreparing && (!fresh || !validPreparationEntry(attempt, obs, port, checkpoints)) {
		return m.invalidPreparation(ctx, lease, attempt)
	}
	return m.applyObservation(ctx, lease, attempt, obs, "provider.start", checkpoints)
}

// The lease supplies the persisted Task and its frozen Offering. No caller
// preparing a new execution can opt into this compatibility boundary.
func (m *Module) resolveAcceptedInputAssets(ctx context.Context, task Task) (map[string][]InputAsset, error) {
	if resolver, ok := m.inputs.(AcceptedTaskInputResolver); ok {
		return resolver.ResolveAccepted(ctx, task)
	}
	return m.inputs.Resolve(ctx, task.OwnerID, task.Offering, task.Input)
}

func (m *Module) verifyAdmittedDependencies(ctx context.Context, task Task, dependencies map[string]TaskDependency) error {
	store, ok := m.repository.(TaskAdmissionStore)
	if !ok {
		if task.Admission != nil || m.preparedStore != nil {
			return &Error{Code: ErrUnavailable, Operation: "verify_task_admission", Detail: "任务批准记录暂不可核验"}
		}
		return nil // Explicit compatibility for unbound legacy repositories.
	}
	id, err := store.AdmittedPreparedInput(ctx, task.OwnerID, task.ID)
	if err != nil {
		return &Error{Code: ErrUnavailable, Operation: "verify_task_admission", Detail: "任务批准记录暂不可核验", Cause: err}
	}
	if id == "" {
		if task.Admission != nil {
			return &Error{Code: ErrInvalidInput, Operation: "verify_task_admission", Detail: "任务批准记录缺失"}
		}
		return nil
	}
	prepared, err := m.GetPreparedInput(ctx, task.OwnerID, id)
	if err != nil {
		if IsCode(err, ErrNotFound) {
			return &Error{Code: ErrInvalidInput, Operation: "verify_task_admission", Detail: "任务批准输入缺失"}
		}
		return err
	}
	if !reflect.DeepEqual(snapshotPreparedDependencies(dependencies), prepared.Dependencies) {
		return &Error{Code: ErrConflict, Operation: "verify_task_admission", Detail: "任务依赖已变化"}
	}
	return nil
}

func (m *Module) observeAttempt(ctx context.Context, lease Lease) error {
	task := lease.Task
	attempt := lease.CurrentAttempt
	if attempt == nil || attempt.State != AttemptRunning || attempt.UpstreamJob == nil {
		failure := &Failure{
			Class:      FailureInternal,
			SafeDetail: "运行中任务缺少可观察的上游作业",
		}
		return m.finishFailed(ctx, lease, attempt, failure, "attempt.job_missing", nil)
	}

	if m.jobTimeout(task) > 0 && m.clock.Now().Sub(attempt.StartedAt) > m.jobTimeout(task) {
		return m.handleAttemptFailure(ctx, lease, attempt, &Failure{
			Class:      FailureTimedOut,
			Retryable:  true,
			SafeDetail: "上游任务超过供给项允许的最长执行时间",
		}, nil)
	}

	port, ok := m.providers.Provider(task.Offering)
	if !ok {
		return m.finishFailed(ctx, lease, attempt, &Failure{
			Class:      FailureInternal,
			SafeDetail: fmt.Sprintf("适配器 %q 未注册", task.Offering.AdapterKey),
		}, "attempt.adapter_missing", nil)
	}
	callCtx, cancel := m.withOfferingTimeout(ctx, task.Offering.Timeout)
	defer cancel()
	// 私有派发检查点在 Observe 入口同样可用（§4.2）。
	checkpoints := m.dispatchCheckpointerFor(&lease, attempt)
	if checkpoints != nil {
		callCtx = WithDispatchCheckpointer(callCtx, checkpoints)
	}
	obs, err := port.Observe(callCtx, ObserveRequest{
		TaskID:    task.ID,
		AttemptID: attempt.ID,
		Offering:  task.Offering,
		Input:     schema.WireInput(task.Offering, task.Input),
		Job:       *attempt.UpstreamJob,
		Opaque:    attempt.Opaque,
	})
	if err != nil {
		failure := providerFailure(err, false)
		if failure.Retryable {
			return m.rescheduleObservation(ctx, lease, attempt, failure)
		}
		return m.handleAttemptFailure(ctx, lease, attempt, failure, nil)
	}
	if err := validateObservation(obs); err != nil {
		return m.handleAttemptFailure(ctx, lease, attempt, &Failure{
			Class:      FailureProtocol,
			SafeDetail: "上游返回了不可识别的任务状态",
		}, nil)
	}
	if obs.Phase == UpstreamPreparing {
		return m.invalidPreparation(ctx, lease, attempt)
	}
	return m.applyObservation(ctx, lease, attempt, obs, "provider.observe", checkpoints)
}

// dispatchCheckpointerFor 为当前租约构造私有派发检查点回调；仓储未实现
// DispatchCheckpointStore 时不注入（Provider 走既有直连路径，§4.5.4）。
func (m *Module) dispatchCheckpointerFor(lease *Lease, attempt *Attempt) *dispatchCheckpointer {
	store, ok := m.repository.(DispatchCheckpointStore)
	if !ok || lease == nil || attempt == nil {
		return nil
	}
	return &dispatchCheckpointer{
		store:     store,
		lease:     lease,
		attempt:   attempt,
		maxBytes:  m.dispatchCheckpointMaxBytes,
		clock:     m.clock,
		ackTokens: dispatchAckToken,
	}
}

func (m *Module) applyObservation(
	ctx context.Context,
	lease Lease,
	attempt *Attempt,
	obs Observation,
	eventType string,
	checkpoints *dispatchCheckpointer,
) error {
	// 转换校验的基准状态：attempt 与 lease.CurrentAttempt 是同一对象，
	// 下列快照必须在任何状态改写之前取得。
	taskStateBefore := lease.Task.State
	attemptStateBefore := AttemptState("")
	if attempt != nil {
		attemptStateBefore = attempt.State
	}
	// 私有派发回执校验（§4.3）：回执必须与当次租约内授予且未消费的 ack
	// 精确匹配；重复、伪造、陈旧或不可验证（webhook/刷新路径无检查点上
	// 下文）的回执一律保守丢弃——不改生命周期、不重发。
	// 回执校验先于陈旧/去重判定（S2）：已匹配当次 ack 的回执证明该次
	// 付费派发真实发生，即便 observation 本身被丢弃，也必须随该次提交
	// 结算检查点，否则栅栏永远停留在 dispatching。
	var settlement *DispatchReceiptSettlement
	if obs.Dispatch != nil {
		if !checkpoints.receiptValid(obs.Dispatch) {
			if lease.Token == "" {
				return nil
			}
			return m.rejectDispatchReceipt(ctx, lease, attempt, obs)
		}
		settlement = &DispatchReceiptSettlement{
			AttemptID:     attempt.ID,
			Sequence:      obs.Dispatch.Sequence,
			AckToken:      obs.Dispatch.AckToken,
			NotDispatched: obs.Dispatch.Result == DispatchResultNotDispatched,
		}
		checkpoints.markSettled()
	}
	if staleObservation(lease.Task, attempt, obs) {
		if validPrivateCheckpointUpdate(lease, attempt, obs, eventType) {
			return m.applyPrivateCheckpointUpdate(ctx, lease, attempt, obs)
		}
		if lease.Token == "" {
			return nil
		}
		return m.rescheduleDuplicateObservation(ctx, lease, attempt, Observation{}, settlement)
	}
	if obs.DedupeKey != "" && obs.DedupeKey == attempt.LastObservationKey {
		return m.rescheduleDuplicateObservation(ctx, lease, attempt, obs, settlement)
	}
	if obs.Phase == UpstreamPreparing {
		port, ok := m.providers.Provider(lease.Task.Offering)
		if eventType != "provider.start" || !ok || !validPreparationEntry(attempt, obs, port, checkpoints) {
			return m.invalidPreparation(ctx, lease, attempt)
		}
	}
	task := lease.Task
	now := m.clock.Now()
	// The Attempt as persisted, for a commit that cannot record obs.Job.
	recorded := *attempt
	attempt.UpdatedAt = now
	attempt.Opaque = obs.Opaque
	attempt.LastObservationKey = obs.DedupeKey
	attempt.LastObservationSequence = max(attempt.LastObservationSequence, obs.Sequence)
	if obs.ObservedAt.After(attempt.LastObservationAt) {
		attempt.LastObservationAt = obs.ObservedAt
	}
	attempt.LastObservationPhase = obs.Phase
	attempt.ObservationFailures = 0
	if obs.Job != nil {
		attempt.UpstreamJob = obs.Job
	}

	replaceClaims := false
	switch obs.Phase {
	case UpstreamPreparing:
		attempt.State = AttemptRunning
		task.State = TaskRunning
		task.Progress = 0
		task.NextActionAt = now.Add(observationDelay(task.Offering, obs))
	case UpstreamAccepted, UpstreamRunning:
		attempt.State = AttemptRunning
		task.State = TaskRunning
		task.Progress = max(task.Progress, clampProgress(obs.Progress))
		task.NextActionAt = now.Add(observationDelay(task.Offering, obs))
	case UpstreamCompleted:
		storedClaims, err := m.storeInlineImages(ctx, task, attempt, obs.Artifacts)
		if err != nil {
			// 内联落盘失败路径同样携带已验证回执结算（F1：含内联失败）。
			return m.finishFailed(ctx, lease, attempt, &Failure{Class: FailureIntegrity, SafeDetail: m.executionMessages(task).InlineStoreFailed}, "materialization.inline_store_failed", settlement)
		}
		obs.Artifacts = storedClaims
		attempt.State = AttemptUpstreamSucceeded
		attempt.FinishedAt = &now
		task.State = TaskMaterializing
		task.Progress = max(task.Progress, 99)
		task.MaterializeFailures = 0
		task.NextActionAt = now
		replaceClaims = true
	case UpstreamFailed:
		if obs.Failure == nil {
			obs.Failure = &Failure{Class: FailureProtocol, SafeDetail: "上游报告失败但没有错误信息"}
		}
		return m.handleAttemptFailure(ctx, lease, attempt, obs.Failure, settlement)
	case UpstreamCanceled:
		if obs.Failure == nil {
			obs.Failure = &Failure{Class: FailureRejected, SafeDetail: "上游任务已取消"}
		}
		obs.Failure = sanitizeFailureEvidence(task, obs.Failure)
		attempt.State = AttemptCanceled
		attempt.Failure = obs.Failure
		attempt.FinishedAt = &now
		task.State = TaskCanceled
		task.Failure = obs.Failure
		task.NextActionAt = time.Time{}
		task.FinishedAt = &now
	}

	task.UpdatedAt = now
	task.Version++
	// 显式状态机兜底：applyObservation 的所有写入都必须命中转换表；
	// 非法转换（例如终态复活）丢弃本次提交并告警，而不是落库。
	if !validTaskTransition(taskStateBefore, task.State) ||
		(attempt != nil &&
			!validAttemptTransition(attemptStateBefore, attempt.State)) {
		m.logger.Warn(
			"丢弃非法任务状态转换",
			"task_id", task.ID,
			"attempt_id", attempt.ID,
			"task_state", string(taskStateBefore)+"->"+string(task.State),
			"attempt_state", string(attemptStateBefore)+"->"+string(attempt.State),
		)
		return nil
	}
	err := m.repository.Commit(ctx, Commit{
		LeaseToken:      lease.Token,
		ExpectedVersion: lease.Task.Version,
		Task:            task,
		Attempt:         attempt,
		ReplaceClaims:   replaceClaims,
		Claims:          obs.Artifacts,
		Events: []Event{{
			TaskID:    task.ID,
			Type:      eventType,
			Data:      observationEventData(obs),
			CreatedAt: now,
		}},
		DispatchReceipt: settlement,
		ClearLease:      true,
	})
	// A webhook (no lease) leaves the Task to its coordinator lease.
	if errors.Is(err, RepositoryUpstreamJobConflict) && obs.Job != nil && lease.Token != "" {
		*attempt = recorded
		return m.failUpstreamJobConflict(ctx, lease, attempt, *obs.Job, settlement)
	}
	return mapRepositoryError("apply_observation", err)
}

// failUpstreamJobConflict ends a Task whose upstream returned a job ID that
// another Attempt already records (#298). The job cannot be bound to this
// Attempt, so it cannot be observed; the upstream may still have accepted
// and charged the submission. Like an ambiguous submission the Task fails as
// submission_unknown and is never sent again. The other Task keeps its job.
// The Attempt keeps its previously recorded job (none after Start); the
// returned ID is kept in the log and a private event only.
func (m *Module) failUpstreamJobConflict(
	ctx context.Context,
	lease Lease,
	attempt *Attempt,
	job UpstreamJob,
	settlement *DispatchReceiptSettlement,
) error {
	task := lease.Task
	evidence := map[string]string{
		"upstream_provider_id": job.ProviderID,
		"remote_id":            job.RemoteID,
	}
	attributes := []any{
		"task_id", task.ID,
		"attempt_id", string(attempt.ID),
		"provider_id", task.Offering.ProviderID,
		"offering_id", string(task.Offering.ID),
		"upstream_provider_id", job.ProviderID,
		"remote_id", job.RemoteID,
	}
	if owner, err := m.repository.LoadByUpstreamJob(ctx, job.ProviderID, job.RemoteID); err == nil {
		evidence["recorded_task_id"] = string(owner.Task.ID)
		attributes = append(attributes, "recorded_task_id", owner.Task.ID)
	}
	m.logger.Error("上游返回的任务 ID 已记录在另一个任务上，本任务停止且不重发", attributes...)
	failure := &Failure{
		Class:        FailureSubmissionUnknown,
		Ambiguous:    true,
		SafeDetail:   m.upstreamJobConflictMessage(task),
		UpstreamCode: "upstream_job_conflict",
	}
	attempt.State = AttemptSubmissionUnknown
	// As for a re-driven SUBMITTING Attempt: a lease-renewal cancel must not
	// drop this terminal write; the Commit CAS still guards ownership.
	return m.finishFailed(
		context.WithoutCancel(ctx), lease, attempt, failure, "attempt.submission_unknown", settlement,
		Event{TaskID: task.ID, Type: "attempt.upstream_job_conflict", Data: eventData(evidence), CreatedAt: m.clock.Now()},
	)
}

func (m *Module) upstreamJobConflictMessage(task Task) string {
	if message := task.Offering.ExecutionMessages.UpstreamJobConflict; message != "" {
		return message
	}
	if current, ok := m.catalog.Get(task.Offering.ID); ok && current.ExecutionMessages.UpstreamJobConflict != "" {
		return current.ExecutionMessages.UpstreamJobConflict
	}
	return m.executionMessages(task).SubmissionUnknown
}

// A private checkpoint update cannot advance task/attempt lifecycle, progress,
// job, source ordering, resources or dispatch. Commit retains the ordinary
// version and current lease-token/expiry checks; no public event is emitted.
func (m *Module) applyPrivateCheckpointUpdate(ctx context.Context, lease Lease, attempt *Attempt, obs Observation) error {
	task := lease.Task
	now := m.clock.Now()
	task.NextActionAt = now.Add(observationDelay(task.Offering, obs))
	task.UpdatedAt = now // Repository lease-expiry checks use this write time.
	task.Version++
	updated := *attempt
	updated.Opaque = append(json.RawMessage(nil), obs.Opaque...)
	updated.LastObservationKey = obs.DedupeKey
	updated.UpdatedAt = now
	return mapRepositoryError("update_private_checkpoint", m.repository.Commit(ctx, Commit{
		LeaseToken:      lease.Token,
		ExpectedVersion: lease.Task.Version,
		Task:            task,
		Attempt:         &updated,
		ClearLease:      true,
	}))
}

// Events are durable and may feed future projections. Persist only the public
// progress summary; Attempt.Opaque and claims retain private provider evidence.
func observationEventData(obs Observation) json.RawMessage {
	return eventData(struct {
		Phase    UpstreamPhase `json:"phase"`
		Progress int           `json:"progress"`
	}{Phase: obs.Phase, Progress: obs.Progress})
}

// rejectDispatchReceipt 保守丢弃携带不可接受回执的 observation：仅重排
// 观察并写事件（不含 sequence/ack），不改变任务/尝试生命周期，不重发。
func (m *Module) rejectDispatchReceipt(
	ctx context.Context,
	lease Lease,
	attempt *Attempt,
	observation Observation,
) error {
	task := lease.Task
	now := m.clock.Now()
	// B17：陈旧/不匹配回执只重排观察时间，不写事件、不增 version——
	// 该路径会被陈旧轮询反复触发，事件与版本抖动只会污染审计流。
	task.NextActionAt = now.Add(observationDelay(task.Offering, observation))
	task.UpdatedAt = now
	attempt.UpdatedAt = now
	return mapRepositoryError(
		"reject_dispatch_receipt",
		m.repository.Commit(ctx, Commit{
			LeaseToken:      lease.Token,
			ExpectedVersion: lease.Task.Version,
			Task:            task,
			Attempt:         attempt,
			ClearLease:      true,
		}),
	)
}

func (m *Module) rescheduleDuplicateObservation(
	ctx context.Context,
	lease Lease,
	attempt *Attempt,
	observation Observation,
	settlement *DispatchReceiptSettlement,
) error {
	task := lease.Task
	now := m.clock.Now()
	task.NextActionAt = now.Add(observationDelay(task.Offering, observation))
	task.UpdatedAt = now
	task.Version++
	attempt.UpdatedAt = now
	return mapRepositoryError(
		"deduplicate_observation",
		m.repository.Commit(ctx, Commit{
			LeaseToken:      lease.Token,
			ExpectedVersion: lease.Task.Version,
			Task:            task,
			Attempt:         attempt,
			Events: []Event{{
				TaskID: task.ID,
				Type:   "provider.observation_deduplicated",
				Data: eventData(map[string]string{
					"dedupe_key": observation.DedupeKey,
				}),
				CreatedAt: now,
			}},
			// 去重/陈旧丢弃不得吞掉已验证回执（S2）：结算与重排同一事务。
			DispatchReceipt: settlement,
			ClearLease:      true,
		}),
	)
}

func (m *Module) handleAttemptFailure(
	ctx context.Context,
	lease Lease,
	attempt *Attempt,
	failure *Failure,
	settlement *DispatchReceiptSettlement,
) error {
	if failure == nil {
		failure = &Failure{Class: FailureInternal, SafeDetail: "未知上游错误"}
	}
	// Upstream-controlled evidence is sanitized once, before it is stored on
	// the Attempt, written to an event or logged (A4).
	failure = sanitizeFailureEvidence(lease.Task, failure)
	if failure.Ambiguous || failure.Class == FailureSubmissionUnknown {
		normalized := *failure
		normalized.Class = FailureSubmissionUnknown
		normalized.Retryable = false
		normalized.Ambiguous = true
		if message := m.executionMessages(lease.Task).SubmissionUnknown; message != "" {
			normalized.SafeDetail = message
		}
		// SafeDetail 会被替换为面向用户的通用文案，但上游取证字段
		// （状态码/错误码/响应摘录）必须原样保留，落库供诊断使用。
		normalized.UpstreamStatus = failure.UpstreamStatus
		normalized.UpstreamCode = failure.UpstreamCode
		normalized.UpstreamExcerpt = failure.UpstreamExcerpt
		attempt.State = AttemptSubmissionUnknown
		return m.finishFailed(
			ctx,
			lease,
			attempt,
			&normalized,
			"attempt.submission_unknown",
			settlement,
		)
	}

	now := m.clock.Now()
	attempt.State = AttemptFailed
	attempt.Failure = failure
	attempt.UpdatedAt = now
	attempt.FinishedAt = &now

	task := lease.Task
	nextOrdinal := attempt.Ordinal + 1
	if failure.Retryable && task.Offering.Retry.Allows(failure.Class, nextOrdinal) {
		task.State = TaskQueued
		task.Failure = nil
		task.Progress = 0
		task.NextActionAt = now.Add(m.taskRetryDelay(task.Offering.Retry, attempt.Ordinal, failure.RetryAfter))
		task.UpdatedAt = now
		task.Version++
		return mapRepositoryError("schedule_retry", m.repository.Commit(ctx, Commit{
			LeaseToken:      lease.Token,
			ExpectedVersion: lease.Task.Version,
			Task:            task,
			Attempt:         attempt,
			Events: []Event{{
				TaskID:    task.ID,
				Type:      "attempt.retry_scheduled",
				Data:      eventData(failure),
				CreatedAt: now,
			}},
			DispatchReceipt: settlement,
			ClearLease:      true,
		}))
	}
	return m.finishFailed(ctx, lease, attempt, failure, "attempt.failed", settlement)
}

func (m *Module) rescheduleObservation(
	ctx context.Context,
	lease Lease,
	attempt *Attempt,
	failure *Failure,
) error {
	task := lease.Task
	now := m.clock.Now()
	failure = sanitizeFailureEvidence(task, failure)
	attempt.ObservationFailures++
	m.logger.Warn(
		"generation observation deferred",
		"task_id", task.ID,
		"attempt_id", string(attempt.ID),
		"provider_id", task.Offering.ProviderID,
		"offering_id", string(task.Offering.ID),
		"observation_failures", attempt.ObservationFailures,
		"class", string(failure.Class),
		"retryable", failure.Retryable,
		"upstream_status", failure.UpstreamStatus,
		"upstream_code", failure.UpstreamCode,
		"upstream_excerpt", failure.UpstreamExcerpt,
		"detail", failure.SafeDetail,
	)
	task.NextActionAt = now.Add(m.taskRetryDelay(RetryPolicy{InitialBackoff: observationDelay(task.Offering, Observation{}), MaxBackoff: task.Offering.Retry.MaxBackoff}, attempt.ObservationFailures, failure.RetryAfter))
	task.UpdatedAt = now
	task.Version++
	attempt.UpdatedAt = now
	return mapRepositoryError("reschedule_observation", m.repository.Commit(ctx, Commit{
		LeaseToken:      lease.Token,
		ExpectedVersion: lease.Task.Version,
		Task:            task,
		Attempt:         attempt,
		Events: []Event{{
			TaskID:    task.ID,
			Type:      "provider.observe_deferred",
			Data:      eventData(failure),
			CreatedAt: now,
		}},
		ClearLease: true,
	}))
}

func (m *Module) materialize(ctx context.Context, lease Lease) error {
	task := lease.Task
	attempt := lease.CurrentAttempt
	if attempt == nil || attempt.State != AttemptUpstreamSucceeded {
		return m.finishFailed(ctx, lease, attempt, &Failure{
			Class:      FailureInternal,
			SafeDetail: "资产落盘阶段缺少已完成的上游尝试",
		}, "materialization.attempt_missing", nil)
	}
	if err := validateClaims(task.Offering.Outputs, lease.Claims); err != nil {
		return m.handleMaterializationFailure(ctx, lease, attempt, &Failure{
			Class:      FailureIntegrity,
			Retryable:  true,
			SafeDetail: err.Error(),
		}, nil)
	}

	port, ok := m.providers.Provider(task.Offering)
	if !ok {
		return m.finishFailed(ctx, lease, attempt, &Failure{
			Class:      FailureInternal,
			SafeDetail: fmt.Sprintf("适配器 %q 未注册", task.Offering.AdapterKey),
		}, "materialization.adapter_missing", nil)
	}
	// A refetch Task downloads through its source's Offering and job (#295).
	call, err := m.providerCallFor(ctx, task, attempt)
	if err != nil {
		if errors.Is(err, errRefetchSourceChanged) || errors.Is(err, RepositoryNotFound) {
			return m.finishFailed(ctx, lease, attempt, refetchFailure(m.executionMessages(task).RefetchSourceUnavailable), "refetch.source_unavailable", nil)
		}
		return err
	}

	written := make([]Asset, 0, len(lease.Claims))
	retainedIdentity := map[AssetID]ArtifactClaim{}
	events := make([]Event, 0)
	roleOrdinals := map[string]int{}
	var contractedFailure *Failure
	for _, claim := range lease.Claims {
		requirement, contracted := requirementForClaim(
			task.Offering.Outputs,
			claim.Role,
			claim.Variant,
		)
		if !contracted {
			requirement = AssetRequirement{Role: claim.Role}
		}
		// 仅 required（含旧语义）缺错才进入 contractedFailure；
		// background/on-demand 产物失败只记事件，不阻断成功（§3.2）。
		blocking := contracted && requirementBlocksSuccess(requirement)
		if requirement.RetainDependencyIdentity {
			canonical, err := CanonicalDependencyBinding(claim.Metadata)
			if err != nil {
				contractedFailure = preferFailure(contractedFailure, &Failure{Class: FailureIntegrity, SafeDetail: "产物依赖身份缺失或无效"})
				continue
			}
			claim.Metadata = canonical
		}
		// A required slot with minimum zero may be absent. Every claim actually
		// supplied for any required slot must still be persisted before success.
		if !mediaTypeAllowed(requirement.MediaTypes, claim.MediaType) {
			events = append(events, artifactSkipped(task.ID, claim, "media_type", m.clock.Now()))
			if blocking {
				contractedFailure = preferFailure(contractedFailure, &Failure{
					Class:     FailureIntegrity,
					Retryable: false,
					SafeDetail: fmt.Sprintf(
						"产物 %q 的媒体类型 %q 不符合供给项约束",
						claim.Role,
						claim.MediaType,
					),
				})
			}
			continue
		}

		if claim.StoredAsset != nil {
			asset, err := m.checkStoredClaim(ctx, task, attempt, claim, requirement)
			if err != nil {
				events = append(events, artifactSkipped(task.ID, claim, "stored_asset", m.clock.Now()))
				if blocking {
					failure := &Failure{Class: FailureIntegrity, Retryable: true, SafeDetail: m.executionMessages(task).StoredAssetInvalid}
					if isFidelityRejection(err) {
						failure = fidelityFailure(err, claim)
					}
					contractedFailure = preferFailure(contractedFailure, failure)
				}
				continue
			}
			roleOrdinals[claim.Role]++
			asset.Ordinal = roleOrdinals[claim.Role]
			asset.Required = false
			written = append(written, asset)
			if requirement.RetainDependencyIdentity {
				retainedIdentity[asset.ID] = claim
			}
			continue
		}
		callCtx, cancel := m.withOfferingTimeout(ctx, task.Offering.Timeout)
		finishCache := m.cache.Begin(cachemanagement.Operation{
			Filename:   cacheFilename(claim),
			TaskID:     string(task.ID),
			TaskType:   string(task.Capability),
			ProviderID: task.Offering.ProviderID,
			AssetKind:  claim.Role,
		})
		body, metadata, err := port.OpenAsset(callCtx, OpenAssetRequest{
			TaskID:    call.TaskID,
			AttemptID: call.AttemptID,
			Offering:  call.Offering,
			Job:       call.Job,
			Claim:     claim,
		})
		if err != nil {
			finishCache()
			cancel()
			events = append(events, artifactSkipped(task.ID, claim, "download", m.clock.Now()))
			m.logger.Warn("asset download rejected",
				"event", "materialization.asset_download_failed",
				"task_id", string(task.ID),
				"attempt_id", string(attempt.ID),
				"offering_id", string(task.Offering.ID),
				"role", safeClaimLabel(claim.Role),
				"format", safeClaimLabel(claim.Variant),
				"blocking", blocking,
				"class", string(providerFailure(err, false).Class))
			if blocking {
				failure := providerFailure(err, false)
				failure.Retryable = true
				contractedFailure = preferFailure(contractedFailure, failure)
			}
			continue
		}

		mediaType := claim.MediaType
		responseMediaType := strings.TrimSpace(
			strings.Split(metadata.MediaType, ";")[0],
		)
		// CDN 对任意文件返回泛型 octet-stream 时不携带类型信息，
		// 保留 Adapter 按协议声明的 claim 类型；具体响应类型（如错误页
		// text/html）仍以上游为准并参与下方契约校验。
		if responseMediaType != "" &&
			(mediaType == "" || !genericMediaType(responseMediaType)) {
			mediaType = responseMediaType
		}
		if !mediaTypeAllowed(requirement.MediaTypes, mediaType) {
			_ = body.Close()
			finishCache()
			cancel()
			events = append(events, artifactSkipped(task.ID, claim, "response_media_type", m.clock.Now()))
			if blocking {
				contractedFailure = preferFailure(contractedFailure, &Failure{
					Class:     FailureIntegrity,
					Retryable: false,
					SafeDetail: fmt.Sprintf(
						"产物 %q 的实际媒体类型 %q 不符合供给项约束",
						claim.Role,
						mediaType,
					),
				})
			}
			continue
		}
		write := AssetWrite{
			AssetID:         m.ids.AssetID(),
			OwnerID:         task.OwnerID,
			TaskID:          task.ID,
			AttemptID:       attempt.ID,
			Role:            claim.Role,
			Variant:         claim.Variant,
			Ordinal:         roleOrdinals[claim.Role] + 1,
			Required:        false,
			MediaType:       mediaType,
			MaxBytes:        requirement.MaxBytes,
			DeliveryProfile: requirement.DeliveryProfile,
			Body:            body,
		}
		asset, putErr := m.assets.Put(ctx, write)
		if putErr == nil {
			putErr = checkWrittenAsset(write, asset)
		}
		closeErr := body.Close()
		finishCache()
		cancel()
		if putErr == nil && closeErr != nil {
			putErr = closeErr
		}
		if putErr != nil {
			diagnostic := storageDiagnosticOf(putErr)
			m.logAssetStoreRejection(task, attempt, claim, mediaType, blocking, diagnostic)
			skipped := artifactSkipped(task.ID, claim, "store", m.clock.Now())
			skipped.Data = eventData(map[string]string{
				"claim_id":       claim.ID,
				"role":           claim.Role,
				"variant":        claim.Variant,
				"reason":         "store",
				"storage_stage":  diagnostic.Stage,
				"storage_reason": diagnostic.Reason,
				"errno":          diagnostic.Errno,
			})
			events = append(events, skipped)
			if blocking {
				contractedFailure = preferFailure(contractedFailure, &Failure{
					Class:      FailureIntegrity,
					Retryable:  true,
					SafeDetail: "资产写入或完整性校验失败",
					// 内部取证字段只落库与日志，公开投影不含：指出哪个产物、
					// 哪个阶段与原因，不含字节、地址或原始错误文本。
					UpstreamCode:    "asset_store_rejected",
					UpstreamExcerpt: assetStoreForensics(claim, diagnostic),
				})
			}
			continue
		}
		if err := m.checkAssetFidelity(ctx, asset, requirement); err != nil {
			diagnostic := storageDiagnosticOf(err)
			m.logAssetStoreRejection(task, attempt, claim, mediaType, blocking, diagnostic)
			skipped := artifactSkipped(task.ID, claim, "fidelity", m.clock.Now())
			skipped.Data = eventData(map[string]string{"claim_id": claim.ID, "role": claim.Role, "variant": claim.Variant, "reason": "fidelity", "storage_reason": diagnostic.Reason})
			events = append(events, skipped)
			m.deleteAssets(ctx, []Asset{asset})
			if blocking {
				failure := fidelityFailure(err, claim)
				if !isFidelityRejection(err) {
					// The checker could not read a stored file (I/O, cancellation).
					// That is not evidence against the paid upstream result, so
					// it keeps the retryable materialization path.
					failure = &Failure{Class: FailureIntegrity, Retryable: true, SafeDetail: "资产写入或完整性校验失败",
						UpstreamCode: "asset_store_rejected", UpstreamExcerpt: assetStoreForensics(claim, diagnostic)}
				}
				contractedFailure = preferFailure(contractedFailure, failure)
			}
			continue
		}
		roleOrdinals[claim.Role]++
		written = append(written, asset)
		if requirement.RetainDependencyIdentity {
			retainedIdentity[asset.ID] = claim
		}
	}
	if err := assignRequiredOutputs(task.Offering.Outputs, written); err != nil {
		m.deleteAssets(ctx, written)
		if contractedFailure == nil {
			contractedFailure = &Failure{
				Class:      FailureIntegrity,
				Retryable:  true,
				SafeDetail: err.Error(),
			}
		}
		return m.handleMaterializationFailure(ctx, lease, attempt, contractedFailure, events)
	}
	if contractedFailure != nil {
		m.deleteAssets(ctx, written)
		return m.handleMaterializationFailure(ctx, lease, attempt, contractedFailure, events)
	}

	retainedClaims := []ArtifactClaim{}
	for _, asset := range written {
		if claim, ok := retainedIdentity[asset.ID]; ok {
			stored := asset
			retainedClaims = append(retainedClaims, ArtifactClaim{ID: claim.ID, Role: claim.Role, Variant: claim.Variant, MediaType: claim.MediaType, Metadata: append(json.RawMessage(nil), claim.Metadata...), StoredAsset: &stored})
		}
	}
	now := m.clock.Now()
	task.State = TaskSucceeded
	task.Progress = 100
	task.Failure = nil
	task.NextActionAt = time.Time{}
	task.UpdatedAt = now
	task.FinishedAt = &now
	task.Version++
	persistResources := false
	// §6.2 触点 1：声明 observe 时初始化 0022 资源态（required→ready、
	// 非必需→pending/ready），存在 pending 则安排下一轮成功后观察
	//（NextActionAt 不清零）；未声明 observe 维持现状（零行为变化）。
	if task.Offering.PostSuccess != nil && task.Offering.PostSuccess.Observe {
		// 以本次实际落盘的资产判定非必需槽位是否已就绪（task.Assets
		// 在本路径为空——资产经 NewAssets 列写入，不在任务行内）。
		task.Assets = written
		task.Resources = initialResourceStates(task, costSelectionItems)
		persistResources = task.Resources != nil
		if len(pendingResourceKeys(task.Resources)) > 0 {
			task.NextActionAt = now.Add(postSuccessInterval(task))
		}
	}
	events = append(events, Event{
		TaskID:    task.ID,
		Type:      "task.succeeded",
		Data:      eventData(map[string]any{"asset_count": len(written)}),
		CreatedAt: now,
	})
	// #330 third follow-ups: when this successful conversion is the exact
	// delivered relation of a failed conversion, complete that Task in this
	// same transaction. The delivery Task itself never waits for the local
	// replay of already-written bytes.
	var completions []ValidatedDeliveryCompletion
	completion, kind, completionErr := m.prepareDeliveryCompletionForMaterialization(ctx, task, attempt, written)
	if completionErr != nil {
		switch {
		case errors.Is(completionErr, ErrDeliveryCompletionNotApplicable):
			// No exact relation: ordinary delivery commits.
		case errors.Is(completionErr, ErrDeliveryCompletionRejected):
			m.logger.Warn("本地交付完成被拒绝，交付任务仍按自身结果成功",
				"task_id", task.ID, "attempt_id", attempt.ID, "relation", kind, "error", completionErr)
		default:
			// A temporary read/DB/I/O failure must delay this success commit so
			// the same transaction can retry; otherwise the local completion
			// would be lost with no queue behind it.
			return completionErr
		}
	} else if completion != nil {
		completions = append(completions, *completion)
	}
	err = m.repository.Commit(ctx, Commit{
		LeaseToken:                   lease.Token,
		ExpectedVersion:              lease.Task.Version,
		Task:                         task,
		Attempt:                      attempt,
		ReplaceClaims:                true,
		Claims:                       retainedClaims,
		NewAssets:                    written,
		Events:                       events,
		ClearLease:                   true,
		PersistResources:             persistResources,
		ValidatedDeliveryCompletions: completions,
	})
	if err != nil {
		return mapRepositoryError("finish_materialization", err)
	}
	return nil
}

func cacheFilename(claim ArtifactClaim) string {
	if parsed, err := url.Parse(claim.Locator); err == nil {
		if filename := path.Base(parsed.Path); filename != "." && filename != "/" && filename != "" {
			return filename
		}
	}
	if claim.Variant != "" {
		return claim.Variant
	}
	return claim.Role
}

func artifactSkipped(
	taskID TaskID,
	claim ArtifactClaim,
	reason string,
	now time.Time,
) Event {
	return Event{
		TaskID: taskID,
		Type:   "materialization.artifact_skipped",
		Data: eventData(map[string]string{
			"claim_id": claim.ID,
			"role":     claim.Role,
			"variant":  claim.Variant,
			"reason":   reason,
		}),
		CreatedAt: now,
	}
}

func preferFailure(current *Failure, candidate *Failure) *Failure {
	if current == nil || (current.Retryable && !candidate.Retryable) {
		return candidate
	}
	return current
}

// assignRequiredOutputs 按 §3.2 规则 3 做确定性分配：资产按
// (Ordinal, ID) 升序，需求按声明顺序；每个需求从尚未分配的匹配资产
// 中取前 minimum 个标记 Required；一个资产至多满足一个需求。
func assignRequiredOutputs(requirements []AssetRequirement, assets []Asset) error {
	order := make([]int, len(assets))
	for index := range order {
		order[index] = index
	}
	sort.SliceStable(order, func(i, j int) bool {
		left, right := assets[order[i]], assets[order[j]]
		if left.Ordinal != right.Ordinal {
			return left.Ordinal < right.Ordinal
		}
		return left.ID < right.ID
	})
	assigned := make([]bool, len(assets))
	for _, requirement := range requirements {
		if !requirementBlocksSuccess(requirement) {
			continue
		}
		minimum := requirementEffectiveMinimum(requirement)
		matched := 0
		for _, index := range order {
			if assigned[index] || assets[index].Role != requirement.Role {
				continue
			}
			if !requirementMatchesVariant(requirement, assets[index].Variant) {
				continue
			}
			if matched < minimum {
				assets[index].Required = true
				assigned[index] = true
			}
			matched++
		}
		if matched < minimum {
			if requirement.Variant == "" && requirement.VariantPrefix == "" {
				// 旧语义错误信息保持逐字节等价（§3.4）。
				return fmt.Errorf(
					"持久资产不完整：%q 至少需要 %d 个，实际为 %d 个",
					requirement.Role,
					minimum,
					matched,
				)
			}
			return fmt.Errorf(
				"持久资产不完整：%s 至少需要 %d 个，实际为 %d 个",
				requirementSlotLabel(requirement),
				minimum,
				matched,
			)
		}
	}
	return nil
}

func (m *Module) handleMaterializationFailure(
	ctx context.Context,
	lease Lease,
	attempt *Attempt,
	failure *Failure,
	events []Event,
) error {
	task := lease.Task
	now := m.clock.Now()
	task.MaterializeFailures++
	maxAttempts := task.Offering.MaterializeAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	if failure.Retryable && task.MaterializeFailures < maxAttempts {
		refreshedClaims, refreshEvent, refreshed := m.tryRefreshClaims(
			ctx,
			task,
			attempt,
		)
		task.State = TaskMaterializing
		task.Failure = failure
		task.NextActionAt = now.Add(m.jitter(exponentialDelay(2*time.Second, time.Minute, task.MaterializeFailures)))
		task.UpdatedAt = now
		task.Version++
		events = append(events, Event{
			TaskID:    task.ID,
			Type:      "materialization.retry_scheduled",
			Data:      eventData(failure),
			CreatedAt: now,
		})
		if refreshEvent != nil {
			events = append(events, *refreshEvent)
		}
		return mapRepositoryError("retry_materialization", m.repository.Commit(ctx, Commit{
			LeaseToken:      lease.Token,
			ExpectedVersion: lease.Task.Version,
			Task:            task,
			Attempt:         attempt,
			ReplaceClaims:   refreshed,
			Claims:          refreshedClaims,
			Events:          events,
			ClearLease:      true,
		}))
	}
	// Keep the final pass count without changing the version used by the lease CAS.
	lease.Task.MaterializeFailures = task.MaterializeFailures
	return m.finishFailed(ctx, lease, attempt, failure, "materialization.failed", nil, events...)
}

func (m *Module) tryRefreshClaims(
	ctx context.Context,
	task Task,
	attempt *Attempt,
) ([]ArtifactClaim, *Event, bool) {
	now := m.clock.Now()
	if attempt == nil {
		return nil, nil, false
	}
	// A refetch Attempt holds no job of its own: refresh through its source's.
	call, err := m.providerCallFor(ctx, task, attempt)
	if err != nil || call.Job == nil {
		return nil, nil, false
	}
	port, available := m.providers.Provider(task.Offering)
	if !available {
		return nil, nil, false
	}
	callCtx, cancel := m.withOfferingTimeout(ctx, task.Offering.Timeout)
	defer cancel()
	// 物化刷新路径没有可持久化意图的租约栅栏（STD1）：显式注入拒绝型
	// 检查点，禁止 Provider 借 nil-checkpointer 兼容分支发起付费派发。
	callCtx = WithDeniedDispatchCheckpointer(callCtx)
	observation, err := port.Observe(callCtx, ObserveRequest{
		TaskID:    call.TaskID,
		AttemptID: call.AttemptID,
		Offering:  call.Offering,
		Input:     schema.WireInput(call.Offering, call.Input),
		Job:       *call.Job,
		Opaque:    call.Opaque,
	})
	if err != nil {
		failure := providerFailure(err, false)
		return nil, &Event{
			TaskID:    task.ID,
			Type:      "materialization.claim_refresh_failed",
			Data:      eventData(failure),
			CreatedAt: now,
		}, false
	}
	if validateObservation(observation) != nil ||
		observation.Phase != UpstreamCompleted ||
		validateClaims(task.Offering.Outputs, observation.Artifacts) != nil {
		return nil, &Event{
			TaskID: task.ID,
			Type:   "materialization.claim_refresh_failed",
			Data: eventData(map[string]string{
				"class":  string(FailureProtocol),
				"detail": "Provider 未返回完整的 completed observation",
			}),
			CreatedAt: now,
		}, false
	}
	// A refresh reads the job the Attempt already records. A different job in
	// its answer is not adopted: it could belong to another Attempt (#298).
	if observation.Job != nil && attempt.UpstreamJob != nil && *observation.Job != *attempt.UpstreamJob {
		return nil, &Event{
			TaskID: task.ID,
			Type:   "materialization.claim_refresh_failed",
			Data: eventData(map[string]string{
				"class":  string(FailureProtocol),
				"detail": "Provider 刷新返回了不同的上游作业",
			}),
			CreatedAt: now,
		}, false
	}
	// A refetch Attempt never takes over its source's job or checkpoint.
	if attempt.UpstreamJob != nil {
		attempt.Opaque = observation.Opaque
		if observation.Job != nil {
			attempt.UpstreamJob = observation.Job
		}
	}
	attempt.UpdatedAt = now
	return observation.Artifacts, &Event{
		TaskID: task.ID,
		Type:   "materialization.claims_refreshed",
		Data: eventData(map[string]any{
			"claim_count": len(observation.Artifacts),
			"dedupe_key":  observation.DedupeKey,
		}),
		CreatedAt: now,
	}, true
}

func (m *Module) finishFailed(
	ctx context.Context,
	lease Lease,
	attempt *Attempt,
	failure *Failure,
	eventType string,
	settlement *DispatchReceiptSettlement,
	additionalEvents ...Event,
) error {
	task := lease.Task
	failure = sanitizeFailureEvidence(task, failure)
	attributes := []any{
		"task_id", task.ID,
		"provider_id", task.Offering.ProviderID,
		"offering_id", string(task.Offering.ID),
		"event", eventType,
		"class", string(failure.Class),
		"retryable", failure.Retryable,
		"ambiguous", failure.Ambiguous,
		"upstream_status", failure.UpstreamStatus,
		"upstream_code", failure.UpstreamCode,
		"upstream_excerpt", failure.UpstreamExcerpt,
		"detail", failure.SafeDetail,
	}
	if attempt != nil {
		attributes = append(attributes, "attempt_id", string(attempt.ID))
	}
	m.logger.Warn("generation attempt finished failed", attributes...)
	now := m.clock.Now()
	task.State = TaskFailed
	task.Failure = failure
	task.NextActionAt = time.Time{}
	task.UpdatedAt = now
	task.FinishedAt = &now
	task.Version++
	if attempt != nil {
		attempt.Failure = failure
		attempt.UpdatedAt = now
		if attempt.State != AttemptSubmissionUnknown && attempt.State != AttemptCanceled {
			attempt.State = AttemptFailed
		}
		if attempt.FinishedAt == nil {
			attempt.FinishedAt = &now
		}
	}
	return mapRepositoryError("finish_failed", m.repository.Commit(ctx, Commit{
		LeaseToken:      lease.Token,
		ExpectedVersion: lease.Task.Version,
		Task:            task,
		Attempt:         attempt,
		Events: append(additionalEvents, Event{
			TaskID:    task.ID,
			Type:      eventType,
			Data:      eventData(failure),
			CreatedAt: now,
		}),
		// 失败终态同样结算已验证回执（F1）：finishFailed 接受 settlement
		// 却不落库会让已发生的付费派发永远停在 dispatching 栅栏。
		DispatchReceipt: settlement,
		ClearLease:      true,
	}))
}

func (m *Module) releaseLease(ctx context.Context, lease Lease, events []Event) error {
	task := lease.Task
	task.Version++
	task.UpdatedAt = m.clock.Now()
	return mapRepositoryError("release_lease", m.repository.Commit(ctx, Commit{
		LeaseToken:      lease.Token,
		ExpectedVersion: lease.Task.Version,
		Task:            task,
		Attempt:         lease.CurrentAttempt,
		Events:          events,
		ClearLease:      true,
	}))
}

func (m *Module) newAttempt(task Task, ordinal int) *Attempt {
	now := m.clock.Now()
	id := m.ids.AttemptID()
	return &Attempt{
		ID:            id,
		TaskID:        task.ID,
		Ordinal:       ordinal,
		Offering:      task.Offering,
		State:         AttemptSubmitting,
		SubmissionKey: fmt.Sprintf("%s:%d:%s", task.ID, ordinal, id),
		StartedAt:     now,
		UpdatedAt:     now,
	}
}

func providerFailure(err error, submitting bool) *Failure {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) && providerErr.Failure != nil {
		copy := *providerErr.Failure
		if copy.SafeDetail == "" {
			copy.SafeDetail = "上游调用失败"
		}
		return &copy
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Failure{
			Class:      FailureTimedOut,
			Retryable:  !submitting,
			Ambiguous:  submitting,
			SafeDetail: "上游请求超时",
		}
	}
	return &Failure{
		Class:      FailureTransport,
		Retryable:  !submitting,
		Ambiguous:  submitting,
		SafeDetail: "无法确认上游请求结果",
	}
}

func retryDelay(policy RetryPolicy, ordinal int, requested time.Duration) time.Duration {
	initial := policy.InitialBackoff
	if initial <= 0 {
		initial = time.Second
	}
	maximum := policy.MaxBackoff
	if maximum <= 0 {
		maximum = time.Minute
	}
	return min(maximum, max(requested, exponentialDelay(initial, maximum, ordinal)))
}

func exponentialDelay(initial, maximum time.Duration, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if initial >= maximum {
		return maximum
	}
	factor := math.Pow(2, float64(attempt-1))
	delay := float64(initial) * factor
	if delay >= float64(maximum) {
		return maximum
	}
	return time.Duration(delay)
}

func observationDelay(offering Offering, obs Observation) time.Duration {
	if obs.PollAfter > 0 {
		return obs.PollAfter
	}
	if offering.PollInterval > 0 {
		return offering.PollInterval
	}
	return 5 * time.Second
}

// withOfferingTimeout 约束单次上游请求；兜底时长来自 Dependencies
// 的 DefaultRequestTimeout（Schema runtime.configuration 声明，B17
// 禁止在内核硬编码运行参数）。
func (m *Module) withOfferingTimeout(
	ctx context.Context,
	timeout time.Duration,
) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = m.defaultRequestTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

// jobTimeout 作业级总时长：优先 Schema 声明的 job_timeout，其次
// request 超时，均未声明时回退 DefaultRequestTimeout。
func (m *Module) jobTimeout(task Task) time.Duration {
	if task.Offering.JobTimeout > 0 {
		return task.Offering.JobTimeout
	}
	if task.Offering.Timeout > 0 {
		return task.Offering.Timeout
	}
	return m.defaultRequestTimeout
}

func clampProgress(value int) int {
	if value < 0 {
		return 0
	}
	if value > 98 {
		return 98
	}
	return value
}

// requirementMatchesVariant 实现 §3.2 匹配规则：显式 variant 精确匹配；
// variant_prefix 前缀匹配；两者皆空（旧语义）匹配同 role 任意变体。
func requirementMatchesVariant(requirement AssetRequirement, variant string) bool {
	if requirement.Variant != "" {
		return variant == requirement.Variant
	}
	if requirement.VariantPrefix != "" {
		return strings.HasPrefix(variant, requirement.VariantPrefix)
	}
	return true
}

// requirementForClaim 按 (role, variant) 解析声明，优先级：精确 variant
// > 最长前缀 > 旧语义 role 通配。未命中任何声明时返回 false（不约束）。
func requirementForClaim(
	requirements []AssetRequirement,
	role string,
	variant string,
) (AssetRequirement, bool) {
	best := AssetRequirement{}
	found := false
	bestPrefixLen := -1
	for _, requirement := range requirements {
		if requirement.Role != role {
			continue
		}
		if requirement.Variant != "" {
			if variant == requirement.Variant {
				return requirement, true
			}
			continue
		}
		if requirement.VariantPrefix != "" {
			if strings.HasPrefix(variant, requirement.VariantPrefix) &&
				len(requirement.VariantPrefix) > bestPrefixLen {
				best = requirement
				found = true
				bestPrefixLen = len(requirement.VariantPrefix)
			}
			continue
		}
		if bestPrefixLen < 0 {
			best = requirement
			found = true
			bestPrefixLen = 0
		}
	}
	return best, found
}

// AssetDelivery 由冻结 Offering 快照声明推导某持久资产的交付类别；
// 未命中声明或旧语义返回空串。
func AssetDelivery(
	outputs []AssetRequirement,
	role string,
	variant string,
) string {
	requirement, ok := requirementForClaim(outputs, role, variant)
	if !ok {
		return ""
	}
	return requirement.Delivery
}

// requirementBlocksSuccess 判定声明是否进入成功门槛：旧语义与
// required 阻断；background/on-demand 缺失不阻断（§3.2 规则 4）。
func requirementBlocksSuccess(requirement AssetRequirement) bool {
	switch requirement.Delivery {
	case AssetDeliveryBackground, AssetDeliveryOnDemand:
		return false
	default:
		return true
	}
}

// requirementEffectiveMinimum 保持旧强制转换语义；显式 required 且
// minimum=0 则表示仅对实际返回的 claim 强制落盘。
func requirementEffectiveMinimum(requirement AssetRequirement) int {
	if requirement.Delivery == AssetDeliveryRequired &&
		requirement.Minimum != nil && *requirement.Minimum == 0 {
		return 0
	}
	if requirement.Minimum != nil && *requirement.Minimum > 0 {
		return *requirement.Minimum
	}
	return 1
}

// requirementSlotLabel 生成指向 role/variant/group 的诊断标签。
func requirementSlotLabel(requirement AssetRequirement) string {
	label := fmt.Sprintf("%q", requirement.Role)
	switch {
	case requirement.Variant != "":
		label += fmt.Sprintf(" variant %q", requirement.Variant)
	case requirement.VariantPrefix != "":
		label += fmt.Sprintf(" variant_prefix %q", requirement.VariantPrefix)
	}
	if requirement.Group != "" {
		label += fmt.Sprintf("（group %q）", requirement.Group)
	}
	return label
}

func validateClaims(requirements []AssetRequirement, claims []ArtifactClaim) error {
	for _, claim := range claims {
		if claim.Role == "" {
			return errors.New("上游产物缺少 role")
		}
		if claim.Locator == "" && len(claim.Inline) == 0 && claim.StoredAsset == nil {
			return fmt.Errorf("上游产物 %q 缺少可读取位置", claim.Role)
		}
	}
	// 计数按声明槽位一对一分配：每个产物至多满足一个需求（§3.2 规则 3）。
	assigned := make([]bool, len(claims))
	for _, requirement := range requirements {
		if !requirementBlocksSuccess(requirement) {
			continue
		}
		minimum := requirementEffectiveMinimum(requirement)
		matched := 0
		for index := range claims {
			if assigned[index] || claims[index].Role != requirement.Role {
				continue
			}
			if !requirementMatchesVariant(requirement, claims[index].Variant) {
				continue
			}
			if matched < minimum {
				assigned[index] = true
			}
			matched++
		}
		if matched < minimum {
			if requirement.Variant == "" && requirement.VariantPrefix == "" {
				// 旧语义错误信息保持逐字节等价（§3.4）。
				return fmt.Errorf(
					"上游产物不完整：%q 至少需要 %d 个，实际为 %d 个",
					requirement.Role,
					minimum,
					matched,
				)
			}
			return fmt.Errorf(
				"上游产物不完整：%s 至少需要 %d 个，实际为 %d 个",
				requirementSlotLabel(requirement),
				minimum,
				matched,
			)
		}
	}
	return nil
}

func mediaTypeAllowed(allowed []string, actual string) bool {
	if len(allowed) == 0 || actual == "" {
		return true
	}
	for _, candidate := range allowed {
		if candidate == actual {
			return true
		}
		if strings.HasSuffix(candidate, "/*") &&
			strings.HasPrefix(actual, strings.TrimSuffix(candidate, "*")) {
			return true
		}
	}
	return false
}

// genericMediaType 判断上游响应类型是否为不携带任何格式信息的占位
// 类型（部分 CDN 对所有文件统一返回，如 Tripo 的 binary/octet-stream）。
func genericMediaType(mediaType string) bool {
	switch strings.ToLower(mediaType) {
	case "application/octet-stream", "binary/octet-stream", "application/binary":
		return true
	}
	return false
}

func (m *Module) deleteAssets(ctx context.Context, assets []Asset) {
	for _, asset := range assets {
		_ = m.assets.Delete(ctx, asset)
	}
}
