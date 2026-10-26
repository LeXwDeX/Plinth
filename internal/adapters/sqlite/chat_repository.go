package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func (r *Repository) ListSessions(
	ctx context.Context,
	ownerID string,
) ([]chat.Session, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, s.owner_id, s.owner_name, s.title, s.created_at,
		       s.updated_at, COUNT(m.id), s.approval_mode, s.agent_scope, s.title_source
		FROM go_chat_sessions AS s
		LEFT JOIN go_chat_messages AS m ON m.session_id = s.id
		WHERE s.owner_id = ?
		GROUP BY s.id
		ORDER BY s.updated_at DESC`,
		ownerID,
	)
	if err != nil {
		return nil, fmt.Errorf("list chat sessions: %w", err)
	}
	defer rows.Close()
	var result []chat.Session
	for rows.Next() {
		session, err := scanChatSession(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := r.fillSessionCredits(ctx, ownerID, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Repository) ListSessionsPage(
	ctx context.Context,
	ownerID string,
	query chat.SessionListQuery,
) (chat.SessionPage, error) {
	pattern := "%" + escapeLike(strings.TrimSpace(query.Search)) + "%"
	// Count and page read in one explicit read transaction: the total repeats the owner and search
	// filter without the cursor, so an empty page after the cursor still reports the full list.
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return chat.SessionPage{}, fmt.Errorf("begin chat session page: %w", err)
	}
	defer tx.Rollback()
	var total int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM go_chat_sessions AS c
		WHERE c.owner_id = ? AND c.title LIKE ? ESCAPE '\' COLLATE NOCASE`,
		ownerID, pattern,
	).Scan(&total); err != nil {
		return chat.SessionPage{}, fmt.Errorf("count chat sessions: %w", err)
	}
	args := []any{ownerID, pattern}
	where := `s.owner_id = ? AND s.title LIKE ? ESCAPE '\' COLLATE NOCASE`
	if query.Cursor != "" {
		updatedAt, sessionID, err := decodeChatSessionCursor(query.Cursor)
		if err != nil {
			return chat.SessionPage{}, fmt.Errorf("%w: %v", chat.ErrInvalidSessionPage, err)
		}
		where += ` AND (s.updated_at < ? OR (s.updated_at = ? AND s.id < ?))`
		args = append(args, updatedAt, updatedAt, sessionID)
	}
	args = append(args, query.Limit+1)
	rows, err := tx.QueryContext(ctx, `
		SELECT s.id, s.owner_id, s.owner_name, s.title, s.created_at,
		       s.updated_at, COUNT(m.id), s.approval_mode, s.agent_scope, s.title_source
		FROM go_chat_sessions AS s
		LEFT JOIN go_chat_messages AS m ON m.session_id = s.id
		WHERE `+where+`
		GROUP BY s.id
		ORDER BY s.updated_at DESC, s.id DESC LIMIT ?`, args...)
	if err != nil {
		return chat.SessionPage{}, fmt.Errorf("page chat sessions: %w", err)
	}
	defer rows.Close()
	page := chat.SessionPage{Sessions: []chat.Session{}, Total: total}
	for rows.Next() {
		session, err := scanChatSession(rows)
		if err != nil {
			return chat.SessionPage{}, err
		}
		page.Sessions = append(page.Sessions, session)
	}
	if err := rows.Err(); err != nil {
		return chat.SessionPage{}, err
	}
	if len(page.Sessions) > query.Limit {
		page.Sessions = page.Sessions[:query.Limit]
		last := page.Sessions[len(page.Sessions)-1]
		page.NextCursor = encodeChatSessionCursor(last.UpdatedAt, last.ID)
	}
	if err := rows.Close(); err != nil {
		return chat.SessionPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return chat.SessionPage{}, fmt.Errorf("commit chat session page: %w", err)
	}
	// Credits use their own statement after the transaction; no read transaction is left open.
	if err := r.fillSessionCredits(ctx, ownerID, page.Sessions); err != nil {
		return chat.SessionPage{}, err
	}
	return page, nil
}

func encodeChatSessionCursor(updatedAt time.Time, sessionID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(formatTime(updatedAt) + "\x00" + sessionID))
}

