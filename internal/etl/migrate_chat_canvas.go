package etl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/canvas"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

type chatMigrationReport struct {
	sessionSource  int64
	sessionLoaded  int
	messageSource  int64
	messageLoaded  int
	sessionSkipped []string
	messageSkipped []string
}

type canvasMigrationReport struct {
	projectSource  int64
	projectLoaded  int
	nodeSource     int64
	nodeLoaded     int
	projectSkipped []string
	nodeSkipped    []string
	linkDegraded   []string
	archives       []sqlite.LegacyMigrationRecordImport
}

func (r canvasMigrationReport) String(
	projectCount int,
	nodeCount int,
) string {
	var report strings.Builder
	fmt.Fprintf(
		&report,
		"Canvas 域: 项目源 %d 行 / 迁移 %d 行 / 跳过 %d 行；"+
			"节点源 %d 行 / 迁移 %d 行 / 跳过 %d 行\n",
		r.projectSource,
		projectCount,
		len(r.projectSkipped),
		r.nodeSource,
		nodeCount,
		len(r.nodeSkipped),
	)
	for _, issue := range r.projectSkipped {
		fmt.Fprintf(&report, "Canvas 项目跳过: %s\n", issue)
	}
	for _, issue := range r.nodeSkipped {
		fmt.Fprintf(&report, "Canvas 节点跳过: %s\n", issue)
	}
	for _, issue := range r.linkDegraded {
		fmt.Fprintf(&report, "Canvas 会话关联归档: %s\n", issue)
	}
	return report.String()
}

func (r chatMigrationReport) String(
	sessionCount int,
	messageCount int,
) string {
	var report strings.Builder
	fmt.Fprintf(
		&report,
		"聊天域: 会话源 %d 行 / 迁移 %d 行 / 跳过 %d 行；"+
			"消息源 %d 行 / 迁移 %d 行 / 跳过 %d 行\n",
		r.sessionSource,
		sessionCount,
		len(r.sessionSkipped),
		r.messageSource,
		messageCount,
		len(r.messageSkipped),
	)
	for _, issue := range r.sessionSkipped {
		fmt.Fprintf(&report, "聊天会话跳过: %s\n", issue)
	}
	for _, issue := range r.messageSkipped {
		fmt.Fprintf(&report, "聊天消息跳过: %s\n", issue)
	}
	return report.String()
}

