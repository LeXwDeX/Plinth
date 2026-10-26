package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

type ChatImportStats struct {
	Sessions ImportStats
	Messages ImportStats
}

// ImportChatHistory 在一个事务中按旧稳定 ID 幂等导入会话与消息。
func (r *Repository) ImportChatHistory(
	ctx context.Context,
	sessions []chat.Session,
	messages []chat.Message,
) (ChatImportStats, error) {
	var result ChatImportStats
	sessionStats, err := r.importETLBatches(
		ctx,
		"chat_session",
		len(sessions),
		func(tx *sql.Tx, start int, end int) (ImportStats, error) {
			var stats ImportStats
			for _, session := range sessions[start:end] {
				claimed, err := claimETLImport(
					ctx,
					tx,
					"chat_session",
					session.ID,
				)
				if err != nil {
					return stats, err
				}
				if !claimed {
					stats.Skipped++
					continue
				}
				if _, err := tx.ExecContext(ctx, `
			INSERT INTO go_chat_sessions (
				id, owner_id, owner_name, title, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				owner_id = excluded.owner_id,
				owner_name = excluded.owner_name,
				title = excluded.title,
				created_at = excluded.created_at,
				updated_at = excluded.updated_at`,
					session.ID,
					session.OwnerID,
					session.OwnerName,
					session.Title,
					formatTime(session.CreatedAt),
					formatTime(session.UpdatedAt),
				); err != nil {
					return stats, fmt.Errorf(
						"导入聊天会话 %s: %w",
						session.ID,
						err,
					)
				}
				stats.Migrated++
			}
			return stats, nil
		},
	)
	if err != nil {
		return result, err
	}
	result.Sessions = sessionStats
	messageStats, err := r.importETLBatches(
		ctx,
		"chat_message",
		len(messages),
		func(tx *sql.Tx, start int, end int) (ImportStats, error) {
			var stats ImportStats
			for _, message := range messages[start:end] {
				claimed, err := claimETLImport(
					ctx,
					tx,
					"chat_message",
					message.ID,
				)
				if err != nil {
					return stats, err
				}
				if !claimed {
					stats.Skipped++
					continue
				}
				imagesJSON, err := json.Marshal(message.ImageURLs)
				if err != nil {
					return stats, fmt.Errorf(
						"编码聊天消息 %s 图片: %w",
						message.ID,
						err,
					)
				}
				toolsJSON, err := marshalToolCalls(message.ToolCalls)
				if err != nil {
					return stats, fmt.Errorf(
						"编码聊天消息 %s 工具调用: %w",
						message.ID,
						err,
					)
				}
				if _, err := tx.ExecContext(ctx, `
			INSERT INTO go_chat_messages (
				id, session_id, role, content, image_urls, tool_calls,
				created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				session_id = excluded.session_id,
				role = excluded.role,
				content = excluded.content,
				image_urls = excluded.image_urls,
				tool_calls = excluded.tool_calls,
				created_at = excluded.created_at`,
					message.ID,
					message.SessionID,
					message.Role,
					message.Content,
					string(imagesJSON),
					toolsJSON,
					formatTime(message.CreatedAt),
				); err != nil {
					return stats, fmt.Errorf(
						"导入聊天消息 %s: %w",
						message.ID,
						err,
					)
				}
				stats.Migrated++
			}
			return stats, nil
		},
	)
	if err != nil {
		return result, err
	}
	result.Messages = messageStats
	return result, nil
}
