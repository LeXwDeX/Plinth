package generation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// post_success.go 落实 §6 核心契约 D 的成功后观察生命周期（W6）：
// 独立入口 observePostSuccess（不复用 observeAttempt）、终态永不回退、
// 可选资源幂等合并、deadline 本地 finalizer、Provider 状态断言的通用
// 校验。父任务 SUCCEEDED 后绝不新建 Attempt、绝不 port.Start（§6.3）。

// observePostSuccess 是成功后观察的独立入口（§6.2 触点 2）：deadline
// 优先本地收尾（零上游调用，保证 finalizer 可达）；否则调用 Provider
// Observe（PostSuccess 标记 + 冻结快照 + 冻结输入 + 检查点栅栏）。
func (m *Module) observePostSuccess(ctx context.Context, lease Lease) error {
	task := lease.Task
	now := m.clock.Now()
	deadline := postSuccessDeadline(task)
	// 本地 finalizer 先于任何上游调用：到期后零 Observe（§6.4）；
	// 无 pending 的陈旧行也在此清调度（有界 sweeper，不留死行）。
	if !now.Before(deadline) || !ResourcesPending(task.Resources) {
		return m.finalizePostSuccess(ctx, lease, !now.Before(deadline))
	}
	attempt := lease.CurrentAttempt
	if attempt == nil || attempt.UpstreamJob == nil {
		// 无可观察上游作业（内联/离线路径）：只能等 deadline 本地收尾。
		// 必须推后 NextActionAt：只释放租约会让任务一直到期，协调器每拍
		// 领取并重写一次，直到 deadline（#510）。
		return m.deferIdlePostSuccess(ctx, lease, deadline, "no_upstream_job")
	}
	port, ok := m.providers.Provider(task.Offering)
	if !ok {
		// 适配器未注册（连接被 Catalog 发布移除、Provider 关闭）：同样无法
		// 观察，按退避推后，适配器恢复后照常观察（#510）。
		return m.deferIdlePostSuccess(ctx, lease, deadline, "adapter_unregistered")
	}
	callCtx, cancel := m.withOfferingTimeout(ctx, task.Offering.Timeout)
	defer cancel()
	// 成功后观察期允许 Provider 推进已同意的私有转换（§4 检查点栅栏
	// 仍然生效：一次 ack 至多一次付费 HTTP）。
	checkpoints := m.dispatchCheckpointerFor(&lease, attempt)
	if checkpoints != nil {
		callCtx = WithDispatchCheckpointer(callCtx, checkpoints)
	}
	obs, err := port.Observe(callCtx, ObserveRequest{
		TaskID:      task.ID,
		AttemptID:   attempt.ID,
		Offering:    task.Offering,
		Job:         *attempt.UpstreamJob,
		Opaque:      attempt.Opaque,
		Input:       schema.WireInput(task.Offering, task.Input),
		PostSuccess: true,
		// 只读槽位身份/状态（P1）：Provider 据此判断哪些期望项仍未兑现
		// （例如上游已完成但本地下载失败），只对未决槽位继续私有工作。
		ResourceSlots: resourceSlotStates(task.Resources, task.Offering.Outputs),
	})
	if err != nil {
		// 成功后观察失败绝不回退/失败任务：有界重排直到 deadline。
		return m.reschedulePostSuccess(ctx, lease, providerFailure(err, false))
	}
	if err := validateObservation(obs); err != nil {
		return m.reschedulePostSuccess(ctx, lease, &Failure{
			Class:      FailureProtocol,
			Retryable:  true,
			SafeDetail: "成功后观察返回了不可识别的状态",
		})
	}
	return m.applyPostSuccessObservation(ctx, lease, attempt, obs, checkpoints)
}