func decodeChatSessionCursor(cursor string) (string, string, error) {
	if len(cursor) > 1024 {
		return "", "", errors.New("chat session cursor too long")
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(string(raw), "\x00", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", "", errors.New("invalid chat session cursor")
	}
	if _, err := parseTime(parts[0]); err != nil {
		return "", "", err
	}
	return parts[0], parts[1], nil
}

func (r *Repository) CreateSession(
	ctx context.Context,
	session chat.Session,
) (chat.Session, error) {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO go_chat_sessions (
			id, owner_id, owner_name, title, created_at, updated_at, approval_mode, agent_scope, title_source
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID,
		session.OwnerID,
		session.OwnerName,
		session.Title,
		formatTime(session.CreatedAt),
		formatTime(session.UpdatedAt),
		sessionApprovalModeOrManual(session.ApprovalMode),
		sessionAgentScopeOrAuto(session.AgentScope),
		sessionTitleSourceOrUser(session.TitleSource),
	)
	if err != nil {
		return chat.Session{}, fmt.Errorf("create chat session: %w", err)
	}
	return session, nil
}

// RenameSession keeps updated_at: only new messages move a session in the
// (updated_at, id) ordering, so a rename never reorders or re-pages the list.
func (r *Repository) RenameSession(
	ctx context.Context,
	ownerID string,
	sessionID string,
	title string,
	_ time.Time,
) (chat.Session, bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE go_chat_sessions
		SET title = ?, title_source = 'user'
		WHERE id = ? AND owner_id = ?`,
		title,
		sessionID,
		ownerID,
	)
	if err != nil {
		return chat.Session{}, false, fmt.Errorf("rename chat session: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return chat.Session{}, false, err
	}
	if affected == 0 {
		return chat.Session{}, false, nil
	}
	session, err := r.getChatSession(ctx, ownerID, sessionID)
	return session, true, err
}

// ApplyAutoTitle writes the automatic title only while the session's title is
// still pending. The condition is part of the UPDATE, so a rename that lands
// while the title is being generated always wins. It leaves updated_at alone:
// a summary is not new activity and must not reorder the session list.
func (r *Repository) ApplyAutoTitle(
	ctx context.Context,
	ownerID string,
	sessionID string,
	title string,
) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE go_chat_sessions
		SET title = ?, title_source = ?
		WHERE id = ? AND owner_id = ? AND title_source = ?`,
		title, chat.TitleSourceAuto, sessionID, ownerID, chat.TitleSourcePending,
	)
	if err != nil {
		return false, fmt.Errorf("apply auto title: %w", err)
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func sessionTitleSourceOrUser(source string) string {
	if source == "" {
		return chat.TitleSourceUser
	}
	return source
}

// SessionNotes reads the settings summary of the owner's own session.
func (r *Repository) SessionNotes(ctx context.Context, ownerID, sessionID string) (string, bool, error) {
	var notes string
	err := r.db.QueryRowContext(ctx, `
		SELECT notes FROM go_chat_sessions WHERE id = ? AND owner_id = ?`,
		sessionID, ownerID).Scan(&notes)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read chat session notes: %w", err)
	}
	return notes, true, nil
}

// SetSessionNotes replaces the settings summary of the owner's own session. It
// keeps updated_at so editing the summary does not reorder the history list.
func (r *Repository) SetSessionNotes(ctx context.Context, ownerID, sessionID, notes string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE go_chat_sessions SET notes = ? WHERE id = ? AND owner_id = ?`,
		notes, sessionID, ownerID)
	if err != nil {
		return false, fmt.Errorf("set chat session notes: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// SessionPick reads the candidate the owner picked for the session. A zero
// Attachment means no pick; found is false for a missing or foreign session.
func (r *Repository) SessionPick(ctx context.Context, ownerID, sessionID string) (chat.Attachment, bool, error) {
	var raw string
	err := r.db.QueryRowContext(ctx, `
		SELECT picked FROM go_chat_sessions WHERE id = ? AND owner_id = ?`,
		sessionID, ownerID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return chat.Attachment{}, false, nil
	}
	if err != nil {
		return chat.Attachment{}, false, fmt.Errorf("read chat session pick: %w", err)
	}
	if raw == "" {
		return chat.Attachment{}, true, nil
	}
	var pick chat.Attachment
	if err := json.Unmarshal([]byte(raw), &pick); err != nil {
		// A pick that cannot be read is no pick; it never fails the session.
		return chat.Attachment{}, true, nil
	}
	return pick, true, nil
}

// SetSessionPick replaces (or, for a zero Attachment, clears) the session's
// pick. It keeps updated_at so picking does not reorder the history list.
func (r *Repository) SetSessionPick(ctx context.Context, ownerID, sessionID string, pick chat.Attachment) (bool, error) {
	raw := ""
	if pick.Kind != "" {
		encoded, err := json.Marshal(chat.Attachment{
			Kind: pick.Kind, Role: pick.Role, AssetID: pick.AssetID,
			TaskID: pick.TaskID, CaptureID: pick.CaptureID,
		})
		if err != nil {
			return false, fmt.Errorf("encode chat session pick: %w", err)
		}
		raw = string(encoded)
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE go_chat_sessions SET picked = ? WHERE id = ? AND owner_id = ?`,
		raw, sessionID, ownerID)
	if err != nil {
		return false, fmt.Errorf("set chat session pick: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func sessionApprovalModeOrManual(mode string) string {
	if mode == "auto" {
		return mode
	}
	return "manual"
}

// sessionAgentScopeOrAuto stores an empty scope as the migration default.
// The Service has already checked a requested scope against the Schema.
func sessionAgentScopeOrAuto(scope string) string {
	if scope == "" {
		return "auto"
	}
	return scope
}

// SessionAgentScope reads the Agent scope of the owner's session. found is
// false for a missing or foreign session.
func (r *Repository) SessionAgentScope(ctx context.Context, ownerID, sessionID string) (string, bool, error) {
	var scope string
	err := r.db.QueryRowContext(ctx, `
		SELECT agent_scope FROM go_chat_sessions WHERE id = ? AND owner_id = ?`,
		sessionID, ownerID).Scan(&scope)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read chat session agent scope: %w", err)
	}
	return scope, true, nil
}

// SetSessionAgentScope updates only the owner's own session. Like the approval
// mode it keeps updated_at, so a scope switch does not reorder the history.
func (r *Repository) SetSessionAgentScope(
	ctx context.Context,
	ownerID string,
	sessionID string,
	scope string,
) (chat.Session, bool, error) {
	if scope == "" {
		return chat.Session{}, false, chat.ErrInvalidAgentScope
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE go_chat_sessions SET agent_scope = ?
		WHERE id = ? AND owner_id = ?`, scope, sessionID, ownerID)
	if err != nil {
		return chat.Session{}, false, fmt.Errorf("set chat session agent scope: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return chat.Session{}, false, err
	}
	if affected == 0 {
		return chat.Session{}, false, nil
	}
	session, err := r.getChatSession(ctx, ownerID, sessionID)
	if err != nil {
		return chat.Session{}, false, err
	}
	return r.sessionWithCredits(ctx, session)
}

// SetSessionApprovalMode updates only the owner's own session. It keeps
// updated_at so a mode switch does not reorder the history list.
func (r *Repository) SetSessionApprovalMode(
	ctx context.Context,
	ownerID string,
	sessionID string,
	mode string,
) (chat.Session, bool, error) {
	if mode != "auto" && mode != "manual" {
		return chat.Session{}, false, chat.ErrInvalidApprovalMode
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE go_chat_sessions SET approval_mode = ?
		WHERE id = ? AND owner_id = ?`, mode, sessionID, ownerID)
	if err != nil {
		return chat.Session{}, false, fmt.Errorf("set chat session approval mode: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return chat.Session{}, false, err
	}
	if affected == 0 {
		return chat.Session{}, false, nil
	}
	session, err := r.getChatSession(ctx, ownerID, sessionID)
	if err != nil {
		return chat.Session{}, false, err
	}
	return r.sessionWithCredits(ctx, session)
}

func (r *Repository) sessionWithCredits(ctx context.Context, session chat.Session) (chat.Session, bool, error) {
	list := []chat.Session{session}
	if err := r.fillSessionCredits(ctx, session.OwnerID, list); err != nil {
		return chat.Session{}, false, err
	}
	return list[0], true, nil
}

// fillSessionCredits sets EstimatedCredits for each session to the sum of the
// estimated credits of the tasks the session accepted. The estimate is computed
// from the frozen Offering and input stored with each task, so a later catalog
// price change never rewrites history. It is owner-scoped.
//
// Only tasks that were or may still be charged count (#324), by the rule the
// usage statistics share (taskMayBeChargedSQL, #327).
func (r *Repository) fillSessionCredits(ctx context.Context, ownerID string, sessions []chat.Session) error {
	if len(sessions) == 0 {
		return nil
	}
	index := make(map[string][]int, len(sessions))
	args := []any{ownerID}
	placeholders := make([]string, 0, len(sessions))
	for position, session := range sessions {
		index[session.ID] = append(index[session.ID], position)
		placeholders = append(placeholders, "?")
		args = append(args, session.ID)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT task.session_id, task.offering_json, task.input_json, task.native_quote_json FROM generation_tasks AS task
		WHERE task.owner_id = ? AND task.session_id IN (`+strings.Join(placeholders, ",")+`)
			AND `+taskMayBeChargedSQL("task"), args...)
	if err != nil {
		return fmt.Errorf("sum chat session credits: %w", err)
	}
	defer rows.Close()
	accumulators := map[string]*schema.PriceAccumulator{}
	for rows.Next() {
		var quoteJSON sql.NullString
		var sessionID, offeringJSON, inputJSON string
		if err := rows.Scan(&sessionID, &offeringJSON, &inputJSON, &quoteJSON); err != nil {
			return err
		}
		var offering generation.Offering
		if err := json.Unmarshal([]byte(offeringJSON), &offering); err != nil {
			return err
		}
		var q *schema.NativeQuote
		if quoteJSON.Valid {
			if err := json.Unmarshal([]byte(quoteJSON.String), &q); err != nil {
				return err
			}
			if err := q.Validate(); err != nil {
				return err
			}
		} else if offering.NativePricing != nil {
			return fmt.Errorf("native session task quote missing")
		}
		credits := 0
		if q == nil {
			credits = offering.EstimateTotalCredits(json.RawMessage(inputJSON))
		}
		a := accumulators[sessionID]
		if a == nil {
			a = &schema.PriceAccumulator{}
			accumulators[sessionID] = a
		}
		if err := a.Add(credits, q); err != nil {
			return err
		}

	}
	if err := rows.Err(); err != nil {
		return err
	}
	for id, a := range accumulators {
		totals, e := a.Totals()
		if e != nil {
			return e
		}
		for _, position := range index[id] {
			sessions[position].EstimatedCredits = a.Legacy
			sessions[position].PriceTotals = totals
		}
	}
	return nil
}

func (r *Repository) DeleteSession(
	ctx context.Context,
	ownerID string,
	sessionID string,
) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin chat session deletion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM go_canvas_projects WHERE owner_id = ? AND session_id = ?`,
		ownerID, sessionID,
	); err != nil {
		return false, fmt.Errorf("delete chat session canvas: %w", err)
	}
	deleted, err := deleteSessionAndDependents(ctx, tx, ownerID, sessionID)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit chat session deletion: %w", err)
	}
	return deleted, nil
}

// sessionDependentDeletes clears the session-scoped tables that carry no
// foreign key to go_chat_sessions (tables with ON DELETE CASCADE clear
// themselves). Every path that deletes a chat session runs these in the same
// transaction so no deletion path leaves orphans behind (#334); a new
// session-scoped table without a cascading foreign key belongs here.
var sessionDependentDeletes = []struct {
	label string
	query string
}{
	{"agent state", `DELETE FROM go_agent_state WHERE owner_id = ? AND scope = ?`},
	{"choices", `DELETE FROM chat_choices WHERE owner_id = ? AND session_id = ?`},
	{"compactions", `DELETE FROM chat_context_compactions WHERE owner_id = ? AND session_id = ?`},
}

// deleteSessionAndDependents deletes one owner's chat session together with
// its session-scoped data inside the caller's transaction and reports whether
// the session row existed.
func deleteSessionAndDependents(
	ctx context.Context,
	tx *sql.Tx,
	ownerID string,
	sessionID string,
) (bool, error) {
	for _, dependent := range sessionDependentDeletes {
		if _, err := tx.ExecContext(ctx, dependent.query, ownerID, sessionID); err != nil {
			return false, fmt.Errorf("delete chat session %s: %w", dependent.label, err)
		}
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM go_chat_sessions WHERE id = ? AND owner_id = ?`,
		sessionID,
		ownerID,
	)
	if err != nil {
		return false, fmt.Errorf("delete chat session: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("chat session deletion rows: %w", err)
	}
	return affected > 0, nil
}

func (r *Repository) OwnsSession(
	ctx context.Context,
	ownerID string,
	sessionID string,
) (bool, error) {
	var found int
	err := r.db.QueryRowContext(ctx, `
		SELECT 1 FROM go_chat_sessions WHERE id = ? AND owner_id = ?`,
		sessionID,
		ownerID,
	).Scan(&found)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, fmt.Errorf("own chat session: %w", err)
	}
}

func (r *Repository) ListMessages(
	ctx context.Context,
	ownerID string,
	sessionID string,
) ([]chat.Message, error) {
	owned, err := r.OwnsSession(ctx, ownerID, sessionID)
	if err != nil {
		return nil, err
	}
	if !owned {
		return nil, chat.ErrSessionNotFound
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, session_id, role, content, image_urls, tool_calls, attachments, created_at
		FROM go_chat_messages
		WHERE session_id = ?
		ORDER BY created_at ASC, rowid ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list chat messages: %w", err)
	}
	defer rows.Close()
	var result []chat.Message
	for rows.Next() {
		message, err := scanChatMessage(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, message)
	}
	return result, rows.Err()
}

// ListMessagesPage reads the newest query.Limit messages before query.Before
// in the ListMessages order (created_at, then rowid) and returns them oldest
// first. The cursor names the oldest message of the page by that same key, so
// a later page continues exactly where this one stopped even when newer
// messages arrive in between.
func (r *Repository) ListMessagesPage(
	ctx context.Context,
	ownerID string,
	sessionID string,
	query chat.MessageListQuery,
) (chat.MessagePage, error) {
	owned, err := r.OwnsSession(ctx, ownerID, sessionID)
	if err != nil {
		return chat.MessagePage{}, err
	}
	if !owned {
		return chat.MessagePage{}, chat.ErrSessionNotFound
	}
	args := []any{sessionID}
	where := `session_id = ?`
	if query.Before != "" {
		createdAt, rowID, err := decodeChatMessageCursor(query.Before)
		if err != nil {
			return chat.MessagePage{}, fmt.Errorf("%w: %v", chat.ErrInvalidMessagePage, err)
		}
		where += ` AND (created_at < ? OR (created_at = ? AND rowid < ?))`
		args = append(args, createdAt, createdAt, rowID)
	}
	args = append(args, query.Limit+1)
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, session_id, role, content, image_urls, tool_calls, attachments, created_at, rowid
		FROM go_chat_messages
		WHERE `+where+`
		ORDER BY created_at DESC, rowid DESC LIMIT ?`, args...)
	if err != nil {
		return chat.MessagePage{}, fmt.Errorf("page chat messages: %w", err)
	}
	defer rows.Close()
	messages := []chat.Message{}
	rowIDs := []int64{}
	for rows.Next() {
		var rowID int64
		message, err := scanChatMessage(rowIDScanner{rows: rows, rowID: &rowID})
		if err != nil {
			return chat.MessagePage{}, err
		}
		messages = append(messages, message)
		rowIDs = append(rowIDs, rowID)
	}
	if err := rows.Err(); err != nil {
		return chat.MessagePage{}, err
	}
	page := chat.MessagePage{}
	if len(messages) > query.Limit {
		messages = messages[:query.Limit]
		oldest := len(messages) - 1
		page.NextCursor = encodeChatMessageCursor(messages[oldest].CreatedAt, rowIDs[oldest])
	}
	slices.Reverse(messages)
	page.Messages = messages
	return page, nil
}

// rowIDScanner reads a chat message row followed by its rowid.
type rowIDScanner struct {
	rows  *sql.Rows
	rowID *int64
}

func (s rowIDScanner) Scan(dest ...any) error {
	return s.rows.Scan(append(dest, s.rowID)...)
}

func encodeChatMessageCursor(createdAt time.Time, rowID int64) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(formatTime(createdAt) + "\x00" + strconv.FormatInt(rowID, 10)),
	)
}

