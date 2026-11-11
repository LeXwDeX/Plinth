package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

const preferenceColumns = `id,kind,source,direction,reason_preset,reason_text,task_id,asset_id,handle,
	alternatives_json,explicit,active,user_edited,created_at,updated_at`

// ListPreferences reads the active preference records of the owner's session,
// newest first. found is false for a missing or foreign session.
func (r *Repository) ListPreferences(
	ctx context.Context,
	ownerID, sessionID string,
) ([]chat.Preference, bool, error) {
	exists, err := r.chatSessionExists(ctx, ownerID, sessionID)
	if err != nil || !exists {
		return nil, false, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+preferenceColumns+`
		FROM chat_session_preferences
		WHERE owner_id = ? AND session_id = ? AND active = 1
		ORDER BY created_at DESC, id DESC`, ownerID, sessionID)
	if err != nil {
		return nil, true, fmt.Errorf("read chat session preferences: %w", err)
	}
	defer rows.Close()
	var preferences []chat.Preference
	for rows.Next() {
		preference, err := scanChatPreference(rows)
		if err != nil {
			return nil, true, err
		}
		preferences = append(preferences, preference)
	}
	if err := rows.Err(); err != nil {
		return nil, true, fmt.Errorf("read chat session preferences: %w", err)
	}
	return preferences, true, nil
}

// PutPreference inserts a preference record. An active record of the same
// object (task_id + asset_id) is replaced in the same transaction, so the
// latest action on an object wins. A weak signal replaces only the same
// object's weak record of the same source, so it never erases an explicit
// rejection. found is false for a missing or foreign session.
func (r *Repository) PutPreference(
	ctx context.Context,
	ownerID, sessionID string,
	preference chat.Preference,
) (chat.Preference, bool, error) {
	alternatives, err := chat.EncodePreferenceAlternatives(preference.Alternatives)
	if err != nil {
		return chat.Preference{}, false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return chat.Preference{}, false, fmt.Errorf("begin put chat session preference: %w", err)
	}
	defer tx.Rollback()
	exists, err := chatSessionExistsTx(ctx, tx, ownerID, sessionID)
	if err != nil {
		return chat.Preference{}, false, err
	}
	if !exists {
		return chat.Preference{}, false, nil
	}
	if preference.TaskID != "" && preference.AssetID != "" {
		query := `DELETE FROM chat_session_preferences
			WHERE owner_id = ? AND session_id = ? AND active = 1 AND task_id = ? AND asset_id = ?`
		arguments := []any{ownerID, sessionID, preference.TaskID, preference.AssetID}
		if !preference.Explicit {
			// A weak signal only supersedes another weak record of the same kind.
			query += ` AND explicit = 0 AND source = ?`
			arguments = append(arguments, preference.Source)
		}
		if _, err := tx.ExecContext(ctx, query, arguments...); err != nil {
			return chat.Preference{}, false, fmt.Errorf("replace chat session preference: %w", err)
		}
	}
	if preference.ID == "" {
		return chat.Preference{}, false, errors.New("sqlite: chat preference ID is required")
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO chat_session_preferences
			(id,owner_id,session_id,kind,source,direction,reason_preset,reason_text,task_id,asset_id,handle,
			 alternatives_json,explicit,active,user_edited,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		preference.ID, ownerID, sessionID, preference.Kind, preference.Source, preference.Direction,
		preference.ReasonPreset, preference.ReasonText, preference.TaskID, preference.AssetID, preference.Handle,
		alternatives, boolInt(preference.Explicit), boolInt(preference.Active), boolInt(preference.UserEdited),
		formatTime(preference.CreatedAt), formatTime(preference.UpdatedAt)); err != nil {
		return chat.Preference{}, false, fmt.Errorf("insert chat session preference: %w", err)
	}
	stored, err := readPreferenceTx(ctx, tx, ownerID, sessionID, preference.ID)
	if err != nil {
		return chat.Preference{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return chat.Preference{}, false, fmt.Errorf("commit chat session preference: %w", err)
	}
	return stored, true, nil
}

// UpdatePreference edits one active record. The edit marks user_edited, and
// active=false hides it. found is false for a missing/foreign session or
// preference; both are the same 404 at the HTTP boundary.
func (r *Repository) UpdatePreference(
	ctx context.Context,
	ownerID, sessionID, preferenceID string,
	edit chat.PreferenceEdit,
) (chat.Preference, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return chat.Preference{}, false, fmt.Errorf("begin update chat session preference: %w", err)
	}
	defer tx.Rollback()
	exists, err := chatSessionExistsTx(ctx, tx, ownerID, sessionID)
	if err != nil || !exists {
		return chat.Preference{}, false, err
	}
	current, err := readPreferenceTx(ctx, tx, ownerID, sessionID, preferenceID)
	if errors.Is(err, chat.ErrPreferenceNotFound) {
		return chat.Preference{}, false, nil
	}
	if err != nil {
		return chat.Preference{}, false, err
	}
	if edit.Direction != nil {
		current.Direction = *edit.Direction
	}
	if edit.ReasonPreset != nil {
		current.ReasonPreset = *edit.ReasonPreset
	}
	if edit.ReasonText != nil {
		current.ReasonText = *edit.ReasonText
	}
	if edit.Active != nil {
		current.Active = *edit.Active
	}
	current.UserEdited = true
	current.UpdatedAt = r.clock()
	if _, err := tx.ExecContext(ctx, `
		UPDATE chat_session_preferences
		SET direction = ?, reason_preset = ?, reason_text = ?, active = ?, user_edited = 1, updated_at = ?
		WHERE id = ? AND owner_id = ? AND session_id = ?`,
		current.Direction, current.ReasonPreset, current.ReasonText, boolInt(current.Active),
		formatTime(current.UpdatedAt), preferenceID, ownerID, sessionID); err != nil {
		return chat.Preference{}, false, fmt.Errorf("update chat session preference: %w", err)
	}
	stored, err := readPreferenceTx(ctx, tx, ownerID, sessionID, preferenceID)
	if err != nil {
		return chat.Preference{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return chat.Preference{}, false, fmt.Errorf("commit chat session preference: %w", err)
	}
	return stored, true, nil
}

// DeletePreference hides one record of the owner's session. The history fact
// stays in the table; inactive records are neither listed nor injected.
func (r *Repository) DeletePreference(
	ctx context.Context,
	ownerID, sessionID, preferenceID string,
) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE chat_session_preferences SET active = 0, updated_at = ?
		WHERE id = ? AND owner_id = ? AND session_id = ? AND active = 1`,
		formatTime(r.clock()), preferenceID, ownerID, sessionID)
	if err != nil {
		return false, fmt.Errorf("delete chat session preference: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// copyBranchPreferences copies the source session's active preference
// records into a new branch with fresh IDs. created_at is preserved so the
// prompt order survives the branch; updated_at is the branch instant.
func copyBranchPreferences(ctx context.Context, tx *sql.Tx, request chat.BranchRequest) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT kind,source,direction,reason_preset,reason_text,task_id,asset_id,handle,alternatives_json,
			explicit,active,user_edited,created_at
		FROM chat_session_preferences
		WHERE owner_id = ? AND session_id = ? AND active = 1
		ORDER BY created_at, id`, request.OwnerID, request.SessionID)
	if err != nil {
		return fmt.Errorf("read branch preferences: %w", err)
	}
	type branchPreference struct {
		kind, source, direction, reasonPreset, reasonText, taskID, assetID, handle, alternatives string
		explicit, active, userEdited                                                             int
		createdAt                                                                                string
	}
	var preferences []branchPreference
	for rows.Next() {
		var preference branchPreference
		if err := rows.Scan(&preference.kind, &preference.source, &preference.direction,
			&preference.reasonPreset, &preference.reasonText, &preference.taskID, &preference.assetID,
			&preference.handle, &preference.alternatives, &preference.explicit, &preference.active,
			&preference.userEdited, &preference.createdAt); err != nil {
			rows.Close()
			return fmt.Errorf("read branch preferences: %w", err)
		}
		preferences = append(preferences, preference)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read branch preferences: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("read branch preferences: %w", err)
	}
	for _, preference := range preferences {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO chat_session_preferences
				(id,owner_id,session_id,kind,source,direction,reason_preset,reason_text,task_id,asset_id,handle,
				 alternatives_json,explicit,active,user_edited,created_at,updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			request.NewID("preference"), request.OwnerID, request.NewSessionID,
			preference.kind, preference.source, preference.direction, preference.reasonPreset,
			preference.reasonText, preference.taskID, preference.assetID, preference.handle,
			preference.alternatives, preference.explicit, preference.active, preference.userEdited,
			preference.createdAt, formatTime(request.At)); err != nil {
			return fmt.Errorf("copy branch preference: %w", err)
		}
	}
	return nil
}

