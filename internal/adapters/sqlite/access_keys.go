package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/LeXwDeX/Plinth/internal/accesskeys"
)

var _ accesskeys.Store = (*Repository)(nil)

const accessKeyColumns = `id, owner_id, name, prefix, secret_hash, scopes_json, created_at, request_count, last_used_at, expires_at, revoked_at`

// scanAccessKey reads one row of accessKeyColumns. An unset time is the empty
// string in the column and the zero time here.
func scanAccessKey(row interface{ Scan(...any) error }) (accesskeys.Record, error) {
	var record accesskeys.Record
	var scopes, created, lastUsed, expires, revoked string
	if err := row.Scan(&record.ID, &record.OwnerID, &record.Name, &record.Prefix, &record.SecretHash,
		&scopes, &created, &record.RequestCount, &lastUsed, &expires, &revoked); err != nil {
		return accesskeys.Record{}, err
	}
	if err := json.Unmarshal([]byte(scopes), &record.Scopes); err != nil {
		return accesskeys.Record{}, fmt.Errorf("access key %s scopes: %w", record.ID, err)
	}
	for _, field := range []struct {
		text string
		into *time.Time
	}{{created, &record.CreatedAt}, {lastUsed, &record.LastUsedAt}, {expires, &record.ExpiresAt}, {revoked, &record.RevokedAt}} {
		if field.text == "" {
			continue
		}
		parsed, err := parseTime(field.text)
		if err != nil {
			return accesskeys.Record{}, err
		}
		*field.into = parsed
	}
	return record, nil
}

// unsetOrTime is the column form of a time that may be unset.
func unsetOrTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return formatTime(value)
}

// CreateAccessKey stores the key unless its owner already has maxActive active
// keys (not revoked, not expired). The count runs in Go over the owner's few
// rows: the column is RFC3339Nano text, whose byte order is not time order, so
// a SQL comparison of the expiry could be off. BEGIN IMMEDIATE makes the count
// and the insert one step, so two concurrent creations cannot both take the
// last place.
func (r *Repository) CreateAccessKey(ctx context.Context, record accesskeys.Record, maxActive int, now time.Time) error {
	if record.ID == "" || record.OwnerID == "" || record.SecretHash == "" || len(record.Scopes) == 0 || maxActive < 1 {
		return accesskeys.ErrInvalid
	}
	scopes, err := json.Marshal(record.Scopes)
	if err != nil {
		return err
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			rollbackGrantConn(conn)
		}
	}()
	rows, err := conn.QueryContext(ctx, `SELECT `+accessKeyColumns+` FROM access_keys WHERE owner_id = ?`, record.OwnerID)
	if err != nil {
		return err
	}
	active := 0
	for rows.Next() {
		existing, err := scanAccessKey(rows)
		if err != nil {
			_ = rows.Close()
			return err
		}
		if existing.Active(now) {
			active++
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if active >= maxActive {
		return accesskeys.ErrLimit
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO access_keys (`+accessKeyColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.OwnerID, record.Name, record.Prefix, record.SecretHash, string(scopes),
		formatTime(record.CreatedAt), record.RequestCount, unsetOrTime(record.LastUsedAt),
		unsetOrTime(record.ExpiresAt), unsetOrTime(record.RevokedAt)); err != nil {
		return err
	}
	if err := commitGrantConn(ctx, conn); err != nil {
		return err
	}
	committed = true
	return nil
}

// AccessKey reads a key by its ID, whoever owns it.
func (r *Repository) AccessKey(ctx context.Context, id string) (accesskeys.Record, bool, error) {
	record, err := scanAccessKey(r.db.QueryRowContext(ctx, `SELECT `+accessKeyColumns+` FROM access_keys WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return accesskeys.Record{}, false, nil
	}
	if err != nil {
		return accesskeys.Record{}, false, err
	}
	return record, true, nil
}

// ListAccessKeys lists the owner's keys, newest first.
func (r *Repository) ListAccessKeys(ctx context.Context, ownerID string) ([]accesskeys.Record, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+accessKeyColumns+` FROM access_keys WHERE owner_id = ?`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []accesskeys.Record{}
	for rows.Next() {
		record, err := scanAccessKey(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Ordered in Go for the same reason the active count is: the stored text is
	// not time-ordered.
	sort.SliceStable(records, func(i, j int) bool {
		if !records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].CreatedAt.After(records[j].CreatedAt)
		}
		return records[i].ID < records[j].ID
	})
	return records, nil
}

// RevokeAccessKey revokes the owner's key. A key that is already revoked is
// returned unchanged with changed false.
func (r *Repository) RevokeAccessKey(ctx context.Context, ownerID, id string, now time.Time) (accesskeys.Record, bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE access_keys SET revoked_at = ? WHERE id = ? AND owner_id = ? AND revoked_at = ''`,
		formatTime(now), id, ownerID)
	if err != nil {
		return accesskeys.Record{}, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return accesskeys.Record{}, false, err
	}
	record, err := scanAccessKey(r.db.QueryRowContext(ctx,
		`SELECT `+accessKeyColumns+` FROM access_keys WHERE id = ? AND owner_id = ?`, id, ownerID))
	if errors.Is(err, sql.ErrNoRows) {
		return accesskeys.Record{}, false, accesskeys.ErrNotFound
	}
	if err != nil {
		return accesskeys.Record{}, false, err
	}
	return record, changed == 1, nil
}

// RecordAccessKeyUse records uses of the key: the last use time and an
// atomic addition to the cumulative count. A revoked key is left as it was.
func (r *Repository) RecordAccessKeyUse(ctx context.Context, id string, at time.Time, uses int64) error {
	if uses < 0 {
		return accesskeys.ErrInvalid
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE access_keys SET last_used_at = ?, request_count = request_count + ? WHERE id = ? AND revoked_at = ''`,
		formatTime(at), uses, id)
	return err
}
