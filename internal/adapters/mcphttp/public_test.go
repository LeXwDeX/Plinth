package mcphttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/mcphttp"
	"github.com/LeXwDeX/Plinth/internal/mcp"
)

type fakePublicRuntime struct {
	mu    sync.Mutex
	calls []string
	tools []mcphttp.PublicTool
	call  func(ctx context.Context, name string, arguments json.RawMessage) (mcphttp.PublicResult, error)
}

func (r *fakePublicRuntime) ListTools(context.Context) ([]mcphttp.PublicTool, error) {
	return r.tools, nil
}

func (r *fakePublicRuntime) CallTool(ctx context.Context, name string, arguments json.RawMessage) (mcphttp.PublicResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, name+" "+string(arguments))
	r.mu.Unlock()
	return r.call(ctx, name, arguments)
}

type observed struct {
	mu       sync.Mutex
	statuses []int
	codes    []int
	causes   []error
}

func (o *observed) observe(_ *http.Request, status, code int, cause error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.statuses, o.codes, o.causes = append(o.statuses, status), append(o.codes, code), append(o.causes, cause)
}

func newPublic(t *testing.T, runtime *fakePublicRuntime, seen *observed) http.Handler {
	t.Helper()
	handler, err := mcphttp.NewPublicHandler(runtime, mcphttp.PublicPolicy{
		ProtocolVersion: mcphttp.ProtocolVersion, ServerName: "Plinth",
		Instructions:    func(*http.Request) string { return "read the status block" },
		MaxRequestBytes: 2048, MaxResponseBytes: 4096, Observe: seen.observe,
		Detail: func(_ *http.Request, kind, scope string) string { return kind + ":" + scope },
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

type response struct {
	status int
	header http.Header
	body   map[string]any
	raw    string
}

func envelope(method string, params map[string]any) string {
	meta := map[string]any{
		"io.modelcontextprotocol/protocolVersion":    mcphttp.ProtocolVersion,
		"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "test", "version": "1"},
		"io.modelcontextprotocol/clientCapabilities": map[string]any{},
	}
	if params == nil {
		params = map[string]any{}
	}
	params["_meta"] = meta
	encoded, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 7, "method": method, "params": params})
	return string(encoded)
}

func send(t *testing.T, handler http.Handler, method, body string, mutate func(*http.Request)) response {
	t.Helper()
	request := httptest.NewRequest(method, "/mcp", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("MCP-Protocol-Version", mcphttp.ProtocolVersion)
	var parsed struct{ Method string }
	var params struct{ Params struct{ Name string } }
	_ = json.Unmarshal([]byte(body), &parsed)
	_ = json.Unmarshal([]byte(body), &params)
	request.Header.Set("Mcp-Method", parsed.Method)
	if params.Params.Name != "" {
		request.Header.Set("Mcp-Name", params.Params.Name)
	}
	if mutate != nil {
		mutate(request)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	result := response{status: recorder.Code, header: recorder.Header(), raw: recorder.Body.String()}
	_ = json.Unmarshal(recorder.Body.Bytes(), &result.body)
	return result
}

func rpcError(t *testing.T, r response) (int, map[string]any) {
	t.Helper()
	failure, ok := r.body["error"].(map[string]any)
	if !ok {
		t.Fatalf("no JSON-RPC error in %s", r.raw)
	}
	return int(failure["code"].(float64)), failure
}

func echoRuntime() *fakePublicRuntime {
	return &fakePublicRuntime{
		tools: []mcphttp.PublicTool{{
			Name: "echo", Description: "echo it", InputSchema: json.RawMessage(`{"type":"object"}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`),
		}, {Name: "bare", Description: "no output schema", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		call: func(_ context.Context, name string, arguments json.RawMessage) (mcphttp.PublicResult, error) {
			return mcphttp.PublicResult{Structured: json.RawMessage(`{"echo":` + string(arguments) + `}`), Text: []string{"first", "last line"}}, nil
		},
	}
}

func TestPublicDiscoverAndToolListContract(t *testing.T) {
	t.Parallel()
	handler := newPublic(t, echoRuntime(), &observed{})
	discover := send(t, handler, http.MethodPost, envelope("server/discover", nil), nil)
	result := discover.body["result"].(map[string]any)
	info := result["serverInfo"].(map[string]any)
	if discover.status != 200 || discover.body["id"].(float64) != 7 || result["resultType"] != "complete" ||
		info["name"] != "Plinth" || result["instructions"] != "read the status block" || result["capabilities"].(map[string]any)["tools"] == nil {
		t.Fatalf("discover = %d %s", discover.status, discover.raw)
	}
	if discover.header.Get("Mcp-Session-Id") != "" || discover.header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("headers = %v; a stateless endpoint issues no session", discover.header)
	}
	list := send(t, handler, http.MethodPost, envelope("tools/list", nil), nil)
	tools := list.body["result"].(map[string]any)["tools"].([]any)
	if list.status != 200 || len(tools) != 2 {
		t.Fatalf("tools/list = %d %s", list.status, list.raw)
	}
	first, second := tools[0].(map[string]any), tools[1].(map[string]any)
	if first["name"] != "echo" || first["description"] != "echo it" || first["inputSchema"] == nil || first["outputSchema"] == nil {
		t.Fatalf("first tool = %v", first)
	}
	if _, has := second["outputSchema"]; has {
		t.Fatalf("a tool without an output schema advertises one: %v", second)
	}
}

func TestPublicToolCallReturnsStructuredContentAndTextBlocks(t *testing.T) {
	t.Parallel()
	runtime := echoRuntime()
	handler := newPublic(t, runtime, &observed{})
	call := send(t, handler, http.MethodPost, envelope("tools/call", map[string]any{
		"name": "echo", "arguments": map[string]any{"list": []any{1, 2.5, "x"}, "nested": map[string]any{"deep": true}},
	}), nil)
	result := call.body["result"].(map[string]any)
	content := result["content"].([]any)
	if call.status != 200 || result["isError"] != false || result["resultType"] != "complete" || len(content) != 2 ||
		content[1].(map[string]any)["text"] != "last line" || content[0].(map[string]any)["type"] != "text" {
		t.Fatalf("call = %d %s", call.status, call.raw)
	}
	echo := result["structuredContent"].(map[string]any)["echo"].(map[string]any)
	if len(echo["list"].([]any)) != 3 || echo["nested"].(map[string]any)["deep"] != true {
		t.Fatalf("arrays, numbers and objects were not carried: %v", echo)
	}
}

func TestPublicToolCallWithoutArgumentsGetsAnEmptyObject(t *testing.T) {
	t.Parallel()
	runtime := echoRuntime()
	handler := newPublic(t, runtime, &observed{})
	if call := send(t, handler, http.MethodPost, envelope("tools/call", map[string]any{"name": "echo"}), nil); call.status != 200 {
		t.Fatalf("call = %d %s", call.status, call.raw)
	}
	if len(runtime.calls) != 1 || runtime.calls[0] != "echo {}" {
		t.Fatalf("calls = %v", runtime.calls)
	}
}

// A business failure is a result the Agent can read and act on: the HTTP call
// and the JSON-RPC call succeed, the tool result says isError. The platform's
// own endpoint folds every tool error into one protocol error instead.
func TestPublicBusinessErrorIsAToolResultWhileTheInternalEndpointFoldsIt(t *testing.T) {
	t.Parallel()
	runtime := echoRuntime()
	runtime.call = func(context.Context, string, json.RawMessage) (mcphttp.PublicResult, error) {
		return mcphttp.PublicResult{IsError: true, Structured: json.RawMessage(`{"error":{"code":"task_not_found","message":"no such task"}}`), Text: []string{"no such task"}}, nil
	}
	seen := &observed{}
	call := send(t, newPublic(t, runtime, seen), http.MethodPost, envelope("tools/call", map[string]any{"name": "echo", "arguments": map[string]any{}}), nil)
	result := call.body["result"].(map[string]any)
	if call.status != 200 || result["isError"] != true || call.body["error"] != nil ||
		result["structuredContent"].(map[string]any)["error"].(map[string]any)["code"] != "task_not_found" {
		t.Fatalf("business error = %d %s", call.status, call.raw)
	}
	if len(seen.statuses) != 0 {
		t.Fatalf("a business error was observed as a refusal: %v", seen.statuses)
	}

	internal := mcphttp.NewHandler(foldingRuntime{}, mcphttp.Policy{MaxResponseBytes: 4096, MaxTools: 4})
	folded := send(t, internal, http.MethodPost, envelope("tools/call", map[string]any{"name": "echo", "arguments": map[string]any{}}), nil)
	if folded.status != 400 || folded.body["result"] != nil {
		t.Fatalf("the internal endpoint = %d %s; it must keep folding tool errors into a protocol error", folded.status, folded.raw)
	}
	if code, _ := rpcError(t, folded); code != -32602 {
		t.Fatalf("internal code = %d", code)
	}
}

type foldingRuntime struct{}

func (foldingRuntime) ListTools(context.Context) ([]mcp.Tool, error) { return nil, nil }
func (foldingRuntime) CallTool(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return nil, mcp.ErrUnavailable
}

func TestPublicRefusalsAreProtocolErrorsAndAreObserved(t *testing.T) {
	t.Parallel()
	runtime := echoRuntime()
	runtime.call = func(_ context.Context, name string, _ json.RawMessage) (mcphttp.PublicResult, error) {
		switch name {
		case "scoped":
			return mcphttp.PublicResult{}, &mcphttp.ScopeError{Scope: "generate"}
		case "missing":
			return mcphttp.PublicResult{}, mcphttp.ErrUnknownTool
		case "badjson":
			return mcphttp.PublicResult{Structured: json.RawMessage(`{broken`)}, nil
		case "huge":
			return mcphttp.PublicResult{Structured: json.RawMessage(`{"x":"` + strings.Repeat("a", 5000) + `"}`)}, nil
		}
		return mcphttp.PublicResult{}, errors.New("secret database path /var/db failed")
	}
	seen := &observed{}
	handler := newPublic(t, runtime, seen)
	call := func(name string) response {
		return send(t, handler, http.MethodPost, envelope("tools/call", map[string]any{"name": name, "arguments": map[string]any{}}), nil)
	}

	scoped := call("scoped")
	if code, failure := rpcError(t, scoped); scoped.status != 403 || code != -32003 ||
		scoped.header.Get("WWW-Authenticate") != `Bearer error="insufficient_scope", scope="generate"` ||
		failure["data"].(map[string]any)["detail"] != "insufficient_scope:generate" {
		t.Fatalf("scope refusal = %d %s %v", scoped.status, scoped.raw, scoped.header)
	}
	missing := call("missing")
	if code, _ := rpcError(t, missing); missing.status != 400 || code != -32602 {
		t.Fatalf("unknown tool = %d %s", missing.status, missing.raw)
	}
	for _, name := range []string{"other", "badjson", "huge"} {
		failed := call(name)
		code, failure := rpcError(t, failed)
		if failed.status != 500 || code != -32603 || strings.Contains(failed.raw, "database") || strings.Contains(failed.raw, "/var") {
			t.Fatalf("%s = %d %s; an internal failure must say nothing of its cause", name, failed.status, failed.raw)
		}
		if failure["data"].(map[string]any)["detail"] != "internal:" {
			t.Fatalf("%s detail = %v", name, failure)
		}
	}
	if len(seen.statuses) != 5 || seen.causes[0] != nil || seen.causes[2] == nil || !strings.Contains(seen.causes[2].Error(), "database") {
		t.Fatalf("observed = %v causes = %v; every refusal is observed and an internal one carries its cause", seen.statuses, seen.causes)
	}
}

func TestPublicEnvelopeIsCheckedLikeTheInternalEndpoint(t *testing.T) {
	t.Parallel()
	handler := newPublic(t, echoRuntime(), &observed{})
	valid := envelope("tools/list", nil)
	for name, test := range map[string]struct {
		body   string
		mutate func(*http.Request)
		status int
		code   int
	}{
		"not json":                 {body: "nope", status: 400, code: -32600},
		"wrong jsonrpc":            {body: strings.Replace(valid, `"2.0"`, `"1.0"`, 1), status: 400, code: -32600},
		"null id":                  {body: strings.Replace(valid, `"id":7`, `"id":null`, 1), status: 400, code: -32600},
		"no params":                {body: `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, status: 400, code: -32600},
		"version header mismatch":  {body: valid, mutate: func(r *http.Request) { r.Header.Set("MCP-Protocol-Version", "2025-01-01") }, status: 400, code: -32020},
		"method header mismatch":   {body: valid, mutate: func(r *http.Request) { r.Header.Set("Mcp-Method", "tools/call") }, status: 400, code: -32020},
		"no method header":         {body: valid, mutate: func(r *http.Request) { r.Header.Del("Mcp-Method") }, status: 400, code: -32020},
		"unsupported version":      {body: strings.ReplaceAll(valid, mcphttp.ProtocolVersion, "2025-01-01"), mutate: func(r *http.Request) { r.Header.Set("MCP-Protocol-Version", "2025-01-01") }, status: 400, code: -32022},
		"no client info":           {body: strings.Replace(valid, "clientInfo", "clientInfoX", 1), status: 400, code: -32602},
		"no client capabilities":   {body: strings.Replace(valid, "clientCapabilities", "clientCapabilitiesX", 1), status: 400, code: -32602},
		"unknown method":           {body: envelope("resources/list", nil), status: 404, code: -32601},
		"oversized body":           {body: `{"x":"` + strings.Repeat("a", 3000) + `"}`, status: 413, code: -32600},
		"tool name header missing": {body: envelope("tools/call", map[string]any{"name": "echo"}), mutate: func(r *http.Request) { r.Header.Del("Mcp-Name") }, status: 400, code: -32020},
		"arguments not an object":  {body: envelope("tools/call", map[string]any{"name": "echo", "arguments": []any{1}}), status: 400, code: -32602},
	} {
		got := send(t, handler, http.MethodPost, test.body, test.mutate)
		if code, _ := rpcError(t, got); got.status != test.status || code != test.code {
			t.Errorf("%s = %d %s; want %d %d", name, got.status, got.raw, test.status, test.code)
		}
	}
	version := send(t, handler, http.MethodPost, strings.ReplaceAll(valid, mcphttp.ProtocolVersion, "2025-01-01"), func(r *http.Request) { r.Header.Set("MCP-Protocol-Version", "2025-01-01") })
	if _, failure := rpcError(t, version); failure["data"].(map[string]any)["supported"].([]any)[0] != mcphttp.ProtocolVersion {
		t.Fatalf("unsupported version data = %s", version.raw)
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodPut} {
		refused := send(t, handler, method, "", nil)
		if refused.status != 405 || refused.header.Get("Allow") != http.MethodPost {
			t.Errorf("%s = %d %v", method, refused.status, refused.header)
		}
	}
}

// Nothing is remembered between requests: a session header changes nothing,
// and two requests are answered alike whichever order they arrive in.
func TestPublicEndpointKeepsNoSessionState(t *testing.T) {
	t.Parallel()
	runtime := echoRuntime()
	handler := newPublic(t, runtime, &observed{})
	list := envelope("tools/list", nil)
	plain := send(t, handler, http.MethodPost, list, nil)
	withSession := send(t, handler, http.MethodPost, list, func(r *http.Request) { r.Header.Set("Mcp-Session-Id", "abc") })
	if plain.raw != withSession.raw || withSession.header.Get("Mcp-Session-Id") != "" {
		t.Fatalf("a session header changed the answer: %q vs %q", plain.raw, withSession.raw)
	}
	one := send(t, handler, http.MethodPost, envelope("tools/call", map[string]any{"name": "echo", "arguments": map[string]any{"n": 1}}), nil)
	two := send(t, handler, http.MethodPost, envelope("tools/call", map[string]any{"name": "echo", "arguments": map[string]any{"n": 2}}), nil)
	if strings.Contains(two.raw, `"n":1`) || !strings.Contains(one.raw, `"n":1`) || !strings.Contains(two.raw, `"n":2`) {
		t.Fatalf("the second request saw the first: %s / %s", one.raw, two.raw)
	}
	initialize := send(t, handler, http.MethodPost, envelope("initialize", nil), nil)
	if code, _ := rpcError(t, initialize); initialize.status != 404 || code != -32601 {
		t.Fatalf("a handshake is not part of the stateless protocol: %d %s", initialize.status, initialize.raw)
	}
}

func TestNewPublicHandlerNeedsItsParts(t *testing.T) {
	t.Parallel()
	good := mcphttp.PublicPolicy{ProtocolVersion: mcphttp.ProtocolVersion, MaxRequestBytes: 1, MaxResponseBytes: 1}
	if _, err := mcphttp.NewPublicHandler(echoRuntime(), good); err != nil {
		t.Fatal(err)
	}
	if _, err := mcphttp.NewPublicHandler(nil, good); err == nil {
		t.Error("no runtime was accepted")
	}
	for name, mutate := range map[string]func(*mcphttp.PublicPolicy){
		"no version":  func(p *mcphttp.PublicPolicy) { p.ProtocolVersion = "" },
		"no request":  func(p *mcphttp.PublicPolicy) { p.MaxRequestBytes = 0 },
		"no response": func(p *mcphttp.PublicPolicy) { p.MaxResponseBytes = 0 },
	} {
		policy := good
		mutate(&policy)
		if _, err := mcphttp.NewPublicHandler(echoRuntime(), policy); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
