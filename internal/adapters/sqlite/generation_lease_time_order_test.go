package sqlite_test

// generation_lease_time_order_test.go 锁定任务就绪/租约/排序的纳秒语义。
// RFC3339Nano 会裁掉小数部分末尾的零，SQLite TEXT 比较因此不是时间序：
// "...52.588Z" 在 bytes 上大于 "...52.5881Z"，尽管前者更早。这里用旧格式
// 种子（先删除 0061 台账再重开，触发升级）覆盖 1ns 前/相等/之后、整秒、
// 不同小数位宽、过期/有效租约、Commit/Dispatch/Renew 守卫与分页游标。

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

const taskTimeLayout = "2006-01-02T15:04:05.000000000Z"

func taskStamp(value time.Time) string {
	return value.UTC().Format(taskTimeLayout)
}

// openLeaseTimeDatabase 先把 schema 建到当前头，再删除 0061 台账并写入
// 种子，使下一次 Open 必然重跑时间归一化；旧二进制上没有 0061，旧值原样
// 保留，测试因此能在旧运行时上失败。
func openLeaseTimeDatabase(t *testing.T, seed func(*sql.DB)) (*sqlite.Repository, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lease-time-order.db")
	initial, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DELETE FROM go_schema_migrations WHERE version = 61`); err != nil {
		t.Fatal(err)
	}
	if seed != nil {
		seed(raw)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	return repository, path
}

type leaseTimeRow struct {
	id         generation.TaskID
	state      generation.TaskState
	nextAction string
	leaseToken string
	leaseUntil string
	createdAt  string
	updatedAt  string
}

func nullableLeaseTime(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func insertLeaseTimeRows(t *testing.T, db *sql.DB, rows []leaseTimeRow) {
	t.Helper()
	for _, row := range rows {
		state := row.state
		if state == "" {
			state = generation.TaskQueued
		}
		updatedAt := row.updatedAt
		if updatedAt == "" {
			updatedAt = row.createdAt
		}
		var leaseOwner any
		if row.leaseToken != "" {
			leaseOwner = "worker-a"
		}
		if _, err := db.Exec(`
			INSERT INTO generation_tasks (
				id, owner_id, capability, offering_json, input_json, request_hash,
				state, progress, materialize_failures, next_action_at, version,
				lease_owner, lease_token, lease_until, created_at, updated_at
			) VALUES (?, 'owner-a', ?, '{"id":"test/offering"}', '{}', 'hash',
				?, 0, 0, ?, 1, ?, ?, ?, ?, ?)`,
			row.id,
			generation.TextTo3D,
			state,
			nullableLeaseTime(row.nextAction),
			leaseOwner,
			nullableLeaseTime(row.leaseToken),
			nullableLeaseTime(row.leaseUntil),
			row.createdAt,
			updatedAt,
		); err != nil {
			t.Fatalf("insert task %s: %v", row.id, err)
		}
	}
}

func claimedIDs(leases []generation.Lease) []generation.TaskID {
	ids := make([]generation.TaskID, 0, len(leases))
	for _, lease := range leases {
		ids = append(ids, lease.Task.ID)
	}
	return ids
}

func TestClaimReadyOrdersVariableFractionTimestampsByInstant(t *testing.T) {
	now := time.Date(2026, 10, 21, 14, 6, 52, 588100000, time.UTC)
	repository, _ := openLeaseTimeDatabase(t, func(db *sql.DB) {
		insertLeaseTimeRows(t, db, []leaseTimeRow{
			{id: "task-whole", nextAction: "2026-10-21T14:06:52Z", createdAt: "2026-10-21T14:00:00Z"},
			{id: "task-prefix", nextAction: "2026-10-21T14:06:52.588Z", createdAt: "2026-10-21T14:00:01Z"},
			{id: "task-equal", nextAction: "2026-10-21T14:06:52.5881Z", createdAt: "2026-10-21T14:00:02Z"},
			{id: "task-after-1ns", nextAction: "2026-10-21T14:06:52.588100001Z", createdAt: "2026-10-21T14:00:03Z"},
			{id: "task-next-null", createdAt: "2026-10-21T14:00:04Z"},
		})
	})

	leases, err := repository.ClaimReady(context.Background(), "worker-a", now, time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []generation.TaskID{"task-whole", "task-prefix", "task-equal"}
	got := claimedIDs(leases)
	if len(got) != len(want) {
		t.Fatalf("claimed = %v; want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("claimed = %v; want %v", got, want)
		}
	}
}

func TestClaimReadyKeepsLeaseExpiryStrictAtNanoseconds(t *testing.T) {
	now := time.Date(2026, 10, 21, 14, 6, 52, 588100000, time.UTC)
	repository, _ := openLeaseTimeDatabase(t, func(db *sql.DB) {
		insertLeaseTimeRows(t, db, []leaseTimeRow{
			{id: "task-expired-prefix", nextAction: "2026-10-21T14:05:00Z", leaseToken: "token-a", leaseUntil: "2026-10-21T14:06:52.588Z", createdAt: "2026-10-21T14:00:00Z"},
			{id: "task-expired-1ns", nextAction: "2026-10-21T14:05:00Z", leaseToken: "token-b", leaseUntil: "2026-10-21T14:06:52.588099999Z", createdAt: "2026-10-21T14:00:01Z"},
			{id: "task-equal", nextAction: "2026-10-21T14:05:00Z", leaseToken: "token-c", leaseUntil: "2026-10-21T14:06:52.5881Z", createdAt: "2026-10-21T14:00:02Z"},
			{id: "task-live-1ns", nextAction: "2026-10-21T14:05:00Z", leaseToken: "token-d", leaseUntil: "2026-10-21T14:06:52.588100001Z", createdAt: "2026-10-21T14:00:03Z"},
		})
	})

	leases, err := repository.ClaimReady(context.Background(), "worker-b", now, time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []generation.TaskID{"task-expired-prefix", "task-expired-1ns"}
	got := claimedIDs(leases)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("claimed = %v; want %v", got, want)
	}
}

func TestCommitLeaseGuardKeepsStrictNanosecondBoundary(t *testing.T) {
	now := time.Date(2026, 10, 21, 14, 6, 52, 588100000, time.UTC)
	repository, _ := openLeaseTimeDatabase(t, func(db *sql.DB) {
		insertLeaseTimeRows(t, db, []leaseTimeRow{
			{id: "task-expired-prefix", state: generation.TaskRunning, leaseToken: "token-a", leaseUntil: "2026-10-21T14:06:52.588Z", createdAt: "2026-10-21T14:00:00Z"},
			{id: "task-expired-1ns", state: generation.TaskRunning, leaseToken: "token-b", leaseUntil: "2026-10-21T14:06:52.588099999Z", createdAt: "2026-10-21T14:00:01Z"},
			{id: "task-equal", state: generation.TaskRunning, leaseToken: "token-c", leaseUntil: "2026-10-21T14:06:52.5881Z", createdAt: "2026-10-21T14:00:02Z"},
			{id: "task-live-1ns", state: generation.TaskRunning, leaseToken: "token-d", leaseUntil: "2026-10-21T14:06:52.588100001Z", createdAt: "2026-10-21T14:00:03Z"},
			{id: "task-live-whole", state: generation.TaskRunning, leaseToken: "token-e", leaseUntil: "2026-10-21T14:06:53Z", createdAt: "2026-10-21T14:00:04Z"},
		})
	})

	for _, tc := range []struct {
		id     generation.TaskID
		token  string
		status string
	}{
		{"task-expired-prefix", "token-a", "conflict"},
		{"task-expired-1ns", "token-b", "conflict"},
		{"task-equal", "token-c", "conflict"},
		{"task-live-1ns", "token-d", "ok"},
		{"task-live-whole", "token-e", "ok"},
	} {
		t.Run(string(tc.id), func(t *testing.T) {
			err := repository.Commit(context.Background(), generation.Commit{
				LeaseToken:      tc.token,
				ExpectedVersion: 1,
				Task: generation.Task{
					ID:          tc.id,
					OwnerID:     "owner-a",
					Capability:  generation.TextTo3D,
					Offering:    generation.Offering{ID: "test/offering"},
					Input:       []byte(`{}`),
					RequestHash: "hash",
					State:       generation.TaskRunning,
					Version:     2,
					UpdatedAt:   now,
				},
			})
			switch tc.status {
			case "conflict":
				if !errors.Is(err, generation.RepositoryConflict) {
					t.Fatalf("err = %v; want conflict", err)
				}
			case "ok":
				if err != nil {
					t.Fatalf("commit: %v", err)
				}
			}
		})
	}
}

func TestCommitDispatchCheckpointLeaseGuardAtNanoseconds(t *testing.T) {
	now := time.Date(2026, 9, 18, 8, 0, 0, 588100000, time.UTC)
	for _, tc := range []struct {
		name       string
		leaseUntil string
		status     string
	}{
		{"expired-prefix", "2026-09-18T08:00:00.588Z", "conflict"},
		{"equal", "2026-09-18T08:00:00.5881Z", "conflict"},
		{"live-1ns", "2026-09-18T08:00:00.588100001Z", "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newCheckpointFixture(t)
			raw, err := sql.Open("sqlite", "file:"+fixture.path)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			if _, err := raw.Exec(`UPDATE generation_tasks SET lease_until = ? WHERE id = ?`, tc.leaseUntil, fixture.task.ID); err != nil {
				t.Fatal(err)
			}
			// Reopen through the upgrade path so the legacy text is normalized
			// exactly as it is on a deployed database.
			if err := fixture.repository.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := raw.Exec(`DELETE FROM go_schema_migrations WHERE version = 61`); err != nil {
				t.Fatal(err)
			}
			if err := raw.Close(); err != nil {
				t.Fatal(err)
			}
			fixture.repository, err = sqlite.Open(fixture.path)
			if err != nil {
				t.Fatal(err)
			}
			write := fixture.write()
			write.Now = now
			_, err = fixture.repository.CommitDispatchCheckpoint(context.Background(), write)
			switch tc.status {
			case "conflict":
				if !errors.Is(err, generation.RepositoryConflict) {
					t.Fatalf("err = %v; want conflict", err)
				}
			case "ok":
				if err != nil {
					t.Fatalf("checkpoint: %v", err)
				}
			}
		})
	}
}

func TestRenewLeaseKeepsStrictNanosecondBoundary(t *testing.T) {
	now := time.Date(2026, 10, 21, 14, 6, 52, 588100000, time.UTC)
	repository, path := openLeaseTimeDatabase(t, func(db *sql.DB) {
		insertLeaseTimeRows(t, db, []leaseTimeRow{
			{id: "task-expired-prefix", state: generation.TaskRunning, leaseToken: "token-a", leaseUntil: "2026-10-21T14:06:52.588Z", createdAt: "2026-10-21T14:00:00Z"},
			{id: "task-equal", state: generation.TaskRunning, leaseToken: "token-b", leaseUntil: "2026-10-21T14:06:52.5881Z", createdAt: "2026-10-21T14:00:01Z"},
			{id: "task-live-1ns", state: generation.TaskRunning, leaseToken: "token-c", leaseUntil: "2026-10-21T14:06:52.588100001Z", createdAt: "2026-10-21T14:00:02Z"},
		})
	})
	repository.SetClock(func() time.Time { return now })

	for _, tc := range []struct {
		id     generation.TaskID
		token  string
		status string
	}{
		{"task-expired-prefix", "token-a", "conflict"},
		{"task-equal", "token-b", "conflict"},
		{"task-live-1ns", "token-c", "ok"},
	} {
		t.Run(string(tc.id), func(t *testing.T) {
			err := repository.RenewLease(context.Background(), tc.id, tc.token, now, time.Minute)
			switch tc.status {
			case "conflict":
				if !errors.Is(err, generation.RepositoryConflict) {
					t.Fatalf("err = %v; want conflict", err)
				}
			case "ok":
				if err != nil {
					t.Fatalf("renew: %v", err)
				}
			}
		})
	}

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var stored string
	if err := raw.QueryRow(`SELECT lease_until FROM generation_tasks WHERE id = 'task-live-1ns'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if want := taskStamp(now.Add(time.Minute)); stored != want {
		t.Fatalf("lease_until = %q; want canonical %q", stored, want)
	}
}

