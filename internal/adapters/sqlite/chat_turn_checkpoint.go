package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

func (r *Repository) GetTurn(ctx context.Context, ownerID, sessionID, assistantID string) (chat.Turn, error) {
	var turn chat.Turn
	var optionsJSON, checkpointJSON, createdAt, updatedAt string
	var stopRequested int
	err := r.db.QueryRowContext(ctx, `SELECT owner_id, session_id, assistant_message_id,
		options_json, state, stop_requested, checkpoint_json, checkpoint_version,
		created_at, updated_at FROM go_chat_turns
		WHERE owner_id = ? AND session_id = ? AND assistant_message_id = ?`,
		ownerID, sessionID, assistantID).Scan(&turn.OwnerID, &turn.SessionID,
		&turn.AssistantMessageID, &optionsJSON, &turn.State, &stopRequested,
		&checkpointJSON, &turn.CheckpointVersion, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return chat.Turn{}, chat.ErrMessageNotFound
	}
	if err != nil {
		return chat.Turn{}, err
	}
	if err := json.Unmarshal([]byte(optionsJSON), &turn.Options); err != nil {
		return chat.Turn{}, fmt.Errorf("decode chat turn options: %w", err)
	}
	if err := json.Unmarshal([]byte(checkpointJSON), &turn.Checkpoint); err != nil {
		return chat.Turn{}, fmt.Errorf("decode chat turn checkpoint: %w", err)
	}
	turn.StopRequested = stopRequested != 0
	turn.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return chat.Turn{}, err
	}
	turn.UpdatedAt, err = parseTime(updatedAt)
	return turn, err
}

// ListTurnsPage uses a stable (created_at, id) cursor so each sweep is bounded.
func (r *Repository) ListTurnsPage(ctx context.Context, states []string, cursor chat.TurnPageCursor, limit int) ([]chat.Turn, error) {
	if len(states) == 0 || limit <= 0 {
		return nil, nil
	}
	args := make([]any, 0, len(states)+4)
	placeholders := make([]string, len(states))
	for i, state := range states {
		placeholders[i] = "?"
		args = append(args, state)
	}
	boundary := ""
	if !cursor.CreatedAt.IsZero() {
		boundary = ` AND (created_at > ? OR (created_at = ? AND assistant_message_id > ?))`
		stamp := formatTime(cursor.CreatedAt)
		args = append(args, stamp, stamp, cursor.AssistantMessageID)
	}
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, `SELECT owner_id, session_id, assistant_message_id FROM go_chat_turns
		WHERE state IN (`+strings.Join(placeholders, ",")+`)`+boundary+`
		ORDER BY created_at, assistant_message_id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	type identity struct{ owner, session, assistant string }
	var ids []identity
	for rows.Next() {
		var id identity
		if err := rows.Scan(&id.owner, &id.session, &id.assistant); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := make([]chat.Turn, 0, len(ids))
	for _, id := range ids {
		turn, err := r.GetTurn(ctx, id.owner, id.session, id.assistant)
		if errors.Is(err, chat.ErrMessageNotFound) { // concurrent session deletion
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, turn)
	}
	return result, nil
}

func (r *Repository) CompareAndSwapTurnCheckpoint(ctx context.Context, ownerID, sessionID, assistantID string,
	version int64, oldState, newState string, checkpoint chat.TurnCheckpoint, allowStopped bool, now time.Time,
) (int64, error) {
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		return 0, err
	}
	allow := 0
	if allowStopped {
		allow = 1
	}
	var next int64
	err = r.db.QueryRowContext(ctx, `UPDATE go_chat_turns SET checkpoint_json = ?,
		checkpoint_version = checkpoint_version + 1, state = ?, updated_at = ?
		WHERE owner_id = ? AND session_id = ? AND assistant_message_id = ?
		AND checkpoint_version = ? AND state = ? AND (stop_requested = 0 OR ? = 1)
		RETURNING checkpoint_version`, string(encoded), newState, formatTime(now), ownerID,
		sessionID, assistantID, version, oldState, allow).Scan(&next)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, chat.ErrTurnCheckpointConflict
	}
	return next, err
}
