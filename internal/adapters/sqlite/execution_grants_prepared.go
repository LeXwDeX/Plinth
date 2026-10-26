package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

var _ executiongrant.PreparedReader = (*Repository)(nil)

func (r *Repository) GetExecutionPreparedSnapshot(ctx context.Context, owner, preparedID string) (executiongrant.PreparedSnapshot, error) {
	if owner == "" || preparedID == "" {
		return executiongrant.PreparedSnapshot{}, executiongrant.ErrInvalid
	}
	var storedOwner, session, hash, payload string
	var required int
	err := r.db.QueryRowContext(ctx, `SELECT owner_id, session_id, input_hash, payload_json,native_quote_required
 FROM generation_prepared_inputs WHERE id=? AND owner_id=?`, preparedID, owner).Scan(&storedOwner, &session, &hash, &payload, &required)
	if errors.Is(err, sql.ErrNoRows) {
		return executiongrant.PreparedSnapshot{}, executiongrant.ErrConflict
	}
	if err != nil {
		return executiongrant.PreparedSnapshot{}, err
	}
	var snapshot executiongrant.PreparedSnapshot
	if err := json.Unmarshal([]byte(payload), &snapshot); err != nil {
		return executiongrant.PreparedSnapshot{}, err
	}
	if snapshot.ID != preparedID || snapshot.OwnerID != storedOwner || snapshot.SessionID != session || snapshot.InputHash != hash {
		return executiongrant.PreparedSnapshot{}, executiongrant.ErrConflict
	}
	if (required == 1) != (snapshot.NativeQuote != nil) || (required != 0 && required != 1) {
		return executiongrant.PreparedSnapshot{}, executiongrant.ErrConflict
	}
	if required == 1 {
		var p generation.PreparedInput
		if json.Unmarshal([]byte(payload), &p) != nil || generation.VerifyPreparedPricingHash(p) != nil {
			return executiongrant.PreparedSnapshot{}, executiongrant.ErrConflict
		}
	}
	return snapshot, nil
}
