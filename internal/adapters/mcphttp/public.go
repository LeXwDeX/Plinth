package mcphttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
)

// The endpoint of the external MCP service (#332). It speaks the same
// stateless protocol as the platform's own read-only endpoint and checks the
// same envelope, but differs where the caller differs: it is an outside Agent
// that has to learn from a failed call what to do next, so a tool's business
// error is a tool result with isError true (the call itself succeeded), tools
// may return structuredContent that conforms to a declared outputSchema, and
// the arguments are those of a declared JSON Schema. The platform's own
// endpoint still folds every tool error into one protocol error.
//
// The handler keeps no state between requests: no session ID is issued or
// read, so a request can be served by any instance. Who the caller is, is the
// transport's business: the runtime reads the authenticated person from the
// request context.

// PublicTool is one tool as tools/list shows it.
type PublicTool struct {
	Name         string
	Description  string
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage
}

// PublicResult is the outcome of a tool call that ran. A business failure is a
// result with IsError true, written for the calling Agent; it is not an error
// of the call.
type PublicResult struct {
	// Structured is the structuredContent; empty for none.
	Structured json.RawMessage
	// Text are the text content blocks, in order.
	Text    []string
	IsError bool
	// Task, when set, is the Task the call created (#332 stage 2). The endpoint
	// answers with a CreateTaskResult only when the client declared the Tasks
	// extension; otherwise it answers with the ordinary result above.
	Task *PublicTask
}

// CallToolResult is the result's ordinary wire shape: the content blocks and
// structuredContent a tools/call returns. It is also the completed result a
// tasks/get carries.
func (r PublicResult) CallToolResult() (map[string]any, error) {
	content := make([]map[string]string, 0, len(r.Text))
	for _, text := range r.Text {
		content = append(content, map[string]string{"type": "text", "text": text})
	}
	called := map[string]any{"content": content, "isError": r.IsError}
	if len(r.Structured) > 0 {
		if !json.Valid(r.Structured) {
			return nil, errors.New("mcphttp: tool returned invalid structured content")
		}
		called["structuredContent"] = r.Structured
	}
	return called, nil
}

// The Tasks extension (#332 stage 2) and its fixed wire vocabulary. The
// extension is declared in server/discover under capabilities.extensions; its
// methods answer the 2026-07-28 schema shapes:
//
//	CreateTaskResult: resultType "task" plus the Task fields
//	Task:             taskId, status, createdAt, lastUpdatedAt, ttlMs,
//	                  statusMessage and pollIntervalMs optional
//	tasks/get:        {taskId} -> Task fields, resultType "complete", plus
//	                  inputRequests / result / error for the status
//	tasks/cancel:     {taskId} -> {resultType "complete"}
//
// The extension is negotiated per request: the client carries
// capabilities.extensions in its _meta, the server advertises its own in
// server/discover. A client that did not declare it never receives a Task.
const (
	TasksExtension    = "io.modelcontextprotocol/tasks"
	MethodTasksGet    = "tasks/get"
	MethodTasksCancel = "tasks/cancel"

	// TaskNotFound is the JSON-RPC code of a Task the caller cannot see: the
	// same answer for a task that does not exist and another owner's task.
	TaskNotFound = -32004

	// The Task status vocabulary of the extension.
	TaskStatusWorking       = "working"
	TaskStatusInputRequired = "input_required"
	TaskStatusCompleted     = "completed"
	TaskStatusFailed        = "failed"
	TaskStatusCancelled     = "cancelled"
)

// PublicTask is a Task as CreateTaskResult and tasks/get carry it.
type PublicTask struct {
	TaskID        string
	Status        string
	StatusMessage string
	CreatedAt     string
	LastUpdatedAt string
	// TTLMs is how long the server keeps the task; the schema allows null.
	TTLMs *int64
	// PollIntervalMs is the suggested wait between tasks/get calls.
	PollIntervalMs *int64
}

// PublicTaskResult is one tasks/get answer. Result and Error are set only for
// the completed and failed statuses.
type PublicTaskResult struct {
	PublicTask
	Result json.RawMessage
	Error  *PublicTaskFailure
}

// PublicTaskFailure is the error a failed Task carries.
type PublicTaskFailure struct {
	Code    int
	Message string
	Data    map[string]any
}

// PublicTaskCancel is a tasks/cancel acknowledgement. Cancellation is
// cooperative; a task that already reached a terminal status is reported as
// such and is not changed.
type PublicTaskCancel struct {
	Status          string
	StatusMessage   string
	AlreadyTerminal bool
}

