package etl_test

import (
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/etl/etltest"
)

func TestMigrateChatPreservesSessionsMessagesAndOrder(t *testing.T) {
	fixture := etltest.NewFixture(t)
	fixture.Exec(t,
		`INSERT INTO chat_sessions (
			id, userId, userName, title, createdAt, updatedAt
		 ) VALUES (
			'legacy-session', 'owner-a', '美术甲', '旧对话',
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:07:06+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO chat_messages (
			id, sessionId, role, content, imageUrls, toolCalls, createdAt
		 ) VALUES
			(
				'legacy-message-1', 'legacy-session', 'user', '请画一只猫',
				'["/api/assets/input-a"]', '[]',
				'2025-02-03T04:06:06+00:00'
			),
			(
				'legacy-message-2', 'legacy-session', 'assistant', '已经生成',
				'["/api/gpt-images/cat.png"]',
				'[{"name":"generate_image","arguments":"{\"prompt\":\"cat\"}","taskId":"task-1"}]',
				'2025-02-03T04:06:06+00:00'
			)`,
	)

	targetDB := migrateTarget(t)
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", targetDB,
	)
	if code != 0 {
		t.Fatalf("迁移应成功，退出码 %d，输出:\n%s", code, output)
	}
	if !strings.Contains(
		output,
		"聊天域: 会话源 1 行 / 迁移 1 行 / 跳过 0 行；"+
			"消息源 2 行 / 迁移 2 行 / 跳过 0 行",
	) {
		t.Fatalf("聊天对账报告缺失:\n%s", output)
	}

	repository, err := sqlite.Open(targetDB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	sessions, err := repository.ListSessions(t.Context(), "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 ||
		sessions[0].ID != "legacy-session" ||
		sessions[0].OwnerName != "美术甲" ||
		sessions[0].Title != "旧对话" ||
		sessions[0].Messages != 2 ||
		sessions[0].CreatedAt.Format(time.RFC3339) !=
			"2025-02-03T04:05:06Z" ||
		sessions[0].UpdatedAt.Format(time.RFC3339) !=
			"2025-02-03T04:07:06Z" {
		t.Fatalf("会话字段保真失败: %#v", sessions)
	}
	messages, err := repository.ListMessages(
		t.Context(),
		"owner-a",
		"legacy-session",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 ||
		messages[0].ID != "legacy-message-1" ||
		messages[0].Role != "user" ||
		messages[0].Content != "请画一只猫" ||
		len(messages[0].ImageURLs) != 1 ||
		messages[0].ImageURLs[0] != "/api/assets/input-a" ||
		messages[1].ID != "legacy-message-2" ||
		messages[1].Role != "assistant" ||
		messages[1].Content != "已经生成" ||
		len(messages[1].ImageURLs) != 1 ||
		messages[1].ImageURLs[0] != "/api/gpt-images/cat.png" {
		t.Fatalf("消息字段或顺序保真失败: %#v", messages)
	}
	if len(messages[1].ToolCalls) != 1 ||
		messages[1].ToolCalls[0].ID != "legacy-message-2-tool-1" ||
		messages[1].ToolCalls[0].Name != "generate_image" ||
		string(messages[1].ToolCalls[0].Arguments) != `{"prompt":"cat"}` ||
		messages[1].ToolCalls[0].TaskID != "task-1" {
		t.Fatalf("工具调用字段保真失败: %#v", messages[1].ToolCalls)
	}
}

func TestMigrateCanvasPreservesProjectNodesAndSessionLink(t *testing.T) {
	fixture := etltest.NewFixture(t)
	fixture.Exec(t,
		`INSERT INTO chat_sessions (
			id, userId, userName, title, createdAt, updatedAt
		 ) VALUES (
			'canvas-session', 'owner-a', '美术甲', '画板会话',
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:07:06+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO canvas_projects (
			id, userId, name, sessionId, viewport, bgStyle,
			createdAt, updatedAt
		 ) VALUES (
			'legacy-project', 'owner-a', '旧画板', 'canvas-session',
			'{"zoom":1.25,"panX":12,"panY":-8}', 'grid',
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:08:06+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO canvas_nodes (
			id, projectId, type, x, y, width, height, zIndex, data,
			visible, locked, createdAt, updatedAt
		 ) VALUES
			(
				'node-z2', 'legacy-project', 'image', 10.5, -2.25,
				320, 180, 2, '{"assetId":"legacy-input","label":"参考图"}',
				1, 1, '2025-02-03T04:06:06+00:00',
				'2025-02-03T04:07:06+00:00'
			),
			(
				'node-z1', 'legacy-project', 'text', 1, 2,
				200, 80, 1, '{"text":"提示词","color":"#fff"}',
				0, 0, '2025-02-03T04:05:30+00:00',
				'2025-02-03T04:05:40+00:00'
			)`,
	)

	targetDB := migrateTarget(t)
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", targetDB,
	)
	if code != 0 {
		t.Fatalf("迁移应成功，退出码 %d，输出:\n%s", code, output)
	}
	if !strings.Contains(
		output,
		"Canvas 域: 项目源 1 行 / 迁移 1 行 / 跳过 0 行；"+
			"节点源 2 行 / 迁移 2 行 / 跳过 0 行",
	) {
		t.Fatalf("Canvas 对账报告缺失:\n%s", output)
	}

	repository, err := sqlite.Open(targetDB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	project, found, err := repository.GetProject(
		t.Context(),
		"owner-a",
		"legacy-project",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !found ||
		project.ID != "legacy-project" ||
		project.OwnerID != "owner-a" ||
		project.Name != "旧画板" ||
		project.SessionID != "canvas-session" ||
		project.Viewport != `{"zoom":1.25,"panX":12,"panY":-8}` ||
		project.BGStyle != "grid" ||
		project.CreatedAt.Format(time.RFC3339) !=
			"2025-02-03T04:05:06Z" ||
		project.UpdatedAt.Format(time.RFC3339) !=
			"2025-02-03T04:08:06Z" {
		t.Fatalf("Canvas 项目字段保真失败: %#v", project)
	}
	if len(project.Nodes) != 2 ||
		project.Nodes[0].ID != "node-z1" ||
		project.Nodes[1].ID != "node-z2" {
		t.Fatalf("Canvas 节点顺序错误: %#v", project.Nodes)
	}
	image := project.Nodes[1]
	if image.ProjectID != "legacy-project" ||
		image.Type != "image" ||
		image.X != 10.5 ||
		image.Y != -2.25 ||
		image.Width != 320 ||
		image.Height != 180 ||
		image.ZIndex != 2 ||
		image.Data != `{"assetId":"legacy-input","label":"参考图"}` ||
		!image.Visible ||
		!image.Locked ||
		image.CreatedAt.Format(time.RFC3339) !=
			"2025-02-03T04:06:06Z" ||
		image.UpdatedAt.Format(time.RFC3339) !=
			"2025-02-03T04:07:06Z" {
		t.Fatalf("Canvas 节点字段保真失败: %#v", image)
	}
	text := project.Nodes[0]
	if text.Visible || text.Locked ||
		text.Data != `{"text":"提示词","color":"#fff"}` {
		t.Fatalf("Canvas 文本节点字段保真失败: %#v", text)
	}
}

func TestMigrateCanvasArchivesMissingSessionLinkWithoutLosingProject(
	t *testing.T,
) {
	fixture := etltest.NewFixture(t)
	fixture.Exec(t,
		`INSERT INTO canvas_projects (
			id, userId, name, sessionId, viewport, bgStyle,
			createdAt, updatedAt
		 ) VALUES (
			'orphan-link-project', 'owner-a', '旧画板',
			'missing-session', '{}', 'dots',
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:08:06+00:00'
		 )`,
	)
	target := migrateTarget(t)
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", target,
	)
	if code != 0 {
		t.Fatalf("缺失会话关联应无损归档后继续迁移:\n%s", output)
	}
	if !strings.Contains(output, "Canvas 会话关联归档") ||
		!strings.Contains(output, "missing-session") {
		t.Fatalf("缺失关联未进入报告:\n%s", output)
	}
	repository, err := sqlite.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	project, found, err := repository.GetProject(
		t.Context(),
		"owner-a",
		"orphan-link-project",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !found || project.SessionID != "" {
		t.Fatalf("项目未保留或悬空关联未清空: %#v", project)
	}
	db := openTargetDatabase(t, target)
	var sourceTable, sessionID string
	if err := db.QueryRow(
		`SELECT source_table,
		        json_extract(payload_json, '$.sessionId')
		 FROM legacy_migration_records
		 WHERE source_id = 'orphan-link-project'`,
	).Scan(&sourceTable, &sessionID); err != nil {
		t.Fatal(err)
	}
	if sourceTable != "canvas_projects.sessionId" ||
		sessionID != "missing-session" {
		t.Fatalf("Canvas 悬空关联归档错误: %s/%s", sourceTable, sessionID)
	}
}

func TestMigrateChatAndCanvasIsIdempotent(t *testing.T) {
	fixture := etltest.NewFixture(t)
	fixture.Exec(t,
		`INSERT INTO chat_sessions (
			id, userId, userName, title, createdAt, updatedAt
		 ) VALUES (
			'idempotent-session', 'owner-a', '美术甲', '幂等会话',
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:07:06+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO chat_messages (
			id, sessionId, role, content, imageUrls, toolCalls, createdAt
		 ) VALUES (
			'idempotent-message', 'idempotent-session', 'user', 'hello',
			'[]', '[]', '2025-02-03T04:06:06+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO canvas_projects (
			id, userId, name, sessionId, viewport, bgStyle,
			createdAt, updatedAt
		 ) VALUES (
			'idempotent-project', 'owner-a', '幂等画板',
			'idempotent-session', '{}', 'dots',
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:08:06+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO canvas_nodes (
			id, projectId, type, x, y, width, height, zIndex, data,
			visible, locked, createdAt, updatedAt
		 ) VALUES (
			'idempotent-node', 'idempotent-project', 'text',
			1, 2, 200, 80, 1, '{}', 1, 0,
			'2025-02-03T04:05:30+00:00',
			'2025-02-03T04:05:40+00:00'
		 )`,
	)

	targetDB := migrateTarget(t)
	for run := 1; run <= 2; run++ {
		code, output := runETL(t,
			"migrate",
			"--legacy-root", fixture.Root,
			"--target-db", targetDB,
		)
		if code != 0 {
			t.Fatalf("第 %d 次迁移失败，退出码 %d:\n%s", run, code, output)
		}
	}

	db := openTargetDatabase(t, targetDB)
	for table, want := range map[string]int{
		"go_chat_sessions":   1,
		"go_chat_messages":   1,
		"go_canvas_projects": 1,
		"go_canvas_nodes":    1,
	} {
		var got int
		if err := db.QueryRow(
			"SELECT COUNT(*) FROM " + table,
		).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s 重跑后行数 = %d，期望 %d", table, got, want)
		}
	}
}
