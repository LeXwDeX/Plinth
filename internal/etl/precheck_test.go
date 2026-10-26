package etl_test

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/etl"
	"github.com/LeXwDeX/Plinth/internal/etl/etltest"
	_ "modernc.org/sqlite"
)

// buildLegacyFixture 按旧 Next 生产布局构造旧项目根目录并播种
// 预检测试所需的最小数据（fixture 骨架见 etltest 包）。
func buildLegacyFixture(t *testing.T) string {
	t.Helper()
	fixture := etltest.NewFixture(t)
	fixture.Exec(t,
		`INSERT INTO users (id, username, passwordHash, updatedAt)
		 VALUES ('user-1', 'alice', 'hash-1', '2026-07-01T00:00:00Z'),
		        ('user-2', 'bob', 'hash-2', '2026-07-02T00:00:00Z')`,
	)
	fixture.Exec(t,
		`INSERT INTO task_cache (taskId, taskType, status, data, updatedAt)
		 VALUES ('task-1', 'text-to-image', 'SUCCEEDED', '{}', '2026-07-01T00:00:00Z'),
		        ('task-2', 'image-to-3d', 'FAILED', '{}', '2026-07-02T00:00:00Z'),
		        ('task-3', 'text-to-3d', 'PENDING', '{}', '2026-07-03T00:00:00Z')`,
	)
	if err := os.WriteFile(
		filepath.Join(fixture.Root, "storage", "assets", "upload.png"),
		[]byte("legacy-upload-bytes"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	return fixture.Root
}

func runETL(t *testing.T, args ...string) (code int, output string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code = etl.Main(args, &stdout, &stderr)
	return code, stdout.String() + stderr.String()
}

func TestPrecheckPassesOnValidLegacyLayout(t *testing.T) {
	root := buildLegacyFixture(t)
	code, output := runETL(t, "precheck", "--legacy-root", root)
	if code != 0 {
		t.Fatalf("预检应通过，退出码 %d，输出:\n%s", code, output)
	}
	for _, expected := range []string{
		"预检通过",
		"users: 2 行",
		"task_cache: 3 行",
		"canvas_nodes: 0 行",
		"assets: 1 个文件",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("输出缺少 %q:\n%s", expected, output)
		}
	}
}

func TestPrecheckAcceptsExplicitDatabasePath(t *testing.T) {
	root := buildLegacyFixture(t)
	defaultDatabase := filepath.Join(root, "storage", "data", "prod.db")
	externalDatabase := filepath.Join(root, "docker-data", "db", "prod.db")
	if err := os.MkdirAll(filepath.Dir(externalDatabase), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(defaultDatabase, externalDatabase); err != nil {
		t.Fatal(err)
	}

	code, output := runETL(
		t,
		"precheck",
		"--legacy-root", root,
		"--legacy-database", externalDatabase,
	)
	if code != 0 {
		t.Fatalf("显式数据库路径应通过预检，退出码 %d，输出:\n%s", code, output)
	}
	if !strings.Contains(output, "users: 2 行") {
		t.Fatalf("预检未读取显式数据库:\n%s", output)
	}
}

func TestPrecheckRejectsMissingTable(t *testing.T) {
	root := buildLegacyFixture(t)
	db, err := sql.Open(
		"sqlite",
		filepath.Join(root, "storage", "data", "prod.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE canvas_nodes`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	code, output := runETL(t, "precheck", "--legacy-root", root)
	if code == 0 {
		t.Fatalf("缺表应拒绝执行，输出:\n%s", output)
	}
	if !strings.Contains(output, "canvas_nodes") {
		t.Errorf("失败明细应指出缺失表 canvas_nodes:\n%s", output)
	}
	if !strings.Contains(output, "预检未通过") {
		t.Errorf("应输出预检未通过结论:\n%s", output)
	}
}

func TestPrecheckRejectsMissingStorageDirectory(t *testing.T) {
	root := buildLegacyFixture(t)
	if err := os.RemoveAll(
		filepath.Join(root, "storage", "thumbnails"),
	); err != nil {
		t.Fatal(err)
	}
	code, output := runETL(t, "precheck", "--legacy-root", root)
	if code == 0 {
		t.Fatalf("缺目录应拒绝执行，输出:\n%s", output)
	}
	if !strings.Contains(output, "thumbnails") {
		t.Errorf("失败明细应指出缺失目录 thumbnails:\n%s", output)
	}
}

func TestPrecheckRejectsUnreadableDatabase(t *testing.T) {
	root := buildLegacyFixture(t)
	if err := os.WriteFile(
		filepath.Join(root, "storage", "data", "prod.db"),
		[]byte("这不是一个 SQLite 数据库"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	code, output := runETL(t, "precheck", "--legacy-root", root)
	if code == 0 {
		t.Fatalf("库文件不可读应拒绝执行，输出:\n%s", output)
	}
	if !strings.Contains(output, "旧库") {
		t.Errorf("失败明细应指出旧库不可读:\n%s", output)
	}
}

func TestMainRequiresLegacyRoot(t *testing.T) {
	code, output := runETL(t, "precheck")
	if code == 0 {
		t.Fatalf("缺少 --legacy-root 不得执行，输出:\n%s", output)
	}
	if !strings.Contains(output, "legacy-root") ||
		!strings.Contains(output, "用法") {
		t.Errorf("应提示用法与缺失参数:\n%s", output)
	}
}

func TestMainRequiresSubcommand(t *testing.T) {
	code, output := runETL(t)
	if code == 0 {
		t.Fatal("无子命令不得执行")
	}
	if !strings.Contains(output, "用法") {
		t.Errorf("应输出用法说明:\n%s", output)
	}
}