func decodeChatMessageCursor(cursor string) (string, int64, error) {
	if len(cursor) > 1024 {
		return "", 0, errors.New("chat message cursor too long")
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", 0, err
	}
	parts := strings.SplitN(string(raw), "\x00", 2)
	if len(parts) != 2 {
		return "", 0, errors.New("invalid chat message cursor")
	}
	if _, err := parseTime(parts[0]); err != nil {
		return "", 0, err
	}
	rowID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || rowID < 1 {
		return "", 0, errors.New("invalid chat message cursor")
	}
	return parts[0], rowID, nil
}

// InsertMessageIfAbsent adds one message to the owner's session outside a
// turn. The insert and the existence check of the session are one statement,
// so a foreign session never receives it.
func (r *Repository) InsertMessageIfAbsent(
	ctx context.Context,
	ownerID string,
	message chat.Message,
	updatedAt time.Time,
) (bool, error) {
	imagesJSON, err := json.Marshal(message.ImageURLs)
	if err != nil {
		return false, err
	}
	toolsJSON, err := marshalToolCalls(message.ToolCalls)
	if err != nil {
		return false, err
	}
	attachmentsJSON, err := marshalAttachments(message.Attachments)
	if err != nil {
		return false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO go_chat_messages (
			id, session_id, role, content, image_urls, tool_calls, attachments, created_at
		)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?
		WHERE EXISTS (SELECT 1 FROM go_chat_sessions WHERE id = ? AND owner_id = ?)
		ON CONFLICT(id) DO NOTHING`,
		message.ID, message.SessionID, message.Role, message.Content,
		string(imagesJSON), toolsJSON, attachmentsJSON, formatTime(message.CreatedAt),
		message.SessionID, ownerID)
	if err != nil {
		return false, fmt.Errorf("insert chat message: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		// Either the session is not the owner's, or the message exists. Only
		// a message of this very session counts as already posted.
		var found int
		err := tx.QueryRowContext(ctx, `
			SELECT 1 FROM go_chat_messages m
			JOIN go_chat_sessions s ON s.id = m.session_id
			WHERE m.id = ? AND m.session_id = ? AND s.owner_id = ?`,
			message.ID, message.SessionID, ownerID).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return false, chat.ErrSessionNotFound
		}
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE go_chat_sessions SET updated_at = ? WHERE id = ?`,
		formatTime(updatedAt), message.SessionID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Repository) StartTurn(
	ctx context.Context,
	ownerID string,
	user chat.Message,
	assistant chat.Message,
	event chat.Event,
	stream chat.Stream,
	turn chat.Turn,
) (chat.Message, chat.Event, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return chat.Message{}, chat.Event{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO go_chat_streams (session_id, assistant_message_id, status, cleanup_at)
		SELECT ?, ?, ?, NULL
		WHERE EXISTS (SELECT 1 FROM go_chat_sessions WHERE id = ? AND owner_id = ?)
		ON CONFLICT(session_id) DO UPDATE SET
			assistant_message_id = excluded.assistant_message_id,
			status = excluded.status, cleanup_at = NULL
		WHERE go_chat_streams.status <> excluded.status
		 OR NOT EXISTS (SELECT 1 FROM go_chat_turns WHERE assistant_message_id = go_chat_streams.assistant_message_id)`,
		stream.SessionID, assistant.ID, stream.Status, user.SessionID, ownerID,
	)
	if err != nil {
		return chat.Message{}, chat.Event{}, err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return chat.Message{}, chat.Event{}, err
	} else if affected == 0 {
		var found int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM go_chat_sessions WHERE id = ? AND owner_id = ?`, user.SessionID, ownerID).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return chat.Message{}, chat.Event{}, chat.ErrSessionNotFound
		}
		if err != nil {
			return chat.Message{}, chat.Event{}, err
		}
		return chat.Message{}, chat.Event{}, chat.ErrTurnActive
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM go_chat_events WHERE session_id = ?`, user.SessionID); err != nil {
		return chat.Message{}, chat.Event{}, err
	}
	if err := insertChatMessage(ctx, tx, user); err != nil {
		return chat.Message{}, chat.Event{}, err
	}
	if err := insertChatMessage(ctx, tx, assistant); err != nil {
		return chat.Message{}, chat.Event{}, err
	}
	optionsJSON, err := json.Marshal(turn.Options)
	if err != nil {
		return chat.Message{}, chat.Event{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO go_chat_turns (
			assistant_message_id, session_id, owner_id, options_json,
			state, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		turn.AssistantMessageID,
		turn.SessionID,
		turn.OwnerID,
		string(optionsJSON),
		turn.State,
		formatTime(turn.CreatedAt),
		formatTime(turn.UpdatedAt),
	); err != nil {
		return chat.Message{}, chat.Event{}, err
	}
	storedEvent, err := appendChatEvent(ctx, tx, event)
	if err != nil {
		return chat.Message{}, chat.Event{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE go_chat_sessions SET updated_at = ? WHERE id = ?`,
		formatTime(assistant.CreatedAt),
		user.SessionID,
	); err != nil {
		return chat.Message{}, chat.Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return chat.Message{}, chat.Event{}, err
	}
	return assistant, storedEvent, nil
}