// finalizePostSuccess 是幂等 deadline finalizer（§6.4）：pending →
// incomplete 只写一次（incomplete 为终态，结构性幂等），清 NextActionAt
// 并写 post_success.incomplete 事件；纯本地事务，零上游调用。
func (m *Module) finalizePostSuccess(
	ctx context.Context,
	lease Lease,
	expired bool,
) error {
	task := lease.Task
	resources := cloneResourceStates(task.Resources)
	pending := pendingResourceKeys(resources)
	if len(pending) == 0 {
		// 无 pending：只清残留调度（有界 sweeper）。
		if task.NextActionAt.IsZero() {
			return m.releaseLease(ctx, lease, nil)
		}
		task.NextActionAt = time.Time{}
		task.UpdatedAt = m.clock.Now()
		task.Version++
		return mapRepositoryError("finalize_post_success", m.repository.Commit(ctx, Commit{
			LeaseToken:      lease.Token,
			ExpectedVersion: lease.Task.Version,
			Task:            task,
			Attempt:         lease.CurrentAttempt,
			Events: []Event{{
				TaskID:    task.ID,
				Type:      "post_success.schedule_cleared",
				Data:      eventData(map[string]string{"reason": "no_pending_resources"}),
				CreatedAt: m.clock.Now(),
			}},
			ClearLease:       true,
			PersistResources: true,
		}))
	}
	if !expired {
		// 未到期但无可观察路径：保留 pending 等 deadline（本地收尾）。
		return m.releaseLease(ctx, lease, nil)
	}
	now := m.clock.Now()
	finalizeExpiredResources(
		resources,
		task.Offering.Outputs,
		task.Assets,
		ResourceReasonMessageKey,
	)
	task.Resources = resources
	task.NextActionAt = time.Time{}
	task.UpdatedAt = now
	task.Version++
	return mapRepositoryError("finalize_post_success", m.repository.Commit(ctx, Commit{
		LeaseToken:      lease.Token,
		ExpectedVersion: lease.Task.Version,
		Task:            task,
		Attempt:         lease.CurrentAttempt,
		Events: []Event{{
			TaskID:    task.ID,
			Type:      "post_success.incomplete",
			Data:      eventData(map[string]any{"slots": len(pending)}),
			CreatedAt: now,
		}},
		ClearLease:       true,
		PersistResources: true,
	}))
}

// reschedulePostSuccess 有界重排成功后观察（§6.2 触点 3 的丢弃路径）：
// 不改状态、不失败任务，按 interval 重排直到 deadline。
func (m *Module) reschedulePostSuccess(
	ctx context.Context,
	lease Lease,
	failure *Failure,
) error {
	task := lease.Task
	now := m.clock.Now()
	task.NextActionAt = now.Add(PostSuccessInterval(task))
	task.UpdatedAt = now
	task.Version++
	m.logger.Warn(
		"generation post-success observation deferred",
		"task_id", task.ID,
		"offering_id", string(task.Offering.ID),
		"class", string(failure.Class),
		"detail", failure.SafeDetail,
	)
	return mapRepositoryError("reschedule_post_success", m.repository.Commit(ctx, Commit{
		LeaseToken:      lease.Token,
		ExpectedVersion: lease.Task.Version,
		Task:            task,
		Attempt:         lease.CurrentAttempt,
		Events: []Event{{
			TaskID:    task.ID,
			Type:      "post_success.deferred",
			Data:      eventData(failure),
			CreatedAt: now,
		}},
		ClearLease: true,
	}))
}

// deferIdlePostSuccess 处理无法观察的成功后任务（无上游作业、适配器未
// 注册）：不调上游、不写事件，只把 NextActionAt 推后并释放租约。推后量
// 随空转时长退避——取声明的观察间隔与「成功以来已过时长」中较大者，
// 每次约翻倍——且不越过 deadline，保证本地 finalizer 按时收尾（#510）。
func (m *Module) deferIdlePostSuccess(
	ctx context.Context,
	lease Lease,
	deadline time.Time,
	reason string,
) error {
	task := lease.Task
	now := m.clock.Now()
	next := idlePostSuccessNextAction(task, now, deadline)
	task.NextActionAt = next
	task.UpdatedAt = now
	task.Version++
	m.logger.Debug(
		"generation post-success observation idle",
		"task_id", task.ID,
		"offering_id", string(task.Offering.ID),
		"reason", reason,
		"next_action_at", next,
	)
	return mapRepositoryError("defer_idle_post_success", m.repository.Commit(ctx, Commit{
		LeaseToken:      lease.Token,
		ExpectedVersion: lease.Task.Version,
		Task:            task,
		Attempt:         lease.CurrentAttempt,
		ClearLease:      true,
	}))
}

// idlePostSuccessNextAction 是空转路径的下一次检查时刻：至少一个声明的
// 观察间隔，随成功以来的时长增长（约每次翻倍），封顶于 deadline。
func idlePostSuccessNextAction(task Task, now, deadline time.Time) time.Time {
	base := task.CreatedAt
	if task.FinishedAt != nil {
		base = *task.FinishedAt
	}
	delay := max(PostSuccessInterval(task), now.Sub(base))
	next := now.Add(delay)
	if next.After(deadline) {
		return deadline
	}
	return next
}

