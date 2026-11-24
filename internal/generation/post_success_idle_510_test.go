package generation_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
)

// A succeeded Task still owes post-success resources, but its Adapter is no
// longer registered (a connection removed by a Catalog publish, a restart with
// the Provider switched off). The post-success observation cannot run, and it
// must say so by moving next_action_at later: releasing the lease alone left the
// Task due, so the coordinator claimed and rewrote it on every poll until the
// deadline (#510).
func TestPostSuccessWithoutAdapterDefersNextAction(t *testing.T) {
	adapter := newPostSuccessAdapter()
	dbPath := filepath.Join(t.TempDir(), "ps-unregistered.db")
	h := newHistoricalPostSuccessHarness(t, adapter, dbPath)
	task := submitPostSuccess(t, h, `{"prompt":"a lantern","output_formats":["GLTF"],"authorize_extra_credits":true}`)
	if !generation.ResourcesPending(task.Resources) {
		t.Fatalf("fixture must leave a pending resource: %#v", task.Resources)
	}
	if err := h.module.Close(); err != nil {
		t.Fatal(err)
	}
	module := unregisteredPostSuccessModule(t, h)
	h.clock.Advance(61 * time.Second)
	first := idleReconcile(t, module, task.ID)
	if !first.NextActionAt.After(h.clock.Now()) {
		t.Fatalf("next_action_at = %s, now %s: an observation that cannot run left the Task due",
			first.NextActionAt, h.clock.Now())
	}
	deadline := generation.PostSuccessDeadline(first)
	if first.NextActionAt.After(deadline) {
		t.Fatalf("next_action_at = %s is past the deadline %s: the local finalizer would be late",
			first.NextActionAt, deadline)
	}
	// A poll before the deferred time does not claim or rewrite the Task.
	h.clock.Advance(500 * time.Millisecond)
	again := idleReconcile(t, module, task.ID)
	if again.Version != first.Version {
		t.Fatalf("version %d → %d: the idle Task was claimed again before its next action",
			first.Version, again.Version)
	}
	// The next idle round backs off further, still bounded by the deadline.
	h.clock.Advance(first.NextActionAt.Add(time.Millisecond).Sub(h.clock.Now()))
	second := idleReconcile(t, module, task.ID)
	firstDelay := first.NextActionAt.Sub(first.UpdatedAt)
	secondDelay := second.NextActionAt.Sub(second.UpdatedAt)
	if secondDelay <= firstDelay && second.NextActionAt.Before(deadline) {
		t.Fatalf("idle delay %s then %s: want a growing backoff", firstDelay, secondDelay)
	}
	// At the deadline the local finalizer still closes the slots.
	h.clock.Advance(deadline.Add(time.Second).Sub(h.clock.Now()))
	final := idleReconcile(t, module, task.ID)
	if generation.ResourcesPending(final.Resources) || !final.NextActionAt.IsZero() {
		t.Fatalf("after the deadline resources=%#v next=%s, want finalized", final.Resources, final.NextActionAt)
	}
}

func idleReconcile(t *testing.T, module *generation.Module, id generation.TaskID) generation.Task {
	t.Helper()
	if _, err := module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{
		WorkerID: "idle-worker", LeaseFor: time.Minute, BatchSize: 10,
	}); err != nil {
		t.Fatal(err)
	}
	task, err := module.Get(context.Background(), "owner-dispatch", id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// unregisteredPostSuccessModule reopens the harness database with a registry
// that does not know the Task's Adapter.
func unregisteredPostSuccessModule(t *testing.T, h *checkpointHarness) *generation.Module {
	t.Helper()
	repository, err := sqlitetest.Open(h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	taskCatalog, err := historicalPostSuccessCatalog(t)
	if err != nil {
		t.Fatal(err)
	}
	localStore, err := local.New(filepath.Join(filepath.Dir(h.dbPath), "assets"))
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := inputassets.New(repository, localStore)
	if err != nil {
		t.Fatal(err)
	}
	repository.SetClock(h.clock.Now)
	module, err := generation.New(generation.Dependencies{
		Conversions: generation.NoConversions,
		Repository:  repository,
		Catalog:     taskCatalog,
		Providers:   providers.NewRegistry(),
		Assets:      localStore,
		Inputs:      inputs,
		Clock:       h.clock,
		Cache:       &recordingCache{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Close() })
	return module
}
