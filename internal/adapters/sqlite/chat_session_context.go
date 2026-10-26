package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// Session thinking level, context usage and compactions (#322, migration
// 0065). Every statement is scoped to the owner; none touches updated_at, so
// these writes never reorder the session list.

const compactionColumns = `id, owner_id, session_id, trigger, summary, through_message_id, through_created_at, rounds, created_at`

func newCompactionID() (string, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "compaction_" + hex.EncodeToString(random), nil
}

// SetSessionReasoningEffort stores the level the owner chose for the session.
// The service has already checked it against the session's model.
func (r *Repository) SetSessionReasoningEffort(ctx context.Context, ownerID, sessionID, effort string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE go_chat_sessions SET reasoning_effort = ?
		WHERE id = ? AND owner_id = ?`, effort, sessionID, ownerID)
	if err != nil {
		return false, fmt.Errorf("set chat session thinking level: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// SessionContext reads the session's level, newest usage and newest
// compaction. found is false for a missing or foreign session.
func (r *Repository) SessionContext(ctx context.Context, ownerID, sessionID string) (chat.SessionContext, bool, error) {
	var state chat.SessionContext
	var prompt, completion sql.NullInt64
	var model, updated string
	err := r.db.QueryRowContext(ctx, `SELECT reasoning_effort, context_prompt_tokens, context_completion_tokens,
		context_model, context_updated_at, chat_model FROM go_chat_sessions WHERE id = ? AND owner_id = ?`,
		sessionID, ownerID).Scan(&state.ReasoningEffort, &prompt, &completion, &model, &updated, &state.ChatModel)
	if errors.Is(err, sql.ErrNoRows) {
		return chat.SessionContext{}, false, nil
	}
	if err != nil {
		return chat.SessionContext{}, false, fmt.Errorf("read chat session context: %w", err)
	}
	if prompt.Valid && completion.Valid {
		usage := &chat.ContextUsage{PromptTokens: int(prompt.Int64), CompletionTokens: int(completion.Int64), Model: model}
		if updated != "" {
			if usage.UpdatedAt, err = parseTime(updated); err != nil {
				return chat.SessionContext{}, false, err
			}
		}
		state.Usage = usage
	}
	latest, err := scanCompaction(r.db.QueryRowContext(ctx, `SELECT `+compactionColumns+` FROM chat_context_compactions
		WHERE owner_id = ? AND session_id = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`, ownerID, sessionID))
	switch {
	case err == nil:
		state.Latest = &latest
	case !errors.Is(err, sql.ErrNoRows):
		return chat.SessionContext{}, false, fmt.Errorf("read chat session compaction: %w", err)
	}
	return state, true, nil
}

// RecordContextUsage replaces the usage of the owner's session; nil records
// that the upstream reported none.
func (r *Repository) RecordContextUsage(ctx context.Context, ownerID, sessionID string, usage *chat.ContextUsage) error {
	var prompt, completion any
	model, updated := "", ""
	if usage != nil {
		prompt, completion, model = usage.PromptTokens, usage.CompletionTokens, usage.Model
		if !usage.UpdatedAt.IsZero() {
			updated = formatTime(usage.UpdatedAt)
		}
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE go_chat_sessions SET context_prompt_tokens = ?,
		context_completion_tokens = ?, context_model = ?, context_updated_at = ?
		WHERE id = ? AND owner_id = ?`, prompt, completion, model, updated, sessionID, ownerID); err != nil {
		return fmt.Errorf("record chat session context usage: %w", err)
	}
	return nil
}

// SaveCompaction stores the compaction if the session's newest one is still
// previousID, and clears the usage: it is unknown until the next request.
func (r *Repository) SaveCompaction(ctx context.Context, compaction chat.Compaction, previousID string) (chat.Compaction, error) {
	id, err := newCompactionID()
	if err != nil {
		return chat.Compaction{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return chat.Compaction{}, fmt.Errorf("begin chat compaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var owned int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM go_chat_sessions WHERE id = ? AND owner_id = ?`,
		compaction.SessionID, compaction.OwnerID).Scan(&owned); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return chat.Compaction{}, chat.ErrSessionNotFound
		}
		return chat.Compaction{}, fmt.Errorf("read chat compaction session: %w", err)
	}
	var newest string
	err = tx.QueryRowContext(ctx, `SELECT id FROM chat_context_compactions WHERE owner_id = ? AND session_id = ?
		ORDER BY created_at DESC, rowid DESC LIMIT 1`, compaction.OwnerID, compaction.SessionID).Scan(&newest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return chat.Compaction{}, fmt.Errorf("read newest chat compaction: %w", err)
	}
	if newest != previousID {
		return chat.Compaction{}, chat.ErrCompactionConflict
	}
	created := compaction.CreatedAt.UTC()
	if created.IsZero() {
		created = time.Now().UTC()
	}
	through := ""
	if !compaction.ThroughCreatedAt.IsZero() {
		through = formatTaskTime(compaction.ThroughCreatedAt)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO chat_context_compactions (`+compactionColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, compaction.OwnerID, compaction.SessionID, compaction.Trigger,
		compaction.Summary, compaction.ThroughMessageID, through, compaction.Rounds, formatTaskTime(created)); err != nil {
		return chat.Compaction{}, fmt.Errorf("insert chat compaction: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE go_chat_sessions SET context_prompt_tokens = NULL,
		context_completion_tokens = NULL, context_model = '', context_updated_at = ''
		WHERE id = ? AND owner_id = ?`, compaction.SessionID, compaction.OwnerID); err != nil {
		return chat.Compaction{}, fmt.Errorf("reset chat session context usage: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return chat.Compaction{}, fmt.Errorf("commit chat compaction: %w", err)
	}
	compaction.ID, compaction.CreatedAt = id, created
	return compaction, nil
}

// ListCompactions returns the newest compactions of the owner's session, at
// most limit, oldest first.
func (r *Repository) ListCompactions(ctx context.Context, ownerID, sessionID string, limit int) ([]chat.Compaction, error) {
	if limit <= 0 {
		limit = 1
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+compactionColumns+` FROM chat_context_compactions
		WHERE owner_id = ? AND session_id = ? ORDER BY created_at DESC, rowid DESC LIMIT ?`, ownerID, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("list chat compactions: %w", err)
	}
	defer rows.Close()
	var result []chat.Compaction
	for rows.Next() {
		compaction, err := scanCompaction(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, compaction)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result, nil
}

func scanCompaction(row interface{ Scan(...any) error }) (chat.Compaction, error) {
	var compaction chat.Compaction
	var through, created string
	if err := row.Scan(&compaction.ID, &compaction.OwnerID, &compaction.SessionID, &compaction.Trigger, &compaction.Summary,
		&compaction.ThroughMessageID, &through, &compaction.Rounds, &created); err != nil {
		return chat.Compaction{}, err
	}
	var err error
	if through != "" {
		if compaction.ThroughCreatedAt, err = parseTime(through); err != nil {
			return chat.Compaction{}, err
		}
	}
	if compaction.CreatedAt, err = parseTime(created); err != nil {
		return chat.Compaction{}, err
	}
	return compaction, nil
}