func loadLegacyChat(
	ctx context.Context,
	legacy legacyQuery,
) ([]chat.Session, []chat.Message, chatMigrationReport, error) {
	var report chatMigrationReport
	if err := legacy.QueryRow(
		`SELECT COUNT(*) FROM chat_sessions`,
	).Scan(&report.sessionSource); err != nil {
		return nil, nil, report, fmt.Errorf("统计旧 chat_sessions: %w", err)
	}
	if err := legacy.QueryRow(
		`SELECT COUNT(*) FROM chat_messages`,
	).Scan(&report.messageSource); err != nil {
		return nil, nil, report, fmt.Errorf("统计旧 chat_messages: %w", err)
	}
	sessionRows, err := legacy.QueryContext(ctx, `
		SELECT CAST(id AS TEXT), COALESCE(userId, ''),
		       COALESCE(userName, ''), COALESCE(title, ''),
		       createdAt, updatedAt
		FROM chat_sessions
		ORDER BY id`)
	if err != nil {
		return nil, nil, report, fmt.Errorf("读取旧 chat_sessions: %w", err)
	}
	sessions := make([]chat.Session, 0, report.sessionSource)
	sessionIDs := map[string]bool{}
	for sessionRows.Next() {
		report.sessionLoaded++
		var session chat.Session
		var createdValue, updatedValue any
		if err := sessionRows.Scan(
			&session.ID,
			&session.OwnerID,
			&session.OwnerName,
			&session.Title,
			&createdValue,
			&updatedValue,
		); err != nil {
			_ = sessionRows.Close()
			return nil, nil, report, fmt.Errorf("读取旧聊天会话行: %w", err)
		}
		session.CreatedAt, err = parseLegacyTime(createdValue)
		if err != nil {
			_ = sessionRows.Close()
			return nil, nil, report, fmt.Errorf(
				"聊天会话 %s 的 createdAt 无法解析: %w",
				session.ID,
				err,
			)
		}
		session.UpdatedAt, err = parseLegacyTime(updatedValue)
		if err != nil {
			_ = sessionRows.Close()
			return nil, nil, report, fmt.Errorf(
				"聊天会话 %s 的 updatedAt 无法解析: %w",
				session.ID,
				err,
			)
		}
		sessions = append(sessions, session)
		sessionIDs[session.ID] = true
	}
	if err := sessionRows.Err(); err != nil {
		_ = sessionRows.Close()
		return nil, nil, report, fmt.Errorf("遍历旧 chat_sessions: %w", err)
	}
	if err := sessionRows.Close(); err != nil {
		return nil, nil, report, fmt.Errorf("关闭旧 chat_sessions 游标: %w", err)
	}

	messageRows, err := legacy.QueryContext(ctx, `
		SELECT CAST(id AS TEXT), CAST(sessionId AS TEXT),
		       COALESCE(role, ''), COALESCE(content, ''),
		       COALESCE(imageUrls, '[]'), COALESCE(toolCalls, '[]'),
		       createdAt
		FROM chat_messages
		ORDER BY createdAt, rowid`)
	if err != nil {
		return nil, nil, report, fmt.Errorf("读取旧 chat_messages: %w", err)
	}
	defer messageRows.Close()
	messages := make([]chat.Message, 0, report.messageSource)
	for messageRows.Next() {
		report.messageLoaded++
		var message chat.Message
		var imageURLsJSON, toolCallsJSON string
		var createdValue any
		if err := messageRows.Scan(
			&message.ID,
			&message.SessionID,
			&message.Role,
			&message.Content,
			&imageURLsJSON,
			&toolCallsJSON,
			&createdValue,
		); err != nil {
			return nil, nil, report, fmt.Errorf("读取旧聊天消息行: %w", err)
		}
		if !sessionIDs[message.SessionID] {
			report.messageSkipped = append(
				report.messageSkipped,
				fmt.Sprintf("%s: 会话 %s 不存在", message.ID, message.SessionID),
			)
			continue
		}
		if err := json.Unmarshal(
			[]byte(imageURLsJSON),
			&message.ImageURLs,
		); err != nil {
			return nil, nil, report, fmt.Errorf(
				"聊天消息 %s 的 imageUrls 无法解析: %w",
				message.ID,
				err,
			)
		}
		message.ToolCalls, err = parseLegacyToolCalls(
			message.ID,
			toolCallsJSON,
		)
		if err != nil {
			return nil, nil, report, err
		}
		message.CreatedAt, err = parseLegacyTime(createdValue)
		if err != nil {
			return nil, nil, report, fmt.Errorf(
				"聊天消息 %s 的 createdAt 无法解析: %w",
				message.ID,
				err,
			)
		}
		messages = append(messages, message)
	}
	if err := messageRows.Err(); err != nil {
		return nil, nil, report, fmt.Errorf("遍历旧 chat_messages: %w", err)
	}
	return sessions, messages, report, nil
}

func parseLegacyToolCalls(
	messageID string,
	raw string,
) ([]chat.ToolCall, error) {
	var records []map[string]any
	if err := json.Unmarshal([]byte(raw), &records); err != nil {
		return nil, fmt.Errorf(
			"聊天消息 %s 的 toolCalls 无法解析: %w",
			messageID,
			err,
		)
	}
	calls := make([]chat.ToolCall, 0, len(records))
	for index, record := range records {
		id, _ := record["id"].(string)
		name, _ := record["name"].(string)
		arguments := legacyToolArguments(record["arguments"])
		if function, ok := record["function"].(map[string]any); ok {
			if name == "" {
				name, _ = function["name"].(string)
			}
			if len(arguments) == 0 {
				arguments = legacyToolArguments(function["arguments"])
			}
		}
		taskID, _ := record["taskId"].(string)
		if taskID == "" {
			taskID, _ = record["task_id"].(string)
		}
		if id == "" {
			id = fmt.Sprintf("%s-tool-%d", messageID, index+1)
		}
		calls = append(calls, chat.ToolCall{
			ID:        id,
			Name:      name,
			Arguments: arguments,
			TaskID:    taskID,
		})
	}
	return calls, nil
}

