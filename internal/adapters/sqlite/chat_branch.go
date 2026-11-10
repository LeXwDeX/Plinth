package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

func (r *Repository) BranchSession(ctx context.Context, request chat.BranchRequest) (chat.Session, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return chat.Session{}, err
	}
	defer tx.Rollback()
	var title, created string
	var rowID int64
	err = tx.QueryRowContext(ctx, `SELECT s.title,m.created_at,m.rowid FROM go_chat_sessions s JOIN go_chat_messages m ON m.session_id=s.id WHERE s.id=? AND s.owner_id=? AND m.id=?`, request.SessionID, request.OwnerID, request.MessageID).Scan(&title, &created, &rowID)
	if errors.Is(err, sql.ErrNoRows) {
		return chat.Session{}, chat.ErrSessionNotFound
	}
	if err != nil {
		return chat.Session{}, err
	}
	var exists int
	if len(request.ActiveStates) != 3 {
		return chat.Session{}, chat.ErrBranchUnavailable
	}
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM go_chat_turns WHERE owner_id=? AND session_id=? AND state IN (?,?,?))`, request.OwnerID, request.SessionID, request.ActiveStates[0], request.ActiveStates[1], request.ActiveStates[2]).Scan(&exists)
	if err != nil {
		return chat.Session{}, err
	}
	if exists != 0 {
		return chat.Session{}, chat.ErrTurnActive
	}
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM execution_grants WHERE owner_id=? AND session_id=? AND status='pending') OR EXISTS(SELECT 1 FROM production_budgets WHERE owner_id=? AND session_id=? AND state='pending')`, request.OwnerID, request.SessionID, request.OwnerID, request.SessionID).Scan(&exists)
	if err != nil {
		return chat.Session{}, err
	}
	if exists != 0 {
		return chat.Session{}, chat.ErrBranchPendingApproval
	}
	if request.NewID == nil || request.NewSessionID == "" || !strings.Contains(request.TitleTemplate, "{title}") {
		return chat.Session{}, chat.ErrBranchUnavailable
	}
	branchTitle := strings.ReplaceAll(request.TitleTemplate, "{title}", title)
	// 分支可读直接父会话及更早祖先的 Task（只读 source_task/lineage 授权）。
	// 压缩记录不复制，后续从完整原始消息重新压缩。
	_, err = tx.ExecContext(ctx, `INSERT INTO go_chat_sessions (id,owner_id,owner_name,title,created_at,updated_at,approval_mode,agent_scope,title_source,notes,brief,chat_model,reasoning_effort,picked,style_lock_enabled,style_lock_text,style_lock_references,visual_anchors,parent_session_id,parent_message_id,parent_title,parent_deleted)
		SELECT ?,owner_id,owner_name,?,?,?,approval_mode,agent_scope,?,notes,brief,chat_model,reasoning_effort,picked,style_lock_enabled,style_lock_text,style_lock_references,visual_anchors,id,?,title,0 FROM go_chat_sessions WHERE id=? AND owner_id=?`, request.NewSessionID, branchTitle, formatTime(request.At), formatTime(request.At), chat.TitleSourceAuto, request.MessageID, request.SessionID, request.OwnerID)
	if err != nil {
		return chat.Session{}, fmt.Errorf("insert branch session: %w", err)
	}
	if err := insertBranchTaskRefs(ctx, tx, request.OwnerID, request.SessionID, request.NewSessionID, formatTime(request.At)); err != nil {
		return chat.Session{}, err
	}
	// The current 3D stage and its captures are copied with fresh IDs. Both
	// are fail-closed: evidence the owner or the source session cannot read
	// refuses the whole branch with ErrBranchScene instead of dropping rows.
	captureRemap, capturesCopied, err := branchCaptureCopy(ctx, tx, request)
	if err != nil {
		return chat.Session{}, err
	}
	sceneCopied, err := branchSceneCopy(ctx, tx, request)
	if err != nil {
		return chat.Session{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,attachments FROM go_chat_messages WHERE session_id=? AND (created_at<? OR (created_at=? AND rowid<=?)) ORDER BY created_at,rowid`, request.SessionID, created, created, rowID)
	if err != nil {
		return chat.Session{}, err
	}
	type sourceMessage struct {
		id, attachments string
	}
	var messages []sourceMessage
	for rows.Next() {
		var message sourceMessage
		if err := rows.Scan(&message.id, &message.attachments); err != nil {
			rows.Close()
			return chat.Session{}, err
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return chat.Session{}, err
	}
	if err := rows.Close(); err != nil {
		return chat.Session{}, err
	}
	for _, message := range messages {
		attachments, err := remapCaptureIDs(message.attachments, captureRemap)
		if err != nil {
			return chat.Session{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO go_chat_messages(id,session_id,role,content,image_urls,tool_calls,attachments,multiview,created_at) SELECT ?,?,role,content,image_urls,tool_calls,?,multiview,created_at FROM go_chat_messages WHERE id=? AND session_id=?`, request.NewID("message"), request.NewSessionID, attachments, message.id, request.SessionID)
		if err != nil {
			return chat.Session{}, fmt.Errorf("copy branch message: %w", err)
		}
	}
	// Neither answered nor unanswered choices are copied; they refer to old message IDs.
	var canvasID, canvasOwner string
	err = tx.QueryRowContext(ctx, `SELECT id,owner_id FROM go_canvas_projects WHERE session_id=?`, request.SessionID).Scan(&canvasID, &canvasOwner)
	canvasCopied := false
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return chat.Session{}, fmt.Errorf("read branch canvas: %w", err)
	}
	if err == nil {
		if canvasOwner != request.OwnerID || request.CanvasProjectIDPrefix == "" {
			return chat.Session{}, chat.ErrBranchUnavailable
		}
		newCanvasID := request.NewID(request.CanvasProjectIDPrefix)
		// The branch session already exists independently of its canvas.
		_, err = tx.ExecContext(ctx, `INSERT INTO go_canvas_projects (id,owner_id,name,session_id,owns_session,viewport,bg_style,created_at,updated_at,revision,placed_results_json,canvas_width,canvas_height,coordinate_version)
			SELECT ?,owner_id,name,?,0,viewport,bg_style,created_at,updated_at,revision,placed_results_json,canvas_width,canvas_height,coordinate_version FROM go_canvas_projects WHERE id=? AND owner_id=?`, newCanvasID, request.NewSessionID, canvasID, request.OwnerID)
		if err != nil {
			return chat.Session{}, fmt.Errorf("copy branch canvas: %w", err)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO go_canvas_nodes (project_id,id,type,x,y,width,height,z_index,data_json,visible,locked,created_at,updated_at)
			SELECT ?,id,type,x,y,width,height,z_index,data_json,visible,locked,created_at,updated_at FROM go_canvas_nodes WHERE project_id=? ORDER BY z_index,rowid`, newCanvasID, canvasID)
		if err != nil {
			return chat.Session{}, fmt.Errorf("copy branch canvas nodes: %w", err)
		}
		canvasCopied = true
	}
	row := tx.QueryRowContext(ctx, `SELECT s.id,s.owner_id,s.owner_name,s.title,s.created_at,s.updated_at,COUNT(m.id),s.approval_mode,s.agent_scope,s.title_source,s.brief,s.explicit_model_3d,s.style_lock_enabled,s.style_lock_text,s.style_lock_references,s.visual_anchors,s.parent_session_id,s.parent_message_id,s.parent_title,s.parent_deleted FROM go_chat_sessions s LEFT JOIN go_chat_messages m ON m.session_id=s.id WHERE s.id=? AND s.owner_id=? GROUP BY s.id`, request.NewSessionID, request.OwnerID)
	session, err := scanChatSession(row)
	if err != nil {
		return chat.Session{}, err
	}
	session.BranchCanvasCopied = canvasCopied
	session.BranchSceneCopied = sceneCopied
	session.BranchCapturesCopied = capturesCopied > 0
	if err := tx.Commit(); err != nil {
		return chat.Session{}, err
	}
	return session, nil
}
