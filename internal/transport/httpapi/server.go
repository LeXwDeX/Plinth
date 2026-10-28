package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/netip"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/assetdelivery"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/cachemanagement"
	"github.com/LeXwDeX/Plinth/internal/canvas"
	"github.com/LeXwDeX/Plinth/internal/capture"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
	"github.com/LeXwDeX/Plinth/internal/lineage"
	"github.com/LeXwDeX/Plinth/internal/llmsettings"
	"github.com/LeXwDeX/Plinth/internal/mcp"
	"github.com/LeXwDeX/Plinth/internal/modeldelivery"
	"github.com/LeXwDeX/Plinth/internal/processing"
	"github.com/LeXwDeX/Plinth/internal/promptenhance"
	"github.com/LeXwDeX/Plinth/internal/provideraccounts"
	"github.com/LeXwDeX/Plinth/internal/rendermanifest"
	"github.com/LeXwDeX/Plinth/internal/routing"
	"github.com/LeXwDeX/Plinth/internal/scene"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
	"github.com/LeXwDeX/Plinth/internal/taskretry"
	"github.com/LeXwDeX/Plinth/internal/transport/httplocale"
)

type Config struct {
	Harness            *resolve.Service
	AgentConfiguration *agentconfiguration.Service
	// LLMSettings is the administrator's LLM settings (#322); nil leaves
	// its routes unregistered.
	LLMSettings            *llmsettings.Service
	MCP                    *mcp.Service
	AccountInputAssets     AccountInputAssetLister
	Readiness              func(context.Context) error
	Generation             *generation.Module
	ExecutionGrants        *executiongrant.Service
	Processing             *processing.Service
	ModelDelivery          *modeldelivery.Service
	Lineage                *lineage.Service
	Retry                  *taskretry.Service
	ProviderWebhooks       ProviderWebhookIngestor
	ProviderWebhookRuntime map[string]ProviderWebhookRuntime
	Identity               IdentityResolver
	// LoginFailureStore 持久化登录失败限流。不注入时限流只活在进程内存里，
	// 重启即清零——可被"撞满阈值 → 等重启 → 再撞"绕开。
	LoginFailureStore                 LoginFailureStore
	Authentication                    *authentication.Service
	Administration                    *administration.UserService
	AdministrationAudit               *administration.AuditService
	AdministrationGenerations         *administration.GenerationService
	AdministrationGenerationDownloads *administration.GenerationDownloadService
	AdministrationUsage               *administration.UsageService
	AdministrationBalanceSummary      *administration.BalanceSummaryService
	AdministrationUserAssets          *administration.UserAssetsModule
	AdministrationBackfillStatus      *administration.BackfillStatusModule
	Cache                             *cachemanagement.Service
	Canvas                            *canvas.Service
	CanvasImages                      *canvas.ImageService
	Captures                          capture.Recorder
	SkillRuns                         *skillrun.Runtime
	// PendingApprovals serves the approvals that outlive their turn.
	PendingApprovals PendingApprovals
	// ThreeViews serves the three-view groups of a session.
	ThreeViews      ThreeViews
	Scene           *scene.Service
	RenderManifests *rendermanifest.Service
	Chat            *chat.Service
	ChatScheduler   ChatTurnScheduler
	// ChatContext compacts a session's context on request (#322); nil makes
	// the compaction route answer that compaction is unavailable.
	ChatContext ChatContextCompactor
	// OwnsAgentSession 校验 harness 的 scope 是当前用户自己的会话；
	// 参数/同意/绑定写入全部经此守卫。
	OwnsAgentSession      func(context.Context, string, string) bool
	AgentParams           *agentparams.Store
	AgentState            *agentparams.Store
	AgentOperations       AgentOperationReader
	PromptEnhancer        *promptenhance.Service
	ProviderAccounts      *provideraccounts.Service
	AssetProxy            AssetProxy
	LegacyGeneratedImages LegacyGeneratedImageStore
	Application           catalog.ApplicationSchema
	SkillCatalog          catalog.SkillCatalogSchema
	InputAssets           InputAssetUploader
	ModelPreviews         ModelPreviewReader
	// AssetPreviews makes the downscaled task pictures small views ask for (#290); nil serves originals.
	AssetPreviews  AssetPreviews
	AssetGateway   SignedAssetGateway
	RuntimeVersion func() string
	// ArchiveTempDir 是 Range 归档物化临时文件的目录。留空时用
	// os.TempDir()；生产装配传入运行卷目录，因为容器默认 /tmp
	// 可能是小容量 tmpfs，装不下上百 MiB 的归档。
	ArchiveTempDir string
	// AssetVerificationStats 返回交付解析缓存累计命中/未命中次数和
	// SHA 实际读取字节数（含命中及失败读取）。
	AssetVerificationStats func() (hits, misses, verifiedBytes uint64)
	Clock                  func() time.Time
	Logger                 *slog.Logger
	// TrustedProxies are the reverse proxies whose X-Forwarded-For names the
	// client (#256). Empty means the TCP peer is always the client.
	TrustedProxies []netip.Prefix
}

type ChatTurnScheduler interface {
	Schedule(context.Context, chat.ProcessTurn) error
	// CancelTurn 用户停止当前会话的活动轮次；返回是否有轮次被中断。
	CancelTurn(ctx context.Context, ownerID, sessionID string) (bool, error)
	// ResumeConsentWait 在同意决定写入后唤醒挂起中的轮次（AGT-12）；
	// 返回是否存在被唤醒的挂起轮次。
	ResumeConsentWait(ownerID, sessionID string) bool
}

type ProviderWebhookIngestor interface {
	IngestProviderWebhook(
		context.Context,
		generation.ProviderWebhookCommand,
	) (generation.ProviderWebhookResult, error)
}

// ProviderWebhookRuntime 是回调契约 ID 到部署秘密值的运行时投影。
// 路由、认证 header 和线协议仍只由 Product Schema 拥有。
type ProviderWebhookRuntime struct {
	PathSecret string
	Key        string
}

type providerWebhookBinding struct {
	schema     catalog.ProviderWebhookSchema
	route      routing.One
	pathSecret string
	key        string
}

type Server struct {
	clientErrors                      perMinuteLimiter
	apiRequests                       perMinuteLimiter
	trustedProxies                    []netip.Prefix
	harness                           *resolve.Service
	agentConfiguration                *agentconfiguration.Service
	llmSettings                       *llmsettings.Service
	mcp                               *mcp.Service
	accountInputAssets                AccountInputAssetLister
	readiness                         func(context.Context) error
	generation                        *generation.Module
	executionGrants                   *executiongrant.Service
	processing                        *processing.Service
	modelDelivery                     *modeldelivery.Service
	lineage                           *lineage.Service
	retry                             *taskretry.Service
	providerWebhooks                  ProviderWebhookIngestor
	providerWebhookBindings           []providerWebhookBinding
	loginLimiter                      *loginFailureLimiter
	identity                          IdentityResolver
	authentication                    *authentication.Service
	administration                    *administration.UserService
	administrationAudit               *administration.AuditService
	administrationGenerations         *administration.GenerationService
	administrationGenerationDownloads *administration.GenerationDownloadService
	administrationUsage               *administration.UsageService
	administrationBalanceSummary      *administration.BalanceSummaryService
	administrationUserAssets          *administration.UserAssetsModule
	administrationBackfillStatus      *administration.BackfillStatusModule
	cache                             *cachemanagement.Service
	canvas                            *canvas.Service
	canvasImages                      *canvas.ImageService
	captures                          capture.Recorder
	skillRuns                         *skillrun.Runtime
	pendingApprovals                  PendingApprovals
	threeViews                        ThreeViews
	scene                             *scene.Service
	renderManifests                   *rendermanifest.Service
	chat                              *chat.Service
	chatScheduler                     ChatTurnScheduler
	chatContext                       ChatContextCompactor
	ownsAgentSession                  func(context.Context, string, string) bool
	promptEnhancer                    *promptenhance.Service
	providerAccounts                  *provideraccounts.Service
	assetProxy                        AssetProxy
	legacyGeneratedImages             LegacyGeneratedImageStore
	application                       catalog.ApplicationSchema
	publicSchemas                     *catalog.LocalizedPublicSchemas
	texts                             *catalog.TextLocalizer
	// taskEventWhitelist 是公开事件面白名单查找索引，唯一真源为 catalog
	// 声明 application.runtime.task_events.public_projection（§8.1；P3）。
	taskEventWhitelist     map[string]map[string]struct{}
	skillCatalog           catalog.SkillCatalogSchema
	agentParams            *agentparams.Store
	agentState             *agentparams.Store
	agentOperations        AgentOperationReader
	inputAssets            InputAssetUploader
	modelPreviews          ModelPreviewReader
	assetPreviews          AssetPreviews
	assetGateway           SignedAssetGateway
	runtimeVersion         func() string
	archiveTempDir         string
	assetVerificationStats func() (hits, misses, verifiedBytes uint64)
	clock                  func() time.Time
	logger                 *slog.Logger
	mux                    *http.ServeMux
	providerAssetRoute     routing.One
	assetItemRoute         routing.One
	taskItemRoute          routing.One
	taskAssetRoute         routing.Two
	modelCaptureItemRoute  routing.One
}