// TaskRuntime is what a runtime that serves the Tasks extension implements.
type TaskRuntime interface {
	// GetTask reads one Task of the caller; another owner's Task and a Task
	// that does not exist are both a TaskError with code TaskNotFound.
	GetTask(ctx context.Context, taskID string) (PublicTaskResult, error)
	// CancelTask asks the generation kernel to cancel one of the caller's
	// Tasks. A terminal Task is acknowledged, not changed.
	CancelTask(ctx context.Context, taskID string) (PublicTaskCancel, error)
}

// TaskError is a refused tasks/get or tasks/cancel. Code is a JSON-RPC code
// (TaskNotFound for a Task the caller cannot see).
type TaskError struct {
	Code    int
	Message string
}

func (e *TaskError) Error() string { return e.Message }

// ErrUnknownTool is returned by a runtime for a tool it does not serve.
var ErrUnknownTool = errors.New("mcphttp: unknown tool")

// ScopeError is returned by a runtime when the caller's key lacks the scope the
// tool needs. The handler answers 403 with a Bearer challenge.
type ScopeError struct{ Scope string }

func (e *ScopeError) Error() string { return "mcphttp: the key lacks scope " + e.Scope }

// PublicRuntime is what the public endpoint serves. Any other error from it is
// an internal failure: the caller is told nothing of its cause.
type PublicRuntime interface {
	// ListTools lists the tools the authenticated caller may call.
	ListTools(ctx context.Context) ([]PublicTool, error)
	// CallTool runs a tool with JSON arguments (an object).
	CallTool(ctx context.Context, name string, arguments json.RawMessage) (PublicResult, error)
}

// The kinds of problem the endpoint words through PublicPolicy.Detail.
const (
	ProblemRequestTooLarge   = "request_too_large"
	ProblemInsufficientScope = "insufficient_scope"
	ProblemInternal          = "internal"
	ProblemMethodNotAllowed  = "method_not_allowed"
)

// PublicPolicy configures the public endpoint from the Product Schema.
type PublicPolicy struct {
	ProtocolVersion string
	ServerName      string
	// Instructions is the usage note server/discover carries, in the
	// request's language; nil for none.
	Instructions func(request *http.Request) string
	// MaxRequestBytes bounds the body; MaxResponseBytes the JSON response.
	MaxRequestBytes  int64
	MaxResponseBytes int64
	// TasksExtension is the extension server/discover advertises (#332 stage
	// 2); empty when the endpoint serves none. TasksMethods are the extension
	// methods it serves (tasks/get, tasks/cancel); a method not listed is a
	// method not found, as before.
	TasksExtension string
	TasksMethods   []string
	// Detail words a problem in the caller's language (Schema copy); nil
	// leaves the standard JSON-RPC message alone.
	Detail func(request *http.Request, kind, scope string) string
	// Observe is told of every request the endpoint refuses or fails, with the
	// internal cause if any, for the server log. It must not retain the request.
	Observe func(request *http.Request, status, code int, cause error)
}

// PublicHandler serves the public endpoint.
type PublicHandler struct {
	runtime PublicRuntime
	policy  PublicPolicy
}

// NewPublicHandler builds the handler; a runtime, a protocol version and both
// size limits are required.
func NewPublicHandler(runtime PublicRuntime, policy PublicPolicy) (*PublicHandler, error) {
	if runtime == nil || policy.ProtocolVersion == "" || policy.MaxRequestBytes <= 0 || policy.MaxResponseBytes <= 0 {
		return nil, errors.New("mcphttp: invalid public endpoint configuration")
	}
	return &PublicHandler{runtime: runtime, policy: policy}, nil
}

