package app

import (
	"os"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
)

// Every New in this package's tests opens its database through the migrated
// template: a new file starts as a copy instead of replaying every migration
// under the race detector. Existing files, such as seeded legacy databases,
// are still opened as they are.
func TestMain(m *testing.M) {
	openRepository = sqlitetest.Open
	os.Exit(m.Run())
}
