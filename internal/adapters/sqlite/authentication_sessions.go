package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"time"
)

func (r *Repository) CreateSessionRecord(ctx context.Context, record authentication.SessionRecord) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO authentication_sessions (id,user_id,issued_at,expires_at) VALUES (?,?,?,?) ON CONFLICT(id) DO NOTHING`, record.ID, record.UserID, formatTime(record.IssuedAt), formatTime(record.ExpiresAt))
	return err
}
func (r *Repository) FindSessionRecord(ctx context.Context, id string) (authentication.SessionRecord, bool, error) {
	var record authentication.SessionRecord
	var issued, expires string
	var revoked sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT id,user_id,issued_at,expires_at,revoked_at FROM authentication_sessions WHERE id=?`, id).Scan(&record.ID, &record.UserID, &issued, &expires, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return record, false, nil
	}
	if err != nil {
		return record, false, err
	}
	record.IssuedAt, err = time.Parse(time.RFC3339Nano, issued)
	if err != nil {
		return record, false, err
	}
	record.ExpiresAt, err = time.Parse(time.RFC3339Nano, expires)
	if err != nil {
		return record, false, err
	}
	if revoked.Valid {
		at, e := time.Parse(time.RFC3339Nano, revoked.String)
		if e != nil {
			return record, false, e
		}
		record.RevokedAt = &at
	}
	return record, true, nil
}
func (r *Repository) RevokeSessionRecord(ctx context.Context, id string, at time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE authentication_sessions SET revoked_at=COALESCE(revoked_at,?) WHERE id=?`, formatTime(at), id)
	return err
}

// RevokeUserSessions 吊销该用户全部活跃会话（SEC-06）：管理员改密、
// 停用、降权后既有会话立即失效。
func (r *Repository) RevokeUserSessions(ctx context.Context, userID string, at time.Time) error {
	_, err := r.db.ExecContext(
		ctx,
		`UPDATE authentication_sessions SET revoked_at=COALESCE(revoked_at,?) WHERE user_id=? AND revoked_at IS NULL`,
		formatTime(at),
		userID,
	)
	return err
}
