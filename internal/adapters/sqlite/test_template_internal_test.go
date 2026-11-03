package sqlite

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// 包内测试不能导入 sqlitetest（它依赖本包），这里保留同样的模板逻辑：
// 迁移只跑一次，新库从迁移好的模板复制，已存在的库原样打开。
var internalTestTemplate = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "plinth-sqlite-internal-")
	if err != nil {
		return nil, fmt.Errorf("create migrated SQLite template directory: %w", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "template.db")
	repository, err := Open(path)
	if err != nil {
		return nil, fmt.Errorf("prepare migrated SQLite template: %w", err)
	}
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

// openTestRepository opens path like Open. A missing file first becomes a copy
// of the migrated template; an existing file or a DSN is opened as it is.
func openTestRepository(path string) (*Repository, error) {
	path = strings.TrimSpace(path)
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
		return Open(path)
	}
	if _, err := os.Stat(path); err == nil {
		return Open(path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	template, err := internalTestTemplate()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, template, 0o600); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	repository, err := Open(path)
	if err != nil {
		_ = os.Remove(path)
	}
	return repository, err
}
