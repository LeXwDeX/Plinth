package generation_test

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// queueScriptAdapter accepts through mock (at progress 0, as Meshy does) and
// then answers the scripted observations in order, all naming the accepted
// job.
type queueScriptAdapter struct {
	mu     sync.Mutex
	script []generation.Observation
	polls  int
	next   generation.ProviderPort
}

func (a *queueScriptAdapter) Start(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	observation, err := a.next.Start(ctx, request)
	observation.Progress = 0
	return observation, err
}

func (a *queueScriptAdapter) Observe(ctx context.Context, request generation.ObserveRequest) (generation.Observation, error) {
	a.mu.Lock()
	index := a.polls
	a.polls++
	a.mu.Unlock()
	if index >= len(a.script) {
		return a.next.Observe(ctx, request)
	}
	observation := a.script[index]
	job := request.Job
	observation.Job = &job
	observation.DedupeKey = job.RemoteID + ":" + observation.DedupeKey
	return observation, nil
}

func (a *queueScriptAdapter) OpenAsset(ctx context.Context, request generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	return a.next.OpenAsset(ctx, request)
}

// #131: a poll that cannot advance the Attempt (same accepted phase and
// progress, even with the same dedupe key) still records the upstream queue
// it reports; the Task read carries it while the Task runs, and it is gone
// once the upstream starts the job and when the Task ends.
func TestUpstreamQueueIsRecordedWhileTheJobWaitsAndClearedWhenItRuns(t *testing.T) {
	ahead := func(value int) *int { return &value }
	adapter := &queueScriptAdapter{next: mock.New(), script: []generation.Observation{
		{Phase: generation.UpstreamAccepted, Queue: &generation.UpstreamQueue{Ahead: ahead(1313)}, DedupeKey: "PENDING:0"},
		{Phase: generation.UpstreamAccepted, Queue: &generation.UpstreamQueue{Ahead: ahead(1200)}, DedupeKey: "PENDING:0"},
		{Phase: generation.UpstreamAccepted, Queue: &generation.UpstreamQueue{Ahead: ahead(-1)}, DedupeKey: "PENDING:0"},
		{Phase: generation.UpstreamRunning, Progress: 10, DedupeKey: "IN_PROGRESS:10"},
	}}
	h := newHarness(t, adapter, nil)
	accepted, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"castle"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	read := func() generation.Task {
		t.Helper()
		task, err := h.module.Get(context.Background(), "owner-a", accepted.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		return task
	}

	h.reconcile(t) // Start: accepted, no queue reported yet.
	if task := read(); task.State != generation.TaskRunning || task.UpstreamQueue != nil {
		t.Fatalf("after start: state %s queue %#v", task.State, task.UpstreamQueue)
	}
	for _, want := range []*int{ahead(1313), ahead(1200), nil} {
		h.reconcile(t)
		task := read()
		// The task page reads the summary projection.
		summary, err := h.module.GetSummaryScoped(context.Background(), generation.OwnTasks("owner-a"), accepted.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		for _, read := range []generation.Task{task, summary} {
			if read.State != generation.TaskRunning || read.Progress != 0 || read.UpstreamQueue == nil {
				t.Fatalf("queued poll: state %s progress %d queue %#v", read.State, read.Progress, read.UpstreamQueue)
			}
			if (want == nil) != (read.UpstreamQueue.Ahead == nil) || (want != nil && *read.UpstreamQueue.Ahead != *want) {
				t.Fatalf("queue ahead = %v, want %v", read.UpstreamQueue.Ahead, want)
			}
		}
	}
	h.reconcile(t)
	if task := read(); task.State != generation.TaskRunning || task.Progress != 10 || task.UpstreamQueue != nil {
		t.Fatalf("running: state %s progress %d queue %#v", task.State, task.Progress, task.UpstreamQueue)
	}
	if task := h.runUntilTerminal(t, "owner-a", accepted.TaskID); task.State != generation.TaskSucceeded || task.UpstreamQueue != nil {
		t.Fatalf("terminal: state %s failure %#v queue %#v", task.State, task.Failure, task.UpstreamQueue)
	}
}