func New(config Config) (*Server, error) {
	if config.Generation == nil {
		return nil, errors.New("httpapi: 缺少 Generation module")
	}
	if config.ProviderWebhooks == nil {
		config.ProviderWebhooks = config.Generation
	}
	if config.Identity == nil {
		return nil, errors.New("httpapi: 缺少 IdentityResolver")
	}
	if config.Authentication == nil {
		return nil, errors.New("httpapi: 缺少 Authentication service")
	}
	if config.Administration == nil {
		return nil, errors.New("httpapi: 缺少 Administration service")
	}
	if config.AdministrationAudit == nil {
		return nil, errors.New(
			"httpapi: 缺少 Administration audit service",
		)
	}
	if config.AdministrationGenerations == nil {
		return nil, errors.New(
			"httpapi: 缺少 Administration generation service",
		)
	}
	if config.AdministrationGenerationDownloads == nil {
		return nil, errors.New(
			"httpapi: 缺少 Administration generation download service",
		)
	}
	if config.AdministrationUsage == nil {
		return nil, errors.New(
			"httpapi: 缺少 Administration usage service",
		)
	}
	if config.AdministrationBalanceSummary == nil {
		return nil, errors.New(
			"httpapi: 缺少 Administration balance summary service",
		)
	}
	if config.AdministrationUserAssets == nil {
		return nil, errors.New(
			"httpapi: 缺少 Administration user assets module",
		)
	}
	if config.AdministrationBackfillStatus == nil {
		return nil, errors.New(
			"httpapi: 缺少 Administration backfill status module",
		)
	}
	if config.Cache == nil {
		return nil, errors.New("httpapi: 缺少 Cache service")
	}
	if config.Canvas == nil {
		return nil, errors.New("httpapi: 缺少 Canvas service")
	}
	if config.CanvasImages == nil {
		return nil, errors.New("httpapi: 缺少 Canvas image service")
	}
	if config.Chat == nil {
		return nil, errors.New("httpapi: 缺少 Chat service")
	}
	if config.ChatScheduler == nil {
		return nil, errors.New("httpapi: 缺少 ChatTurnScheduler")
	}
	if config.PromptEnhancer == nil {
		return nil, errors.New("httpapi: 缺少 PromptEnhancer module")
	}
	if config.ProviderAccounts == nil {
		return nil, errors.New("httpapi: 缺少 ProviderAccounts module")
	}
	if config.AssetProxy == nil {
		return nil, errors.New("httpapi: 缺少 AssetProxy module")
	}
	if config.LegacyGeneratedImages == nil {
		return nil, errors.New(
			"httpapi: 缺少 LegacyGeneratedImageStore",
		)
	}
	if err := catalog.ValidateApplicationSchema(config.Application); err != nil {
		return nil, fmt.Errorf("httpapi: Product Schema 无效: %w", err)
	}
	if err := catalog.ValidateSkillCatalog(config.SkillCatalog); err != nil {
		return nil, fmt.Errorf("httpapi: Skill Catalog Schema 无效: %w", err)
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.RuntimeVersion == nil {
		version := config.Application.Content.Service.Version
		config.RuntimeVersion = func() string { return version }
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	routes := config.Application.Runtime.HTTP.Routes
	providerAssetRoute, err := routing.ParseOne(
		routes.ProviderAsset,
		"token",
	)
	if err != nil {
		return nil, fmt.Errorf("httpapi: provider_asset route 无效: %w", err)
	}
	assetItemRoute, err := routing.ParseOne(routes.AssetItem, "assetID")
	if err != nil {
		return nil, fmt.Errorf("httpapi: asset_item route 无效: %w", err)
	}
	taskItemRoute, err := routing.ParseOne(routes.TaskItem, "taskID")
	if err != nil {
		return nil, fmt.Errorf("httpapi: task_item route 无效: %w", err)
	}
	var modelCaptureItemRoute routing.One
	if routes.ModelCaptureItem != "" {
		modelCaptureItemRoute, err = routing.ParseOne(routes.ModelCaptureItem, "captureID")
		if err != nil {
			return nil, fmt.Errorf("httpapi: model_capture_item route 无效: %w", err)
		}
	}
	taskAssetRoute, err := routing.ParseTwo(
		routes.TaskAsset,
		"taskID",
		"assetID",
	)
	if err != nil {
		return nil, fmt.Errorf("httpapi: task_asset route 无效: %w", err)
	}
	providerWebhookBindings, err := compileProviderWebhookBindings(
		config.Application.ProviderWebhooks,
		config.ProviderWebhookRuntime,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"httpapi: provider_webhooks 无效: %w",
			err,
		)
	}
	server := &Server{
		harness:                 config.Harness,
		readiness:               config.Readiness,
		generation:              config.Generation,
		executionGrants:         config.ExecutionGrants,
		processing:              config.Processing,
		modelDelivery:           config.ModelDelivery,
		lineage:                 config.Lineage,
		retry:                   config.Retry,
		captures:                config.Captures,
		skillRuns:               config.SkillRuns,
		pendingApprovals:        config.PendingApprovals,
		threeViews:              config.ThreeViews,
		scene:                   config.Scene,
		renderManifests:         config.RenderManifests,
		providerWebhooks:        config.ProviderWebhooks,
		providerWebhookBindings: providerWebhookBindings,
		loginLimiter: newLoginFailureLimiter(
			config.Application.Authentication.API.LoginMaxAttemptsPerMinute,
			time.Duration(config.Application.Authentication.API.LoginLockoutSeconds)*time.Second,
		).withLoginFailureStore(config.LoginFailureStore),
		identity:                  tracingIdentity{config.Identity},
		authentication:            config.Authentication,
		agentConfiguration:        config.AgentConfiguration,
		llmSettings:               config.LLMSettings,
		mcp:                       config.MCP,
		accountInputAssets:        config.AccountInputAssets,
		administration:            config.Administration,
		administrationAudit:       config.AdministrationAudit,
		administrationGenerations: config.AdministrationGenerations,
		administrationGenerationDownloads: config.
			AdministrationGenerationDownloads,
		administrationUsage: config.AdministrationUsage,
		administrationBalanceSummary: config.
			AdministrationBalanceSummary,
		administrationUserAssets: config.AdministrationUserAssets,
		administrationBackfillStatus: config.
			AdministrationBackfillStatus,
		cache:                 config.Cache,
		canvas:                config.Canvas,
		canvasImages:          config.CanvasImages,
		chat:                  config.Chat,
		chatScheduler:         config.ChatScheduler,
		chatContext:           config.ChatContext,
		ownsAgentSession:      config.OwnsAgentSession,
		promptEnhancer:        config.PromptEnhancer,
		providerAccounts:      config.ProviderAccounts,
		assetProxy:            config.AssetProxy,
		legacyGeneratedImages: config.LegacyGeneratedImages,
		application:           config.Application,
		taskEventWhitelist: config.Application.Runtime.TaskEvents.
			PublicEventWhitelist(),
		skillCatalog:           config.SkillCatalog,
		inputAssets:            config.InputAssets,
		modelPreviews:          config.ModelPreviews,
		assetPreviews:          config.AssetPreviews,
		assetGateway:           config.AssetGateway,
		runtimeVersion:         config.RuntimeVersion,
		archiveTempDir:         strings.TrimSpace(config.ArchiveTempDir),
		assetVerificationStats: config.AssetVerificationStats,
		clock:                  config.Clock,
		logger:                 config.Logger,
		trustedProxies:         append([]netip.Prefix(nil), config.TrustedProxies...),
		mux:                    http.NewServeMux(),
		agentParams:            agentParamsOrNew(config.AgentParams),
		agentState:             agentParamsOrNew(config.AgentState),
		agentOperations:        config.AgentOperations,
		providerAssetRoute:     providerAssetRoute,
		assetItemRoute:         assetItemRoute,
		taskItemRoute:          taskItemRoute,
		taskAssetRoute:         taskAssetRoute,
		modelCaptureItemRoute:  modelCaptureItemRoute,
	}
	publicSchemas, err := catalog.NewLocalizedPublicSchemas(config.Application)
	if err != nil {
		return nil, fmt.Errorf("httpapi: 公开 Schema 投影: %w", err)
	}
	server.publicSchemas = publicSchemas
	if server.texts, err = catalog.NewTextLocalizer(config.Application); err != nil {
		return nil, fmt.Errorf("httpapi: 界面语言文案: %w", err)
	}
	server.routes()
	return server, nil
}

func compileProviderWebhookBindings(
	schemas []catalog.ProviderWebhookSchema,
	runtime map[string]ProviderWebhookRuntime,
) ([]providerWebhookBinding, error) {
	result := make([]providerWebhookBinding, 0, len(schemas))
	for _, schema := range schemas {
		route, err := routing.ParseOne(schema.Route, schema.PathParameter)
		if err != nil {
			return nil, fmt.Errorf("%s route 无效: %w", schema.ID, err)
		}
		values := runtime[schema.ID]
		result = append(result, providerWebhookBinding{
			schema:     schema,
			route:      route,
			pathSecret: strings.TrimSpace(values.PathSecret),
			key:        strings.TrimSpace(values.Key),
		})
	}
	return result, nil
}

func (s *Server) Handler() http.Handler {
	limit := s.application.Runtime.HTTP.APIRateLimit
	// requestLogMiddleware 必须包住 recoverMiddleware，panic 恢复写出的 500 才能流经 statusRecorder 被记录。
	// 安全响应头（SEC-04）与状态变更同站校验（SEC-03）包在最外层。
	// 访问日志在最外层（#233）：CSRF 拒绝等所有 4xx/5xx 都带请求 ID 记录。
	// 客户端地址先于限流解析（#256），可信代理之外的 X-Forwarded-For 一律忽略。
	return requestLogMiddleware(
		s.logger,
		s.providerAssetRoute,
		s.providerWebhookSecretRoutes(),
		localeTraceMiddleware(s.application, s.texts, clientAddressMiddleware(
			s.trustedProxies,
			s.securityHeadersMiddleware(
				apiRateLimitMiddleware(
					&s.apiRequests,
					limit.PathPrefix,
					limit.RequestsPerMinute,
					s.message("error.rate_limited"),
					s.sameOriginCSRFMiddleware(
						recoverMiddleware(s.logger, s.message("error.internal"), routeTraceMiddleware(s.mux)),
					),
				),
			),
		)),
	)
}

// providerWebhookSecretRoutes 返回带路径 secret 的 Provider Webhook 路由，
// 请求日志必须把其中的 secret 段脱敏，防止读到日志的人伪造回调。
func (s *Server) providerWebhookSecretRoutes() []routing.One {
	routes := make([]routing.One, 0, len(s.providerWebhookBindings))
	for _, binding := range s.providerWebhookBindings {
		if binding.schema.PathSecretSetting == "" {
			continue
		}
		routes = append(routes, binding.route)
	}
	return routes
}

func (s *Server) routes() {
	routes := s.application.Runtime.HTTP.Routes
	legacyRoutes := s.application.Runtime.HTTP.LegacyRoutes
	s.mux.HandleFunc("GET "+routes.Home, s.home)
	s.mux.HandleFunc("GET "+routes.Liveness, s.health)
	s.mux.HandleFunc("GET "+routes.Readiness, s.ready)
	s.mux.HandleFunc("GET "+legacyRoutes.Health, s.legacyHealth)
	s.mux.HandleFunc("GET "+legacyRoutes.Version, s.legacyVersion)
	s.mux.HandleFunc("POST "+legacyRoutes.AuthLogin, s.legacyLogin)
	s.mux.HandleFunc("POST "+legacyRoutes.AuthMock, s.legacyMockAuth)
	s.mux.HandleFunc("GET "+legacyRoutes.AuthSession, s.legacySession)
	s.mux.HandleFunc("DELETE "+legacyRoutes.AuthSession, s.legacyLogout)
	s.mux.HandleFunc("POST "+legacyRoutes.AuthPassword, s.changeOwnPassword)
	s.mux.HandleFunc("GET "+routes.AgentParams, s.withIdentity(s.agentParamsGet))
	s.mux.HandleFunc("PUT "+routes.AgentParams, s.withIdentity(s.agentParamsPut))
	s.mux.HandleFunc("PUT "+routes.AgentBind, s.withIdentity(s.agentBindPut))
	s.mux.HandleFunc("GET "+routes.AgentConsent, s.withIdentity(s.agentConsentGet))
	s.mux.HandleFunc("PUT "+routes.AgentConsent, s.withIdentity(s.agentConsentPut))
	s.mux.HandleFunc(
		"GET "+legacyRoutes.CacheStatus,
		s.withLegacyIdentity(s.legacyCacheStatus),
	)
	s.mux.HandleFunc(
		"GET "+legacyRoutes.GeneratedImage,
		s.withLegacyAssetPrincipal(s.legacyGeneratedImage),
	)
	s.registerLegacyChatContracts(legacyRoutes)
	s.registerLegacyCanvasContracts()
	s.mux.HandleFunc(
		"GET "+legacyRoutes.MeshyProxyAsset,
		s.withPrincipal(s.legacyAssetProxy),
	)
	s.registerAgentConfigurationContracts()
	s.registerLLMSettingsContracts()
	s.registerMCPContract()
	s.registerAccountResourcesContracts()
	s.registerAdministrationUserContracts()
	s.registerAdministrationAuditLogContracts()
	s.registerAdministrationGenerationContracts()
	s.registerAdministrationUsageContract()
	s.registerAdministrationUserAssetsContract()
	s.registerAdministrationBackfillStatusContract()
	s.mux.HandleFunc(
		"GET "+legacyRoutes.TaskStream,
		s.withPrincipal(s.legacyTaskStream),
	)
	s.mux.HandleFunc(
		"POST "+legacyRoutes.PromptEnhance,
		s.withPromptEnhanceIdentity(s.legacyPromptEnhance),
	)
	s.mux.HandleFunc(
		"GET "+legacyRoutes.ProviderBalance,
		s.withProviderBalanceIdentity(s.legacyProviderBalance),
	)
	s.mux.HandleFunc("GET "+routes.ProductSchema, s.productSchema)
	s.mux.HandleFunc("GET "+routes.ChatSkills, s.skillCatalogSchema)
	if routes.ClientErrors != "" {
		s.mux.HandleFunc("POST "+routes.ClientErrors, s.reportClientError)
	}
	if routes.ChatSessionApproval != "" {
		s.mux.HandleFunc("GET "+routes.ChatSessionApproval, s.withIdentity(s.getChatSessionApproval))
		s.mux.HandleFunc("PUT "+routes.ChatSessionApproval, s.withPrincipal(s.putChatSessionApproval))
	}
	if routes.ChatSessionNotes != "" {
		s.mux.HandleFunc("GET "+routes.ChatSessionNotes, s.withIdentity(s.getChatSessionNotes))
		s.mux.HandleFunc("PUT "+routes.ChatSessionNotes, s.withIdentity(s.putChatSessionNotes))
	}
	if s.lineage != nil && routes.ChatSessionLineage != "" {
		s.mux.HandleFunc("GET "+routes.ChatSessionLineage, s.withIdentity(s.getChatSessionLineage))
	}
	if s.pendingApprovals != nil && routes.ChatSessionPendingApprovals != "" {
		s.mux.HandleFunc("GET "+routes.ChatSessionPendingApprovals, s.withIdentity(s.listPendingApprovals))
	}
	if s.threeViews != nil && routes.ChatSessionThreeViews != "" && routes.ChatSessionThreeViewItem != "" {
		s.mux.HandleFunc("GET "+routes.ChatSessionThreeViews, s.withIdentity(s.listThreeViews))
		s.mux.HandleFunc("POST "+routes.ChatSessionThreeViewItem, s.withIdentity(s.advanceThreeView))
	}
	if routes.ChatSessionHarness != "" {
		s.mux.HandleFunc("GET "+routes.ChatSessionHarness, s.withIdentity(s.getChatSessionHarness))
		s.mux.HandleFunc("PUT "+routes.ChatSessionHarness, s.withPrincipal(s.putChatSessionHarness))
	}
	if routes.ChatSessionAgentScope != "" {
		s.mux.HandleFunc("GET "+routes.ChatSessionAgentScope, s.withIdentity(s.getChatSessionAgentScope))
		s.mux.HandleFunc("PUT "+routes.ChatSessionAgentScope, s.withPrincipal(s.putChatSessionAgentScope))
	}
	if routes.ChatSessionReasoning != "" {
		s.mux.HandleFunc("GET "+routes.ChatSessionReasoning, s.withIdentity(s.getChatSessionReasoning))
		s.mux.HandleFunc("PUT "+routes.ChatSessionReasoning, s.withPrincipal(s.putChatSessionReasoning))
	}
	if routes.ChatSessionContext != "" {
		s.mux.HandleFunc("GET "+routes.ChatSessionContext, s.withIdentity(s.getChatSessionContext))
	}
	if routes.ChatSessionModel != "" {
		s.mux.HandleFunc("GET "+routes.ChatSessionModel, s.withIdentity(s.getChatSessionModel))
		s.mux.HandleFunc("PUT "+routes.ChatSessionModel, s.withPrincipal(s.putChatSessionModel))
	}
	if routes.ChatSessionContextCompaction != "" {
		s.mux.HandleFunc("POST "+routes.ChatSessionContextCompaction, s.withPrincipal(s.postChatSessionContextCompaction))
	}
	if routes.ChatSessionChoices != "" {
		s.mux.HandleFunc("GET "+routes.ChatSessionChoices, s.withIdentity(s.listChatSessionChoices))
	}
	if routes.ChatSessionPick != "" {
		s.mux.HandleFunc("GET "+routes.ChatSessionPick, s.withIdentity(s.getChatSessionPick))
		s.mux.HandleFunc("PUT "+routes.ChatSessionPick, s.withIdentity(s.putChatSessionPick))
		s.mux.HandleFunc("DELETE "+routes.ChatSessionPick, s.withIdentity(s.deleteChatSessionPick))
	}
	s.mux.HandleFunc(
		"GET "+legacyRoutes.Providers,
		s.withIdentity(s.legacyProviderDiscovery),
	)
	s.mux.HandleFunc(
		"GET "+legacyRoutes.Tasks,
		s.withPrincipal(s.legacyTaskHistory),
	)
	s.mux.HandleFunc(
		"POST "+legacyRoutes.AssetUpload,
		s.withIdentity(s.legacyUploadAsset),
	)
	s.mux.HandleFunc(
		"GET "+legacyRoutes.AssetItem,
		s.withPrincipal(s.legacyGetAsset),
	)
	for _, capability := range s.application.Capabilities {
		if capability.LegacyAPI == nil {
			continue
		}
		s.registerLegacyTaskContract(capability)
	}
	s.mux.HandleFunc("GET "+routes.ProviderAsset, s.openProviderAsset)
	s.mux.HandleFunc("GET "+routes.Offerings, s.withIdentity(s.listOfferings))
	s.mux.HandleFunc("POST "+routes.Assets, s.withIdentity(s.uploadAsset))
	s.mux.HandleFunc("GET "+routes.AssetItem, s.withIdentity(s.downloadInputAsset))
	if routes.AssetModelPreview != "" {
		s.mux.HandleFunc("GET "+routes.AssetModelPreview, s.withIdentity(s.previewInputModel))
	}
	if s.captures != nil && routes.ModelCaptures != "" && routes.ModelCaptureItem != "" {
		s.mux.HandleFunc("POST "+routes.ModelCaptures, s.withIdentity(s.recordModelCapture))
		s.mux.HandleFunc("GET "+routes.ModelCaptureItem, s.withIdentity(s.getModelCapture))
	}
	if s.skillRuns != nil && routes.SkillRuns != "" && routes.SkillRunItem != "" &&
		routes.SkillRunStep != "" && routes.SkillRunReview != "" {
		s.mux.HandleFunc("POST "+routes.SkillRuns, s.withIdentity(s.createSkillRun))
		s.mux.HandleFunc("GET "+routes.SkillRuns, s.withIdentity(s.listSkillRuns))
		s.mux.HandleFunc("GET "+routes.SkillRunItem, s.withIdentity(s.getSkillRun))
		s.mux.HandleFunc("POST "+routes.SkillRunStep, s.withIdentity(s.advanceSkillStep))
		s.mux.HandleFunc("POST "+routes.SkillRunReview, s.withIdentity(s.reviewSkillStep))
	}
	if s.scene != nil && s.renderManifests != nil && routes.SceneObjects != "" &&
		routes.SceneObjectItem != "" && routes.SceneObjectRepresentations != "" &&
		routes.SceneObjectApply != "" && routes.SceneSnapshot != "" &&
		routes.RenderManifests != "" && routes.RenderManifestItem != "" {
		s.mux.HandleFunc("POST "+routes.SceneObjects, s.withIdentity(s.createSceneObject))
		s.mux.HandleFunc("GET "+routes.SceneSnapshot, s.withIdentity(s.getSceneSnapshot))
		s.mux.HandleFunc("PATCH "+routes.SceneObjectItem, s.withIdentity(s.moveSceneObject))
		s.mux.HandleFunc("DELETE "+routes.SceneObjectItem, s.withIdentity(s.deleteSceneObject))
		s.mux.HandleFunc("POST "+routes.SceneObjectApply, s.withIdentity(s.applySceneCandidate))
		s.mux.HandleFunc("GET "+routes.SceneObjectRepresentations, s.withIdentity(s.listSceneRepresentations))
		s.mux.HandleFunc("POST "+routes.RenderManifests, s.withIdentity(s.createRenderManifest))
		s.mux.HandleFunc("GET "+routes.RenderManifestItem, s.withIdentity(s.getRenderManifest))
	}
	s.mux.HandleFunc("POST "+routes.Tasks, s.withIdentity(s.submitTask))
	if s.retry != nil && routes.TaskRetry != "" {
		s.mux.HandleFunc("POST "+routes.TaskRetry, s.withIdentity(s.retryTask))
	}
	if routes.TaskPrepare != "" {
		s.mux.HandleFunc("POST "+routes.TaskPrepare, s.withIdentity(s.prepareTask))
	}
	if routes.TaskGrants != "" {
		s.mux.HandleFunc("POST "+routes.TaskGrants, s.withIdentity(s.requestTaskGrant))
	}
	if routes.TaskGrantDecision != "" {
		s.mux.HandleFunc("PUT "+routes.TaskGrantDecision, s.withIdentity(s.decideTaskGrant))
	}
	s.mux.HandleFunc("GET "+routes.Tasks, s.withPrincipal(s.listTasks))
	if s.processing != nil && routes.ProcessingPlans != "" {
		s.mux.HandleFunc("POST "+routes.ProcessingPlans, s.withIdentity(s.createProcessingPlan))
		s.mux.HandleFunc("GET "+routes.ProcessingPlans, s.withIdentity(s.listProcessingPlans))
		s.mux.HandleFunc("GET "+routes.ProcessingPlanItem, s.withIdentity(s.getProcessingPlan))
		s.mux.HandleFunc("POST "+routes.ProcessingPlanRetry, s.withIdentity(s.retryProcessingPlan))
		s.mux.HandleFunc("POST "+routes.ProcessingPlanResume, s.withIdentity(s.resumeProcessingPlan))
		if routes.ProcessingPlanReconcile != "" {
			s.mux.HandleFunc("POST "+routes.ProcessingPlanReconcile, s.withIdentity(s.reconcileProcessingPlan))
		}
	}
	if s.modelDelivery != nil && routes.ModelFormats != "" && routes.ModelFormatsComplete != "" && routes.ModelArchive != "" {
		s.mux.HandleFunc("GET "+routes.ModelFormats, s.withPrincipal(s.getModelFormats))
		s.mux.HandleFunc("POST "+routes.ModelFormatsComplete, s.withIdentity(s.completeModelFormats))
		s.mux.HandleFunc("GET "+routes.ModelArchive, s.withPrincipal(s.downloadModelArchive))
	}
	if routes.TaskRecovery != "" && routes.TaskRecoveryPrepare != "" {
		s.mux.HandleFunc("GET "+routes.TaskRecovery, s.withIdentity(s.getTaskRecovery))
		s.mux.HandleFunc("POST "+routes.TaskRecoveryPrepare, s.withIdentity(s.prepareTaskRecovery))
	}
	if routes.TaskRefetch != "" {
		s.mux.HandleFunc("GET "+routes.TaskRefetch, s.withIdentity(s.getTaskRefetch))
		s.mux.HandleFunc("POST "+routes.TaskRefetch, s.withIdentity(s.startTaskRefetch))
	}
	s.mux.HandleFunc("GET "+routes.TaskItem, s.withPrincipal(s.getTask))
	s.mux.HandleFunc("POST "+routes.TaskCancel, s.withIdentity(s.cancelTask))
	s.mux.HandleFunc("GET "+routes.TaskEvents, s.withPrincipal(s.listEvents))
	s.mux.HandleFunc(
		"GET "+routes.TaskAsset,
		s.withPrincipal(s.downloadAsset),
	)
	// 根路径 Provider 回调不能注册成动态 ServeMux 模板，否则会与
	// Schema 可配置的业务动态路由形成歧义。方法级 catch-all 只做
	// 最终精确匹配；其他未知路径仍返回标准 404。
	s.mux.HandleFunc("GET /", s.providerWebhookStatus)
	s.mux.HandleFunc("POST /", s.providerWebhookReceive)
}

type AssetProxy interface {
	Get(
		context.Context,
		assetdelivery.Request,
	) (assetdelivery.Result, error)
}

func (s *Server) registerLegacyTaskContract(
	capability catalog.CapabilityPresentation,
) {
	basePath := capability.LegacyAPI.Path
	s.mux.HandleFunc(
		"POST "+basePath,
		s.withIdentity(func(
			writer http.ResponseWriter,
			request *http.Request,
			owner generation.OwnerID,
		) {
			s.legacySubmitTask(writer, request, owner, capability)
		}),
	)
	s.mux.HandleFunc(
		"GET "+basePath+"/{taskID}",
		s.withPrincipal(func(
			writer http.ResponseWriter,
			request *http.Request,
			principal access.Principal,
		) {
			s.legacyGetTask(writer, request, principal, capability)
		}),
	)
	s.mux.HandleFunc(
		"DELETE "+basePath+"/{taskID}",
		s.withPrincipal(func(
			writer http.ResponseWriter,
			request *http.Request,
			principal access.Principal,
		) {
			s.legacyDeleteTask(writer, request, principal, capability)
		}),
	)
	if capability.LegacyAPI.AssetDownload != nil {
		download := *capability.LegacyAPI.AssetDownload
		s.mux.HandleFunc(
			"GET "+download.Path,
			s.withPrincipal(func(
				writer http.ResponseWriter,
				request *http.Request,
				principal access.Principal,
			) {
				s.legacyDownloadAsset(writer, request, principal, download)
			}),
		)
	}
}

type InputAssetUploader interface {
	UploadAsset(
		context.Context,
		generation.OwnerID,
		generation.InputAssetUpload,
	) (generation.InputAsset, error)
	OpenAuthorized(
		context.Context,
		generation.OwnerID,
		generation.AssetID,
		bool,
	) (generation.InputAsset, io.ReadCloser, error)
}

type SignedAssetGateway interface {
	OpenSigned(
		context.Context,
		string,
	) (generation.InputAsset, io.ReadCloser, error)
}

type LegacyGeneratedImageStore interface {
	Open(context.Context, string) (io.ReadCloser, int64, error)
}

type identityHandler func(http.ResponseWriter, *http.Request, generation.OwnerID)
type principalHandler func(http.ResponseWriter, *http.Request, access.Principal)

func (s *Server) withIdentity(next identityHandler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		principal, err := s.identity.Identify(request)
		if err != nil {
			detail := s.application.Authentication.Messages.Unauthenticated
			writeProblem(writer, http.StatusUnauthorized, "unauthenticated", detail)
			return
		}
		next(writer, request, generation.OwnerID(principal.ID()))
	}
}

