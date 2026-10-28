package catalog

// Runtime configuration, cache and task operational policies.
import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

func validateRuntime(schema RuntimeSchema) error {
	if err := schema.TaskRetention.Validate(); err != nil {
		return err
	}
	if schema.StorageMaintenance.MinimumAgeSeconds <= 0 || schema.StorageMaintenance.MinimumAgeSeconds > int64((1<<63-1)/time.Second) {
		return errors.New("storage_maintenance.minimum_age_seconds 必须是可表示的正时长")
	}
	if err := validateRuntimeConfiguration(schema.Configuration); err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	if err := validateProviderConnections(schema); err != nil {
		return fmt.Errorf("provider_connections: %w", err)
	}
	if err := validateProviderEnvironmentNames(schema); err != nil {
		return err
	}
	if err := validateLegacyMigrationRuntime(schema.LegacyMigration); err != nil {
		return fmt.Errorf("legacy_migration: %w", err)
	}
	if err := validateTaskEvents(schema.TaskEvents); err != nil {
		return fmt.Errorf("task_events: %w", err)
	}
	messages := schema.GenerationMessages
	if messages.InlineStoreFailed == "" || messages.StoredAssetInvalid == "" || messages.SubmissionUnknown == "" {
		return errors.New("generation_messages 必须声明图片存储与含糊提交错误说明")
	}
	// #329 note 99647: one reference-limit text for every place that says it,
	// the general form with the count and a one-picture form.
	if limit := messages.ReferenceLimit; limit != "" && (!strings.Contains(limit, "{model}") ||
		!strings.Contains(limit, "{count}") || !strings.Contains(limit, "{total}")) {
		return errors.New("generation_messages.reference_limit 必须包含 {model}、{count} 与 {total}")
	}
	if one := messages.ReferenceLimitOne; one != "" && (messages.ReferenceLimit == "" ||
		!strings.Contains(one, "{model}") || !strings.Contains(one, "{total}")) {
		return errors.New("generation_messages.reference_limit_one 需要同时声明 reference_limit，并包含 {model} 与 {total}")
	}
	// A refused over-limit list is a different answer from the cut note: it
	// must name the public label and both counts so the person can act (#329).
	if neutral := messages.ReferenceSelectionNote; neutral != "" &&
		(!strings.Contains(neutral, "{model}") || !strings.Contains(neutral, "{count}")) {
		return errors.New("generation_messages.reference_selection_note 必须包含 {model} 与 {count}")
	}
	if capacity := messages.ReferenceCapacity; capacity != "" &&
		(!strings.Contains(capacity, "{model}") || !strings.Contains(capacity, "{count}") ||
			!strings.Contains(capacity, "{total}")) {
		return errors.New("generation_messages.reference_capacity 必须包含 {model}、{count} 与 {total}")
	}
	if exceeded := messages.ReferenceLimitExceeded; exceeded != "" &&
		(!strings.Contains(exceeded, "{label}") || !strings.Contains(exceeded, "{max}") ||
			!strings.Contains(exceeded, "{actual}")) {
		return errors.New("generation_messages.reference_limit_exceeded 必须包含 {label}、{max} 与 {actual}")
	}
	// A synchronous input refusal names the facts the person can act on
	// (#358); a template without its placeholders would drop those facts.
	for name, check := range map[string]struct {
		text         string
		placeholders []string
	}{
		"input_source_too_large":        {messages.InputSourceTooLarge, []string{"{size}", "{limit}"}},
		"input_source_media_type":       {messages.InputSourceMediaType, []string{"{media_type}", "{formats}"}},
		"input_source_dimensions":       {messages.InputSourceDimensions, []string{"{width}", "{height}", "{min_width}", "{min_height}", "{max_pixels}"}},
		"input_source_pixels":           {messages.InputSourcePixels, []string{"{width}", "{height}", "{max_pixels}"}},
		"input_parameter_invalid":       {messages.InputParameterInvalid, []string{"{label}"}},
		"input_source_task_expired":     {messages.InputSourceTaskExpired, []string{"{label}"}},
		"input_source_task_unsupported": {messages.InputSourceTaskUnsupported, []string{"{label}"}},
	} {
		if check.text == "" {
			continue
		}
		for _, placeholder := range check.placeholders {
			if !strings.Contains(check.text, placeholder) {
				return fmt.Errorf("generation_messages.%s 必须包含 %s", name, placeholder)
			}
		}
	}
	for _, connection := range schema.ProviderConnections {
		if connection.ResultRefetch != nil && (messages.RefetchSourceUnavailable == "" || messages.RefetchResultUnavailable == "") {
			return errors.New("声明 result_refetch 时 generation_messages 必须声明重新取回失败的说明")
		}
	}
	policy := schema.TaskCancellation
	if policy.TimeoutSeconds <= 0 || policy.LocalOnly == "" || policy.Requesting == "" || policy.Confirmed == "" || policy.Failed == "" || policy.Unsupported == "" || policy.Unknown == "" {
		return errors.New("task_cancellation 必须声明调用超时和各取消结果的说明")
	}
	coordinator := schema.Coordinator
	if coordinator.PollIntervalMilliseconds <= 0 ||
		coordinator.LeaseSeconds <= 0 ||
		coordinator.BatchSize <= 0 || coordinator.Concurrency <= 0 || coordinator.Concurrency > 100 || coordinator.LeaseRenewMilliseconds <= 0 || coordinator.LeaseRenewMilliseconds >= coordinator.LeaseSeconds*1000 || coordinator.MaxBackoffMilliseconds < coordinator.PollIntervalMilliseconds {
		return errors.New("coordinator 轮询、租约、批量及退避必须为正，并发度应在 1 到 100，续租间隔必须小于租约时长")
	}
	if coordinator.DispatchCheckpointMaxBytes < 0 {
		return errors.New("coordinator dispatch_checkpoint_max_bytes 不能为负")
	}
	if len(schema.ProviderAccounts) == 0 {
		return errors.New("provider_accounts 不能为空")
	}
	for accountID, account := range schema.ProviderAccounts {
		if strings.TrimSpace(accountID) == "" ||
			strings.TrimSpace(account.ProviderID) == "" ||
			strings.TrimSpace(account.BaseURLSetting) == "" ||
			strings.TrimSpace(account.APIKeySetting) == "" {
			return fmt.Errorf("provider_accounts[%q] 配置不完整", accountID)
		}
		for _, setting := range []string{
			account.BaseURLSetting,
			account.APIKeySetting,
		} {
			if _, exists := schema.Configuration.Settings[setting]; !exists {
				return fmt.Errorf(
					"provider_accounts[%q] 引用了未知运行设置 %q",
					accountID,
					setting,
				)
			}
		}
		balance := account.Balance
		if balance.NativeUnit != "" || balance.NativeUnitLabel != "" || balance.MockNativeAmount != "" {
			if err := (schemacontract.NativeAmount{Unit: balance.NativeUnit, UnitLabel: balance.NativeUnitLabel, Amount: balance.MockNativeAmount}).Validate(); err != nil {
				return fmt.Errorf("provider_accounts[%q].balance native configuration invalid: %w", accountID, err)
			}
		}
		if strings.TrimSpace(balance.AdapterKey) == "" ||
			!strings.HasPrefix(balance.EndpointPath, "/") ||
			strings.TrimSpace(balance.AuthorizationHeader) == "" ||
			strings.TrimSpace(balance.UpstreamBalanceField) == "" ||
			balance.RequestTimeoutSeconds <= 0 ||
			balance.ResponseMaxBytes <= 0 ||
			balance.ErrorBodyMaxBytes <= 0 ||
			balance.MockValue < 0 {
			return fmt.Errorf(
				"provider_accounts[%q].balance 配置无效",
				accountID,
			)
		}
	}
	httpRuntime := schema.HTTP
	for name, value := range map[string]int{
		"read_header_timeout_seconds": httpRuntime.ReadHeaderTimeoutSeconds,
		"read_timeout_seconds":        httpRuntime.ReadTimeoutSeconds,
		"write_timeout_seconds":       httpRuntime.WriteTimeoutSeconds,
		"idle_timeout_seconds":        httpRuntime.IdleTimeoutSeconds,
		"shutdown_timeout_seconds":    httpRuntime.ShutdownTimeoutSeconds,
		"default_page_size":           httpRuntime.DefaultPageSize,
		"maximum_page_size":           httpRuntime.MaximumPageSize,
		"maximum_recent_tasks":        httpRuntime.MaximumRecentTasks,
		"maximum_lineage_tasks":       httpRuntime.MaximumLineageTasks,
		"lineage_page_size":           httpRuntime.LineagePageSize,
	} {
		if value <= 0 {
			return fmt.Errorf("http.%s 必须大于 0", name)
		}
	}
	if httpRuntime.DefaultPageSize > httpRuntime.MaximumPageSize {
		return errors.New("http.default_page_size 不能大于 maximum_page_size")
	}
	if httpRuntime.LineagePageSize > httpRuntime.MaximumLineageTasks {
		return errors.New("http.lineage_page_size 不能大于 maximum_lineage_tasks")
	}
	if httpRuntime.LineagePageSize > httpRuntime.MaximumPageSize {
		return errors.New("http.lineage_page_size 不能大于 maximum_page_size")
	}
	if httpRuntime.TaskAdmission.PerOwnerMaxInflightTasks <= 0 {
		return errors.New("http.task_admission.per_owner_max_inflight_tasks 必须大于 0")
	}
	if httpRuntime.JSONBodyMaxBytes <= 0 || httpRuntime.MultipartBodyMaxBytes <= 0 {
		return errors.New("http 请求体上限必须大于 0")
	}
	if len(httpRuntime.SecurityHeaders) == 0 {
		return errors.New("http.security_headers 不能为空")
	}
	if strings.TrimSpace(httpRuntime.PrivateCacheControl) == "" ||
		strings.TrimSpace(httpRuntime.ImmutableAssetCacheControl) == "" ||
		strings.TrimSpace(httpRuntime.SchemaCacheControl) == "" {
		return errors.New("http cache-control 配置不能为空")
	}
	if httpRuntime.PrivateCacheControl != "private, no-store" {
		return errors.New("鉴权资产缓存策略必须是 private, no-store")
	}
	if err := validateBuildAssetCacheControl(httpRuntime.ImmutableAssetCacheControl); err != nil {
		return err
	}
	if err := validateThumbnailCache(httpRuntime); err != nil {
		return err
	}
	// agent 路由为加性声明：旧/外部 catalog 缺省时回填默认路径，保持加载兼容。
	if httpRuntime.Routes.AgentParams == "" {
		httpRuntime.Routes.AgentParams = "/api/v1/agent/params"
	}
	if httpRuntime.Routes.AgentBind == "" {
		httpRuntime.Routes.AgentBind = "/api/v1/agent/bind"
	}
	if httpRuntime.Routes.AgentConsent == "" {
		httpRuntime.Routes.AgentConsent = "/api/v1/agent/consent"
	}
	if httpRuntime.Routes.ProcessingPlans == "" {
		httpRuntime.Routes.ProcessingPlans = "/api/v1/processing-plans"
	}
	if httpRuntime.Routes.ProcessingPlanItem == "" {
		httpRuntime.Routes.ProcessingPlanItem = "/api/v1/processing-plans/{planID}"
	}
	if httpRuntime.Routes.ProcessingPlanRetry == "" {
		httpRuntime.Routes.ProcessingPlanRetry = "/api/v1/processing-plans/{planID}/retry"
	}
	if httpRuntime.Routes.ProcessingPlanResume == "" {
		httpRuntime.Routes.ProcessingPlanResume = "/api/v1/processing-plans/{planID}/resume"
	}
	if limit := httpRuntime.APIRateLimit; limit.RequestsPerMinute < 0 ||
		(limit.RequestsPerMinute > 0 && !strings.HasPrefix(limit.PathPrefix, "/")) {
		return errors.New("http.api_rate_limit 配置无效")
	}
	if report := httpRuntime.ClientErrorReport; httpRuntime.Routes.ClientErrors != "" &&
		(report.MaxBodyBytes <= 0 || report.PerOwnerPerMinute <= 0 || report.MaxTextCharacters <= 0) {
		return errors.New("http.client_error_report 的上限必须为正数")
	}
	if report := httpRuntime.ClientErrorReport; report.AnonymousGlobalPerMinute < 0 ||
		(report.AnonymousGlobalPerMinute > 0 && (report.AnonymousMaxBodyBytes <= 0 || report.AnonymousMaxBodyBytes > report.MaxBodyBytes ||
			report.AnonymousPerClientPerMinute <= 0 || report.AnonymousPerClientPerMinute > report.AnonymousGlobalPerMinute ||
			report.AnonymousMaxTextCharacters <= 0 || report.AnonymousMaxTextCharacters > report.MaxTextCharacters)) {
		return errors.New("http.client_error_report 的匿名上限必须为正数且不大于登录后的上限")
	}
	coreRoutes := map[string]string{
		"home":                   httpRuntime.Routes.Home,
		"liveness":               httpRuntime.Routes.Liveness,
		"readiness":              httpRuntime.Routes.Readiness,
		"product_schema":         httpRuntime.Routes.ProductSchema,
		"chat_skills":            httpRuntime.Routes.ChatSkills,
		"provider_asset":         httpRuntime.Routes.ProviderAsset,
		"offerings":              httpRuntime.Routes.Offerings,
		"assets":                 httpRuntime.Routes.Assets,
		"asset_item":             httpRuntime.Routes.AssetItem,
		"tasks":                  httpRuntime.Routes.Tasks,
		"task_item":              httpRuntime.Routes.TaskItem,
		"processing_plans":       httpRuntime.Routes.ProcessingPlans,
		"processing_plan_item":   httpRuntime.Routes.ProcessingPlanItem,
		"processing_plan_retry":  httpRuntime.Routes.ProcessingPlanRetry,
		"processing_plan_resume": httpRuntime.Routes.ProcessingPlanResume,
		"task_cancel":            httpRuntime.Routes.TaskCancel,
		"task_events":            httpRuntime.Routes.TaskEvents,
		"task_asset":             httpRuntime.Routes.TaskAsset,
	}
	// Newly introduced admission routes are optional in historical Catalog
	// fixtures. The shipped Product Schema declares these routes; older snapshots
	// remain readable and the HTTP layer disables their handlers when absent.
	for name, route := range map[string]string{
		"asset_model_preview": httpRuntime.Routes.AssetModelPreview, "processing_plan_reconcile": httpRuntime.Routes.ProcessingPlanReconcile,
		"chat_session_approval":           httpRuntime.Routes.ChatSessionApproval,
		"chat_session_notes":              httpRuntime.Routes.ChatSessionNotes,
		"chat_session_pick":               httpRuntime.Routes.ChatSessionPick,
		"chat_session_choices":            httpRuntime.Routes.ChatSessionChoices,
		"chat_session_agent_scope":        httpRuntime.Routes.ChatSessionAgentScope,
		"chat_session_reasoning":          httpRuntime.Routes.ChatSessionReasoning,
		"chat_session_context":            httpRuntime.Routes.ChatSessionContext,
		"chat_session_context_compaction": httpRuntime.Routes.ChatSessionContextCompaction,
		"chat_session_model":              httpRuntime.Routes.ChatSessionModel,
		"chat_session_harness":            httpRuntime.Routes.ChatSessionHarness,
		"chat_session_pending_approvals":  httpRuntime.Routes.ChatSessionPendingApprovals,
		"chat_session_three_views":        httpRuntime.Routes.ChatSessionThreeViews,
		"chat_session_three_view_item":    httpRuntime.Routes.ChatSessionThreeViewItem,
		"chat_session_lineage":            httpRuntime.Routes.ChatSessionLineage,
		"task_prepare":                    httpRuntime.Routes.TaskPrepare,
		"task_recovery":                   httpRuntime.Routes.TaskRecovery,
		"task_recovery_prepare":           httpRuntime.Routes.TaskRecoveryPrepare,
		"task_refetch":                    httpRuntime.Routes.TaskRefetch,
		"task_grants":                     httpRuntime.Routes.TaskGrants,
		"task_grant_decision":             httpRuntime.Routes.TaskGrantDecision,
		"model_captures":                  httpRuntime.Routes.ModelCaptures,
		"model_capture_item":              httpRuntime.Routes.ModelCaptureItem,
		"scene_objects":                   httpRuntime.Routes.SceneObjects,
		"scene_object_item":               httpRuntime.Routes.SceneObjectItem,
		"scene_object_representations":    httpRuntime.Routes.SceneObjectRepresentations,
		"scene_object_apply":              httpRuntime.Routes.SceneObjectApply,
		"scene_snapshot":                  httpRuntime.Routes.SceneSnapshot,
		"render_manifests":                httpRuntime.Routes.RenderManifests,
		"render_manifest_item":            httpRuntime.Routes.RenderManifestItem,
		"skill_runs":                      httpRuntime.Routes.SkillRuns,
		"skill_run_item":                  httpRuntime.Routes.SkillRunItem,
		"skill_run_step":                  httpRuntime.Routes.SkillRunStep,
		"skill_run_review":                httpRuntime.Routes.SkillRunReview,
		"model_formats":                   httpRuntime.Routes.ModelFormats,
		"model_formats_complete":          httpRuntime.Routes.ModelFormatsComplete,
		"model_archive":                   httpRuntime.Routes.ModelArchive,
		"client_errors":                   httpRuntime.Routes.ClientErrors,
	} {
		if route != "" {
			coreRoutes[name] = route
		}
	}
	seenCoreRoutes := map[string]string{}
	for name, route := range coreRoutes {
		if !strings.HasPrefix(route, "/") {
			return fmt.Errorf("http core route %s 无效: %q", name, route)
		}
		if previous, exists := seenCoreRoutes[route]; exists {
			return fmt.Errorf(
				"http core route 重复: %s 与 %s 都是 %q",
				previous,
				name,
				route,
			)
		}
		seenCoreRoutes[route] = name
	}
	requiredRouteParameters := map[string][]string{
		"provider_asset":                  {"token"},
		"asset_item":                      {"assetID"},
		"asset_model_preview":             {"assetID"},
		"task_item":                       {"taskID"},
		"task_recovery":                   {"taskID"},
		"task_refetch":                    {"taskID"},
		"task_recovery_prepare":           {"taskID"},
		"task_grant_decision":             {"requestID"},
		"chat_session_approval":           {"sessionID"},
		"chat_session_notes":              {"sessionID"},
		"chat_session_pick":               {"sessionID"},
		"chat_session_choices":            {"sessionID"},
		"chat_session_agent_scope":        {"sessionID"},
		"chat_session_reasoning":          {"sessionID"},
		"chat_session_context":            {"sessionID"},
		"chat_session_context_compaction": {"sessionID"},
		"chat_session_model":              {"sessionID"},
		"chat_session_harness":            {"sessionID"},
		"chat_session_pending_approvals":  {"sessionID"},
		"chat_session_three_views":        {"sessionID"},
		"chat_session_three_view_item":    {"sessionID", "runID", "viewKey"},
		"chat_session_lineage":            {"sessionID"},
		"model_capture_item":              {"captureID"},
		"scene_objects":                   {"sessionID"},
		"scene_object_item":               {"sessionID", "objectID"},
		"scene_object_representations":    {"sessionID", "objectID"},
		"scene_object_apply":              {"sessionID", "objectID"},
		"scene_snapshot":                  {"sessionID"},
		"render_manifests":                {"sessionID"},
		"render_manifest_item":            {"sessionID", "manifestID"},
		"skill_run_item":                  {"runID"},
		"skill_run_step":                  {"runID", "stepKey"},
		"skill_run_review":                {"runID", "stepKey"},
		"processing_plan_item":            {"planID"},
		"processing_plan_retry":           {"planID"},
		"processing_plan_resume":          {"planID"},
		"processing_plan_reconcile":       {"planID"},
		"task_cancel":                     {"taskID"},
		"task_retry":                      {"taskID"},
		"task_events":                     {"taskID"},
		"task_asset":                      {"taskID", "assetID"},
		"model_formats":                   {"taskID"},
		"model_formats_complete":          {"taskID"},
		"model_archive":                   {"taskID"},
	}
	for name, route := range coreRoutes {
		parameters := requiredRouteParameters[name]
		if !configuredRouteParametersMatch(route, parameters) {
			return fmt.Errorf(
				"http core route %s 参数必须恰好为 %v",
				name,
				parameters,
			)
		}
	}
	legacyRoutes := []string{
		httpRuntime.LegacyRoutes.Health,
		httpRuntime.LegacyRoutes.Providers,
		httpRuntime.LegacyRoutes.Tasks,
		httpRuntime.LegacyRoutes.Version,
		httpRuntime.LegacyRoutes.AssetUpload,
		httpRuntime.LegacyRoutes.AssetItem,
		httpRuntime.LegacyRoutes.PromptEnhance,
		httpRuntime.LegacyRoutes.AuthLogin,
		httpRuntime.LegacyRoutes.AuthMock,
		httpRuntime.LegacyRoutes.AuthSession,
		httpRuntime.LegacyRoutes.CacheStatus,
		httpRuntime.LegacyRoutes.GeneratedImage,
		httpRuntime.LegacyRoutes.ChatSessions,
		httpRuntime.LegacyRoutes.ChatSession,
		httpRuntime.LegacyRoutes.ChatMessages,
		httpRuntime.LegacyRoutes.ChatMessage,
		httpRuntime.LegacyRoutes.ChatStream,
		httpRuntime.LegacyRoutes.ProviderBalance,
		httpRuntime.LegacyRoutes.CanvasProjects,
		httpRuntime.LegacyRoutes.CanvasProject,
		httpRuntime.LegacyRoutes.CanvasNodes,
		httpRuntime.LegacyRoutes.CanvasMerge,
		httpRuntime.LegacyRoutes.TaskStream,
		httpRuntime.LegacyRoutes.AdminUsers,
		httpRuntime.LegacyRoutes.AdminUser,
		httpRuntime.LegacyRoutes.AdminAuditLogs,
		httpRuntime.LegacyRoutes.AdminGenerations,
		httpRuntime.LegacyRoutes.AdminGeneration,
		httpRuntime.LegacyRoutes.AdminGenerationDownload,
		httpRuntime.LegacyRoutes.AdminUsage,
		httpRuntime.LegacyRoutes.AdminBalanceSummary,
		httpRuntime.LegacyRoutes.AdminUserAssets,
		httpRuntime.LegacyRoutes.AdminBackfillStatus,
		httpRuntime.LegacyRoutes.MeshyProxyAsset,
	}
	seenLegacyRoutes := map[string]struct{}{}
	for _, route := range legacyRoutes {
		if !strings.HasPrefix(route, "/") {
			return fmt.Errorf("http legacy route 无效: %q", route)
		}
		if _, exists := seenLegacyRoutes[route]; exists {
			return fmt.Errorf("http legacy route 重复: %q", route)
		}
		seenLegacyRoutes[route] = struct{}{}
	}
	if !strings.Contains(
		httpRuntime.LegacyRoutes.GeneratedImage,
		"{filename}",
	) {
		return errors.New(
			"http legacy generated_image route 缺少 {filename}",
		)
	}
	for name, route := range map[string]string{
		"asset_item":       httpRuntime.LegacyRoutes.AssetItem,
		"chat_session":     httpRuntime.LegacyRoutes.ChatSession,
		"chat_messages":    httpRuntime.LegacyRoutes.ChatMessages,
		"chat_stream":      httpRuntime.LegacyRoutes.ChatStream,
		"chat_turn_cancel": httpRuntime.LegacyRoutes.ChatTurnCancel,
	} {
		if len(configuredRouteParameters(route)) != 1 {
			return fmt.Errorf(
				"http legacy %s route 必须声明一个路径参数",
				name,
			)
		}
	}
	if len(configuredRouteParameters(
		httpRuntime.LegacyRoutes.ChatMessage,
	)) != 2 {
		return errors.New(
			"http legacy chat_message route 必须声明两个路径参数",
		)
	}
	providerBalance := httpRuntime.LegacyProviderBalance
	if len(providerBalance.Accounts) == 0 {
		return errors.New(
			"http.legacy_provider_balance 至少声明一个账户",
		)
	}
	seenAccounts := map[string]struct{}{}
	for _, account := range providerBalance.Accounts {
		if _, exists :=
			schema.ProviderAccounts[account.AccountID]; !exists {
			return fmt.Errorf(
				"http.legacy_provider_balance 引用了未知账户 %q",
				account.AccountID,
			)
		}
		if _, duplicate := seenAccounts[account.AccountID]; duplicate {
			return fmt.Errorf(
				"http.legacy_provider_balance 账户 %q 重复声明",
				account.AccountID,
			)
		}
		seenAccounts[account.AccountID] = struct{}{}
		for _, estimate := range account.ConsumptionEstimates {
			if estimate.Credits <= 0 {
				return fmt.Errorf(
					"http.legacy_provider_balance 账户 %q 的消耗估算积分必须为正数",
					account.AccountID,
				)
			}
		}
	}
	if strings.TrimSpace(providerBalance.BalanceField) == "" ||
		strings.TrimSpace(providerBalance.ErrorField) == "" {
		return errors.New(
			"http.legacy_provider_balance 字段配置不能为空",
		)
	}
	proxy := httpRuntime.CandidateProxy
	if !strings.HasPrefix(proxy.TaskPrefix, "/") ||
		!strings.HasPrefix(proxy.CorePrefix, "/") {
		return errors.New("http.candidate_proxy 的 task_prefix 和 core_prefix 必须以 / 开头")
	}
	legacyRouteByKey := map[string]string{
		"health":            httpRuntime.LegacyRoutes.Health,
		"providers":         httpRuntime.LegacyRoutes.Providers,
		"tasks":             httpRuntime.LegacyRoutes.Tasks,
		"version":           httpRuntime.LegacyRoutes.Version,
		"asset_upload":      httpRuntime.LegacyRoutes.AssetUpload,
		"asset_item":        httpRuntime.LegacyRoutes.AssetItem,
		"prompt_enhance":    httpRuntime.LegacyRoutes.PromptEnhance,
		"auth_login":        httpRuntime.LegacyRoutes.AuthLogin,
		"auth_mock":         httpRuntime.LegacyRoutes.AuthMock,
		"auth_session":      httpRuntime.LegacyRoutes.AuthSession,
		"cache_status":      httpRuntime.LegacyRoutes.CacheStatus,
		"generated_image":   httpRuntime.LegacyRoutes.GeneratedImage,
		"chat_sessions":     httpRuntime.LegacyRoutes.ChatSessions,
		"chat_session":      httpRuntime.LegacyRoutes.ChatSession,
		"chat_messages":     httpRuntime.LegacyRoutes.ChatMessages,
		"chat_message":      httpRuntime.LegacyRoutes.ChatMessage,
		"chat_stream":       httpRuntime.LegacyRoutes.ChatStream,
		"chat_turn_cancel":  httpRuntime.LegacyRoutes.ChatTurnCancel,
		"provider_balance":  httpRuntime.LegacyRoutes.ProviderBalance,
		"canvas_projects":   httpRuntime.LegacyRoutes.CanvasProjects,
		"canvas_project":    httpRuntime.LegacyRoutes.CanvasProject,
		"canvas_nodes":      httpRuntime.LegacyRoutes.CanvasNodes,
		"canvas_merge":      httpRuntime.LegacyRoutes.CanvasMerge,
		"task_stream":       httpRuntime.LegacyRoutes.TaskStream,
		"admin_users":       httpRuntime.LegacyRoutes.AdminUsers,
		"admin_user":        httpRuntime.LegacyRoutes.AdminUser,
		"admin_audit_logs":  httpRuntime.LegacyRoutes.AdminAuditLogs,
		"admin_generations": httpRuntime.LegacyRoutes.AdminGenerations,
		"admin_generation":  httpRuntime.LegacyRoutes.AdminGeneration,
		"admin_generation_download": httpRuntime.LegacyRoutes.
			AdminGenerationDownload,
		"admin_usage":           httpRuntime.LegacyRoutes.AdminUsage,
		"admin_balance_summary": httpRuntime.LegacyRoutes.AdminBalanceSummary,
		"admin_user_assets":     httpRuntime.LegacyRoutes.AdminUserAssets,
		"admin_backfill_status": httpRuntime.LegacyRoutes.AdminBackfillStatus,
		"meshy_proxy_asset":     httpRuntime.LegacyRoutes.MeshyProxyAsset,
	}
	seenProxyKeys := map[string]struct{}{}
	for _, key := range proxy.LegacyRouteKeys {
		if _, exists := legacyRouteByKey[key]; !exists {
			return fmt.Errorf(
				"http.candidate_proxy 引用了未知 legacy route %q",
				key,
			)
		}
		if _, exists := seenProxyKeys[key]; exists {
			return fmt.Errorf(
				"http.candidate_proxy legacy route 重复: %q",
				key,
			)
		}
		seenProxyKeys[key] = struct{}{}
	}
	if len(seenProxyKeys) == 0 {
		return errors.New("http.candidate_proxy.legacy_route_keys 不能为空")
	}
	if err := validateInputAssetRuntime(schema.InputAssets); err != nil {
		return fmt.Errorf("input_assets: %w", err)
	}
	legacyInputRootSetting := strings.TrimSpace(
		schema.InputAssets.LegacyStorageRootSetting,
	)
	if legacyInputRootSetting == "" {
		return errors.New(
			"input_assets.legacy_storage_root_setting 不能为空",
		)
	}
	if _, exists := schema.Configuration.Settings[legacyInputRootSetting]; !exists {
		return fmt.Errorf(
			"input_assets.legacy_storage_root_setting 引用了未知运行设置 %q",
			legacyInputRootSetting,
		)
	}
	if err := validateTaskDeletionRuntime(schema.TaskDeletion); err != nil {
		return fmt.Errorf("task_deletion: %w", err)
	}
	if err := validateTaskAuditRuntime(schema.TaskAudit); err != nil {
		return fmt.Errorf("task_audit: %w", err)
	}
	if err := validateCacheRuntime(schema.Cache); err != nil {
		return fmt.Errorf("cache: %w", err)
	}
	if err := validateAssetDeliveryRuntime(schema.AssetDelivery); err != nil {
		return fmt.Errorf("asset_delivery: %w", err)
	}
	assetProxyRootSetting := strings.TrimSpace(
		schema.AssetDelivery.Proxy.Cache.RootSetting,
	)
	if _, exists := schema.Configuration.
		Settings[assetProxyRootSetting]; !exists {
		return fmt.Errorf(
			"asset_delivery.proxy.cache.root_setting 引用了未声明设置 %q",
			assetProxyRootSetting,
		)
	}
	assetProxyThumbnailRootSetting := strings.TrimSpace(
		schema.AssetDelivery.Proxy.Thumbnail.RootSetting,
	)
	if _, exists := schema.Configuration.
		Settings[assetProxyThumbnailRootSetting]; !exists {
		return fmt.Errorf(
			"asset_delivery.proxy.thumbnail.root_setting 引用了未声明设置 %q",
			assetProxyThumbnailRootSetting,
		)
	}
	frontend := schema.Frontend
	if err := validateImageUpload(frontend.ImageUpload); err != nil {
		return fmt.Errorf("frontend.image_upload: %w", err)
	}
	if frontend.ProviderMetadataCacheTTLMilliseconds <= 0 ||
		frontend.TaskHistoryRefreshMilliseconds <= 0 ||
		frontend.TaskHistoryDedupingMilliseconds <= 0 ||
		frontend.BalanceRefreshMilliseconds <= 0 ||
		frontend.CacheStatusRefreshMilliseconds <= 0 ||
		frontend.DefaultPromptMaxLength <= 0 ||
		frontend.DefaultReferenceImageMin <= 0 ||
		frontend.DefaultReferenceImageMax < frontend.DefaultReferenceImageMin ||
		frontend.ProductVersionRefreshMilliseconds <= 0 ||
		frontend.TaskSubmissionCooldownSeconds <= 0 ||
		frontend.TaskQueueMaximum <= 0 ||
		frontend.BatchDownloadIntervalMilliseconds <= 0 ||
		frontend.TaskIdentifierPreviewCharacters <= 0 ||
		frontend.CountdownTickMilliseconds <= 0 ||
		frontend.TaskPollingIntervalMilliseconds <= 0 ||
		frontend.SessionMenuPageSize <= 0 ||
		frontend.SessionMenuSearchDebounceMilliseconds <= 0 ||
		frontend.SessionTitleRefreshAttempts <= 0 ||
		frontend.SessionTitleRefreshIntervalMilliseconds <= 0 ||
		frontend.ChatMessagePageSize <= 0 ||
		frontend.SpinnerFrameMilliseconds <= 0 ||
		frontend.PercentageMultiplier <= 0 ||
		frontend.TaskProgress.SecondsPerMinute <= 0 ||
		frontend.TaskProgress.MillisecondsPerSecond <= 0 ||
		frontend.TaskProgress.MinutesPerHour <= 0 ||
		frontend.TaskProgress.HourDecimalPlaces < 0 ||
		strings.TrimSpace(frontend.TaskProgress.FallbackState) == "" ||
		strings.TrimSpace(frontend.TaskProgress.InitialState) == "" ||
		strings.TrimSpace(frontend.TaskProgress.NotFoundState) == "" ||
		strings.TrimSpace(frontend.DisplayLocale) == "" ||
		!strings.HasPrefix(frontend.WebAssetPrefix, "/") ||
		!strings.HasSuffix(frontend.WebAssetPrefix, "/") ||
		!strings.HasPrefix(frontend.FaviconPath, "/") ||
		!strings.HasPrefix(frontend.ClientRoutes.Home, "/") ||
		!strings.HasPrefix(frontend.ClientRoutes.Login, "/") ||
		frontend.ClientRoutes.Home == frontend.ClientRoutes.Login ||
		strings.TrimSpace(frontend.PercentageSuffix) == "" ||
		strings.TrimSpace(frontend.AdvancedModelFormFeature) == "" {
		return errors.New("frontend 的缓存与刷新间隔必须大于 0")
	}
	switch frontend.AdvancedModelDefaultMode {
	case "regular", "parts":
	default:
		return fmt.Errorf(
			"frontend.advanced_model_default_mode 非法: %q",
			frontend.AdvancedModelDefaultMode,
		)
	}
	switch frontend.Toaster.Position {
	case "top-left", "top-center", "top-right",
		"bottom-left", "bottom-center", "bottom-right":
	default:
		return fmt.Errorf(
			"frontend.toaster.position 无效: %q",
			frontend.Toaster.Position,
		)
	}
	if frontend.TaskAPIPrefix != "" &&
		!strings.HasPrefix(frontend.TaskAPIPrefix, "/") {
		return fmt.Errorf(
			"frontend.task_api_prefix 无效: %q",
			frontend.TaskAPIPrefix,
		)
	}
	return nil
}