// ListSessionTurns 只列指定会话的活动轮次（B16 读路径修复：
// ListMessages 不再触发跨会话全局扫描）。
func (r *Repository) ListSessionTurns(
	ctx context.Context,
	ownerID string,
	sessionID string,
	states []string,
) ([]chat.Turn, error) {
	return r.listTurnsFiltered(ctx, states, " AND owner_id = ? AND session_id = ?", ownerID, sessionID)
}

// RecentSessionTurnOptions returns the options of the session's newest turns,
// newest first, at most limit of them, in any state (#318).
func (r *Repository) RecentSessionTurnOptions(
	ctx context.Context,
	ownerID string,
	sessionID string,
	limit int,
) ([]chat.TurnOptions, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT options_json FROM go_chat_turns
		WHERE session_id = ? AND owner_id = ?
		ORDER BY created_at DESC, rowid DESC
		LIMIT ?`, sessionID, ownerID, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent chat turn options: %w", err)
	}
	defer rows.Close()
	var result []chat.TurnOptions
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return nil, err
		}
		var options chat.TurnOptions
		if err := json.Unmarshal([]byte(encoded), &options); err != nil {
			return nil, fmt.Errorf("decode chat turn options: %w", err)
		}
		result = append(result, options)
	}
	return result, rows.Err()
}

func (r *Repository) ListTurns(
	ctx context.Context,
	states []string,
) ([]chat.Turn, error) {
	return r.listTurnsFiltered(ctx, states, "", "")
}

func (r *Repository) listTurnsFiltered(
	ctx context.Context,
	states []string,
	extraWhere string,
	extraArgs ...any,
) ([]chat.Turn, error) {
	if len(states) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(states))
	arguments := make([]any, len(states))
	for index, state := range states {
		placeholders[index] = "?"
		arguments[index] = state
	}
	arguments = append(arguments, extraArgs...)
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT owner_id, session_id, assistant_message_id, options_json,
		        state, stop_requested, checkpoint_json, checkpoint_version, created_at, updated_at
		 FROM go_chat_turns
		 WHERE state IN (`+strings.Join(placeholders, ",")+")"+extraWhere+`
		 ORDER BY created_at ASC`,
		arguments...,
	)
	if err != nil {
		return nil, fmt.Errorf("list chat turns: %w", err)
	}
	defer rows.Close()
	var result []chat.Turn
	for rows.Next() {
		var turn chat.Turn
		var optionsJSON string
		var stopRequested int
		var checkpointJSON string
		var createdAt string
		var updatedAt string
		if err := rows.Scan(
			&turn.OwnerID,
			&turn.SessionID,
			&turn.AssistantMessageID,
			&optionsJSON,
			&turn.State,
			&stopRequested,
			&checkpointJSON,
			&turn.CheckpointVersion,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(
			[]byte(optionsJSON),
			&turn.Options,
		); err != nil {
			return nil, fmt.Errorf("decode chat turn options: %w", err)
		}
		turn.StopRequested = stopRequested != 0
		if err := json.Unmarshal([]byte(checkpointJSON), &turn.Checkpoint); err != nil {
			return nil, fmt.Errorf("decode chat turn checkpoint: %w", err)
		}
		var err error
		turn.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return nil, err
		}
		turn.UpdatedAt, err = parseTime(updatedAt)
		if err != nil {
			return nil, err
		}
		result = append(result, turn)
	}
	return result, rows.Err()
}

