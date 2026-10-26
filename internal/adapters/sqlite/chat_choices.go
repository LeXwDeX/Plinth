package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// The in-chat choice cards of a session (#321). Every statement is owner and
// session scoped, so a card of anyone else reads as missing. Times are stored
// fixed-width (formatTaskTime) so ORDER BY created_at is time order.

const choiceColumns = `id, owner_id, session_id, assistant_message_id, call_id, tool_name, capability, step_index,
	options_json, default_option_id, allow_default, default_arguments, state, answer_option_id, answer_default,
	answer_source, answer_message_id, created_at, answered_at`

func newChoiceID() (string, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "choice_" + hex.EncodeToString(random), nil
}

func scanChoice(row interface{ Scan(...any) error }) (chat.Choice, error) {
	var choice chat.Choice
	var options, defaults, state, created, answered string
	var allowDefault, answerDefault int
	if err := row.Scan(&choice.ID, &choice.OwnerID, &choice.SessionID, &choice.AssistantMessageID, &choice.CallID,
		&choice.ToolName, &choice.Capability, &choice.StepIndex, &options, &choice.DefaultOptionID, &allowDefault,
		&defaults, &state, &choice.AnswerOptionID, &answerDefault, &choice.AnswerSource, &choice.AnswerMessageID,
		&created, &answered); err != nil {
		return chat.Choice{}, err
	}
	if err := json.Unmarshal([]byte(options), &choice.Options); err != nil {
		return chat.Choice{}, fmt.Errorf("decode chat choice options: %w", err)
	}
	choice.DefaultArguments = json.RawMessage(defaults)
	choice.AllowDefault, choice.AnswerDefault = allowDefault != 0, answerDefault != 0
	choice.State = chat.ChoiceState(state)
	choice.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if answered != "" {
		choice.AnsweredAt, _ = time.Parse(time.RFC3339Nano, answered)
	}
	return choice, nil
}