func validateTaskDeletionRuntime(schema TaskDeletionRuntimeSchema) error {
	if schema.LocalRecordPolicy != "tombstone" {
		return fmt.Errorf(
			"local_record_policy 不受支持: %q",
			schema.LocalRecordPolicy,
		)
	}
	if schema.UpstreamPolicy != "when-supported" {
		return fmt.Errorf("upstream_policy 不受支持: %q", schema.UpstreamPolicy)
	}
	if schema.UpstreamFailurePolicy != "abort" {
		return fmt.Errorf(
			"upstream_failure_policy 不受支持: %q",
			schema.UpstreamFailurePolicy,
		)
	}
	if schema.GeneratedAssetPolicy != "retain" {
		return fmt.Errorf(
			"generated_asset_policy 不受支持: %q",
			schema.GeneratedAssetPolicy,
		)
	}
	if strings.TrimSpace(schema.AuditEventType) == "" ||
		strings.ContainsAny(schema.AuditEventType, " \t\r\n") {
		return fmt.Errorf("audit_event_type 无效: %q", schema.AuditEventType)
	}
	return nil
}

func validateTaskAuditRuntime(schema TaskAuditRuntimeSchema) error {
	for name, value := range map[string]string{
		"create_action":            schema.CreateAction,
		"delete_action":            schema.DeleteAction,
		"resource_type":            schema.ResourceType,
		"detail_fields.capability": schema.DetailFields.Capability,
		"detail_fields.offering_id": schema.DetailFields.
			OfferingID,
		"detail_fields.replayed": schema.DetailFields.Replayed,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s 不能为空", name)
		}
	}
	return nil
}

