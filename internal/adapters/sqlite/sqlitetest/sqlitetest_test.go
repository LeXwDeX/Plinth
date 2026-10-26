package sqlitetest_test

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
)

type migrationRow struct {
	Version  int
	Name     string
	Checksum string
}

func readLedger(t *testing.T, path string) []migrationRow {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT version, name, checksum FROM go_schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ledger []migrationRow
	for rows.Next() {
		var row migrationRow
		if err := rows.Scan(&row.Version, &row.Name, &row.Checksum); err != nil {
			t.Fatal(err)
		}
		ledger = append(ledger, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ledger
}

func readTables(t *testing.T, path string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return tables
}

func fileDigest(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

func TestOpenMigratedSchemaMatchesFreshOpen(t *testing.T) {
	root := t.TempDir()
	freshPath := filepath.Join(root, "fresh.db")
	fresh, err := sqlite.Open(freshPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.Close(); err != nil {
		t.Fatal(err)
	}

	copiedPath := filepath.Join(root, "copied.db")
	copied, err := sqlitetest.Open(copiedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := copied.Close(); err != nil {
		t.Fatal(err)
	}

	freshLedger := readLedger(t, freshPath)
	if len(freshLedger) != sqlite.MigrationHead() {
		t.Fatalf("fresh ledger rows = %d, migration head = %d", len(freshLedger), sqlite.MigrationHead())
	}
	if copiedLedger := readLedger(t, copiedPath); !reflect.DeepEqual(copiedLedger, freshLedger) {
		t.Fatalf("copied ledger = %#v, fresh ledger = %#v", copiedLedger, freshLedger)
	}
	if copiedTables, freshTables := readTables(t, copiedPath), readTables(t, freshPath); !reflect.DeepEqual(copiedTables, freshTables) {
		t.Fatalf("copied tables = %#v, fresh tables = %#v", copiedTables, freshTables)
	}
}

func TestOpenCopiesIndependentDatabases(t *testing.T) {
	root := t.TempDir()
	firstPath := filepath.Join(root, "first.db")
	secondPath := filepath.Join(root, "second.db")
	for _, path := range []string{firstPath, secondPath} {
		repository, err := sqlitetest.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := repository.Close(); err != nil {
			t.Fatal(err)
		}
	}

	first, err := sql.Open("sqlite", firstPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Exec(`CREATE TABLE only_first(value TEXT)`); err != nil {
		first.Close()
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := sql.Open("sqlite", secondPath)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var count int
	if err := second.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'only_first'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("second database has %d copies of a table created only in the first", count)
	}
}

func TestOpenKeepsPersistedContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persisted.db")
	repository, err := sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE marker(value TEXT); INSERT INTO marker(value) VALUES ('kept')`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}

	check, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var value string
	if err := check.QueryRow(`SELECT value FROM marker`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "kept" {
		t.Fatalf("marker = %q, want %q", value, "kept")
	}
}

func TestOpenRejectsTamperedExistingDatabaseWithoutReplacingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tampered.db")
	repository, err := sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE go_schema_migrations SET checksum = 'tampered' WHERE version = 1`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	before := fileDigest(t, path)
	if _, err := sqlitetest.Open(path); err == nil || !strings.HasPrefix(err.Error(), "migration 0001_generation ") {
		t.Fatalf("opening tampered database: %v, want migration 1 checksum mismatch", err)
	}
	if after := fileDigest(t, path); after != before {
		t.Fatal("tampered database bytes changed after a rejected open")
	}

	check, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var checksum string
	if err := check.QueryRow(`SELECT checksum FROM go_schema_migrations WHERE version = 1`).Scan(&checksum); err != nil {
		t.Fatal(err)
	}
	if checksum != "tampered" {
		t.Fatalf("checksum = %q, want the tampered value left in place", checksum)
	}
}

func TestOpenParallelCopiesAreIndependent(t *testing.T) {
	const copies = 8
	root := t.TempDir()
	errs := make(chan error, copies)
	var wait sync.WaitGroup
	for i := range copies {
		wait.Add(1)
		go func() {
			defer wait.Done()
			path := filepath.Join(root, fmt.Sprintf("copy-%d.db", i))
			repository, err := sqlitetest.Open(path)
			if err != nil {
				errs <- err
				return
			}
			if err := repository.Close(); err != nil {
				errs <- err
				return
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				errs <- err
				return
			}
			defer db.Close()
			var head int
			if err := db.QueryRow(`SELECT MAX(version) FROM go_schema_migrations`).Scan(&head); err != nil {
				errs <- err
				return
			}
			if head != sqlite.MigrationHead() {
				errs <- fmt.Errorf("copy %d migration head = %d", i, head)
			}
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestOpenErrorsForInvalidDestinations(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlitetest.Open(filepath.Join(parent, "db.sqlite")); err == nil {
		t.Fatal("opening below a regular file succeeded")
	}

	directory := t.TempDir()
	if _, err := sqlitetest.Open(directory); err == nil {
		t.Fatal("opening a directory as a database succeeded")
	}

	corruptPath := filepath.Join(t.TempDir(), "corrupt.db")
	corrupt := []byte("not a SQLite database")
	if err := os.WriteFile(corruptPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlitetest.Open(corruptPath); err == nil {
		t.Fatal("opening a corrupt existing file succeeded")
	}
	if data, err := os.ReadFile(corruptPath); err != nil || string(data) != string(corrupt) {
		t.Fatalf("corrupt existing file changed: %q, %v", data, err)
	}
}

func TestOpenFallsBackForSpecialPaths(t *testing.T) {
	cases := []struct {
		name  string
		path  func(*testing.T) string
		check func(*testing.T, string)
	}{
		{
			name: "file DSN",
			path: func(t *testing.T) string { return "file:" + filepath.Join(t.TempDir(), "dsn.db") },
			check: func(t *testing.T, path string) {
				if _, err := os.Stat(strings.TrimPrefix(path, "file:")); err != nil {
					t.Fatalf("file DSN target: %v", err)
				}
			},
		},
		{
			name: "memory",
			path: func(*testing.T) string { return ":memory:" },
		},
		{
			name: "whitespace path",
			path: func(t *testing.T) string {
				return "  " + filepath.Join(t.TempDir(), "spaced.db") + "  "
			},
			check: func(t *testing.T, path string) {
				if _, err := os.Stat(strings.TrimSpace(path)); err != nil {
					t.Fatalf("trimmed path: %v", err)
				}
				if _, err := os.Stat(path); err == nil {
					t.Fatal("untrimmed path exists")
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.path(t)
			repository, err := sqlitetest.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := repository.Close(); err != nil {
				t.Fatal(err)
			}
			if tc.check != nil {
				tc.check(t, path)
			}
		})
	}
}