// CreateChoice stores a pending card and supersedes the session's other
// pending cards. The same tool call (a replayed turn) returns its stored card.
func (r *Repository) CreateChoice(ctx context.Context, choice chat.Choice) (chat.Choice, error) {
	options, err := json.Marshal(choice.Options)
	if err != nil {
		return chat.Choice{}, fmt.Errorf("encode chat choice options: %w", err)
	}
	defaults := string(choice.DefaultArguments)
	if defaults == "" {
		defaults = "{}"
	}
	id, err := newChoiceID()
	if err != nil {
		return chat.Choice{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return chat.Choice{}, fmt.Errorf("begin chat choice: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existing, err := scanChoice(tx.QueryRowContext(ctx, `SELECT `+choiceColumns+` FROM chat_choices
		WHERE owner_id = ? AND session_id = ? AND assistant_message_id = ? AND call_id = ?`,
		choice.OwnerID, choice.SessionID, choice.AssistantMessageID, choice.CallID))
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return chat.Choice{}, fmt.Errorf("read chat choice: %w", err)
	}
	var owned int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM go_chat_sessions WHERE id = ? AND owner_id = ?`,
		choice.SessionID, choice.OwnerID).Scan(&owned); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return chat.Choice{}, chat.ErrSessionNotFound
		}
		return chat.Choice{}, fmt.Errorf("read chat choice session: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chat_choices SET state = ?
		WHERE owner_id = ? AND session_id = ? AND state = ?`,
		string(chat.ChoiceSuperseded), choice.OwnerID, choice.SessionID, string(chat.ChoicePending)); err != nil {
		return chat.Choice{}, fmt.Errorf("supersede chat choices: %w", err)
	}
	created := choice.CreatedAt.UTC()
	if created.IsZero() {
		created = time.Now().UTC()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO chat_choices (`+choiceColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', 0, '', '', ?, '')`,
		id, choice.OwnerID, choice.SessionID, choice.AssistantMessageID, choice.CallID, choice.ToolName,
		choice.Capability, choice.StepIndex, string(options), choice.DefaultOptionID, boolInt(choice.AllowDefault),
		defaults, string(chat.ChoicePending), formatTaskTime(created)); err != nil {
		return chat.Choice{}, fmt.Errorf("insert chat choice: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return chat.Choice{}, fmt.Errorf("commit chat choice: %w", err)
	}
	choice.ID, choice.State, choice.CreatedAt = id, chat.ChoicePending, created
	choice.DefaultArguments = json.RawMessage(defaults)
	return choice, nil
}

// ListChoices returns the session's newest cards first, at most limit.
func (r *Repository) ListChoices(ctx context.Context, ownerID, sessionID string, limit int) ([]chat.Choice, error) {
	if limit <= 0 {
		limit = 1
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+choiceColumns+` FROM chat_choices
		WHERE owner_id = ? AND session_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, ownerID, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("list chat choices: %w", err)
	}
	defer rows.Close()
	var choices []chat.Choice
	for rows.Next() {
		choice, err := scanChoice(rows)
		if err != nil {
			return nil, err
		}
		choices = append(choices, choice)
	}
	return choices, rows.Err()
}

func (r *Repository) GetChoice(ctx context.Context, ownerID, sessionID, choiceID string) (chat.Choice, error) {
	choice, err := scanChoice(r.db.QueryRowContext(ctx, `SELECT `+choiceColumns+` FROM chat_choices
		WHERE id = ? AND owner_id = ? AND session_id = ?`, choiceID, ownerID, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return chat.Choice{}, chat.ErrChoiceNotFound
	}
	if err != nil {
		return chat.Choice{}, fmt.Errorf("read chat choice: %w", err)
	}
	return choice, nil
}

// AnswerChoice answers a pending card once. The option must be one of the
// card's; a closed card is ErrChoiceClosed and nothing changes.
func (r *Repository) AnswerChoice(ctx context.Context, ownerID, sessionID, choiceID string, answer chat.ChoiceRecord) (chat.Choice, error) {
	choice, err := r.GetChoice(ctx, ownerID, sessionID, choiceID)
	if err != nil {
		return chat.Choice{}, err
	}
	if choice.State != chat.ChoicePending {
		return chat.Choice{}, chat.ErrChoiceClosed
	}
	if _, ok := choice.Option(answer.OptionID); !ok {
		return chat.Choice{}, chat.ErrChoiceRejected
	}
	answered := answer.AnsweredAt.UTC()
	if answered.IsZero() {
		answered = time.Now().UTC()
	}
	result, err := r.db.ExecContext(ctx, `UPDATE chat_choices SET state = ?, answer_option_id = ?, answer_default = ?,
		answer_source = ?, answer_message_id = ?, answered_at = ?
		WHERE id = ? AND owner_id = ? AND session_id = ? AND state = ?`,
		string(chat.ChoiceAnswered), answer.OptionID, boolInt(answer.UseDefault), answer.Source, answer.MessageID,
		formatTaskTime(answered), choiceID, ownerID, sessionID, string(chat.ChoicePending))
	if err != nil {
		return chat.Choice{}, fmt.Errorf("answer chat choice: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return chat.Choice{}, chat.ErrChoiceClosed
	}
	choice.State, choice.AnswerOptionID, choice.AnswerDefault = chat.ChoiceAnswered, answer.OptionID, answer.UseDefault
	choice.AnswerSource, choice.AnswerMessageID, choice.AnsweredAt = answer.Source, answer.MessageID, answered
	return choice, nil
}

// ReopenChoice reverts the answer recorded for one message (a turn that did
// not start), so the card can be answered again.
func (r *Repository) ReopenChoice(ctx context.Context, ownerID, sessionID, choiceID, messageID string) error {
	if messageID == "" {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `UPDATE chat_choices SET state = ?, answer_option_id = '', answer_default = 0,
		answer_source = '', answer_message_id = '', answered_at = ''
		WHERE id = ? AND owner_id = ? AND session_id = ? AND state = ? AND answer_message_id = ?`,
		string(chat.ChoicePending), choiceID, ownerID, sessionID, string(chat.ChoiceAnswered), messageID)
	if err != nil {
		return fmt.Errorf("reopen chat choice: %w", err)
	}
	return nil
}
