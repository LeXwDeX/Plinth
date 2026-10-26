package generation_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// #298: an upstream returned a job ID that another Task already records. The
// unique index on (upstream_provider_id, upstream_job_id) rejected the write,
// the coordinator treated it as a global persistence fault and the process
// exited. Now only that Task ends (submission_unknown, never resent).

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func submitMeshyPrompt(t *testing.T, h *harness, prompt string) generation.TaskID {
	t.Helper()
	input, err := json.Marshal(map[string]string{"prompt": prompt})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	return accepted.TaskID
}

func TestDuplicateUpstreamJobEndsOnlyThatTaskAndCoordinatorKeepsRunning(t *testing.T) {
	logs := &lockedBuffer{}
	adapter := mock.New()
	h := newConfiguredHarness(t, adapter, nil, func(dependencies *generation.Dependencies) {
		dependencies.Logger = slog.New(slog.NewTextHandler(logs, nil))
	})
	ctx := context.Background()

	first := submitMeshyPrompt(t, h, mock.MarkerDuplicateRemoteID+" first")
	h.reconcile(t) // first records the upstream job
	if aggregate, err := h.repository.Load(ctx, first); err != nil ||
		aggregate.CurrentAttempt == nil || aggregate.CurrentAttempt.UpstreamJob == nil ||
		aggregate.CurrentAttempt.UpstreamJob.RemoteID != mock.DuplicateRemoteID {
		t.Fatalf("first task did not record the upstream job: %#v err=%v", aggregate.CurrentAttempt, err)
	}

	duplicate := submitMeshyPrompt(t, h, mock.MarkerDuplicateRemoteID+" second")
	other := submitMeshyPrompt(t, h, "an unrelated concurrent task")

	runCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	err := h.module.Run(runCtx, generation.CoordinatorConfig{WorkerID: "dup", Concurrency: 2, PollEvery: time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("coordinator stopped on a duplicate upstream job: %v", err)
	}

	failed, err := h.module.Get(ctx, "owner-a", duplicate)
	if err != nil {
		t.Fatal(err)
	}
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	want := taskCatalog.Application().Runtime.GenerationMessages.UpstreamJobConflict
	if want == "" {
		t.Fatal("catalog declares no upstream_job_conflict text")
	}
	if failed.State != generation.TaskFailed || failed.Failure == nil ||
		failed.Failure.Class != generation.FailureSubmissionUnknown || failed.Failure.Retryable ||
		failed.Failure.SafeDetail != want {
		t.Fatalf("duplicate task state=%s failure=%#v", failed.State, failed.Failure)
	}
	aggregate, err := h.repository.Load(ctx, duplicate)
	if err != nil {
		t.Fatal(err)
	}
	if attempt := aggregate.CurrentAttempt; attempt == nil || attempt.State != generation.AttemptSubmissionUnknown || attempt.UpstreamJob != nil {
		t.Fatalf("duplicate attempt=%#v", attempt)
	}
	events, err := h.repository.Events(ctx, "owner-a", duplicate, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	evidence := map[string]string{}
	for _, event := range events {
		if event.Type == "attempt.upstream_job_conflict" {
			if err := json.Unmarshal(event.Data, &evidence); err != nil {
				t.Fatal(err)
			}
		}
	}
	if evidence["remote_id"] != mock.DuplicateRemoteID || evidence["recorded_task_id"] != string(first) {
		t.Fatalf("conflict evidence=%v", evidence)
	}
	logged := logs.String()
	if !strings.Contains(logged, "task_id="+string(duplicate)) || !strings.Contains(logged, "remote_id="+mock.DuplicateRemoteID) ||
		!strings.Contains(logged, "recorded_task_id="+string(first)) {
		t.Fatalf("conflict log does not name the task and remote ID:\n%s", logged)
	}

	// The re-driven Task is not sent again, and the others finish normally.
	before := failed.Version
	h.reconcile(t)
	if again, _ := h.module.Get(ctx, "owner-a", duplicate); again.Version != before || again.State != generation.TaskFailed {
		t.Fatalf("terminal duplicate changed: %#v", again)
	}
	for _, id := range []generation.TaskID{first, other} {
		if task := h.runUntilTerminal(t, "owner-a", id); task.State != generation.TaskSucceeded {
			t.Fatalf("task %s state=%s failure=%#v", id, task.State, task.Failure)
		}
	}
	if aggregate, _ := h.repository.Load(ctx, first); aggregate.CurrentAttempt.UpstreamJob.RemoteID != mock.DuplicateRemoteID {
		t.Fatalf("first task lost its upstream job: %#v", aggregate.CurrentAttempt.UpstreamJob)
	}
}

// failingCommits fails every Commit of one Task with a fixed error, as a
// constraint violation or a bad row would.
type failingCommits struct {
	generation.Repository
	mu   sync.Mutex
	task generation.TaskID
	err  error
}

func (r *failingCommits) Commit(ctx context.Context, commit generation.Commit) error {
	r.mu.Lock()
	task, err := r.task, r.err
	r.mu.Unlock()
	if commit.Task.ID == task {
		return err
	}
	return r.Repository.Commit(ctx, commit)
}

type coded int

func (c coded) Error() string { return fmt.Sprintf("sqlite result code %d", int(c)) }
func (c coded) Code() int     { return int(c) }

// Guard: a step error of one Task never stops the coordinator; only a
// database that is unusable as a whole does.
func TestCoordinatorSurvivesPerTaskStepErrors(t *testing.T) {
	for name, failure := range map[string]error{
		"unique constraint": coded(2067),
		"unclassified":      errors.New("row cannot be written"),
	} {
		t.Run(name, func(t *testing.T) {
			adapter := &coordinatorAdapter{}
			h := newHarness(t, adapter, nil)
			repo := &failingCommits{Repository: h.repository, err: failure}
			m := remediationModule(t, h, repo, adapter)
			bad := submitRemediation(t, m, h.repository, h.clock, "bad")
			repo.mu.Lock()
			repo.task = bad
			repo.mu.Unlock()
			good := submitRemediation(t, m, h.repository, h.clock, "good")
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			err := m.Run(ctx, generation.CoordinatorConfig{Concurrency: 2, PollEvery: time.Millisecond})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("per-task error stopped the coordinator: %v", err)
			}
			task, err := m.Get(context.Background(), "audit", good)
			if err != nil {
				t.Fatal(err)
			}
			if task.State != generation.TaskRunning || adapter.starts.Load() == 0 {
				t.Fatalf("other task did not progress: state=%s starts=%d", task.State, adapter.starts.Load())
			}
		})
	}
	for name, failure := range map[string]error{
		"corrupt":   coded(11),
		"closed db": fmt.Errorf("claim: %w", sql.ErrConnDone),
	} {
		t.Run("global "+name, func(t *testing.T) {
			adapter := &coordinatorAdapter{}
			h := newHarness(t, adapter, nil)
			repo := &leaseRepo{Repository: h.repository, claimError: failure}
			m := remediationModule(t, h, repo, adapter)
			submitRemediation(t, m, h.repository, h.clock, "fast")
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			err := m.Run(ctx, generation.CoordinatorConfig{Concurrency: 1, PollEvery: time.Millisecond})
			if !errors.Is(err, failure) {
				t.Fatalf("global persistence failure did not stop the coordinator: %v", err)
			}
		})
	}
}