func (h *PublicHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "private, no-store")
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		h.refuse(writer, request, envelopeProblem{status: http.StatusMethodNotAllowed, id: nil, code: -32600, message: "Method not allowed"}, ProblemMethodNotAllowed, "", nil)
		return
	}
	contents, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, h.policy.MaxRequestBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			h.refuse(writer, request, envelopeProblem{status: http.StatusRequestEntityTooLarge, id: nil, code: -32600, message: "Request too large"}, ProblemRequestTooLarge, "", nil)
			return
		}
		h.refuse(writer, request, envelopeProblem{status: http.StatusBadRequest, id: 0, code: -32600}, "", "", err)
		return
	}
	message, refused := parseEnvelope(request, contents, h.policy.ProtocolVersion)
	if refused != nil {
		h.refuse(writer, request, *refused, "", "", nil)
		return
	}
	var result any
	switch message.Method {
	case "server/discover":
		capabilities := map[string]any{"tools": map[string]any{}}
		if h.policy.TasksExtension != "" {
			capabilities["extensions"] = map[string]any{h.policy.TasksExtension: map[string]any{}}
		}
		discovered := map[string]any{
			"resultType":   "complete",
			"serverInfo":   map[string]string{"name": h.policy.ServerName, "version": "1"},
			"capabilities": capabilities,
		}
		if h.policy.Instructions != nil {
			discovered["instructions"] = h.policy.Instructions(request)
		}
		result = discovered
	case MethodTasksGet, MethodTasksCancel:
		if !h.servesTaskMethod(message.Method) {
			h.refuse(writer, request, envelopeProblem{status: http.StatusNotFound, id: message.ID, code: -32601, message: "Method not found"}, "", "", nil)
			return
		}
		taskID, ok := message.Params["taskId"].(string)
		if !ok || taskID == "" {
			h.refuse(writer, request, envelopeProblem{status: http.StatusBadRequest, id: message.ID, code: -32602, message: "taskId is required"}, "", "", nil)
			return
		}
		tasks, ok := h.runtime.(TaskRuntime)
		if !ok {
			h.internal(writer, request, message.ID, errors.New("the runtime does not serve the Tasks extension"))
			return
		}
		var (
			answer any
			err    error
		)
		if message.Method == MethodTasksGet {
			answer, err = tasks.GetTask(request.Context(), taskID)
		} else {
			answer, err = tasks.CancelTask(request.Context(), taskID)
		}
		if err != nil {
			h.taskProblem(writer, request, message.ID, err)
			return
		}
		switch typed := answer.(type) {
		case PublicTaskResult:
			result = taskResultWire(typed)
		case PublicTaskCancel:
			result = taskCancelWire(taskID, typed)
		default:
			h.internal(writer, request, message.ID, errors.New("the runtime answered a task method with an unknown result"))
			return
		}
	case "tools/list":
		tools, err := h.runtime.ListTools(request.Context())
		if err != nil {
			h.internal(writer, request, message.ID, err)
			return
		}
		listed := make([]map[string]any, 0, len(tools))
		for _, tool := range tools {
			entry := map[string]any{"name": tool.Name, "description": tool.Description, "inputSchema": tool.InputSchema}
			if len(tool.OutputSchema) > 0 {
				entry["outputSchema"] = tool.OutputSchema
			}
			listed = append(listed, entry)
		}
		result = map[string]any{"resultType": "complete", "tools": listed, "ttlMs": 0, "cacheScope": "private"}
	case "tools/call":
		name, ok := message.Params["name"].(string)
		if !ok || request.Header.Get("Mcp-Name") != name {
			h.refuse(writer, request, envelopeProblem{status: http.StatusBadRequest, id: message.ID, code: -32020}, "", "", nil)
			return
		}
		arguments := map[string]any{}
		if raw, present := message.Params["arguments"]; present && raw != nil {
			typed, isObject := raw.(map[string]any)
			if !isObject {
				h.refuse(writer, request, envelopeProblem{status: http.StatusBadRequest, id: message.ID, code: -32602, message: "Arguments must be an object"}, "", "", nil)
				return
			}
			arguments = typed
		}
		encoded, _ := json.Marshal(arguments)
		outcome, err := h.runtime.CallTool(request.Context(), name, encoded)
		var scope *ScopeError
		switch {
		case errors.Is(err, ErrUnknownTool):
			h.refuse(writer, request, envelopeProblem{status: http.StatusBadRequest, id: message.ID, code: -32602, message: "Unknown tool"}, "", "", nil)
			return
		case errors.As(err, &scope):
			writer.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="`+scope.Scope+`"`)
			h.refuse(writer, request, envelopeProblem{status: http.StatusForbidden, id: message.ID, code: -32003, message: "Insufficient scope"}, ProblemInsufficientScope, scope.Scope, nil)
			return
		case err != nil:
			h.internal(writer, request, message.ID, err)
			return
		}
		if outcome.Task != nil && h.clientDeclaredTasks(message) {
			result = createTaskWire(*outcome.Task)
			break
		}
		called, err := outcome.CallToolResult()
		if err != nil {
			h.internal(writer, request, message.ID, err)
			return
		}
		called["resultType"] = "complete"
		result = called
	default:
		h.refuse(writer, request, envelopeProblem{status: http.StatusNotFound, id: message.ID, code: -32601, message: "Method not found"}, "", "", nil)
		return
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": result})
	if err != nil || int64(len(body)) > h.policy.MaxResponseBytes {
		if err == nil {
			err = errors.New("response exceeds the size limit")
		}
		h.internal(writer, request, message.ID, err)
		return
	}
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(body)
}

// servesTaskMethod says whether the endpoint serves one of the extension's
// methods: the extension must be advertised, and the method listed.
func (h *PublicHandler) servesTaskMethod(method string) bool {
	return h.policy.TasksExtension != "" && slices.Contains(h.policy.TasksMethods, method)
}

// clientDeclaredTasks reads the per-request extension negotiation: the client
// lists the Tasks extension in the capabilities it sent with this request.
func (h *PublicHandler) clientDeclaredTasks(message serverRequest) bool {
	if h.policy.TasksExtension == "" {
		return false
	}
	meta, _ := message.Params["_meta"].(map[string]any)
	capabilities, _ := meta["io.modelcontextprotocol/clientCapabilities"].(map[string]any)
	extensions, _ := capabilities["extensions"].(map[string]any)
	_, declared := extensions[h.policy.TasksExtension]
	return declared
}

// taskProblem answers an error of a task method: a Task the caller cannot see
// is 404 with the extension's code, a missing scope is 403, anything else is
// internal.
func (h *PublicHandler) taskProblem(writer http.ResponseWriter, request *http.Request, id json.RawMessage, err error) {
	var missing *TaskError
	var scope *ScopeError
	switch {
	case errors.As(err, &missing):
		status := http.StatusBadRequest
		if missing.Code == TaskNotFound {
			status = http.StatusNotFound
		}
		h.refuse(writer, request, envelopeProblem{status: status, id: id, code: missing.Code, message: missing.Message}, "", "", nil)
	case errors.As(err, &scope):
		writer.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="`+scope.Scope+`"`)
		h.refuse(writer, request, envelopeProblem{status: http.StatusForbidden, id: id, code: -32003, message: "Insufficient scope"}, ProblemInsufficientScope, scope.Scope, nil)
	default:
		h.internal(writer, request, id, err)
	}
}

