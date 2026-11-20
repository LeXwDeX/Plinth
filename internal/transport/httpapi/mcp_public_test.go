package httpapi_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/accesskeys"
	"github.com/LeXwDeX/Plinth/internal/accountsettings"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/assetlinks"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema/toolschema"
)

func TestMCPListenerServesOnlyTheMCPRoutes(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "read")
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/"}, {http.MethodGet, "/api/v1/tasks"}, {http.MethodGet, "/api/v1/schema"},
		{http.MethodPost, "/api/v1/mcp"}, {http.MethodGet, "/api/v1/account/access-keys"},
		{http.MethodGet, "/healthz"}, {http.MethodGet, "/assets/index.js"},
	} {
		for _, credential := range []string{"", "Bearer " + key} {
			request := httptest.NewRequest(route.method, route.path, nil)
			if credential != "" {
				request.Header.Set("Authorization", credential)
			}
			recorder := httptest.NewRecorder()
			f.handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), `"jsonrpc":"2.0"`) {
				t.Errorf("%s %s (key: %v) = %d %s; want a JSON 404", route.method, route.path, credential != "", recorder.Code, recorder.Body.String())
			}
		}
	}
	// A path of the service with the wrong method is told so before any key is asked for.
	for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodPut} {
		recorder := httptest.NewRecorder()
		f.handler.ServeHTTP(recorder, httptest.NewRequest(method, "/mcp", nil))
		if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != http.MethodPost {
			t.Errorf("%s /mcp = %d %v", method, recorder.Code, recorder.Header())
		}
	}
}

func TestMCPListenerAcceptsAccessKeysOnly(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	valid := f.issueKey("owner-a", "read")
	wrongSecret := valid[:len(valid)-1] + map[bool]string{true: "b", false: "a"}[valid[len(valid)-1] == 'a']
	revoked := f.issueKey("owner-a", "read")
	f.revoke("owner-a", revoked)
	expired, err := f.keys.Create(context.Background(), accesskeys.CreateRequest{OwnerID: "owner-a", Name: "short", Scopes: []string{"read"}, TTL: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)

	var answers []string
	for name, mutate := range map[string]func(*http.Request){
		"no header":    func(r *http.Request) { r.Header.Del("Authorization") },
		"empty bearer": func(r *http.Request) { r.Header.Set("Authorization", "Bearer ") },
		"not a bearer": func(r *http.Request) { r.Header.Set("Authorization", "Basic YTpi") },
		"cookie only": func(r *http.Request) {
			r.Header.Del("Authorization")
			r.AddCookie(&http.Cookie{Name: "session", Value: "abc"})
		},
		"deployment token":   func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+deployToken) },
		"development header": func(r *http.Request) { r.Header.Del("Authorization"); r.Header.Set("X-User-ID", "owner-a") },
		"malformed key":      func(r *http.Request) { r.Header.Set("Authorization", "Bearer plk_nonsense") },
		"made-up key": func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer plk_"+strings.Repeat("a", 16)+"_"+strings.Repeat("b", 52))
		},
		"wrong secret": func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+wrongSecret) },
		"revoked key":  func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+revoked) },
		"expired key":  func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+expired.Key) },
		"key beside a cookie": func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+wrongSecret)
			r.AddCookie(&http.Cookie{Name: "session", Value: "x"})
		},
	} {
		recorder := f.post(valid, "tools/call", map[string]any{"name": "get_status", "arguments": map[string]any{}}, mutate)
		result := decodeToolResult(t, recorder)
		if recorder.Code != http.StatusUnauthorized || recorder.Header().Get("WWW-Authenticate") != `Bearer realm="Plinth"` ||
			recorder.Header().Get("X-Request-ID") == "" || result.rpcError["code"].(float64) != -32001 {
			t.Errorf("%s = %d %v %s", name, recorder.Code, recorder.Header(), recorder.Body.String())
			continue
		}
		data := result.rpcError["data"].(map[string]any)
		if data["request_id"] != recorder.Header().Get("X-Request-ID") || !strings.Contains(data["detail"].(string), "访问 Key") {
			t.Errorf("%s: data = %v", name, data)
		}
		for _, secret := range []string{valid, wrongSecret, revoked, expired.Key} {
			if strings.Contains(recorder.Body.String(), secret) {
				t.Errorf("%s: the refusal echoes a key", name)
			}
		}
		answers = append(answers, strings.Join(strings.Fields(recorder.Body.String()), " "))
	}
	// The caller can never tell which kind of refusal it got: apart from the
	// request ID, the answers are the same.
	for _, answer := range answers {
		if stripRequestID(answer) != stripRequestID(answers[0]) {
			t.Fatalf("refusals differ:\n%s\n%s", answer, answers[0])
		}
	}
	if good := f.call(valid, "get_status", nil); good.code != http.StatusOK || good.isError {
		t.Fatalf("the valid key = %d %s", good.code, good.raw)
	}
	// An owner that was deleted or can no longer act cannot use the key it holds.
	for _, owner := range []string{"owner-gone", "owner-off", "owner-unknown"} {
		if refused := f.call(f.issueKey(owner, "read"), "get_status", nil); refused.code != http.StatusUnauthorized {
			t.Errorf("owner %s: %d %s", owner, refused.code, refused.raw)
		}
	}
	// The refusals are in the access log with their request ID, and the log has
	// no key and no Authorization header value (the Schema's wording of the
	// refusal names the header, not a credential).
	log := f.serverLog()
	if !strings.Contains(log, "status=401") || !strings.Contains(log, "error_code=unauthenticated") || !strings.Contains(log, "request_id=req_") {
		t.Fatalf("the access log does not record the refusals:\n%s", log)
	}
	for _, secret := range []string{valid, wrongSecret, revoked, expired.Key, "Bearer plk_", deployToken} {
		if strings.Contains(log, secret) {
			t.Fatalf("the server log contains %q", secret)
		}
	}
}

func stripRequestID(answer string) string {
	for {
		start := strings.Index(answer, "req_")
		if start < 0 {
			return answer
		}
		end := start + 4
		for end < len(answer) && strings.ContainsRune("0123456789abcdef", rune(answer[end])) {
			end++
		}
		answer = answer[:start] + "ID" + answer[end:]
	}
}

func TestMCPKeyIsRefusedByTheMainServer(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "read", "generate")
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	request.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	f.web.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("the main server answered a key with %d %s", recorder.Code, recorder.Body.String())
	}
}

