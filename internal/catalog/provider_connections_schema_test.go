package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// defaultCatalogDocument 加载默认 Catalog 并返回可变的 JSON 文档副本。
func defaultCatalogDocument(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	return document
}

func loadDocument(t *testing.T, document map[string]any) (*catalog.Catalog, error) {
	t.Helper()
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return catalog.Load(bytes.NewReader(encoded))
}

func applicationOf(t *testing.T, document map[string]any) map[string]any {
	t.Helper()
	application, ok := document["application"].(map[string]any)
	if !ok {
		t.Fatal("fixture missing application")
	}
	return application
}

func runtimeOf(t *testing.T, document map[string]any) map[string]any {
	t.Helper()
	runtime, ok := applicationOf(t, document)["runtime"].(map[string]any)
	if !ok {
		t.Fatal("fixture missing runtime")
	}
	return runtime
}

func settingsOf(t *testing.T, document map[string]any) map[string]any {
	t.Helper()
	configuration, ok := runtimeOf(t, document)["configuration"].(map[string]any)
	if !ok {
		t.Fatal("fixture missing configuration")
	}
	settings, ok := configuration["settings"].(map[string]any)
	if !ok {
		t.Fatal("fixture missing settings")
	}
	return settings
}

func connectionsOf(t *testing.T, document map[string]any) []any {
	t.Helper()
	connections, ok := runtimeOf(t, document)["provider_connections"].([]any)
	if !ok {
		t.Fatal("fixture missing provider_connections")
	}
	return connections
}

func connectionMap(t *testing.T, document map[string]any, id string) map[string]any {
	t.Helper()
	for _, item := range connectionsOf(t, document) {
		connection := item.(map[string]any)
		if connection["id"] == id {
			return connection
		}
	}
	t.Fatalf("connection %q missing", id)
	return nil
}

