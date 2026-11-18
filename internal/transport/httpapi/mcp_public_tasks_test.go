package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/mcphttp"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// tasksCapabilities declares the Tasks extension the way an extension-aware
// client does in every request's _meta.
func tasksCapabilities() map[string]any {
	return map[string]any{"extensions": map[string]any{mcphttp.TasksExtension: map[string]any{}}}
}

// startTask calls a generation tool as an extension-aware client and returns
// the CreateTaskResult.
func (f *mcpFixture) startTask(key, tool string, arguments map[string]any) map[string]any {
	f.t.Helper()
	start := decodeToolResultRaw(f.t, f.postWithCapabilities(key, "tools/call",
		map[string]any{"name": tool, "arguments": arguments}, tasksCapabilities()))
	if start["resultType"] != "task" {
		f.t.Fatalf("%s did not answer a task: %v", tool, start)
	}
	return start
}

func TestMCPTasksExtensionIsDiscovered(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "read")

	discover := decodeToolResultRaw(t, f.post(key, "server/discover", nil))
	capabilities, _ := discover["capabilities"].(map[string]any)
	extensions, _ := capabilities["extensions"].(map[string]any)
	if _, declared := extensions[catalog.MCPTasksExtension]; !declared {
		t.Fatalf("discover declares no Tasks extension: %v", discover)
	}
	if capabilities["tools"] == nil {
		t.Fatalf("discover lost the tools capability: %v", discover)
	}
}

func TestMCPTasksCreateTaskPollAndComplete(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")

	start := f.startTask(key, "generate_image", map[string]any{"prompt": "x", "asset": "Tasks", "idempotency_key": idem("tasks")})
	id, _ := start["taskId"].(string)
	if id == "" || start["status"] != mcphttp.TaskStatusWorking || start["createdAt"] == "" || start["lastUpdatedAt"] == "" {
		t.Fatalf("CreateTaskResult = %v", start)
	}
	if _, ok := start["ttlMs"].(float64); !ok {
		t.Fatalf("CreateTaskResult has no ttlMs: %v", start)
	}
	if interval, ok := start["pollIntervalMs"].(float64); !ok || interval <= 0 {
		t.Fatalf("CreateTaskResult has no pollIntervalMs: %v", start)
	}

	// The task is still running: the poll answers working, with no result yet.
	running := decodeToolResultRaw(t, f.post(key, "tasks/get", map[string]any{"taskId": id}, nil))
	if running["resultType"] != "complete" || running["taskId"] != id || running["status"] != mcphttp.TaskStatusWorking || running["result"] != nil {
		t.Fatalf("running tasks/get = %v", running)
	}

	task := f.finish("owner-a", id)
	if task.State != generation.TaskSucceeded {
		t.Fatalf("task state = %s", task.State)
	}
	done := decodeToolResultRaw(t, f.post(key, "tasks/get", map[string]any{"taskId": id}, nil))
	if done["status"] != mcphttp.TaskStatusCompleted || done["pollIntervalMs"] != nil {
		t.Fatalf("completed tasks/get = %v", done)
	}
	result, _ := done["result"].(map[string]any)
	structured, _ := result["structuredContent"].(map[string]any)
	view, _ := structured["task"].(map[string]any)
	if result["isError"] != false || view["id"] != id || structured["status"] == nil {
		t.Fatalf("completed result = %v", result)
	}
}

func generationSucceeded(t *testing.T) any {
	t.Helper()
	return "SUCCEEDED"
}

