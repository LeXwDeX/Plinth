package generation_test

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// queueModeAdapter accepts through mock at progress 0 and then answers each
// poll from its current mode: queued (the upstream holds the job in its own
// queue) or running at a fixed progress.
type queueModeAdapter struct {
	mu       sync.Mutex
	queued   bool
	progress int
	polls    int
	next     generation.ProviderPort
}

func (a *queueModeAdapter) set(queued bool, progress int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.queued, a.progress = queued, progress
}

func (a *queueModeAdapter) Start(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	observation, err := a.next.Start(ctx, request)
	observation.Progress = 0
	return observation, err
}

func (a *queueModeAdapter) Observe(_ context.Context, request generation.ObserveRequest) (generation.Observation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.polls++
	job := request.Job
	if a.queued {
		ahead := 40
		return generation.Observation{Phase: generation.UpstreamAccepted, Job: &job,
			Queue: &generation.UpstreamQueue{Ahead: &ahead}, DedupeKey: job.RemoteID + ":PENDING:0"}, nil
	}
	return generation.Observation{Phase: generation.UpstreamRunning, Job: &job, Progress: a.progress,
		DedupeKey: job.RemoteID + ":IN_PROGRESS"}, nil
}

func (a *queueModeAdapter) OpenAsset(ctx context.Context, request generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	return a.next.OpenAsset(ctx, request)
}

func (a *queueModeAdapter) pollCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.polls
}

// editedCatalog applies edit to every Offering it hands out, so a test can
// freeze retry and timeout declarations into the Task snapshot.
type editedCatalog struct {
	generation.Catalog
	edit func(*generation.Offering)
}

func (c editedCatalog) Normalize(capability generation.Capability, id generation.OfferingID, input json.RawMessage) (generation.Offering, json.RawMessage, error) {
	offering, normalized, err := c.Catalog.Normalize(capability, id, input)
	c.edit(&offering)
	return offering, normalized, err
}

func (c editedCatalog) Get(id generation.OfferingID) (generation.Offering, bool) {
	offering, ok := c.Catalog.Get(id)
	if ok {
		c.edit(&offering)
	}
	return offering, ok
}

func (c editedCatalog) List(capability generation.Capability) []generation.Offering {
	offerings := c.Catalog.List(capability)
	for index := range offerings {
		c.edit(&offerings[index])
	}
	return offerings
}

const (
	queueTestJobTimeout   = 10 * time.Minute
	queueTestQueueTimeout = 30 * time.Minute
)

// newQueueTimeoutHarness submits a Meshy text-to-3d Task whose frozen Offering
// has a 10 minute job timeout, the given queue wait limit, and a retry policy
// that would resubmit timeouts (so a test proves they are not resubmitted).
func newQueueTimeoutHarness(t *testing.T, adapter *queueModeAdapter, queueLimit time.Duration) (*harness, generation.TaskID) {
	t.Helper()
	h := newConfiguredHarness(t, adapter, nil, func(d *generation.Dependencies) {
		d.Catalog = editedCatalog{Catalog: d.Catalog, edit: func(offering *generation.Offering) {
			offering.Timeout, offering.JobTimeout = queueTestJobTimeout, 0
			offering.UpstreamQueueTimeout = queueLimit
			offering.Retry.MaxAttempts = 3
			offering.Retry.Retryable = append(offering.Retry.Retryable,
				generation.FailureTimedOut, generation.FailureUpstreamQueueTimeout)
		}}
	})
	accepted, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"castle"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, accepted.TaskID
}

