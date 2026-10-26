package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// 本文件覆盖 W4 §4.2 的持久化纪律：CommitDispatchCheckpoint 全谓词 CAS
// （version + lease_token + lease_until>now + 非 tombstone）、opaque_json
// 与 dispatch_checkpoint_json 同事务、普通提交/upsertAttempt 非覆盖、
// 回执结算谓词（重复/错配回执整体回滚）、0021 本地升级与快照恢复证据。

type checkpointFixture struct {
	repository *sqlite.Repository
	path       string
	task       generation.Task
	lease      generation.Lease
	attempt    *generation.Attempt
	now        time.Time
	// clockNow 是租约写事务时钟（M3 后 lease_until 由它派生）；测试叙事
	// 推进合成时间时必须同步推进，语义与墙钟自然流逝一致。
	clockNow time.Time
}

func newCheckpointFixture(t *testing.T) *checkpointFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "checkpoint.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	now := time.Date(2026, 9, 18, 8, 0, 0, 0, time.UTC)
	fixture := &checkpointFixture{now: now, clockNow: now}
	repository.SetClock(func() time.Time { return fixture.clockNow })
	task := generation.Task{
		ID:         "task-dispatch",
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		Offering: generation.Offering{
			ID:         "test/offering",
			ProviderID: "test",
			ModelID:    "test",
			Capability: generation.TextTo3D,
		},
		Input:        json.RawMessage(`{"prompt":"fixture"}`),
		RequestHash:  "hash",
		State:        generation.TaskQueued,
		NextActionAt: now,
		Version:      1,
		CreatedAt:    now,
		UpdatedAt:    now,
		Assets:       []generation.Asset{},
	}
	task = approveCreateFixture(t, repository, task)
	if _, _, err := repository.Create(context.Background(), task, generation.Event{
		TaskID:    task.ID,
		Type:      "task.submitted",
		Data:      json.RawMessage(`{}`),
		CreatedAt: now,
	}, 0); err != nil {
		t.Fatal(err)
	}
	leases, err := repository.ClaimReady(context.Background(), "worker", now, time.Minute, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 || leases[0].Task.ID != task.ID {
		t.Fatalf("leases = %d", len(leases))
	}
	lease := leases[0]
	attempt := &generation.Attempt{
		ID:            "attp-dispatch-1",
		TaskID:        task.ID,
		Ordinal:       1,
		Offering:      task.Offering,
		State:         generation.AttemptSubmitting,
		SubmissionKey: string(task.ID) + ":1:attp-dispatch-1",
		StartedAt:     now,
		UpdatedAt:     now,
	}
	committed := lease.Task
	committed.CurrentAttemptID = attempt.ID
	committed.UpdatedAt = now
	committed.Version++
	if err := repository.Commit(context.Background(), generation.Commit{
		LeaseToken:      lease.Token,
		ExpectedVersion: lease.Task.Version,
		Task:            committed,
		Attempt:         attempt,
	}); err != nil {
		t.Fatal(err)
	}
	lease.Task = committed
	lease.CurrentAttempt = attempt
	fixture.repository = repository
	fixture.path = path
	fixture.task = committed
	fixture.lease = lease
	fixture.attempt = attempt
	return fixture
}

func (f *checkpointFixture) write() generation.DispatchCheckpointWrite {
	return generation.DispatchCheckpointWrite{
		TaskID:          f.task.ID,
		AttemptID:       f.attempt.ID,
		LeaseToken:      f.lease.Token,
		ExpectedVersion: f.lease.Task.Version,
		Now:             f.now,
		Intent:          json.RawMessage(`{"stage":"fixture.create"}`),
		AckToken:        "fixture-ack-token-0000000000000001",
	}
}

