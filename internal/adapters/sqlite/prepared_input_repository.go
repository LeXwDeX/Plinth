package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func (r *Repository) CreatePreparedInput(ctx context.Context, prepared generation.PreparedInput) error {
	required := 0
	if prepared.NativeQuote != nil {
		required = 1
		if err := generation.VerifyPreparedPricingHash(prepared); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(prepared)
	if err != nil {
		return fmt.Errorf("encode prepared input: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO generation_prepared_inputs
		(id, owner_id, session_id, input_hash, payload_json, created_at,native_quote_required)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, prepared.ID, prepared.OwnerID, prepared.SessionID,
		prepared.InputHash, string(encoded), formatTime(prepared.CreatedAt), required)
	if err != nil {
		return fmt.Errorf("create prepared input: %w", err)
	}
	return nil
}

func (r *Repository) GetPreparedInput(ctx context.Context, owner generation.OwnerID,
	id generation.PreparedInputID,
) (generation.PreparedInput, error) {
	var payload, storedHash string
	var required int
	err := r.db.QueryRowContext(ctx, `SELECT payload_json, input_hash,native_quote_required FROM generation_prepared_inputs
		WHERE owner_id = ? AND id = ?`, owner, id).Scan(&payload, &storedHash, &required)
	if errors.Is(err, sql.ErrNoRows) {
		return generation.PreparedInput{}, generation.RepositoryNotFound
	}
	if err != nil {
		return generation.PreparedInput{}, fmt.Errorf("get prepared input: %w", err)
	}
	var prepared generation.PreparedInput
	if err := json.Unmarshal([]byte(payload), &prepared); err != nil {
		return generation.PreparedInput{}, fmt.Errorf("decode prepared input: %w", err)
	}
	if (required == 1) != (prepared.NativeQuote != nil) || (required != 0 && required != 1) {
		return generation.PreparedInput{}, generation.RepositoryConflict
	}
	if prepared.InputHash != storedHash {
		return generation.PreparedInput{}, generation.RepositoryConflict
	}
	return prepared, nil
}
