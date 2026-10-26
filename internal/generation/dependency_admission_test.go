package generation_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
)

type changingDependencyAdapter struct {
	generation.ProviderPort
	delivered bool
	failStart bool
	starts    int
	running   bool
	observes  int
}

func (a *changingDependencyAdapter) ResolveDependencyJob(r generation.DependencyJobRequest) (generation.UpstreamJob, error) {
	job := r.Job
	if a.delivered {
		job.RemoteID = "delivered-refine"
	}
	return job, nil
}
func (a *changingDependencyAdapter) Start(ctx context.Context, r generation.StartRequest) (generation.Observation, error) {
	a.starts++
	if a.running {
		return generation.Observation{Phase: generation.UpstreamAccepted, Job: &generation.UpstreamJob{ProviderID: "meshy", RemoteID: "already-paid"}}, nil
	}
	if a.failStart {
		return generation.Observation{}, &generation.ProviderError{Failure: &generation.Failure{Class: generation.FailureUpstreamUnavailable, Retryable: true, SafeDetail: "temporary"}}
	}
	return a.ProviderPort.Start(ctx, r)
}

func (a *changingDependencyAdapter) Observe(ctx context.Context, r generation.ObserveRequest) (generation.Observation, error) {
	a.observes++
	if a.running {
		return generation.Observation{Phase: generation.UpstreamRunning, Job: &r.Job}, nil
	}
	return a.ProviderPort.Observe(ctx, r)
}

func TestChangedDependencyDoesNotResubmitAlreadyStartedJob(t *testing.T) {
	h, a, p := dependencyAdmissionFixture(t)
	ctx := context.Background()
	cmd := authorizePrepared(t, ctx, h.repository, p, "already-paid")
	accepted, err := h.module.SubmitPrepared(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	a.running = true
	h.reconcile(t)
	before := a.starts
	a.delivered = true
	// An idempotent replay returns the original task rather than re-preparing it.
	replay, err := h.module.SubmitPrepared(ctx, cmd)
	if err != nil || replay.TaskID != accepted.TaskID {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	h.reconcile(t)
	agg, err := h.repository.Load(ctx, accepted.TaskID)
	if err != nil || agg.Task.State != generation.TaskRunning || agg.CurrentAttempt.UpstreamJob.RemoteID != "already-paid" || a.starts != before || a.observes != 1 {
		t.Fatalf("task=%#v starts=%d observes=%d err=%v", agg.Task, a.starts, a.observes, err)
	}
}

func dependencyAdmissionFixture(t *testing.T) (*harness, *changingDependencyAdapter, generation.PreparedInput) {
	t.Helper()
	a := &changingDependencyAdapter{ProviderPort: sixFormatFixtureAdapter{next: &countingDependencyAdapter{}}}
	h := newConfiguredHarness(t, a, nil, func(d *generation.Dependencies) {
		r := providers.NewRegistry()
		if err := r.Register("meshy.v2", a); err != nil {
			t.Fatal(err)
		}
		d.Providers = r
	})
	ctx := context.Background()
	source, err := h.submitApproved(t, ctx, generation.SubmitCommand{OwnerID: "owner-a", Capability: generation.TextTo3D, OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"source"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if task := h.runUntilTerminal(t, "owner-a", source.TaskID); task.State != generation.TaskSucceeded {
		t.Fatalf("source=%#v", task)
	}
	p, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: "owner-a", SessionID: "generation-test-session:owner-a", Capability: generation.Rigging, OfferingID: "meshy/processing/rigging", Input: []byte(fmt.Sprintf(`{"source_task":%q}`, source.TaskID))})
	if err != nil {
		t.Fatal(err)
	}
	return h, a, p
}

func TestDependencyChangedAfterPreparationRequiresNewApproval(t *testing.T) {
	h, a, p := dependencyAdmissionFixture(t)
	ctx := context.Background()
	cmd := authorizePrepared(t, ctx, h.repository, p, "historical-preview")
	a.delivered = true
	// The old hash remains readable, but it cannot authorize a different result.
	old, err := h.module.GetPreparedInput(ctx, p.OwnerID, p.ID)
	if err != nil || old.InputHash != p.InputHash {
		t.Fatalf("historical hash=%s error=%v", old.InputHash, err)
	}
	if _, err = h.module.SubmitPrepared(ctx, cmd); !generation.IsCode(err, generation.ErrConflict) {
		t.Fatalf("error=%v", err)
	}
	if a.starts != 1 {
		t.Fatalf("unexpected paid starts=%d", a.starts)
	}
}

func TestDependencyAdmissionGuardSurvivesReopenAndRetry(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(fmt.Sprintf("retry=%t", retry), func(t *testing.T) {
			h, a, p := dependencyAdmissionFixture(t)
			ctx := context.Background()
			accepted, err := h.module.SubmitPrepared(ctx, authorizePrepared(t, ctx, h.repository, p, "old-accepted"))
			if err != nil {
				t.Fatal(err)
			}
			if retry {
				a.failStart = true
				h.reconcile(t)
				agg, err := h.repository.Load(ctx, accepted.TaskID)
				if err != nil || agg.Task.State != generation.TaskQueued {
					t.Fatalf("retry task=%#v err=%v", agg.Task, err)
				}
			}
			beforeStarts := a.starts
			a.delivered = true
			// Close and reopen the actual SQLite file; json:- Admission is absent.
			if err := h.module.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := sqlite.Open(h.dbPath)
			if err != nil {
				t.Fatal(err)
			}
			h.repository = reopened
			reopened.SetClock(h.clock.Now)
			agg, err := reopened.Load(ctx, accepted.TaskID)
			if err != nil || agg.Task.Admission != nil {
				t.Fatalf("loaded admission=%#v err=%v", agg.Task.Admission, err)
			}
			id, err := reopened.AdmittedPreparedInput(ctx, p.OwnerID, accepted.TaskID)
			if err != nil || id != p.ID {
				t.Fatalf("bound prepared=%s err=%v", id, err)
			}
			for _, check := range []struct {
				owner generation.OwnerID
				task  generation.TaskID
			}{{"other-owner", accepted.TaskID}, {p.OwnerID, "unbound"}} {
				if id, err := reopened.AdmittedPreparedInput(ctx, check.owner, check.task); err != nil || id != "" {
					t.Fatalf("unbound/cross-owner=%s %v", id, err)
				}
			}
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			registry := providers.NewRegistry()
			if err := registry.Register("meshy.v2", a); err != nil {
				t.Fatal(err)
			}
			store, err := local.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			inputs, err := inputassets.New(reopened, store)
			if err != nil {
				t.Fatal(err)
			}
			h.module, err = generation.New(generation.Dependencies{Repository: reopened, Catalog: c, Providers: registry, Assets: h.assets, Inputs: inputs, Clock: h.clock})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = h.module.Close() })
			task := h.runUntilTerminal(t, p.OwnerID, accepted.TaskID)
			if task.State != generation.TaskFailed || task.Failure == nil || task.Failure.Class != generation.FailureInvalidInput || task.Failure.Retryable {
				t.Fatalf("guard result=%#v", task)
			}
			if a.starts != beforeStarts {
				t.Fatalf("dispatch after changed approval: starts=%d before=%d", a.starts, beforeStarts)
			}
			events := materializationEvidenceEvents(t, reopened, accepted.TaskID)
			if countEvents(events, "attempt.task_dependency_changed") != 1 {
				t.Fatalf("events=%#v", events)
			}
		})
	}
}

