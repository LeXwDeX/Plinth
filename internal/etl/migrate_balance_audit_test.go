package etl_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/etl/etltest"
)

func TestMigrateBalanceSnapshotsPreservesAccountValueAndTime(
	t *testing.T,
) {
	fixture := etltest.NewFixture(t)
	fixture.Exec(t,
		`INSERT INTO balance_snapshots (id, balance, capturedAt) VALUES
		 ('balance-old', 812.75, '2025-02-03T04:05:06.789+00:00'),
		 ('balance-new', 799.5, '2025-02-04T05:06:07+00:00')`,
	)
	targetDB := migrateTarget(t)
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", targetDB,
	)
	if code != 0 {
		t.Fatalf("迁移应成功，退出码 %d:\n%s", code, output)
	}
	if !strings.Contains(
		output,
		"余额快照域: 源 2 行 / 迁移 2 行 / 跳过 0 行",
	) {
		t.Fatalf("余额对账报告缺失:\n%s", output)
	}

	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	accountID := product.Application().Administration.Usage.Balance.AccountID
	repository, err := sqlite.Open(targetDB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	history, err := repository.QueryBalanceSnapshots(
		t.Context(),
		time.Time{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 ||
		history[0].ID != "balance-old" ||
		history[0].AccountID != accountID ||
		history[0].Balance != 812.75 ||
		history[0].CapturedAt.Format(
			"2006-01-02T15:04:05.999Z",
		) != "2025-02-03T04:05:06.789Z" ||
		history[1].ID != "balance-new" ||
		history[1].AccountID != accountID ||
		history[1].Balance != 799.5 {
		t.Fatalf("余额快照字段保真失败: %#v", history)
	}
}

func TestMigrateAuditPreservesFiltersAndMaterializesLinkedFile(
	t *testing.T,
) {
	fixture := etltest.NewFixture(t)
	relativeAuditPath := filepath.Join(
		"alice",
		"upload",
		"2025-02-03",
		"reference.png",
	)
	sourceAuditPath := filepath.Join(
		fixture.Root,
		"storage",
		"audit",
		relativeAuditPath,
	)
	if err := os.MkdirAll(filepath.Dir(sourceAuditPath), 0o750); err != nil {
		t.Fatal(err)
	}
	auditBytes := []byte("\x89PNG\r\n\x1a\naudit-evidence")
	if err := os.WriteFile(sourceAuditPath, auditBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	fixture.Exec(t,
		`INSERT INTO audit_logs (
			id, action, userId, userName, resourceType, resourceId,
			detail, ipAddress, userAgent, createdAt
		 ) VALUES
		 (
			'audit-target', 'upload_file', 'user-a', 'Alice', 'asset',
			'asset-a', ?, '192.0.2.10', 'legacy-browser',
			'2025-02-03T04:05:06.789+00:00'
		 ),
		 (
			'audit-other', 'login', 'user-b', 'Bob', 'session',
			'session-b', '{}', '192.0.2.11', 'legacy-browser',
			'2025-02-05T04:05:06+00:00'
		 )`,
		`{"auditPath":"`+filepath.ToSlash(sourceAuditPath)+
			`","filename":"reference.png"}`,
	)

	targetDB := migrateTarget(t)
	targetStorage := filepath.Join(t.TempDir(), "storage-go")
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", targetDB,
		"--target-storage", targetStorage,
	)
	if code != 0 {
		t.Fatalf("迁移应成功，退出码 %d:\n%s", code, output)
	}
	if !strings.Contains(
		output,
		"审计域: 日志源 2 行 / 迁移 2 行 / 跳过 0 行；"+
			"文件源 1 个 / 迁移 1 个 / 重跑跳过 0 个 / 失败 0 个",
	) {
		t.Fatalf("审计对账报告缺失:\n%s", output)
	}

	from := time.Date(2025, 2, 3, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 2, 4, 0, 0, 0, 0, time.UTC)
	repository, err := sqlite.Open(targetDB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	events, total, err := repository.QueryAdministrationAuditEvents(
		t.Context(),
		administration.AuditQuery{
			Page:     1,
			PageSize: 10,
			UserID:   "user-a",
			Action:   "upload_file",
			From:     &from,
			To:       &to,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	targetAuditPath := filepath.Join(
		targetStorage,
		"audit",
		relativeAuditPath,
	)
	if total != 1 ||
		len(events) != 1 ||
		events[0].ID != "audit-target" ||
		events[0].ActorName != "Alice" ||
		events[0].ResourceType != "asset" ||
		events[0].ResourceID != "asset-a" ||
		events[0].IPAddress != "192.0.2.10" ||
		events[0].UserAgent != "legacy-browser" ||
		events[0].CreatedAt.Format(
			"2006-01-02T15:04:05.999Z",
		) != "2025-02-03T04:05:06.789Z" ||
		events[0].Detail["auditPath"] != targetAuditPath {
		t.Fatalf("审计日志字段或过滤保真失败: %#v", events)
	}
	gotBytes, err := os.ReadFile(targetAuditPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotBytes) != string(auditBytes) {
		t.Fatalf("审计文件字节 = %q，期望 %q", gotBytes, auditBytes)
	}
}

func TestMigrateBalanceAndAuditIsIdempotent(t *testing.T) {
	fixture := etltest.NewFixture(t)
	fixture.Exec(t,
		`INSERT INTO balance_snapshots (id, balance, capturedAt)
		 VALUES ('balance-idempotent', 100, '2025-02-03T04:05:06+00:00')`,
	)
	fixture.Exec(t,
		`INSERT INTO audit_logs (
			id, action, userId, createdAt
		 ) VALUES (
			'audit-idempotent', 'login', 'user-a',
			'2025-02-03T04:05:06+00:00'
		 )`,
	)
	auditPath := filepath.Join(
		fixture.Root,
		"storage",
		"audit",
		"alice",
		"login.txt",
	)
	if err := os.MkdirAll(filepath.Dir(auditPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auditPath, []byte("evidence"), 0o640); err != nil {
		t.Fatal(err)
	}

	targetDB := migrateTarget(t)
	targetStorage := filepath.Join(t.TempDir(), "storage-go")
	var secondOutput string
	for run := 1; run <= 2; run++ {
		code, output := runETL(t,
			"migrate",
			"--legacy-root", fixture.Root,
			"--target-db", targetDB,
			"--target-storage", targetStorage,
		)
		if code != 0 {
			t.Fatalf("第 %d 次迁移失败，退出码 %d:\n%s", run, code, output)
		}
		if run == 2 {
			secondOutput = output
		}
	}
	if !strings.Contains(
		secondOutput,
		"余额快照=1 审计日志=1 审计文件=1",
	) {
		t.Fatalf("重跑跳过量不完整:\n%s", secondOutput)
	}
	db := openTargetDatabase(t, targetDB)
	for table, want := range map[string]int{
		"generation_balance_snapshots": 1,
		"administration_audit_events":  1,
	} {
		var got int
		if err := db.QueryRow(
			"SELECT COUNT(*) FROM " + table,
		).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s 重跑后行数 = %d，期望 %d", table, got, want)
		}
	}
	var files int
	if err := filepath.WalkDir(
		filepath.Join(targetStorage, "audit"),
		func(_ string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				files++
			}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if files != 1 {
		t.Fatalf("审计文件重跑后数量 = %d，期望 1", files)
	}
}