// taskFields is the Task object's members, shared by CreateTaskResult and
// tasks/get. ttlMs is required by the schema and may be null.
func taskFields(task PublicTask) map[string]any {
	fields := map[string]any{
		"taskId": task.TaskID, "status": task.Status,
		"createdAt": task.CreatedAt, "lastUpdatedAt": task.LastUpdatedAt,
		"ttlMs": nil,
	}
	if task.TTLMs != nil {
		fields["ttlMs"] = *task.TTLMs
	}
	if task.StatusMessage != "" {
		fields["statusMessage"] = task.StatusMessage
	}
	if task.PollIntervalMs != nil {
		fields["pollIntervalMs"] = *task.PollIntervalMs
	}
	return fields
}

// createTaskWire is the CreateTaskResult a tools/call answers a client that
// declared the extension with.
func createTaskWire(task PublicTask) map[string]any {
	fields := taskFields(task)
	fields["resultType"] = "task"
	return fields
}

// taskResultWire is the tasks/get answer: the Task with the completed result
// or the failure attached.
func taskResultWire(result PublicTaskResult) map[string]any {
	fields := taskFields(result.PublicTask)
	fields["resultType"] = "complete"
	if len(result.Result) > 0 && json.Valid(result.Result) {
		fields["result"] = result.Result
	}
	if result.Error != nil {
		failure := map[string]any{"code": result.Error.Code, "message": result.Error.Message}
		if result.Error.Data != nil {
			failure["data"] = result.Error.Data
		}
		fields["error"] = failure
	}
	return fields
}

// taskCancelWire is the tasks/cancel acknowledgement. The schema declares only
// resultType; the outcome (and whether the Task was already terminal) is in
// _meta under the extension's name.
func taskCancelWire(taskID string, result PublicTaskCancel) map[string]any {
	acknowledged := map[string]any{"resultType": "complete"}
	if result.Status == "" && !result.AlreadyTerminal {
		return acknowledged
	}
	detail := map[string]any{"taskId": taskID, "status": result.Status, "alreadyTerminal": result.AlreadyTerminal}
	if result.StatusMessage != "" {
		detail["statusMessage"] = result.StatusMessage
	}
	acknowledged["_meta"] = map[string]any{TasksExtension: detail}
	return acknowledged
}

func (h *PublicHandler) internal(writer http.ResponseWriter, request *http.Request, id json.RawMessage, cause error) {
	h.refuse(writer, request, envelopeProblem{status: http.StatusInternalServerError, id: id, code: -32603, message: "Internal error"}, ProblemInternal, "", cause)
}

// refuse answers a problem. When the policy words the problem, the wording is
// in error.data.detail; the JSON-RPC message stays the standard one.
func (h *PublicHandler) refuse(writer http.ResponseWriter, request *http.Request, problem envelopeProblem, kind, scope string, cause error) {
	if kind != "" && h.policy.Detail != nil {
		if detail := strings.TrimSpace(h.policy.Detail(request, kind, scope)); detail != "" {
			if problem.data == nil {
				problem.data = map[string]any{}
			}
			problem.data["detail"] = detail
		}
	}
	if h.policy.Observe != nil {
		h.policy.Observe(request, problem.status, problem.code, cause)
	}
	writeEnvelopeProblem(writer, problem)
}
