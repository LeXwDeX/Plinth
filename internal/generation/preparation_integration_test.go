package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type preparationAdapter struct {
	convertNotDispatchedOnce                     bool
	beforeConvert                                func()
	mu                                           sync.Mutex
	starts, uploads, resumes, converts, observes int
	startRequest                                 generation.StartRequest
	resumeRequest                                generation.PreparationRequest
	sequences                                    []int64
	startError, resumeError                      error
	mutateStart, mutateResume                    func(*generation.Observation)
	afterUpload, afterConvert                    func()
}

func (a *preparationAdapter) Start(ctx context.Context, r generation.StartRequest) (generation.Observation, error) {
	a.mu.Lock()
	a.starts++
	a.startRequest = r
	a.mu.Unlock()
	receipt, err := generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{Bytes: json.RawMessage(`{"stage":"upload_dispatching"}`)})
	if err != nil {
		return generation.Observation{}, err
	}
	a.mu.Lock()
	a.uploads++
	if receipt != nil {
		a.sequences = append(a.sequences, receipt.Sequence)
	}
	a.mu.Unlock()
	if a.afterUpload != nil {
		a.afterUpload()
	}
	if a.startError != nil {
		return generation.Observation{}, a.startError
	}
	obs := generation.Observation{Phase: generation.UpstreamPreparing, Opaque: json.RawMessage(`{"stage":"token_ready","token":"private-upload-token"}`), Dispatch: receipt, PollAfter: time.Millisecond}
	if a.mutateStart != nil {
		a.mutateStart(&obs)
	}
	return obs, nil
}
func (a *preparationAdapter) ResumePreparation(ctx context.Context, r generation.PreparationRequest) (generation.Observation, error) {
	a.mu.Lock()
	a.resumes++
	a.resumeRequest = r
	a.mu.Unlock()
	// A restart may contain a dispatched intent whose response never committed.
	// Asking for a permit is fail-closed; no new POST occurs while it is pending.
	if a.beforeConvert != nil {
		a.beforeConvert()
	}
	receipt, err := generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{Bytes: json.RawMessage(`{"stage":"convert_dispatching"}`)})
	if err != nil {
		return generation.Observation{}, err
	}
	if a.convertNotDispatchedOnce {
		a.convertNotDispatchedOnce = false
		receipt.Result = generation.DispatchResultNotDispatched
		return generation.Observation{Phase: generation.UpstreamFailed, Dispatch: receipt, Failure: &generation.Failure{Class: generation.FailureRateLimited, Retryable: true}}, nil
	}
	a.mu.Lock()
	a.converts++
	if receipt != nil {
		a.sequences = append(a.sequences, receipt.Sequence)
	}
	a.mu.Unlock()
	if a.afterConvert != nil {
		a.afterConvert()
	}
	if a.resumeError != nil {
		return generation.Observation{}, a.resumeError
	}
	obs := generation.Observation{Phase: generation.UpstreamAccepted, Job: &generation.UpstreamJob{ProviderID: r.Offering.ProviderID, RemoteID: "converted-model-job"}, Opaque: json.RawMessage(`{"stage":"converted"}`), Dispatch: receipt, PollAfter: time.Millisecond}
	if a.mutateResume != nil {
		a.mutateResume(&obs)
	}
	return obs, nil
}
func (a *preparationAdapter) Observe(_ context.Context, r generation.ObserveRequest) (generation.Observation, error) {
	a.mu.Lock()
	a.observes++
	a.mu.Unlock()
	return generation.Observation{Phase: generation.UpstreamRunning, Job: &r.Job, Progress: 20, Opaque: r.Opaque, PollAfter: time.Millisecond}, nil
}
func (*preparationAdapter) OpenAsset(context.Context, generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	return nil, generation.AssetMetadata{}, errors.New("not expected")
}
func (a *preparationAdapter) counts() (int, int, int, int, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.starts, a.uploads, a.resumes, a.converts, a.observes
}

