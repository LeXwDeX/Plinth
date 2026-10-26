// provider_connections.go 按 Product Schema 的 runtime.provider_connections
// 声明注册运行时 Provider：工厂只按 protocol 分支，凭据/能力/错误词表全部
// 来自声明；不再存在按 provider 身份写死的注册分支。
package app

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	agentmock "github.com/LeXwDeX/Plinth/internal/agents/mock"
	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/assetrecovery"
	"github.com/LeXwDeX/Plinth/internal/providers/httpjson"
	"github.com/LeXwDeX/Plinth/internal/providers/hyper3d"
	"github.com/LeXwDeX/Plinth/internal/providers/localmodel"
	"github.com/LeXwDeX/Plinth/internal/providers/matting"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
	"github.com/LeXwDeX/Plinth/internal/providers/schemaimages"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

// registerProviders 是 Provider 注册的唯一入口。本地协议在 live/mock 均使用真实
// 转换器且不需要凭据；缺少存储时不公开本地路由。其他协议在 mock 使用模拟端口，
// live 按声明凭据与能力注册。
func registerProvidersWithTransport(
	config Config,
	taskCatalog *catalog.Catalog,
	registry *providers.Registry,
	inputs generation.InputAssetResolver,
	assets generation.AssetStore,
	logger *slog.Logger,
	httpTransport func(connectionID string, base http.RoundTripper) http.RoundTripper,
	recoveryStores ...generation.RecoveryStore,
) error {
	if logger == nil {
		logger = slog.Default()
	}
	application := taskCatalog.Application()
	localKeys := map[string]bool{}
	for _, connection := range application.Runtime.ProviderConnections {
		if _, known := catalog.ProviderProtocolEnum[connection.Protocol]; !known {
			return fmt.Errorf("Provider 连接 %q 使用未知协议 %q", connection.ID, connection.Protocol)
		}
		if connection.Protocol != "local-model" && connection.Protocol != "local-recovery" {
			continue
		}
		localKeys[connection.AdapterKey] = true
		offerings := offeringsForAdapter(taskCatalog.List(""), connection.AdapterKey)
		for _, offering := range offerings {
			var err error
			if connection.Protocol == "local-recovery" {
				err = assetrecovery.ValidateOffering(offering)
			} else {
				err = localmodel.ValidateOffering(offering)
			}
			if err != nil {
				return fmt.Errorf("local Offering %q 的 Schema 无效: %w", offering.ID, err)
			}
		}
		if assets == nil || connection.Protocol == "local-model" && inputs == nil || connection.Protocol == "local-recovery" && (len(recoveryStores) != 1 || recoveryStores[0] == nil) {
			logger.Warn("本地模型转换缺少输入或资产存储，路由不可用", "connection", connection.ID)
			continue
		}
		adapter, err := buildProviderAdapterWithTransport(offerings, connection, "", "", 0, "", inputs, assets, httpTransport, recoveryStores...)
		if err != nil {
			return err
		}
		if err := registry.RegisterCapabilities(connection.AdapterKey, adapter, connection.Capabilities...); err != nil {
			return err
		}
	}
	if config.ProviderMode == "mock" {
		options := mock.DevelopmentOptions()
		options.Inputs = inputs
		adapter := mock.NewWithOptions(options)
		seen := map[string]struct{}{}
		for _, offering := range taskCatalog.List("") {
			if localKeys[offering.AdapterKey] {
				continue
			}
			if _, exists := seen[offering.AdapterKey]; exists {
				continue
			}
			seen[offering.AdapterKey] = struct{}{}
			if err := registry.Register(offering.AdapterKey, adapter); err != nil {
				return err
			}
		}
		return nil
	}
	setting := func(name string) string {
		return configuredRuntimeSetting(config, application, name)
	}
	allOfferings := taskCatalog.List("")
	for _, connection := range application.Runtime.ProviderConnections {
		if _, known := catalog.ProviderProtocolEnum[connection.Protocol]; !known {
			return fmt.Errorf(
				"Provider 连接 %q 使用未知协议 %q",
				connection.ID,
				connection.Protocol,
			)
		}
		// chat LLM 与账户余额连接由各自消费方接线，不注册生成 Provider。
		if connection.Protocol == "local-model" || connection.Protocol == "openai-compatible" ||
			connection.Protocol == "http-json" {
			continue
		}
		if setting(connection.APIKeySetting) == "" {
			continue
		}
		baseURL := setting(connection.BaseURLSetting)
		if baseURL == "" {
			// No default address (GEMINI_BASE_URL, #252): the key is never
			// sent to a guessed address; the connection stays unregistered
			// and its offerings show as unavailable.
			logger.Warn("Provider 连接缺少地址，未启用", "connection", connection.ID, "missing_settings", settingEnvironmentName(application, connection.BaseURLSetting))
			continue
		}
		timeout := time.Duration(connection.RequestTimeoutSeconds) * time.Second
		offerings := offeringsForAdapter(allOfferings, connection.AdapterKey)
		capabilities := connectionCapabilities(
			offerings,
			connection,
			setting,
			inputs,
			logger,
		)
		if len(capabilities) == 0 {
			continue
		}
		adapter, err := buildProviderAdapterWithTransport(
			offerings,
			connection,
			baseURL,
			setting(connection.APIKeySetting),
			timeout,
			providerScratchDir(config),
			inputs,
			assets,
			httpTransport,
		)
		if err != nil {
			return err
		}
		if err := registry.RegisterCapabilities(
			connection.AdapterKey,
			adapter,
			capabilities...,
		); err != nil {
			return err
		}
	}
	if len(registry.Keys()) == 0 {
		return errors.New("live 模式没有配置任何可用 Provider")
	}
	return nil
}

