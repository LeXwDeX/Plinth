package generation

import "testing"

// 已失败 Attempt 的迟到回执（含同 remote ID 的 Webhook）必须判陈旧：
// 它会把失败 Attempt/任务改回进行中（复活路径），吞掉计划中的重试。
func TestStaleObservationRejectsTerminalAttemptObservations(t *testing.T) {
	task := Task{State: TaskRunning}
	for _, state := range []AttemptState{
		AttemptFailed,
		AttemptUpstreamSucceeded,
		AttemptCanceled,
		AttemptSubmissionUnknown,
	} {
		attempt := &Attempt{State: state}
		obs := Observation{Phase: UpstreamRunning, Progress: 10}
		if !staleObservation(task, attempt, obs) {
			t.Fatalf("attempt %s: observation must be stale", state)
		}
	}
}

// 状态机表的关键性质：终态只允许等值自返；公开 Task 禁止任何
// 从终态回进行中的转换。
func TestTransitionTablesRejectResurrection(t *testing.T) {
	for _, terminal := range []TaskState{TaskSucceeded, TaskFailed, TaskCanceled} {
		for _, next := range []TaskState{
			TaskQueued, TaskRunning, TaskMaterializing,
		} {
			if validTaskTransition(terminal, next) {
				t.Fatalf("task %s -> %s must be illegal", terminal, next)
			}
		}
		if !validTaskTransition(terminal, terminal) {
			t.Fatalf("task %s self-transition must stay legal", terminal)
		}
	}
	for _, terminal := range []AttemptState{
		AttemptUpstreamSucceeded, AttemptFailed,
		AttemptSubmissionUnknown, AttemptCanceled,
	} {
		for _, next := range []AttemptState{
			AttemptQueued, AttemptSubmitting, AttemptRunning,
		} {
			if validAttemptTransition(terminal, next) {
				t.Fatalf("attempt %s -> %s must be illegal", terminal, next)
			}
		}
		if !validAttemptTransition(terminal, terminal) {
			t.Fatalf("attempt %s self-transition must stay legal", terminal)
		}
	}
	if !validTaskTransition(TaskQueued, TaskMaterializing) {
		t.Fatal("sync providers go QUEUED directly to MATERIALIZING")
	}
	if !validTaskTransition(TaskRunning, TaskQueued) {
		t.Fatal("retryable failures return RUNNING to QUEUED")
	}
}