func submitPreparation(t *testing.T, h *checkpointHarness) generation.TaskID {
	t.Helper()
	accepted, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{OwnerID: "owner-dispatch", Capability: generation.TextTo3D, OfferingID: "meshy/meshy-7.1/text-to-3d", Input: json.RawMessage(`{"prompt":"private bounded preparation"}`)})
	if err != nil {
		t.Fatal(err)
	}
	return accepted.TaskID
}
func advancePreparation(t *testing.T, h *checkpointHarness) {
	t.Helper()
	h.clock.Advance(time.Minute)
	h.reconcile(t)
}
func assertPreparationUnknown(t *testing.T, h *checkpointHarness, id generation.TaskID) {
	t.Helper()
	task := h.task(t, id)
	aggregate, err := h.repository.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskFailed || task.Failure == nil || task.Failure.Class != generation.FailureSubmissionUnknown || !task.Failure.Ambiguous || aggregate.CurrentAttempt.State != generation.AttemptSubmissionUnknown {
		t.Fatalf("want M2 unknown, task=%+v attempt=%+v", task, aggregate.CurrentAttempt)
	}
}
func assertCheckpointSequence(t *testing.T, h *checkpointHarness, id generation.TaskID, state string, sequence int64) {
	t.Helper()
	aggregate, err := h.repository.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	value := readCheckpointColumn(t, h.dbPath, string(aggregate.CurrentAttempt.ID))
	var checkpoint struct {
		State    string `json:"state"`
		Sequence int64  `json:"sequence"`
	}
	if err = json.Unmarshal([]byte(value), &checkpoint); err != nil {
		t.Fatal(err)
	}
	if checkpoint.State != state || checkpoint.Sequence != sequence {
		t.Fatalf("checkpoint=%s want %s/%d", value, state, sequence)
	}
}

func TestPreparationTwoLeasesAndRestartTokenOnly(t *testing.T) {
	a := &preparationAdapter{}
	h := newCheckpointHarness(t, a, filepath.Join(t.TempDir(), "preparing.db"))
	id := submitPreparation(t, h)
	h.reconcile(t)
	aggregate, err := h.repository.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	attempt := aggregate.CurrentAttempt
	if aggregate.Task.State != generation.TaskRunning || aggregate.Task.Progress != 0 || attempt.State != generation.AttemptRunning || attempt.UpstreamJob != nil || attempt.LastObservationPhase != generation.UpstreamPreparing || !strings.Contains(string(attempt.Opaque), "token_ready") {
		t.Fatalf("preparation not persisted: task=%+v attempt=%+v", aggregate.Task, attempt)
	}
	assertCheckpointSequence(t, h, id, "received", 1)
	if a.startRequest.OwnerID != "owner-dispatch" {
		t.Fatalf("Start owner=%q", a.startRequest.OwnerID)
	}
	h = h.restart(t, a)
	advancePreparation(t, h)
	aggregate, err = h.repository.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.CurrentAttempt.UpstreamJob == nil || aggregate.CurrentAttempt.UpstreamJob.RemoteID != "converted-model-job" || aggregate.CurrentAttempt.LastObservationPhase != generation.UpstreamAccepted {
		t.Fatalf("convert Job not committed: %+v", aggregate.CurrentAttempt)
	}
	assertCheckpointSequence(t, h, id, "received", 2)
	r := a.resumeRequest
	if r.OwnerID != "owner-dispatch" || r.TaskID != id || r.AttemptID != attempt.ID || r.SubmissionKey != attempt.SubmissionKey || !reflect.DeepEqual(r.Offering, aggregate.Task.Offering) || !json.Valid(r.Input) || string(r.Opaque) != string(attempt.Opaque) {
		t.Fatalf("resume did not use frozen persisted scope: %+v", r)
	}
	h = h.restart(t, a)
	advancePreparation(t, h)
	starts, uploads, resumes, converts, observes := a.counts()
	if starts != 1 || uploads != 1 || resumes != 1 || converts != 1 || observes != 1 || !reflect.DeepEqual(a.sequences, []int64{1, 2}) {
		t.Fatalf("calls start/upload/resume/convert/observe=%d/%d/%d/%d/%d seq=%v", starts, uploads, resumes, converts, observes, a.sequences)
	}
	events, err := h.module.Events(context.Background(), "owner-dispatch", id, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if strings.Contains(string(event.Data), "private-upload-token") {
			t.Fatal("opaque token leaked into public event")
		}
	}
}

