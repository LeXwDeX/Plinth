// Package sqlitetest opens migrated SQLite repositories for tests.
//
// Every migrated database has the same schema, and running all migrations costs
// about a second under -race. Tests that only need a fresh migrated database
// copy a closed, WAL-checkpointed template instead of replaying the migration
// chain. Production code must not import this package.
package sqlitetest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
)

// migratedTemplate is one immutable template per test binary. Building it in a
// temporary directory and returning only its bytes lets the directory go away
// immediately, without a TestMain in every consuming package.
var migratedTemplate = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "plinth-sqlitetest-")
	if err != nil {
		return nil, fmt.Errorf("create migrated SQLite template directory: %w", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "template.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		return nil, fmt.Errorf("prepare migrated SQLite template: %w", err)
	}
	// Closing the only connection checkpoints the WAL into the main file.
	if err := repository.Close(); err != nil {
		return nil, fmt.Errorf("close migrated SQLite template: %w", err)
	}
	if _, err := os.Stat(path + "-wal"); err == nil {
		return nil, errors.New("migrated SQLite template kept a WAL file after close")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return os.ReadFile(path)
})

// Open opens path like sqlite.Open. A missing file first becomes a copy of the
// migrated template; an existing file is opened unchanged, so restart and
// tamper fixtures keep their own bytes. sqlite.Open still checks every
// migration checksum against the copy, and memory or file DSNs use the real
// sqlite.Open path.
func Open(path string) (*sqlite.Repository, error) {
	path = strings.TrimSpace(path)
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
		return sqlite.Open(path)
	}
	if _, err := os.Stat(path); err == nil {
		return sqlite.Open(path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	template, err := migratedTemplate()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	target, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		_ = os.Remove(path)
		_ = os.Remove(path + "-wal")
		_ = os.Remove(path + "-shm")
	}
	_, writeErr := target.Write(template)
	if closeErr := target.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		cleanup()
		return nil, writeErr
	}
	repository, err := sqlite.Open(path)
	if err != nil {
		cleanup()
	}
	return repository, err
}
