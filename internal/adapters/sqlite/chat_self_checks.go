package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// The persisted 3D self-check records of a session (#246). One row belongs to
// one (owner, session, Task); every statement is owner and session scoped, so
// a foreign record reads as missing. record_json is the truth; status is the
// denormalised delivery state used by list reads and the prompt projection.
// Times are stored fixed-width so ORDER BY updated_at is time order.

const selfCheckColumns = `record_json, revision`

func newSelfCheckID() (string, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "selfcheck_" + hex.EncodeToString(random), nil
}

func scanSelfCheck(row interface{ Scan(...any) error }) (chat.SelfCheckRecord, error) {
	var raw string
	var revision int64
	if err := row.Scan(&raw, &revision); err != nil {
		return chat.SelfCheckRecord{}, err
	}
	return chat.DecodeSelfCheckRecord(raw, revision)
}

// ListSelfChecks returns the owner's records of the session, newest first.
func (r *Repository) ListSelfChecks(ctx context.Context, owner, session string) ([]chat.SelfCheckRecord, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+selfCheckColumns+` FROM chat_self_checks
		WHERE owner_id = ? AND session_id = ? ORDER BY updated_at DESC`, owner, session)
	if err != nil {
		return nil, fmt.Errorf("list self-checks: %w", err)
	}
	defer rows.Close()
	var records []chat.SelfCheckRecord
	for rows.Next() {
		record, err := scanSelfCheck(rows)
		if err != nil {
			return nil, fmt.Errorf("read self-check: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list self-checks: %w", err)
	}
	return records, nil
}

// SelfCheck returns one record of the owner's session.
func (r *Repository) SelfCheck(ctx context.Context, owner, session, taskID string) (chat.SelfCheckRecord, bool, error) {
	record, err := scanSelfCheck(r.db.QueryRowContext(ctx, `SELECT `+selfCheckColumns+` FROM chat_self_checks
		WHERE owner_id = ? AND session_id = ? AND task_id = ?`, owner, session, taskID))
	if errors.Is(err, sql.ErrNoRows) {
		return chat.SelfCheckRecord{}, false, nil
	}
	if err != nil {
		return chat.SelfCheckRecord{}, false, fmt.Errorf("read self-check: %w", err)
	}
	return record, true, nil
}

// SaveSelfCheck writes one record with revision compare-and-swap. A new record
// inserts only when the unique triple is free; an existing one updates only
// when its stored revision still equals expectedRevision. conflict reports a
// lost race, and the caller re-reads.
func (r *Repository) SaveSelfCheck(ctx context.Context, owner, session string, record chat.SelfCheckRecord,
	expectedRevision int64) (chat.SelfCheckRecord, bool, error) {
	raw, err := chat.EncodeSelfCheckRecord(record)
	if err != nil {
		return chat.SelfCheckRecord{}, false, err
	}
	status := ""
	if record.Conclusion != nil {
		status = record.Conclusion.Status
	}
	created := record.CreatedAt
	if created.IsZero() {
		created = record.UpdatedAt
	}
	updated := record.UpdatedAt
	if updated.IsZero() {
		updated = created
	}
	createdText, updatedText := formatTaskTime(created), formatTaskTime(updated)

	if expectedRevision <= 0 {
		id, err := newSelfCheckID()
		if err != nil {
			return chat.SelfCheckRecord{}, false, err
		}
		result, err := r.db.ExecContext(ctx, `INSERT INTO chat_self_checks
			(id, owner_id, session_id, task_id, model_asset_id, status, record_json, revision, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
			ON CONFLICT(owner_id, session_id, task_id) DO NOTHING`,
			id, owner, session, record.TaskID, record.ModelAssetID, status, raw, createdText, updatedText)
		if err != nil {
			return chat.SelfCheckRecord{}, false, fmt.Errorf("insert self-check: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return chat.SelfCheckRecord{}, false, err
		}
		if affected == 0 {
			return chat.SelfCheckRecord{}, true, nil
		}
		stored, found, err := r.storeSelfCheckRead(ctx, owner, session, record.TaskID)
		if err != nil || !found {
			return chat.SelfCheckRecord{}, false, err
		}
		return stored, false, nil
	}

	result, err := r.db.ExecContext(ctx, `UPDATE chat_self_checks
		SET model_asset_id = ?, status = ?, record_json = ?, revision = revision + 1, updated_at = ?
		WHERE owner_id = ? AND session_id = ? AND task_id = ? AND revision = ?`,
		record.ModelAssetID, status, raw, updatedText, owner, session, record.TaskID, expectedRevision)
	if err != nil {
		return chat.SelfCheckRecord{}, false, fmt.Errorf("update self-check: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return chat.SelfCheckRecord{}, false, err
	}
	if affected == 0 {
		return chat.SelfCheckRecord{}, true, nil
	}
	stored, found, err := r.storeSelfCheckRead(ctx, owner, session, record.TaskID)
	if err != nil || !found {
		return chat.SelfCheckRecord{}, false, err
	}
	return stored, false, nil
}

func (r *Repository) storeSelfCheckRead(ctx context.Context, owner, session, taskID string) (chat.SelfCheckRecord, bool, error) {
	record, err := scanSelfCheck(r.db.QueryRowContext(ctx, `SELECT `+selfCheckColumns+` FROM chat_self_checks
		WHERE owner_id = ? AND session_id = ? AND task_id = ?`, owner, session, taskID))
	if errors.Is(err, sql.ErrNoRows) {
		return chat.SelfCheckRecord{}, false, nil
	}
	if err != nil {
		return chat.SelfCheckRecord{}, false, err
	}
	return record, true, nil
}
