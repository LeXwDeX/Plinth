package app

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestProductionSchemaDefaultsToSessionIdentityWithoutBearerTokens(
	t *testing.T,
) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application()
	values := map[string]string{
		"APP_ENV":        "production",
		"SESSION_SECRET": "production-session-secret-32-chars",
	}
	config, err := loadConfigFromSchema(
		schema,
		func(name string) string { return values[name] },
	)
	if err != nil {
		t.Fatal(err)
	}
	if config.AuthMode != "session" {
		t.Fatalf("AuthMode = %q, want session", config.AuthMode)
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("Validate = %v", err)
	}
	fallback, err := buildIdentity(config, nil)
	if err != nil {
		t.Fatalf("buildIdentity = %v", err)
	}
	if fallback != nil {
		t.Fatalf("fallback = %#v, want nil for session-only identity", fallback)
	}
}

func TestRuntimeConfigDefaultsAndEnvironmentSourcesComeFromProductSchema(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application()
	address := schema.Runtime.Configuration.Settings["address"]
	address.Default = "127.0.0.1:14567"
	address.Environment = []catalog.EnvironmentSourceSchema{
		{Name: "CUSTOM_APP_ADDRESS"},
	}
	schema.Runtime.Configuration.Settings["address"] = address
	legacyImages := schema.Runtime.Configuration.
		Settings["legacy_generated_images_root"]
	legacyImages.Environment = []catalog.EnvironmentSourceSchema{
		{Name: "CUSTOM_LEGACY_IMAGES_ROOT"},
	}
	schema.Runtime.Configuration.Settings["legacy_generated_images_root"] = legacyImages
	agentMode := schema.Runtime.Configuration.Settings["agent_mode"]
	agentMode.Environment = []catalog.EnvironmentSourceSchema{
		{Name: "CUSTOM_AGENT_MODE"},
	}
	schema.Runtime.Configuration.Settings["agent_mode"] = agentMode
	llmBaseURL := schema.Runtime.Configuration.Settings["llm_base_url"]
	llmBaseURL.Environment = []catalog.EnvironmentSourceSchema{
		{Name: "CUSTOM_AGENT_BASE_URL"},
	}
	schema.Runtime.Configuration.Settings["configured_agent_base_url"] = llmBaseURL
	schema.Chat.Agent.BaseURLSetting = "configured_agent_base_url"
	llmAPIKey := schema.Runtime.Configuration.Settings["llm_api_key"]
	llmAPIKey.Environment = []catalog.EnvironmentSourceSchema{
		{Name: "CUSTOM_AGENT_API_KEY"},
	}
	schema.Runtime.Configuration.Settings["configured_agent_api_key"] = llmAPIKey
	schema.Chat.Agent.APIKeySetting = "configured_agent_api_key"
	llmModel := schema.Runtime.Configuration.Settings["llm_model"]
	llmModel.Environment = []catalog.EnvironmentSourceSchema{
		{Name: "CUSTOM_AGENT_MODEL"},
	}
	schema.Runtime.Configuration.Settings["configured_agent_model"] = llmModel
	schema.Chat.Agent.ModelSetting = "configured_agent_model"
	providerBaseURL := schema.Runtime.Configuration.Settings["meshy_base_url"]
	providerBaseURL.Environment = []catalog.EnvironmentSourceSchema{
		{Name: "CUSTOM_PROVIDER_ACCOUNT_BASE_URL"},
	}
	schema.Runtime.Configuration.Settings["configured_provider_account_base_url"] = providerBaseURL
	providerAPIKey := schema.Runtime.Configuration.Settings["meshy_api_key"]
	providerAPIKey.Environment = []catalog.EnvironmentSourceSchema{
		{Name: "CUSTOM_PROVIDER_ACCOUNT_API_KEY"},
	}
	schema.Runtime.Configuration.Settings["configured_provider_account_api_key"] = providerAPIKey
	account := schema.Runtime.ProviderAccounts["meshy"]
	account.BaseURLSetting = "configured_provider_account_base_url"
	account.APIKeySetting = "configured_provider_account_api_key"
	schema.Runtime.ProviderAccounts["meshy"] = account

	values := map[string]string{
		"CUSTOM_APP_ADDRESS":               "0.0.0.0:24567",
		"CUSTOM_LEGACY_IMAGES_ROOT":        "/runtime/generated",
		"CUSTOM_AGENT_MODE":                "live",
		"CUSTOM_AGENT_BASE_URL":            "https://agent.example.test/v1",
		"CUSTOM_AGENT_API_KEY":             "configured-agent-key",
		"CUSTOM_AGENT_MODEL":               "configured-agent-model",
		"CUSTOM_PROVIDER_ACCOUNT_BASE_URL": "https://account.example.test",
		"CUSTOM_PROVIDER_ACCOUNT_API_KEY":  "configured-account-key",
		"MESHY_WEBHOOK_PATH_SECRET":        "configured-webhook-path",
		"MESHY_WEBHOOK_KEY":                "configured-webhook-key",
	}
	config, err := loadConfigFromSchema(schema, func(name string) string {
		return values[name]
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.Address != "0.0.0.0:24567" {
		t.Fatalf("Address = %q", config.Address)
	}
	if config.LegacyGeneratedImagesRoot != "/runtime/generated" {
		t.Fatalf(
			"LegacyGeneratedImagesRoot = %q",
			config.LegacyGeneratedImagesRoot,
		)
	}
	if config.AgentMode != "live" ||
		config.RuntimeSettings[schema.Chat.Agent.BaseURLSetting] != "https://agent.example.test/v1" ||
		config.RuntimeSettings[schema.Chat.Agent.APIKeySetting] != "configured-agent-key" ||
		config.RuntimeSettings[schema.Chat.Agent.ModelSetting] != "configured-agent-model" {
		t.Fatalf("agent config = %#v", config.RuntimeSettings)
	}
	if config.RuntimeSettings["configured_provider_account_base_url"] !=
		"https://account.example.test" ||
		config.RuntimeSettings["configured_provider_account_api_key"] !=
			"configured-account-key" {
		t.Fatalf("provider account settings = %#v", config.RuntimeSettings)
	}
	webhook := schema.ProviderWebhooks[0]
	if config.RuntimeSettings[webhook.PathSecretSetting] !=
		"configured-webhook-path" ||
		config.RuntimeSettings[webhook.KeySetting] !=
			"configured-webhook-key" {
		t.Fatalf("provider webhook settings = %#v", config.RuntimeSettings)
	}

	delete(values, "CUSTOM_APP_ADDRESS")
	config, err = loadConfigFromSchema(schema, func(name string) string {
		return values[name]
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.Address != "127.0.0.1:14567" {
		t.Fatalf("fallback Address = %q", config.Address)
	}
}

func TestSkillCatalogFileComesFromProductSchema(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application()
	setting, exists := schema.Runtime.Configuration.Settings["skill_catalog_file"]
	if !exists {
		t.Fatal("missing skill_catalog_file runtime setting")
	}
	setting.Environment = []catalog.EnvironmentSourceSchema{{
		Name: "CONFIGURED_SKILL_CATALOG_FILE",
	}}
	schema.Runtime.Configuration.Settings["skill_catalog_file"] = setting

	config, err := loadConfigFromSchema(schema, func(name string) string {
		if name == "CONFIGURED_SKILL_CATALOG_FILE" {
			return "/runtime/configured-skills.json"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.SkillCatalogFile != "/runtime/configured-skills.json" {
		t.Fatalf("SkillCatalogFile = %q", config.SkillCatalogFile)
	}
}

func TestWebAssetsRootComesFromProductSchema(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application()
	setting, exists := schema.Runtime.Configuration.Settings["web_assets_root"]
	if !exists {
		t.Fatal("missing web_assets_root runtime setting")
	}
	setting.Environment = []catalog.EnvironmentSourceSchema{{
		Name: "CONFIGURED_WEB_ASSETS_ROOT",
	}}
	schema.Runtime.Configuration.Settings["web_assets_root"] = setting

	config, err := loadConfigFromSchema(schema, func(name string) string {
		if name == "CONFIGURED_WEB_ASSETS_ROOT" {
			return "/runtime/go-web"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.WebAssetsRoot != "/runtime/go-web" {
		t.Fatalf("WebAssetsRoot = %q", config.WebAssetsRoot)
	}
}

func TestAuthenticationRuntimeConfigComesFromProductSchema(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application()
	cookieName, exists := schema.Runtime.Configuration.Settings["session_cookie_name"]
	if !exists {
		t.Fatal("missing session_cookie_name runtime setting")
	}
	if cookieName.Type != "string" || cookieName.Default != "" ||
		len(cookieName.Environment) != 1 ||
		cookieName.Environment[0].Name != "SESSION_COOKIE_NAME" || cookieName.Secret {
		t.Fatalf("session_cookie_name setting = %#v", cookieName)
	}
	sessionSecret := schema.Runtime.Configuration.Settings["session_secret"]
	sessionSecret.Environment = []catalog.EnvironmentSourceSchema{
		{Name: "CUSTOM_SESSION_SECRET"},
	}
	schema.Runtime.Configuration.Settings["session_secret"] = sessionSecret
	values := map[string]string{
		"CUSTOM_SESSION_SECRET":  "schema_session_secret_32_chars_x",
		"SESSION_COOKIE_NAME":    "art_ai_stage_session",
		"NEXT_PUBLIC_APP_URL":    "https://candidate.example.test",
		"DEV_MOCK_AUTH":          "true",
		"DEV_MOCK_USER":          `{"id":"mock","name":"Mock","email":"mock@example.test"}`,
		"ADMIN_ALLOWLIST_EMAILS": "one@example.test,two@example.test",
		"ADMIN_USERNAME":         "schema-admin",
		"ADMIN_PASSWORD":         "schema-password",
		"ADMIN_DISPLAY_NAME":     "Schema 管理员",
		"ADMIN_EMAIL":            "admin@example.test",
	}
	config, err := loadConfigFromSchema(schema, func(name string) string {
		return values[name]
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.SessionSecret != values["CUSTOM_SESSION_SECRET"] ||
		config.RuntimeSettings["session_cookie_name"] != "art_ai_stage_session" ||
		config.FrontendPublicAppURL != values["NEXT_PUBLIC_APP_URL"] ||
		!config.MockAuth ||
		config.MockUserJSON != values["DEV_MOCK_USER"] ||
		len(config.AdminAllowlist) != 2 ||
		config.AdminAllowlist[1] != "two@example.test" ||
		config.AdminUsername != "schema-admin" ||
		config.AdminPassword != "schema-password" ||
		config.AdminName != "Schema 管理员" ||
		config.AdminEmail != "admin@example.test" {
		t.Fatalf("authentication config = %#v", config)
	}

	defaultConfig, err := loadConfigFromSchema(schema, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if defaultConfig.RuntimeSettings["session_cookie_name"] != "" {
		t.Fatalf("default session_cookie_name = %q, want empty schema fallback", defaultConfig.RuntimeSettings["session_cookie_name"])
	}
}

func TestConfigRejectsInvalidSessionCookieName(t *testing.T) {
	base := Config{
		Environment:               "test",
		Address:                   "127.0.0.1:0",
		DatabasePath:              ":memory:",
		StorageRoot:               t.TempDir(),
		LegacyGeneratedImagesRoot: t.TempDir(),
		ProviderMode:              "mock",
		AgentMode:                 "mock",
		AuthMode:                  "session",
		SessionSecret:             strings.Repeat("s", 32),
	}
	for _, name := range []string{"art_ai_session", "art-ai.stage~2"} {
		config := base
		config.RuntimeSettings = map[string]string{"session_cookie_name": name}
		if err := config.Validate(); err != nil {
			t.Errorf("valid cookie name %q rejected: %v", name, err)
		}
	}
	for _, name := range []string{"bad name", "bad;name", `bad"name`, "bad\\name", "bad\x7fname", "名_session"} {
		config := base
		config.RuntimeSettings = map[string]string{"session_cookie_name": name}
		if err := config.Validate(); err == nil {
			t.Errorf("invalid cookie name %q accepted", name)
		}
	}
}

// #247: mock login signs anyone in without a password, so production refuses
// to start with it enabled; development keeps it.
func TestConfigRejectsMockLoginInProduction(t *testing.T) {
	base := Config{
		Environment:               "production",
		Address:                   "127.0.0.1:0",
		DatabasePath:              ":memory:",
		StorageRoot:               t.TempDir(),
		LegacyGeneratedImagesRoot: t.TempDir(),
		ProviderMode:              "mock",
		AgentMode:                 "mock",
		AuthMode:                  "session",
		SessionSecret:             strings.Repeat("s", 32),
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("production without mock login rejected: %v", err)
	}
	enabled := base
	enabled.MockAuth = true
	if err := enabled.Validate(); err == nil || !strings.Contains(err.Error(), "DEV_MOCK_AUTH") {
		t.Fatalf("production with mock login = %v, want a DEV_MOCK_AUTH error", err)
	}
	development := enabled
	development.Environment = "development"
	if err := development.Validate(); err != nil {
		t.Fatalf("development mock login rejected: %v", err)
	}
}

func TestLoadConfigAcceptsExistingNextProviderEnvironment(t *testing.T) {
	for _, key := range []string{
		"OPENAI_BASE_URL",
		"OPENAI_API_KEY",
		"GEMINI_BASE_URL",
		"GEMINI_API_KEY",
		"MESHY_BASE_URL",
		"TRIPO_BASE_URL",
		"MATTING_BASE_URL",
		"MATTING_API_KEY",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("APP_ENV", "development")
	t.Setenv("PROVIDER_MODE", "live")
	t.Setenv("AUTH_MODE", "development")
	t.Setenv("LLM_BASE_URL", "http://llms-proxy.test:8000/v1/")
	t.Setenv("LLM_API_KEY", "llm-key")
	t.Setenv("MESHY_API_BASE_URL", "https://meshy.test/")
	t.Setenv("MESHY_API_KEY", "meshy-key")
	t.Setenv("TRIPO_API_BASE_URL", "https://tripo.test/v2/openapi/")
	t.Setenv("TRIPO_API_KEY", "tripo-key")
	t.Setenv("MATTING_API_URL", "http://matting.test:8001/")
	t.Setenv("MATTING_API_TOKEN", "matting-token")

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	providerSettings := map[string]string{
		"openai_base_url":  config.RuntimeSettings["openai_base_url"],
		"openai_api_key":   config.RuntimeSettings["openai_api_key"],
		"gemini_base_url":  config.RuntimeSettings["gemini_base_url"],
		"gemini_api_key":   config.RuntimeSettings["gemini_api_key"],
		"meshy_base_url":   config.RuntimeSettings["meshy_base_url"],
		"meshy_api_key":    config.RuntimeSettings["meshy_api_key"],
		"tripo_base_url":   config.RuntimeSettings["tripo_base_url"],
		"tripo_api_key":    config.RuntimeSettings["tripo_api_key"],
		"matting_base_url": config.RuntimeSettings["matting_base_url"],
		"matting_api_key":  config.RuntimeSettings["matting_api_key"],
	}
	wantProviderSettings := map[string]string{
		// #252: the Agent's LLM_BASE_URL redirects no image Provider and the
		// Agent's key enables none; an empty GEMINI_BASE_URL has no default.
		"openai_base_url":  "https://api.openai.com/v1",
		"openai_api_key":   "",
		"gemini_base_url":  "",
		"gemini_api_key":   "",
		"meshy_base_url":   "https://meshy.test/",
		"meshy_api_key":    "meshy-key",
		"tripo_base_url":   "https://tripo.test/v2/openapi/",
		"tripo_api_key":    "tripo-key",
		"matting_base_url": "http://matting.test:8001/",
		"matting_api_key":  "matting-token",
	}
	for name, want := range wantProviderSettings {
		if providerSettings[name] != want {
			t.Fatalf("provider setting %q = %q, want %q", name, providerSettings[name], want)
		}
	}
}