func (r *Repository) chatSessionExists(ctx context.Context, ownerID, sessionID string) (bool, error) {
	var exists int
	err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM go_chat_sessions WHERE id = ? AND owner_id = ?)`,
		sessionID, ownerID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check chat session: %w", err)
	}
	return exists != 0, nil
}

func chatSessionExistsTx(ctx context.Context, tx *sql.Tx, ownerID, sessionID string) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM go_chat_sessions WHERE id = ? AND owner_id = ?)`,
		sessionID, ownerID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check chat session: %w", err)
	}
	return exists != 0, nil
}

type preferenceScanner interface {
	Scan(dest ...any) error
}

func scanChatPreference(scanner preferenceScanner) (chat.Preference, error) {
	var preference chat.Preference
	var alternatives, createdAt, updatedAt string
	var explicit, active, userEdited int
	err := scanner.Scan(
		&preference.ID, &preference.Kind, &preference.Source, &preference.Direction,
		&preference.ReasonPreset, &preference.ReasonText, &preference.TaskID, &preference.AssetID,
		&preference.Handle, &alternatives, &explicit, &active, &userEdited, &createdAt, &updatedAt)
	if err != nil {
		return chat.Preference{}, err
	}
	preference.Alternatives = chat.DecodePreferenceAlternatives(alternatives)
	preference.Explicit = explicit != 0
	preference.Active = active != 0
	preference.UserEdited = userEdited != 0
	preference.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return chat.Preference{}, err
	}
	preference.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return chat.Preference{}, err
	}
	return preference, nil
}

func readPreferenceTx(
	ctx context.Context,
	tx *sql.Tx,
	ownerID, sessionID, preferenceID string,
) (chat.Preference, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT `+preferenceColumns+`
		FROM chat_session_preferences
		WHERE id = ? AND owner_id = ? AND session_id = ?`, preferenceID, ownerID, sessionID)
	preference, err := scanChatPreference(row)
	if errors.Is(err, sql.ErrNoRows) {
		return chat.Preference{}, chat.ErrPreferenceNotFound
	}
	if err != nil {
		return chat.Preference{}, fmt.Errorf("read chat session preference: %w", err)
	}
	return preference, nil
}
