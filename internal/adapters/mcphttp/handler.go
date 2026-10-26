package mcphttp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/LeXwDeX/Plinth/internal/mcp"
)

// Runtime is restricted to tool description and reads. The authenticated Scope
// is injected by the transport; owner/session fields are never model arguments.
type Runtime interface {
	ListTools(context.Context) ([]mcp.Tool, error)
	CallTool(context.Context, string, json.RawMessage) (json.RawMessage, error)
}
type Handler struct {
	runtime Runtime
	policy  Policy
}

type serverRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  map[string]any  `json:"params"`
}

func NewHandler(runtime Runtime, policy Policy) *Handler {
	return &Handler{runtime: runtime, policy: policy}
}
func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "private, no-store")
	if h.runtime == nil || h.policy.MaxResponseBytes <= 0 || h.policy.MaxTools <= 0 {
		h.problem(writer, http.StatusServiceUnavailable, nil, -32603)
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	contents, err := io.ReadAll(io.LimitReader(request.Body, h.policy.MaxResponseBytes+1))
	if err != nil || int64(len(contents)) > h.policy.MaxResponseBytes {
		h.problem(writer, http.StatusBadRequest, 0, -32600)
		return
	}
	var message serverRequest
	if json.Unmarshal(contents, &message) != nil || message.JSONRPC != "2.0" || !validRequestID(message.ID) || message.Params == nil {
		h.problem(writer, http.StatusBadRequest, 0, -32600)
		return
	}
	meta, ok := message.Params["_meta"].(map[string]any)
	version, versionOK := meta["io.modelcontextprotocol/protocolVersion"].(string)
	if !ok || !versionOK || request.Header.Get("MCP-Protocol-Version") != version || request.Header.Get("Mcp-Method") != message.Method {
		h.problem(writer, http.StatusBadRequest, message.ID, -32020)
		return
	}
	if version != ProtocolVersion {
		writer.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(writer).Encode(map[string]any{"jsonrpc": "2.0", "id": message.ID, "error": map[string]any{"code": -32022, "message": "Unsupported protocol version", "data": map[string]any{"supported": []string{ProtocolVersion}, "requested": version}}})
		return
	}
	if _, ok := meta["io.modelcontextprotocol/clientInfo"].(map[string]any); !ok {
		h.problem(writer, http.StatusBadRequest, message.ID, -32602)
		return
	}
	if _, ok := meta["io.modelcontextprotocol/clientCapabilities"].(map[string]any); !ok {
		h.problem(writer, http.StatusBadRequest, message.ID, -32602)
		return
	}
	var result any
	switch message.Method {
	case "server/discover":
		result = map[string]any{"resultType": "complete", "serverInfo": map[string]string{"name": "Plinth workspace", "version": "1"}, "capabilities": map[string]any{"tools": map[string]any{}}}
	case "tools/list":
		tools, err := h.runtime.ListTools(request.Context())
		if err != nil || len(tools) > h.policy.MaxTools {
			h.problem(writer, http.StatusBadRequest, message.ID, -32602)
			return
		}
		if tools == nil {
			tools = []mcp.Tool{}
		}
		result = map[string]any{"resultType": "complete", "tools": tools, "ttlMs": 0, "cacheScope": "private"}
	case "tools/call":
		name, ok := message.Params["name"].(string)
		if !ok || request.Header.Get("Mcp-Name") != name {
			h.problem(writer, http.StatusBadRequest, message.ID, -32020)
			return
		}
		arguments, ok := message.Params["arguments"].(map[string]any)
		if !ok {
			h.problem(writer, http.StatusBadRequest, message.ID, -32602)
			return
		}
		encoded, _ := json.Marshal(arguments)
		value, err := h.runtime.CallTool(request.Context(), name, encoded)
		if err != nil || !json.Valid(value) {
			h.problem(writer, http.StatusBadRequest, message.ID, -32602)
			return
		}
		result = json.RawMessage(value)
	default:
		h.problem(writer, http.StatusNotFound, message.ID, -32601)
		return
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": result})
	if err != nil || int64(len(body)) > h.policy.MaxResponseBytes {
		h.problem(writer, http.StatusBadRequest, message.ID, -32603)
		return
	}
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(body)
}
func (h *Handler) problem(writer http.ResponseWriter, status int, id any, code int) {
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": "MCP request rejected"}})
}

func validRequestID(value json.RawMessage) bool {
	if len(value) == 0 || len(value) > 256 || string(value) == "null" {
		return false
	}
	var id any
	if json.Unmarshal(value, &id) != nil {
		return false
	}
	switch typed := id.(type) {
	case string:
		return len(typed) > 0
	case float64:
		return typed == float64(int64(typed))
	default:
		return false
	}
}

type localDoer struct{ handler http.Handler }
type recorder struct {
	header http.Header
	status int
	bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }
func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}
func (r *recorder) Write(contents []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.Buffer.Write(contents)
}
func (d localDoer) Do(request *http.Request) (*http.Response, error) {
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	response := &recorder{header: make(http.Header)}
	d.handler.ServeHTTP(response, request)
	return &http.Response{StatusCode: response.status, Header: response.header, Body: io.NopCloser(bytes.NewReader(response.Bytes())), ContentLength: int64(response.Len()), Status: strconv.Itoa(response.status)}, nil
}

// NewLocal still serializes and validates the genuine JSON-RPC envelope and
// metadata. It avoids a self-HTTP authentication bypass or forwarding cookies.
func NewLocal(runtime Runtime, policy Policy) (*Peer, error) {
	return newPeer("http://mcp.local/", ProtocolVersion, "", localDoer{NewHandler(runtime, policy)}, policy)
}
