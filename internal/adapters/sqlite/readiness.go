package sqlite

import (
	"context"
	"database/sql"
	"errors"
)

// The SQLite driver does not implement driver.Pinger. Exercise the critical
// tables and acquire a real write transaction, then roll back the no-op probe.
func (r *Repository) CheckReadiness(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE go_schema_migrations SET applied_at = applied_at WHERE version = (SELECT MAX(version) FROM go_schema_migrations)`); err != nil {
		return err
	}
	for _, query := range []string{`SELECT id FROM generation_tasks LIMIT 1`, `SELECT assistant_message_id FROM go_chat_turns LIMIT 1`} {
		var id string
		if err := tx.QueryRowContext(ctx, query).Scan(&id); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return tx.Rollback()
}