func (s *Server) withPrincipal(next principalHandler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		principal, err := s.identity.Identify(request)
		if err != nil {
			detail := s.application.Authentication.Messages.Unauthenticated
			writeProblem(writer, http.StatusUnauthorized, "unauthenticated", detail)
			return
		}
		next(writer, request, principal)
	}
}

// taskReadScope 与 legacyTaskHistory 一致：tasks.read.any 持有者（管理员）
// 跨 owner 读取，否则限制为自己的任务。列表与单任务读路径必须共用同一
// 权限口径，避免历史上“列表可见、详情 404”的不对称。
func taskReadScope(principal access.Principal) generation.TaskListScope {
	if principal.Can(access.PermissionTasksReadAny) {
		return generation.AllTasks()
	}
	return generation.OwnTasks(generation.OwnerID(principal.ID()))
}

// withLegacyAssetPrincipal 与 withLegacyIdentity 共用同一认证错误形状，
// 但保留完整 Principal 以便读路径按权限选择 scope。
func (s *Server) withLegacyAssetPrincipal(next principalHandler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		principal, err := s.identity.Identify(request)
		if err != nil {
			s.writeAuthenticationError(
				writer,
				http.StatusUnauthorized,
				s.application.Authentication.Messages.Unauthenticated,
			)
			return
		}
		next(writer, request, principal)
	}
}

func (s *Server) withLegacyIdentity(next identityHandler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		principal, err := s.identity.Identify(request)
		if err != nil {
			s.writeAuthenticationError(
				writer,
				http.StatusUnauthorized,
				s.application.Authentication.Messages.Unauthenticated,
			)
			return
		}
		next(writer, request, generation.OwnerID(principal.ID()))
	}
}

func (s *Server) home(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set(
		"Cache-Control",
		s.application.Runtime.HTTP.PrivateCacheControl,
	)
	if err := homeDocument.Execute(writer, s.application.Content.Home); err != nil {
		s.logger.Error("渲染 Go 运行时根页失败", "error", err)
	}
}

func (s *Server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": s.application.Content.Service.ID,
	})
}

func (s *Server) legacyHealth(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{
		"status":    "ok",
		"service":   s.application.Content.Service.Name,
		"version":   s.application.Content.Service.Version,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (s *Server) legacyVersion(writer http.ResponseWriter, _ *http.Request) {
	version := strings.TrimSpace(s.runtimeVersion())
	if version == "" {
		version = s.application.Content.Service.Version
	}
	writer.Header().Set("X-Art-AI-Runtime", "go")
	writeJSON(writer, http.StatusOK, map[string]string{
		"version": version,
	})
}

func (s *Server) productSchema(
	writer http.ResponseWriter,
	request *http.Request,
) {
	// ETag 取自公开投影的实际内容，而不是手写的 revision 字符串。
	// 站点曾因此长期读到过期 Schema：改了 content.navigation 却没动 revision，
	// 服务端对旧 If-None-Match 回 304，浏览器继续用部署前的 13 项导航。
	// 由内容派生可让任何 Schema 变更都必然换 ETag，不再依赖人记得改版本号。
	// 每种界面语言是一份独立投影，ETag 也各不相同，换语言不会被 304 留在旧语言。
	locale := httplocale.FromRequest(s.application, request)
	projection := s.publicSchemas.For(locale)
	writer.Header().Set("ETag", projection.ETag)
	writer.Header().Set(
		"Cache-Control",
		s.application.Runtime.HTTP.SchemaCacheControl,
	)
	httplocale.MarkResponse(writer.Header(), projection.Locale)
	if request.Header.Get("If-None-Match") == projection.ETag {
		writer.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]json.RawMessage{
		"schema": projection.JSON,
	})
}

func (s *Server) skillCatalogSchema(
	writer http.ResponseWriter,
	request *http.Request,
) {
	principal, err := s.identity.Identify(request)
	if err != nil {
		writeProblem(writer, 401, "unauthenticated", s.application.Authentication.Messages.Unauthenticated)
		return
	}
	skillCatalog, err := s.effectiveSkillCatalog(request, principal.ID())
	if err != nil {
		writeProblem(writer, 500, "configuration_unavailable", s.application.AgentConfiguration.UI["load_failed"], err)
		return
	}
	etag := `"` + skillCatalog.Revision + `"`
	writer.Header().Set("ETag", etag)
	writer.Header().Set("Cache-Control", "private, no-store")
	if request.Header.Get("If-None-Match") == etag {
		writer.WriteHeader(http.StatusNotModified)
		return
	}

	fields := s.application.Chat.Skills.Fields
	skills := make([]map[string]any, 0, len(skillCatalog.Skills))
	for _, skill := range skillCatalog.Skills {
		projected := map[string]any{
			fields.ID:              skill.ID,
			fields.Name:            skill.Name,
			fields.Category:        skill.Category,
			fields.RecommendedSize: skill.RecommendedSize,
			fields.ThumbnailPath:   skill.ThumbnailPath,
			fields.RawImageURL:     skill.RawImageURL,
			fields.Prompt:          skill.Prompt,
		}
		if skill.NameZH != "" {
			projected[fields.NameZH] = skill.NameZH
		}
		if skill.ImageToImageTemplate != "" {
			projected[fields.ImageToImageTemplate] =
				skill.ImageToImageTemplate
		}
		if len(skill.Tags) > 0 {
			projected[fields.Tags] = append([]string(nil), skill.Tags...)
		}
		if skill.Tool != "" {
			projected[fields.Tool] = skill.Tool
			projected[fields.RequiresImage] = skill.RequiresImage
			projected[fields.DefaultMessage] = skill.DefaultMessage
			projected[fields.OneShot] = skill.OneShot
		}
		skills = append(skills, projected)
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		fields.Revision: skillCatalog.Revision,
		fields.Skills:   skills,
	})
}

func (s *Server) legacySubmitTask(
	writer http.ResponseWriter,
	_ *http.Request,
	_ generation.OwnerID,
	_ catalog.CapabilityPresentation,
) {
	// Legacy forms do not carry a durable session or execution-grant identity.
	// A bearer token or session cookie alone cannot authorize a paid operation.
	s.writeLegacyError(writer, http.StatusForbidden, "error.execution_grant_required")
}

func (s *Server) legacyMultipartTaskInput(
	writer http.ResponseWriter,
	request *http.Request,
	owner generation.OwnerID,
	capability catalog.CapabilityPresentation,
) (map[string]any, bool) {
	protocol := capability.LegacyAPI.Multipart
	if s.inputAssets == nil {
		s.writeLegacyError(
			writer,
			http.StatusServiceUnavailable,
			"error.input_asset_service_unavailable",
		)
		return nil, false
	}
	request.Body = http.MaxBytesReader(
		writer,
		request.Body,
		s.application.Runtime.HTTP.MultipartBodyMaxBytes,
	)
	if err := request.ParseMultipartForm(
		s.application.Runtime.InputAssets.UploadMaxBytes,
	); err != nil {
		s.writeLegacyError(
			writer,
			http.StatusBadRequest,
			"error.invalid_upload",
		)
		return nil, false
	}
	if request.MultipartForm != nil {
		defer request.MultipartForm.RemoveAll()
	}

	input := make(map[string]any)
	params := strings.TrimSpace(request.FormValue(protocol.ParamsField))
	if params != "" {
		if err := json.Unmarshal([]byte(params), &input); err != nil ||
			input == nil {
			s.writeLegacyError(
				writer,
				http.StatusBadRequest,
				protocol.InvalidParamsMessageKey,
			)
			return nil, false
		}
	}
	modelID, _ := input[capability.LegacyAPI.ModelParameter].(string)
	offeringID, err := s.legacyOfferingID(capability.ID, modelID)
	if err != nil {
		s.writeLegacyDomainError(writer, err)
		return nil, false
	}
	offering, exists := s.generation.Offering(offeringID)
	if !exists {
		s.writeLegacyDomainError(writer, &generation.Error{
			Code:      generation.ErrUnavailable,
			Operation: "legacy_multipart_offering",
		})
		return nil, false
	}
	if !catalog.LegacyOfferingApplies(protocol.OfferingIDs, offering.ID) {
		s.writeLegacyDomainError(writer, unsupportedLegacyInput())
		return nil, false
	}
	target, exists := parameterByName(
		offering.Parameters,
		protocol.TargetParameter,
	)
	targetType := "asset"
	if protocol.Cardinality == "many" {
		targetType = "asset-array"
	}
	if !exists || target.Type != targetType {
		s.writeLegacyDomainError(writer, &generation.Error{
			Code:      generation.ErrUnavailable,
			Operation: "legacy_multipart_target",
		})
		return nil, false
	}

	var headers []*multipart.FileHeader
	if request.MultipartForm != nil {
		headers = request.MultipartForm.File[protocol.FileField]
	}
	if len(headers) == 0 {
		s.writeTemplatedLegacyError(
			writer,
			http.StatusBadRequest,
			protocol.MissingFileMessageKey,
			map[string]string{"field": protocol.FileField},
		)
		return nil, false
	}
	if protocol.Cardinality == "one" && len(headers) != 1 {
		s.writeLegacyError(
			writer,
			http.StatusBadRequest,
			"error.invalid_upload",
		)
		return nil, false
	}
	if protocol.Cardinality == "many" &&
		(len(headers) < target.MinItems ||
			(target.MaxItems > 0 && len(headers) > target.MaxItems)) {
		s.writeTemplatedLegacyError(
			writer,
			http.StatusBadRequest,
			protocol.InvalidCountMessageKey,
			map[string]string{
				"minimum": strconv.Itoa(target.MinItems),
				"maximum": strconv.Itoa(target.MaxItems),
			},
		)
		return nil, false
	}

	assetIDs := make([]generation.AssetID, 0, len(headers))
	for _, header := range headers {
		assetID, uploaded := s.uploadLegacyMultipartAsset(
			writer,
			request,
			owner,
			target,
			protocol,
			header,
		)
		if !uploaded {
			return nil, false
		}
		assetIDs = append(assetIDs, assetID)
	}
	if protocol.Cardinality == "many" {
		input[target.Name] = assetIDs
	} else {
		input[target.Name] = assetIDs[0]
	}
	return input, true
}

