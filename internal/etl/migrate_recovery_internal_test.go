package etl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/etl/etltest"

	_ "modernc.org/sqlite"
)

func TestMigrateResumesAfterCommittedMiddleBatch(t *testing.T) {
	fixture := etltest.NewFixture(t)
	for index := range 600 {
		id := fmt.Sprintf("resume-user-%03d", index)
		fixture.Exec(t,
			`INSERT INTO users (
				id, username, passwordHash, createdAt, updatedAt
			 ) VALUES (?, ?, 'hash',
				'2025-02-03T04:05:06+00:00',
				'2025-02-03T04:05:06+00:00')`,
			id,
			id,
		)
	}
	layout := resolveLegacyLayout(fixture.Root)
	resumedDB := filepath.Join(t.TempDir(), "resumed.db")
	resumedStorage := filepath.Join(t.TempDir(), "resumed-storage")
	interrupted := errors.New("模拟进程中断")
	ctx := sqlite.WithETLBatchObserver(
		context.Background(),
		func(domain string, committed int) error {
			if domain == "authentication_user" && committed == 250 {
				return interrupted
			}
			return nil
		},
	)
	if _, err := migrateWithContext(
		ctx,
		layout,
		resumedDB,
		resumedStorage,
	); !errors.Is(err, interrupted) {
		t.Fatalf("应在首个已提交批次后中断，实际 %v", err)
	}
	if count := countRowsInDatabase(
		t,
		resumedDB,
		"authentication_users",
	); count != 250 {
		t.Fatalf("中断前应提交 250 个用户，实际 %d", count)
	}

	resumed, err := migrate(layout, resumedDB, resumedStorage)
	if err != nil {
		t.Fatalf("断点续跑失败: %v", err)
	}
	if resumed.failureCount != 0 {
		t.Fatalf("断点续跑对账失败:\n%s", resumed.report)
	}
	if !strings.Contains(resumed.report, "重跑跳过量: 用户=250") {
		t.Fatalf("续跑报告未记录已提交批次:\n%s", resumed.report)
	}

	oneShotDB := filepath.Join(t.TempDir(), "one-shot.db")
	oneShotStorage := filepath.Join(t.TempDir(), "one-shot-storage")
	oneShot, err := migrate(layout, oneShotDB, oneShotStorage)
	if err != nil {
		t.Fatalf("一次性迁移失败: %v", err)
	}
	if oneShot.failureCount != 0 {
		t.Fatalf("一次性迁移对账失败:\n%s", oneShot.report)
	}
	for _, query := range []string{
		`SELECT id, username, password_hash, created_at, updated_at
		 FROM authentication_users ORDER BY id`,
		`SELECT domain, source_id FROM etl_import_items
		 ORDER BY domain, source_id`,
	} {
		resumedDump := dumpDatabaseQuery(t, resumedDB, query)
		oneShotDump := dumpDatabaseQuery(t, oneShotDB, query)
		if resumedDump != oneShotDump {
			t.Fatalf(
				"续跑结果与一次性迁移不等价，查询 %q:\n续跑:\n%s\n一次性:\n%s",
				query,
				resumedDump,
				oneShotDump,
			)
		}
	}
}

func countRowsInDatabase(
	t *testing.T,
	databasePath string,
	table string,
) int {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM " + table,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func dumpDatabaseQuery(
	t *testing.T,
	databasePath string,
	query string,
) string {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result strings.Builder
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&result, "%v\n", values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result.String()
}