// applyPostSuccessObservation 应用一次成功后观察（§6.2 触点 3/§6.3）：
// 回执校验先于丢弃判定；只接受 COMPLETED + 新可选产物；资源断言经
// 通用校验应用；绝不改 Task.State/Attempt.State、绝不新建 Attempt、
// 绝不 port.Start；合并提交携带 NewAssets + 资源态 + 事件，单事务 CAS。
func (m *Module) applyPostSuccessObservation(
	ctx context.Context,
	lease Lease,
	attempt *Attempt,
	obs Observation,
	checkpoints *dispatchCheckpointer,
) error {
	task := lease.Task
	now := m.clock.Now()
	deadline := postSuccessDeadline(task)

	// 回执校验先于一切丢弃（S2 纪律）：不可验证的回执整体保守丢弃。
	var settlement *DispatchReceiptSettlement
	if obs.Dispatch != nil {
		if checkpoints == nil || !checkpoints.receiptValid(obs.Dispatch) {
			if lease.Token == "" {
				return nil
			}
			return m.rejectPostSuccessReceipt(ctx, lease, attempt, obs)
		}
		settlement = &DispatchReceiptSettlement{
			AttemptID:     attempt.ID,
			Sequence:      obs.Dispatch.Sequence,
			AckToken:      obs.Dispatch.AckToken,
			NotDispatched: obs.Dispatch.Result == DispatchResultNotDispatched,
		}
		checkpoints.markSettled()
	}

	resources := cloneResourceStates(task.Resources)
	assets := append([]Asset(nil), task.Assets...)
	events := make([]Event, 0)

	// Provider 状态断言（§6.5）：通用合法性校验后应用；非法断言忽略。
	applied := applyResourceUpdates(resources, task.Offering.Outputs, obs.ResourceUpdates, assets)
	events = append(events, resourceUpdateEvents(task.ID, applied, now)...)

	if len(obs.Opaque) > 0 {
		attempt.Opaque = obs.Opaque
	}
	attempt.UpdatedAt = now

	var newAssets []Asset
	if obs.Phase == UpstreamCompleted {
		var mergeEvents []Event
		var merged []Asset
		var rejected []ResourceUpdate
		merged, mergeEvents, assets, rejected = m.mergeOptionalArtifacts(ctx, task, attempt, obs.Artifacts, assets)
		newAssets = merged
		events = append(events, mergeEvents...)
		markMergedResources(resources, task.Offering, task.Offering.Outputs, assets)
		// 同一字节再下载也会被拒（#576）：槽位落 failed，不空等 deadline。
		events = append(events, resourceUpdateEvents(task.ID, applyResourceUpdates(resources, task.Offering.Outputs, rejected, assets), now)...)
	} else {
		// 非 COMPLETED（私有子作业运行中/失败/取消/进度回退）＝陈旧：
		// 丢弃 + 事件，资源保持 pending，父任务终态永不回退（§6.2 触点 3；
		// 子作业仍在运行按"父已完成、资源待定"处理，不回滚父进度）。
		events = append(events, Event{
			TaskID: task.ID,
			Type:   "post_success.observation_dropped",
			Data: eventData(map[string]string{
				"phase": string(obs.Phase),
			}),
			CreatedAt: now,
		})
	}

	// 调度推进：仍有 pending 且未到期 → 下一轮；到期 → 本地收尾
	//（已有资产的槽位按事实转 ready，其余 incomplete）；无 pending →
	// 清调度（观察生命周期结束）。
	pending := pendingResourceKeys(resources)
	task.Resources = resources
	task.UpdatedAt = now
	task.Version++
	clearLease := true
	switch {
	case len(pending) > 0 && now.Before(deadline):
		task.NextActionAt = now.Add(pollAfterOrInterval(task, obs))
	case len(pending) > 0:
		finalizeExpiredResources(resources, task.Offering.Outputs, assets, ResourceReasonMessageKey)
		task.NextActionAt = time.Time{}
		events = append(events, Event{
			TaskID:    task.ID,
			Type:      "post_success.incomplete",
			Data:      eventData(map[string]any{"slots": len(pending)}),
			CreatedAt: now,
		})
	default:
		task.NextActionAt = time.Time{}
	}
	return mapRepositoryError("apply_post_success", m.repository.Commit(ctx, Commit{
		LeaseToken:       lease.Token,
		ExpectedVersion:  lease.Task.Version,
		Task:             task,
		Attempt:          attempt,
		NewAssets:        newAssets,
		Events:           events,
		DispatchReceipt:  settlement,
		PersistResources: true,
		ClearLease:       clearLease,
	}))
}

