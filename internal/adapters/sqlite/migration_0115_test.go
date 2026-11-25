package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

// #599: 0115 adds the nullable project group key to authentication_users. A
// database from before it keeps every existing user, all ungrouped (NULL), and
// the administration read path reports them as "" rather than failing.
func TestMigration0115AddsNullableProjectGroupToExistingUsers599(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project-group.db")
	r, err := openTestRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	// Put the database back where 0114 left it.
	for _, statement := range []string{
		`DELETE FROM go_schema_migrations WHERE version = 115`,
		`ALTER TABLE authentication_users DROP COLUMN project_group_key`,
		`INSERT INTO authentication_users (id, username, password_hash, nickname, name, email, roles_json,
			is_admin, is_active, created_at, updated_at)
		 VALUES ('user-old', 'old', 'hash', 'Old', 'Old', 'old@example.test', '["user"]', 0, 1,
			'2026-11-24T00:00:00Z', '2026-11-24T00:00:00Z')`,
	} {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := openTestRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	var head int
	if err := upgraded.db.QueryRow(`SELECT MAX(version) FROM go_schema_migrations`).Scan(&head); err != nil || head != MigrationHead() || head < 115 {
		t.Fatalf("head = %d err=%v", head, err)
	}
	var notNull int
	var defaultValue any
	if err := upgraded.db.QueryRow(`SELECT "notnull", dflt_value FROM pragma_table_info('authentication_users')
		WHERE name = 'project_group_key'`).Scan(&notNull, &defaultValue); err != nil {
		t.Fatalf("project_group_key column missing: %v", err)
	}
	if notNull != 0 || defaultValue != nil {
		t.Fatalf("project_group_key must be nullable without default: notnull=%d default=%v", notNull, defaultValue)
	}
	var raw any
	if err := upgraded.db.QueryRow(`SELECT project_group_key FROM authentication_users WHERE id = 'user-old'`).Scan(&raw); err != nil || raw != nil {
		t.Fatalf("existing user must start ungrouped (NULL): %v err=%v", raw, err)
	}
	stored, exists, err := upgraded.FindAdministrationUser(context.Background(), "user-old")
	if err != nil || !exists || stored.ProjectGroupKey != "" || stored.Username != "old" {
		t.Fatalf("existing user after upgrade = %+v exists=%v err=%v", stored, exists, err)
	}
}
