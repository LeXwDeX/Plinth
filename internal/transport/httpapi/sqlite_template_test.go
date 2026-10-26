package httpapi

import (
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
)

// OpenMigratedTestRepository opens path like sqlite.Open. A missing file first
// becomes a copy of a migrated template; an existing file is opened as it is.
func OpenMigratedTestRepository(path string) (*sqlite.Repository, error) {
	return sqlitetest.Open(path)
}