// rejectPostSuccessReceipt 保守丢弃携带不可接受回执的成功后观察：仅
// 静默重排（B17：不写事件、不增 version），不改资源态、不重发（§4.3 纪律）。
func (m *Module) rejectPostSuccessReceipt(
	ctx context.Context,
	lease Lease,
	attempt *Attempt,
	observation Observation,
) error {
	task := lease.Task
	now := m.clock.Now()
	task.NextActionAt = now.Add(pollAfterOrInterval(task, observation))
	task.UpdatedAt = now
	attempt.UpdatedAt = now
	return mapRepositoryError(
		"reject_post_success_receipt",
		m.repository.Commit(ctx, Commit{
			LeaseToken:      lease.Token,
			ExpectedVersion: lease.Task.Version,
			Task:            task,
			Attempt:         attempt,
			ClearLease:      true,
		}),
	)
}

// mergeOptionalArtifacts 幂等合并迟到可选产物（§6.3）：只接受非阻断
// （background/on-demand）声明的 claims；逻辑身份 (role, variant) 去重；
// ordinal 取该 role 既有资产之外的下一个空闲值；失败只写事件、绝不失败
// 任务：可能下次成功的失败（下载、I/O）资源保持 pending（deadline 收尾），
// 确定性拒收（内容检查、保真检查、媒体类型不符，#576）另返回 failed
// 断言。返回新增资产、事件、合并后的资产全集与拒收断言。
func (m *Module) mergeOptionalArtifacts(
	ctx context.Context,
	task Task,
	attempt *Attempt,
	claims []ArtifactClaim,
	existing []Asset,
) ([]Asset, []Event, []Asset, []ResourceUpdate) {
	merged := append([]Asset(nil), existing...)
	newAssets := make([]Asset, 0)
	events := make([]Event, 0)
	rejected := make([]ResourceUpdate, 0)
	ordinals := map[string]int{}
	for _, asset := range merged {
		if asset.Ordinal > ordinals[asset.Role] {
			ordinals[asset.Role] = asset.Ordinal
		}
	}
	existingByIdentity := map[string]struct{}{}
	for _, asset := range merged {
		existingByIdentity[asset.Role+"|"+asset.Variant] = struct{}{}
	}
	now := m.clock.Now()
	for _, claim := range claims {
		requirement, contracted := requirementForClaim(task.Offering.Outputs, claim.Role, claim.Variant)
		if !contracted || requirementBlocksSuccess(requirement) {
			// 只合并非必需槽位；必需/未声明产物不属于成功后观察。
			continue
		}
		identity := claim.Role + "|" + claim.Variant
		if _, duplicate := existingByIdentity[identity]; duplicate {
			continue // 幂等：同 (role, variant) 已落盘，跳过
		}
		asset, err := m.storeOptionalClaim(ctx, task, attempt, claim, requirement, ordinals)
		if err != nil {
			events = append(events, Event{
				TaskID: task.ID,
				Type:   "post_success.asset_failed",
				Data: eventData(map[string]string{
					"role":    claim.Role,
					"variant": claim.Variant,
					"detail":  err.Error(),
				}),
				CreatedAt: now,
			})
			if deterministicAssetRejection(err) {
				rejected = append(rejected, rejectedResourceUpdate(claim))
			}
			continue
		}
		existingByIdentity[identity] = struct{}{}
		newAssets = append(newAssets, asset)
		merged = append(merged, asset)
		events = append(events, Event{
			TaskID: task.ID,
			Type:   "post_success.asset",
			Data: eventData(map[string]string{
				"role":    asset.Role,
				"variant": asset.Variant,
			}),
			CreatedAt: now,
		})
	}
	return newAssets, events, merged, rejected
}