func TestMCPTasksAreScopedToTheOwner(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")
	other := f.issueKey("owner-b", "generate")
	start := f.startTask(key, "generate_image", map[string]any{"prompt": "x", "asset": "Scoped", "idempotency_key": idem("scoped")})
	id := start["taskId"].(string)

	for name, test := range map[string]struct{ key, taskID string }{
		"unknown task":  {key, "task-does-not-exist"},
		"another owner": {other, id},
	} {
		recorder := f.post(test.key, "tasks/get", map[string]any{"taskId": test.taskID})
		result := decodeToolResult(t, recorder)
		if recorder.Code != http.StatusNotFound || result.rpcError == nil || result.rpcError["code"].(float64) != mcphttp.TaskNotFound {
			t.Errorf("%s = %d %s", name, recorder.Code, recorder.Body.String())
		}
	}
	if recorder := f.post(key, "tasks/get", map[string]any{}); recorder.Code != http.StatusBadRequest {
		t.Fatalf("tasks/get without taskId = %d %s", recorder.Code, recorder.Body.String())
	}

	// Reading a task needs read; cancelling it needs generate (which includes
	// read), because cancellation changes work that may cost money.
	read := f.issueKey("owner-a", "read")
	if recorder := f.post(read, "tasks/get", map[string]any{"taskId": id}); recorder.Code != http.StatusOK {
		t.Fatalf("read key tasks/get = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := f.post(read, "tasks/cancel", map[string]any{"taskId": id}); recorder.Code != http.StatusForbidden {
		t.Fatalf("read key tasks/cancel = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestMCPTasksCancelCancelsAndSaysWhenItCannot(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")

	start := f.startTask(key, "generate_image", map[string]any{"prompt": "x", "asset": "Cancel", "idempotency_key": idem("cancel")})
	id := start["taskId"].(string)
	cancel := decodeToolResultRaw(t, f.post(key, "tasks/cancel", map[string]any{"taskId": id}, nil))
	meta, _ := cancel["_meta"].(map[string]any)
	detail, _ := meta[mcphttp.TasksExtension].(map[string]any)
	if cancel["resultType"] != "complete" || detail["status"] != mcphttp.TaskStatusCancelled || detail["alreadyTerminal"] != false {
		t.Fatalf("tasks/cancel = %v", cancel)
	}
	if view := f.call(key, "get_task", map[string]any{"task_id": id}).structured["task"].(map[string]any); view["state"] != "CANCELED" {
		t.Fatalf("canceled task = %v", view)
	}

	again := decodeToolResultRaw(t, f.post(key, "tasks/cancel", map[string]any{"taskId": id}, nil))
	againMeta, _ := again["_meta"].(map[string]any)
	againDetail, _ := againMeta[mcphttp.TasksExtension].(map[string]any)
	if againDetail["alreadyTerminal"] != true || againDetail["status"] != mcphttp.TaskStatusCancelled ||
		!strings.Contains(againDetail["statusMessage"].(string), "已经结束") {
		t.Fatalf("terminal cancel = %v", again)
	}

	// A task that ended before the request is not reopened or changed.
	done := f.succeededTask("owner-a", "session-tasks-terminal", "tasks-terminal")
	terminal := decodeToolResultRaw(t, f.post(key, "tasks/cancel", map[string]any{"taskId": string(done.ID)}, nil))
	terminalMeta, _ := terminal["_meta"].(map[string]any)
	terminalDetail, _ := terminalMeta[mcphttp.TasksExtension].(map[string]any)
	if terminalDetail["alreadyTerminal"] != true || terminalDetail["status"] != mcphttp.TaskStatusCompleted {
		t.Fatalf("succeeded task cancel = %v", terminal)
	}

	missing := decodeToolResult(t, f.post(key, "tasks/cancel", map[string]any{"taskId": "task-does-not-exist"}))
	if missing.rpcError == nil || missing.rpcError["code"].(float64) != mcphttp.TaskNotFound {
		t.Fatalf("unknown task cancel = %s", missing.raw)
	}
}

func TestMCPTasksNeedTheExtensionDeclaration(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil, func(application *catalog.ApplicationSchema) {
		application.MCPPublic.Tasks = catalog.MCPPublicTasks{}
	})
	key := f.issueKey("owner-a", "generate")

	discover := decodeToolResultRaw(t, f.post(key, "server/discover", nil))
	capabilities, _ := discover["capabilities"].(map[string]any)
	if extensions, _ := capabilities["extensions"].(map[string]any); extensions != nil {
		t.Fatalf("disabled Tasks extension is advertised: %v", discover)
	}
	if recorder := f.post(key, "tasks/get", map[string]any{"taskId": "anything"}); recorder.Code != http.StatusNotFound {
		t.Fatalf("disabled tasks/get = %d %s", recorder.Code, recorder.Body.String())
	}
	// A client that declares the extension still receives the ordinary result
	// when the server does not serve it.
	start := decodeToolResultRaw(t, f.postWithCapabilities(key, "tools/call",
		map[string]any{"name": "generate_image", "arguments": map[string]any{"prompt": "x", "idempotency_key": idem("no-tasks")}},
		tasksCapabilities()))
	if start["resultType"] == "task" || start["structuredContent"] == nil {
		t.Fatalf("disabled Tasks extension returned a task: %v", start)
	}
}
