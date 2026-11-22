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

// turnColumns is the column list every read of a whole turn selects, in scan
// order. A caller that needs only the identity or the options reads those
// columns alone instead (#382).
const turnColumns = `owner_id, session_id, assistant_message_id, options_json,
	state, end_reason, stop_requested, checkpoint_json, checkpoint_version, created_at, updated_at`

// scanTurn decodes one row read with turnColumns.
func scanTurn(scan func(dest ...any) error) (chat.Turn, error) {
	var turn chat.Turn
	var optionsJSON, checkpointJSON, createdAt, updatedAt string
	var stopRequested int
	err := scan(&turn.OwnerID, &turn.SessionID, &turn.AssistantMessageID, &optionsJSON,
		&turn.State, &turn.EndReason, &stopRequested, &checkpointJSON,
		&turn.CheckpointVersion, &createdAt, &updatedAt)
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
	if turn.CreatedAt, err = parseTime(createdAt); err != nil {
		return chat.Turn{}, err
	}
	turn.UpdatedAt, err = parseTime(updatedAt)
	return turn, err
}

func (r *Repository) GetTurn(ctx context.Context, ownerID, sessionID, assistantID string) (chat.Turn, error) {
	turn, err := scanTurn(r.db.QueryRowContext(ctx, `SELECT `+turnColumns+`
		FROM go_chat_turns
		WHERE owner_id = ? AND session_id = ? AND assistant_message_id = ?`,
		ownerID, sessionID, assistantID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return chat.Turn{}, chat.ErrMessageNotFound
	}
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
	// One statement for the page, not an identity query plus one read per
	// turn: a sweep of 64 waiting turns used to decode 64 checkpoints (#382).
	rows, err := r.db.QueryContext(ctx, `SELECT `+turnColumns+` FROM go_chat_turns
		WHERE state IN (`+strings.Join(placeholders, ",")+`)`+boundary+`
		ORDER BY created_at, assistant_message_id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]chat.Turn, 0, limit)
	for rows.Next() {
		turn, err := scanTurn(rows.Scan)
		if err != nil {
			return nil, err
		}
		result = append(result, turn)
	}
	return result, rows.Err()
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