// RequestTurnStop records the owner/session-scoped stop intent atomically
// before the caller cancels a worker. A terminal turn cannot be marked again.
func (r *Repository) RequestTurnStop(
	ctx context.Context, ownerID, sessionID string, activeStates []string, updatedAt time.Time,
) (chat.Turn, bool, error) {
	if ownerID == "" || sessionID == "" || len(activeStates) == 0 {
		return chat.Turn{}, false, nil
	}
	placeholders := make([]string, len(activeStates))
	args := []any{formatTime(updatedAt), ownerID, sessionID}
	for index, state := range activeStates {
		placeholders[index] = "?"
		args = append(args, state)
	}
	args = append(args, ownerID, sessionID)
	for _, state := range activeStates {
		args = append(args, state)
	}
	query := `UPDATE go_chat_turns SET stop_requested = 1, updated_at = ?
		WHERE assistant_message_id = (
			SELECT assistant_message_id FROM go_chat_turns
			WHERE owner_id = ? AND session_id = ? AND state IN (` + strings.Join(placeholders, ",") + `)
			ORDER BY created_at DESC LIMIT 1
		) AND owner_id = ? AND session_id = ? AND state IN (` + strings.Join(placeholders, ",") + `)
		RETURNING assistant_message_id, options_json, state`
	var turn chat.Turn
	var optionsJSON string
	err := r.db.QueryRowContext(ctx, query, args...).Scan(&turn.AssistantMessageID, &optionsJSON, &turn.State)
	if errors.Is(err, sql.ErrNoRows) {
		return chat.Turn{}, false, nil
	}
	if err != nil {
		return chat.Turn{}, false, fmt.Errorf("request chat turn stop: %w", err)
	}
	if err := json.Unmarshal([]byte(optionsJSON), &turn.Options); err != nil {
		return chat.Turn{}, false, fmt.Errorf("decode stopped chat turn options: %w", err)
	}
	turn.OwnerID, turn.SessionID, turn.StopRequested = ownerID, sessionID, true
	return turn, true, nil
}

