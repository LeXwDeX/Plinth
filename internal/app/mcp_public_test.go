package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

type safeBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *safeBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// mcpTestApplication assembles the application with the external MCP listener
// at address ("" for none). rewrite changes the default Catalog document before
// it is loaded.
func mcpTestApplication(t *testing.T, address string, rewrite func(string) string) (*Application, *safeBuffer) {
	t.Helper()
	root := t.TempDir()
	config := Config{
		RuntimeSettings: map[string]string{"llm_model": "test-model"},
		Environment:     "development", Address: "127.0.0.1:0", MCPPublicAddress: address,
		DatabasePath: filepath.Join(root, "art-ai.db"), StorageRoot: filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		ProviderMode:              "mock", AgentMode: "mock", AuthMode: "session",
		SessionSecret: strings.Repeat("s", 32),
		AdminUsername: "schema-admin", AdminPassword: "correct-horse", AdminName: "Schema 管理员", AdminEmail: "admin@example.test",
	}
	if rewrite != nil {
		document, err := os.ReadFile(filepath.Join("..", "catalog", "default_catalog.json"))
		if err != nil {
			t.Fatal(err)
		}
		config.CatalogFile = filepath.Join(root, "catalog.json")
		if err := os.WriteFile(config.CatalogFile, []byte(rewrite(string(document))), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	logs := &safeBuffer{}
	application, err := New(config, slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.generation.Close() })
	return application, logs
}

// The listener needs both the Product Schema's enabled flag and an address in
// the runtime configuration; with either missing nothing listens.
func TestMCPListenerNeedsTheSchemaAndAnAddress(t *testing.T) {
	t.Parallel()
	noAddress, logs := mcpTestApplication(t, "", nil)
	if noAddress.mcpServer != nil || noAddress.MCPAddress() != "" {
		t.Fatal("a listener exists without an address")
	}
	if listener, err := noAddress.bindMCP(); listener != nil || err != nil {
		t.Fatalf("bindMCP without a server = %v %v", listener, err)
	}
	if !strings.Contains(logs.String(), "不启动对外 MCP 监听") {
		t.Fatalf("the startup does not say why nothing listens:\n%s", logs.String())
	}

	enabled, _ := mcpTestApplication(t, "127.0.0.1:0", nil)
	server := enabled.mcpServer
	if server == nil || server.Addr != "127.0.0.1:0" || server.Handler == nil {
		t.Fatalf("server = %+v", server)
	}
	if server.ReadHeaderTimeout != 10*time.Second || server.ReadTimeout != 30*time.Second ||
		server.WriteTimeout != 1800*time.Second || server.IdleTimeout != 120*time.Second {
		t.Fatalf("timeouts = %v %v %v %v; they come from the Schema", server.ReadHeaderTimeout, server.ReadTimeout, server.WriteTimeout, server.IdleTimeout)
	}
	if enabled.mcpServer == enabled.server || enabled.server.Addr == server.Addr && enabled.server.Addr != "127.0.0.1:0" {
		t.Fatal("the external listener shares the main server")
	}

	disabled, logs := mcpTestApplication(t, "127.0.0.1:0", func(document string) string {
		return strings.Replace(document, "\"mcp_public\": {\n      \"enabled\": true", "\"mcp_public\": {\n      \"enabled\": false", 1)
	})
	if disabled.mcpServer != nil || !strings.Contains(logs.String(), "没有启用 application.mcp_public") {
		t.Fatalf("a Schema that disables the service still has a listener:\n%s", logs.String())
	}
	withoutSection, _ := mcpTestApplication(t, "127.0.0.1:0", func(document string) string {
		start := strings.Index(document, "    \"mcp_public\": {")
		end := strings.Index(document, "    \"chat\": {\n      \"attachment_purposes\"")
		document = document[:start] + document[end:]
		// The English overlay of the removed section is not an error for a Catalog without it.
		return document
	})
	if withoutSection.mcpServer != nil {
		t.Fatal("a Catalog without the section has a listener")
	}
}

func TestMCPListenAddressIsValidated(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		address string
		valid   bool
	}{
		"empty is no listener": {"", true},
		"loopback":             {"127.0.0.1:13334", true},
		"all interfaces":       {":13334", true},
		"ephemeral":            {"127.0.0.1:0", true},
		"no port":              {"127.0.0.1", false},
		"port out of range":    {"127.0.0.1:70000", false},
		"port not a number":    {"127.0.0.1:http", false},
		"the main address":     {"127.0.0.1:13333", false},
		"a url":                {"http://127.0.0.1:13334", false},
	} {
		if err := validMCPListenAddress(test.address, "127.0.0.1:13333"); (err == nil) != test.valid {
			t.Errorf("%s (%q): err = %v", name, test.address, err)
		}
	}
	config := Config{Address: "127.0.0.1:13333", DatabasePath: "x", StorageRoot: "x", LegacyGeneratedImagesRoot: "x",
		ProviderMode: "mock", AgentMode: "mock", AuthMode: "development", MCPPublicAddress: "127.0.0.1:13333"}
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "mcp_public_listen_addr") {
		t.Fatalf("Validate = %v", err)
	}
}