// A key that may generate may also read: a caller that starts work has to be
// able to poll the Tasks it started and fetch their files. A key never gains
// the right to generate by reading.
func TestMCPGenerateScopeIncludesRead(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	generateOnly := f.issueKey("owner-a", "generate")
	list := decodeToolResultRaw(t, f.post(generateOnly, "tools/list", nil))
	if len(list["tools"].([]any)) != 12 {
		t.Fatalf("a generate key lists %d tools; it carries the read scope too, so 7 reading and 5 starting tools", len(list["tools"].([]any)))
	}
	task := f.succeededTask("owner-a", "", "generate-reads")
	for _, tool := range []struct {
		name string
		args map[string]any
	}{
		{"get_status", nil}, {"list_services", nil}, {"list_tasks", nil},
		{"get_task", map[string]any{"task_id": string(task.ID)}},
		{"get_task_assets", map[string]any{"task_id": string(task.ID)}},
		{"inspect_model", map[string]any{"task_id": string(task.ID)}},
	} {
		if result := f.call(generateOnly, tool.name, tool.args); result.code != http.StatusOK || result.isError {
			t.Errorf("%s with a generate key = %d %s", tool.name, result.code, result.raw)
		}
	}
	download := httptest.NewRequest(http.MethodGet, "/mcp/tasks/"+string(task.ID)+"/assets/"+string(task.Assets[0].ID), nil)
	download.Header.Set("Authorization", "Bearer "+generateOnly)
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, download)
	if recorder.Code != http.StatusOK || recorder.Body.Len() == 0 {
		t.Fatalf("download with a generate key = %d", recorder.Code)
	}
	// What is stored and listed is what the key was issued with.
	if stored, err := f.keys.List(context.Background(), "owner-a"); err != nil || len(stored) != 1 || strings.Join(stored[0].Scopes, ",") != "generate" {
		t.Fatalf("stored = %+v err=%v", stored, err)
	}
}

// The scope of a tool is the Schema's: a tool that needs the generate scope is
// refused to a key that may only read, and left out of its tool list.
func TestMCPScopeIsEnforcedPerTool(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil, func(application *catalog.ApplicationSchema) {
		for index := range application.MCPPublic.Tools {
			if application.MCPPublic.Tools[index].Name == "get_task" {
				application.MCPPublic.Tools[index].Scope = "generate"
			}
		}
	})
	readOnly, generateOnly := f.issueKey("owner-a", "read"), f.issueKey("owner-a", "generate")
	toolNames := func(key string) []string {
		var names []string
		for _, raw := range decodeToolResultRaw(t, f.post(key, "tools/list", nil))["tools"].([]any) {
			names = append(names, raw.(map[string]any)["name"].(string))
		}
		return names
	}
	if names := toolNames(readOnly); len(names) != 6 || containsString(anySlice(names), "get_task") {
		t.Fatalf("a read key lists %v", names)
	}
	if names := toolNames(generateOnly); len(names) != 12 || !containsString(anySlice(names), "get_task") {
		t.Fatalf("a generate key lists %v", names)
	}
	denied := f.post(readOnly, "tools/call", map[string]any{"name": "get_task", "arguments": map[string]any{"task_id": "task_x"}})
	if denied.Code != http.StatusForbidden || denied.Header().Get("WWW-Authenticate") != `Bearer error="insufficient_scope", scope="generate"` {
		t.Fatalf("scope refusal = %d %v %s", denied.Code, denied.Header(), denied.Body.String())
	}
	if detail := decodeToolResult(t, denied).rpcError["data"].(map[string]any)["detail"].(string); !strings.Contains(detail, "「generate」") {
		t.Fatalf("detail = %q", detail)
	}
	if code := decodeToolResult(t, denied).rpcError["code"].(float64); code != -32003 {
		t.Fatalf("code = %v", code)
	}
	if ok := f.call(generateOnly, "get_task", map[string]any{"task_id": "task_x"}); ok.code != http.StatusOK || !ok.isError || ok.problemCode() != "task_not_found" {
		t.Fatalf("the generate key was refused: %d %s", ok.code, ok.raw)
	}
	// Every other tool needs read, which both keys carry.
	for _, key := range []string{readOnly, generateOnly} {
		if status := f.call(key, "get_status", nil); status.code != http.StatusOK || status.isError {
			t.Fatalf("get_status = %d %s", status.code, status.raw)
		}
	}
	// The download route needs read, which both keys carry.
	task := f.succeededTask("owner-a", "", "scope-task")
	for _, key := range []string{readOnly, generateOnly} {
		download := httptest.NewRequest(http.MethodGet, "/mcp/tasks/"+string(task.ID)+"/assets/"+string(task.Assets[0].ID), nil)
		download.Header.Set("Authorization", "Bearer "+key)
		recorder := httptest.NewRecorder()
		f.handler.ServeHTTP(recorder, download)
		if recorder.Code != http.StatusOK {
			t.Fatalf("download = %d", recorder.Code)
		}
	}
}