// advanceAndReconcile moves the clock by d (reconcile adds its own 31s).
func (h *harness) advanceAndReconcile(t *testing.T, id generation.TaskID, d time.Duration) generation.Task {
	t.Helper()
	h.clock.Advance(d)
	h.reconcile(t)
	task, err := h.module.Get(context.Background(), "owner-a", id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// assertFinalAmbiguousFailure checks the "may have been charged" settlement of
// an accepted job that timed out: one Attempt, an ambiguous, non-retryable
// failure of class, and no resubmission on later passes.
func assertFinalAmbiguousFailure(t *testing.T, h *harness, adapter *queueModeAdapter, id generation.TaskID,
	task generation.Task, class generation.FailureClass) {
	t.Helper()
	if task.State != generation.TaskFailed || task.Failure == nil || task.Failure.Class != class ||
		!task.Failure.Ambiguous || task.Failure.Retryable {
		t.Fatalf("task = state %s failure %#v, want ambiguous final %s", task.State, task.Failure, class)
	}
	polls := adapter.pollCount()
	for range 3 {
		h.advanceAndReconcile(t, id, time.Hour)
	}
	events := h.eventsOf(t, id)
	if countEvents(events, "attempt.started") != 1 || countEvents(events, "attempt.retry_scheduled") != 0 ||
		adapter.pollCount() != polls {
		t.Fatalf("a timed-out accepted job must not be resubmitted: events=%v polls=%d->%d",
			eventTypes(events), polls, adapter.pollCount())
	}
	aggregate, err := h.repository.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	attempt := aggregate.CurrentAttempt
	if attempt == nil || attempt.UpstreamJob == nil || attempt.Failure == nil || !attempt.Failure.Ambiguous {
		t.Fatalf("the attempt keeps its upstream job and the ambiguous failure: %#v", attempt)
	}
}

// #136: while the upstream reports the accepted job queued, that time does
// not count toward the job timeout; only the queue wait limit ends it, as
// upstream_queue_timeout, ambiguous and never resubmitted.
func TestUpstreamQueueTimeDoesNotCountTowardTheJobTimeout(t *testing.T) {
	adapter := &queueModeAdapter{next: mock.New(), queued: true}
	h, id := newQueueTimeoutHarness(t, adapter, queueTestQueueTimeout)
	h.reconcile(t) // start: accepted
	h.reconcile(t) // first poll: queued, the queue clock starts
	queuedSince := h.clock.Now().Add(-31 * time.Second)

	// Well past the 10 minute job timeout, still within the 30 minute queue limit.
	task := h.advanceAndReconcile(t, id, 20*time.Minute)
	if task.State != generation.TaskRunning || task.Failure != nil || task.UpstreamQueue == nil {
		t.Fatalf("queued past the job timeout: state %s failure %#v queue %#v", task.State, task.Failure, task.UpstreamQueue)
	}
	aggregate, err := h.repository.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !aggregate.CurrentAttempt.UpstreamQueuedSince.Equal(queuedSince) {
		t.Fatalf("queue clock started at %v, want %v", aggregate.CurrentAttempt.UpstreamQueuedSince, queuedSince)
	}

	// Past the queue wait limit: fails before polling the upstream again.
	polls := adapter.pollCount()
	task = h.advanceAndReconcile(t, id, 10*time.Minute)
	if adapter.pollCount() != polls {
		t.Fatalf("the limit is checked before polling: polls %d -> %d", polls, adapter.pollCount())
	}
	assertFinalAmbiguousFailure(t, h, adapter, id, task, generation.FailureUpstreamQueueTimeout)
}

// #136: after the job leaves the queue, the job timeout counts only the time
// it was not queued.
func TestJobTimeoutCountsOnlyTimeOutsideTheUpstreamQueue(t *testing.T) {
	adapter := &queueModeAdapter{next: mock.New(), queued: true}
	h, id := newQueueTimeoutHarness(t, adapter, queueTestQueueTimeout)
	h.reconcile(t) // start (t+0): accepted
	h.reconcile(t) // t+31s: queued
	h.advanceAndReconcile(t, id, 20*time.Minute)
	adapter.set(false, 10)
	task := h.advanceAndReconcile(t, id, 0) // the job runs; ~20.5 minutes were queued
	if task.State != generation.TaskRunning || task.UpstreamQueue != nil {
		t.Fatalf("running: state %s queue %#v", task.State, task.UpstreamQueue)
	}
	aggregate, err := h.repository.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	queued := aggregate.CurrentAttempt.UpstreamQueuedFor
	if !aggregate.CurrentAttempt.UpstreamQueuedSince.IsZero() || queued < 20*time.Minute || queued > 22*time.Minute {
		t.Fatalf("queue clock after the queue: since %v for %v", aggregate.CurrentAttempt.UpstreamQueuedSince, queued)
	}
	// About 2 minutes ran outside the queue; 7 more stay within 10 minutes.
	if task = h.advanceAndReconcile(t, id, 7*time.Minute); task.State != generation.TaskRunning {
		t.Fatalf("within the job timeout outside the queue: state %s failure %#v", task.State, task.Failure)
	}
	task = h.advanceAndReconcile(t, id, 2*time.Minute)
	assertFinalAmbiguousFailure(t, h, adapter, id, task, generation.FailureTimedOut)
}

// #136: without upstream queueing the job timeout is unchanged (measured from
// the Attempt's start), and a timed-out accepted job is final and ambiguous
// however the Offering's retry policy reads.
func TestJobTimeoutWithoutUpstreamQueueIsUnchanged(t *testing.T) {
	adapter := &queueModeAdapter{next: mock.New(), progress: 10}
	h, id := newQueueTimeoutHarness(t, adapter, queueTestQueueTimeout)
	h.reconcile(t) // start (t+0)
	h.reconcile(t) // t+31s: running
	if task := h.advanceAndReconcile(t, id, 8*time.Minute); task.State != generation.TaskRunning {
		t.Fatalf("within the job timeout: state %s failure %#v", task.State, task.Failure)
	}
	task := h.advanceAndReconcile(t, id, 90*time.Second)
	assertFinalAmbiguousFailure(t, h, adapter, id, task, generation.FailureTimedOut)
}

// #136: an Offering without a queue wait limit (an older frozen snapshot or
// a Provider that declares none) keeps counting queue time toward the job
// timeout, as before.
func TestUpstreamQueueTimeCountsWithoutAQueueLimit(t *testing.T) {
	adapter := &queueModeAdapter{next: mock.New(), queued: true}
	h, id := newQueueTimeoutHarness(t, adapter, 0)
	h.reconcile(t)
	h.reconcile(t)
	if task := h.advanceAndReconcile(t, id, 8*time.Minute); task.State != generation.TaskRunning || task.UpstreamQueue == nil {
		t.Fatalf("queued within the job timeout: state %s queue %#v", task.State, task.UpstreamQueue)
	}
	task := h.advanceAndReconcile(t, id, 2*time.Minute)
	assertFinalAmbiguousFailure(t, h, adapter, id, task, generation.FailureTimedOut)
}