func validateCacheRuntime(schema CacheRuntimeSchema) error {
	if schema.MillisecondsPerSecond <= 0 || schema.BytesPerMegabyte <= 0 ||
		schema.SizeDecimalPlaces < 0 || schema.SizeDecimalPlaces > 6 {
		return errors.New("缓存时间、容量单位和小数位配置无效")
	}
	fields := map[string]string{
		"active_downloads":    schema.Fields.ActiveDownloads,
		"active_items":        schema.Fields.ActiveItems,
		"filename":            schema.Fields.Filename,
		"elapsed":             schema.Fields.Elapsed,
		"task_id":             schema.Fields.TaskID,
		"task_type":           schema.Fields.TaskType,
		"provider_id":         schema.Fields.ProviderID,
		"asset_kind":          schema.Fields.AssetKind,
		"total_cached_files":  schema.Fields.TotalCachedFiles,
		"total_cache_size_mb": schema.Fields.TotalCacheSizeMB,
	}
	seen := map[string]string{}
	for name, field := range fields {
		if strings.TrimSpace(field) == "" {
			return fmt.Errorf("cache.fields.%s 不能为空", name)
		}
		if previous, exists := seen[field]; exists {
			return fmt.Errorf(
				"cache.fields.%s 与 %s 重复使用 %q",
				name,
				previous,
				field,
			)
		}
		seen[field] = name
	}
	return nil
}