func (s *Server) uploadLegacyMultipartAsset(
	writer http.ResponseWriter,
	request *http.Request,
	owner generation.OwnerID,
	target generation.Parameter,
	protocol *catalog.LegacyMultipartTaskSchema,
	header *multipart.FileHeader,
) (generation.AssetID, bool) {
	extension := normalizedFileExtension(header.Filename)
	if !extensionAllowed(target.Extensions, extension) {
		s.writeTemplatedLegacyError(
			writer,
			http.StatusBadRequest,
			protocol.InvalidExtensionMessageKey,
			map[string]string{
				"extension": extension,
				"extensions": strings.Join(
					normalizedExtensions(target.Extensions),
					", ",
				),
			},
		)
		return "", false
	}
	file, err := header.Open()
	if err != nil {
		s.writeLegacyError(
			writer,
			http.StatusBadRequest,
			"error.invalid_upload",
		)
		return "", false
	}
	defer file.Close()
	mediaType := strings.TrimSpace(header.Header.Get("Content-Type"))
	if mediaType == "" {
		mediaType = s.application.Runtime.InputAssets.DefaultMediaType
	}
	if !s.uploadMediaTypeAllowed(mediaType) {
		s.writeLegacyError(
			writer,
			http.StatusBadRequest,
			"error.input_media_type_not_allowed",
		)
		return "", false
	}
	asset, err := s.inputAssets.UploadAsset(
		request.Context(),
		owner,
		generation.InputAssetUpload{
			Body:             file,
			OriginalFilename: header.Filename,
			AssetType:        s.inputAssetType(mediaType, header.Filename),
			MediaType:        mediaType,
			MaxBytes:         target.MaxBytes,
		},
	)
	if err != nil {
		s.writeLegacyDomainError(writer, err)
		return "", false
	}
	return asset.ID, true
}

func parameterByName(
	parameters []generation.Parameter,
	name string,
) (generation.Parameter, bool) {
	for _, parameter := range parameters {
		if parameter.Name == name {
			return parameter, true
		}
	}
	return generation.Parameter{}, false
}

func normalizedFileExtension(filename string) string {
	return strings.ToLower(filepath.Ext(strings.TrimSpace(filename)))
}

func normalizedExtensions(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" {
			continue
		}
		if !strings.HasPrefix(normalized, ".") {
			normalized = "." + normalized
		}
		result = append(result, normalized)
	}
	return result
}

func extensionAllowed(configured []string, extension string) bool {
	if len(configured) == 0 {
		return true
	}
	for _, allowed := range normalizedExtensions(configured) {
		if extension == allowed {
			return true
		}
	}
	return false
}

func applyLegacyFieldForwarding(
	input map[string]any,
	offering generation.Offering,
	fields []catalog.LegacyFieldSchema,
) error {
	// Validate before rewriting so a rejected request keeps its original input.
	for _, field := range fields {
		if _, present := input[field.Name]; !present {
			continue
		}
		target := field.TargetParameter
		if target == "" {
			target = field.Name
		}
		if !catalog.LegacyOfferingApplies(field.OfferingIDs, offering.ID) {
			return unsupportedLegacyInput()
		}
		if _, exists := parameterByName(offering.Parameters, target); !exists {
			return unsupportedLegacyInput()
		}
		if target != field.Name {
			if _, present := input[target]; present {
				return unsupportedLegacyInput()
			}
		}
	}
	for _, field := range fields {
		target := field.TargetParameter
		if target == "" || target == field.Name {
			continue
		}
		if value, present := input[field.Name]; present {
			input[target] = value
			delete(input, field.Name)
		}
	}
	return nil
}

func unsupportedLegacyInput() error {
	return &generation.Error{Code: generation.ErrInvalidInput, Operation: "legacy_input_not_applicable"}
}

func restoreLegacyFields(
	response map[string]any,
	fields []catalog.LegacyFieldSchema,
	offeringID generation.OfferingID,
) {
	for _, field := range fields {
		if !field.Restore || field.TargetParameter == "" || !catalog.LegacyOfferingApplies(field.OfferingIDs, offeringID) {
			continue
		}
		value, exists := response[field.TargetParameter]
		if !exists {
			continue
		}
		response[field.Name] = value
		delete(response, field.TargetParameter)
	}
}

func legacyValuePresent(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		return len(typed) > 0
	case nil:
		return false
	default:
		return strings.TrimSpace(fmt.Sprint(typed)) != ""
	}
}

type legacyInlineAsset struct {
	mediaType string
	content   []byte
}

func (s *Server) persistLegacyInlineAssets(
	ctx context.Context,
	owner generation.OwnerID,
	input map[string]any,
	offering generation.Offering,
	aliases []catalog.LegacyAssetAliasSchema,
) error {
	applicable := make([]catalog.LegacyAssetAliasSchema, 0, len(aliases))
	for _, alias := range aliases {
		if catalog.LegacyOfferingApplies(alias.OfferingIDs, offering.ID) {
			applicable = append(applicable, alias)
			continue
		}
		if _, provided := input[alias.Name]; provided {
			return unsupportedLegacyInput()
		}
	}
	aliases = applicable
	if len(aliases) == 0 {
		return nil
	}
	if s.inputAssets == nil {
		return &generation.Error{
			Code:      generation.ErrUnavailable,
			Operation: "persist_legacy_inline_assets",
		}
	}
	sources := make([]string, 0)
	seen := make(map[string]struct{})
	var target generation.Parameter
	targetFound := false
	for _, alias := range aliases {
		if !targetFound {
			target, targetFound = parameterBySemantic(offering, alias.Semantic)
		}
		raw, exists := input[alias.Name]
		if !exists {
			continue
		}
		var values []string
		switch typed := raw.(type) {
		case string:
			values = []string{typed}
		case []any:
			values = make([]string, 0, len(typed))
			for _, item := range typed {
				text, ok := item.(string)
				if !ok {
					return invalidLegacyInlineAsset()
				}
				values = append(values, text)
			}
		default:
			return invalidLegacyInlineAsset()
		}
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				return invalidLegacyInlineAsset()
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			sources = append(sources, value)
		}
	}
	if len(sources) == 0 {
		return nil
	}
	if !targetFound {
		return &generation.Error{
			Code:      generation.ErrInvalidInput,
			Operation: "persist_legacy_inline_assets",
		}
	}

	decoded := make([]legacyInlineAsset, 0, len(sources))
	for _, source := range sources {
		asset, err := decodeLegacyDataURI(source)
		if err != nil {
			return err
		}
		decoded = append(decoded, asset)
	}
	references := make([]generation.AssetID, 0, len(decoded))
	for _, candidate := range decoded {
		if !s.uploadMediaTypeAllowed(candidate.mediaType) {
			return &generation.Error{
				Code:      generation.ErrInvalidInput,
				Operation: "persist_legacy_inline_assets",
			}
		}
		asset, err := s.inputAssets.UploadAsset(
			ctx,
			owner,
			generation.InputAssetUpload{
				Body:      bytes.NewReader(candidate.content),
				AssetType: s.inputAssetType(candidate.mediaType, ""),
				MediaType: candidate.mediaType,
				MaxBytes:  target.MaxBytes,
			},
		)
		if err != nil {
			return err
		}
		references = append(references, asset.ID)
	}
	for _, alias := range aliases {
		delete(input, alias.Name)
	}
	switch target.Type {
	case "asset":
		input[target.Name] = references[0]
	default:
		input[target.Name] = references
	}
	return nil
}

func parameterBySemantic(
	offering generation.Offering,
	semantic generation.ParameterSemantic,
) (generation.Parameter, bool) {
	for _, parameter := range offering.Parameters {
		if parameter.Semantic == semantic {
			return parameter, true
		}
	}
	return generation.Parameter{}, false
}

func decodeLegacyDataURI(source string) (legacyInlineAsset, error) {
	metadata, payload, exists := strings.Cut(source, ",")
	if !exists || !strings.HasPrefix(metadata, "data:") {
		return legacyInlineAsset{}, invalidLegacyInlineAsset()
	}
	mediaType, encoding, exists := strings.Cut(strings.TrimPrefix(metadata, "data:"), ";")
	if !exists ||
		!strings.EqualFold(strings.TrimSpace(encoding), "base64") ||
		strings.TrimSpace(mediaType) == "" {
		return legacyInlineAsset{}, invalidLegacyInlineAsset()
	}
	content, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(content) == 0 {
		return legacyInlineAsset{}, invalidLegacyInlineAsset()
	}
	return legacyInlineAsset{
		mediaType: strings.ToLower(strings.TrimSpace(mediaType)),
		content:   content,
	}, nil
}

func invalidLegacyInlineAsset() error {
	return &generation.Error{
		Code:      generation.ErrInvalidInput,
		Operation: "persist_legacy_inline_assets",
	}
}

func (s *Server) legacyOfferingID(
	capability generation.Capability,
	modelID string,
) (generation.OfferingID, error) {
	if strings.TrimSpace(modelID) == "" {
		// 默认选路与原生入口共用 generation 的单一实现，
		// 避免两套规则漂移（B6）。
		return s.generation.DefaultOfferingID(capability)
	}
	for _, offering := range s.generation.Offerings(capability) {
		if offering.ModelID == modelID && s.generation.OfferingAvailable(offering.ID) {
			return offering.ID, nil
		}
	}
	return "", &generation.Error{
		Code:      generation.ErrUnavailable,
		Operation: "legacy_offering",
	}
}

func (s *Server) legacyGetTask(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
	capability catalog.CapabilityPresentation,
) {
	task, err := s.generation.GetScoped(
		request.Context(),
		taskReadScope(principal),
		generation.TaskID(request.PathValue("taskID")),
	)
	if err != nil {
		s.writeLegacyDomainError(writer, err)
		return
	}
	if task.Capability != capability.ID {
		s.writeLegacyError(
			writer,
			http.StatusNotFound,
			"error.task_not_found",
		)
		return
	}
	response := map[string]any{}
	if err := json.Unmarshal(task.Input, &response); err != nil {
		s.writeLegacyError(
			writer,
			http.StatusInternalServerError,
			"error.task_parameters_corrupt",
		)
		return
	}
	if len(capability.LegacyAPI.AssetAliases) > 0 {
		presentationOffering := task.Offering
		if current, exists := s.generation.Offering(task.Offering.ID); exists {
			presentationOffering.Parameters = current.Parameters
		}
		restoreLegacyAssetAliases(
			response,
			presentationOffering,
			capability.LegacyAPI.AssetAliases,
			s.assetItemRoute,
		)
	}
	restoreLegacyTaskAliases(response, capability.LegacyAPI.TaskAliases, task.Offering.ID)
	restoreLegacyFields(response, capability.LegacyAPI.Fields, task.Offering.ID)
	response["id"] = task.ID
	// 状态词表与任务 SSE/历史同源（Schema task_states 公开映射），
	// GET 不再泄漏内部状态名（QUEUED/MATERIALIZING）。
	response["status"] = s.publicTaskState(task.State)
	response["progress"] = task.Progress
	response["ai_model"] = task.Offering.ModelID
	projectLegacyOutputAssets(
		response,
		task,
		capability.LegacyAPI.OutputProjections,
		capability.LegacyAPI.AssetDownload,
		s.taskAssetRoute,
	)
	projectLegacyResultAssets(
		response,
		task,
		*capability.LegacyAPI,
		s.taskAssetRoute,
	)
	response["createdAt"] = task.CreatedAt
	response["updatedAt"] = task.UpdatedAt
	if task.Failure != nil {
		response["task_error"] = map[string]string{
			"message": s.publicFailure(requestLocale(request), task.Failure).Detail,
		}
	}
	writeJSON(writer, http.StatusOK, response)
}

func applyLegacyTaskAliases(
	input map[string]any,
	offering generation.Offering,
	aliases []catalog.LegacyTaskAliasSchema,
) error {
	for _, alias := range aliases {
		if _, present := input[alias.Name]; !present {
			continue
		}
		target, exists := parameterByName(offering.Parameters, alias.TargetParameter)
		if !catalog.LegacyOfferingApplies(alias.OfferingIDs, offering.ID) || !exists || target.Type != "task" {
			return unsupportedLegacyInput()
		}
		if alias.TargetParameter != alias.Name {
			if _, present := input[alias.TargetParameter]; present {
				return unsupportedLegacyInput()
			}
		}
	}
	for _, alias := range aliases {
		if value, present := input[alias.Name]; present {
			input[alias.TargetParameter] = value
			if alias.TargetParameter != alias.Name {
				delete(input, alias.Name)
			}
		}
	}
	return nil
}

func restoreLegacyTaskAliases(
	response map[string]any,
	aliases []catalog.LegacyTaskAliasSchema,
	offeringID generation.OfferingID,
) {
	for _, alias := range aliases {
		if !alias.Restore || !catalog.LegacyOfferingApplies(alias.OfferingIDs, offeringID) {
			continue
		}
		value, exists := response[alias.TargetParameter]
		if !exists {
			continue
		}
		response[alias.Name] = value
		delete(response, alias.TargetParameter)
	}
}

