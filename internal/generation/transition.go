package generation

// taskTransitions 与 attemptTransitions 是公开 Task/Attempt 的显式状态机。
// 终态只允许等值自返（幂等重放），不允许任何"复活"路径：迟到的
// Webhook/观测、并发取消都不得把终态或已失败 Attempt 改回进行中。
// applyObservation 在提交前用这里的表做兜底校验，非法转换直接丢弃。
var taskTransitions = map[TaskState][]TaskState{
	TaskQueued: {
		TaskRunning,
		// 同步 Provider 的 Start 直接返回 Completed：QUEUED 一步进入
		// MATERIALIZING，不经过 RUNNING。
		TaskMaterializing,
		TaskFailed,
		TaskCanceled,
	},
	TaskRunning: {
		TaskRunning,
		TaskMaterializing,
		TaskQueued,
		TaskFailed,
		TaskCanceled,
	},
	TaskMaterializing: {
		TaskMaterializing,
		TaskSucceeded,
		TaskFailed,
		TaskCanceled,
	},
	TaskSucceeded: {TaskSucceeded},
	TaskFailed:    {TaskFailed},
	TaskCanceled:  {TaskCanceled},
}

var attemptTransitions = map[AttemptState][]AttemptState{
	AttemptQueued: {
		AttemptSubmitting,
		AttemptCanceled,
	},
	AttemptSubmitting: {
		AttemptRunning,
		AttemptUpstreamSucceeded,
		AttemptFailed,
		AttemptSubmissionUnknown,
		AttemptCanceled,
	},
	AttemptRunning: {
		AttemptRunning,
		AttemptUpstreamSucceeded,
		AttemptFailed,
		AttemptSubmissionUnknown,
		AttemptCanceled,
	},
	AttemptUpstreamSucceeded: {AttemptUpstreamSucceeded},
	AttemptFailed:            {AttemptFailed},
	AttemptSubmissionUnknown: {AttemptSubmissionUnknown},
	AttemptCanceled:          {AttemptCanceled},
	AttemptDeliveryCompleted: {AttemptDeliveryCompleted},
}

func validTaskTransition(from, to TaskState) bool {
	for _, allowed := range taskTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

func validAttemptTransition(from, to AttemptState) bool {
	for _, allowed := range attemptTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// Terminal 报告该 Attempt 状态是否终态：终态 Attempt 的生命周期已结束，
// 取消与观测都不得改写它（历史 Attempt 保留其失败事实）。
func (s AttemptState) Terminal() bool {
	return s == AttemptUpstreamSucceeded ||
		s == AttemptFailed ||
		s == AttemptSubmissionUnknown ||
		s == AttemptCanceled ||
		s == AttemptDeliveryCompleted
}