func validateRuntimeConfiguration(schema RuntimeConfigurationSchema) error {
	// required 是 Go 侧按名字直接消费的核心设置：app/config.go 的运行
	// 接线名单（经 runtimeWiringSettingNames）+ 环境判定与 ETL 消费的
	// application_environment / legacy_backend_startup_enabled，必须在
	// 任意 Product Schema 中声明；Provider 连接与 Chat Agent 的
	// base_url/api_key/model 设置由各自的 Schema 引用校验
	//（validateProviderConnections / validateChat）保证完整性，不进此
	// 清单，使 Provider 与 LLM 家族的新增、退役、更名保持纯 Schema 变更。
	required := []string{
		"application_environment",
		"address",
		"database_path",
		"storage_root",
		"web_assets_root",
		"legacy_generated_images_root",
		"catalog_file",
		"skill_catalog_file",
		"legacy_backend_startup_enabled",
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
	}
	for _, name := range required {
		if _, exists := schema.Settings[name]; !exists {
			return fmt.Errorf("缺少必需运行设置 %q", name)
		}
	}
	if err := validateRetiredEnvironment(schema); err != nil {
		return err
	}
	for name, setting := range schema.Settings {
		if strings.TrimSpace(name) == "" {
			return errors.New("运行设置名称不能为空")
		}
		switch setting.Type {
		case "string", "string-list", "json":
		default:
			return fmt.Errorf("运行设置 %q 的 type 无效: %q", name, setting.Type)
		}
		if len(setting.Environment) == 0 {
			return fmt.Errorf("运行设置 %q 必须声明 environment 来源", name)
		}
		sourceNames := map[string]struct{}{}
		for index, source := range setting.Environment {
			if strings.TrimSpace(source.Name) == "" {
				return fmt.Errorf("运行设置 %q environment[%d].name 不能为空", name, index)
			}
			if _, exists := sourceNames[source.Name]; exists {
				return fmt.Errorf("运行设置 %q environment 来源重复: %q", name, source.Name)
			}
			sourceNames[source.Name] = struct{}{}
		}
		if setting.Type == "string-list" && setting.Separator == "" {
			return fmt.Errorf("运行设置 %q 的 string-list 必须声明 separator", name)
		}
		if setting.Type != "string-list" && setting.Separator != "" {
			return fmt.Errorf("运行设置 %q 只有 string-list 可以声明 separator", name)
		}
		if setting.Secret &&
			(strings.TrimSpace(setting.Default) != "" ||
				hasNonemptyRuntimeDefault(setting.DefaultByEnvironment)) {
			return fmt.Errorf("敏感运行设置 %q 禁止在 Product Schema 中提供默认值", name)
		}
		allowed := map[string]struct{}{}
		for _, value := range setting.AllowedValues {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("运行设置 %q 的 allowed_values 不能为空", name)
			}
			if _, exists := allowed[value]; exists {
				return fmt.Errorf("运行设置 %q 的 allowed_values 重复: %q", name, value)
			}
			allowed[value] = struct{}{}
		}
		if err := validateRuntimeDefaultAllowed(name, "default", setting.Default, allowed); err != nil {
			return err
		}
		for environment, value := range setting.DefaultByEnvironment {
			if strings.TrimSpace(environment) == "" {
				return fmt.Errorf("运行设置 %q 的 default_by_environment key 不能为空", name)
			}
			if err := validateRuntimeDefaultAllowed(
				name,
				"default_by_environment."+environment,
				value,
				allowed,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateRetiredEnvironment keeps the retired list unambiguous: a name is
// either read by a setting or retired, never both (#249).
func validateRetiredEnvironment(schema RuntimeConfigurationSchema) error {
	active := map[string]string{}
	for name, setting := range schema.Settings {
		for _, source := range setting.Environment {
			active[source.Name] = name
		}
	}
	seen := map[string]struct{}{}
	for index, name := range schema.RetiredEnvironment {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name {
			return fmt.Errorf("runtime.configuration.retired_environment[%d] 无效", index)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("runtime.configuration.retired_environment 重复: %q", name)
		}
		seen[name] = struct{}{}
		if setting, exists := active[name]; exists {
			return fmt.Errorf(
				"runtime.configuration.retired_environment 的 %q 仍被运行设置 %q 读取",
				name,
				setting,
			)
		}
	}
	return nil
}

func hasNonemptyRuntimeDefault(values map[string]string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func validateRuntimeDefaultAllowed(
	name string,
	field string,
	value string,
	allowed map[string]struct{},
) error {
	if len(allowed) == 0 || strings.TrimSpace(value) == "" {
		return nil
	}
	if _, exists := allowed[value]; !exists {
		return fmt.Errorf(
			"运行设置 %q 的 %s 值 %q 不在 allowed_values 中",
			name,
			field,
			value,
		)
	}
	return nil
}

func cacheDirectives(value string) map[string]string {
	directives := map[string]string{}
	for _, part := range strings.Split(value, ",") {
		name, argument, _ := strings.Cut(strings.TrimSpace(part), "=")
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			directives[name] = strings.TrimSpace(argument)
		}
	}
	return directives
}

// validateBuildAssetCacheControl accepts no-store or a private immutable
// policy for content-hashed build files, which carry no user data (#225).
func validateBuildAssetCacheControl(value string) error {
	directives := cacheDirectives(value)
	_, private := directives["private"]
	_, public := directives["public"]
	_, shared := directives["s-maxage"]
	_, noStore := directives["no-store"]
	_, immutable := directives["immutable"]
	if !private || public || shared || (!noStore && !immutable) {
		return errors.New("构建产物缓存策略必须是 private 的 no-store 或 immutable")
	}
	return nil
}

// validateThumbnailCache keeps thumbnail reuse private, bounded and limited to
// the declared roles (#225).
func validateThumbnailCache(httpRuntime HTTPRuntimeSchema) error {
	if httpRuntime.SessionChangeClearSiteData != "" && !strings.Contains(httpRuntime.SessionChangeClearSiteData, `"cache"`) {
		return errors.New(`http.session_change_clear_site_data 必须包含 "cache"`)
	}
	// Additive declaration: a Catalog without it keeps every asset no-store.
	if httpRuntime.ThumbnailCacheControl == "" && len(httpRuntime.ThumbnailAssetRoles) == 0 &&
		httpRuntime.ThumbnailCacheMaxAgeSeconds == 0 {
		return nil
	}
	if httpRuntime.ThumbnailCacheMaxAgeSeconds <= 0 || httpRuntime.ThumbnailCacheMaxAgeSeconds > 86400 {
		return errors.New("http.thumbnail_cache_max_age_seconds 必须在 1–86400 之间")
	}
	if len(httpRuntime.ThumbnailAssetRoles) == 0 {
		return errors.New("http.thumbnail_asset_roles 不能为空")
	}
	for _, role := range httpRuntime.ThumbnailAssetRoles {
		if strings.TrimSpace(role) == "" {
			return errors.New("http.thumbnail_asset_roles 含空角色")
		}
	}
	directives := cacheDirectives(httpRuntime.ThumbnailCacheControl)
	_, private := directives["private"]
	_, public := directives["public"]
	_, shared := directives["s-maxage"]
	maxAge, hasMaxAge := directives["max-age"]
	seconds, err := strconv.Atoi(maxAge)
	if !private || public || shared || !hasMaxAge || err != nil || seconds <= 0 ||
		seconds > httpRuntime.ThumbnailCacheMaxAgeSeconds {
		return fmt.Errorf("http.thumbnail_cache_control 必须是 private 且 max-age 不超过 %d 秒", httpRuntime.ThumbnailCacheMaxAgeSeconds)
	}
	return nil
}