func cloneValue(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDefaultCatalogDeclaresProviderConnections(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	connections := taskCatalog.Application().Runtime.ProviderConnections
	if len(connections) < 7 {
		t.Fatalf("provider_connections = %#v", connections)
	}
	protocols := map[string]string{}
	for _, connection := range connections {
		if _, known := catalog.ProviderProtocolEnum[connection.Protocol]; !known {
			t.Errorf("connection %q protocol %q not in enum", connection.ID, connection.Protocol)
		}
		if previous, exists := protocols[connection.AdapterKey]; exists {
			t.Errorf("adapter_key %q declared by %q and %q", connection.AdapterKey, previous, connection.ID)
		}
		protocols[connection.AdapterKey] = connection.ID
	}
	for _, adapterKey := range []string{
		"openai-images.v1",
		"gemini-images.v1",
		"jimeng-images.v1",
		"meshy.v2",
		"tripo.v3",
		"matting.v1",
	} {
		if _, exists := protocols[adapterKey]; !exists {
			t.Errorf("adapter_key %q lacks a connection", adapterKey)
		}
	}
	meshy := connectionByID(t, connections, "meshy")
	if len(meshy.ConditionalCapabilities) != 1 ||
		len(meshy.ConditionalCapabilities[0].Capabilities) != 5 ||
		len(meshy.ConditionalCapabilities[0].AllOfSettings) != 2 {
		t.Errorf("meshy conditional capabilities = %#v", meshy.ConditionalCapabilities)
	}
	if meshy.StatusMapping == nil || meshy.ErrorMapping == nil {
		t.Errorf("meshy connection lacks status/error mapping: %#v", meshy)
	}
	tripo := connectionByID(t, connections, "tripo")
	if tripo.ErrorMapping == nil || len(tripo.ErrorMapping.UpstreamCodeMap) < 20 {
		t.Errorf("tripo error code table = %#v", tripo.ErrorMapping)
	}
}

func connectionByID(
	t *testing.T,
	connections []catalog.ProviderConnectionSchema,
	id string,
) catalog.ProviderConnectionSchema {
	t.Helper()
	for _, connection := range connections {
		if connection.ID == id {
			return connection
		}
	}
	t.Fatalf("connection %q missing", id)
	return catalog.ProviderConnectionSchema{}
}

func TestProviderConnectionsRejectUnknownProtocol(t *testing.T) {
	document := defaultCatalogDocument(t)
	connection := cloneValue(t, connectionsOf(t, document)[0])
	connection["id"] = "acme"
	connection["protocol"] = "carrier-pigeon"
	runtimeOf(t, document)["provider_connections"] = append(
		connectionsOf(t, document),
		connection,
	)
	if _, err := loadDocument(t, document); err == nil ||
		!strings.Contains(err.Error(), "未知协议") {
		t.Fatalf("err = %v", err)
	}
}

func TestProviderConnectionsRejectDuplicateAdapterKey(t *testing.T) {
	document := defaultCatalogDocument(t)
	connection := cloneValue(t, connectionsOf(t, document)[0])
	connection["id"] = "acme-duplicate"
	runtimeOf(t, document)["provider_connections"] = append(
		connectionsOf(t, document),
		connection,
	)
	if _, err := loadDocument(t, document); err == nil ||
		!strings.Contains(err.Error(), "重复") {
		t.Fatalf("err = %v", err)
	}
}

func TestProviderConnectionsRejectUnknownSettingReference(t *testing.T) {
	document := defaultCatalogDocument(t)
	connection := cloneValue(t, connectionsOf(t, document)[0])
	connection["id"] = "acme"
	connection["adapter_key"] = "acme-images.v1"
	connection["api_key_setting"] = "acme_secret_setting_missing"
	runtimeOf(t, document)["provider_connections"] = append(
		connectionsOf(t, document),
		connection,
	)
	if _, err := loadDocument(t, document); err == nil ||
		!strings.Contains(err.Error(), "未知运行设置") {
		t.Fatalf("err = %v", err)
	}
}

func TestProviderConnectionsRejectInvalidErrorClass(t *testing.T) {
	document := defaultCatalogDocument(t)
	connection := cloneValue(t, connectionMap(t, document, "tripo"))
	connection["error_mapping"] = map[string]any{
		"upstream_code_map": map[string]any{
			"E1": map[string]any{"class": "not-a-class"},
		},
	}
	replaceConnection(t, document, connection)
	if _, err := loadDocument(t, document); err == nil ||
		!strings.Contains(err.Error(), "upstream_code_map") {
		t.Fatalf("err = %v", err)
	}
}

func TestProviderConnectionsRejectInvalidStatusPhase(t *testing.T) {
	document := defaultCatalogDocument(t)
	connection := cloneValue(t, connectionMap(t, document, "tripo"))
	connection["status_mapping"] = map[string]any{
		"upstream_status": map[string]any{
			"weird": map[string]any{"phase": "teleporting"},
		},
	}
	replaceConnection(t, document, connection)
	if _, err := loadDocument(t, document); err == nil ||
		!strings.Contains(err.Error(), "未知相位") {
		t.Fatalf("err = %v", err)
	}
}

func TestProviderConnectionsRejectConditionalOverlap(t *testing.T) {
	document := defaultCatalogDocument(t)
	connection := cloneValue(t, connectionMap(t, document, "matting"))
	connection["conditional_capabilities"] = []any{map[string]any{
		"capabilities":    []any{"background-removal"},
		"all_of_settings": []any{"public_base_url"},
	}}
	replaceConnection(t, document, connection)
	if _, err := loadDocument(t, document); err == nil ||
		!strings.Contains(err.Error(), "同时出现在无条件与条件声明") {
		t.Fatalf("err = %v", err)
	}
}

func TestProviderConnectionsRejectConditionalUnknownSetting(t *testing.T) {
	document := defaultCatalogDocument(t)
	connection := cloneValue(t, connectionMap(t, document, "matting"))
	connection["conditional_capabilities"] = []any{map[string]any{
		"capabilities":    []any{"background-removal-duplicate"},
		"all_of_settings": []any{"setting_that_does_not_exist"},
	}}
	replaceConnection(t, document, connection)
	if _, err := loadDocument(t, document); err == nil ||
		!strings.Contains(err.Error(), "未知运行设置") {
		t.Fatalf("err = %v", err)
	}
}

func replaceConnection(
	t *testing.T,
	document map[string]any,
	connection map[string]any,
) {
	t.Helper()
	runtime := runtimeOf(t, document)
	connections := connectionsOf(t, document)
	for index, item := range connections {
		if item.(map[string]any)["id"] == connection["id"] {
			connections[index] = connection
			runtime["provider_connections"] = connections
			return
		}
	}
	runtime["provider_connections"] = append(connections, connection)
}

// TestPublicProjectionExcludesProviderConnections 是 §8.3 风格的负面哨兵：
// 连接声明、secret 设置值、设置名与映射词表不得进入公开 Product Schema 投影。
func TestPublicProjectionExcludesProviderConnections(t *testing.T) {
	const (
		settingNameSentinel  = "acme_secret_api_key"
		settingValueSentinel = "SENTINEL_SECRET_acbe4f71"
		mappingCodeSentinel  = "SENTINEL_CODE_E4242"
		accountSentinel      = "SENTINEL_ACCOUNT_REF"
	)
	document := defaultCatalogDocument(t)
	settings := settingsOf(t, document)
	settings[settingNameSentinel] = map[string]any{
		"type":        "string",
		"environment": []any{map[string]any{"name": "ACME_SECRET_API_KEY"}},
		"default":     "",
		"secret":      true,
	}
	connection := cloneValue(t, connectionsOf(t, document)[0])
	connection["id"] = "acme-sentinel"
	connection["adapter_key"] = "acme-sentinel.v1"
	connection["api_key_setting"] = settingNameSentinel
	connection["error_mapping"] = map[string]any{
		"upstream_code_map": map[string]any{
			mappingCodeSentinel: map[string]any{"class": "rate_limited"},
		},
	}
	connection["base_url_setting"] = "openai_base_url"
	runtimeOf(t, document)["provider_connections"] = append(
		connectionsOf(t, document),
		connection,
	)
	// 连接自身的 secret 值通过运行设置注入（Public() 不含 RuntimeSettings），
	// 这里以设置名哨兵证明连接引用不出现在公开面。
	taskCatalog, err := loadDocument(t, document)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(taskCatalog.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	public := string(encoded)
	for _, forbidden := range []string{
		"provider_connections",
		settingNameSentinel,
		settingValueSentinel,
		mappingCodeSentinel,
	} {
		if strings.Contains(public, forbidden) {
			t.Errorf("public projection leaks %q", forbidden)
		}
	}
	if strings.Contains(public, `"provider_accounts"`) {
		t.Error("public projection leaks provider account wiring")
	}
	if strings.Contains(public, "acme-sentinel") {
		t.Error("public projection leaks connection id")
	}
	// offerings 端点是显式白名单结构：序列化不得包含 provider_options 或连接词表。
	offering, exists := taskCatalog.Get("tripo/tripo-v3.1/text-to-3d")
	if !exists {
		t.Fatal("tripo offering missing")
	}
	offeringEncoded, err := json.Marshal(offering)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(offeringEncoded), mappingCodeSentinel) ||
		strings.Contains(string(offeringEncoded), settingValueSentinel) {
		t.Error("offering serialization leaks connection sentinels")
	}
}

// TestNormalizeAppliesDeclaredForcedWhen：forced_when 由 Normalize 服务端
// 最终裁决（默认值先就位、再覆盖、后校验）。
func TestNormalizeAppliesDeclaredForcedWhen(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offeringID := generation.OfferingID("tripo/tripo-v3.1-full/text-to-3d")
	offering, normalized, err := taskCatalog.Normalize(
		generation.TextTo3D,
		offeringID,
		json.RawMessage(`{"prompt":"cat","authorize_extra_credits":true}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = offering
	var values map[string]any
	if err := json.Unmarshal(normalized, &values); err != nil {
		t.Fatal(err)
	}
	if _, forced := values["texture"]; !forced {
		t.Fatal("forced parameters absent from normalized input")
	}
	// 默认 generate_parts=false：不触发强制，texture 保持默认 true。
	if values["texture"] != true {
		t.Errorf("texture = %v, want default true", values["texture"])
	}
}

// #252: a base URL alias that is another connection's or the Agent's
// canonical variable sends this connection's API key to that vendor, so the
// Catalog rejects it with no exception: the transitional annotation that once
// let GEMINI_BASE_URL fall back to the Agent's LLM_BASE_URL is gone, and an
// alias that still carries it is an unknown field. Each connection reads only
// its own address variable.
func TestProviderBaseURLAliasMayNotBorrowAnotherCanonicalVariable(t *testing.T) {
	for _, borrowed := range []string{"LLM_BASE_URL", "MESHY_BASE_URL"} {
		document := defaultCatalogDocument(t)
		settingsOf(t, document)["openai_base_url"].(map[string]any)["environment"] = []any{
			map[string]any{"name": "OPENAI_BASE_URL"},
			map[string]any{"name": borrowed, "deprecated": true},
		}
		if _, err := loadDocument(t, document); err == nil ||
			!strings.Contains(err.Error(), borrowed) ||
			!strings.Contains(err.Error(), strings.ToLower(borrowed)) {
			t.Fatalf("borrowed alias %s err = %v", borrowed, err)
		}
	}

	document := defaultCatalogDocument(t)
	settingsOf(t, document)["gemini_base_url"].(map[string]any)["environment"] = []any{
		map[string]any{"name": "GEMINI_BASE_URL"},
		map[string]any{"name": "LLM_BASE_URL", "deprecated": true, "transitional": "dev-test still relies on it"},
	}
	if _, err := loadDocument(t, document); err == nil || !strings.Contains(err.Error(), "transitional") {
		t.Fatalf("transitional alias err = %v; the annotation must no longer be accepted", err)
	}

	// The default Catalog carries no transitional annotation and no address
	// alias that is another setting's canonical variable.
	for name, raw := range settingsOf(t, defaultCatalogDocument(t)) {
		environment, _ := raw.(map[string]any)["environment"].([]any)
		for _, source := range environment {
			if _, marked := source.(map[string]any)["transitional"]; marked {
				t.Errorf("setting %q still has a transitional alias: %v", name, source)
			}
		}
	}
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	settings := taskCatalog.Application().Runtime.Configuration.Settings
	canonicalOwners := map[string]string{}
	for name, setting := range settings {
		if len(setting.Environment) > 0 {
			canonicalOwners[setting.Environment[0].Name] = name
		}
	}
	for name, setting := range settings {
		if len(setting.Environment) == 0 {
			continue
		}
		for _, source := range setting.Environment[1:] {
			if owner, borrowed := canonicalOwners[source.Name]; borrowed && owner != name {
				t.Errorf("setting %q reads %s, the canonical variable of %q", name, source.Name, owner)
			}
		}
	}
	gemini := settings["gemini_base_url"]
	if len(gemini.Environment) != 1 || gemini.Environment[0].Name != "GEMINI_BASE_URL" {
		t.Fatalf("GEMINI_BASE_URL must read only itself, environment = %#v", gemini.Environment)
	}
	// Gemini images speak a gateway's chat-completions image contract, not
	// Google's native API, so there is no public address to default to; the
	// former 127.0.0.1:8000 default only ever meant a gateway on the host
	// (#251). Empty leaves the connection unconfigured.
	if gemini.Default != "" || len(gemini.DefaultByEnvironment) != 0 {
		t.Fatalf("GEMINI_BASE_URL default = %q %v; want none", gemini.Default, gemini.DefaultByEnvironment)
	}
}