func (s *Server) legacyDownloadAsset(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
	schema catalog.LegacyAssetDownloadSchema,
) {
	s.setPrivateAssetCacheControl(writer)
	taskID, assetID, valid := parseLegacyAssetToken(
		request.PathValue(schema.TokenParameter),
	)
	if !valid {
		s.writeLegacyError(
			writer,
			http.StatusNotFound,
			"error.legacy_asset_not_found",
		)
		return
	}
	content, err := s.generation.OpenAssetScoped(
		request.Context(),
		taskReadScope(principal),
		taskID,
		assetID,
	)
	if err != nil {
		s.writeLegacyDomainError(writer, err)
		return
	}
	defer content.Body.Close()
	taskAssetAudit := s.application.Runtime.AssetDelivery.TaskAssetAudit
	s.recordProductAudit(request, administration.AuditEvent{
		Action:       taskAssetAudit.DownloadAction,
		ActorID:      principal.ID(),
		ActorName:    principal.Claims.Name,
		ResourceType: taskAssetAudit.ResourceType,
		ResourceID:   string(assetID),
		Detail: map[string]any{
			taskAssetAudit.DetailFields.TaskID:  string(taskID),
			taskAssetAudit.DetailFields.AssetID: string(assetID),
		},
	})
	writer.Header().Set("Content-Type", schema.MediaType)
	if content.Size > 0 {
		writer.Header().Set(
			"Content-Length",
			strconv.FormatInt(content.Size, 10),
		)
	}
	if content.SHA256 != "" {
		writer.Header().Set("ETag", `"`+content.SHA256+`"`)
	}
	filename := strings.ReplaceAll(
		schema.FilenameTemplate,
		"{token}",
		request.PathValue(schema.TokenParameter),
	)
	filename = strings.NewReplacer(
		"\r", "",
		"\n", "",
		`"`, "",
	).Replace(filename)
	writer.Header().Set(
		"Content-Disposition",
		`attachment; filename="`+filename+`"`,
	)
	if schema.CacheHeader != "" {
		writer.Header().Set(schema.CacheHeader, schema.CacheValue)
	}
	s.setPrivateAssetCacheControl(writer)
	setSandboxedBytesHeaders(writer)
	writer.WriteHeader(http.StatusOK)
	_, _ = io.Copy(writer, content.Body)
}

func (s *Server) legacyDeleteTask(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
	capability catalog.CapabilityPresentation,
) {
	scope := generation.OwnTasks(generation.OwnerID(principal.ID()))
	if principal.IsAdmin() {
		scope = generation.AllTasks()
	}
	configured := s.application.Runtime.TaskDeletion
	err := s.generation.Delete(request.Context(), generation.DeleteCommand{
		Scope:              scope,
		ActorID:            generation.OwnerID(principal.ID()),
		TaskID:             generation.TaskID(request.PathValue("taskID")),
		ExpectedCapability: capability.ID,
		Policy: generation.TaskDeletionPolicy{
			LocalRecordPolicy:     configured.LocalRecordPolicy,
			UpstreamPolicy:        configured.UpstreamPolicy,
			UpstreamFailurePolicy: configured.UpstreamFailurePolicy,
			GeneratedAssetPolicy:  configured.GeneratedAssetPolicy,
			AuditEventType:        configured.AuditEventType,
		},
	})
	if err != nil {
		var domainErr *generation.Error
		if errors.As(err, &domainErr) &&
			domainErr.Code == generation.ErrNotFound {
			s.writeLegacyError(
				writer,
				http.StatusNotFound,
				"error.legacy_task_delete_not_found",
			)
			return
		}
		s.writeLegacyError(
			writer,
			http.StatusInternalServerError,
			"error.legacy_task_delete_failed",
		)
		return
	}
	taskID := generation.TaskID(request.PathValue("taskID"))
	taskAudit := s.application.Runtime.TaskAudit
	s.recordProductAudit(request, administration.AuditEvent{
		Action:       taskAudit.DeleteAction,
		ActorID:      principal.ID(),
		ActorName:    principal.Claims.Name,
		ResourceType: taskAudit.ResourceType,
		ResourceID:   string(taskID),
	})
	writeJSON(writer, http.StatusOK, map[string]bool{"success": true})
}

func restoreLegacyAssetAliases(
	response map[string]any,
	offering generation.Offering,
	aliases []catalog.LegacyAssetAliasSchema,
	assetItemRoute routing.One,
) {
	applicable := make([]catalog.LegacyAssetAliasSchema, 0, len(aliases))
	for _, alias := range aliases {
		if catalog.LegacyOfferingApplies(alias.OfferingIDs, offering.ID) {
			applicable = append(applicable, alias)
		}
	}
	aliases = applicable
	if len(aliases) == 0 {
		return
	}
	target, exists := parameterBySemantic(offering, aliases[0].Semantic)
	if !exists {
		return
	}
	rawValue, exists := response[target.Name]
	if !exists {
		return
	}
	rawReferences := make([]any, 0)
	switch typed := rawValue.(type) {
	case []any:
		rawReferences = typed
	case string:
		rawReferences = []any{typed}
	default:
		return
	}
	urls := make([]string, 0, len(rawReferences))
	for _, raw := range rawReferences {
		id, ok := raw.(string)
		if !ok || strings.TrimSpace(id) == "" {
			continue
		}
		urls = append(urls, assetItemRoute.Render(id))
	}
	delete(response, target.Name)
	for _, alias := range aliases {
		if !alias.Restore || len(urls) < alias.RestoreMinimumItems {
			continue
		}
		switch alias.Cardinality {
		case "one":
			if len(urls) > 0 {
				response[alias.Name] = urls[0]
			}
		case "many":
			response[alias.Name] = urls
		}
	}
}

func (s *Server) listOfferings(
	writer http.ResponseWriter,
	request *http.Request,
	_ generation.OwnerID,
) {
	capability := generation.Capability(request.URL.Query().Get("capability"))
	if capability != "" && !capability.Valid() {
		s.writeDomainError(writer, &generation.Error{
			Code: generation.ErrInvalidCapability,
		})
		return
	}
	offerings := s.generation.Offerings(capability)
	response := make([]offeringResponse, 0, len(offerings))
	for _, offering := range offerings {
		response = append(response, offeringResponse{
			PriceMode: nativePriceMode(offering), DefaultNativeQuote: defaultNativePrice(offering), UpperNativeQuote: upperNativePrice(offering),
			ID:                            offering.ID,
			Revision:                      offering.Revision,
			ProviderID:                    offering.ProviderID,
			ProviderName:                  offering.ProviderName,
			ProviderShortLabel:            offering.ProviderShortLabel,
			ModelID:                       offering.ModelID,
			ModelName:                     offering.ModelName,
			Capability:                    offering.Capability,
			Available:                     s.generation.OfferingAvailable(offering.ID),
			Recommended:                   offering.Recommended,
			Priority:                      offering.Priority,
			EstimatedCredits:              offering.QuotedCredits(),
			CreditsMetered:                offering.CreditsMetered,
			MaximumCredits:                offering.MaximumEstimatedCredits(),
			EstimatedCreditsUnitParameter: offering.EstimatedCreditsUnitParameter,
			ReferenceSelectionPolicy:      offering.ReferenceSelectionPolicy,
			ExtraCreditConsentParameter:   extraCreditConsentParameter(offering),
			Parameters:                    offering.Parameters,
			InputRules:                    offering.InputRules,
			Outputs:                       offering.Outputs,
		})
	}
	writeJSON(writer, http.StatusOK, s.localizedSchemaData(request, map[string]any{"offerings": response}))
}

type offeringResponse struct {
	PriceMode          string                  `json:"price_mode,omitempty"`
	DefaultNativeQuote *generation.NativeQuote `json:"default_native_quote,omitempty"`
	UpperNativeQuote   *generation.NativeQuote `json:"upper_native_quote,omitempty"`
	ID                 generation.OfferingID   `json:"id"`
	Revision           string                  `json:"revision"`
	ProviderID         string                  `json:"provider_id"`
	ProviderName       string                  `json:"provider_name"`
	ProviderShortLabel string                  `json:"provider_short_label"`
	ModelID            string                  `json:"model_id"`
	ModelName          string                  `json:"model_name"`
	Capability         generation.Capability   `json:"capability"`
	Available          bool                    `json:"available"`
	Recommended        bool                    `json:"recommended,omitempty"`
	Priority           int                     `json:"priority,omitempty"`
	EstimatedCredits   int                     `json:"estimated_credits,omitempty"`
	// CreditsMetered marks offerings whose provider bills in credits; the UI
	// shows no credits for the rest.
	CreditsMetered bool `json:"credits_metered,omitempty"`
	// EstimatedCreditsUnitParameter names the numeric parameter the estimate is
	// multiplied by (a count of images); the client shows cost x count with it.
	EstimatedCreditsUnitParameter string `json:"estimated_credits_unit_parameter,omitempty"`
	ReferenceSelectionPolicy      string `json:"reference_selection_policy,omitempty"`
	// MaximumCredits is the derived highest estimate when optional billed
	// choices apply; the cost declaration itself is never public.
	MaximumCredits int `json:"maximum_estimated_credits,omitempty"`
	// ExtraCreditConsentParameter names the boolean parameter that carries the
	// person's consent to extra credits (an extra-export selection); the client
	// shows it as a checkbox. Only the name is public, never the amounts.
	ExtraCreditConsentParameter string                        `json:"extra_credit_consent_parameter,omitempty"`
	Parameters                  []generation.Parameter        `json:"parameters"`
	InputRules                  []generation.InputRule        `json:"input_rules,omitempty"`
	Outputs                     []generation.AssetRequirement `json:"outputs"`
}

func extraCreditConsentParameter(offering generation.Offering) string {
	return offering.ExtraConsentParameter()
}

func (s *Server) legacyUploadAsset(
	writer http.ResponseWriter,
	request *http.Request,
	owner generation.OwnerID,
) {
	if s.inputAssets == nil {
		s.writeLegacyError(
			writer,
			http.StatusInternalServerError,
			"error.legacy_asset_upload_failed",
		)
		return
	}
	httpRuntime := s.application.Runtime.HTTP
	inputRuntime := s.application.Runtime.InputAssets
	request.Body = http.MaxBytesReader(
		writer,
		request.Body,
		httpRuntime.MultipartBodyMaxBytes,
	)
	reader, err := request.MultipartReader()
	if err != nil {
		s.writeLegacyError(
			writer,
			http.StatusBadRequest,
			"error.legacy_asset_missing_upload",
		)
		return
	}
	part, err := nextFilePart(reader, inputRuntime.UploadFieldName)
	if err != nil {
		s.writeLegacyError(
			writer,
			http.StatusBadRequest,
			"error.legacy_asset_missing_upload",
		)
		return
	}
	defer part.Close()
	payload, err := io.ReadAll(io.LimitReader(
		part,
		inputRuntime.UploadMaxBytes+1,
	))
	if err != nil {
		s.writeLegacyError(
			writer,
			http.StatusInternalServerError,
			"error.legacy_asset_upload_failed",
		)
		return
	}
	if int64(len(payload)) > inputRuntime.UploadMaxBytes {
		// The limit as every size is written: decimal MB with its unit (#327).
		detail := strings.ReplaceAll(
			s.localeMessage(writer, "error.legacy_asset_upload_too_large"),
			"{limit}",
			schemacontract.SizeText(inputRuntime.UploadMaxBytes, false),
		)
		writeJSON(
			writer,
			http.StatusBadRequest,
			map[string]string{"error": detail},
		)
		return
	}
	mediaType := strings.TrimSpace(part.Header.Get("Content-Type"))
	if mediaType == "" {
		mediaType = inputRuntime.DefaultMediaType
	}
	if !s.uploadMediaTypeAllowed(mediaType) {
		s.writeLegacyError(
			writer,
			http.StatusBadRequest,
			"error.input_media_type_not_allowed",
		)
		return
	}
	asset, err := s.inputAssets.UploadAsset(
		request.Context(),
		owner,
		generation.InputAssetUpload{
			Body:             bytes.NewReader(payload),
			OriginalFilename: part.FileName(),
			AssetType:        s.inputAssetType(mediaType, part.FileName()),
			MediaType:        mediaType,
			MaxBytes:         inputRuntime.UploadMaxBytes,
		},
	)
	if err != nil {
		s.writeLegacyAssetDomainError(
			writer,
			err,
			"error.legacy_asset_upload_failed",
		)
		return
	}
	assetAudit := inputRuntime.Audit
	s.recordProductAudit(request, administration.AuditEvent{
		Action:       assetAudit.UploadAction,
		ActorID:      string(owner),
		ResourceType: assetAudit.ResourceType,
		ResourceID:   string(asset.ID),
		Detail: map[string]any{
			assetAudit.DetailFields.Filename:  part.FileName(),
			assetAudit.DetailFields.MediaType: mediaType,
			assetAudit.DetailFields.FileSize:  int64(len(payload)),
		},
	})
	fields := inputRuntime.API.Fields
	writeJSON(writer, http.StatusOK, map[string]any{
		fields.AssetID:      asset.ID,
		fields.AssetType:    asset.AssetType,
		fields.StoredSHA256: asset.SHA256,
		fields.FileSize:     asset.Size,
	})
}

func (s *Server) legacyGetAsset(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	s.setPrivateAssetCacheControl(writer)
	if s.inputAssets == nil {
		s.writeLegacyError(
			writer,
			http.StatusInternalServerError,
			"error.legacy_asset_get_failed",
		)
		return
	}
	asset, body, err := s.inputAssets.OpenAuthorized(
		request.Context(),
		generation.OwnerID(principal.ID()),
		generation.AssetID(configuredPathValue(
			request,
			s.application.Runtime.HTTP.LegacyRoutes.AssetItem,
			0,
		)),
		principal.IsAdmin(),
	)
	if err != nil {
		s.writeLegacyAssetDomainError(
			writer,
			err,
			"error.legacy_asset_get_failed",
		)
		return
	}
	defer body.Close()
	inputRuntime := s.application.Runtime.InputAssets
	assetAudit := inputRuntime.Audit
	s.recordProductAudit(request, administration.AuditEvent{
		Action:       assetAudit.AccessAction,
		ActorID:      principal.ID(),
		ActorName:    principal.Claims.Name,
		ResourceType: assetAudit.ResourceType,
		ResourceID:   string(asset.ID),
	})
	if request.URL.Query().Get(inputRuntime.JSONFormatParameter) ==
		inputRuntime.JSONFormatValue {
		payload, err := io.ReadAll(body)
		if err != nil {
			s.writeLegacyError(
				writer,
				http.StatusInternalServerError,
				"error.legacy_asset_get_failed",
			)
			return
		}
		fields := inputRuntime.API.Fields
		writeJSON(writer, http.StatusOK, map[string]any{
			fields.ID:               asset.ID,
			fields.AssetType:        asset.AssetType,
			fields.OriginalFilename: asset.OriginalFilename,
			fields.MediaType:        asset.MediaType,
			fields.FileSize:         asset.Size,
			fields.DataURI: "data:" + asset.MediaType + ";base64," +
				base64.StdEncoding.EncodeToString(payload),
			fields.CreatedAt: asset.CreatedAt,
		})
		return
	}
	contentType, inline := s.inputAssetDelivery(asset.MediaType)
	writer.Header().Set("Content-Length", strconv.FormatInt(asset.Size, 10))
	setInputAssetBinaryHeaders(writer, contentType, inline)
	writer.WriteHeader(http.StatusOK)
	_, _ = io.Copy(writer, body)
}

func (s *Server) inputAssetType(mediaType string, filename string) string {
	runtime := s.application.Runtime.InputAssets
	normalizedMediaType := strings.ToLower(strings.TrimSpace(
		strings.SplitN(mediaType, ";", 2)[0],
	))
	extension := strings.ToLower(filepath.Ext(filename))
	for _, rule := range runtime.TypeRules {
		if slicesContains(rule.MediaTypes, normalizedMediaType) ||
			slicesContains(rule.Extensions, extension) {
			return rule.AssetType
		}
	}
	return runtime.DefaultAssetType
}

func slicesContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func normalizedUploadMediaType(mediaType string) string {
	return strings.ToLower(strings.TrimSpace(
		strings.SplitN(mediaType, ";", 2)[0],
	))
}