func (r *Repository) TurnStopRequested(ctx context.Context, ownerID, sessionID, assistantMessageID string) (bool, error) {
	var requested int
	err := r.db.QueryRowContext(ctx, `SELECT stop_requested FROM go_chat_turns
		WHERE owner_id = ? AND session_id = ? AND assistant_message_id = ?`,
		ownerID, sessionID, assistantMessageID).Scan(&requested)
	if errors.Is(err, sql.ErrNoRows) {
		return false, chat.ErrMessageNotFound
	}
	return requested != 0, err
}

func (r *Repository) UpdateTurnState(
	ctx context.Context,
	assistantMessageID string,
	state string,
	updatedAt time.Time,
) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE go_chat_turns SET state = ?, updated_at = ?
		WHERE assistant_message_id = ?`,
		state,
		formatTime(updatedAt),
		assistantMessageID,
	)
	if err != nil {
		return fmt.Errorf("update chat turn state: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return chat.ErrMessageNotFound
	}
	return nil
}

func (r *Repository) DeleteMessage(
	ctx context.Context,
	ownerID string,
	sessionID string,
	messageID string,
) (bool, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()
	var found int
	err = tx.QueryRowContext(ctx, `
		SELECT 1 FROM go_chat_sessions WHERE id = ? AND owner_id = ?`,
		sessionID,
		ownerID,
	).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM go_chat_messages WHERE id = ? AND session_id = ?
		AND NOT EXISTS (SELECT 1 FROM go_chat_streams WHERE session_id = go_chat_messages.session_id
		 AND assistant_message_id = go_chat_messages.id AND cleanup_at IS NULL)`,
		messageID,
		sessionID,
	)
	if err != nil {
		return true, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return true, false, err
	}
	if affected == 0 {
		var found int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM go_chat_messages WHERE id=? AND session_id=?`, messageID, sessionID).Scan(&found); err == nil {
			return true, false, chat.ErrTurnActive
		} else if !errors.Is(err, sql.ErrNoRows) {
			return true, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return true, false, err
	}
	return true, affected > 0, nil
}

func (r *Repository) UpdateMessage(
	ctx context.Context,
	ownerID string,
	message chat.Message,
	updatedAt time.Time,
) (bool, error) {
	imagesJSON, err := json.Marshal(message.ImageURLs)
	if err != nil {
		return false, err
	}
	toolsJSON, err := marshalToolCalls(message.ToolCalls)
	if err != nil {
		return false, err
	}
	attachmentsJSON, err := marshalAttachments(message.Attachments)
	if err != nil {
		return false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE go_chat_messages
		SET role = ?, content = ?, image_urls = ?, tool_calls = ?, attachments = ?
		WHERE id = ? AND session_id = ?
		  AND EXISTS (
			SELECT 1 FROM go_chat_sessions
			WHERE id = ? AND owner_id = ?
		  )`,
		message.Role,
		message.Content,
		string(imagesJSON),
		toolsJSON,
		attachmentsJSON,
		message.ID,
		message.SessionID,
		message.SessionID,
		ownerID,
	)
	if err != nil {
		return false, fmt.Errorf("update chat message: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE go_chat_sessions SET updated_at = ? WHERE id = ?`,
		formatTime(updatedAt),
		message.SessionID,
	); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Repository) AppendEvent(
	ctx context.Context,
	event chat.Event,
) (chat.Event, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return chat.Event{}, err
	}
	defer tx.Rollback()
	stored, err := appendChatEvent(ctx, tx, event)
	if err != nil {
		return chat.Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return chat.Event{}, err
	}
	return stored, nil
}