func TestPreparationMutationErrorsBecomeUnknownWithoutRetry(t *testing.T) {
	for _, stage := range []string{"upload", "convert"} {
		t.Run(stage, func(t *testing.T) {
			a := &preparationAdapter{}
			if stage == "upload" {
				a.startError = context.DeadlineExceeded
			} else {
				a.resumeError = context.DeadlineExceeded
			}
			h := newCheckpointHarness(t, a, filepath.Join(t.TempDir(), "unknown.db"))
			id := submitPreparation(t, h)
			h.reconcile(t)
			seq := int64(1)
			if stage == "convert" {
				advancePreparation(t, h)
				seq = 2
			}
			assertPreparationUnknown(t, h, id)
			assertCheckpointSequence(t, h, id, "dispatching", seq)
			h = h.restart(t, a)
			advancePreparation(t, h)
			starts, uploads, resumes, converts, observes := a.counts()
			if starts != 1 || uploads != 1 || observes != 0 || stage == "convert" && (resumes != 1 || converts != 1) || stage == "upload" && (resumes != 0 || converts != 0) {
				t.Fatalf("ambiguous mutation replayed: %d/%d/%d/%d/%d", starts, uploads, resumes, converts, observes)
			}
		})
	}
}

func TestPreparationConvertCrashAfterPermitRestartsFailClosed(t *testing.T) {
	a := &preparationAdapter{afterConvert: func() { panic("process died after conversion POST") }}
	h := newCheckpointHarness(t, a, filepath.Join(t.TempDir(), "crash.db"))
	id := submitPreparation(t, h)
	h.reconcile(t)
	h.clock.Advance(time.Minute)
	if _, err := h.module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{WorkerID: "crash", LeaseFor: time.Minute, BatchSize: 1}); err == nil {
		t.Fatal("want simulated crash error")
	}
	assertCheckpointSequence(t, h, id, "dispatching", 2)
	a.afterConvert = nil
	h.clock.Advance(61 * time.Second)
	h = h.restart(t, a)
	h.reconcile(t)
	assertPreparationUnknown(t, h, id)
	starts, uploads, _, converts, observes := a.counts()
	if starts != 1 || uploads != 1 || converts != 1 || observes != 0 {
		t.Fatalf("replayed paid request %d/%d/%d/%d", starts, uploads, converts, observes)
	}
}

func TestPreparationRejectsInvalidInitialObservations(t *testing.T) {
	cases := map[string]func(*generation.Observation){"job": func(o *generation.Observation) {
		o.Job = &generation.UpstreamJob{ProviderID: "meshy", RemoteID: "fake"}
	}, "progress": func(o *generation.Observation) { o.Progress = 1 }, "failure": func(o *generation.Observation) { o.Failure = &generation.Failure{Class: generation.FailureRejected} }, "artifact": func(o *generation.Observation) { o.Artifacts = []generation.ArtifactClaim{{ID: "fake"}} }, "resource": func(o *generation.Observation) {
		o.ResourceUpdates = []generation.ResourceUpdate{{Role: "model", State: generation.ResourceReady}}
	}, "empty-opaque": func(o *generation.Observation) { o.Opaque = nil }, "no-permit-receipt": func(o *generation.Observation) { o.Dispatch = nil }, "not-dispatched": func(o *generation.Observation) { o.Dispatch.Result = generation.DispatchResultNotDispatched }}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			a := &preparationAdapter{mutateStart: mutate}
			h := newCheckpointHarness(t, a, filepath.Join(t.TempDir(), "invalid.db"))
			id := submitPreparation(t, h)
			h.reconcile(t)
			task := h.task(t, id)
			if task.State != generation.TaskFailed || task.Failure == nil || task.Failure.Class != generation.FailureProtocol {
				t.Fatalf("invalid preparing admitted: %+v", task)
			}
			advancePreparation(t, h)
			_, uploads, resumes, converts, _ := a.counts()
			if uploads != 1 || resumes != 0 || converts != 0 {
				t.Fatal("invalid preparing resumed")
			}
		})
	}
}

