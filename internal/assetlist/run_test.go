package assetlist_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// ensureRunStore keeps runs in memory and mimics CreateRun's one-run-per-list
// rule; the embedded nil interface keeps the rest of the run port out.
type ensureRunStore struct {
	assetlist.RunStore
	runs        map[string]assetlist.Run
	createCalls int
	statusesErr error
	createErr   error
}

func newEnsureRunStore() *ensureRunStore {
	return &ensureRunStore{runs: map[string]assetlist.Run{}}
}

func ensureRunKey(ownerID, listID string) string { return ownerID + "\x00" + listID }

func (store *ensureRunStore) GetRunByList(
	_ context.Context,
	ownerID, projectID, listID string,
) (assetlist.Run, error) {
	run, found := store.runs[ensureRunKey(ownerID, listID)]
	if !found || run.ProjectID != projectID {
		return assetlist.Run{}, assetlist.ErrNotFound
	}
	return run, nil
}

// RunSummaries keeps list reads undecorated in this fixture.
func (store *ensureRunStore) RunSummaries(_ context.Context, _, _ string) (map[string]assetlist.RunSummary, error) {
	return nil, nil
}

func (store *ensureRunStore) RunsByStatus(_ context.Context, status string) ([]assetlist.Run, error) {
	if store.statusesErr != nil {
		return nil, store.statusesErr
	}
	result := make([]assetlist.Run, 0)
	for _, run := range store.runs {
		if run.Status == status {
			result = append(result, run)
		}
	}
	return result, nil
}

func (store *ensureRunStore) CreateRun(_ context.Context, run assetlist.Run) (assetlist.Run, error) {
	store.createCalls++
	if store.createErr != nil {
		return assetlist.Run{}, store.createErr
	}
	key := ensureRunKey(run.OwnerID, run.ListID)
	if existing, found := store.runs[key]; found {
		return existing, nil
	}
	store.runs[key] = run
	return run, nil
}

func newEnsureRunService(t *testing.T, runs assetlist.RunStore, mutate func(*assetlist.Config)) *assetlist.Service {
	t.Helper()
	service, _, _ := newService(t, func(config *assetlist.Config) {
		config.Runs = runs
		config.Schema.Execution = &catalog.AssetListExecutionSchema{Enabled: true}
		if mutate != nil {
			mutate(config)
		}
	})
	return service
}

// The first EnsureRun stores a running run; repeating it returns the same run
// and writes nothing.
func TestEnsureRunCreatesOneRunningRun(t *testing.T) {
	t.Parallel()
	runs := newEnsureRunStore()
	service := newEnsureRunService(t, runs, nil)
	ctx := context.Background()

	run, err := service.EnsureRun(ctx, " owner-a ", " prj_1 ", " alist_1 ")
	if err != nil {
		t.Fatal(err)
	}
	if run.OwnerID != "owner-a" || run.ProjectID != "prj_1" || run.ListID != "alist_1" ||
		run.Status != assetlist.RunRunning || !run.Active() {
		t.Fatalf("run = %+v", run)
	}
	if !strings.HasPrefix(run.ID, "run_") {
		t.Fatalf("run id = %q", run.ID)
	}
	replayed, err := service.EnsureRun(ctx, "owner-a", "prj_1", "alist_1")
	if err != nil || replayed.ID != run.ID || runs.createCalls != 1 {
		t.Fatalf("replayed = %+v, %v; creates = %d", replayed, err, runs.createCalls)
	}
}

// One running list per owner: a second confirmation waits as idle, while
// another owner's running list does not block this owner.
func TestEnsureRunWaitsBehindTheOwnersRunningList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	runs := newEnsureRunStore()
	runs.runs[ensureRunKey("owner-a", "alist_running")] = assetlist.Run{
		ID: "run_running", OwnerID: "owner-a", ProjectID: "prj_1",
		ListID: "alist_running", Status: assetlist.RunRunning,
	}
	service := newEnsureRunService(t, runs, nil)
	waiting, err := service.EnsureRun(ctx, "owner-a", "prj_1", "alist_2")
	if err != nil || waiting.Status != assetlist.RunIdle || !waiting.Waiting() {
		t.Fatalf("second list = %+v, %v; want idle", waiting, err)
	}

	other := newEnsureRunStore()
	other.runs[ensureRunKey("owner-b", "alist_running")] = assetlist.Run{
		ID: "run_other", OwnerID: "owner-b", ProjectID: "prj_1",
		ListID: "alist_running", Status: assetlist.RunRunning,
	}
	service = newEnsureRunService(t, other, nil)
	started, err := service.EnsureRun(ctx, "owner-a", "prj_1", "alist_2")
	if err != nil || started.Status != assetlist.RunRunning {
		t.Fatalf("other owner = %+v, %v; want running", started, err)
	}
}

