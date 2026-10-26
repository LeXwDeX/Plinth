package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/logging"
)

type Config struct {
	RuntimeSettings map[string]string
	// DeprecatedEnvironment lists "ALIAS -> CANONICAL" for every setting whose
	// value came from a deprecated environment alias (#252), for a startup
	// warning. Values are never included.
	DeprecatedEnvironment []string
	// RetiredEnvironment lists configured variables that are no longer read.
	// It contains names only, for one startup warning; values are discarded.
	RetiredEnvironment        []string
	Environment               string
	Address                   string
	DatabasePath              string
	StorageRoot               string
	WebAssetsRoot             string
	LegacyGeneratedImagesRoot string
	CatalogFile               string
	SkillCatalogFile          string
	PublicBaseURL             string
	AssetSigningKey           string
	ProviderMode              string
	AgentMode                 string
	AuthMode                  string
	DevelopmentUser           generation.OwnerID
	DevelopmentName           string
	DevelopmentEmail          string
	DevelopmentRoles          []string
	AuthTokens                map[string]access.Claims
	SessionSecret             string
	FrontendPublicAppURL      string
	MockAuth                  bool
	MockUserJSON              string
	AdminAllowlist            []string
	AdminUsername             string
	AdminPassword             string
	AdminName                 string
	AdminEmail                string
	AdminPasswordReset        bool
	// TrustedProxies are the reverse proxies whose X-Forwarded-For names the
	// real client (#256); empty means the TCP peer is the client.
	TrustedProxies []netip.Prefix
	Logging        LoggingConfig
	// AgentMaxToolCallsPerReply overrides the Schema's per-reply tool call
	// budget (chat.agent.max_tool_calls_per_reply, #275); 0 keeps the Schema
	// value.
	AgentMaxToolCallsPerReply int
	// ContextWindowTokens overrides the context window of the configured
	// chat model (chat.agent.context_window_setting, #322); 0 keeps the
	// model profile's value.
	ContextWindowTokens int
	// ExtraChatConnections are the chat model connections LLM_CONNECTIONS
	// lists beyond the deployment's own, each read from its own <ID>_*
	// variables (#322 Scope 2).
	ExtraChatConnections []ChatConnectionConfig
}

// LoggingConfig is the server-private logging wiring (#233). Level defaults to
// debug; Directory empty means stdout only.
type LoggingConfig struct {
	Level         slog.Level
	Directory     string
	MaxBytes      int64
	MaxFiles      int
	RetentionDays int
}

func LoadConfig() (Config, error) {
	bootstrapCatalog, err := catalog.LoadDefault()
	if err != nil {
		return Config{}, err
	}
	bootstrapSchema := bootstrapCatalog.Application()
	environment, err := runtimeSettingValue(
		bootstrapSchema,
		"application_environment",
		"",
		os.Getenv,
	)
	if err != nil {
		return Config{}, err
	}
	catalogFile, err := runtimeSettingValue(
		bootstrapSchema,
		"catalog_file",
		environment,
		os.Getenv,
	)
	if err != nil {
		return Config{}, err
	}
	taskCatalog := bootstrapCatalog
	if catalogFile != "" {
		taskCatalog, err = loadCatalog(catalogFile)
		if err != nil {
			return Config{}, err
		}
	}
	config, err := loadConfigFromSchema(taskCatalog.Application(), os.Getenv)
	if err != nil {
		return Config{}, err
	}
	config.CatalogFile = catalogFile
	return config, nil
}

// runtimeWiringSettingNames 是 Go 运行时按名字直接消费的设置名单：
// 核心接线字面量 + Schema 声明的 Chat Agent 设置引用。名单与默认
// Catalog 的声明一致性由 config 测试守卫；Provider 连接设置不在此列，
// 由 provider_connections 的 base_url_setting/api_key_setting 引用解析。
func runtimeWiringSettingNames(schema catalog.ApplicationSchema) []string {
	return []string{
		"address",
		"database_path",
		"storage_root",
		"web_assets_root",
		"legacy_generated_images_root",
		"catalog_file",
		"skill_catalog_file",
		"public_base_url",
		"asset_signing_key",
		"provider_mode",
		"agent_mode",
		"auth_mode",
		"development_user_id",
		"development_user_name",
		"development_user_email",
		"development_user_roles",
		"auth_tokens_json",
		"session_secret",
		"session_cookie_name",
		"frontend_public_app_url",
		"dev_mock_auth",
		"dev_mock_user_json",
		"admin_allowlist_emails",
		"admin_username",
		"admin_password",
		"admin_display_name",
		"admin_email",
		schema.Chat.Agent.BaseURLSetting,
		schema.Chat.Agent.APIKeySetting,
		schema.Chat.Agent.ModelSetting,
	}
}

