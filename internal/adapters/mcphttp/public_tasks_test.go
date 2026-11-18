package mcphttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/mcphttp"
)

// fakeTaskRuntime is the echo runtime with the Tasks extension's methods.
type fakeTaskRuntime struct {
	*fakePublicRuntime
	get    func(ctx context.Context, taskID string) (mcphttp.PublicTaskResult, error)
	cancel func(ctx context.Context, taskID string) (mcphttp.PublicTaskCancel, error)
}

func (r *fakeTaskRuntime) GetTask(ctx context.Context, taskID string) (mcphttp.PublicTaskResult, error) {
	return r.get(ctx, taskID)
}

func (r *fakeTaskRuntime) CancelTask(ctx context.Context, taskID string) (mcphttp.PublicTaskCancel, error) {
	return r.cancel(ctx, taskID)
}

func newPublicTasks(t *testing.T, runtime mcphttp.PublicRuntime, methods ...string) http.Handler {
	t.Helper()
	handler, err := mcphttp.NewPublicHandler(runtime, mcphttp.PublicPolicy{
		ProtocolVersion: mcphttp.ProtocolVersion, ServerName: "Plinth",
		Instructions:     func(*http.Request) string { return "read the status block" },
		MaxRequestBytes:  2048,
		MaxResponseBytes: 4096,
		TasksExtension:   mcphttp.TasksExtension,
		TasksMethods:     methods,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

// taskEnvelope is one request of a client that declared the Tasks extension.
func taskEnvelope(method string, params map[string]any) string {
	if params == nil {
		params = map[string]any{}
	}
	params["_meta"] = map[string]any{
		"io.modelcontextprotocol/protocolVersion": mcphttp.ProtocolVersion,
		"io.modelcontextprotocol/clientInfo":      map[string]any{"name": "test", "version": "1"},
		"io.modelcontextprotocol/clientCapabilities": map[string]any{
			"extensions": map[string]any{mcphttp.TasksExtension: map[string]any{}},
		},
	}
	encoded, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 7, "method": method, "params": params})
	return string(encoded)
}

func int64Pointer(value int64) *int64 { return &value }

func TestPublicTasksDiscoverAndCreateTaskResult(t *testing.T) {
	t.Parallel()
	runtime := echoRuntime()
	runtime.call = func(context.Context, string, json.RawMessage) (mcphttp.PublicResult, error) {
		return mcphttp.PublicResult{
			Structured: json.RawMessage(`{"task_id":"task-1"}`), Text: []string{"started"},
			Task: &mcphttp.PublicTask{
				TaskID: "task-1", Status: mcphttp.TaskStatusWorking, StatusMessage: "进行中",
				CreatedAt: "2026-11-18T00:00:00Z", LastUpdatedAt: "2026-11-18T00:00:01Z",
				TTLMs: int64Pointer(86400000), PollIntervalMs: int64Pointer(5000),
			},
		}, nil
	}
	handler := newPublicTasks(t, runtime, mcphttp.MethodTasksGet, mcphttp.MethodTasksCancel)

	discover := send(t, handler, http.MethodPost, envelope("server/discover", nil), nil)
	capabilities := discover.body["result"].(map[string]any)["capabilities"].(map[string]any)
	extensions := capabilities["extensions"].(map[string]any)
	if _, declared := extensions[mcphttp.TasksExtension]; !declared {
		t.Fatalf("discover = %s", discover.raw)
	}

	// A client that declared the extension receives CreateTaskResult.
	called := send(t, handler, http.MethodPost, taskEnvelope("tools/call",
		map[string]any{"name": "echo", "arguments": map[string]any{}}), nil)
	result := called.body["result"].(map[string]any)
	if result["resultType"] != "task" || result["taskId"] != "task-1" || result["status"] != mcphttp.TaskStatusWorking ||
		result["ttlMs"].(float64) != 86400000 || result["pollIntervalMs"].(float64) != 5000 || result["statusMessage"] != "进行中" {
		t.Fatalf("CreateTaskResult = %s", called.raw)
	}
	if result["content"] != nil {
		t.Fatalf("CreateTaskResult carries the ordinary content: %s", called.raw)
	}

	// A client that did not declare it receives the ordinary result.
	plain := send(t, handler, http.MethodPost, envelope("tools/call",
		map[string]any{"name": "echo", "arguments": map[string]any{}}), nil)
	if plain.body["result"].(map[string]any)["resultType"] != "complete" {
		t.Fatalf("plain tools/call = %s", plain.raw)
	}
}

func TestPublicTasksGetAndCancelShapes(t *testing.T) {
	t.Parallel()
	runtime := &fakeTaskRuntime{
		fakePublicRuntime: echoRuntime(),
		get: func(_ context.Context, taskID string) (mcphttp.PublicTaskResult, error) {
			if taskID == "missing" {
				return mcphttp.PublicTaskResult{}, &mcphttp.TaskError{Code: mcphttp.TaskNotFound, Message: "no such task"}
			}
			return mcphttp.PublicTaskResult{
				PublicTask: mcphttp.PublicTask{
					TaskID: taskID, Status: mcphttp.TaskStatusCompleted,
					CreatedAt: "2026-11-18T00:00:00Z", LastUpdatedAt: "2026-11-18T00:00:09Z",
					TTLMs: int64Pointer(1000),
				},
				Result: json.RawMessage(`{"content":[],"isError":false,"structuredContent":{"task":{"id":"task-1"}}}`),
			}, nil
		},
		cancel: func(_ context.Context, taskID string) (mcphttp.PublicTaskCancel, error) {
			if taskID == "missing" {
				return mcphttp.PublicTaskCancel{}, &mcphttp.TaskError{Code: mcphttp.TaskNotFound, Message: "no such task"}
			}
			return mcphttp.PublicTaskCancel{Status: mcphttp.TaskStatusCancelled}, nil
		},
	}
	handler := newPublicTasks(t, runtime, mcphttp.MethodTasksGet, mcphttp.MethodTasksCancel)

	got := send(t, handler, http.MethodPost, taskEnvelope("tasks/get", map[string]any{"taskId": "task-1"}), nil)
	result := got.body["result"].(map[string]any)
	completed, _ := result["result"].(map[string]any)
	structured, _ := completed["structuredContent"].(map[string]any)
	task, _ := structured["task"].(map[string]any)
	if result["resultType"] != "complete" || result["status"] != mcphttp.TaskStatusCompleted || task["id"] != "task-1" {
		t.Fatalf("tasks/get = %s", got.raw)
	}

	cancel := send(t, handler, http.MethodPost, taskEnvelope("tasks/cancel", map[string]any{"taskId": "task-1"}), nil)
	cancelResult := cancel.body["result"].(map[string]any)
	meta := cancelResult["_meta"].(map[string]any)[mcphttp.TasksExtension].(map[string]any)
	if cancelResult["resultType"] != "complete" || meta["status"] != mcphttp.TaskStatusCancelled || meta["taskId"] != "task-1" {
		t.Fatalf("tasks/cancel = %s", cancel.raw)
	}

	missing := send(t, handler, http.MethodPost, taskEnvelope("tasks/get", map[string]any{"taskId": "missing"}), nil)
	code, _ := rpcError(t, missing)
	if missing.status != http.StatusNotFound || code != mcphttp.TaskNotFound {
		t.Fatalf("missing task = %d %s", missing.status, missing.raw)
	}
	if malformed := send(t, handler, http.MethodPost, taskEnvelope("tasks/get", nil), nil); malformed.status != http.StatusBadRequest {
		t.Fatalf("tasks/get without taskId = %d %s", malformed.status, malformed.raw)
	}
}

func TestPublicTasksMethodsFollowThePolicy(t *testing.T) {
	t.Parallel()
	runtime := &fakeTaskRuntime{
		fakePublicRuntime: echoRuntime(),
		get: func(context.Context, string) (mcphttp.PublicTaskResult, error) {
			return mcphttp.PublicTaskResult{}, nil
		},
		cancel: func(context.Context, string) (mcphttp.PublicTaskCancel, error) {
			return mcphttp.PublicTaskCancel{}, nil
		},
	}
	handler := newPublicTasks(t, runtime, mcphttp.MethodTasksGet)
	cancel := send(t, handler, http.MethodPost, taskEnvelope("tasks/cancel", map[string]any{"taskId": "task-1"}), nil)
	code, _ := rpcError(t, cancel)
	if cancel.status != http.StatusNotFound || code != -32601 {
		t.Fatalf("undeclared tasks/cancel = %d %s", cancel.status, cancel.raw)
	}
}