func (r *Repository) ListEvents(
	ctx context.Context,
	ownerID string,
	sessionID string,
	fromID int64,
) ([]chat.Event, bool, error) {
	owned, err := r.OwnsSession(ctx, ownerID, sessionID)
	if err != nil || !owned {
		return nil, owned, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT event_id, session_id, data_json, created_at
		FROM go_chat_events
		WHERE session_id = ? AND event_id >= ?
		ORDER BY event_id ASC`,
		sessionID,
		fromID,
	)
	if err != nil {
		return nil, true, fmt.Errorf("list chat events: %w", err)
	}
	defer rows.Close()
	var result []chat.Event
	for rows.Next() {
		var event chat.Event
		var dataJSON string
		var createdAt string
		if err := rows.Scan(
			&event.ID,
			&event.SessionID,
			&dataJSON,
			&createdAt,
		); err != nil {
			return nil, true, err
		}
		if err := json.Unmarshal([]byte(dataJSON), &event.Data); err != nil {
			return nil, true, fmt.Errorf("decode chat event: %w", err)
		}
		event.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return nil, true, err
		}
		result = append(result, event)
	}
	return result, true, rows.Err()
}

func (r *Repository) ReadStream(
	ctx context.Context,
	ownerID string,
	sessionID string,
	fromID int64,
	now time.Time,
) (chat.Stream, []chat.Event, bool, bool, error) {
	owned, err := r.OwnsSession(ctx, ownerID, sessionID)
	if err != nil || !owned {
		return chat.Stream{}, nil, owned, false, err
	}
	var stream chat.Stream
	var cleanupAt sql.NullString
	err = r.db.QueryRowContext(ctx, `
		SELECT session_id, status, cleanup_at
		FROM go_chat_streams WHERE session_id = ?`,
		sessionID,
	).Scan(&stream.SessionID, &stream.Status, &cleanupAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return chat.Stream{}, nil, true, false, nil
	case err != nil:
		return chat.Stream{}, nil, true, false, err
	}
	if cleanupAt.Valid {
		parsed, err := parseTime(cleanupAt.String)
		if err != nil {
			return chat.Stream{}, nil, true, false, err
		}
		stream.CleanupAt = &parsed
		if !parsed.After(now) {
			return chat.Stream{}, nil, true, false, nil
		}
	}
	events, _, err := r.ListEvents(ctx, ownerID, sessionID, fromID)
	return stream, events, true, true, err
}

func (r *Repository) FinishStream(
	ctx context.Context,
	sessionID string,
	assistantMessageID string,
	status string,
	cleanupAt time.Time,
) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE go_chat_streams
		SET status = ?, cleanup_at = ?
		WHERE session_id = ? AND assistant_message_id = ?`,
		status,
		formatTime(cleanupAt),
		sessionID,
		assistantMessageID,
	)
	if err != nil {
		return fmt.Errorf("finish chat stream: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return chat.ErrStreamNotFound
	}
	return nil
}

// Session 读取 owner 自己的单个会话（B16 标题限流用，
// 替代旧的 ListSessions 全量扫描）。
func (r *Repository) Session(
	ctx context.Context,
	ownerID string,
	sessionID string,
) (chat.Session, bool, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT s.id, s.owner_id, s.owner_name, s.title, s.created_at,
		       s.updated_at, COUNT(m.id), s.approval_mode, s.agent_scope, s.title_source
		FROM go_chat_sessions AS s
		LEFT JOIN go_chat_messages AS m ON m.session_id = s.id
		WHERE s.id = ? AND s.owner_id = ?
		GROUP BY s.id`,
		sessionID,
		ownerID,
	)
	session, err := scanChatSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return chat.Session{}, false, nil
	}
	if err != nil {
		return chat.Session{}, false, err
	}
	return r.sessionWithCredits(ctx, session)
}

func (r *Repository) getChatSession(
	ctx context.Context,
	ownerID string,
	sessionID string,
) (chat.Session, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT s.id, s.owner_id, s.owner_name, s.title, s.created_at,
		       s.updated_at, COUNT(m.id), s.approval_mode, s.agent_scope, s.title_source
		FROM go_chat_sessions AS s
		LEFT JOIN go_chat_messages AS m ON m.session_id = s.id
		WHERE s.id = ? AND s.owner_id = ?
		GROUP BY s.id`,
		sessionID,
		ownerID,
	)
	return scanChatSession(row)
}

type chatSessionScanner interface {
	Scan(...any) error
}

func scanChatSession(scanner chatSessionScanner) (chat.Session, error) {
	var session chat.Session
	var createdAt string
	var updatedAt string
	if err := scanner.Scan(
		&session.ID,
		&session.OwnerID,
		&session.OwnerName,
		&session.Title,
		&createdAt,
		&updatedAt,
		&session.Messages,
		&session.ApprovalMode,
		&session.AgentScope,
		&session.TitleSource,
	); err != nil {
		return chat.Session{}, err
	}
	var err error
	session.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return chat.Session{}, err
	}
	session.UpdatedAt, err = parseTime(updatedAt)
	return session, err
}

// marshalAttachments stores a message's structured attachments; an absent list
// is the empty JSON array so the column stays decodable.
func marshalAttachments(attachments []chat.Attachment) (string, error) {
	if len(attachments) == 0 {
		return "[]", nil
	}
	encoded, err := json.Marshal(attachments)
	if err != nil {
		return "", fmt.Errorf("encode chat attachments: %w", err)
	}
	return string(encoded), nil
}

type chatMessageScanner interface {
	Scan(...any) error
}

