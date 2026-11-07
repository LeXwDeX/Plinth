package chat

import (
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestBackgroundTaskVisible(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	earlier := now.Add(-time.Minute)
	later := now.Add(time.Minute)
	failure := &generation.Failure{Class: generation.FailureRejected}
	finished := func(at time.Time) *time.Time { return &at }
	cases := []struct {
		name      string
		task      generation.Task
		lastReply time.Time
		want      bool
	}{
		{name: "running", task: generation.Task{State: generation.TaskRunning}, lastReply: now, want: true},
		{name: "succeeded", task: generation.Task{State: generation.TaskSucceeded}, lastReply: earlier, want: false},
		{name: "cancelled", task: generation.Task{State: generation.TaskCanceled}, lastReply: earlier, want: false},
		{name: "failed after the reply", task: generation.Task{State: generation.TaskFailed, Failure: failure, FinishedAt: finished(later)}, lastReply: now, want: true},
		{name: "failed before the reply", task: generation.Task{State: generation.TaskFailed, Failure: failure, FinishedAt: finished(earlier)}, lastReply: now, want: false},
		{name: "failed with no finish time", task: generation.Task{State: generation.TaskFailed, Failure: failure}, lastReply: now, want: true},
		{name: "failed with no reply yet", task: generation.Task{State: generation.TaskFailed, Failure: failure, FinishedAt: finished(earlier)}, want: true},
		{name: "failed without a reason", task: generation.Task{State: generation.TaskFailed, FinishedAt: finished(later)}, lastReply: now, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := backgroundTaskVisible(tc.task, tc.lastReply); got != tc.want {
				t.Fatalf("backgroundTaskVisible = %v, want %v", got, tc.want)
			}
		})
	}
}
