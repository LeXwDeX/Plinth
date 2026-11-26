// provider_connections.go 按 Product Schema 的 runtime.provider_connections
// 声明注册运行时 Provider：工厂只按 protocol 分支，凭据/能力/错误词表全部
// 来自声明；不再存在按 provider 身份写死的注册分支。
package app

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/inlinepictures"
	agentmock "github.com/LeXwDeX/Plinth/internal/agents/mock"
	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"
	"github.com/LeXwDeX/Plinth/internal/agents/openairesponses"

	"github.com/LeXwDeX/Plinth/internal/assets/imagefit"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/assetrecovery"
	"github.com/LeXwDeX/Plinth/internal/providers/httpjson"
	"github.com/LeXwDeX/Plinth/internal/providers/hyper3d"
	"github.com/LeXwDeX/Plinth/internal/providers/localmodel"
	"github.com/LeXwDeX/Plinth/internal/providers/localparts"
	"github.com/LeXwDeX/Plinth/internal/providers/matting"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
	"github.com/LeXwDeX/Plinth/internal/providers/schemaimages"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"github.com/LeXwDeX/Plinth/internal/providers/upstreampool"
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
	// One process-wide upstream concurrency pool serves every registered
	// Provider pool (#468). It is in-memory only; a restart begins from zero.
	pool := upstreampool.New(upstreampool.Options{})
	localKeys := map[string]bool{}
	for _, connection := range application.Runtime.ProviderConnections {
		if _, known := catalog.ProviderProtocolEnum[connection.Protocol]; !known {
			return fmt.Errorf("Provider 连接 %q 使用未知协议 %q", connection.ID, connection.Protocol)
		}
		if connection.Protocol != "local-model" && connection.Protocol != "local-recovery" && connection.Protocol != "local-model-parts" {
			continue
		}
		localKeys[connection.AdapterKey] = true
		offerings := offeringsForAdapter(taskCatalog.List(""), connection.AdapterKey)
		for _, offering := range offerings {
			var err error
			switch connection.Protocol {
			case "local-recovery":
				err = assetrecovery.ValidateOffering(offering)
			case "local-model-parts":
				err = localparts.ValidateOffering(offering)
			default:
				err = localmodel.ValidateOffering(offering)
			}
			if err != nil {
				return fmt.Errorf("local Offering %q 的 Schema 无效: %w", offering.ID, err)
			}
		}
		if assets == nil || (connection.Protocol == "local-model" || connection.Protocol == "local-model-parts") && inputs == nil || connection.Protocol == "local-recovery" && (len(recoveryStores) != 1 || recoveryStores[0] == nil) {
			logger.Warn("平台处理缺少输入或资产存储，路由不可用", "connection", connection.ID)
			continue
		}
		adapter, err := buildProviderAdapterWithTransport(offerings, connection, "", "", 0, "", inputs, assets, httpTransport, pool, recoveryStores...)
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
		if connection.Protocol == "local-model" || connection.Protocol == "local-model-parts" ||
			connection.Protocol == "openai-compatible" || connection.Protocol == "http-json" {
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
		registerUpstreamPools(pool, connection, offerings)
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
			pool,
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

// registerUpstreamPools registers the connection's declared upstream pools
// (#468). The limit comes from the declaration's environment variable when set,
// else its default; the hard maximum lifetime is the largest job timeout plus
// queue wait among the connection's Offerings, so a leaked slot cannot outlive
// the longest job that could hold it. A connection without a declaration
// registers nothing and is never queued.
func registerUpstreamPools(
	pool *upstreampool.Manager,
	connection catalog.ProviderConnectionSchema,
	offerings []generation.Offering,
) {
	if pool == nil || connection.Concurrency == nil || len(connection.Concurrency.Pools) == 0 {
		return
	}
	maxLifetime := time.Duration(0)
	for _, offering := range offerings {
		timeout := offering.Timeout
		if offering.JobTimeout > timeout {
			timeout = offering.JobTimeout
		}
		if lifetime := timeout + offering.UpstreamQueueTimeout; lifetime > maxLifetime {
			maxLifetime = lifetime
		}
	}
	for name, declaration := range connection.Concurrency.Pools {
		limit := declaration.Default
		if declaration.LimitEnv != "" {
			if raw := strings.TrimSpace(os.Getenv(declaration.LimitEnv)); raw != "" {
				if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
					limit = parsed
				}
			}
		}
		pool.Register(connection.AdapterKey, name, upstreampool.PoolConfig{
			Limit:       limit,
			MaxLifetime: maxLifetime,
			IdleTTL:     poolSecondsSetting(declaration.IdleTTLSeconds),
			WaitHint:    poolSecondsSetting(declaration.WaitHintSeconds),
			QueuePoll:   poolSecondsSetting(declaration.QueuePollSeconds),
		})
	}
}

// poolSecondsSetting resolves one optional pool policy value: the declared
// default, overridden by its environment variable when set. Zero (absent or an
// invalid override) leaves the Manager's or Adapter's own fallback in place.
func poolSecondsSetting(setting *catalog.ProviderPoolSettingSchema) time.Duration {
	if setting == nil {
		return 0
	}
	value := setting.Default
	if setting.Env != "" {
		if raw := strings.TrimSpace(os.Getenv(setting.Env)); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
				value = parsed
			}
		}
	}
	if value <= 0 {
		return 0
	}
	return time.Duration(value) * time.Second
}

