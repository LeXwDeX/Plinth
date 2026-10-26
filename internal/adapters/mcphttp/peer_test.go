package mcphttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/mcp"
)

func transportPolicy() Policy { return Policy{MaxResponseBytes: 4096, MaxTools: 4} }

type readRuntime struct{ calls int }

func (*readRuntime) ListTools(context.Context) ([]mcp.Tool, error) {
	return []mcp.Tool{{Name: "read", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`)}}, nil
}
func (r *readRuntime) CallTool(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	r.calls++
	return json.RawMessage(`{"resultType":"complete","content":[{"type":"text","text":"public"}]}`), nil
}

func TestLocalJSONRPCRoundTripAndHTTPHeaderMetadataAgreement(t *testing.T) {
	runtime := &readRuntime{}
	peer, err := NewLocal(runtime, transportPolicy())
	if err != nil {
		t.Fatal(err)
	}
	tools, err := peer.ListTools(context.Background())
	if err != nil || len(tools) != 1 || tools[0].Name != "read" {
		t.Fatalf("list: %#v %v", tools, err)
	}
	if _, err := peer.CallTool(context.Background(), "read", json.RawMessage(`{}`)); err != nil || runtime.calls != 1 {
		t.Fatal(err)
	}
	if _, err := peer.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		id                                                             any
		version, headerVersion, method, headerMethod, name, headerName string
		code, status                                                   int
	}{
		{"string-id", ProtocolVersion, ProtocolVersion, "tools/list", "tools/list", "", "", 0, 200},
		{0, ProtocolVersion, ProtocolVersion, "tools/list", "tools/call", "", "", -32020, 400},
		{1, "2025-11-25", "2025-11-25", "tools/list", "tools/list", "", "", -32022, 400},
		{1, ProtocolVersion, "2025-11-25", "tools/list", "tools/list", "", "", -32020, 400},
		{1, ProtocolVersion, ProtocolVersion, "tools/call", "tools/call", "read", "other", -32020, 400},
		{nil, ProtocolVersion, ProtocolVersion, "tools/list", "tools/list", "", "", -32600, 400},
	} {
		params := map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/protocolVersion": test.version, "io.modelcontextprotocol/clientInfo": map[string]string{"name": "test", "version": "1"}, "io.modelcontextprotocol/clientCapabilities": map[string]any{}}}
		if test.name != "" {
			params["name"] = test.name
			params["arguments"] = map[string]any{}
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": test.id, "method": test.method, "params": params})
		request := httptest.NewRequest("POST", "http://local/mcp", strings.NewReader(string(body)))
		request.Header.Set("MCP-Protocol-Version", test.headerVersion)
		request.Header.Set("Mcp-Method", test.headerMethod)
		request.Header.Set("Mcp-Name", test.headerName)
		response := httptest.NewRecorder()
		NewHandler(runtime, transportPolicy()).ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("status %d: %s", response.Code, response.Body.String())
		}
		if test.code != 0 && !strings.Contains(response.Body.String(), fmt.Sprintf(`"code":%d`, test.code)) {
			t.Fatal(response.Body.String())
		}
	}
	if runtime.calls != 1 {
		t.Fatal("invalid envelopes reached runtime")
	}
}

func TestHTTPOriginRedirectLimitsAndAuthenticationEchoFailClosed(t *testing.T) {
	redirectTargets := 0
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirectTargets++ }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer source.Close()
	if _, err := NewHTTP(source.URL, ProtocolVersion, "", []string{target.URL}, transportPolicy()); err == nil {
		t.Fatal("unapproved origin accepted")
	}
	peer, err := NewHTTP(source.URL, ProtocolVersion, "", []string{source.URL}, transportPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peer.ListTools(context.Background()); err == nil || redirectTargets != 0 {
		t.Fatal("followed redirect")
	}
	for _, mode := range []string{"echo", "oversize", "wrong-id", "loop"} {
		t.Run(mode, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var message rpcRequest
				_ = json.NewDecoder(r.Body).Decode(&message)
				if r.Header.Get("Authorization") != "Bearer private-test-value" || r.Header.Get("Mcp-Method") != "tools/list" || r.Header.Get("Cookie") != "" {
					t.Error("authentication contract")
				}
				w.Header().Set("Content-Type", "application/json")
				switch mode {
				case "echo":
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"complete","tools":[],"secret":"private-test-value"}}`, message.ID)
				case "oversize":
					fmt.Fprint(w, strings.Repeat("x", 5000))
				case "wrong-id":
					fmt.Fprint(w, `{"jsonrpc":"2.0","id":0,"result":{}}`)
				case "loop":
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"complete","tools":[],"nextCursor":"repeat"}}`, message.ID)
				}
			}))
			defer server.Close()
			peer, err := NewHTTP(server.URL, ProtocolVersion, "private-test-value", []string{server.URL}, transportPolicy())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := peer.ListTools(context.Background()); err == nil {
				t.Fatal("invalid response accepted")
			}
			if requests > 2 {
				t.Fatal("unbounded pagination")
			}
		})
	}
}

func TestSSEHandlesNotificationsAndRejectsMismatchedOrDuplicateResults(t *testing.T) {
	complete := `{"jsonrpc":"2.0","id":3,"result":{"resultType":"complete","content":[{"type":"text","text":"ok"}]}}`
	valid := "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{}}\n\ndata: " + complete + "\n\n"
	if _, err := parseSSE([]byte(valid), 3); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{valid + "data: " + complete + "\n\n", strings.Replace(valid, `"id":3`, `"id":4`, 1), "data: invalid\n\n"} {
		if _, err := parseSSE([]byte(data), 3); err == nil {
			t.Fatal("invalid SSE accepted")
		}
	}
}
