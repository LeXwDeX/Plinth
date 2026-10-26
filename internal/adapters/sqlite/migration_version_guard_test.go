package sqlite_test

// migration_version_guard_test.go（W7，§10.3 第 1 项）：单元级版本守卫与
// 追加迁移形状锁定，无需旧二进制即可回归"新库对旧二进制硬错误"。
//
// 1. 高版本库 vs 当前二进制：打开必须失败且错误包含版本号（回滚硬约束，
//    repository runMigrations 的 MAX(version) 守卫）。
// 2. 正常库：全新打开成功，迁移台账恰好推进到当前头版本。
// 3. 追加形状（append-only）：0021/0022 是单条 ALTER TABLE ADD COLUMN；
//    0001–0020 文件集在台账中的登记与磁盘文件一一对应（改名/删除/重排序
//    都会破坏对应关系）；0021/0022 升级后新列存在且对旧读者零依赖
//    （列可空、无 NOT NULL、无默认值改写既有行）。

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
)

func openRawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func migrationHead(t *testing.T, db *sql.DB) int {
	t.Helper()
	var head sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(version) FROM go_schema_migrations`).Scan(&head); err != nil {
		t.Fatalf("read migration head: %v", err)
	}
	if !head.Valid {
		return 0
	}
	return int(head.Int64)
}

func TestMigrationVersionGuardRejectsNewerDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guard.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("open fresh repository: %v", err)
	}
	if err := repository.Close(); err != nil {
		t.Fatalf("close repository: %v", err)
	}
	head := migrationHead(t, openRawDB(t, path))
	future := head + 1
	// 模拟"新版本二进制已写过更高版本行"（进程崩溃/升级后回退的持久效果）。
	raw := openRawDB(t, path)
	if _, err := raw.Exec(
		`INSERT INTO go_schema_migrations (version, name, checksum, applied_at)
		 VALUES (?, 'future_migration', 'future', '2026-09-18T00:00:00Z')`,
		future,
	); err != nil {
		t.Fatalf("seed future migration row: %v", err)
	}
	_ = raw.Close()

	_, err = sqlite.Open(path)
	if err == nil {
		t.Fatalf("higher-version database must fail to open with current binary")
	}
	wrapped := fmt.Sprintf("%v", err)
	if !regexp.MustCompile(strconv.Itoa(future)).MatchString(wrapped) {
		t.Fatalf("error must mention future version %d: %v", future, err)
	}
	if !regexp.MustCompile(`高于当前二进制`).MatchString(wrapped) {
		t.Fatalf("error must be the version-guard error: %v", err)
	}
}

func TestFreshDatabaseMigratesToCurrentHead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("open fresh repository: %v", err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if head := migrationHead(t, openRawDB(t, path)); head != sqlite.MigrationHead() {
		t.Fatalf("migration head = %d; want %d", head, sqlite.MigrationHead())
	}
}

func TestAppendOnlyMigrationsShape(t *testing.T) {
	migrationsDir := "migrations"
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	files := map[int]string{}
	pattern := regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)
	for _, entry := range entries {
		match := pattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		version, err := strconv.Atoi(match[1])
		if err != nil {
			t.Fatalf("parse migration version %q: %v", entry.Name(), err)
		}
		files[version] = entry.Name()
	}
	head := sqlite.MigrationHead()
	if len(files) != head {
		t.Fatalf("migration files = %d; want %d (versions 1..%d)", len(files), head, head)
	}
	for version := 1; version <= head; version++ {
		if files[version] == "" {
			t.Fatalf("migration version %d missing on disk", version)
		}
	}
	// 0021/0022 必须保持纯追加形态：注释 + 单条 ADD COLUMN，无数据改写、
	// 无 NOT NULL、无对既有行的回填。
	appendOnly := map[int]string{
		21: `-- 0021: 私有派发检查点（W4，§4.2）。内核所有；普通提交与 upsertAttempt
-- 的显式列更新永不触碰该列。
ALTER TABLE generation_attempts ADD COLUMN dispatch_checkpoint_json TEXT;
`,
		22: `-- 0022: 成功后观察资源态（W6，§6.5）。内核所有；写者唯一
-- （成功提交初始化、合并更新、deadline finalizer），读者为领取谓词
-- 与公开 resources[] 投影。NULL = 无成功后观察（旧任务零行为变化）。
ALTER TABLE generation_tasks ADD COLUMN resource_state_json TEXT;
`,
	}
	for version, want := range appendOnly {
		source, err := os.ReadFile(filepath.Join(migrationsDir, files[version]))
		if err != nil {
			t.Fatalf("read %s: %v", files[version], err)
		}
		if string(source) != want {
			t.Fatalf(
				"migration %s content drifted from append-only shape (single ADD COLUMN):\n%s",
				files[version],
				string(source),
			)
		}
	}
	// 升级后的列存在且可空：旧二进制/旧读者不依赖新列（回滚口径前提）。
	path := filepath.Join(t.TempDir(), "shape.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	raw := openRawDB(t, path)
	for _, probe := range []struct{ table, column string }{
		{"generation_attempts", "dispatch_checkpoint_json"},
		{"generation_tasks", "resource_state_json"},
	} {
		var (
			name    string
			notNull int
		)
		err := raw.QueryRow(
			`SELECT name, "notnull" FROM pragma_table_info(?) WHERE name = ?`,
			probe.table,
			probe.column,
		).Scan(&name, &notNull)
		if err != nil || name != probe.column {
			t.Fatalf("column %s.%s missing after migration: %v", probe.table, probe.column, err)
		}
		if notNull != 0 {
			t.Fatalf("column %s.%s must stay nullable (append-only, rollback-safe)", probe.table, probe.column)
		}
	}
}