func TestPreparationResumeCannotRemainPreparingOrSkipRealJob(t *testing.T) {
	for name, mutate := range map[string]func(*generation.Observation){"preparing-again": func(o *generation.Observation) { o.Phase = generation.UpstreamPreparing; o.Job = nil }, "nil-job": func(o *generation.Observation) { o.Job = nil }, "running": func(o *generation.Observation) { o.Phase = generation.UpstreamRunning }, "missing-receipt": func(o *generation.Observation) { o.Dispatch = nil }} {
		t.Run(name, func(t *testing.T) {
			a := &preparationAdapter{mutateResume: mutate}
			h := newCheckpointHarness(t, a, filepath.Join(t.TempDir(), "invalid-resume.db"))
			id := submitPreparation(t, h)
			h.reconcile(t)
			advancePreparation(t, h)
			task := h.task(t, id)
			if task.State != generation.TaskFailed || task.Failure == nil || task.Failure.Class != generation.FailureProtocol {
				t.Fatalf("invalid resume admitted: %+v", task)
			}
		})
	}
}

func TestPreparationStaleReceiptCannotAdvanceOpaqueOrJob(t *testing.T) {
	a := &preparationAdapter{mutateStart: func(o *generation.Observation) { o.Dispatch.AckToken = "stale" }}
	h := newCheckpointHarness(t, a, filepath.Join(t.TempDir(), "stale.db"))
	id := submitPreparation(t, h)
	h.reconcile(t)
	aggregate, err := h.repository.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.Task.State != generation.TaskRunning || aggregate.CurrentAttempt.State != generation.AttemptSubmitting {
		t.Fatalf("stale receipt changed lifecycle: task=%s attempt=%s", aggregate.Task.State, aggregate.CurrentAttempt.State)
	}
	if aggregate.CurrentAttempt.UpstreamJob != nil || aggregate.CurrentAttempt.LastObservationPhase == generation.UpstreamPreparing || strings.Contains(string(aggregate.CurrentAttempt.Opaque), "token_ready") {
		t.Fatalf("stale receipt changed persisted continuation: %+v", aggregate.CurrentAttempt)
	}
	assertCheckpointSequence(t, h, id, "dispatching", 1)
}

func TestPreparationCancelAfterTokenPreventsConversion(t *testing.T) {
	a := &preparationAdapter{}
	h := newCheckpointHarness(t, a, filepath.Join(t.TempDir(), "cancel.db"))
	id := submitPreparation(t, h)
	h.reconcile(t)
	if _, err := h.module.Cancel(context.Background(), "owner-dispatch", id); err != nil {
		t.Fatal(err)
	}
	h = h.restart(t, a)
	advancePreparation(t, h)
	starts, uploads, resumes, converts, _ := a.counts()
	if starts != 1 || uploads != 1 || resumes != 0 || converts != 0 || h.task(t, id).State != generation.TaskCanceled {
		t.Fatal("canceled private preparation continued")
	}
}

func TestPreparationCancelWinsCASAfterConvertPermit(t *testing.T) {
	a := &preparationAdapter{}
	h := newCheckpointHarness(t, a, filepath.Join(t.TempDir(), "cas.db"))
	id := submitPreparation(t, h)
	h.reconcile(t)
	a.afterConvert = func() {
		if _, err := h.module.Cancel(context.Background(), "owner-dispatch", id); err != nil {
			t.Error(err)
		}
	}
	advancePreparation(t, h)
	aggregate, err := h.repository.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.Task.State != generation.TaskCanceled || aggregate.CurrentAttempt.State != generation.AttemptCanceled || aggregate.CurrentAttempt.UpstreamJob != nil {
		t.Fatalf("stale convert receipt overwrote cancellation: %+v", aggregate)
	}
	assertCheckpointSequence(t, h, id, "dispatching", 2)
}

func TestPreparationUploadCrashAfterPermitRestartsFailClosed(t *testing.T) {
	a := &preparationAdapter{afterUpload: func() { panic("process died after upload POST") }}
	h := newCheckpointHarness(t, a, filepath.Join(t.TempDir(), "upload-crash.db"))
	id := submitPreparation(t, h)
	if _, err := h.module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{WorkerID: "upload-crash", LeaseFor: time.Minute, BatchSize: 1}); err == nil {
		t.Fatal("want simulated upload crash")
	}
	assertCheckpointSequence(t, h, id, "dispatching", 1)
	a.afterUpload = nil
	h.clock.Advance(61 * time.Second)
	h = h.restart(t, a)
	h.reconcile(t)
	assertPreparationUnknown(t, h, id)
	starts, uploads, resumes, converts, observes := a.counts()
	if starts != 1 || uploads != 1 || resumes != 0 || converts != 0 || observes != 0 {
		t.Fatalf("upload unknown replayed %d/%d/%d/%d/%d", starts, uploads, resumes, converts, observes)
	}
}

