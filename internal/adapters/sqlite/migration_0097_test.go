package sqlite

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// historicalExecutionGrantsSQL is the execution_grants schema a database has
// before 0097: the statements of 0036, 0040, 0051 and 0075 that define it,
// taken from the migration files themselves so the test cannot drift from what
// production databases hold.
func historicalExecutionGrantsSQL(t *testing.T) []string {
	t.Helper()
	create := regexp.MustCompile(`(?s)CREATE TABLE execution_grants \(.*?\n\);`)
	pieces := regexp.MustCompile(`(?s)(CREATE (?:UNIQUE )?INDEX [a-z_]+\s+ON execution_grants\b.*?;|ALTER TABLE execution_grants ADD COLUMN.*?;)`)
	var statements []string
	for _, file := range []string{"0036_execution_grants.sql", "0040_execution_grant_turn.sql", "0051_harness.sql", "0075_production_budgets.sql"} {
		raw, err := os.ReadFile(filepath.Join("migrations", file))
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if file == "0036_execution_grants.sql" {
			statement := create.FindString(text)
			if statement == "" {
				t.Fatalf("%s: no execution_grants definition", file)
			}
			statements = append(statements, statement)
		}
		statements = append(statements, pieces.FindAllString(text, -1)...)
	}
	if len(statements) != 7 {
		// 1 table (0036), 2 indexes (0036, 0040) and 4 columns: trigger_turn_id
		// (0040), harness_revision and harness_digest (0051), budget_eligible (0075).
		t.Fatalf("historical execution_grants statements = %d: %q", len(statements), statements)
	}
	return statements
}

const executionGrantColumns = `request_id, owner_id, session_id, offering_id, offering_revision, prepared_input_id,
 prepared_input_hash, idempotency_key, operation_id, plan_step_id, status, task_id, source, request_version, decided_by,
 decided_at, created_at, updated_at, trigger_turn_id, harness_revision, harness_digest, budget_eligible`