func anySlice(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

func TestMCPProtocolContractThroughTheListener(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "read")

	discover := decodeToolResultRaw(t, f.post(key, "server/discover", nil))
	info := discover["serverInfo"].(map[string]any)
	if info["name"] != "Plinth" || !strings.Contains(discover["instructions"].(string), "状态块") || discover["capabilities"].(map[string]any)["tools"] == nil {
		t.Fatalf("discover = %v", discover)
	}
	english := decodeToolResultRaw(t, f.post(key, "server/discover", nil, func(r *http.Request) { r.Header.Set("Accept-Language", "en") }))
	if !strings.Contains(english["instructions"].(string), "status block") {
		t.Fatalf("english instructions = %v", english["instructions"])
	}

	list := decodeToolResultRaw(t, f.post(key, "tools/list", nil))
	tools := list["tools"].([]any)
	if len(tools) != 7 {
		t.Fatalf("tools = %d", len(tools))
	}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		output := tool["outputSchema"].(map[string]any)
		properties := output["properties"].(map[string]any)
		if tool["description"] == "" || tool["inputSchema"].(map[string]any)["additionalProperties"] != false ||
			properties["status"] == nil || !containsString(output["required"], "status") {
			t.Errorf("tool %v: schemas = %v", tool["name"], tool)
		}
	}
	if englishList := decodeToolResultRaw(t, f.post(key, "tools/list", nil, func(r *http.Request) { r.Header.Set("Accept-Language", "en") })); !strings.Contains(
		englishList["tools"].([]any)[2].(map[string]any)["description"].(string), "Reads one task") {
		t.Fatalf("english tool description = %v", englishList["tools"].([]any)[2])
	}

	for name, test := range map[string]struct {
		method string
		params map[string]any
		mutate func(*http.Request)
		status int
		code   float64
	}{
		"unknown tool":         {"tools/call", map[string]any{"name": "delete_everything", "arguments": map[string]any{}}, nil, 400, -32602},
		"unknown method":       {"resources/list", nil, nil, 404, -32601},
		"handshake":            {"initialize", nil, nil, 404, -32601},
		"wrong protocol":       {"tools/list", nil, func(r *http.Request) { r.Header.Set("MCP-Protocol-Version", "2025-01-01") }, 400, -32020},
		"method header":        {"tools/list", nil, func(r *http.Request) { r.Header.Set("Mcp-Method", "tools/call") }, 400, -32020},
		"arguments not object": {"tools/call", map[string]any{"name": "get_status", "arguments": []any{}}, nil, 400, -32602},
	} {
		recorder := f.post(key, test.method, test.params, test.mutate)
		result := decodeToolResult(t, recorder)
		if recorder.Code != test.status || result.rpcError == nil || result.rpcError["code"].(float64) != test.code {
			t.Errorf("%s = %d %s", name, recorder.Code, recorder.Body.String())
		}
	}

	// Oversized request bodies are refused with 413 before anything runs.
	huge := f.post(key, "tools/call", map[string]any{"name": "get_status", "arguments": map[string]any{"padding": strings.Repeat("x", 1<<20+10)}})
	if huge.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body = %d", huge.Code)
	}
	if log := f.serverLog(); !strings.Contains(log, "status=413") || !strings.Contains(log, "error_code=mcp_invalid_request") {
		t.Fatalf("the 413 is not in the access log:\n%s", log)
	}

	// No session: a header that names one changes nothing, none is issued, and
	// two requests are independent.
	first := f.post(key, "tools/list", nil)
	second := f.post(key, "tools/list", nil, func(r *http.Request) { r.Header.Set("Mcp-Session-Id", "abc") })
	if first.Header().Get("Mcp-Session-Id") != "" || second.Header().Get("Mcp-Session-Id") != "" ||
		strings.Contains(first.Body.String(), `"id":`+"0") || len(first.Body.String()) != len(second.Body.String()) {
		t.Fatalf("session state leaked: %q %q", first.Body.String(), second.Body.String())
	}
}