func TestPreparationStaleConvertReceiptCannotCommitJob(t *testing.T) {
	a := &preparationAdapter{mutateResume: func(o *generation.Observation) { o.Dispatch.AckToken = "stale-upload-ack" }}
	h := newCheckpointHarness(t, a, filepath.Join(t.TempDir(), "stale-convert.db"))
	id := submitPreparation(t, h)
	h.reconcile(t)
	advancePreparation(t, h)
	aggregate, err := h.repository.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.Task.State != generation.TaskRunning || aggregate.CurrentAttempt.State != generation.AttemptRunning || aggregate.CurrentAttempt.UpstreamJob != nil || aggregate.CurrentAttempt.LastObservationPhase != generation.UpstreamPreparing || !strings.Contains(string(aggregate.CurrentAttempt.Opaque), "convert_dispatching") {
		t.Fatalf("stale conversion accepted: state=%s phase=%s job=%+v opaque=%s", aggregate.Task.State, aggregate.CurrentAttempt.LastObservationPhase, aggregate.CurrentAttempt.UpstreamJob, aggregate.CurrentAttempt.Opaque)
	}
	assertCheckpointSequence(t, h, id, "dispatching", 2)
	h = h.restart(t, a)
	advancePreparation(t, h)
	assertPreparationUnknown(t, h, id)
	starts, uploads, _, converts, observes := a.counts()
	if starts != 1 || uploads != 1 || converts != 1 || observes != 0 {
		t.Fatal("stale conversion receipt caused another POST")
	}
}

func TestPreparationCancelBeforePermitPreventsConvertSend(t *testing.T) {
	a := &preparationAdapter{}
	h := newCheckpointHarness(t, a, filepath.Join(t.TempDir(), "before-permit.db"))
	id := submitPreparation(t, h)
	h.reconcile(t)
	a.beforeConvert = func() {
		if _, err := h.module.Cancel(context.Background(), "owner-dispatch", id); err != nil {
			t.Error(err)
		}
	}
	advancePreparation(t, h)
	_, uploads, resumes, converts, _ := a.counts()
	if uploads != 1 || resumes != 1 || converts != 0 || h.task(t, id).State != generation.TaskCanceled {
		t.Fatal("stale lease obtained conversion send permit")
	}
	assertCheckpointSequence(t, h, id, "received", 1)
}

type noPreparationResumer struct{ adapter *preparationAdapter }

func (a noPreparationResumer) Start(ctx context.Context, r generation.StartRequest) (generation.Observation, error) {
	return a.adapter.Start(ctx, r)
}
func (a noPreparationResumer) Observe(ctx context.Context, r generation.ObserveRequest) (generation.Observation, error) {
	return a.adapter.Observe(ctx, r)
}
func (a noPreparationResumer) OpenAsset(ctx context.Context, r generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	return a.adapter.OpenAsset(ctx, r)
}
func TestPreparationRequiresOptionalResumePort(t *testing.T) {
	a := &preparationAdapter{}
	h := newCheckpointHarness(t, noPreparationResumer{a}, filepath.Join(t.TempDir(), "unsupported-port.db"))
	id := submitPreparation(t, h)
	h.reconcile(t)
	task := h.task(t, id)
	if task.State != generation.TaskFailed || task.Failure == nil || task.Failure.Class != generation.FailureProtocol {
		t.Fatalf("unsupported preparing admitted: %s %+v", task.State, task.Failure)
	}
}

func TestPreparationExplicitFailureSettlesConversionReceipt(t *testing.T) {
	for _, result := range []string{generation.DispatchResultDispatched, generation.DispatchResultNotDispatched} {
		t.Run(result, func(t *testing.T) {
			a := &preparationAdapter{mutateResume: func(o *generation.Observation) {
				o.Phase = generation.UpstreamFailed
				o.Job = nil
				o.Failure = &generation.Failure{Class: generation.FailureRejected}
				o.Dispatch.Result = result
			}}
			h := newCheckpointHarness(t, a, filepath.Join(t.TempDir(), "rejected.db"))
			id := submitPreparation(t, h)
			h.reconcile(t)
			advancePreparation(t, h)
			task := h.task(t, id)
			if task.State != generation.TaskFailed || task.Failure == nil || task.Failure.Class != generation.FailureRejected || task.Failure.Ambiguous {
				t.Fatalf("explicit rejection lost %s %+v", task.State, task.Failure)
			}
			state := "received"
			if result == generation.DispatchResultNotDispatched {
				state = "idle"
			}
			assertCheckpointSequence(t, h, id, state, 2)
		})
	}
}

