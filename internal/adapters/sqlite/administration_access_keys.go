package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/administration"
)

var _ administration.AccessKeyAdminRepository = (*Repository)(nil)

// accessKeyAdminColumns joins the key with the owner's account so the
// administrator sees who a key belongs to without a second look-up.
const accessKeyAdminColumns = `keys.id, keys.owner_id,
	COALESCE(user.username, ''), COALESCE(NULLIF(user.nickname, ''), NULLIF(user.name, ''), ''),
	keys.name, keys.prefix, keys.scopes_json, keys.created_at, keys.last_used_at,
	keys.expires_at, keys.revoked_at, keys.request_count`

const accessKeyAdminFrom = ` FROM access_keys AS keys
	LEFT JOIN authentication_users AS user ON user.id = keys.owner_id`

func scanAccessKeyAdmin(row interface{ Scan(...any) error }) (administration.AccessKeyRecord, error) {
	var record administration.AccessKeyRecord
	var scopes, created, lastUsed, expires, revoked string
	if err := row.Scan(&record.ID, &record.OwnerID, &record.OwnerUsername, &record.OwnerName,
		&record.Name, &record.Prefix, &scopes, &created, &lastUsed, &expires, &revoked,
		&record.RequestCount); err != nil {
		return administration.AccessKeyRecord{}, err
	}
	if err := json.Unmarshal([]byte(scopes), &record.Scopes); err != nil {
		return administration.AccessKeyRecord{}, fmt.Errorf("access key %s scopes: %w", record.ID, err)
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
			return administration.AccessKeyRecord{}, err
		}
		*field.into = parsed
	}
	return record, nil
}

// accessKeySearchPattern wraps the search term for LIKE and escapes the
// wildcards, so a search for "%" looks for a literal percent sign.
func accessKeySearchPattern(search string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + replacer.Replace(search) + "%"
}

// ListAdministrationAccessKeys returns every key matching the filters. The
// service orders and pages them.
func (r *Repository) ListAdministrationAccessKeys(ctx context.Context, query administration.AccessKeyQuery) ([]administration.AccessKeyRecord, error) {
	conditions := make([]string, 0, 2)
	args := make([]any, 0, 7)
	if query.OwnerID != "" {
		conditions = append(conditions, "keys.owner_id = ?")
		args = append(args, query.OwnerID)
	}
	if query.Search != "" {
		pattern := accessKeySearchPattern(query.Search)
		conditions = append(conditions, `(keys.name LIKE ? ESCAPE '\' OR keys.prefix LIKE ? ESCAPE '\'
			OR user.username LIKE ? ESCAPE '\' OR user.nickname LIKE ? ESCAPE '\' OR user.name LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern, pattern, pattern, pattern)
	}
	sqlText := `SELECT ` + accessKeyAdminColumns + accessKeyAdminFrom
	if len(conditions) > 0 {
		sqlText += " WHERE " + strings.Join(conditions, " AND ")
	}
	rows, err := r.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("列出管理访问 Key: %w", err)
	}
	defer rows.Close()
	records := []administration.AccessKeyRecord{}
	for rows.Next() {
		record, err := scanAccessKeyAdmin(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历管理访问 Key: %w", err)
	}
	return records, nil
}

// RevokeAdministrationAccessKey revokes any person's key. A missing key is
// administration.ErrAccessKeyNotFound; an already revoked key is returned
// unchanged with changed false.
func (r *Repository) RevokeAdministrationAccessKey(ctx context.Context, keyID string, now time.Time) (administration.AccessKeyRecord, bool, error) {
	result, err := r.db.ExecContext(ctx,
		`UPDATE access_keys SET revoked_at = ? WHERE id = ? AND revoked_at = ''`, formatTime(now), keyID)
	if err != nil {
		return administration.AccessKeyRecord{}, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return administration.AccessKeyRecord{}, false, err
	}
	record, err := scanAccessKeyAdmin(r.db.QueryRowContext(ctx,
		`SELECT `+accessKeyAdminColumns+accessKeyAdminFrom+` WHERE keys.id = ?`, keyID))
	if errors.Is(err, sql.ErrNoRows) {
		return administration.AccessKeyRecord{}, false, administration.ErrAccessKeyNotFound
	}
	if err != nil {
		return administration.AccessKeyRecord{}, false, err
	}
	return record, changed == 1, nil
}

const accessKeySwitchKey = "access_keys.enabled"

// AccessKeySwitch reads the overall switch. set is false until an
// administrator changes it, and the Schema's default applies.
func (r *Repository) AccessKeySwitch(ctx context.Context) (bool, bool, error) {
	var value string
	err := r.db.QueryRowContext(ctx,
		`SELECT value FROM access_key_settings WHERE key = ?`, accessKeySwitchKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return value == "true", true, nil
}

// SetAccessKeySwitch persists the overall switch.
func (r *Repository) SetAccessKeySwitch(ctx context.Context, enabled bool, now time.Time) error {
	value := "false"
	if enabled {
		value = "true"
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO access_key_settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		accessKeySwitchKey, value, formatTime(now))
	return err
}
