package etl_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/etl/etltest"
)

// legacyPasswordHash 复用 etltest 共享的旧库播种测试哈希。
const legacyPasswordHash = etltest.PasswordHash

// seedLegacyUsers 播种覆盖全部映射难点的旧用户：
//   - admin：adapter ISO 格式时间（+00:00）+ 毫秒整数 lastLogin
//   - SSO 用户：username 为 NULL（回退 openId）、逗号分隔 roles、
//     CURRENT_TIMESTAMP 空格格式时间、空密码哈希
//   - disabled：已停用
//   - ghost：已软删除
func seedLegacyUsers(t *testing.T, fixture *etltest.Fixture) {
	t.Helper()
	fixture.Exec(t,
		`INSERT INTO users (
			id, openId, username, passwordHash, nickname, email, name,
			roles, isAdmin, isActive, createdAt, updatedAt, deletedAt,
			lastLogin
		 ) VALUES
		 ('legacy-admin-id', NULL, 'admin', ?, '管理员',
		  'admin@example.test', 'Admin', '', 1, 1,
		  '2025-01-02T03:04:05.678+00:00', '2025-01-02T03:04:05.678+00:00',
		  NULL, 1750000000000),
		 ('legacy-sso-id', 'sso-open-1', NULL, '', '美术甲',
		  'artist@example.test', '美术甲', 'artist, reviewer', 0, 1,
		  '2025-03-04 05:06:07', '2025-03-04 05:06:07', NULL, NULL),
		 ('legacy-disabled-id', NULL, 'disabled-user', ?, '', '', '',
		  '', 0, 0, '2025-04-01T00:00:00+00:00', '2025-04-01T00:00:00+00:00',
		  NULL, NULL),
		 ('legacy-ghost-id', NULL, 'ghost', ?, '', '', '',
		  '', 0, 1, '2025-05-01T00:00:00+00:00', '2025-05-01T00:00:00+00:00',
		  '2025-05-06T07:08:09.100+00:00', NULL)`,
		legacyPasswordHash, legacyPasswordHash, legacyPasswordHash,
	)
}

func migrateTarget(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "go.db")
}

func TestMigrateUsersFieldFidelity(t *testing.T) {
	fixture := etltest.NewFixture(t)
	seedLegacyUsers(t, fixture)
	target := migrateTarget(t)
	code, output := runETL(t,
		"migrate", "--legacy-root", fixture.Root, "--target-db", target,
	)
	if code != 0 {
		t.Fatalf("迁移应成功，退出码 %d，输出:\n%s", code, output)
	}

	repository, err := sqlite.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	ctx := context.Background()

	admin, exists, err := repository.FindByUsername(ctx, "admin")
	if err != nil || !exists {
		t.Fatalf("admin 应存在: exists=%v err=%v", exists, err)
	}
	if admin.ID != "legacy-admin-id" ||
		admin.PasswordHash != legacyPasswordHash ||
		admin.Nickname != "管理员" ||
		admin.Name != "Admin" ||
		admin.Email != "admin@example.test" ||
		len(admin.Roles) != 0 ||
		!admin.Admin || !admin.Active || admin.Deleted {
		t.Fatalf("admin 字段不一致: %#v", admin)
	}

	sso, exists, err := repository.FindByID(ctx, "legacy-sso-id")
	if err != nil || !exists {
		t.Fatalf("SSO 用户应存在: exists=%v err=%v", exists, err)
	}
	if sso.OpenID != "sso-open-1" ||
		sso.Username != "sso-open-1" ||
		sso.PasswordHash != "" ||
		len(sso.Roles) != 2 ||
		sso.Roles[0] != "artist" || sso.Roles[1] != "reviewer" ||
		sso.Admin || !sso.Active {
		t.Fatalf("SSO 用户字段不一致: %#v", sso)
	}

	if _, exists, err = repository.FindActiveByUsername(
		ctx, "disabled-user",
	); err != nil || exists {
		t.Fatalf("停用用户不得按激活查询命中: exists=%v err=%v", exists, err)
	}
	disabled, exists, err := repository.FindByUsername(ctx, "disabled-user")
	if err != nil || !exists || disabled.Active {
		t.Fatalf("停用用户应存在且 Active=false: %#v", disabled)
	}

	ghost, exists, err := repository.FindByUsername(ctx, "ghost")
	if err != nil || !exists || !ghost.Deleted {
		t.Fatalf("软删除用户应存在且 Deleted=true: %#v", ghost)
	}

	// 时间统一转 Go 侧 RFC3339Nano（UTC）：ISO +00:00、
	// CURRENT_TIMESTAMP 空格格式、毫秒整数三种旧形态都要正确落库。
	assertColumn := func(id, column, expected string) {
		t.Helper()
		var value sql.NullString
		if err := queryTargetColumn(target, id, column, &value); err != nil {
			t.Fatal(err)
		}
		if !value.Valid || value.String != expected {
			t.Errorf("%s.%s = %#v, 期望 %q", id, column, value, expected)
		}
	}
	assertColumn("legacy-admin-id", "created_at", "2025-01-02T03:04:05.678Z")
	assertColumn(
		"legacy-admin-id",
		"last_login",
		time.UnixMilli(1750000000000).UTC().Format(time.RFC3339Nano),
	)
	assertColumn("legacy-sso-id", "created_at", "2025-03-04T05:06:07Z")
	assertColumn("legacy-ghost-id", "deleted_at", "2025-05-06T07:08:09.1Z")
}