// A deployment without the run store or with execution disabled creates no
// run; blank identities are not looked up.
func TestEnsureRunRequiresExecution(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	plain, _, _ := newService(t, nil)
	if _, err := plain.EnsureRun(ctx, "owner-a", "prj_1", "alist_1"); !errors.Is(err, assetlist.ErrRunUnavailable) {
		t.Fatalf("no run store = %v; want ErrRunUnavailable", err)
	}
	disabled := newEnsureRunService(t, newEnsureRunStore(), func(config *assetlist.Config) {
		config.Schema.Execution = &catalog.AssetListExecutionSchema{Enabled: false}
	})
	if _, err := disabled.EnsureRun(ctx, "owner-a", "prj_1", "alist_1"); !errors.Is(err, assetlist.ErrExecutionDisabled) {
		t.Fatalf("disabled = %v; want ErrExecutionDisabled", err)
	}
	service := newEnsureRunService(t, newEnsureRunStore(), nil)
	if _, err := service.EnsureRun(ctx, "", "prj_1", "alist_1"); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("blank owner = %v; want ErrNotFound", err)
	}
}

// Decide creates the run after a confirm (including replace) and never after a
// reject; a repeated decision leaves the one run untouched.
func TestDecideConfirmCreatesTheRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	runs := newEnsureRunStore()
	service := newEnsureRunService(t, runs, nil)
	list, err := service.Propose(ctx, proposal())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Decide(ctx, assetlist.DecideInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: list.ID, Decision: assetlist.DecisionConfirm,
	}); err != nil {
		t.Fatal(err)
	}
	run, err := runs.GetRunByList(ctx, "owner-a", "prj_1", list.ID)
	if err != nil || run.Status != assetlist.RunRunning {
		t.Fatalf("confirmed run = %+v, %v", run, err)
	}
	if _, err := service.Decide(ctx, assetlist.DecideInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: list.ID, Decision: assetlist.DecisionConfirm,
	}); err != nil {
		t.Fatal(err)
	}
	if runs.createCalls != 1 {
		t.Fatalf("repeated confirm created %d runs", runs.createCalls)
	}

	second := proposal()
	second.RequestKey = "req_2"
	second.Title = "Second list"
	rejected, err := service.Propose(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Decide(ctx, assetlist.DecideInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: rejected.ID, Decision: assetlist.DecisionReject,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runs.GetRunByList(ctx, "owner-a", "prj_1", rejected.ID); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("rejected list run = %v; want none", err)
	}
}

// A run write that fails does not undo the person's confirmation: the runner
// creates the run lazily on its next sweep.
func TestDecideConfirmSurvivesARunWriteFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	runs := newEnsureRunStore()
	runs.createErr = errors.New("database is locked")
	service := newEnsureRunService(t, runs, nil)
	list, err := service.Propose(ctx, proposal())
	if err != nil {
		t.Fatal(err)
	}
	decided, err := service.Decide(ctx, assetlist.DecideInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: list.ID, Decision: assetlist.DecisionConfirm,
	})
	if err != nil || decided.Status != assetlist.StatusConfirmed {
		t.Fatalf("confirm = %+v, %v", decided, err)
	}
	if _, err := runs.GetRunByList(ctx, "owner-a", "prj_1", list.ID); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("failed run write left a run: %v", err)
	}
}

// missingRunStore adds the confirmed lists without a run to the EnsureRun
// fixture (#508).
type missingRunStore struct {
	*ensureRunStore
	missing []assetlist.Run
}

func (store *missingRunStore) ConfirmedListsWithoutRun(_ context.Context, limit int) ([]assetlist.Run, error) {
	result := make([]assetlist.Run, 0, len(store.missing))
	for _, key := range store.missing {
		if _, found := store.runs[ensureRunKey(key.OwnerID, key.ListID)]; found {
			continue
		}
		result = append(result, key)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

// #508 asset-list-04: a confirmed list whose run was never stored (EnsureRun
// failed at confirm, or the list was confirmed while automatic production was
// off) gets an idle run, so the card offers the person's explicit start. It is
// never started automatically, even when the owner runs nothing else.
func TestBackfillIdleRunsGivesAConfirmedListWithoutARunAnIdleRun508(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	runs := &missingRunStore{ensureRunStore: newEnsureRunStore(), missing: []assetlist.Run{
		{OwnerID: "owner-a", ProjectID: "prj_1", ListID: "alist_1"},
	}}
	service := newEnsureRunService(t, runs, nil)
	created, err := service.BackfillIdleRuns(ctx)
	if err != nil || created != 1 {
		t.Fatalf("backfill = %d, %v; want one idle run", created, err)
	}
	run := runs.runs[ensureRunKey("owner-a", "alist_1")]
	if run.Status != assetlist.RunIdle || run.ProjectID != "prj_1" || !strings.HasPrefix(run.ID, "run_") {
		t.Fatalf("backfilled run = %+v; want idle", run)
	}
	again, err := service.BackfillIdleRuns(ctx)
	if err != nil || again != 0 || runs.createCalls != 1 {
		t.Fatalf("second backfill = %d, %v; creates = %d", again, err, runs.createCalls)
	}

	// With automatic production off nothing is created.
	off := &missingRunStore{ensureRunStore: newEnsureRunStore(), missing: runs.missing}
	disabled := newEnsureRunService(t, off, func(config *assetlist.Config) {
		config.Schema.Execution = &catalog.AssetListExecutionSchema{Enabled: false}
	})
	if created, err := disabled.BackfillIdleRuns(ctx); !errors.Is(err, assetlist.ErrExecutionDisabled) ||
		created != 0 || off.createCalls != 0 {
		t.Fatalf("disabled backfill = %d, %v; creates = %d", created, err, off.createCalls)
	}
}