func (s *Server) uploadMediaTypeAllowed(mediaType string) bool {
	allowed := s.application.Runtime.InputAssets.AllowedUploadMediaTypes
	if len(allowed) == 0 {
		return true
	}
	return slicesContains(
		allowed,
		normalizedUploadMediaType(mediaType),
	)
}

// inputAssetDelivery 决定输入资产二进制下发的 Content-Type 与是否内联：
// 只有 Schema 声明为可内联的类型按原类型下发，其余一律降级为
// attachment + octet-stream，防止存储型 XSS。
func (s *Server) inputAssetDelivery(
	mediaType string,
) (contentType string, inline bool) {
	inputRuntime := s.application.Runtime.InputAssets
	normalized := normalizedUploadMediaType(mediaType)
	if slicesContains(inputRuntime.InlineMediaTypes, normalized) {
		return normalized, true
	}
	return inputRuntime.DefaultMediaType, false
}

func setInputAssetBinaryHeaders(
	writer http.ResponseWriter,
	contentType string,
	inline bool,
) {
	writer.Header().Set("Content-Type", contentType)
	if inline {
		writer.Header().Set("Content-Disposition", "inline")
	} else {
		writer.Header().Set("Content-Disposition", "attachment")
	}
	writer.Header().Set("Content-Security-Policy", "sandbox")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
}

func (s *Server) writeLegacyAssetDomainError(
	writer http.ResponseWriter,
	err error,
	fallbackMessageKey string,
) {
	noteCause(writer, err)
	var domainErr *generation.Error
	if errors.As(err, &domainErr) {
		switch domainErr.Code {
		case generation.ErrInvalidModelInput, generation.ErrUnsupportedModelInput, generation.ErrModelInputLimit:
			s.writeLegacyError(writer, http.StatusBadRequest, "error."+string(domainErr.Code))
			return
		case generation.ErrNotFound:
			s.writeLegacyError(
				writer,
				http.StatusNotFound,
				"error.legacy_asset_not_found",
			)
			return
		case generation.ErrForbidden:
			s.writeLegacyError(
				writer,
				http.StatusForbidden,
				"error.legacy_asset_forbidden",
			)
			return
		}
	}
	s.writeLegacyError(
		writer,
		http.StatusInternalServerError,
		fallbackMessageKey,
	)
}

func (s *Server) writeLegacyError(
	writer http.ResponseWriter,
	status int,
	messageKey string,
) {
	writeJSON(
		writer,
		status,
		map[string]string{
			s.application.Authentication.API.ErrorField: s.localeMessage(writer, messageKey),
		},
	)
}

func (s *Server) writeTemplatedLegacyError(
	writer http.ResponseWriter,
	status int,
	messageKey string,
	values map[string]string,
) {
	detail := s.localeMessage(writer, messageKey)
	for name, value := range values {
		detail = strings.ReplaceAll(detail, "{"+name+"}", value)
	}
	writeJSON(writer, status, map[string]string{"error": detail})
}

func (s *Server) uploadAsset(
	writer http.ResponseWriter,
	request *http.Request,
	owner generation.OwnerID,
) {
	if s.inputAssets == nil {
		s.writeMessageProblem(
			writer,
			http.StatusServiceUnavailable,
			"unavailable",
			"error.input_asset_service_unavailable",
		)
		return
	}
	request.Body = http.MaxBytesReader(
		writer,
		request.Body,
		s.application.Runtime.HTTP.MultipartBodyMaxBytes,
	)
	reader, err := request.MultipartReader()
	if err != nil {
		s.writeMessageProblem(
			writer,
			http.StatusBadRequest,
			"invalid_input",
			"error.multipart_required",
		)
		return
	}
	inputRuntime := s.application.Runtime.InputAssets
	part, err := nextFilePart(reader, inputRuntime.UploadFieldName)
	if err != nil {
		s.writeMessageProblem(
			writer,
			http.StatusBadRequest,
			"invalid_input",
			"error.invalid_upload",
		)
		return
	}
	defer part.Close()
	mediaType := strings.TrimSpace(part.Header.Get("Content-Type"))
	if mediaType == "" {
		mediaType = inputRuntime.DefaultMediaType
	}
	if !s.uploadMediaTypeAllowed(mediaType) {
		s.writeMessageProblem(
			writer,
			http.StatusBadRequest,
			"invalid_input",
			"error.input_media_type_not_allowed",
		)
		return
	}
	asset, err := s.inputAssets.UploadAsset(
		request.Context(),
		owner,
		generation.InputAssetUpload{
			Body:             part,
			OriginalFilename: part.FileName(),
			AssetType:        s.inputAssetType(mediaType, part.FileName()),
			MediaType:        mediaType,
			MaxBytes:         inputRuntime.UploadMaxBytes,
		},
	)
	if err != nil {
		s.writeDomainError(writer, err)
		return
	}
	assetAudit := inputRuntime.Audit
	s.recordProductAudit(request, administration.AuditEvent{
		Action:       assetAudit.UploadAction,
		ActorID:      string(owner),
		ResourceType: assetAudit.ResourceType,
		ResourceID:   string(asset.ID),
		Detail: map[string]any{
			assetAudit.DetailFields.Filename:  part.FileName(),
			assetAudit.DetailFields.MediaType: mediaType,
			assetAudit.DetailFields.FileSize:  asset.Size,
		},
	})
	writeJSON(writer, http.StatusCreated, inputAssetResponse{
		ID:        asset.ID,
		MediaType: asset.MediaType,
		Size:      asset.Size,
		SHA256:    asset.SHA256,
		CreatedAt: asset.CreatedAt,
	})
}

func (s *Server) downloadInputAsset(
	writer http.ResponseWriter,
	request *http.Request,
	owner generation.OwnerID,
) {
	s.setPrivateAssetCacheControl(writer)
	if s.inputAssets == nil {
		s.writeMessageProblem(
			writer,
			http.StatusServiceUnavailable,
			"unavailable",
			"error.input_asset_service_unavailable",
		)
		return
	}
	asset, body, err := s.inputAssets.OpenAuthorized(
		request.Context(),
		owner,
		generation.AssetID(request.PathValue("assetID")),
		false,
	)
	if err != nil {
		s.writeDomainError(writer, err)
		return
	}
	defer body.Close()
	assetAudit := s.application.Runtime.InputAssets.Audit
	recordAccess := func() {
		s.recordProductAudit(request, administration.AuditEvent{
			Action:       assetAudit.AccessAction,
			ActorID:      string(owner),
			ResourceType: assetAudit.ResourceType,
			ResourceID:   string(asset.ID),
		})
	}
	// #290: uploaded pictures and board snapshots show as small tiles too; same preview as task pictures.
	var original io.Reader = body
	if s.wantsAssetPreview(request, asset.MediaType, asset.Size) {
		served, read := s.serveAssetPreview(writer, request, previewSource{
			Digest: asset.SHA256, MediaType: asset.MediaType, Size: asset.Size, Body: body,
		}, recordAccess)
		if served {
			return
		}
		original = io.MultiReader(bytes.NewReader(read), body)
	}
	recordAccess()
	contentType, inline := s.inputAssetDelivery(asset.MediaType)
	writer.Header().Set("Content-Length", strconv.FormatInt(asset.Size, 10))
	writer.Header().Set("ETag", `"`+asset.SHA256+`"`)
	setInputAssetBinaryHeaders(writer, contentType, inline)
	writer.WriteHeader(http.StatusOK)
	_, _ = io.Copy(writer, original)
}

func (s *Server) openProviderAsset(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if s.assetGateway == nil {
		http.NotFound(writer, request)
		return
	}
	asset, body, err := s.assetGateway.OpenSigned(
		request.Context(),
		request.PathValue("token"),
	)
	if err != nil {
		// 签名 URL 不区分令牌、所有者和资产是否存在，避免形成枚举 oracle。
		http.NotFound(writer, request)
		return
	}
	defer body.Close()
	writer.Header().Set("Content-Type", asset.MediaType)
	writer.Header().Set("Content-Length", strconv.FormatInt(asset.Size, 10))
	writer.Header().Set("ETag", `"`+asset.SHA256+`"`)
	writer.Header().Set(
		"Cache-Control",
		s.application.Runtime.HTTP.PrivateCacheControl,
	)
	setSandboxedBytesHeaders(writer)
	writer.WriteHeader(http.StatusOK)
	_, _ = io.Copy(writer, body)
}

type submitRequest struct {
	PreparedInputID        generation.PreparedInputID `json:"prepared_input_id"`
	AuthorizationRequestID string                     `json:"authorization_request_id"`
	SessionID              string                     `json:"session_id,omitempty"`
	Capability             generation.Capability      `json:"capability,omitempty"`
	OfferingID             generation.OfferingID      `json:"offering_id,omitempty"`
	Input                  json.RawMessage            `json:"input,omitempty"`
}

func (s *Server) submitTask(
	writer http.ResponseWriter,
	request *http.Request,
	owner generation.OwnerID,
) {
	var body submitRequest
	if err := s.decodeJSON(writer, request, &body); err != nil {
		return
	}
	if body.PreparedInputID == "" || body.AuthorizationRequestID == "" {
		s.writeMessageProblem(writer, http.StatusForbidden, "execution_grant_required", "error.execution_grant_required")
		return
	}
	if body.Capability != "" || body.OfferingID != "" || len(body.Input) != 0 {
		s.writeMessageProblem(writer, http.StatusBadRequest, "invalid_input", "error.invalid_input")
		return
	}
	prepared, err := s.generation.GetPreparedInput(request.Context(), owner, body.PreparedInputID)
	if err != nil {
		s.writeDomainError(writer, err)
		return
	}
	if prepared.SessionID == "" {
		s.writeMessageProblem(writer, http.StatusBadRequest, "session_required", "error.session_required")
		return
	}
	if body.SessionID != "" && body.SessionID != prepared.SessionID {
		s.writeMessageProblem(writer, http.StatusConflict, "conflict", "error.conflict")
		return
	}
	accepted, err := s.generation.SubmitPrepared(request.Context(), generation.SubmitPreparedCommand{
		OwnerID: owner, SessionID: prepared.SessionID,
		PreparedInputID:  prepared.ID,
		IdempotencyKey:   strings.TrimSpace(request.Header.Get("Idempotency-Key")),
		OperationID:      standaloneOperationID(prepared.ID),
		AuthorizationRef: body.AuthorizationRequestID,
	})
	if err != nil {
		s.writeDomainError(writer, err)
		return
	}
	taskAudit := s.application.Runtime.TaskAudit
	s.recordProductAudit(request, administration.AuditEvent{
		Action:       taskAudit.CreateAction,
		ActorID:      string(owner),
		ResourceType: taskAudit.ResourceType,
		ResourceID:   string(accepted.TaskID),
		Detail: map[string]any{
			taskAudit.DetailFields.Capability: prepared.Capability,
			taskAudit.DetailFields.OfferingID: prepared.OfferingID,
			taskAudit.DetailFields.Replayed:   accepted.Replayed,
		},
	})
	writer.Header().Set(
		"Location",
		s.taskItemRoute.Render(string(accepted.TaskID)),
	)
	writeJSON(writer, http.StatusAccepted, accepted)
}

func (s *Server) listTasks(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	query := generation.ListQuery{
		Capability: generation.Capability(request.URL.Query().Get("capability")),
		Cursor:     request.URL.Query().Get("cursor"),
		SessionID:  request.URL.Query().Get("session_id"),
	}
	scope := taskReadScope(principal)
	if query.SessionID != "" {
		owned, err := s.chat.OwnsSession(request.Context(), principal.ID(), query.SessionID)
		if err != nil {
			s.writeChatInternalError(writer, err)
			return
		}
		if !owned {
			s.writeMessageProblem(writer, http.StatusNotFound, "not_found", "error.task_not_found")
			return
		}
		scope = generation.OwnTasks(generation.OwnerID(principal.ID()))
	}
	if limit := request.URL.Query().Get("limit"); limit != "" {
		parsed, err := strconv.Atoi(limit)
		if err != nil || parsed < 1 {
			s.writeMessageProblem(
				writer,
				http.StatusBadRequest,
				"invalid_input",
				"error.limit_positive",
			)
			return
		}
		query.Limit = parsed
	}
	for _, raw := range request.URL.Query()["state"] {
		query.States = append(query.States, generation.TaskState(raw))
	}
	page, err := s.generation.List(
		request.Context(),
		scope,
		query,
	)
	if err != nil {
		s.writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, s.taskPageResponse(requestLocale(request), page))
}

func (s *Server) getTask(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	task, err := s.generation.GetSummaryScoped(
		request.Context(),
		taskReadScope(principal),
		generation.TaskID(request.PathValue("taskID")),
	)
	if err != nil {
		s.writeDomainError(writer, err)
		return
	}
	response := s.newTaskResponse(requestLocale(request), task)
	// The summary read model carries no input. The owner's own task is read once more through the
	// kernel for the prompt and public parameters; another reader (an administrator) gets none.
	if full, err := s.generation.GetScoped(request.Context(), taskReadScope(principal), generation.TaskID(request.PathValue("taskID"))); err == nil &&
		string(full.OwnerID) == principal.ID() {
		response.Input = publicTaskInput(full)
		response.CreditsCharged = publicCreditsCharged(full)
		withSourceRetry(&response, s.generation.TaskSourceRetry(request.Context(), full))
		if provenance, err := s.generation.TaskRecoveryProvenance(request.Context(), full); err == nil {
			response.RecoverySource = provenance
		}
		// Reference images re-encoded to fit the Offering (#276): the owner sees
		// what was sent, from which of their assets, how and at what size.
		if derivations, err := s.generation.TaskInputDerivations(request.Context(), full); err == nil {
			response.InputDerivations = derivations
		} else {
			s.logger.Warn("read task input derivations", "task_id", full.ID, "error", err)
		}
	}
	writeJSON(writer, http.StatusOK, response)
}

func (s *Server) cancelTask(
	writer http.ResponseWriter,
	request *http.Request,
	owner generation.OwnerID,
) {
	task, err := s.generation.Cancel(
		request.Context(),
		owner,
		generation.TaskID(request.PathValue("taskID")),
	)
	if err != nil {
		s.writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, s.newTaskResponse(requestLocale(request), task))
}

func (s *Server) listEvents(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	after, err := parseNonnegativeInt64(request.URL.Query().Get("after"))
	if err != nil {
		s.writeMessageProblem(
			writer,
			http.StatusBadRequest,
			"invalid_input",
			"error.after_nonnegative",
		)
		return
	}
	events, err := s.generation.EventsScoped(
		request.Context(),
		taskReadScope(principal),
		generation.TaskID(request.PathValue("taskID")),
		after,
		100,
	)
	if err != nil {
		s.writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"events": s.publicEvents(requestLocale(request), events),
	})
}