func TestMCPListenAddressComesFromTheRuntimeSetting(t *testing.T) {
	t.Parallel()
	catalogue, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	product := catalogue.Application()
	setting, declared := product.Runtime.Configuration.Settings["mcp_public_listen_addr"]
	if !declared || setting.Default != "" || len(setting.Environment) != 1 || setting.Environment[0].Name != "MCP_PUBLIC_LISTEN_ADDR" {
		t.Fatalf("setting = %+v declared=%v; it must be declared, unset by default", setting, declared)
	}
	config, err := loadConfigFromSchema(product, func(name string) string {
		return map[string]string{"MCP_PUBLIC_LISTEN_ADDR": " 127.0.0.1:13400 ", "APP_ENV": "development"}[name]
	})
	if err != nil || config.MCPPublicAddress != "127.0.0.1:13400" {
		t.Fatalf("MCPPublicAddress = %q err=%v", config.MCPPublicAddress, err)
	}
	if config, err = loadConfigFromSchema(product, func(name string) string { return map[string]string{"APP_ENV": "development"}[name] }); err != nil || config.MCPPublicAddress != "" {
		t.Fatalf("unset MCPPublicAddress = %q err=%v", config.MCPPublicAddress, err)
	}
}

// End to end: a person creates a key in the web API, the key works on the
// second listener and on nothing else, the listener serves no page and no web
// API, and shutting the process down closes both.
func TestMCPListenerRunsBesideTheMainServer(t *testing.T) {
	application, _ := mcpTestApplication(t, "127.0.0.1:0", nil)
	main := application.server.Handler

	login := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"schema-admin","password":"correct-horse"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	main.ServeHTTP(login, loginRequest)
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("login = %d %s", login.Code, login.Body.String())
	}
	create := httptest.NewRecorder()
	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/account/access-keys", strings.NewReader(`{"name":"end to end","scopes":["read"]}`))
	createRequest.Header.Set("Content-Type", "application/json")
	createRequest.Header.Set("Origin", "http://example.com")
	createRequest.AddCookie(login.Result().Cookies()[0])
	main.ServeHTTP(create, createRequest)
	var created struct {
		Key string `json:"key"`
	}
	if create.Code != http.StatusCreated || json.Unmarshal(create.Body.Bytes(), &created) != nil || !strings.HasPrefix(created.Key, "plk_") {
		t.Fatalf("create key = %d %s", create.Code, create.Body.String())
	}
	// The key is no credential of the main server.
	withKey := httptest.NewRecorder()
	keyRequest := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	keyRequest.Header.Set("Authorization", "Bearer "+created.Key)
	main.ServeHTTP(withKey, keyRequest)
	if withKey.Code != http.StatusUnauthorized {
		t.Fatalf("the main server answered a key with %d", withKey.Code)
	}
	// And the main server does not serve the external MCP endpoint.
	mcpOnMain := httptest.NewRecorder()
	main.ServeHTTP(mcpOnMain, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}")))
	if mcpOnMain.Code == http.StatusOK || strings.Contains(mcpOnMain.Body.String(), `"jsonrpc"`) {
		t.Fatalf("the main server answers /mcp: %d %s", mcpOnMain.Code, mcpOnMain.Body.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- application.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run returned %v after the shutdown", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("Run did not return after the shutdown")
		}
	}()
	var address string
	for deadline := time.Now().Add(10 * time.Second); address == "" && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		address = application.MCPAddress()
	}
	if address == "" || strings.HasSuffix(address, ":0") {
		t.Fatalf("the listener did not bind: %q", address)
	}
	base := "http://" + address
	client := &http.Client{Timeout: 10 * time.Second}
	get := func(path string) (int, string) {
		response, err := client.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(body)
	}
	for _, path := range []string{"/", "/api/v1/schema", "/api/v1/tasks", "/healthz", "/api/auth/login"} {
		if status, body := get(path); status != http.StatusNotFound || !strings.Contains(body, `"jsonrpc"`) {
			t.Errorf("GET %s on the external listener = %d %s; want a JSON 404", path, status, body)
		}
	}
	call := func(key, method string, params map[string]any) (int, map[string]any) {
		params["_meta"] = map[string]any{
			"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
			"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "e2e", "version": "1"},
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		request, _ := http.NewRequest(http.MethodPost, base+"/mcp", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("MCP-Protocol-Version", "2026-07-28")
		request.Header.Set("Mcp-Method", method)
		if name, ok := params["name"].(string); ok {
			request.Header.Set("Mcp-Name", name)
		}
		if key != "" {
			request.Header.Set("Authorization", "Bearer "+key)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var decoded map[string]any
		_ = json.NewDecoder(response.Body).Decode(&decoded)
		return response.StatusCode, decoded
	}
	if status, _ := call("", "tools/list", map[string]any{}); status != http.StatusUnauthorized {
		t.Fatalf("no key = %d", status)
	}
	status, decoded := call(created.Key, "tools/call", map[string]any{"name": "get_status", "arguments": map[string]any{}})
	result, _ := decoded["result"].(map[string]any)
	block, _ := result["structuredContent"].(map[string]any)["status"].(map[string]any)
	if status != http.StatusOK || result["isError"] != false || block == nil {
		t.Fatalf("get_status = %d %v", status, decoded)
	}
	balances, _ := block["balances"].([]any)
	if block["step_mode"] != "agent" || len(balances) != 3 || balances[0].(map[string]any)["state"] != "ok" || balances[0].(map[string]any)["amount"] == nil {
		t.Fatalf("status block = %v; the mock providers' balances are read through the shared cache", block)
	}
}