func (f *checkpointFixture) commitTaskVersion(t *testing.T) int64 {
	t.Helper()
	aggregate, err := f.repository.Load(context.Background(), f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return aggregate.Task.Version
}

func TestCommitDispatchCheckpointPersistsIntentAndStateAtomically(t *testing.T) {
	fixture := newCheckpointFixture(t)
	ack, err := fixture.repository.CommitDispatchCheckpoint(
		context.Background(),
		fixture.write(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if ack.Sequence != 1 || ack.AckToken != "fixture-ack-token-0000000000000001" {
		t.Fatalf("ack = %#v", ack)
	}
	if ack.NewVersion != fixture.lease.Task.Version+1 {
		t.Fatalf("NewVersion = %d; want %d（CAS 写入的新版本）", ack.NewVersion, fixture.lease.Task.Version+1)
	}
	if version := fixture.commitTaskVersion(t); version != ack.NewVersion {
		t.Fatalf("persisted version = %d; want %d", version, ack.NewVersion)
	}
	aggregate, err := fixture.repository.Load(context.Background(), fixture.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(aggregate.CurrentAttempt.Opaque) != `{"stage":"fixture.create"}` {
		t.Fatalf("opaque = %s; want intent 原样持久化", aggregate.CurrentAttempt.Opaque)
	}
	var state struct {
		Sequence int64  `json:"sequence"`
		State    string `json:"state"`
		Ack      string `json:"ack"`
	}
	if err := json.Unmarshal(
		[]byte(rawCheckpointColumn(t, fixture, fixture.attempt.ID)),
		&state,
	); err != nil {
		t.Fatalf("dispatch_checkpoint_json = %s（%v）", rawCheckpointColumn(t, fixture, fixture.attempt.ID), err)
	}
	if state.Sequence != 1 || state.State != "dispatching" || state.Ack != ack.AckToken {
		t.Fatalf("state = %#v", state)
	}
}

func TestCommitDispatchCheckpointEnforcesFullPredicate(t *testing.T) {
	t.Run("版本不匹配返回冲突且零写入", func(t *testing.T) {
		fixture := newCheckpointFixture(t)
		write := fixture.write()
		write.ExpectedVersion = fixture.lease.Task.Version + 7
		if _, err := fixture.repository.CommitDispatchCheckpoint(context.Background(), write); !errors.Is(err, generation.RepositoryConflict) {
			t.Fatalf("err = %v; want conflict", err)
		}
		if column := rawCheckpointColumn(t, fixture, fixture.attempt.ID); column != "" {
			t.Fatalf("column = %s; want 未写入", column)
		}
	})
	t.Run("租约 token 不匹配返回冲突", func(t *testing.T) {
		fixture := newCheckpointFixture(t)
		write := fixture.write()
		write.LeaseToken = "wrong-token"
		if _, err := fixture.repository.CommitDispatchCheckpoint(context.Background(), write); !errors.Is(err, generation.RepositoryConflict) {
			t.Fatalf("err = %v; want conflict", err)
		}
	})
	t.Run("租约过期返回冲突（lease_until<=now）", func(t *testing.T) {
		fixture := newCheckpointFixture(t)
		write := fixture.write()
		write.Now = fixture.now.Add(2 * time.Minute) // 租约在 +1m 到期
		if _, err := fixture.repository.CommitDispatchCheckpoint(context.Background(), write); !errors.Is(err, generation.RepositoryConflict) {
			t.Fatalf("err = %v; want conflict", err)
		}
	})
	t.Run("任务已删除（tombstone）返回冲突", func(t *testing.T) {
		fixture := newCheckpointFixture(t)
		if err := fixture.repository.Tombstone(context.Background(), generation.Tombstone{
			TaskID:          fixture.task.ID,
			ActorID:         "owner-a",
			ExpectedVersion: fixture.lease.Task.Version,
			DeletedAt:       fixture.now,
			Event: generation.Event{
				TaskID:    fixture.task.ID,
				Type:      "task.deleted",
				Data:      json.RawMessage(`{}`),
				CreatedAt: fixture.now,
			},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.repository.CommitDispatchCheckpoint(
			context.Background(),
			fixture.write(),
		); !errors.Is(err, generation.RepositoryConflict) {
			t.Fatalf("err = %v; want conflict", err)
		}
	})
	t.Run("存在未回执 dispatching 时拒绝新许可", func(t *testing.T) {
		fixture := newCheckpointFixture(t)
		if _, err := fixture.repository.CommitDispatchCheckpoint(
			context.Background(),
			fixture.write(),
		); err != nil {
			t.Fatal(err)
		}
		write := fixture.write()
		write.AckToken = "fixture-ack-token-0000000000000002"
		write.ExpectedVersion = fixture.commitTaskVersion(t)
		if _, err := fixture.repository.CommitDispatchCheckpoint(context.Background(), write); !errors.Is(err, generation.ErrDispatchPending) {
			t.Fatalf("err = %v; want ErrDispatchPending", err)
		}
	})
}

func TestOrdinaryCommitNeverClobbersCheckpointColumn(t *testing.T) {
	fixture := newCheckpointFixture(t)
	if _, err := fixture.repository.CommitDispatchCheckpoint(
		context.Background(),
		fixture.write(),
	); err != nil {
		t.Fatal(err)
	}
	before := rawCheckpointColumn(t, fixture, fixture.attempt.ID)
	version := fixture.commitTaskVersion(t)

	// 普通提交（无 DispatchReceipt）：仅推进任务与 attempt 常规列。
	task := fixture.lease.Task
	task.Version = version + 1
	task.UpdatedAt = fixture.now.Add(time.Second)
	attempt := *fixture.attempt
	attempt.UpdatedAt = task.UpdatedAt
	if err := fixture.repository.Commit(context.Background(), generation.Commit{
		LeaseToken:      fixture.lease.Token,
		ExpectedVersion: version,
		Task:            task,
		Attempt:         &attempt,
		ClearLease:      true,
	}); err != nil {
		t.Fatal(err)
	}
	if after := rawCheckpointColumn(t, fixture, fixture.attempt.ID); after != before {
		t.Fatalf("checkpoint 被普通提交改写: before=%s after=%s", before, after)
	}

	// upsertAttempt 路径（ReplaceClaims）同样不触碰该列。
	task.Version = version + 2
	task.UpdatedAt = fixture.now.Add(2 * time.Second)
	attempt.Opaque = json.RawMessage(`{"stage":"observed"}`)
	if err := fixture.repository.Commit(context.Background(), generation.Commit{
		ExpectedVersion: version + 1,
		Task:            task,
		Attempt:         &attempt,
		ReplaceClaims:   true,
		Claims:          nil,
	}); err != nil {
		t.Fatal(err)
	}
	if after := rawCheckpointColumn(t, fixture, fixture.attempt.ID); after != before {
		t.Fatalf("checkpoint 被 upsertAttempt 改写: before=%s after=%s", before, after)
	}
}

func TestDispatchReceiptSettlementPredicate(t *testing.T) {
	newPending := func(t *testing.T) (*checkpointFixture, generation.DispatchCheckpointAcknowledge) {
		fixture := newCheckpointFixture(t)
		ack, err := fixture.repository.CommitDispatchCheckpoint(
			context.Background(),
			fixture.write(),
		)
		if err != nil {
			t.Fatal(err)
		}
		return fixture, ack
	}
	settledCommit := func(
		fixture *checkpointFixture,
		settlement generation.DispatchReceiptSettlement,
		version int64,
	) error {
		task := fixture.lease.Task
		task.Version = version + 1
		task.UpdatedAt = fixture.now.Add(time.Second)
		attempt := *fixture.attempt
		attempt.UpdatedAt = task.UpdatedAt
		return fixture.repository.Commit(context.Background(), generation.Commit{
			LeaseToken:      fixture.lease.Token,
			ExpectedVersion: version,
			Task:            task,
			Attempt:         &attempt,
			DispatchReceipt: &settlement,
		})
	}

	t.Run("匹配回执结算为 received", func(t *testing.T) {
		fixture, ack := newPending(t)
		version := fixture.commitTaskVersion(t)
		if err := settledCommit(fixture, generation.DispatchReceiptSettlement{
			AttemptID: fixture.attempt.ID,
			Sequence:  ack.Sequence,
			AckToken:  ack.AckToken,
		}, version); err != nil {
			t.Fatal(err)
		}
		if column := rawCheckpointColumn(t, fixture, fixture.attempt.ID); !jsonFieldEquals(column, "state", "received") {
			t.Fatalf("column = %s; want received", column)
		}
	})
	t.Run("重复回执整体回滚且零生命周期突变", func(t *testing.T) {
		fixture, ack := newPending(t)
		version := fixture.commitTaskVersion(t)
		settlement := generation.DispatchReceiptSettlement{
			AttemptID: fixture.attempt.ID,
			Sequence:  ack.Sequence,
			AckToken:  ack.AckToken,
		}
		if err := settledCommit(fixture, settlement, version); err != nil {
			t.Fatal(err)
		}
		versionAfter := fixture.commitTaskVersion(t)
		if err := settledCommit(fixture, settlement, versionAfter); !errors.Is(err, generation.RepositoryConflict) {
			t.Fatalf("replay err = %v; want conflict", err)
		}
		if again := fixture.commitTaskVersion(t); again != versionAfter {
			t.Fatalf("version = %d; want %d（重复回执不得推进任务）", again, versionAfter)
		}
	})
	t.Run("错配 sequence/ack 回执回滚", func(t *testing.T) {
		fixture, ack := newPending(t)
		version := fixture.commitTaskVersion(t)
		for _, settlement := range []generation.DispatchReceiptSettlement{{
			AttemptID: fixture.attempt.ID,
			Sequence:  ack.Sequence + 1,
			AckToken:  ack.AckToken,
		}, {
			AttemptID: fixture.attempt.ID,
			Sequence:  ack.Sequence,
			AckToken:  "forged-ack",
		}} {
			if err := settledCommit(fixture, settlement, version); !errors.Is(err, generation.RepositoryConflict) {
				t.Fatalf("err = %v; want conflict", err)
			}
		}
		if column := rawCheckpointColumn(t, fixture, fixture.attempt.ID); !jsonFieldEquals(column, "state", "dispatching") {
			t.Fatalf("column = %s; want 保持 dispatching", column)
		}
	})
	t.Run("not_dispatched 清回 idle 且 sequence 单调", func(t *testing.T) {
		fixture, ack := newPending(t)
		version := fixture.commitTaskVersion(t)
		if err := settledCommit(fixture, generation.DispatchReceiptSettlement{
			AttemptID:     fixture.attempt.ID,
			Sequence:      ack.Sequence,
			AckToken:      ack.AckToken,
			NotDispatched: true,
		}, version); err != nil {
			t.Fatal(err)
		}
		if column := rawCheckpointColumn(t, fixture, fixture.attempt.ID); !jsonFieldEquals(column, "state", "idle") {
			t.Fatalf("column = %s; want idle", column)
		}
		// idle 后允许新许可，sequence 递增；但需要有效租约——重新领取。
		now := fixture.now.Add(2 * time.Minute)
		fixture.clockNow = now // 合成时间线推进到重领取时刻：新租约 lease_until 由该时刻派生。
		leases, err := fixture.repository.ClaimReady(context.Background(), "worker-2", now, time.Minute, 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(leases) != 1 {
			t.Fatalf("leases = %d", len(leases))
		}
		next, err := fixture.repository.CommitDispatchCheckpoint(context.Background(), generation.DispatchCheckpointWrite{
			TaskID:          fixture.task.ID,
			AttemptID:       fixture.attempt.ID,
			LeaseToken:      leases[0].Token,
			ExpectedVersion: leases[0].Task.Version,
			Now:             now,
			Intent:          json.RawMessage(`{"stage":"fixture.retry"}`),
			AckToken:        "fixture-ack-token-0000000000000002",
		})
		if err != nil {
			t.Fatal(err)
		}
		if next.Sequence != 2 {
			t.Fatalf("sequence = %d; want 2（单调递增）", next.Sequence)
		}
	})
}

// TestDispatchCheckpointMigrationUpgradeAndSnapshotRestore 是 0021 的本地
// 升级 + 快照恢复证据：升级前库（0020 形态，DROP COLUMN + 删除台账行）在
// 重开时重放 migration 21 且既有数据完好；停写快照可整体恢复。
func TestDispatchCheckpointMigrationUpgradeAndSnapshotRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint.db")

	// 1) 新库（0021 已应用）写入任务 + 检查点数据。
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)
	// 合成时间线：租约写事务时钟随测试叙事推进（与 ClaimReady 的 now 参数一致）。
	clockNow := now
	repository.SetClock(func() time.Time { return clockNow })
	task := generation.Task{
		ID:         "task-upgrade",
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		Offering: generation.Offering{
			ID:         "test/offering",
			ProviderID: "test",
			ModelID:    "test",
			Capability: generation.TextTo3D,
		},
		Input:        json.RawMessage(`{"prompt":"upgrade"}`),
		RequestHash:  "hash-upgrade",
		State:        generation.TaskQueued,
		NextActionAt: now,
		Version:      1,
		CreatedAt:    now,
		UpdatedAt:    now,
		Assets:       []generation.Asset{},
	}
	task = approveCreateFixture(t, repository, task)
	if _, _, err := repository.Create(context.Background(), task, generation.Event{
		TaskID:    task.ID,
		Type:      "task.submitted",
		Data:      json.RawMessage(`{}`),
		CreatedAt: now,
	}, 0); err != nil {
		t.Fatal(err)
	}
	leases, err := repository.ClaimReady(context.Background(), "worker", now, time.Minute, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 {
		t.Fatalf("leases = %d", len(leases))
	}
	attempt := &generation.Attempt{
		ID:            "attp-upgrade-1",
		TaskID:        task.ID,
		Ordinal:       1,
		Offering:      task.Offering,
		State:         generation.AttemptSubmitting,
		SubmissionKey: string(task.ID) + ":1:attp-upgrade-1",
		StartedAt:     now,
		UpdatedAt:     now,
	}
	committed := leases[0].Task
	committed.CurrentAttemptID = attempt.ID
	committed.Version++
	committed.UpdatedAt = now
	if err := repository.Commit(context.Background(), generation.Commit{
		LeaseToken:      leases[0].Token,
		ExpectedVersion: leases[0].Task.Version,
		Task:            committed,
		Attempt:         attempt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CommitDispatchCheckpoint(context.Background(), generation.DispatchCheckpointWrite{
		TaskID:          task.ID,
		AttemptID:       attempt.ID,
		LeaseToken:      leases[0].Token,
		ExpectedVersion: committed.Version,
		Now:             now,
		Intent:          json.RawMessage(`{"stage":"upgrade.create"}`),
		AckToken:        "upgrade-ack-token-0000000000000001",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}

	// 2) 停写窗口取得升级前等效快照（备份全部 SQLite 文件）。
	snapshot := path + ".snapshot"
	if err := copySQLiteFiles(path, snapshot); err != nil {
		t.Fatal(err)
	}

	// 3) 降级到 0020 形态（DROP COLUMN + 删除台账行），重开=真实升级路径。
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DELETE FROM go_schema_migrations WHERE version = 21`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`ALTER TABLE generation_attempts DROP COLUMN dispatch_checkpoint_json`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	upgraded.SetClock(func() time.Time { return clockNow })
	// 升级后既有数据完好，检查点栅栏语义可用（pending 拒绝新许可）。
	aggregate, err := upgraded.Load(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.CurrentAttempt == nil ||
		string(aggregate.CurrentAttempt.Opaque) != `{"stage":"upgrade.create"}` {
		t.Fatalf("attempt opaque = %s", aggregate.CurrentAttempt.Opaque)
	}
	// 合成时间线推进到升级后领取时刻：租约写事务时钟与 claim 参数同刻。
	clockNow = now.Add(2 * time.Minute)
	afterUpgrade, err := upgraded.ClaimReady(context.Background(), "worker-3", now.Add(2*time.Minute), time.Minute, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterUpgrade) != 1 {
		t.Fatalf("leases = %d", len(afterUpgrade))
	}
	// 模拟的 0020 形态从未携带检查点数据（列随降级删除），升级后的栅栏
	// 必须可用：首次授予许可，随后 pending 拒绝新许可。
	if _, err := upgraded.CommitDispatchCheckpoint(context.Background(), generation.DispatchCheckpointWrite{
		TaskID:          task.ID,
		AttemptID:       attempt.ID,
		LeaseToken:      afterUpgrade[0].Token,
		ExpectedVersion: afterUpgrade[0].Task.Version,
		Now:             now.Add(2 * time.Minute),
		Intent:          json.RawMessage(`{"stage":"upgrade.retry"}`),
		AckToken:        "upgrade-ack-token-0000000000000002",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := upgraded.CommitDispatchCheckpoint(context.Background(), generation.DispatchCheckpointWrite{
		TaskID:          task.ID,
		AttemptID:       attempt.ID,
		LeaseToken:      afterUpgrade[0].Token,
		ExpectedVersion: afterUpgrade[0].Task.Version + 1,
		Now:             now.Add(2 * time.Minute),
		Intent:          json.RawMessage(`{"stage":"upgrade.retry"}`),
		AckToken:        "upgrade-ack-token-0000000000000003",
	}); !errors.Is(err, generation.ErrDispatchPending) {
		t.Fatalf("err = %v; want ErrDispatchPending（升级后 pending 栅栏生效）", err)
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}

	// 4) 快照恢复：拷回快照文件后重开，状态与快照一致。
	if err := copySQLiteFiles(snapshot, path); err != nil {
		t.Fatal(err)
	}
	restored, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	restoredAggregate, err := restored.Load(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restoredAggregate.CurrentAttempt == nil ||
		string(restoredAggregate.CurrentAttempt.Opaque) != `{"stage":"upgrade.create"}` {
		t.Fatalf("restored opaque = %s", restoredAggregate.CurrentAttempt.Opaque)
	}
}

func copySQLiteFiles(source string, target string) error {
	if err := copyFile(source, target); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := copyFileIfExists(source+suffix, target+suffix); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(source string, target string) error {
	content, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(target, content, 0o600)
}

func copyFileIfExists(source string, target string) error {
	content, err := os.ReadFile(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.WriteFile(target, content, 0o600)
}

func jsonFieldEquals(raw string, key string, want string) bool {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return false
	}
	value, ok := decoded[key].(string)
	return ok && value == want
}

// rawCheckpointColumn 以独立只读连接直读检查点列（WAL 允许多连接）。
func rawCheckpointColumn(t *testing.T, fixture *checkpointFixture, attemptID generation.AttemptID) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+fixture.path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value sql.NullString
	if err := db.QueryRow(
		`SELECT dispatch_checkpoint_json FROM generation_attempts WHERE id = ?`,
		attemptID,
	).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if !value.Valid {
		return ""
	}
	return value.String
}