func (s *Server) downloadAsset(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	s.setPrivateAssetCacheControl(writer)
	content, err := s.generation.OpenAssetScoped(
		request.Context(),
		taskReadScope(principal),
		generation.TaskID(request.PathValue("taskID")),
		generation.AssetID(request.PathValue("assetID")),
	)
	if err != nil {
		s.writeDomainError(writer, err)
		return
	}
	defer content.Body.Close()
	taskAssetAudit := s.application.Runtime.AssetDelivery.TaskAssetAudit
	taskID := request.PathValue("taskID")
	assetID := request.PathValue("assetID")
	recordDownload := func() {
		s.recordProductAudit(request, administration.AuditEvent{
			Action:       taskAssetAudit.DownloadAction,
			ActorID:      principal.ID(),
			ActorName:    principal.Claims.Name,
			ResourceType: taskAssetAudit.ResourceType,
			ResourceID:   assetID,
			Detail: map[string]any{
				taskAssetAudit.DetailFields.TaskID:  taskID,
				taskAssetAudit.DetailFields.AssetID: assetID,
			},
		})
	}
	// #290: a small view asks for the downscaled copy; anything without one gets the original below.
	var body io.Reader = content.Body
	if s.wantsAssetPreview(request, content.MediaType, content.Size) {
		served, read := s.serveAssetPreview(writer, request, previewSource{
			Digest: content.SHA256, MediaType: content.MediaType, Size: content.Size, Body: content.Body,
		}, recordDownload)
		if served {
			return
		}
		body = io.MultiReader(bytes.NewReader(read), content.Body)
	}
	etag := `"` + content.SHA256 + `"`
	// #225：授权已通过（上面 OpenAssetScoped 按本人范围读取）。只有 Schema
	// 声明的缩略图角色可被浏览器短期复用，并对同一 ETag 回 304；其余保持 no-store。
	if slices.Contains(s.application.Runtime.HTTP.ThumbnailAssetRoles, content.Role) {
		writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.ThumbnailCacheControl)
		// The cached bytes belong to the signed-in session: after logout or a different
		// login the browser's entry no longer matches and the request is authorized again.
		// Clear-Site-Data does the same only over HTTPS; this also holds on plain HTTP.
		writer.Header().Set("Vary", "Cookie, Authorization")
		if etagMatches(request.Header.Get("If-None-Match"), etag) {
			writer.Header().Set("ETag", etag)
			writer.WriteHeader(http.StatusNotModified)
			return
		}
	}
	recordDownload()
	// A model file is saved under its Schema format name (#264) and served
	// with its format's media type (#330); any other non-preview file (MTL,
	// reports, ...) gets <role>-<task>.<extension> and its saved type.
	// Previewed pictures stay inline; every file is sandboxed (#315).
	mediaType := content.MediaType
	filename := storedAssetFilename(content.Role, taskID, content.Variant, content.MediaType)
	if s.modelDelivery != nil {
		saved := generation.Asset{Role: content.Role, Variant: content.Variant, MediaType: content.MediaType}
		if name, named := s.modelDelivery.DownloadFilename(generation.TaskID(taskID), saved); named {
			filename = name
		}
		if declared, named := s.modelDelivery.DownloadMediaType(saved); named {
			mediaType = declared
		}
	}
	if mediaType != "" {
		writer.Header().Set("Content-Type", mediaType)
	}
	s.setStoredAssetDeliveryHeaders(writer, mediaType, filename)
	if content.Size > 0 {
		writer.Header().Set("Content-Length", strconv.FormatInt(content.Size, 10))
	}
	writer.Header().Set("ETag", etag)
	writer.WriteHeader(http.StatusOK)
	_, _ = io.Copy(writer, body)
}

// etagMatches implements the If-None-Match comparison (weak, list, "*").
func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == "*" || candidate == etag {
			return true
		}
	}
	return false
}

// setPrivateAssetCacheControl marks full authenticated asset bytes (models,
// originals, archives) with the Schema's private no-store policy: they are
// re-authorized on each request, and browsers must not retain them after
// logout or a permission change. A stable content hash remains useful as an
// ETag. Thumbnails use their own private, max-age policy (#225).
func (s *Server) setPrivateAssetCacheControl(writer http.ResponseWriter) {
	writer.Header().Set(
		"Cache-Control",
		s.application.Runtime.HTTP.PrivateCacheControl,
	)
}

func (s *Server) decodeJSON(
	writer http.ResponseWriter,
	request *http.Request,
	target any,
) error {
	return s.decodeJSONWithLimit(
		writer,
		request,
		target,
		s.application.Runtime.HTTP.JSONBodyMaxBytes,
	)
}

func (s *Server) decodeJSONWithLimit(
	writer http.ResponseWriter,
	request *http.Request,
	target any,
	maxBytes int64,
) error {
	request.Body = http.MaxBytesReader(writer, request.Body, maxBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		s.writeMessageProblem(
			writer,
			http.StatusBadRequest,
			"invalid_json",
			"error.invalid_json",
		)
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		s.writeMessageProblem(
			writer,
			http.StatusBadRequest,
			"invalid_json",
			"error.trailing_json",
		)
		return errors.New("JSON 尾部存在额外内容")
	}
	return nil
}

func (s *Server) decodeLegacyJSONWithLimit(
	writer http.ResponseWriter,
	request *http.Request,
	target any,
	maxBytes int64,
) error {
	request.Body = http.MaxBytesReader(writer, request.Body, maxBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		s.writeLegacyError(
			writer,
			http.StatusBadRequest,
			"error.invalid_json",
		)
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		s.writeLegacyError(
			writer,
			http.StatusBadRequest,
			"error.trailing_json",
		)
		return errors.New("JSON 尾部存在额外内容")
	}
	return nil
}

func parseNonnegativeInt64(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, errors.New("必须是非负整数")
	}
	return parsed, nil
}

func nextFilePart(
	reader *multipart.Reader,
	fieldName string,
) (*multipart.Part, error) {
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("upload file part missing")
		}
		if err != nil {
			return nil, errors.New("read upload file part")
		}
		if part.FormName() == fieldName && part.FileName() != "" {
			return part, nil
		}
		part.Close()
	}
}

type inputAssetResponse struct {
	ID        generation.AssetID `json:"id"`
	MediaType string             `json:"media_type"`
	Size      int64              `json:"size"`
	SHA256    string             `json:"sha256"`
	CreatedAt time.Time          `json:"created_at"`
}

type taskResponse struct {
	NativeQuote *generation.NativeQuote `json:"native_quote,omitempty"`
	ID          generation.TaskID       `json:"id"`
	SessionID   string                  `json:"session_id,omitempty"`
	Capability  generation.Capability   `json:"capability"`
	OfferingID  generation.OfferingID   `json:"offering_id"`
	ProviderID  string                  `json:"provider_id"`
	ModelID     string                  `json:"model_id"`
	State       generation.TaskState    `json:"state"`
	Progress    int                     `json:"progress"`
	Failure     *publicFailure          `json:"failure,omitempty"`
	Version     int64                   `json:"version"`
	CreatedAt   time.Time               `json:"created_at"`
	UpdatedAt   time.Time               `json:"updated_at"`
	FinishedAt  *time.Time              `json:"finished_at,omitempty"`
	Assets      []assetResponse         `json:"assets"`
	// Input 只出现在所有者读取单个任务的响应里（见 getTask），是冻结输入的公开投影。
	Input          *taskInputResponse             `json:"input,omitempty"`
	RecoverySource *generation.RecoveryProvenance `json:"recovery_source,omitempty"`
	// InputDerivations 也只给所有者：冻结输入中为满足 Offering 上限而重新编码的参考图，
	// 列出来源资产（仅限所有者自己的）、方式、前后大小和宽高（#276）。
	InputDerivations []generation.InputDerivationView `json:"input_derivations,omitempty"`
	// CreditsCharged 同样只给所有者：已成功且 credits_metered 的 Offering 按其声明的
	// 预计口径（EstimateTotalCredits）给出本次消耗积分；其余不输出。
	CreditsCharged *int `json:"credits_charged,omitempty"`
	// resources[]/resources_pending 是 W6 加性资源完成度投影（§6.2 触点 9、
	// §8.1 白名单）：仅 0022 资源态存在时输出；reason 只投影文案键解析后
	// 的安全文本，不泄漏调度内部。
	Resources        []resourceResponse `json:"resources,omitempty"`
	ResourcesPending bool               `json:"resources_pending"`
	// UpstreamJobCreatedAt / UpstreamJobGone carry the facts of the upstream
	// job age rule (#326) the 加工 panel mirrors: when the Task holding this
	// Task's job was made (only when that is another Task) and whether a
	// later step found the job gone. Neither names the job itself.
	UpstreamJobCreatedAt *time.Time `json:"upstream_job_created_at,omitempty"`
	UpstreamJobGone      bool       `json:"upstream_job_gone,omitempty"`
}

type resourceResponse struct {
	Role          string `json:"role"`
	Variant       string `json:"variant,omitempty"`
	VariantPrefix string `json:"variant_prefix,omitempty"`
	Group         string `json:"group,omitempty"`
	Delivery      string `json:"delivery,omitempty"`
	State         string `json:"state"`
	Reason        string `json:"reason,omitempty"`
}

type publicFailure struct {
	Class     generation.FailureClass `json:"class"`
	Retryable bool                    `json:"retryable"`
	Ambiguous bool                    `json:"ambiguous,omitempty"`
	Detail    string                  `json:"detail"`
	// SourceRetry is the owner's judgment of running a step that failed as
	// source_expired again (#326): saved_file, source_expired or
	// source_incompatible (generation.SourceRetry). Only the owner's task
	// detail carries it.
	SourceRetry generation.SourceRetry `json:"source_retry,omitempty"`
}

// withSourceRetry adds the owner's judgment of running a failed step again
// (#326) to a task response that has a failure.
func withSourceRetry(response *taskResponse, judgment generation.SourceRetry) {
	if response.Failure != nil {
		response.Failure.SourceRetry = judgment
	}
}

type assetResponse struct {
	DeliveryProfile string             `json:"delivery_profile,omitempty"`
	ID              generation.AssetID `json:"id"`
	Role            string             `json:"role"`
	Variant         string             `json:"variant,omitempty"`
	Required        bool               `json:"required"`
	Delivery        string             `json:"delivery,omitempty"`
	Ordinal         int                `json:"ordinal"`
	MediaType       string             `json:"media_type"`
	Size            int64              `json:"size"`
	SHA256          string             `json:"sha256"`
	URL             string             `json:"url"`
	// Format is the public name of a model file's format ("GLB"), never the
	// Provider's result field (#327); empty for files of no known format.
	Format string `json:"format,omitempty"`
}

func (s *Server) newTaskResponse(locale string, task generation.Task) taskResponse {
	result := taskResponse{
		NativeQuote:     s.quoteIn(locale, task.Offering, task.NativeQuote),
		ID:              task.ID,
		SessionID:       task.SessionID,
		Capability:      task.Capability,
		OfferingID:      task.Offering.ID,
		ProviderID:      task.Offering.ProviderID,
		ModelID:         task.Offering.ModelID,
		State:           task.State,
		Progress:        task.Progress,
		Failure:         s.publicFailure(locale, task.Failure),
		Version:         task.Version,
		CreatedAt:       task.CreatedAt,
		UpstreamJobGone: task.UpstreamJobGone,
		UpdatedAt:       task.UpdatedAt,
		FinishedAt:      task.FinishedAt,
		Assets:          make([]assetResponse, 0, len(task.Assets)),
	}
	for _, asset := range task.Assets {
		format, _ := s.application.ModelDelivery.FormatLabelOf(asset.Role, asset.Variant, asset.MediaType)
		result.Assets = append(result.Assets, assetResponse{
			Format:          format,
			DeliveryProfile: asset.DeliveryProfile,
			ID:              asset.ID,
			Role:            asset.Role,
			Variant:         asset.Variant,
			Required:        asset.Required,
			Delivery:        s.generation.AssetDeliveryFor(task, asset.Role, asset.Variant),
			Ordinal:         asset.Ordinal,
			MediaType:       asset.MediaType,
			Size:            asset.Size,
			SHA256:          asset.SHA256,
			URL: s.taskAssetRoute.Render(
				string(task.ID),
				string(asset.ID),
			),
		})
	}
	if !task.UpstreamJobCreatedAt.IsZero() {
		created := task.UpstreamJobCreatedAt
		result.UpstreamJobCreatedAt = &created
	}
	result.Resources = s.resourceResponses(locale, task)
	result.ResourcesPending = generation.ResourcesPending(task.Resources)
	return result
}