type failedAdmissionRead struct {
	*sqlite.Repository
	err      error
	prepared bool
}

func (r *failedAdmissionRead) AdmittedPreparedInput(ctx context.Context, owner generation.OwnerID, task generation.TaskID) (generation.PreparedInputID, error) {
	if !r.prepared && r.err != nil {
		return "", r.err
	}
	return r.Repository.AdmittedPreparedInput(ctx, owner, task)
}
func (r *failedAdmissionRead) GetPreparedInput(ctx context.Context, owner generation.OwnerID, id generation.PreparedInputID) (generation.PreparedInput, error) {
	if r.prepared && r.err != nil {
		return generation.PreparedInput{}, r.err
	}
	return r.Repository.GetPreparedInput(ctx, owner, id)
}

func TestDependencyAdmissionReadFailureDoesNotDispatchAndRecovers(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, readErr := range []error{errors.New("private repository failure"), context.Canceled} {
			t.Run(fmt.Sprintf("prepared=%t/error=%v", prepared, readErr), func(t *testing.T) {
				var repo *failedAdmissionRead
				a := &changingDependencyAdapter{ProviderPort: sixFormatFixtureAdapter{next: &countingDependencyAdapter{}}}
				h := newConfiguredHarness(t, a, nil, func(d *generation.Dependencies) {
					repo = &failedAdmissionRead{Repository: d.Repository.(*sqlite.Repository), prepared: prepared}
					d.Repository = repo
				})
				ctx := context.Background()
				accepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{OwnerID: "owner-a", Capability: generation.TextTo3D, OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"read failure"}`)})
				if err != nil {
					t.Fatal(err)
				}
				beforeID, err := h.repository.AdmittedPreparedInput(ctx, "owner-a", accepted.TaskID)
				if err != nil {
					t.Fatal(err)
				}
				repo.err = readErr
				_, err = h.module.ReconcileOnce(ctx, generation.CoordinatorConfig{WorkerID: "read-failure", LeaseFor: time.Minute, BatchSize: 10})
				if err == nil {
					t.Fatal("storage failure not returned")
				}
				task, getErr := h.module.Get(ctx, "owner-a", accepted.TaskID)
				if getErr != nil || task.State.Terminal() || task.Failure != nil || a.starts != 0 {
					t.Fatalf("task=%#v starts=%d err=%v", task, a.starts, getErr)
				}
				if countEvents(materializationEvidenceEvents(t, h.repository, accepted.TaskID), "attempt.task_dependency_changed") != 0 {
					t.Fatal("storage failure recorded as invalid dependency")
				}
				repo.err = nil
				h.clock.Advance(2 * time.Minute)
				task = h.runUntilTerminal(t, "owner-a", accepted.TaskID)
				afterID, err := h.repository.AdmittedPreparedInput(ctx, "owner-a", accepted.TaskID)
				if err != nil || task.State != generation.TaskSucceeded || a.starts != 1 || afterID != beforeID {
					t.Fatalf("recovery state=%s starts=%d admission=%s/%s err=%v", task.State, a.starts, beforeID, afterID, err)
				}
			})
		}
	}
}
