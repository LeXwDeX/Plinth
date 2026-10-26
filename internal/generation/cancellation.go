package generation

import (
	"context"
	"errors"
	"time"
)

func (m *Module) Cancel(ctx context.Context, owner OwnerID, id TaskID) (Task, error) {
	aggregate, err := m.repository.Load(ctx, id)
	if err != nil {
		return Task{}, mapRepositoryError("cancel", err)
	}
	if aggregate.Task.OwnerID != owner {
		return Task{}, &Error{Code: ErrNotFound, Operation: "cancel"}
	}
	task := aggregate.Task
	if task.State.Terminal() {
		return m.Get(ctx, owner, id)
	}
	policy := task.Offering.Cancellation
	if policy.Unknown == "" {
		policy = m.defaultCancellation
	}
	// Historical snapshots predate the cancellation protocol. Current catalog is
	// authoritative for whether an upstream cancellation API can safely be called.
	if current, ok := m.catalog.Get(task.Offering.ID); ok {
		policy = current.Cancellation
	}
	var request *DeleteProviderRequest
	detail := policy.LocalOnly
	if aggregate.CurrentAttempt != nil {
		detail = policy.Unknown
		if aggregate.CurrentAttempt.UpstreamJob != nil {
			detail = policy.Unsupported
			if policy.Supported {
				detail = policy.Requesting
				request = &DeleteProviderRequest{TaskID: id, AttemptID: aggregate.CurrentAttempt.ID, Offering: task.Offering, Job: *aggregate.CurrentAttempt.UpstreamJob}
			}
		}
	}
	now := m.clock.Now()
	task.State = TaskCanceled
	task.Failure = &Failure{Class: FailureRejected, SafeDetail: detail}
	task.UpdatedAt = now
	task.FinishedAt = &now
	task.NextActionAt = time.Time{}
	task.Version++
	// 终态 Attempt（含已失败/结果不明）是历史事实：重试中的任务取消
	// 只取消任务与在飞 Attempt，不改写终态 Attempt。
	if aggregate.CurrentAttempt != nil &&
		!aggregate.CurrentAttempt.State.Terminal() {
		aggregate.CurrentAttempt.State = AttemptCanceled
		aggregate.CurrentAttempt.UpdatedAt = now
		aggregate.CurrentAttempt.FinishedAt = &now
	}
	// Winning this CAS reserves the one cancellation call durably before any
	// external effect. Repeated requests (including after restart) never repeat it.
	err = m.repository.Commit(ctx, Commit{ExpectedVersion: aggregate.Task.Version, Task: task, Attempt: aggregate.CurrentAttempt, ClearLease: true, Events: []Event{{TaskID: id, Type: "task.canceled", Data: eventData(map[string]string{"reason": "user_requested", "upstream": detail}), CreatedAt: now}}})
	if err != nil {
		if errors.Is(err, RepositoryConflict) {
			current, getErr := m.Get(ctx, owner, id)
			if getErr == nil && current.State.Terminal() {
				return current, nil
			}
		}
		return Task{}, mapRepositoryError("cancel", err)
	}
	if request == nil {
		return m.Get(ctx, owner, id)
	}
	detail = policy.Failed
	if provider, ok := m.providers.Provider(task.Offering); ok {
		if canceler, ok := provider.(ProviderTaskCanceler); ok {
			callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(policy.TimeoutSeconds)*time.Second)
			callErr := canceler.CancelProviderTask(callCtx, *request)
			cancel()
			if callErr == nil {
				detail = policy.Confirmed
			}
		} else {
			detail = policy.Unsupported
		}
	}
	task.Failure = &Failure{Class: FailureRejected, SafeDetail: detail}
	task.Version++
	task.UpdatedAt = m.clock.Now()
	resultCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(policy.TimeoutSeconds)*time.Second)
	defer cancel()
	err = m.repository.Commit(resultCtx, Commit{ExpectedVersion: task.Version - 1, Task: task, Events: []Event{{TaskID: id, Type: "task.upstream_cancel_result", Data: eventData(map[string]string{"detail": detail}), CreatedAt: task.UpdatedAt}}})
	if err != nil {
		return Task{}, mapRepositoryError("record_cancel_result", err)
	}
	return m.Get(resultCtx, owner, id)
}