// resourceResponses 由 0022 资源态构建公开 resources[] 投影：槽位键
// "<role>|<variant 或 variant_prefix>|<group>"；delivery 由冻结快照/摘要
// 回退推导；reason 只输出文案键解析后的安全文本（未知键省略）。
func (s *Server) resourceResponses(locale string, task generation.Task) []resourceResponse {
	if len(task.Resources) == 0 {
		return nil
	}
	keys := make([]string, 0, len(task.Resources))
	for key := range task.Resources {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	resources := make([]resourceResponse, 0, len(keys))
	for _, key := range keys {
		role, scope, group, err := generation.ParseResourceStateKey(key)
		if err != nil {
			continue
		}
		state := task.Resources[key]
		response := resourceResponse{
			Role:  role,
			Group: group,
			State: string(state.State),
		}
		if strings.HasSuffix(scope, ".") && !strings.Contains(strings.TrimSuffix(scope, "."), ".") {
			// 前缀槽位（如 "converted."）：scope 即 variant_prefix。
			response.VariantPrefix = scope
		} else if scope != "" {
			response.Variant = scope
		}
		response.Delivery = s.generation.AssetDeliveryFor(task, role, scope)
		if state.Reason != "" {
			if message := s.messageIn(locale, state.Reason); message != "" {
				response.Reason = message
			}
		}
		resources = append(resources, response)
	}
	return resources
}

func (s *Server) taskPageResponse(locale string, page generation.TaskPage) map[string]any {
	tasks := make([]taskResponse, 0, len(page.Tasks))
	for _, task := range page.Tasks {
		tasks = append(tasks, s.newTaskResponse(locale, task))
	}
	return map[string]any{"tasks": tasks, "next_cursor": page.NextCursor}
}

type problem struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
	// UserFacing says Detail is the specific reason the request was refused,
	// written for the requester (#288), rather than the generic message.
	UserFacing bool   `json:"user_facing,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
}

func (s *Server) publicFailure(locale string, failure *generation.Failure) *publicFailure {
	if failure == nil {
		return nil
	}
	return &publicFailure{
		Class:     failure.Class,
		Retryable: failure.Retryable,
		Ambiguous: failure.Ambiguous,
		Detail:    s.messageIn(locale, "failure."+string(failure.Class)),
	}
}

// publicEvents 按 catalog 白名单把内核事件投影为公开事件面（§8.1
// SSE·事件行；P3）：application.runtime.task_events.public_projection
// 是唯一真源——未声明的事件类型整体丢弃；声明的类型只保留声明字段。
// failure 形状数据仍先改写为 publicFailure 投影（detail 以文案键解析、
// 上游取证字段永不出面），再经同一字段白名单收口；非对象或不可解析
// 的数据一律收敛为空对象（fail-closed，绝不透传未声明内容）。
func (s *Server) publicEvents(locale string, events []generation.Event) []generation.Event {
	result := make([]generation.Event, 0, len(events))
	for _, event := range events {
		allowed, declared := s.taskEventWhitelist[event.Type]
		if !declared {
			continue
		}
		var failure generation.Failure
		if err := json.Unmarshal(event.Data, &failure); err == nil &&
			failure.Class != "" {
			publicData, err := json.Marshal(s.publicFailure(locale, &failure))
			if err != nil {
				continue
			}
			event.Data = publicData
		}
		event.Data = pickPublicEventFields(event.Data, allowed)
		result = append(result, event)
	}
	return result
}

// pickPublicEventFields 只保留白名单字段；非对象数据返回空对象。
func pickPublicEventFields(
	data json.RawMessage,
	allowed map[string]struct{},
) json.RawMessage {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return json.RawMessage("{}")
	}
	picked := make(map[string]json.RawMessage, len(allowed))
	for key, value := range fields {
		if _, ok := allowed[key]; ok {
			picked[key] = value
		}
	}
	encoded, err := json.Marshal(picked)
	if err != nil {
		return json.RawMessage("{}")
	}
	return encoded
}

func (s *Server) message(key string) string {
	return s.application.Content.Messages[key]
}

// messageIn is a message in a UI locale ("" is the default one).
func (s *Server) messageIn(locale, key string) string {
	if s.texts == nil {
		return s.message(key)
	}
	return s.texts.Message(locale, key)
}

// textIn is the locale's copy of a default-locale Schema text.
func (s *Server) textIn(locale, text string) string {
	return s.texts.Text(locale, text)
}

// parameterAdjustmentsIn projects frozen adjustments with their Schema copy
// in the locale. The values, recommended values, names, sources and the
// person's reason are evidence and stay as written; the frozen input itself
// is not changed.
func (s *Server) parameterAdjustmentsIn(
	locale string,
	adjustments []generation.ParameterAdjustment,
) []generation.ParameterAdjustment {
	if adjustments == nil {
		return nil
	}
	localized := make([]generation.ParameterAdjustment, len(adjustments))
	copy(localized, adjustments)
	for index := range localized {
		localized[index].Label = s.textIn(locale, localized[index].Label)
		localized[index].Advice = s.textIn(locale, localized[index].Advice)
	}
	return localized
}

// localizedSchemaData returns value with every Schema text in the request
// locale. value must be made of Schema data only (offerings, tool
// descriptions), never of a person's content.
func (s *Server) localizedSchemaData(request *http.Request, value any) any {
	locale := requestLocale(request)
	if !s.texts.Translates(locale) {
		return value
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var generic any
	if err := decoder.Decode(&generic); err != nil {
		return value
	}
	return s.texts.LocalizeJSON(locale, generic)
}

// quoteIn is a frozen native quote with its fee item labels (offering copy) in
// the locale; the frozen quote itself is never changed. UnitLabel is Provider
// data, not Schema copy, and stays raw even when it equals a translated text.
// A label renamed since the quote was frozen has no translation of its own, so
// it reads the translation of the same-id fee item that the frozen Offering
// declares now, or, once that Offering is gone, the current Offering for the
// same model. With neither, the frozen text stays.
func (s *Server) quoteIn(locale string, frozen generation.Offering, quote *generation.NativeQuote) *generation.NativeQuote {
	if quote == nil || !s.texts.Translates(locale) {
		return quote
	}
	localized := *quote
	localized.Components = make([]schemacontract.NativeCharge, len(quote.Components))
	var current *schemacontract.NativePricing
	looked := false
	for index, component := range quote.Components {
		label := s.textIn(locale, component.Label)
		if label == component.Label {
			if !looked {
				current, looked = s.currentNativePricing(frozen), true
			}
			if declared := declaredChargeLabel(current, component.ID); declared != "" {
				if translated := s.textIn(locale, declared); translated != declared {
					label = translated
				}
			}
		}
		component.Label = label
		localized.Components[index] = component
	}
	return &localized
}

// currentNativePricing is the price declaration the frozen Offering has now:
// its own while it is registered, else that of the current Offering for the
// same provider, model and capability.
func (s *Server) currentNativePricing(frozen generation.Offering) *schemacontract.NativePricing {
	if s.generation == nil || frozen.ID == "" {
		return nil
	}
	if offering, ok := s.generation.Offering(frozen.ID); ok {
		return offering.NativePricing
	}
	for _, offering := range s.generation.Offerings(frozen.Capability) {
		if offering.ProviderID == frozen.ProviderID && offering.ModelID == frozen.ModelID && offering.NativePricing != nil {
			return offering.NativePricing
		}
	}
	return nil
}

// declaredChargeLabel is the label pricing declares for fee item id, or "".
func declaredChargeLabel(pricing *schemacontract.NativePricing, id string) string {
	if pricing == nil {
		return ""
	}
	for _, component := range pricing.Components {
		if component.ID == id {
			return component.Label
		}
	}
	if pricing.Selection != nil && pricing.Selection.ID == id {
		return pricing.Selection.Label
	}
	return ""
}

// requestLocale is the UI locale of the request; "" is the default one.
func requestLocale(request *http.Request) string {
	return catalog.LocaleFrom(request.Context())
}

// localeMessage is a message in the locale of the request being answered.
func (s *Server) localeMessage(writer http.ResponseWriter, key string) string {
	return traceFromWriter(writer).localeMessage(key, s.message(key))
}

func (s *Server) writeMessageProblem(
	writer http.ResponseWriter,
	status int,
	code string,
	messageKey string,
) {
	writeProblem(writer, status, code, s.message(messageKey))
}

func (s *Server) writeTemplatedMessageProblem(
	writer http.ResponseWriter,
	status int,
	code string,
	messageKey string,
	values map[string]string,
) {
	detail := s.localeMessage(writer, messageKey)
	for name, value := range values {
		detail = strings.ReplaceAll(detail, "{"+name+"}", value)
	}
	writeProblem(writer, status, code, detail)
}

func (s *Server) writeDomainError(writer http.ResponseWriter, err error) {
	noteCause(writer, err)
	var domainErr *generation.Error
	if !errors.As(err, &domainErr) {
		s.writeMessageProblem(
			writer,
			http.StatusInternalServerError,
			"internal",
			"error.internal",
		)
		return
	}
	status := http.StatusInternalServerError
	switch domainErr.Code {
	case generation.ErrInvalidCapability, generation.ErrInvalidInput, generation.ErrUnknownOffering,
		generation.ErrNoOffering, generation.ErrInvalidModelInput, generation.ErrUnsupportedModelInput, generation.ErrModelInputLimit:
		status = http.StatusBadRequest
	case generation.ErrIdempotencyConflict, generation.ErrConflict:
		status = http.StatusConflict
	case generation.ErrNotFound:
		status = http.StatusNotFound
	case generation.ErrForbidden:
		status = http.StatusForbidden
	case generation.ErrUnavailable:
		status = http.StatusServiceUnavailable
	case generation.ErrTooManyRequests:
		status = http.StatusTooManyRequests
	}
	if domainErr.RetryAfter > 0 {
		writer.Header().Set(
			"Retry-After",
			strconv.Itoa(int(domainErr.RetryAfter.Round(time.Second)/time.Second)),
		)
	}
	if status >= http.StatusInternalServerError && s.logger != nil {
		s.logger.Error(
			"domain operation failed",
			"operation", domainErr.Operation,
			"code", domainErr.Code,
			"cause", domainErr.Cause,
		)
	}
	if reason := userFacingReason(writer, status, err); reason != "" {
		writeReasonProblem(writer, status, string(domainErr.Code), reason)
		return
	}
	s.writeMessageProblem(
		writer,
		status,
		string(domainErr.Code),
		"error."+string(domainErr.Code),
	)
}

// userFacingReason is the requester-visible reason of a refused request
// (#288). Only a 400 answer carries one, and only when the error chain holds
// a Detail its source marked UserFacing: Schema refusal text about the
// requester's own input, such as a reference image over the model's pixel
// limit. Every other Detail is diagnostic (internal parameters, IDs, upstream
// bodies, paths) and the answer keeps the generic Schema message.
// The reason is written in the request's language from the Schema template
// it was filled from, when the refusal keeps one (#329 note 99647).
func userFacingReason(writer http.ResponseWriter, status int, err error) string {
	if status != http.StatusBadRequest {
		return ""
	}
	trace := traceFromWriter(writer)
	return generation.UserFacingError(err).LocalizedDetail(func(text string) string { return trace.localeText(text) })
}

// writeReasonProblem answers with a user-facing reason in detail and marks it
// so clients show it instead of their own generic failure text.
func writeReasonProblem(writer http.ResponseWriter, status int, code, reason string) {
	body := problem{Code: code, Detail: reason, UserFacing: true}
	if trace := traceFromWriter(writer); trace != nil {
		trace.noteProblem(code, reason, nil)
		body.RequestID = trace.id
	}
	writeJSON(writer, status, body)
}

func (s *Server) writeLegacyDomainError(
	writer http.ResponseWriter,
	err error,
) {
	noteCause(writer, err)
	var domainErr *generation.Error
	if !errors.As(err, &domainErr) {
		s.writeLegacyError(
			writer,
			http.StatusInternalServerError,
			"error.internal",
		)
		return
	}
	status := http.StatusInternalServerError
	switch domainErr.Code {
	case generation.ErrInvalidCapability, generation.ErrInvalidInput, generation.ErrUnknownOffering,
		generation.ErrNoOffering, generation.ErrInvalidModelInput, generation.ErrUnsupportedModelInput, generation.ErrModelInputLimit:
		status = http.StatusBadRequest
	case generation.ErrIdempotencyConflict, generation.ErrConflict:
		status = http.StatusConflict
	case generation.ErrNotFound:
		status = http.StatusNotFound
	case generation.ErrForbidden:
		status = http.StatusForbidden
	case generation.ErrUnavailable:
		status = http.StatusServiceUnavailable
	case generation.ErrTooManyRequests:
		status = http.StatusTooManyRequests
	}
	if domainErr.RetryAfter > 0 {
		writer.Header().Set(
			"Retry-After",
			strconv.Itoa(int(domainErr.RetryAfter.Round(time.Second)/time.Second)),
		)
	}
	if reason := userFacingReason(writer, status, err); reason != "" {
		writeJSON(writer, status, map[string]string{s.application.Authentication.API.ErrorField: reason})
		return
	}
	s.writeLegacyError(
		writer,
		status,
		"error."+string(domainErr.Code),
	)
}

// writeProblem answers with the public code and detail. Internal causes go
// only to the access log (#233), together with the request ID the body names.
func writeProblem(writer http.ResponseWriter, status int, code, detail string, causes ...error) {
	// A detail that is Schema copy goes out in the request's locale; the access log keeps the default text.
	body := problem{Code: code, Detail: traceFromWriter(writer).localeText(detail)}
	if trace := traceFromWriter(writer); trace != nil {
		trace.noteProblem(code, detail, causes)
		body.RequestID = trace.id
	}
	writeJSON(writer, status, body)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	// Any JSON body may carry Schema copy in the request's UI locale (an
	// error detail, an offering label), so a shared cache must key on it.
	if trace := traceFromWriter(writer); trace != nil && trace.locale != "" {
		httplocale.MarkResponse(writer.Header(), trace.locale)
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	// #316: every JSON error body names the request ID the access log records
	// (the X-Request-ID header), whichever writer built it.
	if trace := traceFromWriter(writer); status >= http.StatusBadRequest && trace != nil {
		value = withRequestID(value, trace.id)
	}
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

// withRequestID adds request_id to a JSON object body that has none. Other
// bodies (lists, scalars) and bodies that already carry a request_id of
// their own are returned unchanged.
func withRequestID(value any, requestID string) any {
	encoded, err := json.Marshal(value)
	if err != nil || len(bytes.TrimSpace(encoded)) == 0 || bytes.TrimSpace(encoded)[0] != '{' {
		return value
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(encoded, &object) != nil {
		return value
	}
	if _, present := object["request_id"]; present {
		return value
	}
	quoted, _ := json.Marshal(requestID)
	object["request_id"] = quoted
	return object
}

func recoverMiddleware(
	logger *slog.Logger,
	internalErrorText string,
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error(
					"HTTP handler panic",
					"method", request.Method,
					"path", request.URL.Path,
					"panic", recovered,
				)
				writeProblem(
					writer,
					http.StatusInternalServerError,
					"internal",
					internalErrorText,
				)
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

func requestLogMiddleware(
	logger *slog.Logger,
	providerAssetRoute routing.One,
	webhookSecretRoutes []routing.One,
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		trace := newRequestTrace()
		writer.Header().Set(requestIDHeader, trace.id)
		traced := request.WithContext(context.WithValue(request.Context(), requestTraceKey{}, trace))
		recorder := &statusRecorder{ResponseWriter: writer, trace: trace}
		next.ServeHTTP(recorder, traced)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		attributes := []any{
			"request_id", trace.id,
			"method", request.Method,
			"path", safeLogPath(request.URL.Path, providerAssetRoute, webhookSecretRoutes),
			"status", status,
			"duration_ms", time.Since(started).Milliseconds(),
		}
		// routeTraceMiddleware copies the ServeMux match into the trace: the
		// request the mux served is a copy of traced once any middleware in
		// between calls WithContext.
		trace.mu.Lock()
		if trace.route != "" {
			attributes = append(attributes, "route", safeLogPath(trace.route, providerAssetRoute, webhookSecretRoutes))
		}
		if trace.sessionID != "" {
			attributes = append(attributes, "session_id", trace.sessionID)
		}
		if trace.owner != "" {
			attributes = append(attributes, "owner_id", trace.owner)
		}
		if trace.code != "" {
			attributes = append(attributes, "error_code", trace.code, "error_detail", trace.detail)
		}
		if len(trace.causes) > 0 {
			attributes = append(attributes, "cause", strings.Join(trace.causes, "; "))
		}
		trace.mu.Unlock()
		level := slog.LevelInfo
		switch {
		case status >= 500:
			level = slog.LevelError
		case status >= 400:
			level = slog.LevelWarn
		}
		logger.Log(request.Context(), level, "HTTP request", attributes...)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
	trace   *requestTrace
}

func (recorder *statusRecorder) WriteHeader(status int) {
	if !recorder.written {
		recorder.status = status
		recorder.written = true
	}
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *statusRecorder) Write(data []byte) (int, error) {
	if !recorder.written {
		recorder.status = http.StatusOK
		recorder.written = true
	}
	return recorder.ResponseWriter.Write(data)
}

func (recorder *statusRecorder) Unwrap() http.ResponseWriter { return recorder.ResponseWriter }

func (recorder *statusRecorder) FlushError() error {
	if !recorder.written {
		recorder.status = http.StatusOK
		recorder.written = true
	}
	return http.NewResponseController(recorder.ResponseWriter).Flush()
}

func (recorder *statusRecorder) Flush() { _ = recorder.FlushError() }

func safeLogPath(
	value string,
	providerAssetRoute routing.One,
	webhookSecretRoutes []routing.One,
) string {
	if redacted, matched := providerAssetRoute.Redact(
		value,
		"redacted",
	); matched {
		return redacted
	}
	for _, route := range webhookSecretRoutes {
		if redacted, matched := route.Redact(value, "redacted"); matched {
			return redacted
		}
	}
	return value
}

var homeDocument = template.Must(template.New("home").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>{{.Title}}</title>
  <style>
    body{font:16px/1.6 system-ui,sans-serif;max-width:780px;margin:64px auto;padding:0 24px;background:#111;color:#eee}
    code{background:#242424;padding:3px 7px;border-radius:5px} a{color:#8ab4ff}
  </style>
</head>
<body>
  <h1>{{.Title}}</h1>
  <p>{{.Description}}</p>
  {{range .Links}}<p><a href="{{.Href}}">{{.Label}}</a>{{if .Method}} · <code>{{.Method}} {{.Href}}</code>{{end}}</p>{{end}}
</body>
</html>`))

func Shutdown(server *http.Server, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return server.Shutdown(ctx)
}