func scanChatMessage(scanner chatMessageScanner) (chat.Message, error) {
	var message chat.Message
	var imagesJSON string
	var toolsJSON string
	var attachmentsJSON string
	var createdAt string
	if err := scanner.Scan(
		&message.ID,
		&message.SessionID,
		&message.Role,
		&message.Content,
		&imagesJSON,
		&toolsJSON,
		&attachmentsJSON,
		&createdAt,
	); err != nil {
		return chat.Message{}, err
	}
	if err := json.Unmarshal([]byte(imagesJSON), &message.ImageURLs); err != nil {
		return chat.Message{}, fmt.Errorf("decode chat images: %w", err)
	}
	if err := unmarshalToolCalls(toolsJSON, &message.ToolCalls); err != nil {
		return chat.Message{}, err
	}
	if err := json.Unmarshal([]byte(attachmentsJSON), &message.Attachments); err != nil {
		return chat.Message{}, fmt.Errorf("decode chat attachments: %w", err)
	}
	var err error
	message.CreatedAt, err = parseTime(createdAt)
	return message, err
}

func insertChatMessage(
	ctx context.Context,
	tx *sql.Tx,
	message chat.Message,
) error {
	imagesJSON, err := json.Marshal(message.ImageURLs)
	if err != nil {
		return err
	}
	toolsJSON, err := marshalToolCalls(message.ToolCalls)
	if err != nil {
		return err
	}
	attachmentsJSON, err := marshalAttachments(message.Attachments)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO go_chat_messages (
			id, session_id, role, content, image_urls, tool_calls, attachments, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		message.ID,
		message.SessionID,
		message.Role,
		message.Content,
		string(imagesJSON),
		toolsJSON,
		attachmentsJSON,
		formatTime(message.CreatedAt),
	)
	return err
}

func appendChatEvent(
	ctx context.Context,
	tx *sql.Tx,
	event chat.Event,
) (chat.Event, error) {
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(event_id) + 1, 0)
		FROM go_chat_events WHERE session_id = ?`,
		event.SessionID,
	).Scan(&event.ID); err != nil {
		return chat.Event{}, err
	}
	dataJSON, err := json.Marshal(event.Data)
	if err != nil {
		return chat.Event{}, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO go_chat_events (
			session_id, event_id, data_json, created_at
		) VALUES (?, ?, ?, ?)`,
		event.SessionID,
		event.ID,
		string(dataJSON),
		formatTime(event.CreatedAt),
	)
	return event, err
}

type storedToolCall struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Arguments      string `json:"arguments"`
	TaskID         string `json:"task_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	// EstimatedCredits and ApprovalSource are stored with the call when its
	// paid step is accepted; older rows simply omit them.
	EstimatedCredits int                     `json:"estimated_credits,omitempty"`
	NativeQuote      *generation.NativeQuote `json:"native_quote,omitempty"`
	ApprovalSource   string                  `json:"approval_source,omitempty"`
}

func marshalToolCalls(calls []chat.ToolCall) (string, error) {
	stored := make([]storedToolCall, 0, len(calls))
	for _, call := range calls {
		stored = append(stored, storedToolCall{
			ID:               call.ID,
			Name:             call.Name,
			Arguments:        string(call.Arguments),
			TaskID:           call.TaskID,
			IdempotencyKey:   call.IdempotencyKey,
			EstimatedCredits: call.EstimatedCredits,
			NativeQuote:      call.NativeQuote,
			ApprovalSource:   call.ApprovalSource,
		})
	}
	encoded, err := json.Marshal(stored)
	return string(encoded), err
}

func unmarshalToolCalls(raw string, target *[]chat.ToolCall) error {
	var stored []storedToolCall
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return fmt.Errorf("decode chat tools: %w", err)
	}
	result := make([]chat.ToolCall, 0, len(stored))
	for _, call := range stored {
		result = append(result, chat.ToolCall{
			ID:               call.ID,
			Name:             call.Name,
			Arguments:        []byte(call.Arguments),
			TaskID:           call.TaskID,
			IdempotencyKey:   call.IdempotencyKey,
			EstimatedCredits: call.EstimatedCredits,
			NativeQuote:      call.NativeQuote,
			ApprovalSource:   call.ApprovalSource,
		})
	}
	*target = result
	return nil
}

// SaveAgentState / DeleteAgentState / LoadAgentState 实现
// agentparams.StatePersister：会话级 agent 状态（参数、绑定、同意）
// 写穿透到 SQLite，重启后同意门禁与参数仍然生效。
func (r *Repository) SaveAgentState(
	ownerID string,
	scope string,
	key string,
	value json.RawMessage,
) error {
	_, err := r.db.ExecContext(context.Background(), `
		INSERT INTO go_agent_state (owner_id, scope, state_key, value_json, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (owner_id, scope, state_key) DO UPDATE SET
			value_json = excluded.value_json,
			updated_at = excluded.updated_at`,
		ownerID, scope, key, string(value), time.Now(),
	)
	if err != nil {
		return fmt.Errorf("save agent state: %w", err)
	}
	return nil
}

func (r *Repository) DeleteAgentState(
	ownerID string,
	scope string,
	keys ...string,
) error {
	if len(keys) == 0 {
		if _, err := r.db.ExecContext(context.Background(), `
			DELETE FROM go_agent_state WHERE owner_id = ? AND scope = ?`,
			ownerID, scope,
		); err != nil {
			return fmt.Errorf("delete agent state scope: %w", err)
		}
		return nil
	}
	for _, key := range keys {
		if _, err := r.db.ExecContext(context.Background(), `
			DELETE FROM go_agent_state
			WHERE owner_id = ? AND scope = ? AND state_key = ?`,
			ownerID, scope, key,
		); err != nil {
			return fmt.Errorf("delete agent state key: %w", err)
		}
	}
	return nil
}

func (r *Repository) LoadAgentState(
	ownerID string,
	scope string,
) (map[string]json.RawMessage, error) {
	rows, err := r.db.QueryContext(context.Background(), `
		SELECT state_key, value_json FROM go_agent_state
		WHERE owner_id = ? AND scope = ?`,
		ownerID, scope,
	)
	if err != nil {
		return nil, fmt.Errorf("load agent state: %w", err)
	}
	defer rows.Close()
	state := map[string]json.RawMessage{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan agent state: %w", err)
		}
		state[key] = json.RawMessage(value)
	}
	return state, rows.Err()
}
