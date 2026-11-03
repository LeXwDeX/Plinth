package etl

import (
	"os"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
)

// A new target database starts as a copy of the migrated template instead of
// replaying every schema migration under the race detector; an existing target
// is still opened as it is.
func TestMain(m *testing.M) {
	openTarget = sqlitetest.Open
	os.Exit(m.Run())
}
