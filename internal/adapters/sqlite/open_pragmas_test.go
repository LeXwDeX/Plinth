package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

// The pragmas that make a connection safe (foreign key enforcement, the busy
// timeout) are per connection. modernc.org/sqlite drops a connection whose
// last statement was interrupted by a canceled context, and database/sql then
// opens a replacement; a replacement without the pragmas silently turns off
// the cascades and checks the chat deletion relies on (#384: orphaned turns of
// deleted sessions found at the next start). Every connection the pool opens
// must therefore carry them, not just the first.
func TestEveryPooledConnectionKeepsForeignKeysAndBusyTimeout(t *testing.T) {
	r, err := openTestRepository(filepath.Join(t.TempDir(), "pragmas.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	// With no idle connection each query opens a new one, as the pool does
	// after it discarded the interrupted connection.
	r.db.SetMaxIdleConns(0)
	for attempt := 0; attempt < 3; attempt++ {
		var foreignKeys, busyTimeout int
		if err := r.db.QueryRowContext(context.Background(), `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			t.Fatal(err)
		}
		if err := r.db.QueryRowContext(context.Background(), `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
			t.Fatal(err)
		}
		if foreignKeys != 1 || busyTimeout != 5000 {
			t.Fatalf("fresh connection %d: foreign_keys=%d busy_timeout=%d, want 1 and 5000", attempt, foreignKeys, busyTimeout)
		}
	}
}