func loadConfigFromSchema(
	schema catalog.ApplicationSchema,
	lookup func(string) string,
) (Config, error) {
	environment, err := runtimeSettingValue(
		schema,
		"application_environment",
		"",
		lookup,
	)
	if err != nil {
		return Config{}, err
	}
	value := func(name string) (string, error) {
		return runtimeSettingValue(schema, name, environment, lookup)
	}
	values := map[string]string{}
	settingNames := runtimeWiringSettingNames(schema)
	for name := range schema.Runtime.Configuration.Settings {
		settingNames = append(settingNames, name)
	}
	seenSettings := map[string]struct{}{}
	for _, name := range settingNames {
		if _, exists := seenSettings[name]; exists {
			continue
		}
		seenSettings[name] = struct{}{}
		values[name], err = value(name)
		if err != nil {
			return Config{}, err
		}
	}
	config := Config{
		DeprecatedEnvironment:     deprecatedEnvironmentInUse(schema, lookup),
		RetiredEnvironment:        retiredEnvironmentInUse(schema, lookup),
		RuntimeSettings:           values,
		Environment:               environment,
		Address:                   values["address"],
		DatabasePath:              values["database_path"],
		StorageRoot:               values["storage_root"],
		WebAssetsRoot:             values["web_assets_root"],
		LegacyGeneratedImagesRoot: values["legacy_generated_images_root"],
		CatalogFile:               values["catalog_file"],
		SkillCatalogFile:          values["skill_catalog_file"],
		PublicBaseURL:             values["public_base_url"],
		AssetSigningKey:           values["asset_signing_key"],
		ProviderMode:              values["provider_mode"],
		AgentMode:                 values["agent_mode"],
		AuthMode:                  values["auth_mode"],
		DevelopmentUser:           generation.OwnerID(values["development_user_id"]),
		DevelopmentName:           values["development_user_name"],
		DevelopmentEmail:          values["development_user_email"],
		DevelopmentRoles: runtimeSettingList(
			schema,
			"development_user_roles",
			values["development_user_roles"],
		),
		SessionSecret:        values["session_secret"],
		FrontendPublicAppURL: values["frontend_public_app_url"],
		MockAuth:             values["dev_mock_auth"] == "true",
		MockUserJSON:         values["dev_mock_user_json"],
		AdminAllowlist: runtimeSettingList(
			schema,
			"admin_allowlist_emails",
			values["admin_allowlist_emails"],
		),
		AdminUsername:      values["admin_username"],
		AdminPassword:      values["admin_password"],
		AdminName:          values["admin_display_name"],
		AdminEmail:         values["admin_email"],
		AdminPasswordReset: values["admin_password_reset"] == "true",
	}
	if raw := values["auth_tokens_json"]; raw != "" {
		var tokens map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &tokens); err != nil {
			return Config{}, fmt.Errorf("解析运行设置 auth_tokens_json: %w", err)
		}
		config.AuthTokens = make(map[string]access.Claims, len(tokens))
		for token, encodedClaims := range tokens {
			var owner string
			if err := json.Unmarshal(encodedClaims, &owner); err == nil {
				config.AuthTokens[token] = access.Claims{ID: owner}
				continue
			}
			var claims access.Claims
			if err := json.Unmarshal(encodedClaims, &claims); err != nil {
				return Config{}, fmt.Errorf(
					"解析运行设置 auth_tokens_json 的身份记录: %w",
					err,
				)
			}
			config.AuthTokens[token] = claims
		}
	}
	if config.Logging, err = loggingConfig(values); err != nil {
		return Config{}, err
	}
	budgetSetting := schema.Chat.Agent.MaxToolCallsPerReplySetting
	if config.AgentMaxToolCallsPerReply, err = catalog.ParseToolCallBudget(
		values[budgetSetting],
		schema.Chat.Agent.MaxToolCallsPerReplyMaximum,
	); err != nil {
		return Config{}, fmt.Errorf("运行设置 %s 无效: %w", budgetSetting, err)
	}
	if setting := schema.Chat.Agent.ContextWindowSetting; setting != "" {
		if raw := strings.TrimSpace(values[setting]); raw != "" {
			window, err := strconv.Atoi(raw)
			if err != nil || window < 1 {
				return Config{}, fmt.Errorf("运行设置 %s 必须是正整数（token 数）: %q", setting, raw)
			}
			config.ContextWindowTokens = window
		}
	}
	if config.ExtraChatConnections, err = loadExtraChatConnections(schema, values, lookup); err != nil {
		return Config{}, err
	}
	for _, cidr := range runtimeSettingList(schema, "trusted_proxy_cidrs", values["trusted_proxy_cidrs"]) {
		prefix, err := parseTrustedProxy(cidr)
		if err != nil {
			return Config{}, fmt.Errorf("运行设置 trusted_proxy_cidrs 含无效网段 %q", cidr)
		}
		config.TrustedProxies = append(config.TrustedProxies, prefix.Masked())
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

// parseTrustedProxy accepts a CIDR or a single address (taken as /32 or /128).
func parseTrustedProxy(value string) (netip.Prefix, error) {
	value = strings.TrimSpace(value)
	if address, err := netip.ParseAddr(value); err == nil {
		address = address.Unmap()
		return netip.PrefixFrom(address, address.BitLen()), nil
	}
	return netip.ParsePrefix(value)
}

// deprecatedEnvironmentInUse names the deprecated aliases that supplied a
// setting: the first non-empty source of a setting is the one that is used.
func deprecatedEnvironmentInUse(schema catalog.ApplicationSchema, lookup func(string) string) []string {
	var used []string
	for _, name := range slices.Sorted(maps.Keys(schema.Runtime.Configuration.Settings)) {
		for _, source := range schema.Runtime.Configuration.Settings[name].Environment {
			if strings.TrimSpace(lookup(source.Name)) == "" {
				continue
			}
			if source.Deprecated {
				used = append(used, source.Name+" -> "+strings.ToUpper(name))
			}
			break
		}
	}
	return used
}

func retiredEnvironmentInUse(schema catalog.ApplicationSchema, lookup func(string) string) []string {
	var names []string
	for _, name := range schema.Runtime.Configuration.RetiredEnvironment {
		if strings.TrimSpace(lookup(name)) != "" {
			names = append(names, name)
		}
	}
	return names
}

func runtimeSettingValue(
	schema catalog.ApplicationSchema,
	name string,
	environment string,
	lookup func(string) string,
) (string, error) {
	setting, exists := schema.Runtime.Configuration.Settings[name]
	if !exists {
		return "", fmt.Errorf("Product Schema 缺少运行设置 %q", name)
	}
	for _, source := range setting.Environment {
		value := strings.TrimSpace(lookup(source.Name))
		if value == "" {
			continue
		}
		if source.TrimSuffix != "" || source.Append != "" {
			value = strings.TrimRight(value, "/")
			value = strings.TrimSuffix(value, source.TrimSuffix)
			value += source.Append
		}
		return validateRuntimeSettingValue(name, setting, value)
	}
	value := setting.Default
	if environmentValue, exists :=
		setting.DefaultByEnvironment[environment]; exists {
		value = environmentValue
	}
	return validateRuntimeSettingValue(name, setting, strings.TrimSpace(value))
}

func validateRuntimeSettingValue(
	name string,
	setting catalog.RuntimeSettingSchema,
	value string,
) (string, error) {
	if setting.Required && value == "" {
		return "", fmt.Errorf("运行设置 %q 不能为空", name)
	}
	if len(setting.AllowedValues) > 0 {
		for _, allowed := range setting.AllowedValues {
			if value == allowed {
				return value, nil
			}
		}
		if setting.Secret {
			return "", fmt.Errorf("运行设置 %q 的值不在 Product Schema 允许范围内", name)
		}
		return "", fmt.Errorf(
			"运行设置 %q 的值 %q 不在 Product Schema 允许范围内",
			name,
			value,
		)
	}
	return value, nil
}

func runtimeSettingList(
	schema catalog.ApplicationSchema,
	name string,
	raw string,
) []string {
	if raw == "" {
		return nil
	}
	separator := schema.Runtime.Configuration.Settings[name].Separator
	if separator == "" {
		return []string{raw}
	}
	result := make([]string, 0)
	for _, value := range strings.Split(raw, separator) {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func (c Config) Validate() error {
	switch {
	case strings.TrimSpace(c.Address) == "":
		return errors.New("运行设置 address 不能为空")
	case strings.TrimSpace(c.DatabasePath) == "":
		return errors.New("运行设置 database_path 不能为空")
	case strings.TrimSpace(c.StorageRoot) == "":
		return errors.New("运行设置 storage_root 不能为空")
	case strings.TrimSpace(c.LegacyGeneratedImagesRoot) == "":
		return errors.New("运行设置 legacy_generated_images_root 不能为空")
	}
	if c.ProviderMode != "mock" && c.ProviderMode != "live" {
		return fmt.Errorf("运行设置 provider_mode 的值无效: %q", c.ProviderMode)
	}
	if c.AgentMode != "mock" && c.AgentMode != "live" {
		return fmt.Errorf("运行设置 agent_mode 的值无效: %q", c.AgentMode)
	}
	if c.AuthMode != "development" &&
		c.AuthMode != "session" &&
		c.AuthMode != "bearer" {
		return fmt.Errorf("运行设置 auth_mode 的值无效: %q", c.AuthMode)
	}
	if c.Environment == "production" && c.AuthMode == "development" {
		return errors.New("生产环境禁止 development 身份模式")
	}
	// #247：mock 登录不校验密码即可签发会话，生产环境拒绝启动。
	if c.Environment == "production" && c.MockAuth {
		return errors.New("生产环境禁止开启 DEV_MOCK_AUTH（mock 登录）")
	}
	if c.SessionSecret != "" && len(c.SessionSecret) < 32 {
		return errors.New("SESSION_SECRET 至少需要 32 个字符")
	}
	if c.Environment == "production" && c.SessionSecret == "" {
		return errors.New("生产环境必须配置 SESSION_SECRET")
	}
	if cookieName := strings.TrimSpace(c.RuntimeSettings["session_cookie_name"]); cookieName != "" {
		if err := (&http.Cookie{Name: cookieName}).Valid(); err != nil {
			return errors.New("运行设置 session_cookie_name 必须是合法 HTTP Cookie 名称")
		}
	}
	if (c.PublicBaseURL == "") != (c.AssetSigningKey == "") {
		return errors.New("PUBLIC_BASE_URL 与 ASSET_SIGNING_KEY 必须同时配置")
	}
	if c.AssetSigningKey != "" && len(c.AssetSigningKey) < 32 {
		return errors.New("ASSET_SIGNING_KEY 至少需要 32 字节")
	}
	if c.PublicBaseURL != "" {
		parsed, err := url.Parse(c.PublicBaseURL)
		if err != nil ||
			parsed.Scheme == "" ||
			parsed.Host == "" ||
			parsed.User != nil ||
			parsed.RawQuery != "" ||
			parsed.Fragment != "" {
			return errors.New("PUBLIC_BASE_URL 必须是无凭据、query 和 fragment 的 HTTP(S) URL")
		}
		if parsed.Scheme != "https" && parsed.Scheme != "http" {
			return errors.New("PUBLIC_BASE_URL 仅支持 HTTP(S)")
		}
		if c.Environment == "production" && parsed.Scheme != "https" {
			return errors.New("生产环境 PUBLIC_BASE_URL 必须使用 HTTPS")
		}
	}
	return nil
}

func loggingConfig(values map[string]string) (LoggingConfig, error) {
	level, err := logging.ParseLevel(values["log_level"])
	if err != nil {
		return LoggingConfig{}, fmt.Errorf("运行设置 log_level 无效: %w", err)
	}
	result := LoggingConfig{Level: level, Directory: strings.TrimSpace(values["log_directory"])}
	positive := func(name string) (int, error) {
		number, err := strconv.Atoi(strings.TrimSpace(values[name]))
		if err != nil || number <= 0 {
			return 0, fmt.Errorf("运行设置 %s 必须是正整数: %q", name, values[name])
		}
		return number, nil
	}
	megabytes, err := positive("log_file_max_megabytes")
	if err != nil {
		return LoggingConfig{}, err
	}
	if result.MaxFiles, err = positive("log_file_max_files"); err != nil {
		return LoggingConfig{}, err
	}
	if result.RetentionDays, err = positive("log_retention_days"); err != nil {
		return LoggingConfig{}, err
	}
	result.MaxBytes = int64(megabytes) << 20
	return result, nil
}