func legacyToolArguments(value any) []byte {
	switch typed := value.(type) {
	case string:
		return []byte(typed)
	case nil:
		return nil
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return nil
		}
		return encoded
	}
}

func loadLegacyCanvas(
	ctx context.Context,
	legacy legacyQuery,
) ([]canvas.Project, []canvas.Node, canvasMigrationReport, error) {
	var report canvasMigrationReport
	if err := legacy.QueryRow(
		`SELECT COUNT(*) FROM canvas_projects`,
	).Scan(&report.projectSource); err != nil {
		return nil, nil, report, fmt.Errorf("统计旧 canvas_projects: %w", err)
	}
	if err := legacy.QueryRow(
		`SELECT COUNT(*) FROM canvas_nodes`,
	).Scan(&report.nodeSource); err != nil {
		return nil, nil, report, fmt.Errorf("统计旧 canvas_nodes: %w", err)
	}
	sessionIDs, err := legacyIDSet(ctx, legacy, "chat_sessions")
	if err != nil {
		return nil, nil, report, err
	}
	projectRows, err := legacy.QueryContext(ctx, `
		SELECT CAST(id AS TEXT), COALESCE(userId, ''),
		       COALESCE(name, ''), COALESCE(sessionId, ''),
		       COALESCE(viewport, '{}'), COALESCE(bgStyle, ''),
		       createdAt, updatedAt
		FROM canvas_projects
		ORDER BY id`)
	if err != nil {
		return nil, nil, report, fmt.Errorf("读取旧 canvas_projects: %w", err)
	}
	projects := make([]canvas.Project, 0, report.projectSource)
	projectIDs := map[string]bool{}
	for projectRows.Next() {
		report.projectLoaded++
		var project canvas.Project
		var createdValue, updatedValue any
		if err := projectRows.Scan(
			&project.ID,
			&project.OwnerID,
			&project.Name,
			&project.SessionID,
			&project.Viewport,
			&project.BGStyle,
			&createdValue,
			&updatedValue,
		); err != nil {
			_ = projectRows.Close()
			return nil, nil, report, fmt.Errorf("读取旧 Canvas 项目行: %w", err)
		}
		if !json.Valid([]byte(project.Viewport)) {
			_ = projectRows.Close()
			return nil, nil, report, fmt.Errorf(
				"Canvas 项目 %s 的 viewport 不是合法 JSON",
				project.ID,
			)
		}
		if project.SessionID != "" && !sessionIDs[project.SessionID] {
			missingSessionID := project.SessionID
			reason := "引用的旧聊天会话不存在，项目保留并清空悬空关联"
			payload, marshalErr := json.Marshal(struct {
				ProjectID string `json:"projectId"`
				SessionID string `json:"sessionId"`
			}{
				ProjectID: project.ID,
				SessionID: missingSessionID,
			})
			if marshalErr != nil {
				_ = projectRows.Close()
				return nil, nil, report, fmt.Errorf(
					"编码 Canvas 项目 %s 悬空会话关联: %w",
					project.ID,
					marshalErr,
				)
			}
			report.archives = append(
				report.archives,
				sqlite.LegacyMigrationRecordImport{
					SourceTable: "canvas_projects.sessionId",
					SourceID:    project.ID,
					Reason:      reason,
					Payload:     payload,
				},
			)
			report.linkDegraded = append(
				report.linkDegraded,
				fmt.Sprintf(
					"%s: 会话 %s 不存在，保留项目但清空关联",
					project.ID,
					missingSessionID,
				),
			)
			project.SessionID = ""
		}
		project.CreatedAt, err = parseLegacyTime(createdValue)
		if err != nil {
			_ = projectRows.Close()
			return nil, nil, report, fmt.Errorf(
				"Canvas 项目 %s 的 createdAt 无法解析: %w",
				project.ID,
				err,
			)
		}
		project.UpdatedAt, err = parseLegacyTime(updatedValue)
		if err != nil {
			_ = projectRows.Close()
			return nil, nil, report, fmt.Errorf(
				"Canvas 项目 %s 的 updatedAt 无法解析: %w",
				project.ID,
				err,
			)
		}
		projects = append(projects, project)
		projectIDs[project.ID] = true
	}
	if err := projectRows.Err(); err != nil {
		_ = projectRows.Close()
		return nil, nil, report, fmt.Errorf("遍历旧 canvas_projects: %w", err)
	}
	if err := projectRows.Close(); err != nil {
		return nil, nil, report, fmt.Errorf("关闭旧 canvas_projects 游标: %w", err)
	}

	nodeRows, err := legacy.QueryContext(ctx, `
		SELECT CAST(id AS TEXT), CAST(projectId AS TEXT),
		       COALESCE(type, ''), x, y, width, height, zIndex,
		       COALESCE(data, '{}'), visible, locked, createdAt, updatedAt
		FROM canvas_nodes
		ORDER BY projectId, zIndex, rowid`)
	if err != nil {
		return nil, nil, report, fmt.Errorf("读取旧 canvas_nodes: %w", err)
	}
	defer nodeRows.Close()
	nodes := make([]canvas.Node, 0, report.nodeSource)
	for nodeRows.Next() {
		report.nodeLoaded++
		var node canvas.Node
		var visible, locked int
		var createdValue, updatedValue any
		if err := nodeRows.Scan(
			&node.ID,
			&node.ProjectID,
			&node.Type,
			&node.X,
			&node.Y,
			&node.Width,
			&node.Height,
			&node.ZIndex,
			&node.Data,
			&visible,
			&locked,
			&createdValue,
			&updatedValue,
		); err != nil {
			return nil, nil, report, fmt.Errorf("读取旧 Canvas 节点行: %w", err)
		}
		if !projectIDs[node.ProjectID] {
			report.nodeSkipped = append(
				report.nodeSkipped,
				fmt.Sprintf("%s: 项目 %s 不存在", node.ID, node.ProjectID),
			)
			continue
		}
		if !json.Valid([]byte(node.Data)) {
			return nil, nil, report, fmt.Errorf(
				"Canvas 节点 %s 的 data 不是合法 JSON",
				node.ID,
			)
		}
		node.Visible = visible != 0
		node.Locked = locked != 0
		node.CreatedAt, err = parseLegacyTime(createdValue)
		if err != nil {
			return nil, nil, report, fmt.Errorf(
				"Canvas 节点 %s 的 createdAt 无法解析: %w",
				node.ID,
				err,
			)
		}
		node.UpdatedAt, err = parseLegacyTime(updatedValue)
		if err != nil {
			return nil, nil, report, fmt.Errorf(
				"Canvas 节点 %s 的 updatedAt 无法解析: %w",
				node.ID,
				err,
			)
		}
		nodes = append(nodes, node)
	}
	if err := nodeRows.Err(); err != nil {
		return nil, nil, report, fmt.Errorf("遍历旧 canvas_nodes: %w", err)
	}
	return projects, nodes, report, nil
}

func legacyIDSet(
	ctx context.Context,
	legacy legacyQuery,
	table string,
) (map[string]bool, error) {
	allowed := map[string]bool{
		"chat_sessions": true,
	}
	if !allowed[table] {
		return nil, fmt.Errorf("不支持读取旧表 ID: %s", table)
	}
	rows, err := legacy.QueryContext(
		ctx,
		`SELECT CAST(id AS TEXT) FROM `+table,
	)
	if err != nil {
		return nil, fmt.Errorf("读取旧 %s ID: %w", table, err)
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("读取旧 %s ID 行: %w", table, err)
		}
		result[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历旧 %s ID: %w", table, err)
	}
	return result, nil
}