// buildProviderAdapterWithTransport 每个受支持协议恰有一个构造分支；新增
// 同协议身份零代码改动，未知协议启动期报错。
func buildProviderAdapterWithTransport(
	offerings []generation.Offering,
	connection catalog.ProviderConnectionSchema,
	baseURL string,
	apiKey string,
	timeout time.Duration,
	scratchDir string,
	inputs generation.InputAssetResolver,
	assets generation.AssetStore,
	httpTransport func(connectionID string, base http.RoundTripper) http.RoundTripper,
	recoveryStores ...generation.RecoveryStore,
) (generation.ProviderPort, error) {
	switch connection.Protocol {
	case "local-recovery":
		for _, offering := range offerings {
			if err := assetrecovery.ValidateOffering(offering); err != nil {
				return nil, err
			}
		}
		if len(recoveryStores) != 1 {
			return nil, errors.New("local recovery receipt store unavailable")
		}
		return assetrecovery.New(recoveryStores[0], assets)
	case "local-model":
		for _, offering := range offerings {
			if err := localmodel.ValidateOffering(offering); err != nil {
				return nil, fmt.Errorf("local Offering %q 的 Schema 无效: %w", offering.ID, err)
			}
		}
		adapter, err := localmodel.New(localmodel.Config{Inputs: inputs, Assets: assets})
		if err != nil {
			return nil, err
		}
		return adapter, nil
	case "image-sync":
		return schemaimages.New(schemaimages.Config{
			BaseURL:        baseURL,
			APIKey:         apiKey,
			AuthHeader:     connection.AuthHeader,
			AuthPrefix:     connection.AuthPrefix,
			RequestTimeout: timeout,
			HTTPClient:     wireHTTPClient(httpTransport, connection.ID, timeout),
			ErrorMapping:   connection.ErrorMapping,
			InputAssets:    inputs,
			ScratchDir:     scratchDir,
		})
	case "meshy":
		for _, offering := range offerings {
			if err := meshy.ValidateOffering(offering); err != nil {
				return nil, fmt.Errorf(
					"Meshy Offering %q 的 wire Schema 无效: %w",
					offering.ID,
					err,
				)
			}
		}
		publisher, _ := inputs.(generation.InputAssetPublisher)
		return meshy.New(meshy.Config{
			BaseURL:         baseURL,
			APIKey:          apiKey,
			AuthHeader:      connection.AuthHeader,
			AuthPrefix:      connection.AuthPrefix,
			RequestTimeout:  timeout,
			HTTPClient:      wireHTTPClient(httpTransport, connection.ID, timeout),
			ErrorMapping:    connection.ErrorMapping,
			StatusMapping:   connection.StatusMapping,
			AllowHTTPAssets: connection.AllowHTTPAssets,
			InputAssets:     inputs,
			ScratchDir:      scratchDir,
			InputPublisher:  publisher,
		})
	case "tripo":
		for _, offering := range offerings {
			if err := tripo.ValidateOffering(offering); err != nil {
				return nil, fmt.Errorf("Tripo Offering %q 的 wire Schema 无效: %w", offering.ID, err)
			}
		}
		return tripo.New(tripo.Config{
			BaseURL:               baseURL,
			APIKey:                apiKey,
			AuthHeader:            connection.AuthHeader,
			AuthPrefix:            connection.AuthPrefix,
			RequestTimeout:        timeout,
			HTTPClient:            wireHTTPClient(httpTransport, connection.ID, timeout),
			NativeSourceTransport: nativeSourceTransport(httpTransport, connection.ID),
			ErrorMapping:          connection.ErrorMapping,
			StatusMapping:         connection.StatusMapping,
			AllowHTTPAssets:       connection.AllowHTTPAssets,
			InputAssets:           inputs,
			ScratchDir:            scratchDir,
			OfferingRoutes:        offerings,
		})
	case "hyper3d.rodin":
		for _, offering := range offerings {
			if err := hyper3d.ValidateOffering(offering); err != nil {
				return nil, fmt.Errorf(
					"Hyper3D Offering %q 的 wire Schema 无效: %w",
					offering.ID,
					err,
				)
			}
		}
		return hyper3d.New(hyper3d.Config{
			BaseURL:         baseURL,
			APIKey:          apiKey,
			AuthHeader:      connection.AuthHeader,
			AuthPrefix:      connection.AuthPrefix,
			RequestTimeout:  timeout,
			HTTPClient:      wireHTTPClient(httpTransport, connection.ID, timeout),
			ErrorMapping:    connection.ErrorMapping,
			StatusMapping:   connection.StatusMapping,
			AllowHTTPAssets: connection.AllowHTTPAssets,
			InputAssets:     inputs,
			ScratchDir:      scratchDir,
		})
	case "matting":
		for _, offering := range offerings {
			if err := matting.ValidateOffering(offering); err != nil {
				return nil, fmt.Errorf(
					"Matting Offering %q 的 wire Schema 无效: %w",
					offering.ID,
					err,
				)
			}
		}
		return matting.New(matting.Config{
			BaseURL:        baseURL,
			APIKey:         apiKey,
			AuthHeader:     connection.AuthHeader,
			AuthPrefix:     connection.AuthPrefix,
			RequestTimeout: timeout,
			HTTPClient:     wireHTTPClient(httpTransport, connection.ID, timeout),
			InputAssets:    inputs,
			ScratchDir:     scratchDir,
		})
	default:
		return nil, fmt.Errorf(
			"Provider 连接 %q 使用未知协议 %q",
			connection.ID,
			connection.Protocol,
		)
	}
}

