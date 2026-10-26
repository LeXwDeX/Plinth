package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// OpenReadOnly opens an existing command database without creating a file,
// journal sidecar or migrating it. It requires the exact current migration
// ledger, so a command cannot read a schema that this binary does not know.
func OpenReadOnly(path string) (*Repository, error) {
	return openCommandDatabase(path, true)
}

// OpenExisting opens an existing command database for writes without running
// any migration. The caller must have deployed the exact current schema.
func OpenExisting(path string) (*Repository, error) {
	return openCommandDatabase(path, false)
}

func openCommandDatabase(path string, readOnly bool) (*Repository, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("数据库不是普通文件")
	}
	query := url.Values{"_pragma": {"foreign_keys(1)", "busy_timeout(5000)"}}
	if readOnly {
		query.Set("mode", "ro")
		// immutable=1 prevents SQLite from creating -shm/-wal sidecars while a
		// command previews an already checkpointed offline database.
		query.Set("immutable", "1")
	} else {
		query.Set("mode", "rw")
	}
	dsn := url.URL{Scheme: "file", Path: absolute, RawQuery: query.Encode()}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := verifyCommandSchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Repository{db: db, clock: func() time.Time { return time.Now().UTC() }}, nil
}

// verifyCommandSchema compares the whole migration ledger to this binary's
// list. It never writes, so an old database is refused instead of migrated.
func verifyCommandSchema(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT version, name, checksum FROM go_schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("数据库缺少 migration ledger，请先启动服务完成正式升级: %w", err)
	}
	defer rows.Close()
	all := migrations()
	index := 0
	for rows.Next() {
		var version int
		var name, checksum string
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			return err
		}
		if index >= len(all) || all[index].version != version || all[index].name != name || migrationChecksum(all[index].sql) != checksum {
			return fmt.Errorf("数据库 schema 与当前二进制不一致（migration %d），请先启动服务完成正式升级", version)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if index != len(all) {
		return fmt.Errorf("数据库 schema 停在 migration %d，当前需要 %d；请先启动服务完成正式升级", index, len(all))
	}
	var column string
	err = db.QueryRowContext(ctx, `SELECT name FROM pragma_table_info('generation_attempts') WHERE name = 'delivery_completion_json'`).Scan(&column)
	if errors.Is(err, sql.ErrNoRows) || column != "delivery_completion_json" {
		return errors.New("数据库缺少本地交付完成列，请先启动服务完成正式升级")
	}
	return nil
}