func decodeToolResultRaw(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope struct {
		Result map[string]any `json:"result"`
		Error  any            `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil || envelope.Result == nil || recorder.Code != http.StatusOK {
		t.Fatalf("answer = %d %s", recorder.Code, recorder.Body.String())
	}
	return envelope.Result
}

func containsString(list any, want string) bool {
	items, _ := list.([]any)
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

// Every tool answers with the status block under its one key, whatever the
// call: it conforms to the declared schema, and the served output schema
// holds for the whole structured result, a refusal included.
func TestMCPEveryToolResponseCarriesTheStatusBlock(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "read")
	task := f.succeededTask("owner-a", "", "block-task")
	other := f.succeededTask("owner-b", "", "block-other")
	service := f.application.MCPPublic
	for name, call := range map[string]struct {
		tool    string
		args    map[string]any
		isError bool
	}{
		"list_services":                {"list_services", nil, false},
		"get_status":                   {"get_status", nil, false},
		"get_task":                     {"get_task", map[string]any{"task_id": string(task.ID)}, false},
		"list_tasks":                   {"list_tasks", nil, false},
		"list_tasks of an asset":       {"list_tasks", map[string]any{"asset_id": task.SessionID}, false},
		"get_task_assets":              {"get_task_assets", map[string]any{"task_id": string(task.ID)}, false},
		"inspect_model":                {"inspect_model", map[string]any{"task_id": string(task.ID)}, false},
		"inspect_image":                {"inspect_image", map[string]any{"task_id": string(task.ID)}, false},
		"get_task of another person":   {"get_task", map[string]any{"task_id": string(other.ID)}, true},
		"get_task_assets of another":   {"get_task_assets", map[string]any{"task_id": string(other.ID)}, true},
		"list_tasks of another asset":  {"list_tasks", map[string]any{"asset_id": other.SessionID}, true},
		"get_task without its id":      {"get_task", nil, true},
		"get_task with a stray field":  {"get_task", map[string]any{"task_id": "x", "owner_id": "owner-b"}, true},
		"list_tasks with a bad limit":  {"list_tasks", map[string]any{"limit": 0}, true},
		"list_services unknown filter": {"list_services", map[string]any{"capability": "no-such-capability"}, true},
	} {
		result := f.call(key, call.tool, call.args)
		if result.code != http.StatusOK || result.isError != call.isError {
			t.Errorf("%s = %d isError=%v %s", name, result.code, result.isError, result.raw)
			continue
		}
		status := result.status(t)
		for _, field := range []string{"step_mode", "step_mode_label", "step_cap", "asset", "total_used_steps", "balances", "step_costs", "failure", "inspection"} {
			if _, present := status[field]; !present {
				t.Errorf("%s: the status block lacks %q: %s", name, field, result.raw)
			}
		}
		if len(status["balances"].([]any)) != 3 || status["step_mode"] != "agent" {
			t.Errorf("%s: status = %v", name, status)
		}
		served, _, err := service.ToolOutputSchema(call.tool)
		if err != nil {
			t.Fatal(err)
		}
		declared, err := toolschema.Parse(served, toolschema.Options{RootObject: true})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(result.structured)
		if err := declared.Validate(encoded); err != nil {
			t.Errorf("%s: the result breaks the served output schema: %v\n%s", name, err, encoded)
		}
		if len(result.text) != 2 || !strings.HasPrefix(result.text[1], "状态：") || strings.Contains(result.text[1], "\n") {
			t.Errorf("%s: text = %q; the last line must be one status summary", name, result.text)
		}
		if call.isError && result.text[0] == "" {
			t.Errorf("%s: an error result says nothing", name)
		}
		if !call.isError && result.text[0] != string(encoded) && result.text[0] != compact(t, result.raw, "structuredContent") {
			// The first text item is the structured result itself.
			var again map[string]any
			if err := json.Unmarshal([]byte(result.text[0]), &again); err != nil || again["status"] == nil {
				t.Errorf("%s: the first text item is not the result: %q", name, result.text[0])
			}
		}
	}
	// In English the summary line follows the request's language.
	english := decodeToolResult(t, f.post(key, "tools/call", map[string]any{"name": "get_status", "arguments": map[string]any{}},
		func(r *http.Request) { r.Header.Set("Accept-Language", "en") }))
	if !strings.HasPrefix(english.text[1], "Status: 3D step mode \"Managed by the Agent\"") {
		t.Fatalf("english summary = %q", english.text[1])
	}
}

func compact(t *testing.T, raw, key string) string {
	t.Helper()
	var envelope struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return ""
	}
	var out strings.Builder
	out.Write(envelope.Result[key])
	return out.String()
}

func TestMCPGetTaskIsOwnerScopedAndNeverForeign(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	keyA, keyB := f.issueKey("owner-a", "read"), f.issueKey("owner-b", "read")
	mine := f.succeededTask("owner-a", "", "own-task")
	theirs := f.succeededTask("owner-b", "", "their-task")

	own := f.call(keyA, "get_task", map[string]any{"task_id": string(mine.ID)})
	task := own.structured["task"].(map[string]any)
	if own.isError || task["id"] != string(mine.ID) || task["state"] != "SUCCEEDED" || task["state_label"] != "已完成" || task["terminal"] != true ||
		task["outcome"] != "success" || task["asset_id"] != mine.SessionID || task["asset_count"].(float64) < 1 || task["failure"] != nil ||
		task["poll_after_seconds"] != nil || task["capability"] != "text-to-image" || task["provider"] != "openai" {
		t.Fatalf("own task = %s", own.raw)
	}
	if strings.Contains(own.raw, "owner-a") || strings.Contains(own.raw, "storage_key") {
		t.Fatalf("a private field leaked: %s", own.raw)
	}
	if got := own.status(t)["asset"].(map[string]any); got["id"] != mine.SessionID || got["title"] == nil {
		t.Fatalf("the status block does not name the task's asset: %v", got)
	}

	for name, test := range map[string]struct{ key, id string }{
		"another owner's task": {keyA, string(theirs.ID)},
		"the same from B":      {keyB, string(mine.ID)},
		"a made-up id":         {keyA, "task_does_not_exist"},
	} {
		result := f.call(test.key, "get_task", map[string]any{"task_id": test.id})
		if result.code != http.StatusOK || !result.isError || result.problemCode() != "task_not_found" ||
			strings.Contains(result.raw, string(theirs.ID)+`","asset`) || result.status(t)["asset"] != nil {
			t.Errorf("%s = %s", name, result.raw)
		}
	}
	// The same answer for a task that is someone else's and one that never was.
	foreign := f.call(keyA, "get_task", map[string]any{"task_id": string(theirs.ID)})
	absent := f.call(keyA, "get_task", map[string]any{"task_id": "task_does_not_exist"})
	if foreign.text[0] != absent.text[0] || foreign.problemCode() != absent.problemCode() {
		t.Fatalf("a foreign task is told apart from a missing one: %q vs %q", foreign.text[0], absent.text[0])
	}
}

func TestMCPFailedTaskCarriesTheAttributionAndTheAdvice(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "read")
	busy := f.succeededTask("owner-a", "", "failed-busy")
	unknown := f.succeededTask("owner-a", "", "failed-unknown")
	f.exec(`UPDATE generation_tasks SET state = 'FAILED', failure_json = ? WHERE id = ?`,
		`{"class":"rate_limited","retryable":true,"detail":"internal upstream text must not leak"}`, string(busy.ID))
	f.exec(`UPDATE generation_tasks SET state = 'FAILED', failure_json = ? WHERE id = ?`,
		`{"class":"submission_unknown","retryable":false,"ambiguous":true,"detail":"maybe charged"}`, string(unknown.ID))

	retry := f.call(key, "get_task", map[string]any{"task_id": string(busy.ID)})
	failure := retry.structured["task"].(map[string]any)["failure"].(map[string]any)
	blockFailure := retry.status(t)["failure"].(map[string]any)
	if failure["category"] != "upstream_capacity" || failure["next_action"] != "retry_later" || failure["retryable"] != true ||
		failure["user_decision_required"] != false || !strings.Contains(failure["advice"].(string), "retry later") ||
		blockFailure["task_id"] != string(busy.ID) || blockFailure["category"] != "upstream_capacity" || blockFailure["next_action"] != "retry_later" {
		t.Fatalf("failure = %v block = %v", failure, blockFailure)
	}
	if strings.Contains(retry.raw, "internal upstream text") {
		t.Fatalf("the upstream text leaked: %s", retry.raw)
	}

	ambiguous := f.call(key, "get_task", map[string]any{"task_id": string(unknown.ID)})
	block := ambiguous.status(t)["failure"].(map[string]any)
	if block["next_action"] != "ask_user" || block["user_decision_required"] != true || block["retryable"] != false {
		t.Fatalf("a task that may have been charged = %v; it must send the Agent to the person", block)
	}
	if listed := f.call(key, "get_task_assets", map[string]any{"task_id": string(busy.ID)}); listed.status(t)["failure"] == nil {
		t.Fatalf("get_task_assets of a failed task has no attribution: %s", listed.raw)
	}
}

func TestMCPListTasksIsOwnerScopedAndPaged(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	keyA := f.issueKey("owner-a", "read")
	var mine []string
	for _, name := range []string{"page-1", "page-2", "page-3"} {
		mine = append(mine, string(f.succeededTask("owner-a", "", name).ID))
	}
	theirs := f.succeededTask("owner-b", "", "page-foreign")

	seen := map[string]bool{}
	cursor := ""
	for pages := 0; pages < 5; pages++ {
		arguments := map[string]any{"limit": 2}
		if cursor != "" {
			arguments["cursor"] = cursor
		}
		page := f.call(keyA, "list_tasks", arguments)
		if page.isError {
			t.Fatalf("page %d = %s", pages, page.raw)
		}
		tasks := page.structured["tasks"].([]any)
		if len(tasks) > 2 {
			t.Fatalf("a page of limit 2 has %d tasks", len(tasks))
		}
		for _, raw := range tasks {
			id := raw.(map[string]any)["id"].(string)
			if id == string(theirs.ID) {
				t.Fatal("another owner's task was listed")
			}
			seen[id] = true
		}
		next, _ := page.structured["next_cursor"].(string)
		if next == "" {
			break
		}
		cursor = next
	}
	for _, id := range mine {
		if !seen[id] {
			t.Errorf("task %s was not listed", id)
		}
	}
	// By asset: the asset is the session of the tasks.
	session := f.module
	_ = session
	task, err := f.module.Get(context.Background(), "owner-a", generation.TaskID(mine[0]))
	if err != nil {
		t.Fatal(err)
	}
	byAsset := f.call(keyA, "list_tasks", map[string]any{"asset_id": task.SessionID})
	if byAsset.isError || len(byAsset.structured["tasks"].([]any)) != 3 || byAsset.status(t)["asset"].(map[string]any)["id"] != task.SessionID {
		t.Fatalf("by asset = %s", byAsset.raw)
	}
	// Another person's asset does not exist for this key; a bad cursor is the caller's mistake.
	foreign := f.call(keyA, "list_tasks", map[string]any{"asset_id": theirs.SessionID})
	if !foreign.isError || foreign.problemCode() != "asset_not_found" {
		t.Fatalf("foreign asset = %s", foreign.raw)
	}
	if bad := f.call(keyA, "list_tasks", map[string]any{"cursor": "definitely-not-a-cursor"}); !bad.isError || bad.problemCode() != "invalid_arguments" {
		t.Fatalf("bad cursor = %s", bad.raw)
	}
}

func TestMCPGetTaskAssetsAndTheDownloadRoute(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	keyA, keyB := f.issueKey("owner-a", "read"), f.issueKey("owner-b", "read")
	task := f.succeededTask("owner-a", "", "download-task")
	result := f.call(keyA, "get_task_assets", map[string]any{"task_id": string(task.ID)})
	assets := result.structured["assets"].([]any)
	if result.isError || result.structured["complete"] != true || result.structured["state"] != "SUCCEEDED" || len(assets) != len(task.Assets) || len(assets) == 0 {
		t.Fatalf("assets = %s", result.raw)
	}
	if expires, present := result.structured["download_expires_at"]; !present || expires != nil {
		t.Fatalf("unsigned download_expires_at = %v (present %v)", expires, present)
	}
	for _, raw := range assets {
		asset := raw.(map[string]any)
		url := asset["download_url"].(string)
		wantPrefix := "http://example.com/mcp/tasks/" + string(task.ID) + "/assets/"
		if !strings.HasPrefix(url, wantPrefix) || asset["role"] == "" || asset["media_type"] == "" || asset["size"].(float64) <= 0 || len(asset["sha256"].(string)) != 64 {
			t.Fatalf("asset = %v", asset)
		}
		if strings.Contains(url, "/api/v1/") || strings.Contains(url, "storage") {
			t.Fatalf("the download URL points at the main server or storage: %s", url)
		}
		// The URL is served by the listener itself with the same key.
		path := strings.TrimPrefix(url, "http://example.com")
		download := func(key string) *httptest.ResponseRecorder {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			if key != "" {
				request.Header.Set("Authorization", "Bearer "+key)
			}
			recorder := httptest.NewRecorder()
			f.handler.ServeHTTP(recorder, request)
			return recorder
		}
		got := download(keyA)
		content, err := f.module.OpenAsset(context.Background(), "owner-a", task.ID, generation.AssetID(asset["id"].(string)))
		if err != nil {
			t.Fatal(err)
		}
		want, _ := io.ReadAll(content.Body)
		content.Body.Close()
		if got.Code != http.StatusOK || got.Body.String() != string(want) || len(want) == 0 {
			t.Fatalf("download = %d (%d bytes), want %d bytes", got.Code, got.Body.Len(), len(want))
		}
		if got.Header().Get("X-Content-Type-Options") != "nosniff" || got.Header().Get("Content-Security-Policy") != "sandbox" ||
			!strings.Contains(got.Header().Get("Cache-Control"), "no-store") || got.Header().Get("Content-Type") == "" {
			t.Fatalf("download headers = %v", got.Header())
		}
		if other := download(keyB); other.Code != http.StatusNotFound || strings.Contains(other.Body.String(), string(want)) {
			t.Fatalf("another owner's key downloaded: %d", other.Code)
		}
		if anonymous := download(""); anonymous.Code != http.StatusUnauthorized || anonymous.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("anonymous download = %d", anonymous.Code)
		}
	}
	foreign := f.call(keyB, "get_task_assets", map[string]any{"task_id": string(task.ID)})
	if !foreign.isError || foreign.problemCode() != "task_not_found" {
		t.Fatalf("another owner's assets = %s", foreign.raw)
	}
	// A cookie, a deployment token or a made-up asset are refused or not found.
	cookie := httptest.NewRequest(http.MethodGet, "/mcp/tasks/"+string(task.ID)+"/assets/"+string(task.Assets[0].ID), nil)
	cookie.AddCookie(&http.Cookie{Name: "session", Value: "x"})
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, cookie)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("cookie download = %d", recorder.Code)
	}
	missing := httptest.NewRequest(http.MethodGet, "/mcp/tasks/"+string(task.ID)+"/assets/asset_nope", nil)
	missing.Header.Set("Authorization", "Bearer "+keyA)
	recorder = httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, missing)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing asset = %d", recorder.Code)
	}
}

func TestMCPGetTaskAssetsSignsShortLivedDownloadLinks(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "read")
	task := f.succeededTask("owner-a", "", "signed-download-task")

	result := f.call(key, "get_task_assets", map[string]any{"task_id": string(task.ID), "signed": true})
	assets := result.structured["assets"].([]any)
	if result.isError || len(assets) != len(task.Assets) || len(assets) == 0 {
		t.Fatalf("signed assets = %s", result.raw)
	}
	expiresText, ok := result.structured["download_expires_at"].(string)
	if !ok {
		t.Fatalf("download_expires_at = %v", result.structured["download_expires_at"])
	}
	expires, err := time.Parse(time.RFC3339, expiresText)
	if err != nil {
		t.Fatalf("download_expires_at %q: %v", expiresText, err)
	}
	if until := expires.Sub(f.clock.Now()); until < 290*time.Second || until > 310*time.Second {
		t.Fatalf("download_expires_at is %v away, want about the Schema TTL", until)
	}
	unsigned := f.call(key, "get_task_assets", map[string]any{"task_id": string(task.ID), "signed": false})
	if expires := unsigned.structured["download_expires_at"]; expires != nil {
		t.Fatalf("signed:false download_expires_at = %v", expires)
	}
	for _, raw := range unsigned.structured["assets"].([]any) {
		if url := raw.(map[string]any)["download_url"].(string); !strings.HasPrefix(url, "http://example.com/mcp/tasks/") {
			t.Fatalf("signed:false download URL = %s", url)
		}
	}

	var signedPath string
	for _, raw := range assets {
		asset := raw.(map[string]any)
		url := asset["download_url"].(string)
		if !strings.HasPrefix(url, "http://example.com/mcp/signed-assets/") {
			t.Fatalf("signed download URL = %s", url)
		}
		signedPath = strings.TrimPrefix(url, "http://example.com")
		// No Authorization header: the token is the credential.
		recorder := httptest.NewRecorder()
		f.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, signedPath, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("anonymous signed download = %d %s", recorder.Code, recorder.Body.String())
		}
		content, err := f.module.OpenAsset(context.Background(), "owner-a", task.ID, generation.AssetID(asset["id"].(string)))
		if err != nil {
			t.Fatal(err)
		}
		want, _ := io.ReadAll(content.Body)
		content.Body.Close()
		if recorder.Body.String() != string(want) || len(want) == 0 {
			t.Fatalf("download = %d bytes, want %d bytes", recorder.Body.Len(), len(want))
		}
		if sum := fmt.Sprintf("%x", sha256.Sum256(recorder.Body.Bytes())); sum != asset["sha256"] {
			t.Fatalf("download sha256 = %s, want %s", sum, asset["sha256"])
		}
		if float64(len(want)) != asset["size"].(float64) {
			t.Fatalf("download size = %d, want %v", len(want), asset["size"])
		}
		if recorder.Header().Get("X-Content-Type-Options") != "nosniff" || recorder.Header().Get("Content-Security-Policy") != "sandbox" ||
			!strings.Contains(recorder.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("download headers = %v", recorder.Header())
		}
	}

	token := strings.TrimPrefix(signedPath, "/mcp/signed-assets/")
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		t.Fatalf("signed token = %q", token)
	}
	tamper := func(value string) string {
		changed := []byte(value)
		if changed[0] == 'A' {
			changed[0] = 'B'
		} else {
			changed[0] = 'A'
		}
		return string(changed)
	}
	for _, bad := range []string{tamper(parts[0]) + "." + parts[1], parts[0] + "." + tamper(parts[1])} {
		recorder := httptest.NewRecorder()
		f.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/mcp/signed-assets/"+bad, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("tampered token = %d %s", recorder.Code, recorder.Body.String())
		}
	}
	// The same signer's token for another owner names an asset that owner does not have.
	signer := assetlinks.NewSignerWithClock([]byte(strings.Repeat("k", 32)), f.clock.Now)
	foreign, _, err := signer.Sign(task.ID, task.Assets[0].ID, generation.OwnerID("owner-b"), 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/mcp/signed-assets/"+foreign, nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("another owner's signed link = %d %s", recorder.Code, recorder.Body.String())
	}

	log := f.serverLog()
	if strings.Contains(log, token) {
		t.Fatalf("the access log holds the signed token")
	}
	if !strings.Contains(log, "/mcp/signed-assets/{redacted}") {
		t.Fatalf("the access log does not redact the signed route: %s", log)
	}

	// A link past its TTL is refused, the same 404 as any other bad token.
	f.clock.Advance(301 * time.Second)
	recorder = httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, signedPath, nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expired signed link = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestMCPGetStatusListsTheKeysAssetsAndStepsWithoutCountingAnything(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "read")
	f.keySteps("owner-a", "asset-barrel", "key-one", 2)
	time.Sleep(5 * time.Millisecond)
	f.keySteps("owner-a", "asset-crate", "key-one", 1)
	f.keySteps("owner-b", "asset-foreign", "key-two", 4)
	grantsBefore := f.countRows(`SELECT COUNT(*) FROM execution_grants`)
	tasksBefore := f.countRows(`SELECT COUNT(*) FROM generation_tasks`)

	status := f.call(key, "get_status", nil)
	assets := status.structured["assets"].([]any)
	if status.isError || len(assets) != 2 || status.structured["assets_truncated"] != false {
		t.Fatalf("get_status = %s", status.raw)
	}
	crate, barrel := assets[0].(map[string]any), assets[1].(map[string]any)
	if crate["asset_id"] != "asset-crate" || crate["used_steps"].(float64) != 1 || barrel["asset_id"] != "asset-barrel" ||
		barrel["used_steps"].(float64) != 2 || barrel["title"] != "MCP · asset-barrel" || barrel["last_activity_at"] == nil {
		t.Fatalf("assets = %v", assets)
	}
	block := status.status(t)
	if block["total_used_steps"].(float64) != 3 || block["step_mode"] != "agent" || block["step_cap"] != nil || block["asset"] != nil {
		t.Fatalf("status = %v; the other person's steps must not count", block)
	}
	// A call about one asset names its steps.
	asset := f.call(key, "list_tasks", map[string]any{"asset_id": "asset-barrel"})
	if got := asset.status(t)["asset"].(map[string]any); got["used_steps"].(float64) != 2 || got["steps_left"] != nil {
		t.Fatalf("asset block = %v", got)
	}
	// Reading counts no step and creates nothing.
	for range 3 {
		f.call(key, "get_status", nil)
	}
	if f.countRows(`SELECT COUNT(*) FROM execution_grants`) != grantsBefore || f.countRows(`SELECT COUNT(*) FROM generation_tasks`) != tasksBefore {
		t.Fatal("reading the status created a grant or a Task")
	}
	if again := f.call(key, "get_status", nil).status(t); again["total_used_steps"].(float64) != 3 {
		t.Fatalf("reading changed the count: %v", again)
	}

	// Under the cap mode the status says how many steps are left.
	if _, err := f.repository.PutAccountSettings(context.Background(), "owner-a", 0, accountsettings.Settings{
		List: approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeAgent, Cap: 20},
		MCP:  approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeCap, Cap: 5},
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	capped := f.call(key, "list_tasks", map[string]any{"asset_id": "asset-barrel"}).status(t)
	got := capped["asset"].(map[string]any)
	if capped["step_mode"] != "cap" || capped["step_cap"].(float64) != 5 || got["steps_left"].(float64) != 3 || got["used_steps"].(float64) != 2 {
		t.Fatalf("capped status = %v", capped)
	}
}

func TestMCPBalancesAreUnknownNotZeroWhenReadingFailsAndTheCallStillSucceeds(t *testing.T) {
	t.Parallel()
	readers, counting := balanceReaders(map[string]string{"meshy": "1000", "tripo": "50.5", "hyper3d": "7"})
	counting["hyper3d"].fail.Store(true)
	f := newMCPFixture(t, readers)
	key := f.issueKey("owner-a", "read")

	result := f.call(key, "get_status", nil)
	if result.code != http.StatusOK || result.isError {
		t.Fatalf("a failing provider failed the call: %d %s", result.code, result.raw)
	}
	balances := map[string]map[string]any{}
	for _, raw := range result.status(t)["balances"].([]any) {
		entry := raw.(map[string]any)
		balances[entry["provider"].(string)] = entry
	}
	meshy, tripo, hyper := balances["meshy"], balances["tripo"], balances["hyper3d"]
	if meshy["state"] != "ok" || meshy["amount"] != "1000" || meshy["unit"] != "meshy.credit" || meshy["unit_label"] != "Meshy credits" ||
		meshy["label"] != "MeshyAI" || meshy["read_at"] == nil {
		t.Fatalf("meshy = %v", meshy)
	}
	if tripo["amount"] != "50.5" || hyper["state"] != "unknown" || hyper["amount"] != nil || hyper["unit"] != nil || hyper["read_at"] != nil {
		t.Fatalf("tripo = %v hyper3d = %v; an unreadable balance is unknown, not zero", tripo, hyper)
	}
	if !strings.Contains(result.text[1], "Hyper3D 未知") || !strings.Contains(result.text[1], "MeshyAI 1000 Meshy credits") {
		t.Fatalf("summary = %q", result.text[1])
	}

	// The reading is cached: a second call inside the TTL asks no provider, the
	// failing one included; past the TTL each is asked again.
	f.call(key, "get_status", nil)
	for id, reader := range counting {
		if reader.calls.Load() != 1 {
			t.Fatalf("%s was read %d times within the cache time", id, reader.calls.Load())
		}
	}
	counting["hyper3d"].fail.Store(false)
	counting["meshy"].amount.Amount = "999"
	f.clock.Advance(time.Duration(f.application.StepPolicy.BalanceCacheSeconds+1) * time.Second)
	later := f.call(key, "get_status", nil).status(t)["balances"].([]any)
	for id, reader := range counting {
		if reader.calls.Load() != 2 {
			t.Fatalf("%s was read %d times after the cache time", id, reader.calls.Load())
		}
	}
	byProvider := map[string]map[string]any{}
	for _, raw := range later {
		byProvider[raw.(map[string]any)["provider"].(string)] = raw.(map[string]any)
	}
	if byProvider["hyper3d"]["state"] != "ok" || byProvider["hyper3d"]["amount"] != "7" || byProvider["meshy"]["amount"] != "999" {
		t.Fatalf("after the cache time = %v", byProvider)
	}
}

func TestMCPListServicesFollowsTheCatalogAndNeverInventsOne(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "read")
	all := f.call(key, "list_services", nil)
	if all.isError {
		t.Fatalf("list_services = %s", all.raw)
	}
	groups := all.structured["groups"].([]any)
	if len(groups) != 3 {
		t.Fatalf("groups = %d", len(groups))
	}
	listed := map[string]map[string]any{}
	capabilityOf := map[string]string{}
	for _, rawGroup := range groups {
		group := rawGroup.(map[string]any)
		for _, rawCapability := range group["capabilities"].([]any) {
			capability := rawCapability.(map[string]any)
			for _, rawService := range capability["services"].([]any) {
				service := rawService.(map[string]any)
				listed[service["id"].(string)] = service
				capabilityOf[service["id"].(string)] = capability["capability"].(string)
			}
		}
	}
	// The listing is the Catalog's own offering list, filtered by the Schema's
	// service groups: nothing is added and nothing enabled is left out.
	served := map[string]bool{}
	for _, group := range f.application.MCPPublic.ServiceGroups {
		for _, capability := range group.Capabilities {
			for _, offering := range f.module.Offerings(capability) {
				if !offering.Enabled {
					continue
				}
				served[string(offering.ID)] = true
				entry, found := listed[string(offering.ID)]
				if !found {
					t.Errorf("offering %s is missing from list_services", offering.ID)
					continue
				}
				if entry["available"] != f.module.OfferingAvailable(offering.ID) || capabilityOf[string(offering.ID)] != string(capability) ||
					entry["provider"] != offering.ProviderID || entry["model"] != offering.ModelID {
					t.Errorf("offering %s = %v", offering.ID, entry)
				}
			}
		}
	}
	for id := range listed {
		if !served[id] {
			t.Errorf("list_services names %s, which is no enabled offering of a served capability", id)
		}
	}
	// Meshy's 3D service says what a step costs, in the provider's unit, and what it needs.
	image3D := f.module.Offerings(generation.ImageTo3D)[0]
	entry := listed[string(image3D.ID)]
	cost, _ := entry["step_cost"].(map[string]any)
	if image3D.NativePricing != nil && (cost == nil || cost["unit"] == "" || cost["amount"] == "") {
		t.Fatalf("service %s has a native price but no step cost: %v", image3D.ID, entry)
	}
	if len(entry["inputs"].([]any)) == 0 || len(entry["parameters"].([]any)) == 0 {
		t.Fatalf("service %s lists no inputs or parameters: %v", image3D.ID, entry)
	}
	for _, raw := range entry["parameters"].([]any) {
		if raw.(map[string]any)["name"] == "" {
			t.Fatalf("parameter without a name: %v", raw)
		}
	}
	// Filters.
	one := f.call(key, "list_services", map[string]any{"capability": "image-to-3d"})
	oneGroups := one.structured["groups"].([]any)
	if len(oneGroups) != 1 || len(oneGroups[0].(map[string]any)["capabilities"].([]any)) != 1 {
		t.Fatalf("filtered = %s", one.raw)
	}
	if bad := f.call(key, "list_services", map[string]any{"capability": "format-conversion-x"}); !bad.isError || bad.problemCode() != "invalid_arguments" {
		t.Fatalf("unserved capability = %s", bad.raw)
	}
	onlyAvailable := f.call(key, "list_services", map[string]any{"available_only": true})
	for _, rawGroup := range onlyAvailable.structured["groups"].([]any) {
		for _, rawCapability := range rawGroup.(map[string]any)["capabilities"].([]any) {
			for _, rawService := range rawCapability.(map[string]any)["services"].([]any) {
				if rawService.(map[string]any)["available"] != true {
					t.Fatalf("available_only listed an unavailable service: %v", rawService)
				}
			}
		}
	}
	// The labels follow the request's language.
	english := decodeToolResult(t, f.post(key, "tools/call", map[string]any{"name": "list_services", "arguments": map[string]any{"capability": "text-to-3d"}},
		func(r *http.Request) { r.Header.Set("Accept-Language", "en") }))
	if english.isError || strings.Contains(english.raw, "文生3D") {
		t.Fatalf("english listing = %s", english.raw)
	}
}

func TestMCPToolDeclarationsAreAllImplemented(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	// A Schema that declares a tool the code does not implement cannot be served.
	application.MCPPublic.Tools = append(application.MCPPublic.Tools, catalog.MCPPublicTool{
		Name: "unwritten_tool", Description: "x", Scope: "read",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
	})
	if err := catalog.ValidateApplicationSchema(application); err == nil {
		t.Fatal("the Catalog accepted a tool the code does not implement")
	}
	for _, name := range catalog.MCPPublicToolNames() {
		found := false
		for _, tool := range product.Application().MCPPublic.Tools {
			found = found || tool.Name == name
		}
		if !found {
			t.Errorf("the default Catalog does not declare the implemented tool %q", name)
		}
	}
}

// The administrator's overall switch (#332) stops the whole service: every key
// request and every signed download is refused with an explicit JSON-RPC
// error, a valid key is not counted, and turning the switch back on restores
// the service and its usage counter.
func TestMCPListenerRefusesEverythingWhileAccessKeysAreOff(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "read")
	if result := f.call(key, "get_status", nil); result.code != http.StatusOK || result.isError {
		t.Fatalf("baseline = %d %s", result.code, result.raw)
	}
	waitForAccessKeyUsage(t, f, "owner-a", 1)

	// A refused authentication is never counted.
	wrong := key[:len(key)-1] + map[bool]string{true: "b", false: "a"}[key[len(key)-1] == 'a']
	if result := f.call(wrong, "get_status", nil); result.code != http.StatusUnauthorized {
		t.Fatalf("wrong key = %d %s", result.code, result.raw)
	}
	if record := findAccessKey(t, f, "owner-a"); record.RequestCount != 1 {
		t.Fatalf("a refused key was counted: %+v", record)
	}

	if err := f.repository.SetAccessKeySwitch(context.Background(), false, time.Now()); err != nil {
		t.Fatal(err)
	}
	for name, recorder := range map[string]*httptest.ResponseRecorder{
		"endpoint": f.post(key, "tools/call", map[string]any{"name": "get_status", "arguments": map[string]any{}}),
		"download": func() *httptest.ResponseRecorder {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/mcp/tasks/task/assets/asset", nil)
			request.Header.Set("Authorization", "Bearer "+key)
			f.handler.ServeHTTP(recorder, request)
			return recorder
		}(),
		"signed download": func() *httptest.ResponseRecorder {
			recorder := httptest.NewRecorder()
			f.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/mcp/signed-assets/garbage", nil))
			return recorder
		}(),
	} {
		if recorder.Code != http.StatusForbidden ||
			!strings.Contains(recorder.Body.String(), `"code":-32005`) ||
			!strings.Contains(recorder.Body.String(), "整体关闭") {
			t.Errorf("%s while off = %d %s", name, recorder.Code, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), key) {
			t.Errorf("%s echoes the key", name)
		}
	}
	if record := findAccessKey(t, f, "owner-a"); record.RequestCount != 1 {
		t.Fatalf("a refused request was counted: %+v", record)
	}

	if err := f.repository.SetAccessKeySwitch(context.Background(), true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if result := f.call(key, "get_status", nil); result.code != http.StatusOK || result.isError {
		t.Fatalf("after re-enabling = %d %s", result.code, result.raw)
	}
}

// A switch that cannot be read is an internal failure, never an implicit "on"
// (#332): the listener answers 503 with the JSON-RPC internal error and does
// not fall through to the key, the rate limit or the tool.
func TestMCPListenerRefusesWhenTheAccessKeySwitchCannotBeRead(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "read")
	database, err := sql.Open("sqlite", f.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`DROP TABLE access_key_settings`); err != nil {
		t.Fatal(err)
	}
	refusals := map[string]*httptest.ResponseRecorder{
		"endpoint": f.post(key, "tools/call", map[string]any{"name": "get_status", "arguments": map[string]any{}}),
		"signed download": func() *httptest.ResponseRecorder {
			recorder := httptest.NewRecorder()
			f.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/mcp/signed-assets/garbage", nil))
			return recorder
		}(),
	}
	for name, recorder := range refusals {
		if recorder.Code != http.StatusServiceUnavailable ||
			!strings.Contains(recorder.Body.String(), `"code":-32603`) {
			t.Errorf("%s with an unreadable switch = %d %s; want 503 internal error", name, recorder.Code, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), key) {
			t.Errorf("%s echoes the key", name)
		}
	}
	// The refused request never reached the key: no usage was counted.
	if record := findAccessKey(t, f, "owner-a"); record.RequestCount != 0 {
		t.Fatalf("a refused request was counted: %+v", record)
	}
}

func findAccessKey(t *testing.T, f *mcpFixture, owner string) accesskeys.Record {
	t.Helper()
	records, err := f.keys.List(context.Background(), owner)
	if err != nil || len(records) == 0 {
		t.Fatalf("list keys: %v", err)
	}
	return records[0]
}

func waitForAccessKeyUsage(t *testing.T, f *mcpFixture, owner string, want int64) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if record := findAccessKey(t, f, owner); record.RequestCount >= want {
			return
		}
	}
	t.Fatalf("request count of %s never reached %d: %+v", owner, want, findAccessKey(t, f, owner))
}