func queryTargetColumn(
	target string,
	id string,
	column string,
	value *sql.NullString,
) error {
	db, err := sql.Open("sqlite", target)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.QueryRow(
		`SELECT `+column+` FROM authentication_users WHERE id = ?`, id,
	).Scan(value)
}

func TestMigrateUsersIdempotent(t *testing.T) {
	fixture := etltest.NewFixture(t)
	seedLegacyUsers(t, fixture)
	target := migrateTarget(t)
	dump := func() string {
		db, err := sql.Open("sqlite", target)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		rows, err := db.Query(
			`SELECT id, open_id, username, password_hash, nickname, name,
				email, roles_json, is_admin, is_active, created_at,
				updated_at, deleted_at, last_login
			 FROM authentication_users ORDER BY id`,
		)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var lines []string
		for rows.Next() {
			values := make([]sql.NullString, 14)
			pointers := make([]any, len(values))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			var fields []string
			for _, value := range values {
				fields = append(fields, value.String)
			}
			lines = append(lines, strings.Join(fields, "|"))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(lines, "\n")
	}

	for run := 1; run <= 2; run++ {
		code, output := runETL(t,
			"migrate", "--legacy-root", fixture.Root, "--target-db", target,
		)
		if code != 0 {
			t.Fatalf("第 %d 次迁移应成功，输出:\n%s", run, output)
		}
	}
	first := dump()
	code, output := runETL(t,
		"migrate", "--legacy-root", fixture.Root, "--target-db", target,
	)
	if code != 0 {
		t.Fatalf("重跑迁移应成功，输出:\n%s", output)
	}
	if second := dump(); second != first {
		t.Fatalf("重跑后内容漂移:\n前:\n%s\n后:\n%s", first, second)
	}
	if count := strings.Count(first, "\n") + 1; count != 4 {
		t.Fatalf("用户行数应为 4，实际 %d:\n%s", count, first)
	}
}

func TestMigrateReportsUserCounts(t *testing.T) {
	fixture := etltest.NewFixture(t)
	seedLegacyUsers(t, fixture)
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", migrateTarget(t),
	)
	if code != 0 {
		t.Fatalf("迁移应成功，输出:\n%s", output)
	}
	if !strings.Contains(output, "用户域: 源 4 行 / 迁移 4 行 / 跳过 0 行") {
		t.Errorf("应输出用户域对账计数:\n%s", output)
	}
}