func TestMigration0061NormalizesTaskScheduleTimesIdempotently(t *testing.T) {
	repository, path := openLeaseTimeDatabase(t, func(db *sql.DB) {
		insertLeaseTimeRows(t, db, []leaseTimeRow{
			{
				id:         "task-legacy",
				state:      generation.TaskRunning,
				nextAction: "2026-10-21T14:06:52.588Z",
				leaseToken: "token-a",
				leaseUntil: "2026-10-21T14:06:53Z",
				createdAt:  "2026-10-21T14:06:54.5881Z",
			},
			{
				id:        "task-null",
				createdAt: "2026-10-21T14:06:55Z",
			},
			{
				id:         "task-unrecognized",
				nextAction: "not-a-time",
				leaseToken: "token-b",
				leaseUntil: "2026-10-21T14:06:52+00:00",
				createdAt:  "2026-10-21T14:06:52.5Z",
			},
		})
		if _, err := db.Exec(`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES ('session','owner-a','Owner','frozen','2026-10-21T14:00:00Z','2026-10-21T14:00:00Z')`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`
			INSERT INTO generation_tasks (
				id, owner_id, session_id, capability, offering_json, input_json, request_hash,
				state, progress, materialize_failures, next_action_at, version,
				lease_owner, lease_token, lease_until, created_at, updated_at
			) VALUES ('task-frozen', 'owner-a', 'session', ?, '{"id":"test/offering"}', '{}', 'hash',
				'FAILED', 0, 0, '2026-10-21T14:06:52.588Z', 1,
				'worker-a', 'token-frozen', '2026-10-21T14:06:53Z',
				'2026-10-21T14:00:00Z', '2026-10-21T14:00:00Z')`,
			generation.TextTo3D,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO generation_attempts(id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at) VALUES ('attempt-frozen','task-frozen',1,'{"id":"test/offering"}','FAILED','once','2026-10-21T14:00:00Z','2026-10-21T14:00:00Z')`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO generation_recovery_receipts(id,owner_id,session_id,source_task_id,source_attempt_id,digest,payload_json,created_at) VALUES ('receipt-frozen','owner-a','session','task-frozen','attempt-frozen','digest-frozen','{}','2026-10-21T14:00:00Z')`); err != nil {
			t.Fatal(err)
		}
	})
	if head := sqlite.MigrationHead(); head < 61 {
		t.Fatalf("migration head = %d; want >= 61", head)
	}
	_ = repository

	readTimes := func(t *testing.T) (next, lease, created sql.NullString, nullNext, nullLease sql.NullString) {
		t.Helper()
		raw, err := sql.Open("sqlite", "file:"+path)
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		if err := raw.QueryRow(`SELECT next_action_at, lease_until, created_at FROM generation_tasks WHERE id = 'task-legacy'`).Scan(&next, &lease, &created); err != nil {
			t.Fatal(err)
		}
		if err := raw.QueryRow(`SELECT next_action_at, lease_until FROM generation_tasks WHERE id = 'task-null'`).Scan(&nullNext, &nullLease); err != nil {
			t.Fatal(err)
		}
		return next, lease, created, nullNext, nullLease
	}

	next, lease, created, nullNext, nullLease := readTimes(t)
	if next.String != "2026-10-21T14:06:52.588000000Z" {
		t.Fatalf("next_action_at = %q", next.String)
	}
	if lease.String != "2026-10-21T14:06:53.000000000Z" {
		t.Fatalf("lease_until = %q", lease.String)
	}
	if created.String != "2026-10-21T14:06:54.5881Z" {
		t.Fatalf("created_at rewritten: %q", created.String)
	}
	if nullNext.Valid || nullLease.Valid {
		t.Fatalf("NULL schedule columns changed: next=%v lease=%v", nullNext, nullLease)
	}

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	var unrecognizedNext, unrecognizedLease string
	if err := raw.QueryRow(`SELECT next_action_at, lease_until FROM generation_tasks WHERE id = 'task-unrecognized'`).Scan(&unrecognizedNext, &unrecognizedLease); err != nil {
		t.Fatal(err)
	}
	var frozenNext, frozenLease string
	if err := raw.QueryRow(`SELECT next_action_at, lease_until FROM generation_tasks WHERE id = 'task-frozen'`).Scan(&frozenNext, &frozenLease); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	if unrecognizedNext != "not-a-time" || unrecognizedLease != "2026-10-21T14:06:52+00:00" {
		t.Fatalf("unrecognized values rewritten: next=%q lease=%q", unrecognizedNext, unrecognizedLease)
	}
	if frozenNext != "2026-10-21T14:06:52.588Z" || frozenLease != "2026-10-21T14:06:53Z" {
		t.Fatalf("recovery-bound row rewritten: next=%q lease=%q", frozenNext, frozenLease)
	}

	// Closing and opening again must not change normalized rows (idempotent).
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	next, lease, created, _, _ = readTimes(t)
	if next.String != "2026-10-21T14:06:52.588000000Z" ||
		lease.String != "2026-10-21T14:06:53.000000000Z" ||
		created.String != "2026-10-21T14:06:54.5881Z" {
		t.Fatalf("second open changed rows: next=%q lease=%q created=%q", next.String, lease.String, created.String)
	}
}