// rejectingRoundTripper fails every request. It keeps a decorator that
// returns nil from falling back to the real network.
type rejectingRoundTripper struct{}

func (rejectingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("wire transport decorator returned nil")
}

// wireHTTPClient builds one connection's HTTP client. A nil decorator returns
// nil so httpjson and the Agent keep their own default clients unchanged.
func wireHTTPClient(
	decorate func(connectionID string, base http.RoundTripper) http.RoundTripper,
	connectionID string,
	timeout time.Duration,
) *http.Client {
	if decorate == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = httpjson.DefaultRequestTimeout
	}
	base := http.DefaultTransport
	if defaultTransport, ok := base.(*http.Transport); ok {
		base = defaultTransport.Clone()
	}
	transport := decorate(connectionID, base)
	if transport == nil {
		transport = rejectingRoundTripper{}
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

// nativeSourceTransport scopes Tripo's anonymous-source decoration to its own
// connection ID. Tripo applies it only after building the secured client.
func nativeSourceTransport(
	decorate func(connectionID string, base http.RoundTripper) http.RoundTripper,
	connectionID string,
) func(http.RoundTripper) http.RoundTripper {
	if decorate == nil {
		return nil
	}
	return func(base http.RoundTripper) http.RoundTripper {
		return decorate(connectionID+".native-source", base)
	}
}

// providerScratchDir is where remote Provider Adapters keep private upload
// copies: the storage volume's tmp directory, which the local asset store
// creates and the archive download already uses. The container's /tmp is a
// 64 MB tmpfs, too small for the 150 MB models some Offerings accept (#321 A).
func providerScratchDir(config Config) string {
	if strings.TrimSpace(config.StorageRoot) == "" {
		return ""
	}
	return filepath.Join(config.StorageRoot, "tmp")
}

// connectionCapabilities 按声明计算运行时可用能力：无条件能力恒可用；
// 条件能力在 all_of_settings 全部非空时可用。meshy 协议保留离线内联
// startup 校验器：网关不可用时按 Offering 声明（inline_model_input）降级
// 或停用，并输出与历史一致的告警日志。
func connectionCapabilities(
	offerings []generation.Offering,
	connection catalog.ProviderConnectionSchema,
	setting func(string) string,
	inputs generation.InputAssetResolver,
	logger *slog.Logger,
) []generation.Capability {
	capabilities := append(
		[]generation.Capability(nil),
		connection.Capabilities...,
	)
	if len(connection.ConditionalCapabilities) == 0 {
		return capabilities
	}
	if connection.Protocol != "meshy" {
		for _, group := range connection.ConditionalCapabilities {
			if allSettingsPresent(group.AllOfSettings, setting) {
				capabilities = append(capabilities, group.Capabilities...)
			}
		}
		return capabilities
	}
	publisher, _ := inputs.(generation.InputAssetPublisher)
	for _, group := range connection.ConditionalCapabilities {
		gatewayReady := publisher != nil &&
			allSettingsPresent(group.AllOfSettings, setting)
		if gatewayReady {
			capabilities = append(capabilities, group.Capabilities...)
			continue
		}
		var inlineReady, disabled []string
		for _, capability := range group.Capabilities {
			if meshy.CapabilitySupportsOfflineAssets(offerings, capability) {
				capabilities = append(capabilities, capability)
				inlineReady = append(inlineReady, string(capability))
			} else {
				disabled = append(disabled, string(capability))
			}
		}
		if len(inlineReady) > 0 {
			logger.Warn(
				"资产网关未配置（PUBLIC_BASE_URL 与 ASSET_SIGNING_KEY），" +
					"Meshy 后处理能力（" + strings.Join(inlineReady, "/") +
					"）将按 Offering 声明以 data URI 内联提交模型",
			)
		}
		if len(disabled) > 0 {
			logger.Warn(
				"Meshy 后处理能力（" + strings.Join(disabled, "/") +
					"）因资产网关未配置且 Offering 未声明 inline_model_input 而停用，" +
					"需配置 PUBLIC_BASE_URL（生产必须 HTTPS）与 ASSET_SIGNING_KEY（≥32 字节）后重启生效",
			)
		}
	}
	return capabilities
}

func allSettingsPresent(
	settings []string,
	setting func(string) string,
) bool {
	for _, name := range settings {
		if strings.TrimSpace(setting(name)) == "" {
			return false
		}
	}
	return true
}

// offeringsForAdapter establishes the connection boundary before adapter validation
// and route construction. Offerings owned by another connection never cross it.
func offeringsForAdapter(offerings []generation.Offering, adapterKey string) []generation.Offering {
	var scoped []generation.Offering
	for _, offering := range offerings {
		if offering.AdapterKey == adapterKey {
			scoped = append(scoped, offering)
		}
	}
	return scoped
}

// buildChatAgentWithTransport 按 chat.agent.connection_id 解析声明连接并按
// 协议构造 Chat LLM 适配器；协议分支唯一，未知协议启动期报错。mock 模式不变。
func buildChatAgentWithTransport(
	config Config,
	schema catalog.ApplicationSchema,
	httpTransport func(connectionID string, base http.RoundTripper) http.RoundTripper,
) (agents.Adapter, error) {
	registry := agents.NewRegistry()
	var adapter agents.Adapter
	switch config.AgentMode {
	case "mock":
		texts, err := catalog.NewTextLocalizer(schema)
		if err != nil {
			return nil, err
		}
		mockAdapter, err := agentmock.New(agentmock.Config{
			Localize:        texts.TextIn,
			ToolName:        schema.Chat.Agent.MockToolName,
			PromptArgument:  schema.Chat.Agent.MockPromptArgument,
			Summary:         schema.Chat.Agent.MockSummary,
			FailureSummary:  schema.Chat.Agent.MockFailureSummary,
			WaitingSummary:  schema.Chat.Agent.MockWaitingSummary,
			WaitingContent:  schema.Chat.Agent.ConsentWaitingContent,
			RechargeContent: schema.Chat.Agent.ConsentRechargeContent,
			DeniedSummary:   schema.Chat.Agent.MockDeniedSummary,
			DeniedContent:   schema.Chat.Agent.ConsentDeniedContent,
			ScopeRefusal:    schema.Chat.AgentScope.MockRefusal,
			ChoiceSummary:   schema.Chat.Choice.MockSummary,
			SummaryTemplate: schema.Chat.ContextCompaction.MockSummaryTemplate,
			UserRole:        schema.Chat.Roles.User,
			ToolRole:        schema.Chat.Roles.Tool,
		})
		if err != nil {
			return nil, err
		}
		adapter = mockAdapter
	case "live":
		connection, err := chatAgentConnection(schema)
		if err != nil {
			return nil, err
		}
		if connection.Protocol != "openai-compatible" {
			return nil, fmt.Errorf(
				"Chat Agent 连接 %q 使用未知协议 %q",
				connection.ID,
				connection.Protocol,
			)
		}
		// Every chat model connection gets its own adapter from its own
		// address and key (#322 Scope 2); a request names its connection.
		connections := chatConnections(config, schema)
		adapters := make(map[string]agents.Adapter, len(connections))
		configured := false
		for _, chatConnection := range connections {
			if missing := chatConnection.missingSettings(); len(missing) > 0 {
				// No base URL (#251), API key or model: the connection
				// reports itself not configured (#253).
				adapters[chatConnection.ID] = agents.NotConfigured{Missing: missing}
				continue
			}
			liveAdapter, err := openaicompatible.New(
				openaicompatible.Config{
					BaseURL:           chatConnection.BaseURL,
					APIKey:            chatConnection.APIKey,
					EndpointPath:      schema.Chat.Agent.EndpointPath,
					AuthorizationName: schema.Chat.Agent.AuthorizationName,
					AuthorizationType: schema.Chat.Agent.AuthorizationType,
					RequestTimeout: time.Duration(
						schema.Chat.Agent.RequestTimeoutSeconds,
					) * time.Second,
					Client: wireHTTPClient(
						httpTransport,
						chatConnection.ID,
						time.Duration(schema.Chat.Agent.RequestTimeoutSeconds)*time.Second,
					),
					ScannerMaxBytes:   schema.Chat.Agent.ScannerMaxBytes,
					ErrorBodyMaxBytes: schema.Chat.Agent.ErrorBodyMaxBytes,
					// Each request's model picks its own profile, so the
					// thinking level a session chose reaches the right field
					// (#322).
					ThinkingFor: func(model string) *openaicompatible.ThinkingOptions {
						return chatThinkingOptions(schema.Chat.Agent, model)
					},
					StreamIncludeUsage: schema.Chat.Agent.StreamIncludeUsage,
				},
			)
			if err != nil {
				return nil, fmt.Errorf("Chat Agent 连接 %s: %w", chatConnection.ID, err)
			}
			adapters[chatConnection.ID] = liveAdapter
			configured = true
		}
		if !configured {
			// No connection can serve: the application still starts and the
			// Agent reports itself not configured (#253).
			adapter = adapters[connections[0].ID]
			break
		}
		router, err := agents.NewConnectionRouter(connections[0].ID, adapters)
		if err != nil {
			return nil, err
		}
		adapter = router
	default:
		return nil, fmt.Errorf(
			"运行设置 agent_mode 的值无效: %q",
			config.AgentMode,
		)
	}
	if err := registry.Register(schema.Chat.Agent.AdapterKey, adapter); err != nil {
		return nil, err
	}
	selected, exists := registry.Adapter(schema.Chat.Agent.AdapterKey)
	if !exists {
		return nil, fmt.Errorf(
			"Chat Agent Adapter %q 未注册",
			schema.Chat.Agent.AdapterKey,
		)
	}
	return selected, nil
}

func chatAgentConnection(
	schema catalog.ApplicationSchema,
) (catalog.ProviderConnectionSchema, error) {
	connectionID := strings.TrimSpace(schema.Chat.Agent.ConnectionID)
	if connectionID == "" {
		return catalog.ProviderConnectionSchema{}, fmt.Errorf(
			"Product Schema chat.agent 缺少 connection_id 声明",
		)
	}
	connection, exists := catalog.ProviderConnectionByID(
		schema.Runtime.ProviderConnections,
		connectionID,
	)
	if !exists {
		return catalog.ProviderConnectionSchema{}, fmt.Errorf(
			"chat.agent.connection_id 引用了未知 Provider 连接 %q",
			connectionID,
		)
	}
	return connection, nil
}

func chatThinkingOptions(
	agent catalog.ChatAgentSchema,
	model string,
) *openaicompatible.ThinkingOptions {
	profile, matched := chatModelProfile(agent.Thinking, model)
	if !matched {
		// No vendor profile: only standard OpenAI chat-completions fields
		// (#253); stream usage follows the agent-level switch.
		return nil
	}
	return &openaicompatible.ThinkingOptions{
		RequestFields:      profile.RequestFields,
		ReasoningPassback:  profile.ReasoningPassback,
		EffortField:        profile.EffortField,
		EffortLevels:       profile.EffortLevels,
		DefaultEffort:      profile.DefaultEffort,
		StreamIncludeUsage: profile.IncludeStreamUsage(agent.StreamIncludeUsage),
	}
}

// chatModelProfile is the first vendor profile whose prefix the configured
// model name starts with, case-insensitively; none while profiles are off.
func chatModelProfile(thinking *catalog.ChatThinkingSchema, model string) (catalog.ChatThinkingModelProfile, bool) {
	return thinking.ProfileFor(model)
}

// settingEnvironmentName is the canonical environment variable of a runtime
// setting (its name when it reads none), for logs that name a missing setting.
func settingEnvironmentName(schema catalog.ApplicationSchema, name string) string {
	if environment := schema.Runtime.Configuration.Settings[name].Environment; len(environment) > 0 {
		return environment[0].Name
	}
	return name
}