func TestMigrateRejectsUnparseableTime(t *testing.T) {
	fixture := etltest.NewFixture(t)
	fixture.Exec(t,
		`INSERT INTO users (id, username, passwordHash, createdAt, updatedAt)
		 VALUES ('bad-time-id', 'bad-time', 'hash', '不是时间', '也不是时间')`,
	)
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", migrateTarget(t),
	)
	if code == 0 {
		t.Fatalf("无法解析的时间必须拒绝迁移，输出:\n%s", output)
	}
	if !strings.Contains(output, "bad-time-id") ||
		!strings.Contains(output, "无法解析") {
		t.Errorf("失败明细应指出用户与字段:\n%s", output)
	}
}

func TestMigrateReplacesUnusedManagedBootstrapAdmin(t *testing.T) {
	fixture := etltest.NewFixture(t)
	seedLegacyUsers(t, fixture)
	target := migrateTarget(t)
	repository, err := sqlite.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateBootstrapAdmin(
		context.Background(),
		authentication.UserRecord{
			ID:       "other-admin-id",
			Username: "admin",
			Admin:    true,
			Active:   true,
		},
		time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	code, output := runETL(t,
		"migrate", "--legacy-root", fixture.Root, "--target-db", target,
	)
	if code != 0 {
		t.Fatalf("未使用的启动管理员应被旧用户替换，输出:\n%s", output)
	}
	repository, err = sqlite.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	admin, exists, err := repository.FindByUsername(t.Context(), "admin")
	if err != nil || !exists || admin.ID != "legacy-admin-id" {
		t.Fatalf("迁移后管理员 = %#v exists=%v err=%v", admin, exists, err)
	}
	if _, exists, err := repository.FindByID(
		t.Context(),
		"other-admin-id",
	); err != nil || exists {
		t.Fatalf("启动管理员不应残留: exists=%v err=%v", exists, err)
	}
}

func TestMigrateRejectsUnmanagedUsernameConflict(t *testing.T) {
	fixture := etltest.NewFixture(t)
	seedLegacyUsers(t, fixture)
	target := migrateTarget(t)
	repository, err := sqlite.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	db := openTargetDatabase(t, target)
	if _, err := db.Exec(`
		INSERT INTO authentication_users (
			id, username, is_admin, is_active, created_at, updated_at,
			bootstrap_managed
		) VALUES (
			'other-admin-id', 'admin', 1, 1,
			'2026-07-01T00:00:00Z', '2026-07-01T00:00:00Z', 0
		)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	code, output := runETL(t,
		"migrate", "--legacy-root", fixture.Root, "--target-db", target,
	)
	if code == 0 {
		t.Fatalf("普通目标用户冲突必须拒绝迁移，输出:\n%s", output)
	}
	if !strings.Contains(output, "admin") ||
		!strings.Contains(output, "用户名") {
		t.Errorf("失败明细应指出冲突用户名:\n%s", output)
	}
}

func TestMigrateRequiresFlags(t *testing.T) {
	fixture := etltest.NewFixture(t)
	code, output := runETL(t, "migrate", "--legacy-root", fixture.Root)
	if code == 0 {
		t.Fatalf("缺少 --target-db 不得执行，输出:\n%s", output)
	}
	if !strings.Contains(output, "target-db") {
		t.Errorf("应提示缺失参数 target-db:\n%s", output)
	}
}

func TestMigrateRunsPrecheckFirst(t *testing.T) {
	fixture := etltest.NewFixture(t)
	seedLegacyUsers(t, fixture)
	fixture.Exec(t, `DROP TABLE canvas_nodes`)
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", migrateTarget(t),
	)
	if code == 0 {
		t.Fatalf("预检不通过必须拒绝迁移，输出:\n%s", output)
	}
	if !strings.Contains(output, "预检未通过") {
		t.Errorf("应输出预检失败结论:\n%s", output)
	}
}