// created_at stays RFC3339Nano (frozen recovery digests), so the tie-break
// compares it without the trailing Z; a shorter fraction must still sort by
// instant. The stored values are equal-prefix but different widths.
func TestClaimReadyTieBreakOrdersVariableFractionCreatedAtByInstant(t *testing.T) {
	now := time.Date(2026, 10, 21, 14, 6, 52, 588100000, time.UTC)
	repository, _ := openLeaseTimeDatabase(t, func(db *sql.DB) {
		insertLeaseTimeRows(t, db, []leaseTimeRow{
			{id: "task-created-first", nextAction: "2026-10-21T14:05:00Z", createdAt: "2026-10-21T14:00:00.588Z"},
			{id: "task-created-second", nextAction: "2026-10-21T14:05:00Z", createdAt: "2026-10-21T14:00:00.5881Z"},
		})
	})

	leases, err := repository.ClaimReady(context.Background(), "worker-a", now, time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []generation.TaskID{"task-created-first", "task-created-second"}
	got := claimedIDs(leases)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("claimed = %v; want %v", got, want)
	}
}

// TestMigration0061SQLPadsEveryRFC3339NanoShapeAndRerunsUnchanged runs the
// migration text itself twice on a bare table: every fraction width that
// RFC3339Nano can emit becomes nine digits, anything else stays as stored,
// and the second run changes nothing.
func TestMigration0061SQLPadsEveryRFC3339NanoShapeAndRerunsUnchanged(t *testing.T) {
	migration, err := os.ReadFile("migrations/0061_generation_task_time_order.sql")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "shapes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE generation_tasks (id TEXT PRIMARY KEY, next_action_at TEXT, lease_until TEXT);
		CREATE TABLE generation_recovery_receipts (source_task_id TEXT NOT NULL);`); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ stored, want string }{
		{"2026-10-21T14:06:52Z", "2026-10-21T14:06:52.000000000Z"},
		{"2026-10-21T14:06:52.5Z", "2026-10-21T14:06:52.500000000Z"},
		{"2026-10-21T14:06:52.588Z", "2026-10-21T14:06:52.588000000Z"},
		{"2026-10-21T14:06:52.58810001Z", "2026-10-21T14:06:52.588100010Z"},
		{"2026-10-21T14:06:52.588100001Z", "2026-10-21T14:06:52.588100001Z"},
		{"2026-10-21T14:06:52.5a8Z", "2026-10-21T14:06:52.5a8Z"},
		{"2026-10-21T14:06:52.5+08:00", "2026-10-21T14:06:52.5+08:00"},
		{"2026-10-21T14:06:52+08:00", "2026-10-21T14:06:52+08:00"},
		{"2026-10-21 14:06:52.5Z", "2026-10-21 14:06:52.5Z"},
		{"2026-10-21T14:06:52.5z", "2026-10-21T14:06:52.5z"},
	}
	for i, c := range cases {
		if _, err := db.Exec(`INSERT INTO generation_tasks VALUES (?, ?, ?)`, i, c.stored, c.stored); err != nil {
			t.Fatal(err)
		}
	}
	for run := 1; run <= 2; run++ {
		if _, err := db.Exec(string(migration)); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		for i, c := range cases {
			var next, lease string
			if err := db.QueryRow(`SELECT next_action_at, lease_until FROM generation_tasks WHERE id = ?`, i).Scan(&next, &lease); err != nil {
				t.Fatal(err)
			}
			if next != c.want || lease != c.want {
				t.Fatalf("run %d, %q: next=%q lease=%q; want %q", run, c.stored, next, lease, c.want)
			}
		}
	}
}
