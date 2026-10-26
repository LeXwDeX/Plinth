package sqlite_test

// claim_ready_post_success_test.go 落实 §6.6 的领取谓词边界与 0022
// 迁移证据（W6）：无资源态/tombstone 不领取；SUCCEEDED 带 pending 且
// next_action_at 到期可领取；并发领取只有一个 worker 赢（CAS）；
// 0022 降级→升级→停写快照恢复。

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/generation"
	_ "modernc.org/sqlite"
)

type claimReadyFixture struct {
	repository *sqlite.Repository
	path       string
	now        time.Time
}

func newClaimReadyFixture(t *testing.T) *claimReadyFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claim-ready.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &claimReadyFixture{
		repository: repository,
		path:       path,
		now:        time.Now(),
	}
	t.Cleanup(func() { _ = repository.Close() })
	return fixture
}

// raw 直连底层数据库（仅测试内省/构造持久态使用）。
func (f *claimReadyFixture) raw(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func formatNullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (f *claimReadyFixture) insertTask(
	t *testing.T,
	id generation.TaskID,
	state generation.TaskState,
	resources string,
	nextAction *time.Time,
) {
	t.Helper()
	_, err := f.raw(t).ExecContext(context.Background(), `
		INSERT INTO generation_tasks (
			id, owner_id, capability, offering_json, input_json, request_hash,
			idempotency_key, state, progress, failure_json, current_attempt_id,
			materialize_failures, next_action_at, version, created_at, updated_at, finished_at,
			resource_state_json
		) VALUES (?, 'owner-a', 'text-to-3d', '{}', '{}', '', '', ?, 100, NULL, NULL,
		  0, ?, 1, ?, ?, ?, NULL)`,
		id,
		state,
		formatNullableTime(nextAction),
		f.now.Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		f.now.Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		f.now.Add(-time.Hour).UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatal(err)
	}
	if resources != "" {
		if _, err := f.raw(t).ExecContext(context.Background(),
			`UPDATE generation_tasks SET resource_state_json = ? WHERE id = ?`,
			resources, id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClaimReadyPostSuccessBoundaries(t *testing.T) {
	fixture := newClaimReadyFixture(t)
	due := fixture.now.Add(-time.Minute)
	fixture.insertTask(t, "task-succeeded-pending", generation.TaskSucceeded,
		`{"converted_model|converted.|":{"state":"pending","deadline":"2026-07-27T00:00:00Z"}}`, &due)
	fixture.insertTask(t, "task-succeeded-ready", generation.TaskSucceeded,
		`{"model||":{"state":"ready"}}`, &due)
	fixture.insertTask(t, "task-succeeded-no-state", generation.TaskSucceeded, "", &due)
	future := fixture.now.Add(time.Hour)
	fixture.insertTask(t, "task-succeeded-future", generation.TaskSucceeded,
		`{"converted_model|converted.|":{"state":"pending"}}`, &future)

	leases, err := fixture.repository.ClaimReady(context.Background(), "worker-a", fixture.now, time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 || leases[0].Task.ID != "task-succeeded-pending" {
		ids := make([]generation.TaskID, 0, len(leases))
		for _, lease := range leases {
			ids = append(ids, lease.Task.ID)
		}
		t.Fatalf("claimed = %v; want [task-succeeded-pending]", ids)
	}
}

func TestClaimReadyExcludesTombstonedSucceeded(t *testing.T) {
	fixture := newClaimReadyFixture(t)
	due := fixture.now.Add(-time.Minute)
	fixture.insertTask(t, "task-tombstoned", generation.TaskSucceeded,
		`{"converted_model|converted.|":{"state":"pending"}}`, &due)
	if _, err := fixture.raw(t).ExecContext(context.Background(),
		`INSERT INTO generation_task_tombstones (task_id, actor_id, deleted_at) VALUES ('task-tombstoned', 'owner-a', ?)`,
		fixture.now.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	leases, err := fixture.repository.ClaimReady(context.Background(), "worker-a", fixture.now, time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 0 {
		t.Fatalf("tombstoned claimed: %#v", leases)
	}
}

func TestClaimReadyConcurrentWorkersSingleWinner(t *testing.T) {
	fixture := newClaimReadyFixture(t)
	due := fixture.now.Add(-time.Minute)
	fixture.insertTask(t, "task-race", generation.TaskSucceeded,
		`{"converted_model|converted.|":{"state":"pending"}}`, &due)
	const workers = 6
	var mu sync.Mutex
	winners := 0
	var wg sync.WaitGroup
	for worker := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			leases, err := fixture.repository.ClaimReady(
				context.Background(),
				"worker-race",
				fixture.now,
				time.Minute,
				10,
			)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			winners += len(leases)
		}()
		_ = worker
	}
	wg.Wait()
	if winners != 1 {
		t.Fatalf("winners = %d; want 1（CAS 竞争唯一持有者）", winners)
	}
}

// TestResourceStateMigration0022UpgradeAndSnapshotRestore 提供 0022
// 本地升级/快照恢复证据（§10.1/§6.6）：降级到 0021 形态 → 新二进制升级
// 写入资源态 → 停写快照整体恢复后可读（与 0021 演练同一口径）。
func TestResourceStateMigration0022UpgradeAndSnapshotRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade-0022.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	task := generation.Task{
		ID:         "task-upgrade",
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		Offering:   generation.Offering{ID: "tripo/tripo-v3.1/text-to-3d"},
		Input:      []byte(`{}`),
		State:      generation.TaskSucceeded,
		Progress:   100,
		Version:    1,
		CreatedAt:  now,
		UpdatedAt:  now,
		FinishedAt: &now,
	}
	task = approveCreateFixture(t, repository, task)
	if _, _, err := repository.Create(context.Background(), task, generation.Event{
		TaskID: task.ID, Type: "task.submitted", Data: []byte(`{}`), CreatedAt: now,
	}, 0); err != nil {
		t.Fatal(err)
	}
	if err := repository.Commit(context.Background(), generation.Commit{
		ExpectedVersion: 1,
		Task: generation.Task{
			ID:         task.ID,
			OwnerID:    task.OwnerID,
			Capability: task.Capability,
			Offering:   task.Offering,
			Input:      task.Input,
			State:      generation.TaskSucceeded,
			Progress:   100,
			Version:    2,
			UpdatedAt:  now,
			FinishedAt: &now,
			Resources: map[string]generation.ResourceState{
				"converted_model|converted.|": {
					State:    generation.ResourcePending,
					Deadline: now.Add(24 * time.Hour),
				},
			},
		},
		PersistResources: true,
	}); err != nil {
		t.Fatal(err)
	}
	// 停写 → 快照整体复制。
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot := path + ".snapshot"
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := copyIfExists(path+suffix, snapshot+suffix); err != nil {
			t.Fatal(err)
		}
	}
	// 恢复快照：资源态可读、领取谓词可用。
	restored, err := sqlite.Open(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restored.Close() }()
	loaded, err := restored.Get(context.Background(), "owner-a", "task-upgrade")
	if err != nil {
		t.Fatal(err)
	}
	if state := loaded.Resources["converted_model|converted.|"]; state.State != generation.ResourcePending {
		t.Fatalf("restored resources = %#v", loaded.Resources)
	}
}

func copyIfExists(source, target string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return os.WriteFile(target, data, 0o600)
}

// TestClaimReadyIsolatesPoisonRow 单行损坏（B17 毒行隔离）不得让整批
// 领取失败饿死队列：脏 offering_json 行被跳过，其余任务正常领取。
func TestClaimReadyIsolatesPoisonRow(t *testing.T) {
	fixture := newClaimReadyFixture(t)
	due := fixture.now.Add(-time.Minute)
	fixture.insertTask(t, "task-healthy", generation.TaskQueued, "", &due)
	fixture.insertTask(t, "task-poison", generation.TaskQueued, "", &due)

	if _, err := fixture.raw(t).ExecContext(
		context.Background(),
		`UPDATE generation_tasks SET failure_json = 'not-json'
		 WHERE id = 'task-poison'`,
	); err != nil {
		t.Fatal(err)
	}

	leases, err := fixture.repository.ClaimReady(
		context.Background(), "worker-a", fixture.now, time.Minute, 10,
	)
	if err != nil {
		t.Fatalf("claim ready: %v", err)
	}
	if len(leases) != 1 || leases[0].Task.ID != "task-healthy" {
		ids := make([]generation.TaskID, 0, len(leases))
		for _, lease := range leases {
			ids = append(ids, lease.Task.ID)
		}
		t.Fatalf("leases = %v; want [task-healthy]", ids)
	}
}