// tripoPoolDefault 返回 Tripo 连接为「未声明自己用哪个池」的 Offering 准备的
// 默认池（#487）。上游按 task category 分池，正常声明是多个池、每个 Offering
// 自己声明所属池；只有一个池的连接（测试、自建网关）继续让整个连接共用它。
func tripoPoolDefault(connection catalog.ProviderConnectionSchema) string {
	if connection.Concurrency == nil || len(connection.Concurrency.Pools) != 1 {
		return ""
	}
	for name := range connection.Concurrency.Pools {
		return name
	}
	return ""
}

// validateTripoPoolDeclarations 校验每个 Tripo Offering 声明的上游池都在连接的
// concurrency.pools 里注册过（#487）。池是否真的排队由注册决定，所以一个拼错
// 或已删除的池名会让该 Offering 静默地永不排队；宁可启动期失败。
func validateTripoPoolDeclarations(
	connection catalog.ProviderConnectionSchema,
	offerings []generation.Offering,
) error {
	if connection.Concurrency == nil || len(connection.Concurrency.Pools) == 0 {
		return nil
	}
	for _, offering := range offerings {
		pool, err := tripo.OfferingPool(offering)
		if err != nil {
			return fmt.Errorf(
				"Tripo Offering %q 的 provider_options.pool 无效: %w", offering.ID, err)
		}
		if pool == "" {
			continue
		}
		if _, declared := connection.Concurrency.Pools[pool]; !declared {
			return fmt.Errorf(
				"Tripo Offering %q 声明了连接未注册的 upstream 池 %q", offering.ID, pool)
		}
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
	pool *upstreampool.Manager,
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
	case "local-model-parts":
		for _, offering := range offerings {
			if err := localparts.ValidateOffering(offering); err != nil {
				return nil, fmt.Errorf("local Offering %q 的 Schema 无效: %w", offering.ID, err)
			}
		}
		adapter, err := localparts.New(localparts.Config{Inputs: inputs, Assets: assets})
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
			BaseURL:                baseURL,
			APIKey:                 apiKey,
			AuthHeader:             connection.AuthHeader,
			AuthPrefix:             connection.AuthPrefix,
			RequestTimeout:         timeout,
			HTTPClient:             wireHTTPClient(httpTransport, connection.ID, timeout),
			ErrorMapping:           connection.ErrorMapping,
			StatusMapping:          connection.StatusMapping,
			AllowHTTPAssets:        connection.AllowHTTPAssets,
			InputAssets:            inputs,
			ScratchDir:             scratchDir,
			InputPublisher:         publisher,
			RetrySubmitServerError: connection.RetrySubmitServerError,
			PollTimeout:            time.Duration(connection.PollRequestTimeoutSeconds) * time.Second,
		})
	case "tripo":
		for _, offering := range offerings {
			if err := tripo.ValidateOffering(offering); err != nil {
				return nil, fmt.Errorf("Tripo Offering %q 的 wire Schema 无效: %w", offering.ID, err)
			}
		}
		if err := validateTripoPoolDeclarations(connection, offerings); err != nil {
			return nil, err
		}
		return tripo.New(tripo.Config{
			BaseURL:                baseURL,
			APIKey:                 apiKey,
			AuthHeader:             connection.AuthHeader,
			AuthPrefix:             connection.AuthPrefix,
			RequestTimeout:         timeout,
			HTTPClient:             wireHTTPClient(httpTransport, connection.ID, timeout),
			NativeSourceTransport:  nativeSourceTransport(httpTransport, connection.ID),
			ErrorMapping:           connection.ErrorMapping,
			StatusMapping:          connection.StatusMapping,
			AllowHTTPAssets:        connection.AllowHTTPAssets,
			InputAssets:            inputs,
			ScratchDir:             scratchDir,
			OfferingRoutes:         offerings,
			Pool:                   pool,
			PoolID:                 tripoPoolDefault(connection),
			RetrySubmitServerError: connection.RetrySubmitServerError,
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
			BaseURL:                baseURL,
			APIKey:                 apiKey,
			AuthHeader:             connection.AuthHeader,
			AuthPrefix:             connection.AuthPrefix,
			RequestTimeout:         timeout,
			HTTPClient:             wireHTTPClient(httpTransport, connection.ID, timeout),
			ErrorMapping:           connection.ErrorMapping,
			StatusMapping:          connection.StatusMapping,
			AllowHTTPAssets:        connection.AllowHTTPAssets,
			InputAssets:            inputs,
			ScratchDir:             scratchDir,
			RetrySubmitServerError: connection.RetrySubmitServerError,
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
			BaseURL:                baseURL,
			APIKey:                 apiKey,
			AuthHeader:             connection.AuthHeader,
			AuthPrefix:             connection.AuthPrefix,
			RequestTimeout:         timeout,
			HTTPClient:             wireHTTPClient(httpTransport, connection.ID, timeout),
			ErrorMapping:           connection.ErrorMapping,
			InputAssets:            inputs,
			ScratchDir:             scratchDir,
			RetrySubmitServerError: connection.RetrySubmitServerError,
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

// projectCitationTemplate is the mock Agent's closing line for project asset
// use (#240). A Catalog without projects has none.
func projectCitationTemplate(schema catalog.ApplicationSchema) string {
	if schema.Project == nil || schema.Project.Agent == nil {
		return ""
	}
	return schema.Project.Agent.CitationTemplate
}

// requestPictureEncoder builds the encoder of chat.agent.request_pictures
// (#555); nil when the Catalog declares none. One encoder serves the chat layer
// and every chat model Adapter, so a picture is encoded once and the call log
// can still name its size before encoding.
func requestPictureEncoder(agent catalog.ChatAgentSchema) (*inlinepictures.Encoder, error) {
	declared := agent.RequestPictures
	if declared == nil {
		return nil, nil
	}
	background, err := imagefit.ParseHexColor(declared.BackgroundColor)
	if err != nil {
		return nil, fmt.Errorf("chat.agent.request_pictures.background_color: %w", err)
	}
	return inlinepictures.New(inlinepictures.Policy{
		Format:        declared.Format,
		Quality:       declared.Quality,
		Background:    background,
		MaximumPixels: declared.MaxPixels,
	})
}

// buildChatAgentWithTransport 按 chat.agent.connection_id 解析声明连接并按
// 协议构造 Chat LLM 适配器；协议分支唯一，未知协议启动期报错。mock 模式不变。
func buildChatAgentWithTransport(
	config Config,
	schema catalog.ApplicationSchema,
	httpTransport func(connectionID string, base http.RoundTripper) http.RoundTripper,
	logger *slog.Logger,
	pictures *inlinepictures.Encoder,
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
			Localize:         texts.TextIn,
			ToolName:         schema.Chat.Agent.MockToolName,
			PromptArgument:   schema.Chat.Agent.MockPromptArgument,
			Summary:          schema.Chat.Agent.MockSummary,
			FailureSummary:   schema.Chat.Agent.MockFailureSummary,
			WaitingSummary:   schema.Chat.Agent.MockWaitingSummary,
			WaitingContent:   schema.Chat.Agent.ConsentWaitingContent,
			RechargeContent:  schema.Chat.Agent.ConsentRechargeContent,
			DeniedSummary:    schema.Chat.Agent.MockDeniedSummary,
			DeniedContent:    schema.Chat.Agent.ConsentDeniedContent,
			ScopeRefusal:     schema.Chat.AgentScope.MockRefusal,
			ChoiceSummary:    schema.Chat.Choice.MockSummary,
			SummaryTemplate:  schema.Chat.ContextCompaction.MockSummaryTemplate,
			CitationTemplate: projectCitationTemplate(schema),
			UserRole:         schema.Chat.Roles.User,
			ToolRole:         schema.Chat.Roles.Tool,
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
			// One client for both wires of the connection (#401), so a
			// recorded or routed transport applies to every request.
			// Both wires of the connection share one memory of its last
			// used-up quota, which reads a gateway's later mute (#640).
			quota := newChatQuotaMemory(chatExhaustedQuota(connection.ErrorMapping), schema.Chat.Agent.QuotaMute, nil)
			client := wireHTTPClient(
				httpTransport,
				chatConnection.ID,
				time.Duration(schema.Chat.Agent.RequestTimeoutSeconds)*time.Second,
			)
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
					FirstByteTimeout:  time.Duration(schema.Chat.Agent.FirstByteTimeoutSeconds) * time.Second,
					Client:            client,
					ScannerMaxBytes:   schema.Chat.Agent.ScannerMaxBytes,
					ErrorBodyMaxBytes: schema.Chat.Agent.ErrorBodyMaxBytes,
					// Each request's model picks its own profile, so the
					// thinking level a session chose reaches the right field
					// (#322).
					ThinkingFor: func(model string) *openaicompatible.ThinkingOptions {
						return chatThinkingOptions(schema.Chat.Agent, model)
					},
					StreamIncludeUsage: schema.Chat.Agent.StreamIncludeUsage,
					ParallelToolCalls:  schema.Chat.Agent.ParallelToolCalls,
					// Every model call leaves one line: model, thinking level,
					// duration, first byte, tokens and the upstream's request
					// id. No prompt, no picture, no key (#382).
					Logger: logger,
					// Every chat connection speaks the declared connection's
					// protocol, so its error_mapping reads their refusals.
					ExhaustedQuota: quota.reader(),
					// Every inline picture goes out in the declared format,
					// with the chat layer's encodings shared (#555).
					Pictures: pictures,
				},
			)
			if err != nil {
				return nil, fmt.Errorf("Chat Agent 连接 %s: %w", chatConnection.ID, err)
			}
			var served agents.Adapter = liveAdapter
			if schema.Chat.Agent.Thinking.UsesResponses() {
				// A profile that declares wire_api responses is served by the
				// Responses Adapter, from the same address, key and client
				// (#401); the connection picks per request by its model.
				responsesAdapter, err := openairesponses.New(openairesponses.Config{
					BaseURL:           chatConnection.BaseURL,
					APIKey:            chatConnection.APIKey,
					EndpointPath:      schema.Chat.Agent.ResponsesEndpointPath,
					AuthorizationName: schema.Chat.Agent.AuthorizationName,
					AuthorizationType: schema.Chat.Agent.AuthorizationType,
					RequestTimeout: time.Duration(
						schema.Chat.Agent.RequestTimeoutSeconds,
					) * time.Second,
					FirstByteTimeout:  time.Duration(schema.Chat.Agent.FirstByteTimeoutSeconds) * time.Second,
					Client:            client,
					ScannerMaxBytes:   schema.Chat.Agent.ScannerMaxBytes,
					ErrorBodyMaxBytes: schema.Chat.Agent.ErrorBodyMaxBytes,
					OptionsFor: func(model string) *openairesponses.Options {
						return responsesOptions(schema.Chat.Agent, model)
					},
					ParallelToolCalls: schema.Chat.Agent.ParallelToolCalls,
					Logger:            logger,
					ExhaustedQuota:    quota.reader(),
					Pictures:          pictures,
				})
				if err != nil {
					return nil, fmt.Errorf("Chat Agent 连接 %s: %w", chatConnection.ID, err)
				}
				served = chatWire{
					chatCompletions: liveAdapter,
					responses:       responsesAdapter,
					wireFor:         schema.Chat.Agent.Thinking.WireFor,
				}
			}
			// A transient failure of a model call is retried, and a round is
			// bounded, as chat.agent declares (#509).
			adapters[chatConnection.ID] = agents.NewRetrying(served, chatRetryPolicy(schema.Chat.Agent, logger))
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

// chatExhaustedQuota reads a chat model refusal through the chat
// connection's Schema error_mapping with the same algorithm as the generation
// adapters (#120). A connection that declares none reads no refusal as a
// used-up quota.
func chatExhaustedQuota(mapping *generation.ErrorMapping) func(int, []byte) (string, bool) {
	if mapping == nil {
		return nil
	}
	return func(status int, body []byte) (string, bool) {
		class, exhausted := httpjson.DeclaredExhaustion(mapping, status, body)
		return string(class), exhausted
	}
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

// chatAgentDisplayName is the chat model connection's display name, which
// names the upstream in the text of a refused model call; "" when the Schema
// declares no chat connection.
func chatAgentDisplayName(schema catalog.ApplicationSchema) string {
	connection, err := chatAgentConnection(schema)
	if err != nil {
		return ""
	}
	return connection.DisplayName
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
		ReasoningKind:      profile.ReasoningKind(),
		EffortField:        profile.EffortField,
		EffortLevels:       profile.EffortLevels,
		DefaultEffort:      profile.DefaultEffort,
		StreamIncludeUsage: profile.IncludeStreamUsage(agent.StreamIncludeUsage),
		OmitTemperature:    profile.OmitTemperature,
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

// chatRetryPolicy is the retry of model calls chat.agent declares (#509): its
// retry and round_timeout_seconds. A Schema without retry sends each call once.
func chatRetryPolicy(agent catalog.ChatAgentSchema, logger *slog.Logger) agents.RetryPolicy {
	policy := agents.RetryPolicy{
		MaxAttempts:  1,
		RoundTimeout: time.Duration(agent.RoundTimeoutSeconds) * time.Second,
		Logger:       logger,
	}
	if retry := agent.Retry; retry != nil {
		policy.MaxAttempts = retry.MaxAttempts
		policy.InitialBackoff = time.Duration(retry.InitialBackoffMillis) * time.Millisecond
		policy.MaxBackoff = time.Duration(retry.MaxBackoffMillis) * time.Millisecond
		policy.MaxRetryAfter = time.Duration(retry.MaxRetryAfterSeconds) * time.Second
		policy.EmptyRetries = retry.EmptyResponseRetries
	}
	return policy
}