// snapshotGrants reads every row of execution_grants (all columns that existed
// before 0097) as comparable text, in primary key order.
func snapshotGrants(t *testing.T, r *Repository) []string {
	t.Helper()
	rows, err := r.db.Query(`SELECT ` + executionGrantColumns + ` FROM execution_grants ORDER BY request_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		values := make([]any, 22)
		pointers := make([]any, 22)
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%#v", values))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// schemaOf is the shape of the table other than its added column: every
// column's position, type, nullability, default and key position, the indexes
// with their definitions, and the number of unique constraints.
func schemaOf(t *testing.T, r *Repository) (columns, indexes []string, unique int) {
	t.Helper()
	rows, err := r.db.Query(`SELECT cid, name, type, "notnull", COALESCE(dflt_value, 'NULL'), pk FROM pragma_table_info('execution_grants') ORDER BY cid`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind, dflt string
		if err := rows.Scan(&cid, &name, &kind, &notNull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, fmt.Sprintf("%d|%s|%s|%d|%s|%d", cid, name, kind, notNull, dflt, pk))
	}
	_ = rows.Close()
	rows, err = r.db.Query(`SELECT name, sql FROM sqlite_master WHERE type = 'index' AND tbl_name = 'execution_grants' AND sql IS NOT NULL ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name, statement string
		if err := rows.Scan(&name, &statement); err != nil {
			t.Fatal(err)
		}
		indexes = append(indexes, name+": "+strings.Join(strings.Fields(statement), " "))
	}
	_ = rows.Close()
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM pragma_index_list('execution_grants') WHERE origin = 'u'`).Scan(&unique); err != nil {
		t.Fatal(err)
	}
	return columns, indexes, unique
}

// Migration 0097 rebuilds execution_grants to allow the access_key source. A
// database that holds grants of every old source and status upgrades with each
// row, column, default, constraint and index unchanged; only the new column is
// added, empty.
func TestMigration0097RebuildsExecutionGrantsKeepingEveryRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grants.db")
	r, err := openTestRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	// Put the database back where 0096 left it: the pre-0097 table with its
	// indexes, and no ledger row for 0097.
	setup := []string{`DELETE FROM go_schema_migrations WHERE version = 97`, `DROP TABLE execution_grants`}
	setup = append(setup, historicalExecutionGrantsSQL(t)...)
	for _, statement := range setup {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	// Rows of every old source and status, with every optional column filled in
	// the way the code fills it.
	if _, err := r.db.Exec(`INSERT INTO execution_grants
 (request_id, owner_id, session_id, offering_id, offering_revision, prepared_input_id, prepared_input_hash, idempotency_key,
  operation_id, plan_step_id, status, task_id, source, request_version, decided_by, decided_at, created_at, updated_at,
  trigger_turn_id, harness_revision, harness_digest, budget_eligible) VALUES
 ('eg_1','owner-a','s1','o','rev','p1','h1','k1','op1','',         'pending', NULL,   'human_browser',        1, NULL,           NULL,                       '2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','',     0,'',    0),
 ('eg_2','owner-a','s1','o','rev','p2','h2','k2','op2','step-1',   'approved',NULL,   'agent_human_decision', 3, 'policy:auto',  '2026-01-02T00:00:00.5Z',   '2026-01-01T00:00:00Z','2026-01-02T00:00:00.5Z','turn-1',2,'dig-1',1),
 ('eg_3','owner-b','s2','o','rev','p3','h3','k3','op3','',         'bound',   'task-3','human_browser',       2, 'user-b',       '2026-01-03T00:00:00Z',     '2026-01-01T00:00:00Z','2026-01-03T00:00:00Z','',     0,'',    0),
 ('eg_4','owner-b','s2','o','rev','p4','h4','k4','op4','',         'denied',  NULL,   'agent_human_decision', 1, 'policy:steps', '2026-01-04T00:00:00Z',     '2026-01-01T00:00:00Z','2026-01-04T00:00:00Z','turn-2',0,'',    1),
 ('eg_5','owner-b','s2','o','rev','p5','h5','k5','op5','',         'claimed', NULL,   'human_browser',        1, 'user-b',       '2026-01-05T00:00:00Z',     '2026-01-01T00:00:00Z','2026-01-05T00:00:00Z','',     0,'',    0)`); err != nil {
		t.Fatal(err)
	}
	beforeRows := snapshotGrants(t, r)
	beforeColumns, beforeIndexes, beforeUnique := schemaOf(t, r)
	if len(beforeRows) != 5 || len(beforeColumns) != 22 || len(beforeIndexes) != 2 || beforeUnique != 3 {
		t.Fatalf("the historical fixture is wrong: rows=%d columns=%d indexes=%v unique=%d", len(beforeRows), len(beforeColumns), beforeIndexes, beforeUnique)
	}
	// The old table refuses the new source: that is what the migration lifts.
	if _, err := r.db.Exec(`INSERT INTO execution_grants (request_id,owner_id,session_id,offering_id,offering_revision,prepared_input_id,prepared_input_hash,idempotency_key,operation_id,status,source,request_version,created_at,updated_at)
 VALUES ('eg_x','o','s','of','rev','p','h','kx','opx','pending','access_key',1,'t','t')`); err == nil {
		t.Fatal("the historical table already accepts the access_key source")
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
	if err := upgraded.db.QueryRow(`SELECT MAX(version) FROM go_schema_migrations`).Scan(&head); err != nil || head != MigrationHead() || head < 97 {
		t.Fatalf("head = %d err=%v", head, err)
	}
	afterRows := snapshotGrants(t, upgraded)
	if fmt.Sprint(afterRows) != fmt.Sprint(beforeRows) {
		t.Fatalf("rows changed:\nbefore %v\nafter  %v", beforeRows, afterRows)
	}
	afterColumns, afterIndexes, afterUnique := schemaOf(t, upgraded)
	if len(afterColumns) != 23 || fmt.Sprint(afterColumns[:22]) != fmt.Sprint(beforeColumns) ||
		afterColumns[22] != "22|access_key_id|TEXT|1|''|0" {
		t.Fatalf("columns after:\n%v\nbefore:\n%v", afterColumns, beforeColumns)
	}
	if fmt.Sprint(afterIndexes) != fmt.Sprint(beforeIndexes) || afterUnique != beforeUnique {
		t.Fatalf("indexes changed: before %v (%d unique) after %v (%d unique)", beforeIndexes, beforeUnique, afterIndexes, afterUnique)
	}
	var keyed int
	if err := upgraded.db.QueryRow(`SELECT COUNT(*) FROM execution_grants WHERE access_key_id <> ''`).Scan(&keyed); err != nil || keyed != 0 {
		t.Fatalf("existing rows carry a key: %d err=%v", keyed, err)
	}

	// The old sources and every constraint of the table still work.
	insert := func(id, status, source, task, key string) error {
		_, err := upgraded.db.Exec(`INSERT INTO execution_grants
 (request_id, owner_id, session_id, offering_id, offering_revision, prepared_input_id, prepared_input_hash, idempotency_key,
  operation_id, status, task_id, source, request_version, created_at, updated_at, access_key_id)
 VALUES (?, 'owner-a', 's9', 'o', 'rev', 'p', 'h', ?, ?, ?, NULLIF(?, ''), ?, 1, 't', 't', ?)`,
			id, "k-"+id, "op-"+id, status, task, source, key)
		return err
	}
	for name, call := range map[string]struct {
		run  func() error
		fail bool
	}{
		"browser grant":            {func() error { return insert("n1", "pending", "human_browser", "", "") }, false},
		"agent grant":              {func() error { return insert("n2", "pending", "agent_human_decision", "", "") }, false},
		"access key grant":         {func() error { return insert("n3", "pending", "access_key", "", "key-1") }, false},
		"key grant without a key":  {func() error { return insert("n4", "pending", "access_key", "", "") }, true},
		"key on a browser grant":   {func() error { return insert("n5", "pending", "human_browser", "", "key-1") }, true},
		"unknown source":           {func() error { return insert("n6", "pending", "robot", "", "") }, true},
		"unknown status":           {func() error { return insert("n7", "waiting", "human_browser", "", "") }, true},
		"bound without a task":     {func() error { return insert("n8", "bound", "human_browser", "", "") }, true},
		"task on an unbound grant": {func() error { return insert("n9", "pending", "human_browser", "task-x", "") }, true},
		"duplicate request id":     {func() error { return insert("eg_1", "pending", "human_browser", "", "") }, true},
		"duplicate idempotency key": {func() error {
			_, err := upgraded.db.Exec(`UPDATE execution_grants SET idempotency_key = 'k1' WHERE request_id = 'eg_2'`)
			return err
		}, true},
		"duplicate task": {func() error { return insert("n10", "bound", "human_browser", "task-3", "") }, true},
		"non-positive version": {func() error {
			_, err := upgraded.db.Exec(`UPDATE execution_grants SET request_version = 0 WHERE request_id = 'eg_1'`)
			return err
		}, true},
		"budget flag outside 0 and 1": {func() error {
			_, err := upgraded.db.Exec(`UPDATE execution_grants SET budget_eligible = 2 WHERE request_id = 'eg_1'`)
			return err
		}, true},
		"negative harness revision": {func() error {
			_, err := upgraded.db.Exec(`UPDATE execution_grants SET harness_revision = -1 WHERE request_id = 'eg_1'`)
			return err
		}, true},
	} {
		if err := call.run(); (err != nil) != call.fail {
			t.Errorf("%s: err = %v; want failure=%v", name, err, call.fail)
		}
	}
	// Opening again changes nothing: the ledger holds 0097 and its checksum.
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openTestRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	columns, _, _ := schemaOf(t, reopened)
	if len(columns) != 23 {
		t.Fatalf("columns after reopening = %d", len(columns))
	}
}

// 0096 adds the access_keys table and its owner index; 0105 appends the usage
// column, so the head schema carries it too.
func TestMigration0096CreatesTheAccessKeysTable(t *testing.T) {
	r := openAccessKeyRepo(t)
	rows, err := r.db.Query(`SELECT name, type, "notnull", COALESCE(dflt_value, 'NULL'), pk FROM pragma_table_info('access_keys') ORDER BY cid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var name, kind, dflt string
		var notNull, pk int
		if err := rows.Scan(&name, &kind, &notNull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, fmt.Sprintf("%s|%s|%d|%s|%d", name, kind, notNull, dflt, pk))
	}
	want := []string{
		"id|TEXT|0|NULL|1", "owner_id|TEXT|1|NULL|0", "name|TEXT|1|NULL|0", "prefix|TEXT|1|NULL|0",
		"secret_hash|TEXT|1|NULL|0", "scopes_json|TEXT|1|NULL|0", "created_at|TEXT|1|NULL|0",
		"last_used_at|TEXT|1|''|0", "expires_at|TEXT|1|''|0", "revoked_at|TEXT|1|''|0",
		"request_count|INTEGER|1|0|0",
	}
	if fmt.Sprint(columns) != fmt.Sprint(want) {
		t.Fatalf("columns = %v", columns)
	}
	var index int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'access_keys_owner' AND tbl_name = 'access_keys'`).Scan(&index); err != nil || index != 1 {
		t.Fatalf("owner index = %d err=%v", index, err)
	}
	if _, err := r.db.Exec(`INSERT INTO access_keys (id, owner_id, name, prefix, secret_hash, scopes_json, created_at) VALUES ('a','o','n','p','h','not json','t')`); err == nil {
		t.Fatal("scopes_json accepted text that is not a JSON array")
	}
	if _, err := r.db.Exec(`INSERT INTO access_keys (id, owner_id, name, prefix, secret_hash, scopes_json, created_at) VALUES ('a','o','n','p','h','{"a":1}','t')`); err == nil {
		t.Fatal("scopes_json accepted an object")
	}
}