func preparationRetryCatalog(t *testing.T, maxAttempts int) *catalog.Catalog {
	t.Helper()
	raw := catalogtest.DefaultCatalogBytes(t)
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var offerings []map[string]json.RawMessage
	if err := json.Unmarshal(doc["offerings"], &offerings); err != nil {
		t.Fatal(err)
	}
	for _, offering := range offerings {
		if string(offering["id"]) != `"meshy/meshy-7.1/text-to-3d"` {
			continue
		}
		var retry map[string]json.RawMessage
		if err := json.Unmarshal(offering["retry"], &retry); err != nil {
			t.Fatal(err)
		}
		retry["max_attempts"], _ = json.Marshal(maxAttempts)
		offering["retry"], _ = json.Marshal(retry)
	}
	doc["offerings"], _ = json.Marshal(offerings)
	raw, _ = json.Marshal(doc)
	result, err := catalog.Load(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPreparationKnownNotDispatchedHonorsFrozenRetryPolicy(t *testing.T) {
	for _, maxAttempts := range []int{1, 2} {
		t.Run(strconv.Itoa(maxAttempts), func(t *testing.T) {
			a := &preparationAdapter{convertNotDispatchedOnce: true}
			h := newCheckpointHarnessWithCatalog(t, a, filepath.Join(t.TempDir(), "retry.db"), nil, preparationRetryCatalog(t, maxAttempts))
			id := submitPreparation(t, h)
			h.reconcile(t)
			advancePreparation(t, h)
			aggregate, err := h.repository.Load(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if aggregate.Task.Offering.Retry.MaxAttempts != maxAttempts || aggregate.CurrentAttempt.Ordinal != 1 || aggregate.CurrentAttempt.State != generation.AttemptFailed {
				t.Fatal("retry did not use frozen policy")
			}
			assertCheckpointSequence(t, h, id, "idle", 2)
			if maxAttempts == 1 {
				if aggregate.Task.State != generation.TaskFailed || aggregate.Task.Failure == nil || aggregate.Task.Failure.Ambiguous {
					t.Fatal("single-attempt known rejection must fail without ambiguity")
				}
				advancePreparation(t, h)
				starts, uploads, _, converts, _ := a.counts()
				if starts != 1 || uploads != 1 || converts != 0 {
					t.Fatal("max_attempts=1 resubmitted")
				}
				return
			}
			if aggregate.Task.State != generation.TaskQueued {
				t.Fatalf("known retryable rejection should follow existing declared retry, state=%s", aggregate.Task.State)
			}
			advancePreparation(t, h)
			advancePreparation(t, h)
			starts, uploads, resumes, converts, _ := a.counts()
			if starts != 2 || uploads != 2 || resumes != 2 || converts != 1 {
				t.Fatalf("declared retry not bounded: %d/%d/%d/%d", starts, uploads, resumes, converts)
			}
		})
	}
}

func TestPreparationAmbiguityOverridesOtherwiseRetryableFailure(t *testing.T) {
	a := &preparationAdapter{resumeError: &generation.ProviderError{Failure: &generation.Failure{Class: generation.FailureRateLimited, Retryable: true, Ambiguous: true}}}
	h := newCheckpointHarnessWithCatalog(t, a, filepath.Join(t.TempDir(), "unknown-retry.db"), nil, preparationRetryCatalog(t, 2))
	id := submitPreparation(t, h)
	h.reconcile(t)
	advancePreparation(t, h)
	assertPreparationUnknown(t, h, id)
	advancePreparation(t, h)
	starts, uploads, resumes, converts, _ := a.counts()
	if starts != 1 || uploads != 1 || resumes != 1 || converts != 1 {
		t.Fatal("ambiguous failure followed otherwise declared retry")
	}
}
