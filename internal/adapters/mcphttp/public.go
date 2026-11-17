package mcphttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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
}

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
		discovered := map[string]any{
			"resultType":   "complete",
			"serverInfo":   map[string]string{"name": h.policy.ServerName, "version": "1"},
			"capabilities": map[string]any{"tools": map[string]any{}},
		}
		if h.policy.Instructions != nil {
			discovered["instructions"] = h.policy.Instructions(request)
		}
		result = discovered
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
		content := make([]map[string]string, 0, len(outcome.Text))
		for _, text := range outcome.Text {
			content = append(content, map[string]string{"type": "text", "text": text})
		}
		called := map[string]any{"resultType": "complete", "content": content, "isError": outcome.IsError}
		if len(outcome.Structured) > 0 {
			if !json.Valid(outcome.Structured) {
				h.internal(writer, request, message.ID, errors.New("tool returned invalid structured content"))
				return
			}
			called["structuredContent"] = outcome.Structured
		}
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