// storeOptionalClaim 下载并落盘单个可选产物（复用 materialize 的校验
// 纪律：媒体类型约束 + 大小上限 + CAS 摘要）。
func (m *Module) storeOptionalClaim(
	ctx context.Context,
	task Task,
	attempt *Attempt,
	claim ArtifactClaim,
	requirement AssetRequirement,
	ordinals map[string]int,
) (Asset, error) {
	if claim.StoredAsset != nil {
		asset, err := m.checkStoredClaim(ctx, task, attempt, claim, requirement)
		if err != nil {
			return Asset{}, err
		}
		ordinals[claim.Role]++
		asset.Ordinal = ordinals[claim.Role]
		asset.Required = false
		return asset, nil
	}
	if claim.Locator == "" && len(claim.Inline) == 0 {
		return Asset{}, fmt.Errorf("可选产物 %q 缺少可读取位置", claim.Variant)
	}
	if !mediaTypeAllowed(requirement.MediaTypes, claim.MediaType) {
		return Asset{}, fmt.Errorf(
			"可选产物 %q 的媒体类型 %q 不符合供给项约束: %w",
			claim.Variant,
			claim.MediaType,
			errOptionalMediaType,
		)
	}
	if requirement.MaxBytes > 0 && len(claim.Inline) > 0 && int64(len(claim.Inline)) > requirement.MaxBytes {
		return Asset{}, fmt.Errorf("可选产物 %q 超过供给项大小上限", claim.Variant)
	}
	if len(claim.Inline) > 0 {
		ordinals[claim.Role]++
		write := AssetWrite{
			AssetID:         m.ids.AssetID(),
			OwnerID:         task.OwnerID,
			TaskID:          task.ID,
			AttemptID:       attempt.ID,
			Role:            claim.Role,
			Variant:         claim.Variant,
			Ordinal:         ordinals[claim.Role],
			MediaType:       claim.MediaType,
			MaxBytes:        requirement.MaxBytes,
			DeliveryProfile: requirement.DeliveryProfile,
			Body:            bytes.NewReader(claim.Inline),
		}
		asset, err := m.assets.Put(ctx, write)
		if err == nil {
			err = checkWrittenAsset(write, asset)
		}
		if err != nil {
			return Asset{}, err
		}
		if err := m.checkAssetFidelity(ctx, asset, requirement); err != nil {
			m.deleteAssets(ctx, []Asset{asset})
			return Asset{}, err
		}
		return asset, nil
	}
	port, ok := m.providers.Provider(task.Offering)
	if !ok {
		return Asset{}, fmt.Errorf("适配器 %q 未注册", task.Offering.AdapterKey)
	}
	callCtx, cancel := m.withOfferingTimeout(ctx, task.Offering.Timeout)
	defer cancel()
	body, _, err := port.OpenAsset(callCtx, OpenAssetRequest{
		TaskID:    task.ID,
		AttemptID: attempt.ID,
		Offering:  task.Offering,
		Job:       attempt.UpstreamJob,
		Claim:     claim,
	})
	if err != nil {
		return Asset{}, err
	}
	ordinals[claim.Role]++
	write := AssetWrite{
		AssetID:         m.ids.AssetID(),
		OwnerID:         task.OwnerID,
		TaskID:          task.ID,
		AttemptID:       attempt.ID,
		Role:            claim.Role,
		Variant:         claim.Variant,
		Ordinal:         ordinals[claim.Role],
		MediaType:       claim.MediaType,
		MaxBytes:        requirement.MaxBytes,
		DeliveryProfile: requirement.DeliveryProfile,
		Body:            body,
	}
	asset, putErr := m.assets.Put(ctx, write)
	if putErr == nil {
		putErr = checkWrittenAsset(write, asset)
	}
	closeErr := body.Close()
	if putErr != nil {
		return Asset{}, putErr
	}
	if closeErr != nil {
		return Asset{}, closeErr
	}
	if err := m.checkAssetFidelity(ctx, asset, requirement); err != nil {
		m.deleteAssets(ctx, []Asset{asset})
		return Asset{}, err
	}
	return asset, nil
}

// pollAfterOrInterval 优先 Provider 声明的轮询提示，否则用声明间隔。
func pollAfterOrInterval(task Task, obs Observation) time.Duration {
	if obs.PollAfter > 0 {
		return obs.PollAfter
	}
	return PostSuccessInterval(task)
}

// cloneResourceStates 深拷贝资源态（避免修改租约内聚合的共享 map）。
func cloneResourceStates(states map[string]ResourceState) map[string]ResourceState {
	cloned := make(map[string]ResourceState, len(states))
	for key, state := range states {
		cloned[key] = state
	}
	return cloned
}

// costSelectionItems 读取冻结输入中某个多选参数的选中值（通用规则执行；
// 金额计算仍归 catalog 共享评估器，这里只解析身份）：非空字符串、去重、
// 保持输入顺序。selection_parameter 未声明/参数缺失/输入不可解码/非数组
// 一律返回 nil（未选择 → 不初始化 on-demand 槽位）。
func costSelectionItems(
	offering Offering,
	input json.RawMessage,
	parameter string,
) []string {
	if parameter == "" {
		return nil
	}
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 {
		return nil
	}
	var values map[string]any
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if err := decoder.Decode(&values); err != nil {
		return nil
	}
	items, ok := values[parameter].([]any)
	if !ok {
		return nil
	}
	selections := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		text, ok := item.(string)
		if !ok || text == "" || seen[text] {
			continue
		}
		seen[text] = true
		selections = append(selections, text)
	}
	return selections
}
