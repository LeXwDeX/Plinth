package httpapi_test

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/etl"
	"github.com/LeXwDeX/Plinth/internal/etl/etltest"
)

// TestETLMigratedChatAndCanvasMatchHTTPContracts 把 ETL 迁移后的聊天与画板
// 数据库直接交给现有 HTTP 栈，验证旧前端依赖的投影仍可读取。
func TestETLMigratedChatAndCanvasMatchHTTPContracts(t *testing.T) {
	t.Parallel()
	fixture := etltest.NewFixture(t)
	fixture.Exec(t,
		`INSERT INTO chat_sessions (
			id, userId, userName, title, createdAt, updatedAt
		 ) VALUES (
			'migrated-session', 'local-user', '本地用户', '迁移会话',
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:07:06+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO chat_messages (
			id, sessionId, role, content, imageUrls, toolCalls, createdAt
		 ) VALUES (
			'migrated-message', 'migrated-session', 'assistant', '迁移消息',
			'["/api/assets/legacy-input"]',
			'[{"name":"generate_image","arguments":"{\"prompt\":\"cat\"}","taskId":"task-1"}]',
			'2025-02-03T04:06:06+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO canvas_projects (
			id, userId, name, sessionId, viewport, bgStyle,
			createdAt, updatedAt
		 ) VALUES (
			'migrated-project', 'local-user', '迁移画板',
			'migrated-session', '{"zoom":1.5}', 'grid',
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:08:06+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO canvas_nodes (
			id, projectId, type, x, y, width, height, zIndex, data,
			visible, locked, createdAt, updatedAt
		 ) VALUES (
			'migrated-node', 'migrated-project', 'image',
			10, 20, 320, 180, 1, '{"assetId":"legacy-input"}', 1, 0,
			'2025-02-03T04:06:06+00:00',
			'2025-02-03T04:07:06+00:00'
		 )`,
	)

	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"admin"},
		nil,
		nil,
		nil,
		nil,
		func(t *testing.T, databasePath string) {
			t.Helper()
			var stdout, stderr bytes.Buffer
			if code := etl.Main([]string{
				"migrate",
				"--legacy-root", fixture.Root,
				"--target-db", databasePath,
			}, &stdout, &stderr); code != 0 {
				t.Fatalf(
					"ETL 迁移失败，退出码 %d:\n%s%s",
					code, stdout.String(), stderr.String(),
				)
			}
		},
	)

	sessionsResponse := performChatRequest(
		handler,
		http.MethodGet,
		"/api/chat/sessions",
		"",
	)
	if sessionsResponse.Code != http.StatusOK {
		t.Fatalf(
			"会话列表 status=%d body=%s",
			sessionsResponse.Code,
			sessionsResponse.Body.String(),
		)
	}
	var sessions []map[string]any
	decodeChatRecorder(t, sessionsResponse, &sessions)
	if len(sessions) != 1 ||
		sessions[0]["id"] != "migrated-session" ||
		sessions[0]["title"] != "迁移会话" {
		t.Fatalf("迁移会话投影 = %#v", sessions)
	}
	count, _ := sessions[0]["_count"].(map[string]any)
	if count["messages"] != float64(1) {
		t.Fatalf("迁移会话消息数 = %#v", count)
	}

	messagesResponse := performChatRequest(
		handler,
		http.MethodGet,
		"/api/chat/sessions/migrated-session/messages",
		"",
	)
	if messagesResponse.Code != http.StatusOK {
		t.Fatalf(
			"消息列表 status=%d body=%s",
			messagesResponse.Code,
			messagesResponse.Body.String(),
		)
	}
	var messages []map[string]any
	decodeChatRecorder(t, messagesResponse, &messages)
	if len(messages) != 1 ||
		messages[0]["id"] != "migrated-message" ||
		messages[0]["content"] != "迁移消息" ||
		messages[0]["imageUrls"] != `["/api/assets/legacy-input"]` ||
		messages[0]["toolCalls"] !=
			`[{"arguments":"{\"prompt\":\"cat\"}","name":"generate_image","taskId":"task-1"}]` {
		t.Fatalf("迁移消息投影 = %#v", messages)
	}

	projectsResponse := performCanvasRequest(
		handler,
		http.MethodGet,
		"/api/canvas/projects",
		"",
	)
	if projectsResponse.Code != http.StatusOK {
		t.Fatalf(
			"画板列表 status=%d body=%s",
			projectsResponse.Code,
			projectsResponse.Body.String(),
		)
	}
	var projects []map[string]any
	decodeCanvasRecorder(t, projectsResponse, &projects)
	if len(projects) != 1 ||
		projects[0]["id"] != "migrated-project" ||
		projects[0]["sessionId"] != "migrated-session" {
		t.Fatalf("迁移画板列表 = %#v", projects)
	}

	projectResponse := performCanvasRequest(
		handler,
		http.MethodGet,
		"/api/canvas/projects/migrated-project",
		"",
	)
	if projectResponse.Code != http.StatusOK {
		t.Fatalf(
			"画板详情 status=%d body=%s",
			projectResponse.Code,
			projectResponse.Body.String(),
		)
	}
	var project map[string]any
	decodeCanvasRecorder(t, projectResponse, &project)
	nodes, ok := project["nodes"].([]any)
	if project["name"] != "迁移画板" ||
		project["viewport"] != `{"zoom":1.5}` ||
		!ok ||
		len(nodes) != 1 {
		t.Fatalf("迁移画板详情 = %#v", project)
	}
	node, _ := nodes[0].(map[string]any)
	if node["id"] != "migrated-node" ||
		node["data"] != `{"assetId":"legacy-input"}` {
		t.Fatalf("迁移画板节点 = %#v", node)
	}
}
