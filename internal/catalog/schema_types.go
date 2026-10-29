package catalog

import (
	"encoding/json"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// Schema types define the catalog document, application configuration and public DTOs.
// Loading, domain validation, public projection and cloning live in separate catalog files.

type Catalog struct {
	application ApplicationSchema
	byID        generationMap
}

type generationMap map[schemacontract.OfferingID]schemacontract.Offering

type document struct {
	Version     int                `json:"version"`
	Application *ApplicationSchema `json:"application"`
	Offerings   []rawOffering      `json:"offerings"`
}

// ApplicationSchema 是运行策略、权限和展示文本的统一配置真源。
// 它与 Offering 放在同一份严格校验的 Product Schema Catalog 中，
// 以便运行时、HTTP 投影和前端展示消费同一版本的配置。
type ApplicationSchema struct {
	AgentConfiguration AgentConfigurationSchema `json:"agent_configuration"`
	// LLMSettings is the administrator's LLM settings page and the
	// environment contract of chat model connections (#322). Optional so
	// older external Catalog documents stay valid.
	LLMSettings *LLMSettingsSchema `json:"llm_settings,omitempty"`
	Revision    string             `json:"revision"`
	// Terminology declares the user-facing words for one concept and the
	// aliases Catalog loading rejects. Server-side only: the browser consumes
	// the copy itself, not the glossary.
	Terminology       *TerminologySchema       `json:"terminology,omitempty"`
	Runtime           RuntimeSchema            `json:"runtime"`
	Content           ContentSchema            `json:"content"`
	Chat              ChatSchema               `json:"chat"`
	Canvas            CanvasSchema             `json:"canvas"`
	ProviderWebhooks  []ProviderWebhookSchema  `json:"provider_webhooks"`
	ProviderDiscovery ProviderDiscoverySchema  `json:"provider_discovery"`
	TaskHistory       TaskHistorySchema        `json:"task_history"`
	TaskStream        TaskStreamSchema         `json:"task_stream"`
	Administration    AdministrationSchema     `json:"administration"`
	Capabilities      []CapabilityPresentation `json:"capabilities"`
	TaskStates        []TaskStatePresentation  `json:"task_states"`
	AuditActions      []ValueLabel             `json:"audit_actions"`
	Authentication    AuthenticationSchema     `json:"authentication"`
	// ModelDelivery is server-side only: it is not part of the public
	// projection (the workspace consumes the coverage API instead).
	ModelDelivery ModelDeliverySchema `json:"model_delivery"`
	// Localization declares the UI locales and every non-default locale's
	// copy. Omitted, the application has one locale: runtime.frontend.display_locale.
	Localization *LocalizationSchema `json:"localization,omitempty"`
	// ProviderDeprecations lists, per Provider, the models, wire fields and
	// values its current documentation has deprecated or retired (#329).
	// Catalog loading rejects an Offering that would send one. Server-side
	// only: not part of the public projection.
	ProviderDeprecations []ProviderDeprecationSchema `json:"provider_deprecations,omitempty"`
}

// ProviderDeprecationSchema is one Provider's deprecation baseline, checked
// against its official documentation on VerifiedAt (#329).
type ProviderDeprecationSchema struct {
	ProviderID string   `json:"provider_id"`
	VerifiedAt string   `json:"verified_at"`
	Sources    []string `json:"sources"`
	// ModelFields are the wire fields that carry the upstream model, such as
	// Meshy's ai_model.
	ModelFields []string `json:"model_fields"`
	// HistoryOfferings are disabled Offerings kept only so historical tasks
	// and legacy mappings keep their identity. They are exempt from the
	// checks below and must stay disabled.
	HistoryOfferings []schemacontract.OfferingID `json:"history_offerings,omitempty"`
	Models           []DeprecatedWireValue       `json:"models,omitempty"`
	Parameters       []DeprecatedWireField       `json:"parameters,omitempty"`
	Values           []DeprecatedWireFieldValue  `json:"values,omitempty"`
	// Exemptions keep one deprecated field on one Offering while its
	// replacement is not yet delivered. Each names the tracking Issue.
	Exemptions []DeprecationExemption `json:"exemptions,omitempty"`
}

// DeprecationExemption allows one deprecated field on one Offering.
type DeprecationExemption struct {
	OfferingID schemacontract.OfferingID `json:"offering_id"`
	Field      string                    `json:"field"`
	Issue      string                    `json:"issue"`
	Reason     string                    `json:"reason"`
}

// DeprecatedWireValue is a model value no Offering may send. Endpoints
// limits the entry to those provider_options.endpoint values; empty means
// every endpoint.
type DeprecatedWireValue struct {
	Value       string   `json:"value"`
	Endpoints   []string `json:"endpoints,omitempty"`
	Replacement string   `json:"replacement,omitempty"`
	Reason      string   `json:"reason"`
}

// DeprecatedWireField is a request field no Offering may send.
type DeprecatedWireField struct {
	Name        string   `json:"name"`
	Endpoints   []string `json:"endpoints,omitempty"`
	Replacement string   `json:"replacement,omitempty"`
	Reason      string   `json:"reason"`
}

// DeprecatedWireFieldValue is one value of a request field no Offering may
// send.
type DeprecatedWireFieldValue struct {
	Field       string   `json:"field"`
	Value       string   `json:"value"`
	Endpoints   []string `json:"endpoints,omitempty"`
	Replacement string   `json:"replacement,omitempty"`
	Reason      string   `json:"reason"`
}

// ProviderWebhookSchema 拥有 Provider 回调入口的路径、认证、payload 定位和响应线协议。
type ProviderWebhookSchema struct {
	ID                 string                   `json:"id"`
	ProviderID         string                   `json:"provider_id"`
	Route              string                   `json:"route"`
	PathParameter      string                   `json:"path_parameter"`
	PathSecretSetting  string                   `json:"path_secret_setting"`
	KeySetting         string                   `json:"key_setting"`
	KeyHeaders         []string                 `json:"key_headers"`
	PayloadJobIDFields []string                 `json:"payload_job_id_fields"`
	EventType          string                   `json:"event_type"`
	API                ProviderWebhookAPISchema `json:"api"`
}

type ProviderWebhookAPISchema struct {
	TimestampLayout string                     `json:"timestamp_layout"`
	Fields          ProviderWebhookAPIFields   `json:"fields"`
	Values          ProviderWebhookAPIValues   `json:"values"`
	Statuses        ProviderWebhookAPIStatuses `json:"statuses"`
}

type ProviderWebhookAPIFields struct {
	Error     string `json:"error"`
	Received  string `json:"received"`
	Timestamp string `json:"timestamp"`
	Status    string `json:"status"`
	Service   string `json:"service"`
}

type ProviderWebhookAPIValues struct {
	NotFound     string `json:"not_found"`
	Unauthorized string `json:"unauthorized"`
	BadRequest   string `json:"bad_request"`
	Unavailable  string `json:"unavailable"`
	OK           string `json:"ok"`
	Service      string `json:"service"`
}

type ProviderWebhookAPIStatuses struct {
	OK           int `json:"ok"`
	BadRequest   int `json:"bad_request"`
	Unauthorized int `json:"unauthorized"`
	NotFound     int `json:"not_found"`
	Unavailable  int `json:"unavailable"`
}

// ProviderDiscoverySchema 拥有 Provider/Offering 浏览器发现接口的公开线协议。
type ProviderDiscoverySchema struct {
	API                          ProviderDiscoveryAPISchema     `json:"api"`
	LegacyModels                 []ProviderDiscoveryLegacyModel `json:"legacy_models"`
	LegacyConstraintOptionLabels map[string]map[string]string   `json:"legacy_constraint_option_labels"`
}

type ProviderDiscoveryLegacyModel struct {
	ModelID               string                                                                    `json:"model_id"`
	NativePricing         bool                                                                      `json:"native_pricing,omitempty"`
	ProviderKey           string                                                                    `json:"provider_key"`
	Name                  string                                                                    `json:"name,omitempty"`
	Recommended           *bool                                                                     `json:"recommended,omitempty"`
	ParameterProjections  map[schemacontract.Capability]ProviderDiscoveryLegacyParameterProjection  `json:"parameter_projections,omitempty"`
	ConstraintProjections map[schemacontract.Capability]ProviderDiscoveryLegacyConstraintProjection `json:"constraint_projections,omitempty"`
}

type ProviderDiscoveryLegacyParameterProjection struct {
	Order  []string                                               `json:"order,omitempty"`
	Omit   []string                                               `json:"omit,omitempty"`
	Fields map[string]ProviderDiscoveryLegacyParameterFieldSchema `json:"fields,omitempty"`
}

type ProviderDiscoveryLegacyParameterFieldSchema struct {
	Omit        []string        `json:"omit,omitempty"`
	Required    *bool           `json:"required,omitempty"`
	Description *string         `json:"description,omitempty"`
	Default     json.RawMessage `json:"default,omitempty"`
}

type ProviderDiscoveryLegacyConstraintProjection struct {
	Omit      []string                                        `json:"omit,omitempty"`
	Overrides ProviderDiscoveryLegacyConstraintOverrideSchema `json:"overrides,omitempty"`
}

type ProviderDiscoveryLegacyConstraintOverrideSchema struct {
	PromptMaxLength         *int     `json:"prompt_max_length,omitempty"`
	NegativePromptMaxLength *int     `json:"negative_prompt_max_length,omitempty"`
	ReferenceImageMin       *int     `json:"reference_image_min,omitempty"`
	ReferenceImageMax       *int     `json:"reference_image_max,omitempty"`
	ReferenceImageMaxSize   *int64   `json:"reference_image_max_size,omitempty"`
	PolycountMin            *float64 `json:"polycount_min,omitempty"`
	PolycountMax            *float64 `json:"polycount_max,omitempty"`
}

type ProviderDiscoveryAPISchema struct {
	Query             ProviderDiscoveryQuerySchema            `json:"query"`
	ProjectionValues  ProviderDiscoveryProjectionValueSchema  `json:"projection_values"`
	EnvelopeFields    ProviderDiscoveryEnvelopeFieldSchema    `json:"envelope_fields"`
	ProviderFields    ProviderDiscoveryProviderFieldSchema    `json:"provider_fields"`
	ParameterFields   ProviderDiscoveryParameterFieldSchema   `json:"parameter_fields"`
	OptionFields      ProviderDiscoveryOptionFieldSchema      `json:"option_fields"`
	ConditionFields   ProviderDiscoveryConditionFieldSchema   `json:"condition_fields"`
	NoticeFields      ProviderDiscoveryNoticeFieldSchema      `json:"notice_fields"`
	ForcedValueFields ProviderDiscoveryForcedValueFieldSchema `json:"forced_value_fields"`
	InputFields       ProviderDiscoveryInputFieldSchema       `json:"input_fields"`
	ConstraintFields  ProviderDiscoveryConstraintFieldSchema  `json:"constraint_fields"`
}

type ProviderDiscoveryQuerySchema struct {
	Capability string `json:"capability"`
	Domain     string `json:"domain"`
	Projection string `json:"projection"`
}

type ProviderDiscoveryProjectionValueSchema struct {
	Extended string `json:"extended"`
}

type ProviderDiscoveryEnvelopeFieldSchema struct {
	Providers    string `json:"providers"`
	DefaultModel string `json:"default_model"`
}

type ProviderDiscoveryProviderFieldSchema struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Capabilities     string `json:"capabilities"`
	Domain           string `json:"domain"`
	Features         string `json:"features"`
	ParameterSchemas string `json:"parameter_schemas"`
	InputSchemas     string `json:"input_schemas"`
	Constraints      string `json:"constraints"`
	Recommended      string `json:"recommended"`
	EstimatedCredits string `json:"estimated_credits"`
	NativeQuotes     string `json:"native_quotes,omitempty"`
	ProviderKey      string `json:"provider_key"`
	// EstimatedExtraCredits 是安全派生的额外积分估算线名（extended
	// 投影专用，§8.2）：只输出数字，不输出 cost 原始声明。
	EstimatedExtraCredits string `json:"estimated_extra_credits,omitempty"`
}

type ProviderDiscoveryParameterFieldSchema struct {
	AdjustmentPolicy string `json:"adjustment_policy,omitempty"`
	AdjustmentReason string `json:"adjustment_reason,omitempty"`
	ReadOnly         string `json:"readonly,omitempty"`
	Validation       string `json:"validation,omitempty"`
	Name             string `json:"name"`
	Label            string `json:"label"`
	Type             string `json:"type"`
	Options          string `json:"options"`
	Default          string `json:"default"`
	Required         string `json:"required"`
	Minimum          string `json:"minimum"`
	Maximum          string `json:"maximum"`
	Step             string `json:"step"`
	Description      string `json:"description"`
	DisabledWhen     string `json:"disabled_when"`
	VisibleWhen      string `json:"visible_when"`
	ForcedWhen       string `json:"forced_when"`
	Notices          string `json:"notices"`
	// CostExtra 是安全派生的参数级"参与额外计费"标记线名（extended
	// 投影专用）：布尔值，由 cost.extra_when 派生，不泄漏条件本身。
	CostExtra string `json:"cost_extra,omitempty"`
	// Semantic 是参数语义线名（extended 投影专用）：前端按语义而非
	// 参数名判定能力（B7），缺省不输出。
	Semantic string `json:"semantic,omitempty"`
}

type ProviderDiscoveryOptionFieldSchema struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Group string `json:"group"`
}

// ProviderDiscoveryForcedValueFieldSchema 声明 forced_when 条目对象的线名称。
type ProviderDiscoveryForcedValueFieldSchema struct {
	When  string `json:"when"`
	Value string `json:"value"`
}

type ProviderDiscoveryConditionFieldSchema struct {
	Parameter string `json:"parameter"`
	Operator  string `json:"operator"`
	Value     string `json:"value"`
}

type ProviderDiscoveryNoticeFieldSchema struct {
	When       string `json:"when"`
	MessageKey string `json:"message_key"`
	Tone       string `json:"tone"`
}

type ProviderDiscoveryInputFieldSchema struct {
	SameSession       string `json:"same_session,omitempty"`
	TaskSourceBinding string `json:"task_source_binding,omitempty"`
	ImageInput        string `json:"image_input,omitempty"`
	UploadedModel     string `json:"uploaded_model,omitempty"`
	TaskSourceKind    string `json:"task_source_kind,omitempty"`
	TaskOfferings     string `json:"task_offerings,omitempty"`
	TaskCapabilities  string `json:"task_capabilities,omitempty"`
	SameProvider      string `json:"same_provider,omitempty"`
	Name              string `json:"name"`
	Label             string `json:"label"`
	Type              string `json:"type"`
	MediaTypes        string `json:"media_types"`
	Extensions        string `json:"extensions"`
	MaxBytes          string `json:"max_bytes"`
	MinItems          string `json:"min_items"`
	MaxItems          string `json:"max_items"`
}

type ProviderDiscoveryConstraintFieldSchema struct {
	PromptMaxLength         string `json:"prompt_max_length"`
	NegativePromptMaxLength string `json:"negative_prompt_max_length"`
	ReferenceImageMin       string `json:"reference_image_min"`
	ReferenceImageMax       string `json:"reference_image_max"`
	ReferenceImageFormats   string `json:"reference_image_formats"`
	ReferenceImageMaxSize   string `json:"reference_image_max_size"`
	PolycountMin            string `json:"polycount_min"`
	PolycountMax            string `json:"polycount_max"`
	OutputSizes             string `json:"output_sizes"`
}

// TaskHistorySchema 拥有任务历史兼容接口的查询、响应字段和控制值。
// Transport 与浏览器只实现分页和投影算法，不拥有公开线协议名称。
type TaskHistorySchema struct {
	API           TaskHistoryAPISchema     `json:"api"`
	Assets        TaskHistoryAssetSchema   `json:"assets"`
	Values        TaskHistoryValueSchema   `json:"values"`
	Labels        TaskHistoryLabelSchema   `json:"labels"`
	HiddenFields  map[string][]string      `json:"hidden_fields"`
	AssetRecovery *TaskAssetRecoverySchema `json:"asset_recovery,omitempty"`
}

// TaskAssetRecoverySchema is optional so historical Product Schema stays readable.
// All recovery UI copy is public Schema; it does not grant recovery eligibility.
type TaskAssetRecoverySchema struct {
	Action             string `json:"action"`
	EligibilityLoading string `json:"eligibility_loading"`
	EligibilityFailed  string `json:"eligibility_failed"`
	Preparing          string `json:"preparing"`
	PrepareFailed      string `json:"prepare_failed"`
	AwaitingApproval   string `json:"awaiting_approval"`
	Approve            string `json:"approve"`
	Deny               string `json:"deny"`
	Submitting         string `json:"submitting"`
	SubmitFailed       string `json:"submit_failed"`
	Submitted          string `json:"submitted"`
	OpenRecoveryTask   string `json:"open_recovery_task"`
	OpenSourceTask     string `json:"open_source_task"`
	SourceLabel        string `json:"source_label"`
	Description        string `json:"description"`
}

// TaskHistoryLabelSchema 声明任务详情展示层的公开标签：
// models 是仅用于展示的历史模型名（不激活任何 Offering），
// parameters 按 capability 作用域声明任务参数字段的展示标签。
type TaskHistoryLabelSchema struct {
	Models     map[string]string            `json:"models"`
	Parameters map[string]map[string]string `json:"parameters"`
}

type TaskHistoryAssetSchema struct {
	PreviewRoles   []string                         `json:"preview_roles"`
	Attachments    TaskAttachmentPresentationSchema `json:"attachments"`
	PrintReport    PrintReportPresentationSchema    `json:"print_report"`
	RigCheckReport RigCheckReportPresentationSchema `json:"rig_check_report"`
}

type TaskAttachmentPresentationSchema struct {
	Title        string            `json:"title"`
	RoleLabels   map[string]string `json:"role_labels"`
	FallbackRole string            `json:"fallback_role"`
	Format       string            `json:"format"`
	MediaType    string            `json:"media_type"`
	Size         string            `json:"size"`
	UnknownSize  string            `json:"unknown_size"`
	Download     string            `json:"download"`
	NoPreview    string            `json:"no_preview"`
}

// PrintReportPresentationSchema describes the public normalized report protocol
// and its presentation. Provider wire fields never enter the browser.
type PrintReportPresentationSchema struct {
	MediaType           string            `json:"media_type"`
	Format              string            `json:"format"`
	MaxBytes            int64             `json:"max_bytes"`
	Title               string            `json:"title"`
	Loading             string            `json:"loading"`
	LoadFailed          string            `json:"load_failed"`
	Retry               string            `json:"retry"`
	Unsupported         string            `json:"unsupported"`
	DiagnosticNotice    string            `json:"diagnostic_notice"`
	UnknownValue        string            `json:"unknown_value"`
	NotReported         string            `json:"not_reported"`
	KindLabels          map[string]string `json:"kind_labels"`
	StatusLabels        map[string]string `json:"status_labels"`
	DetailsStatusLabels map[string]string `json:"details_status_labels"`
	FieldLabels         map[string]string `json:"field_labels"`
	ValueLabels         map[string]string `json:"value_labels"`
}

// RigCheckReportPresentationSchema describes only the normalized compatibility report.
// It neither declares a successful rig nor authorizes a follow-up operation.
type RigCheckReportPresentationSchema struct {
	MediaType     string            `json:"media_type"`
	Format        string            `json:"format"`
	MaxBytes      int64             `json:"max_bytes"`
	Title         string            `json:"title"`
	Loading       string            `json:"loading"`
	LoadFailed    string            `json:"load_failed"`
	Retry         string            `json:"retry"`
	Unsupported   string            `json:"unsupported"`
	Limitation    string            `json:"limitation"`
	FieldLabels   map[string]string `json:"field_labels"`
	BooleanLabels map[string]string `json:"boolean_labels"`
	SpeciesLabels map[string]string `json:"species_labels"`
}

type TaskHistoryAPISchema struct {
	Query  TaskHistoryQuerySchema `json:"query"`
	Fields TaskHistoryFieldSchema `json:"fields"`
}

type TaskHistoryQuerySchema struct {
	Page         string `json:"page"`
	PageSize     string `json:"page_size"`
	Recent       string `json:"recent"`
	Type         string `json:"type"`
	Search       string `json:"search"`
	Status       string `json:"status"`
	HideFailures string `json:"hide_failures"`
}

type TaskHistoryFieldSchema struct {
	Data         string `json:"data"`
	Total        string `json:"total"`
	Page         string `json:"page"`
	PageSize     string `json:"page_size"`
	ID           string `json:"id"`
	TaskType     string `json:"task_type"`
	Status       string `json:"status"`
	Progress     string `json:"progress"`
	ThumbnailURL string `json:"thumbnail_url"`
	CreatedAt    string `json:"created_at"`
	Prompt       string `json:"prompt"`
}

type TaskHistoryValueSchema struct {
	AllCapability string `json:"all_capability"`
	Enabled       string `json:"enabled"`
}

type TaskStreamSchema struct {
	API          TaskStreamAPISchema          `json:"api"`
	Aliases      []TaskStreamCapabilityAlias  `json:"aliases"`
	Events       TaskStreamEventSchema        `json:"events"`
	Timing       TaskStreamTimingSchema       `json:"timing"`
	Presentation TaskStreamPresentationSchema `json:"presentation"`
}

type TaskStreamAPISchema struct {
	Fields         TaskStreamFieldSchema         `json:"fields"`
	PathParameters TaskStreamPathParameterSchema `json:"path_parameters"`
	Query          TaskStreamQuerySchema         `json:"query"`
}

// TaskStreamQuerySchema 声明 SSE 查询参数名（W6 §6.2 触点 6）：
// completion 参数是声明式 opt-in——值 "post_success" 时终态任务在
// resources_pending 期间续流，资源落地或截止后断流；缺省（不携带或
// 值不匹配）保持旧契约（终态立即断流）。字段名归 Schema，opt-in 值
// "post_success" 是协议常量。
type TaskStreamQuerySchema struct {
	Completion string `json:"completion"`
}

type TaskStreamFieldSchema struct {
	TaskID     string `json:"task_id"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	Progress   string `json:"progress"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
	TaskError  string `json:"task_error"`
	Message    string `json:"message"`
	StatusCode string `json:"status_code"`
	Error      string `json:"error"`
}

type TaskStreamPathParameterSchema struct {
	Type   string `json:"type"`
	TaskID string `json:"task_id"`
}

type TaskStreamCapabilityAlias struct {
	ExternalType string                    `json:"external_type"`
	Capability   schemacontract.Capability `json:"capability"`
}

type TaskStreamEventSchema struct {
	Message string `json:"message"`
	Error   string `json:"error"`
}

type TaskStreamTimingSchema struct {
	PollIntervalMilliseconds int `json:"poll_interval_milliseconds"`
}

type TaskStreamPresentationSchema struct {
	UnsupportedType string `json:"unsupported_type"`
	StreamFailed    string `json:"stream_failed"`
	TaskNotFound    string `json:"task_not_found"`
}

// AdministrationSchema 拥有管理域的 HTTP 线协议、约束、审计语义与 UI 文本。
// 管理服务只负责业务不变量，不拥有公开字段名或可见文本。
type AdministrationSchema struct {
	Navigation     []AdministrationNavigationSchema   `json:"navigation"`
	Users          AdministrationUsersSchema          `json:"users"`
	AuditLogs      AdministrationAuditLogsSchema      `json:"audit_logs"`
	Generations    AdministrationGenerationsSchema    `json:"generations"`
	Usage          AdministrationUsageSchema          `json:"usage"`
	UserAssets     AdministrationUserAssetsSchema     `json:"user_assets"`
	BackfillStatus AdministrationBackfillStatusSchema `json:"backfill_status"`
}

type AdministrationNavigationSchema struct {
	Key   string `json:"key"`
	Href  string `json:"href"`
	Label string `json:"label"`
	Icon  string `json:"icon"`
	Match string `json:"match"`
}

type AdministrationBackfillStatusSchema struct {
	API               AdministrationBackfillStatusAPISchema        `json:"api"`
	Permission        string                                       `json:"permission"`
	Audit             AdministrationTriggerAuditSchema             `json:"audit"`
	Statuses          AdministrationBackfillStatusStateSchema      `json:"statuses"`
	SyncProviderIDs   []string                                     `json:"sync_provider_ids"`
	CacheResultFields AdministrationBackfillCacheResultFieldSchema `json:"cache_result_fields"`
	Messages          AdministrationBackfillStatusMessageSchema    `json:"messages"`
}

type AdministrationBackfillStatusAPISchema struct {
	Fields AdministrationBackfillStatusFieldSchema `json:"fields"`
}

type AdministrationBackfillStatusFieldSchema struct {
	Error      string `json:"error"`
	Message    string `json:"message"`
	Fixed      string `json:"fixed"`
	Total      string `json:"total"`
	SyncFixed  string `json:"sync_fixed"`
	AsyncFixed string `json:"async_fixed"`
	Unchanged  string `json:"unchanged"`
}

type AdministrationBackfillStatusStateSchema struct {
	Pending   string `json:"pending"`
	Succeeded string `json:"succeeded"`
}

type AdministrationBackfillCacheResultFieldSchema struct {
	ModelURLs    string `json:"model_urls"`
	ThumbnailURL string `json:"thumbnail_url"`
	ImageURLs    string `json:"image_urls"`
	TextureURLs  string `json:"texture_urls"`
	BaseColor    string `json:"base_color"`
	VideoURL     string `json:"video_url"`
}

type AdministrationBackfillStatusMessageSchema struct {
	NoPending     string `json:"no_pending"`
	Complete      string `json:"complete"`
	InternalError string `json:"internal_error"`
}

type AdministrationUsersSchema struct {
	API          AdministrationUsersAPISchema          `json:"api"`
	Permission   string                                `json:"permission"`
	Guards       AdministrationUsersGuardsSchema       `json:"guards"`
	Limits       AdministrationUsersLimitsSchema       `json:"limits"`
	Defaults     AdministrationUsersDefaultsSchema     `json:"defaults"`
	Audit        AdministrationUsersAuditSchema        `json:"audit"`
	Messages     AdministrationUsersMessageSchema      `json:"messages"`
	Presentation AdministrationUsersPresentationSchema `json:"presentation"`
}

// AdministrationUsersGuardsSchema 声明管理员数量的不变量。
// 没有它，一次误操作（降权或停用最后一个管理员）就会让系统永久失去管理入口，
// 只能改库恢复。
type AdministrationUsersGuardsSchema struct {
	MinimumActiveAdministrators int `json:"minimum_active_administrators"`
}

type AdministrationUsersAPISchema struct {
	Fields         AdministrationUserFieldSchema         `json:"fields"`
	PathParameters AdministrationUserPathParameterSchema `json:"path_parameters"`
	Identity       AdministrationUserIdentitySchema      `json:"identity"`
}

// AdministrationUserIdentitySchema 声明登录身份对管理写入的约束：
// 会话签发要求 email 非空（见 authentication.validUser），因此建号与改号
// 必须按同一契约校验，否则账号会被创建成无法登录的"死号"。
type AdministrationUserIdentitySchema struct {
	EmailRequired      bool `json:"email_required"`
	EmailMustContainAt bool `json:"email_must_contain_at"`
}

type AdministrationUserFieldSchema struct {
	Data      string `json:"data"`
	Success   string `json:"success"`
	Error     string `json:"error"`
	ID        string `json:"id"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Nickname  string `json:"nickname"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	IsAdmin   string `json:"is_admin"`
	IsActive  string `json:"is_active"`
	CreatedAt string `json:"created_at"`
	LastLogin string `json:"last_login"`
	OpenID    string `json:"open_id"`
}

type AdministrationUserPathParameterSchema struct {
	UserID string `json:"user_id"`
}

type AdministrationUsersLimitsSchema struct {
	UsernameMinimumCharacters int `json:"username_minimum_characters"`
	PasswordMinimumCharacters int `json:"password_minimum_characters"`
}

type AdministrationUsersDefaultsSchema struct {
	IDPrefix string `json:"id_prefix"`
	Active   bool   `json:"active"`
}

// AdministrationReadAuditSchema 声明管理读路径的审计动作。
// 管理读此前完全不写审计，"管理员看了什么"不可追溯；动作名由 Schema 提供，
// 代码只消费，不在 handler 里硬编码第二个真源。
type AdministrationReadAuditSchema struct {
	ReadAction   string `json:"read_action"`
	ResourceType string `json:"resource_type"`
}

// AdministrationTriggerAuditSchema 声明管理写/触发类动作（如补数回填）。
// 它不是读路径，语义上与读审计分开，避免把状态变更记成"读"。
type AdministrationTriggerAuditSchema struct {
	TriggerAction string `json:"trigger_action"`
	ResourceType  string `json:"resource_type"`
}

type AdministrationUsersAuditSchema struct {
	ReadAction       string                                    `json:"read_action"`
	CreateAction     string                                    `json:"create_action"`
	UpdateAction     string                                    `json:"update_action"`
	DeactivateAction string                                    `json:"deactivate_action"`
	DeleteAction     string                                    `json:"delete_action"`
	ResourceType     string                                    `json:"resource_type"`
	DetailFields     AdministrationUsersAuditDetailFieldSchema `json:"detail_fields"`
}

type AdministrationUsersAuditDetailFieldSchema struct {
	Username string `json:"username"`
	IsAdmin  string `json:"is_admin"`
	IsActive string `json:"is_active"`
}

type AdministrationUsersMessageSchema struct {
	UserNotFound      string `json:"user_not_found"`
	SelfDeactivate    string `json:"self_deactivate"`
	SelfDelete        string `json:"self_delete"`
	DuplicateUsername string `json:"duplicate_username"`
	InvalidUsername   string `json:"invalid_username"`
	InvalidPassword   string `json:"invalid_password"`
	InvalidEmail      string `json:"invalid_email"`
	LastAdministrator string `json:"last_administrator"`
	InvalidRequest    string `json:"invalid_request"`
	InternalError     string `json:"internal_error"`
}

type AdministrationUsersPresentationSchema struct {
	DateLocale       string            `json:"date_locale"`
	LoadingRows      int               `json:"loading_rows"`
	DateTimeOptions  map[string]string `json:"date_time_options"`
	EmptyPlaceholder string            `json:"empty_placeholder"`
	Text             map[string]string `json:"text"`
}

type AdministrationAuditLogsSchema struct {
	API            AdministrationAuditLogsAPISchema          `json:"api"`
	Permission     string                                    `json:"permission"`
	Audit          AdministrationReadAuditSchema             `json:"audit"`
	Defaults       AdministrationAuditLogsDefaultsSchema     `json:"defaults"`
	RequestHeaders AdministrationAuditRequestHeadersSchema   `json:"request_headers"`
	WritePolicy    AdministrationAuditWritePolicySchema      `json:"write_policy"`
	Limits         AdministrationAuditLogsLimitsSchema       `json:"limits"`
	Messages       AdministrationAuditLogsMessageSchema      `json:"messages"`
	Presentation   AdministrationAuditLogsPresentationSchema `json:"presentation"`
}

type AdministrationAuditLogsDefaultsSchema struct {
	IDPrefix string `json:"id_prefix"`
}

type AdministrationAuditRequestHeadersSchema struct {
	ForwardedFor string `json:"forwarded_for"`
	RealIP       string `json:"real_ip"`
	UserAgent    string `json:"user_agent"`
}

type AdministrationAuditWritePolicySchema struct {
	MaximumAttempts        int `json:"maximum_attempts"`
	RetryDelayMilliseconds int `json:"retry_delay_milliseconds"`
}

type AdministrationAuditLogsAPISchema struct {
	Fields AdministrationAuditLogFieldSchema      `json:"fields"`
	Query  AdministrationAuditLogQueryFieldSchema `json:"query"`
}

type AdministrationAuditLogFieldSchema struct {
	Data         string `json:"data"`
	Error        string `json:"error"`
	Total        string `json:"total"`
	Page         string `json:"page"`
	PageSize     string `json:"page_size"`
	ID           string `json:"id"`
	Action       string `json:"action"`
	UserID       string `json:"user_id"`
	UserName     string `json:"user_name"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Detail       string `json:"detail"`
	IPAddress    string `json:"ip_address"`
	UserAgent    string `json:"user_agent"`
	CreatedAt    string `json:"created_at"`
}

type AdministrationAuditLogQueryFieldSchema struct {
	Page     string `json:"page"`
	PageSize string `json:"page_size"`
	UserID   string `json:"user_id"`
	Action   string `json:"action"`
	From     string `json:"from"`
	To       string `json:"to"`
}

type AdministrationAuditLogsLimitsSchema struct {
	DefaultPage     int `json:"default_page"`
	DefaultPageSize int `json:"default_page_size"`
	MaximumPageSize int `json:"maximum_page_size"`
}

type AdministrationAuditLogsMessageSchema struct {
	InternalError string `json:"internal_error"`
}

type AdministrationAuditLogsPresentationSchema struct {
	DefaultRecentDays           int               `json:"default_recent_days"`
	LoadingRows                 int               `json:"loading_rows"`
	UserIDPreviewCharacters     int               `json:"user_id_preview_characters"`
	ResourceIDPreviewCharacters int               `json:"resource_id_preview_characters"`
	PreviewSuffix               string            `json:"preview_suffix"`
	EmptyPlaceholder            string            `json:"empty_placeholder"`
	DateLocale                  string            `json:"date_locale"`
	DateTimeOptions             map[string]string `json:"date_time_options"`
	ActionBadgeClasses          map[string]string `json:"action_badge_classes"`
	ActionPrefixBadgeClasses    map[string]string `json:"action_prefix_badge_classes"`
	DefaultBadgeClass           string            `json:"default_badge_class"`
	Text                        map[string]string `json:"text"`
}

type AdministrationGenerationsSchema struct {
	API          AdministrationGenerationsAPISchema          `json:"api"`
	Permission   string                                      `json:"permission"`
	Limits       AdministrationGenerationsLimitsSchema       `json:"limits"`
	Assets       AdministrationGenerationAssetSchema         `json:"assets"`
	Audit        AdministrationGenerationsAuditSchema        `json:"audit"`
	Messages     AdministrationGenerationsMessageSchema      `json:"messages"`
	Presentation AdministrationGenerationsPresentationSchema `json:"presentation"`
}

type AdministrationGenerationsAPISchema struct {
	Fields         AdministrationGenerationFieldSchema         `json:"fields"`
	Query          AdministrationGenerationQueryFieldSchema    `json:"query"`
	PathParameters AdministrationGenerationPathParameterSchema `json:"path_parameters"`
}

type AdministrationGenerationFieldSchema struct {
	Data             string `json:"data"`
	Error            string `json:"error"`
	Total            string `json:"total"`
	Page             string `json:"page"`
	PageSize         string `json:"page_size"`
	ID               string `json:"id"`
	UserID           string `json:"user_id"`
	UserName         string `json:"user_name"`
	UserEmail        string `json:"user_email"`
	TaskType         string `json:"task_type"`
	TaskID           string `json:"task_id"`
	EstimatedCredits string `json:"estimated_credits"`
	TaskStatus       string `json:"task_status"`
	Params           string `json:"params"`
	ResultURLs       string `json:"result_urls"`
	ThumbnailURL     string `json:"thumbnail_url"`
	CreatedAt        string `json:"created_at"`
	FinishedAt       string `json:"finished_at"`
	Ledger           string `json:"ledger"`
	TaskCache        string `json:"task_cache"`
	// InputDerivations is optional: when empty the detail omits re-encoded
	// input images (#276).
	InputDerivations string `json:"input_derivations,omitempty"`
	Status           string `json:"status"`
	ProviderID       string `json:"provider_id"`
	CacheData        string `json:"cache_data"`
	CachedAt         string `json:"cached_at"`
	UpdatedAt        string `json:"updated_at"`
	Redirect         string `json:"redirect"`
}

type AdministrationGenerationQueryFieldSchema struct {
	Page     string `json:"page"`
	PageSize string `json:"page_size"`
	UserID   string `json:"user_id"`
	TaskType string `json:"task_type"`
	Status   string `json:"status"`
	From     string `json:"from"`
	To       string `json:"to"`
}

type AdministrationGenerationPathParameterSchema struct {
	TaskID string `json:"task_id"`
}

type AdministrationGenerationsLimitsSchema struct {
	DefaultPage       int `json:"default_page"`
	DefaultPageSize   int `json:"default_page_size"`
	MaximumPageSize   int `json:"maximum_page_size"`
	DefaultRecentDays int `json:"default_recent_days"`
}

type AdministrationGenerationAssetSchema struct {
	ThumbnailRole                     string            `json:"thumbnail_role"`
	DownloadFilenameTemplate          string            `json:"download_filename_template"`
	AssetFilenameTemplate             string            `json:"asset_filename_template"`
	VariantSeparator                  string            `json:"variant_separator"`
	DuplicateSeparator                string            `json:"duplicate_separator"`
	OrdinalBase                       int               `json:"ordinal_base"`
	ArchiveMediaType                  string            `json:"archive_media_type"`
	DefaultMediaType                  string            `json:"default_media_type"`
	UnknownExtension                  string            `json:"unknown_extension"`
	MaximumPackageBytes               int64             `json:"maximum_package_bytes"`
	MediaTypeExtensions               map[string]string `json:"media_type_extensions"`
	DownloadExcludedRoles             []string          `json:"download_excluded_roles"`
	LegacyCacheRootSetting            string            `json:"legacy_cache_root_setting"`
	LegacyCachePrefixCharacters       int               `json:"legacy_cache_prefix_characters"`
	LegacyCacheMetadataSuffix         string            `json:"legacy_cache_metadata_suffix"`
	LegacyCacheContentTypeField       string            `json:"legacy_cache_content_type_field"`
	RemoteDownloadTimeoutMilliseconds int               `json:"remote_download_timeout_milliseconds"`
	MaximumRemoteRedirects            int               `json:"maximum_remote_redirects"`
	RemoteThumbnailURLQuery           string            `json:"remote_thumbnail_url_query"`
	RemoteThumbnailTaskIDQuery        string            `json:"remote_thumbnail_task_id_query"`
	PreviewImageExtensions            []string          `json:"preview_image_extensions"`
}

type AdministrationGenerationsAuditSchema struct {
	ReadAction     string `json:"read_action"`
	DownloadAction string `json:"download_action"`
	ResourceType   string `json:"resource_type"`
}

type AdministrationGenerationsMessageSchema struct {
	TaskNotFound            string `json:"task_not_found"`
	CacheNotFound           string `json:"cache_not_found"`
	NoDownloadableAssets    string `json:"no_downloadable_assets"`
	AllDownloadsFailed      string `json:"all_downloads_failed"`
	InvalidRequest          string `json:"invalid_request"`
	InternalError           string `json:"internal_error"`
	DownloadFailed          string `json:"download_failed"`
	UnknownDownloadResponse string `json:"unknown_download_response"`
}

type AdministrationGenerationsPresentationSchema struct {
	LoadingRows             int               `json:"loading_rows"`
	UserIDPreviewCharacters int               `json:"user_id_preview_characters"`
	TaskIDPreviewCharacters int               `json:"task_id_preview_characters"`
	PreviewSuffix           string            `json:"preview_suffix"`
	EmptyPlaceholder        string            `json:"empty_placeholder"`
	DateLocale              string            `json:"date_locale"`
	DateTimeOptions         map[string]string `json:"date_time_options"`
	Text                    map[string]string `json:"text"`
}

type AdministrationUsageSchema struct {
	API          AdministrationUsageAPISchema          `json:"api"`
	Permission   string                                `json:"permission"`
	Audit        AdministrationReadAuditSchema         `json:"audit"`
	Defaults     AdministrationUsageDefaultsSchema     `json:"defaults"`
	Validation   AdministrationUsageValidationSchema   `json:"validation"`
	Balance      AdministrationUsageBalanceSchema      `json:"balance"`
	Messages     AdministrationUsageMessageSchema      `json:"messages"`
	Presentation AdministrationUsagePresentationSchema `json:"presentation"`
}

type AdministrationUsageBalanceSchema struct {
	AccountID           string `json:"account_id"`
	HistoryDays         int    `json:"history_days"`
	SnapshotIDPrefix    string `json:"snapshot_id_prefix"`
	SnapshotWritePolicy string `json:"snapshot_write_policy"`
}

type AdministrationUsageAPISchema struct {
	Fields        AdministrationUsageFieldSchema        `json:"fields"`
	Query         AdministrationUsageQuerySchema        `json:"query"`
	BalanceFields AdministrationUsageBalanceFieldSchema `json:"balance_fields"`
}

type AdministrationUsageBalanceFieldSchema struct {
	CurrentBalance string `json:"current_balance"`
	History        string `json:"history"`
	Balance        string `json:"balance"`
	CapturedAt     string `json:"captured_at"`
}

type AdministrationUsageFieldSchema struct {
	Data             string `json:"data"`
	Error            string `json:"error"`
	UserID           string `json:"user_id"`
	UserName         string `json:"user_name"`
	UserEmail        string `json:"user_email"`
	TotalTasks       string `json:"total_tasks"`
	SucceededTasks   string `json:"succeeded_tasks"`
	FailedTasks      string `json:"failed_tasks"`
	EstimatedCredits string `json:"estimated_credits"`
	LastActiveAt     string `json:"last_active_at"`
	ByType           string `json:"by_type"`
	// Total holds the estimate of every listed user together (#327).
	Total string `json:"total"`
}

type AdministrationUsageQuerySchema struct {
	Days      string `json:"days"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

type AdministrationUsageDefaultsSchema struct {
	Days            int `json:"days"`
	CustomRangeDays int `json:"custom_range_days"`
}

type AdministrationUsageValidationSchema struct {
	MaximumDays int    `json:"maximum_days"`
	DatePattern string `json:"date_pattern"`
	DateLayout  string `json:"date_layout"`
	Timezone    string `json:"timezone"`
}

type AdministrationUsageMessageSchema struct {
	InvalidRange  string `json:"invalid_range"`
	InternalError string `json:"internal_error"`
}

type AdministrationUsageRangeSchema struct {
	Days  int    `json:"days"`
	Label string `json:"label"`
}

type AdministrationUsagePresentationSchema struct {
	QuickRanges         []AdministrationUsageRangeSchema `json:"quick_ranges"`
	LoadingRows         int                              `json:"loading_rows"`
	EmptyPlaceholder    string                           `json:"empty_placeholder"`
	ApproximationPrefix string                           `json:"approximation_prefix"`
	RangeSeparator      string                           `json:"range_separator"`
	DateSeparator       string                           `json:"date_separator"`
	DateLocale          string                           `json:"date_locale"`
	DateTimeOptions     map[string]string                `json:"date_time_options"`
	Text                map[string]string                `json:"text"`
}

type AdministrationUserAssetsSchema struct {
	API          AdministrationUserAssetsAPISchema          `json:"api"`
	Permission   string                                     `json:"permission"`
	Audit        AdministrationReadAuditSchema              `json:"audit"`
	Limits       AdministrationUserAssetsLimitsSchema       `json:"limits"`
	Filters      AdministrationUserAssetsFiltersSchema      `json:"filters"`
	Messages     AdministrationUserAssetsMessageSchema      `json:"messages"`
	Presentation AdministrationUserAssetsPresentationSchema `json:"presentation"`
}

type AdministrationUserAssetsAPISchema struct {
	EnvelopeFields   AdministrationUserAssetsEnvelopeFieldSchema   `json:"envelope_fields"`
	PageFields       AdministrationUserAssetsPageFieldSchema       `json:"page_fields"`
	UserFields       AdministrationUserAssetsUserFieldSchema       `json:"user_fields"`
	SummaryFields    AdministrationUserAssetsSummaryFieldSchema    `json:"summary_fields"`
	UploadFields     AdministrationUserAssetsUploadFieldSchema     `json:"upload_fields"`
	GenerationFields AdministrationUserAssetsGenerationFieldSchema `json:"generation_fields"`
	DownloadFields   AdministrationUserAssetsDownloadFieldSchema   `json:"download_fields"`
	Query            AdministrationUserAssetsQuerySchema           `json:"query"`
	PathParameters   AdministrationUserAssetsPathParameterSchema   `json:"path_parameters"`
}

type AdministrationUserAssetsEnvelopeFieldSchema struct {
	Error       string `json:"error"`
	User        string `json:"user"`
	Summary     string `json:"summary"`
	Uploads     string `json:"uploads"`
	Generations string `json:"generations"`
	Downloads   string `json:"downloads"`
}

type AdministrationUserAssetsPageFieldSchema struct {
	Data     string `json:"data"`
	Total    string `json:"total"`
	Page     string `json:"page"`
	PageSize string `json:"page_size"`
}

type AdministrationUserAssetsUserFieldSchema struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Nickname  string `json:"nickname"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	IsAdmin   string `json:"is_admin"`
	IsActive  string `json:"is_active"`
	CreatedAt string `json:"created_at"`
	LastLogin string `json:"last_login"`
}

type AdministrationUserAssetsSummaryFieldSchema struct {
	TotalUploads      string `json:"total_uploads"`
	TotalGenerations  string `json:"total_generations"`
	TotalDownloads    string `json:"total_downloads"`
	TotalStorageBytes string `json:"total_storage_bytes"`
}

type AdministrationUserAssetsUploadFieldSchema struct {
	ID               string `json:"id"`
	AssetType        string `json:"asset_type"`
	OriginalFilename string `json:"original_filename"`
	StoredFilename   string `json:"stored_filename"`
	MimeType         string `json:"mime_type"`
	FileSize         string `json:"file_size"`
	StoragePath      string `json:"storage_path"`
	CreatedAt        string `json:"created_at"`
}

type AdministrationUserAssetsGenerationFieldSchema struct {
	ID               string `json:"id"`
	TaskType         string `json:"task_type"`
	TaskID           string `json:"task_id"`
	TaskStatus       string `json:"task_status"`
	ResultURLs       string `json:"result_urls"`
	Params           string `json:"params"`
	EstimatedCredits string `json:"estimated_credits"`
	ThumbnailURL     string `json:"thumbnail_url"`
	CreatedAt        string `json:"created_at"`
	FinishedAt       string `json:"finished_at"`
}

type AdministrationUserAssetsDownloadFieldSchema struct {
	ID           string `json:"id"`
	Action       string `json:"action"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Detail       string `json:"detail"`
	CreatedAt    string `json:"created_at"`
}

type AdministrationUserAssetsQuerySchema struct {
	PageSize        string `json:"page_size"`
	UploadsPage     string `json:"uploads_page"`
	GenerationsPage string `json:"generations_page"`
	DownloadsPage   string `json:"downloads_page"`
	From            string `json:"from"`
	To              string `json:"to"`
}

type AdministrationUserAssetsPathParameterSchema struct {
	UserID string `json:"user_id"`
}

type AdministrationUserAssetsLimitsSchema struct {
	DefaultPage       int `json:"default_page"`
	DefaultPageSize   int `json:"default_page_size"`
	MaximumPageSize   int `json:"maximum_page_size"`
	FrontendPageSize  int `json:"frontend_page_size"`
	DefaultRecentDays int `json:"default_recent_days"`
}

type AdministrationUserAssetsFiltersSchema struct {
	DownloadActions            []AdministrationUserAssetsDownloadActionSchema `json:"download_actions"`
	ImageAssetType             string                                         `json:"image_asset_type"`
	ModelAssetType             string                                         `json:"model_asset_type"`
	ImageMediaTypePrefix       string                                         `json:"image_media_type_prefix"`
	GeneratedImageResourceType string                                         `json:"generated_image_resource_type"`
}

type AdministrationUserAssetsDownloadActionSchema struct {
	Action    string `json:"action"`
	Label     string `json:"label"`
	TextClass string `json:"text_class"`
	DotClass  string `json:"dot_class"`
}

type AdministrationUserAssetsMessageSchema struct {
	UserNotFound  string `json:"user_not_found"`
	InternalError string `json:"internal_error"`
}

type AdministrationUserAssetsPresentationSchema struct {
	LoadingRows                 int               `json:"loading_rows"`
	FilenamePreviewCharacters   int               `json:"filename_preview_characters"`
	TaskIDPreviewCharacters     int               `json:"task_id_preview_characters"`
	ResourceIDPreviewCharacters int               `json:"resource_id_preview_characters"`
	PreviewSuffix               string            `json:"preview_suffix"`
	EmptyPlaceholder            string            `json:"empty_placeholder"`
	DateLocale                  string            `json:"date_locale"`
	DateTimeOptions             map[string]string `json:"date_time_options"`
	BytesPerUnit                int               `json:"bytes_per_unit"`
	SizeDecimalPlaces           int               `json:"size_decimal_places"`
	SizeUnits                   []string          `json:"size_units"`
	AssetTypeLabels             map[string]string `json:"asset_type_labels"`
	Text                        map[string]string `json:"text"`
}

// PublicApplicationSchema 是可以交给浏览器消费的 Product Schema 投影。
// 服务端环境变量接线和敏感设置元数据只保留在 ApplicationSchema。
type PublicApplicationSchema struct {
	AgentConfiguration PublicAgentConfigurationSchema `json:"agent_configuration"`
	LLMSettings        *PublicLLMSettingsSchema       `json:"llm_settings,omitempty"`
	Revision           string                         `json:"revision"`
	Runtime            PublicRuntimeSchema            `json:"runtime"`
	Content            ContentSchema                  `json:"content"`
	Chat               PublicChatSchema               `json:"chat"`
	Canvas             PublicCanvasSchema             `json:"canvas"`
	TaskHistory        TaskHistorySchema              `json:"task_history"`
	TaskStream         PublicTaskStreamSchema         `json:"task_stream"`
	Administration     PublicAdministrationSchema     `json:"administration"`
	Capabilities       []CapabilityPresentation       `json:"capabilities"`
	TaskStates         []TaskStatePresentation        `json:"task_states"`
	AuditActions       []ValueLabel                   `json:"audit_actions"`
	// DeliveryFormats lists the formats a finished 3D task is expected to
	// deliver, in presentation order, so the workspace can always show every
	// one of them. It carries only id and label; the conversion routes and
	// source rules stay server-side and reach the browser as the coverage
	// projection of one task.
	DeliveryFormats []PublicDeliveryFormat     `json:"delivery_formats"`
	Authentication  PublicAuthenticationSchema `json:"authentication"`
	Localization    PublicLocalizationSchema   `json:"localization"`
}

// PublicDeliveryFormat is the browser-visible part of ModelFormatSchema.
type PublicDeliveryFormat struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type PublicRuntimeSchema struct {
	HTTP          PublicHTTPRuntimeSchema          `json:"http"`
	InputAssets   PublicInputAssetRuntimeSchema    `json:"input_assets"`
	Cache         PublicCacheRuntimeSchema         `json:"cache"`
	AssetDelivery PublicAssetDeliveryRuntimeSchema `json:"asset_delivery"`
	Frontend      FrontendRuntimeSchema            `json:"frontend"`
	// GenerationMessages publishes only the reference-limit pair, so the
	// composer says what the server answers in the same words (#329 note
	// 99647); the other generation messages stay server-side.
	GenerationMessages PublicGenerationMessagesSchema `json:"generation_messages"`
}

// PublicGenerationMessagesSchema is the reference-limit text pair of
// runtime.generation_messages (see schema.ExecutionMessages).
type PublicGenerationMessagesSchema struct {
	ReferenceLimit    string `json:"reference_limit,omitempty"`
	ReferenceLimitOne string `json:"reference_limit_one,omitempty"`
	ReferenceCapacity string `json:"reference_capacity,omitempty"`
}

// Public DTOs list browser-owned fields explicitly. Adding a server setting to
// ApplicationSchema cannot publish it without a deliberate change here.
type PublicHTTPRuntimeSchema struct {
	Routes                CoreHTTPRoutesSchema        `json:"routes"`
	LegacyRoutes          LegacyHTTPRoutesSchema      `json:"legacy_routes"`
	LegacyProviderBalance LegacyProviderBalanceSchema `json:"legacy_provider_balance"`
}

type PublicInputAssetRuntimeSchema struct {
	ModelProfile     *ModelInputProfile   `json:"model_profile,omitempty"`
	UploadFieldName  string               `json:"upload_field_name"`
	UploadMaxBytes   int64                `json:"upload_max_bytes"`
	DefaultAssetType string               `json:"default_asset_type"`
	API              InputAssetAPISchema  `json:"api"`
	TypeRules        []InputAssetTypeRule `json:"type_rules"`
}

type PublicCacheRuntimeSchema struct {
	BytesPerMegabyte int64             `json:"bytes_per_megabyte"`
	Fields           CacheStatusFields `json:"fields"`
}

type PublicAssetDeliveryRuntimeSchema struct {
	Proxy        PublicAssetProxyRuntimeSchema `json:"proxy"`
	AssetPreview struct {
		Query        string `json:"query"`
		RequestValue string `json:"request_value"`
	} `json:"asset_preview"`
}

type PublicAssetProxyRuntimeSchema struct {
	API struct {
		Query AssetProxyQuerySchema `json:"query"`
	} `json:"api"`
	Security struct {
		AllowedSchemes []string `json:"allowed_schemes"`
		AllowedHosts   []string `json:"allowed_hosts,omitempty"`
	} `json:"security"`
	Thumbnail struct {
		RequestValue string `json:"request_value"`
	} `json:"thumbnail"`
}

type PublicChatSchema struct {
	API      PublicChatAPISchema `json:"api"`
	Defaults ChatDefaultsSchema  `json:"defaults"`
	Roles    ChatRolesSchema     `json:"roles"`
	State    ChatStateSchema     `json:"state"`
	Frontend ChatFrontendSchema  `json:"frontend"`
	Context  struct {
		ImageIndexBase                int    `json:"image_index_base"`
		MaximumSessionNotesCharacters int    `json:"maximum_session_notes_characters"`
		CanvasInputSource             string `json:"canvas_input_source"`
		// ReferenceFilename names a composer picture uploaded as an input asset at send time (#292), the
		// same base name the server gives a data URI it uploads as a reference.
		ReferenceFilename string `json:"reference_filename"`
	} `json:"context"`
	Stream PublicChatStreamSchema `json:"stream"`
	Tools  []PublicChatToolSchema `json:"tools"`
	Skills struct {
		Fields     ChatSkillCatalogFields    `json:"fields"`
		Creation   ChatSkillCreationSchema   `json:"creation"`
		Categories []ChatSkillCategorySchema `json:"categories"`
	} `json:"skills"`
	Presentation   ChatPresentationSchema   `json:"presentation"`
	ApprovalPolicy ChatApprovalPolicySchema `json:"approval_policy"`
	AgentScope     PublicChatAgentScope     `json:"agent_scope"`
	Harness        PublicHarnessSchema      `json:"harness"`
	// ContextCompaction is what the composer's context meter needs (#322);
	// the prompts stay on the server.
	ContextCompaction PublicChatContextCompaction `json:"context_compaction"`
	AgentStatus       PublicChatAgentStatus       `json:"agent_status"`
}

// PublicChatAgentStatus tells the browser whether the deployment's Agent can
// answer (chat.agent itself stays server-only); while it cannot, the reason
// is presentation.messages.agent_not_configured.
// Which setting is missing stays in the server log.
type PublicChatAgentStatus struct {
	Available bool `json:"available"`
}

// PublicChatAgentScope is what the browser needs of the Agent scopes: which
// ones exist and which workspace domains each leaves usable. The labels are
// content messages (ui.workspace.agent_scope.*); tool classes and the Agent's
// texts stay on the server.
type PublicChatAgentScope struct {
	DefaultMode string                     `json:"default_mode"`
	Modes       []PublicChatAgentScopeMode `json:"modes"`
}

// PublicChatContextCompaction is the browser's view of
// chat.context_compaction: whether compaction exists and the shares of the
// context window the meter colors and the server compacts at.
type PublicChatContextCompaction struct {
	Enabled            bool    `json:"enabled"`
	AutoThresholdRatio float64 `json:"auto_threshold_ratio"`
	WarningRatio       float64 `json:"warning_ratio"`
	KeepRecentRounds   int     `json:"keep_recent_rounds"`
}

// PublicChatAgentScopeMode is one scope as the browser sees it.
type PublicChatAgentScopeMode struct {
	ID               string   `json:"id"`
	WorkspaceDomains []string `json:"workspace_domains"`
}

type PublicChatAPISchema struct {
	Fields         ChatAPIFields           `json:"fields"`
	ResponseFields ChatAPIResponseFields   `json:"response_fields"`
	Attachment     ChatAttachmentAPIFields `json:"attachment"`
	PromptEnhance  struct {
		PromptField               string `json:"prompt_field"`
		CapabilityField           string `json:"capability_field"`
		ModelField                string `json:"model_field"`
		ResultField               string `json:"result_field"`
		PromptResultField         string `json:"prompt_result_field"`
		NegativePromptResultField string `json:"negative_prompt_result_field"`
		ErrorField                string `json:"error_field"`
	} `json:"prompt_enhance"`
}

type PublicChatStreamSchema struct {
	EventFields ChatStreamEventFields `json:"event_fields"`
	EventTypes  ChatStreamEventTypes  `json:"event_types"`
}

type PublicChatToolSchema struct {
	RequireUniqueOffering bool                      `json:"require_unique_offering,omitempty"`
	Name                  string                    `json:"name"`
	PendingCapability     string                    `json:"pending_capability"`
	Capability            schemacontract.Capability `json:"capability"`
	FallbackCapability    schemacontract.Capability `json:"fallback_capability"`
	// WorkspaceDomain is the workspace the tool's result belongs to; the
	// client shows that workspace when a result of the other domain arrives.
	WorkspaceDomain string `json:"workspace_domain"`
}

type PublicCanvasSchema struct {
	API      CanvasAPISchema `json:"api"`
	Defaults struct {
		BGStyle string `json:"bg_style"`
		// CoordinateVersion is sent with every canvas frame save (#246).
		CoordinateVersion int `json:"coordinate_version"`
	} `json:"defaults"`
	Limits       CanvasLimitsSchema       `json:"limits"`
	Frontend     CanvasFrontendSchema     `json:"frontend"`
	Presentation CanvasPresentationSchema `json:"presentation"`
}

type PublicTaskStreamSchema struct {
	Aliases []TaskStreamCapabilityAlias `json:"aliases"`
}

type PublicAdministrationSchema struct {
	Navigation  []AdministrationNavigationSchema      `json:"navigation"`
	Users       PublicAdministrationUsersSchema       `json:"users"`
	AuditLogs   PublicAdministrationAuditLogsSchema   `json:"audit_logs"`
	Generations PublicAdministrationGenerationsSchema `json:"generations"`
	Usage       PublicAdministrationUsageSchema       `json:"usage"`
	UserAssets  PublicAdministrationUserAssetsSchema  `json:"user_assets"`
}

type PublicAdministrationUsersSchema struct {
	API          AdministrationUsersAPISchema          `json:"api"`
	Limits       AdministrationUsersLimitsSchema       `json:"limits"`
	Messages     AdministrationUsersMessageSchema      `json:"messages"`
	Presentation AdministrationUsersPresentationSchema `json:"presentation"`
}

type PublicAdministrationAuditLogsSchema struct {
	API          AdministrationAuditLogsAPISchema          `json:"api"`
	Limits       AdministrationAuditLogsLimitsSchema       `json:"limits"`
	Messages     AdministrationAuditLogsMessageSchema      `json:"messages"`
	Presentation AdministrationAuditLogsPresentationSchema `json:"presentation"`
}

type PublicAdministrationGenerationAssets struct {
	DownloadFilenameTemplate   string   `json:"download_filename_template"`
	RemoteThumbnailURLQuery    string   `json:"remote_thumbnail_url_query"`
	RemoteThumbnailTaskIDQuery string   `json:"remote_thumbnail_task_id_query"`
	PreviewImageExtensions     []string `json:"preview_image_extensions"`
}

type PublicAdministrationGenerationsSchema struct {
	API          AdministrationGenerationsAPISchema          `json:"api"`
	Limits       AdministrationGenerationsLimitsSchema       `json:"limits"`
	Assets       PublicAdministrationGenerationAssets        `json:"assets"`
	Messages     AdministrationGenerationsMessageSchema      `json:"messages"`
	Presentation AdministrationGenerationsPresentationSchema `json:"presentation"`
}

type PublicAdministrationUsageSchema struct {
	API          AdministrationUsageAPISchema          `json:"api"`
	Defaults     AdministrationUsageDefaultsSchema     `json:"defaults"`
	Messages     AdministrationUsageMessageSchema      `json:"messages"`
	Presentation AdministrationUsagePresentationSchema `json:"presentation"`
}

type PublicAdministrationUserAssetsSchema struct {
	API          AdministrationUserAssetsAPISchema          `json:"api"`
	Limits       AdministrationUserAssetsLimitsSchema       `json:"limits"`
	Filters      AdministrationUserAssetsFiltersSchema      `json:"filters"`
	Messages     AdministrationUserAssetsMessageSchema      `json:"messages"`
	Presentation AdministrationUserAssetsPresentationSchema `json:"presentation"`
}

type PublicAuthenticationSchema struct {
	API          PublicAuthenticationAPI `json:"api"`
	PublicPaths  []string                `json:"public_paths"`
	SessionRetry SessionRetrySchema      `json:"session_retry"`
	Messages     AuthenticationTexts     `json:"messages"`
	Login        LoginPresentation       `json:"login"`
}

type PublicAuthenticationAPI struct {
	IDField       string `json:"id_field"`
	UsernameField string `json:"username_field"`
	PasswordField string `json:"password_field"`
	NameField     string `json:"name_field"`
	EmailField    string `json:"email_field"`
	ErrorField    string `json:"error_field"`
	UserField     string `json:"user_field"`
	IsAdminField  string `json:"is_admin_field"`
}

// CanvasSchema 同时拥有画板领域默认值、旧 HTTP 线协议和可见文本。
// 领域服务只实现项目/节点状态算法，不拥有产品字段名、路由或显示文本。
type CanvasSchema struct {
	API          CanvasAPISchema          `json:"api"`
	Defaults     CanvasDefaultsSchema     `json:"defaults"`
	Limits       CanvasLimitsSchema       `json:"limits"`
	Audit        CanvasAuditSchema        `json:"audit"`
	Frontend     CanvasFrontendSchema     `json:"frontend"`
	Presentation CanvasPresentationSchema `json:"presentation"`
}

type CanvasAPISchema struct {
	Fields         CanvasAPIFields         `json:"fields"`
	PathParameters CanvasAPIPathParameters `json:"path_parameters"`
}

type CanvasAPIFields struct {
	ID        string `json:"id"`
	Created   string `json:"created"`
	UserID    string `json:"user_id"`
	Name      string `json:"name"`
	SessionID string `json:"session_id"`
	Viewport  string `json:"viewport"`
	BGStyle   string `json:"bg_style"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Nodes     string `json:"nodes"`
	Count     string `json:"count"`
	NodeCount string `json:"node_count"`
	OK        string `json:"ok"`
	Error     string `json:"error"`
	ProjectID string `json:"project_id"`
	Type      string `json:"type"`
	X         string `json:"x"`
	Y         string `json:"y"`
	Width     string `json:"width"`
	Height    string `json:"height"`
	ZIndex    string `json:"z_index"`
	Data      string `json:"data"`
	Visible   string `json:"visible"`
	Locked    string `json:"locked"`
	Revision  string `json:"revision"`
	AssetID   string `json:"asset_id"`
	URL       string `json:"url"`
	ImageURL  string `json:"image_url"`
	// PlacedResults 是画板已放置结果清单（#239/#262）的线协议字段名：
	// GET 项目时 null 表示尚未初始化，节点保存请求可选携带同名数组。
	PlacedResults string `json:"placed_results"`
	// CanvasWidth、CanvasHeight 与 CoordinateVersion 是画布像素尺寸（#246）的线协议
	// 字段名：GET 项目时三者为 null 表示旧画布尚未迁移；节点保存请求可同时携带。
	CanvasWidth       string `json:"canvas_width"`
	CanvasHeight      string `json:"canvas_height"`
	CoordinateVersion string `json:"coordinate_version"`
}

type CanvasAPIPathParameters struct {
	ProjectID string `json:"project_id"`
}

type CanvasDefaultsSchema struct {
	ProjectName         string  `json:"project_name"`
	Viewport            string  `json:"viewport"`
	BGStyle             string  `json:"bg_style"`
	ProjectIDPrefix     string  `json:"project_id_prefix"`
	NodeIDPrefix        string  `json:"node_id_prefix"`
	NodeX               float64 `json:"node_x"`
	NodeY               float64 `json:"node_y"`
	NodeWidth           float64 `json:"node_width"`
	NodeHeight          float64 `json:"node_height"`
	NodeZIndex          int     `json:"node_z_index"`
	NodeData            string  `json:"node_data"`
	NodeVisible         bool    `json:"node_visible"`
	NodeLocked          bool    `json:"node_locked"`
	MergeFilenamePrefix string  `json:"merge_filename_prefix"`
	MergeExtension      string  `json:"merge_extension"`
	MergeMediaType      string  `json:"merge_media_type"`
	MergeAssetType      string  `json:"merge_asset_type"`
	// CoordinateVersion 是当前画布坐标契约版本（#246）：2 = 世界坐标 1 单位 = 1 像素。
	// 未记录版本的旧画布按 frontend.artboard.legacy_max_edge 一次性迁移。
	CoordinateVersion int `json:"coordinate_version"`
}

type CanvasLimitsSchema struct {
	MaximumMergeNodes  int `json:"maximum_merge_nodes"`
	MaximumBitmapNodes int `json:"maximum_bitmap_nodes"`
	MaximumDimension   int `json:"maximum_dimension"`
	// MinimumDimension 是画布像素尺寸（#246）的最小边长；最大边长沿用 MaximumDimension。
	MinimumDimension int `json:"minimum_dimension"`
	// MaximumPlacedResults 与 MaximumPlacedResultKeyCharacters 约束已放置结果清单
	// 的条目数（去重后）和单个键的字符数。
	MaximumPlacedResults             int `json:"maximum_placed_results"`
	MaximumPlacedResultKeyCharacters int `json:"maximum_placed_result_key_characters"`
}

type CanvasAuditSchema struct {
	CreateAction        string                        `json:"create_action"`
	DeleteAction        string                        `json:"delete_action"`
	MergeAction         string                        `json:"merge_action"`
	ProjectResourceType string                        `json:"project_resource_type"`
	MergeResourceType   string                        `json:"merge_resource_type"`
	DetailFields        CanvasAuditDetailFieldsSchema `json:"detail_fields"`
}

type CanvasAuditDetailFieldsSchema struct {
	Name string `json:"name"`
}

type CanvasFrontendSchema struct {
	FocusDelayMilliseconds           int     `json:"focus_delay_milliseconds"`
	AutoSaveDelayMilliseconds        int     `json:"auto_save_delay_milliseconds"`
	SavedIndicatorMilliseconds       int     `json:"saved_indicator_milliseconds"`
	ContextMenuBindDelayMilliseconds int     `json:"context_menu_bind_delay_milliseconds"`
	PermissionRedirectPath           string  `json:"permission_redirect_path"`
	InitialNodeX                     float64 `json:"initial_node_x"`
	InitialNodeY                     float64 `json:"initial_node_y"`
	NodeGapPixels                    float64 `json:"node_gap_pixels"`
	MinimumNodeSize                  float64 `json:"minimum_node_size"`
	MaximumNodeEdge                  float64 `json:"maximum_node_edge"`
	ModelCaptureLongEdgePixels       int     `json:"model_capture_long_edge_pixels"`
	// ToThreeDLongEdgePixels is the long edge of the board picture handed to an image-to-3D Offering when no
	// 2D output size names exact pixels (the artboard itself is only max_edge units across).
	ToThreeDLongEdgePixels int                  `json:"to_3d_long_edge_pixels"`
	Artboard               CanvasArtboardSchema `json:"artboard"`
	// InitialLayer is the bitmap layer a new canvas starts with (#246 phase 2).
	InitialLayer CanvasInitialLayerSchema `json:"initial_layer"`
	Brush        CanvasBrushSchema        `json:"brush"`
	Selection    CanvasSelectionSchema    `json:"selection"`
	// Mask is the tint of the quick-mask area on the board (#310), separate from the selection tint.
	Mask                   CanvasMaskSchema         `json:"mask"`
	Snap                   CanvasSnapSchema         `json:"snap"`
	UploadTaskID           string                   `json:"upload_task_id"`
	ExportFilenameFallback string                   `json:"export_filename_fallback"`
	ExportExtension        string                   `json:"export_extension"`
	Export                 CanvasExportSchema       `json:"export"`
	Viewport               CanvasViewportSchema     `json:"viewport"`
	Drawing                CanvasDrawingSchema      `json:"drawing"`
	History                CanvasHistorySchema      `json:"history"`
	Clipboard              CanvasClipboardSchema    `json:"clipboard"`
	Style                  CanvasStyleSchema        `json:"style"`
	Backgrounds            []CanvasBackgroundSchema `json:"backgrounds"`
	Keyboard               CanvasKeyboardSchema     `json:"keyboard"`
	Text                   CanvasTextSchema         `json:"text"`
	RelativeTime           CanvasRelativeTimeSchema `json:"relative_time"`
	// StageActions declares every button of the stage toolbar in display order; the frontend only binds
	// a handler to each id and never keeps a second list of names, icons or hints.
	StageActions []CanvasStageActionSchema `json:"stage_actions"`
	// ProcessingAction controls only the presentation of capability-derived processing navigation.
	// It neither chooses an Offering nor declares its execution kind or actual cost.
	ProcessingAction CanvasActionPresentationSchema `json:"processing_action"`
	// Tools declares every button of the stage's left rails in display order: the 2D canvas tool rail and the
	// 3D viewer's view bar. The frontend only binds a handler to each id and never keeps a second list of
	// names, icons, hints or shortcuts.
	Tools []CanvasToolSchema `json:"tools"`
	// PanelTabs declares the right panel: its width and, per kind of stage object, which tabs it has, in what
	// order, which one opens first, and the tab to open instead when the first has nothing to offer. The
	// frontend only binds a body to each tab id and never keeps a second list of tabs, names, icons or hints.
	PanelTabs CanvasPanelTabsSchema `json:"panel_tabs"`
}

// CanvasActionPresentationSchema shares the stage-action group, cost-hint and display-marker vocabulary.
// A process_panel hint may be unmarked navigation; approval_card requires a mark and free forbids it.
// CallsModel is a legacy display name, never an execution or authorization decision.
type CanvasActionPresentationSchema struct {
	Group      string `json:"group"`
	CallsModel bool   `json:"calls_model"`
	CostHint   string `json:"cost_hint"`
}

// CanvasStageActionSchema is one stage-toolbar button. Kinds say on which stage object it appears (a
// picture, a 3D model, or the board while no version is on the stage); Group places it in the toolbar
// (produce / output / view). CallsModel marks a button that may spend credits, and CostHint says which
// cost sentence the hover hint carries (application.content.messages "ui.studio.hint.cost.<cost_hint>").
// Icon is a lucide kebab-case name; the *Key fields name application.content.messages entries.
// Shortcut, when set, names a canvas.frontend.keyboard setting whose key is printed beside the name.
// 3D processing steps are not listed here: they are generated from the processing capabilities.
type CanvasStageActionSchema struct {
	ID         string   `json:"id"`
	Kinds      []string `json:"kinds"`
	Group      string   `json:"group"`
	CallsModel bool     `json:"calls_model"`
	CostHint   string   `json:"cost_hint"`
	Icon       string   `json:"icon"`
	LabelKey   string   `json:"label_key"`
	HintKey    string   `json:"hint_key"`
	TargetKey  string   `json:"target_key"`
	Shortcut   string   `json:"shortcut,omitempty"`
}

// CanvasToolSchema is one button of a stage rail. Rail says which rail draws it: "canvas" is the 2D
// board's tool rail, "viewer" is the 3D viewer's view bar. Section groups buttons inside the rail (a thin
// line separates sections): canvas has select / draw / snap / history, viewer has views. Rail tools never
// call a model, so unlike CanvasStageActionSchema there is no cost line: the hover hint is the name with its
// shortcut, what the tool does, and (code-supplied) why it is unavailable right now.
// Icon is a lucide kebab-case name; LabelKey and HintKey name application.content.messages entries.
// Shortcut, when set, names a canvas.frontend.keyboard setting that holds a single key; Modifiers (any of
// mod, shift, alt) are printed before it, so undo_key "z" with [mod] reads Ctrl+Z.
type CanvasToolSchema struct {
	ID        string   `json:"id"`
	Rail      string   `json:"rail"`
	Section   string   `json:"section"`
	Icon      string   `json:"icon"`
	LabelKey  string   `json:"label_key"`
	HintKey   string   `json:"hint_key"`
	Shortcut  string   `json:"shortcut,omitempty"`
	Modifiers []string `json:"modifiers,omitempty"`
}

// CanvasPanelTabsSchema declares the right panel (the one column at the right of the stage, always open).
// Tabs lists every tab it can show; Kinds says which of them each kind of stage object gets.
// WidthPixels is the column's width.
type CanvasPanelTabsSchema struct {
	WidthPixels int                     `json:"width_pixels"`
	Tabs        []CanvasPanelTabSchema  `json:"tabs"`
	Kinds       []CanvasPanelKindSchema `json:"kinds"`
}

// CanvasPanelTabSchema is one tab of the right panel. ID is one of the bodies the frontend can draw
// (layers, history, details, process, formats, parts); Icon is a lucide kebab-case name; LabelKey and HintKey
// name application.content.messages entries (the hover hint is the name, its shortcut and the one-line hint).
// Shortcut, when set, names a canvas.frontend.keyboard setting that holds a single key or shortcut: pressing
// it switches to this tab. The tab bar prints that key beside the name.
type CanvasPanelTabSchema struct {
	ID       string `json:"id"`
	Icon     string `json:"icon"`
	LabelKey string `json:"label_key"`
	HintKey  string `json:"hint_key"`
	Shortcut string `json:"shortcut,omitempty"`
}

// CanvasPanelKindSchema lists the tabs of one kind of stage object, left to right: "image" is a picture on
// the stage, "board" is the canvas with content but no version on the stage, "model" is a 3D model.
// Default is the tab that opens when the person has not chosen one for this kind (2D and 3D each remember
// the last tab for the visit). DefaultWhenUnavailable, when set, replaces Default while the default tab has
// nothing to offer; today only the "process" tab can be empty, when no processing step has a usable Offering.
type CanvasPanelKindSchema struct {
	Kind                   string   `json:"kind"`
	Tabs                   []string `json:"tabs"`
	Default                string   `json:"default"`
	DefaultWhenUnavailable string   `json:"default_when_unavailable,omitempty"`
}

// CanvasArtboardSchema 声明 2D 虚线画板区的世界坐标锚点与最大边长；
// 宽高比优先由 Offering 参数驱动，独立作图使用声明的默认比例。
type CanvasArtboardSchema struct {
	// CenterX/CenterY anchor the canvas centre in world coordinates; the canvas is
	// its pixel size around this point (coordinate version 2: one unit = one pixel).
	CenterX float64 `json:"center_x"`
	CenterY float64 `json:"center_y"`
	// Resize declares the anchor a canvas resize keeps content at.
	Resize CanvasResizeSchema `json:"resize"`
	// LegacyMaxEdge is the long edge, in world units, of the artboard of a canvas
	// saved before coordinate version 2; it is read only to migrate such a canvas.
	LegacyMaxEdge float64 `json:"legacy_max_edge"`
	// DefaultWidth/DefaultHeight size a new canvas when no 2D model names a size.
	DefaultWidth  int `json:"default_width"`
	DefaultHeight int `json:"default_height"`
	// RatioPixels maps an aspect ratio to the pixels a ratio-only Offering
	// (aspect_ratio without a pixel size) produces; it sizes the canvas for such a model.
	RatioPixels map[string]CanvasPixelSize `json:"ratio_pixels"`
	// RatioPixelTiers overrides RatioPixels for an Offering that also has a resolution
	// tier parameter: parameter name -> tier value -> ratio -> pixels (Gemini image_size 1K/2K/4K).
	RatioPixelTiers map[string]map[string]map[string]CanvasPixelSize `json:"ratio_pixel_tiers"`
	// FrameStrokeWidth and FrameLineDash style the dashed artboard outline in
	// screen pixels at any zoom (the board draws it with stroke scaling off).
	// They are separate from export.frame, which styles legacy frame nodes.
	FrameStrokeWidth float64                   `json:"frame_stroke_width"`
	FrameLineDash    []float64                 `json:"frame_line_dash"`
	Label            CanvasArtboardLabelSchema `json:"label"`
	// OutsideDim is the translucent veil over everything outside the dashed frame (it never takes pointer events).
	OutsideDim CanvasOutsideDimSchema `json:"outside_dim"`
	// Transparency is the checkerboard drawn inside the frame while the exported canvas background is
	// transparent (#246): the pixel area is visible and so is what will export as alpha 0.
	Transparency CanvasTransparencySchema `json:"transparency"`
}

// CanvasInitialLayerSchema declares the bitmap layer a new canvas starts with (#246): a normal, unlocked
// picture layer covering the canvas that the brush and eraser paint on without inserting a picture first.
// Fill is export.transparent_background_value (transparent) or a #rrggbb colour.
type CanvasInitialLayerSchema struct {
	Enabled bool   `json:"enabled"`
	Fill    string `json:"fill"`
	// MaximumBitmapPixels caps the bitmap the brush paints the layer in (width*height); a larger canvas
	// gets a proportionally smaller bitmap stretched over the frame, so an 8192x8192 canvas never
	// allocates a 256 MB bitmap or uploads one per stroke batch.
	MaximumBitmapPixels int `json:"maximum_bitmap_pixels"`
}

// CanvasResizeSchema declares where a canvas resize keeps content (#246): the default anchor and the
// anchors offered. Content keeps its pixel size and its position relative to the anchor.
type CanvasResizeSchema struct {
	DefaultAnchor string   `json:"default_anchor"`
	Anchors       []string `json:"anchors"`
}

// CanvasPixelSize is a whole-pixel width and height.
type CanvasPixelSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// CanvasTransparencySchema is the checkerboard of the transparent canvas area: CellPixels is one square
// in screen pixels (it keeps its size at any zoom), the two colours alternate at Opacity (0,1].
type CanvasTransparencySchema struct {
	CellPixels float64 `json:"cell_pixels"`
	LightColor string  `json:"light_color"`
	DarkColor  string  `json:"dark_color"`
	Opacity    float64 `json:"opacity"`
}

// CanvasOutsideDimSchema colours the veil outside the artboard; Opacity is 0..1.
type CanvasOutsideDimSchema struct {
	Color   string  `json:"color"`
	Opacity float64 `json:"opacity"`
}

// CanvasBrushSchema bounds the pixel brush (also the quick-mask brush) in canvas pixels.
type CanvasBrushSchema struct {
	MinimumSize float64 `json:"minimum_size"`
	MaximumSize float64 `json:"maximum_size"`
	DefaultSize float64 `json:"default_size"`
}

// CanvasSelectionSchema styles the marching-ants outline and the tint of the canvas-level selection.
type CanvasSelectionSchema struct {
	AntsDash             []float64 `json:"ants_dash"`
	AntsTickMilliseconds int       `json:"ants_tick_milliseconds"`
	TintColor            string    `json:"tint_color"`
	TintOpacity          float64   `json:"tint_opacity"`
	// PreviewMaxEdge caps the pixel size of the raster preview of the selection.
	PreviewMaxEdge int `json:"preview_max_edge"`
}

// CanvasMaskSchema tints the quick-mask area on the board (#310): every stroke, region, erase and inversion is
// merged into one area and this tint is laid over it once, so overlaps never darken. It is its own key rather
// than the selection tint so the mask stays clearly readable (#309).
type CanvasMaskSchema struct {
	// TintColorToken names a CSS custom property such as --info.
	TintColorToken string  `json:"tint_color_token"`
	TintOpacity    float64 `json:"tint_opacity"`
}

// CanvasArtboardLabelSchema styles the size label inside the artboard's top-left corner (#282), in screen
// pixels at any zoom.
type CanvasArtboardLabelSchema struct {
	CommonRatios   []string `json:"common_ratios"`
	FontSizePixels float64  `json:"font_size_pixels"`
	// OffsetPixels is the inset from the dashed frame.
	OffsetPixels float64 `json:"offset_pixels"`
	// ColorToken names a CSS custom property such as --muted-foreground.
	ColorToken string `json:"color_token"`
	// PaddingPixels, BackgroundToken and BackgroundOpacity draw the plate that keeps the label readable over pictures.
	PaddingPixels     float64 `json:"padding_pixels"`
	BackgroundToken   string  `json:"background_token"`
	BackgroundOpacity float64 `json:"background_opacity"`
}

// CanvasSnapSchema declares drag snapping: the default of the toolbar toggle,
// the screen-pixel capture distance, the guide line style, and whether group
// members are snap targets of their own.
type CanvasSnapSchema struct {
	DefaultEnabled   bool    `json:"default_enabled"`
	ThresholdPixels  float64 `json:"threshold_pixels"`
	GuideStrokeWidth float64 `json:"guide_stroke_width"`
	// GuideColorToken names a CSS custom property such as --reminder.
	GuideColorToken string `json:"guide_color_token"`
	// GroupMemberTargets: a group is always one snap target (the box of its
	// visible members); true also keeps each member's own edges as secondary
	// targets, used only on an axis no unit is within reach of (#273).
	GroupMemberTargets bool `json:"group_member_targets"`
}

type CanvasExportSchema struct {
	PaddingPixels              float64 `json:"padding_pixels"`
	DefaultBackgroundColor     string  `json:"default_background_color"`
	TransparentBackgroundValue string  `json:"transparent_background_value"`
	// ArtboardBackgroundColor is what every artboard picture is drawn over (upload, inpaint, to-3D,
	// merge, export): transparent_background_value keeps empty canvas transparent (#271), or #rrggbb.
	ArtboardBackgroundColor string                   `json:"artboard_background_color"`
	OutputMediaType         string                   `json:"output_media_type"`
	ImagePlaceholderColor   string                   `json:"image_placeholder_color"`
	Frame                   CanvasExportFrameSchema  `json:"frame"`
	Arrow                   CanvasExportArrowSchema  `json:"arrow"`
	Pencil                  CanvasExportPencilSchema `json:"pencil"`
}

type CanvasExportFrameSchema struct {
	StrokeColor string    `json:"stroke_color"`
	StrokeWidth float64   `json:"stroke_width"`
	LineDash    []float64 `json:"line_dash"`
}

type CanvasExportArrowSchema struct {
	HeadLength       float64 `json:"head_length"`
	HeadAngleDivisor float64 `json:"head_angle_divisor"`
}

type CanvasExportPencilSchema struct {
	LineCap  string `json:"line_cap"`
	LineJoin string `json:"line_join"`
}

// CanvasTextSchema declares how text layers look: the canvas and the exported picture read the
// same family, line height and default colour, so a text node is the same in both. The colour is
// fixed and never follows the interface theme.
type CanvasTextSchema struct {
	DefaultColor string  `json:"default_color"`
	FontFamily   string  `json:"font_family"`
	LineHeight   float64 `json:"line_height"`
}

type CanvasViewportSchema struct {
	DefaultZoom          float64 `json:"default_zoom"`
	DefaultPanX          float64 `json:"default_pan_x"`
	DefaultPanY          float64 `json:"default_pan_y"`
	MinimumZoom          float64 `json:"minimum_zoom"`
	MaximumZoom          float64 `json:"maximum_zoom"`
	ZoomStep             float64 `json:"zoom_step"`
	DecimalPlaces        int     `json:"decimal_places"`
	PercentageMultiplier float64 `json:"percentage_multiplier"`
	FitPaddingPixels     float64 `json:"fit_padding_pixels"`
	// WheelLineHeightPixels converts line-mode wheel deltas (some mice) to pixels for panning.
	WheelLineHeightPixels float64 `json:"wheel_line_height_pixels"`
}

type CanvasDrawingSchema struct {
	DefaultNodeSize              float64 `json:"default_node_size"`
	TextNodeWidth                float64 `json:"text_node_width"`
	TextNodeHeight               float64 `json:"text_node_height"`
	InitialShapeSize             float64 `json:"initial_shape_size"`
	PencilMinimumPoints          int     `json:"pencil_minimum_points"`
	PencilPadding                float64 `json:"pencil_padding"`
	MinimumDrawSize              float64 `json:"minimum_draw_size"`
	LineHeight                   float64 `json:"line_height"`
	MinimumCommittedShapeSize    float64 `json:"minimum_committed_shape_size"`
	SelectionDragThresholdPixels float64 `json:"selection_drag_threshold_pixels"`
	// BrushIdleCommitMilliseconds is how long a pause in brush or eraser strokes waits before the
	// local bitmap is uploaded as one canvas step.
	BrushIdleCommitMilliseconds int `json:"brush_idle_commit_milliseconds"`
}

type CanvasHistorySchema struct {
	MaximumDepth                 int `json:"maximum_depth"`
	RecentLimit                  int `json:"recent_limit"`
	SnapshotThrottleMilliseconds int `json:"snapshot_throttle_milliseconds"`
}

type CanvasClipboardSchema struct {
	OffsetX float64 `json:"offset_x"`
	OffsetY float64 `json:"offset_y"`
}

type CanvasStyleSchema struct {
	DefaultStrokeWidth      float64 `json:"default_stroke_width"`
	DefaultStrokeColor      string  `json:"default_stroke_color"`
	DefaultShapeStrokeColor string  `json:"default_shape_stroke_color"`
	DefaultFillColor        string  `json:"default_fill_color"`
	// DefaultBackgroundColor is the background colour that D restores (the foreground is DefaultStrokeColor).
	DefaultBackgroundColor string    `json:"default_background_color"`
	TransparentFillValue   string    `json:"transparent_fill_value"`
	PresetColors           []string  `json:"preset_colors"`
	StrokeWidths           []float64 `json:"stroke_widths"`
	// FontSizes are the text size presets of the selection controls; DefaultFontSize must be one of them.
	FontSizes           []float64 `json:"font_sizes"`
	DefaultFontSize     float64   `json:"default_font_size"`
	RGBMinimum          int       `json:"rgb_minimum"`
	RGBMaximum          int       `json:"rgb_maximum"`
	SelectionBorder     string    `json:"selection_border"`
	SelectionBackground string    `json:"selection_background"`
}

type CanvasBackgroundSchema struct {
	Value           string  `json:"value"`
	LabelKey        string  `json:"label_key"`
	Pattern         string  `json:"pattern"`
	BackgroundColor string  `json:"background_color"`
	PatternColor    string  `json:"pattern_color"`
	CellSize        float64 `json:"cell_size"`
	PatternWidth    float64 `json:"pattern_width"`
}

type CanvasKeyboardSchema struct {
	SpaceCode string `json:"space_code"`
	// SpaceTapMilliseconds is the longest Space press that still counts as a tap (recenter).
	SpaceTapMilliseconds float64 `json:"space_tap_milliseconds"`
	// RecenterKey is pressed with Ctrl/Cmd to fit the dashed artboard, like the toolbar fit button.
	RecenterKey      string `json:"recenter_key"`
	UndoKey          string `json:"undo_key"`
	RedoKey          string `json:"redo_key"`
	AlternateRedoKey string `json:"alternate_redo_key"`
	CopyKey          string `json:"copy_key"`
	PasteKey         string `json:"paste_key"`
	SelectAllKey     string `json:"select_all_key"`
	GroupKey         string `json:"group_key"`
	// LayerForwardKey and LayerBackwardKey reorder layers with Ctrl/Cmd (Shift = to the edge).
	LayerForwardKey  string `json:"layer_forward_key"`
	LayerBackwardKey string `json:"layer_backward_key"`
	// Tool keys follow Photoshop: one bare key per tool. Shift on the brush key swaps brush and pencil,
	// Shift on the shape key steps through ShapeCycle.
	SelectToolKey     string   `json:"select_tool_key"`
	HandToolKey       string   `json:"hand_tool_key"`
	MarqueeToolKey    string   `json:"marquee_tool_key"`
	LassoToolKey      string   `json:"lasso_tool_key"`
	MaskToolKey       string   `json:"mask_tool_key"`
	CropToolKey       string   `json:"crop_tool_key"`
	BrushToolKey      string   `json:"brush_tool_key"`
	EraserToolKey     string   `json:"eraser_tool_key"`
	TextToolKey       string   `json:"text_tool_key"`
	ShapeToolKey      string   `json:"shape_tool_key"`
	ShapeCycle        []string `json:"shape_cycle"`
	ZoomToolKey       string   `json:"zoom_tool_key"`
	EyedropperToolKey string   `json:"eyedropper_tool_key"`
	// BrushSizeDownKey / BrushSizeUpKey resize the brush by BrushSizeStepRatio of its current size.
	BrushSizeDownKey   string  `json:"brush_size_down_key"`
	BrushSizeUpKey     string  `json:"brush_size_up_key"`
	BrushSizeStepRatio float64 `json:"brush_size_step_ratio"`
	// SwapColorsKey swaps foreground and background colour (in the quick mask it swaps paint and erase);
	// DefaultColorsKey restores the default pair.
	SwapColorsKey    string `json:"swap_colors_key"`
	DefaultColorsKey string `json:"default_colors_key"`
	// HelpKey opens the keyboard-shortcut panel (outside text fields).
	HelpKey string `json:"help_key"`
	// MaskConfirmKey finishes the mask (hands the painted area to inpainting); MaskExitKey leaves the mask tool.
	MaskConfirmKey string   `json:"mask_confirm_key"`
	MaskExitKey    string   `json:"mask_exit_key"`
	DeleteKeys     []string `json:"delete_keys"`
	// Shortcuts are written as "mod+alt+shift+e": modifiers (mod = Ctrl or Cmd) joined with the key by "+".
	GenerateShortcut        string `json:"generate_shortcut"`
	LockShortcut            string `json:"lock_shortcut"`
	MergeVisibleShortcut    string `json:"merge_visible_shortcut"`
	CutShortcut             string `json:"cut_shortcut"`
	DuplicateShortcut       string `json:"duplicate_shortcut"`
	DeselectShortcut        string `json:"deselect_shortcut"`
	InvertSelectionShortcut string `json:"invert_selection_shortcut"`
	ZoomInShortcut          string `json:"zoom_in_shortcut"`
	ZoomOutShortcut         string `json:"zoom_out_shortcut"`
	ZoomActualShortcut      string `json:"zoom_actual_shortcut"`
	ZoomSelectionShortcut   string `json:"zoom_selection_shortcut"`
	LayersPanelShortcut     string `json:"layers_panel_shortcut"`
	HistoryPanelShortcut    string `json:"history_panel_shortcut"`
	// NudgePixels / NudgeLargePixels are the arrow-key (and Shift+arrow) move distances in canvas pixels.
	NudgePixels      float64 `json:"nudge_pixels"`
	NudgeLargePixels float64 `json:"nudge_large_pixels"`
	// ReservedShortcuts are canvas keys whose browser default (select page text, bookmark, zoom the page...)
	// is always cancelled while the pointer or focus is on the canvas.
	ReservedShortcuts []string `json:"reserved_shortcuts"`
}

type CanvasRelativeTimeSchema struct {
	MillisecondsPerMinute int    `json:"milliseconds_per_minute"`
	MinutesPerHour        int    `json:"minutes_per_hour"`
	HoursPerDay           int    `json:"hours_per_day"`
	DaysPerMonth          int    `json:"days_per_month"`
	Locale                string `json:"locale"`
}

type CanvasPresentationSchema struct {
	Messages CanvasMessageSchema `json:"messages"`
	Text     map[string]string   `json:"text"`
}

type CanvasMessageSchema struct {
	Forbidden           string `json:"forbidden"`
	ProjectNotFound     string `json:"project_not_found"`
	NodesMustArray      string `json:"nodes_must_array"`
	BitmapLimitExceeded string `json:"bitmap_limit_exceeded"`
	InvalidMerge        string `json:"invalid_merge"`
	DimensionLimit      string `json:"dimension_limit"`
	MergeFailed         string `json:"merge_failed"`
	InternalError       string `json:"internal_error"`
	RevisionConflict    string `json:"revision_conflict"`
	// PlacedResultsInvalid 是已放置结果清单类型、空键、控制字符或上限校验失败的提示。
	PlacedResultsInvalid string `json:"placed_results_invalid"`
	// CanvasFrameInvalid 是画布像素尺寸或坐标契约版本校验失败的提示（#246）。
	CanvasFrameInvalid string `json:"canvas_frame_invalid"`
}

type ContentSchema struct {
	Brand             BrandPresentation              `json:"brand"`
	Service           ServicePresentation            `json:"service"`
	Home              HomePresentation               `json:"home"`
	Navigation        []NavigationPresentation       `json:"navigation"`
	AssetKinds        []ValueLabel                   `json:"asset_kinds"`
	AssetKindPatterns []AssetKindPatternPresentation `json:"asset_kind_patterns"`
	Messages          map[string]string              `json:"messages"`
}

type AssetKindPatternPresentation struct {
	Prefix          string `json:"prefix"`
	MessageKey      string `json:"message_key"`
	UppercaseSuffix bool   `json:"uppercase_suffix,omitempty"`
}

type BrandPresentation struct {
	Title               string `json:"title"`
	MetadataTitle       string `json:"metadata_title"`
	MetadataDescription string `json:"metadata_description"`
	VersionPrefix       string `json:"version_prefix"`
}

type NavigationPresentation struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Href      string `json:"href"`
	Icon      string `json:"icon"`
	Order     int    `json:"order"`
	Placement string `json:"placement"`
	AdminOnly bool   `json:"admin_only,omitempty"`
}

type ServicePresentation struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type HomePresentation struct {
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Links       []HomeLinkPresentation `json:"links"`
}

type HomeLinkPresentation struct {
	Href   string `json:"href"`
	Label  string `json:"label"`
	Method string `json:"method,omitempty"`
}

type StorageMaintenanceSchema struct {
	MinimumAgeSeconds int64 `json:"minimum_age_seconds"`
}

type RuntimeSchema struct {
	TaskRetention      schemacontract.TaskRetentionPolicy `json:"task_retention"`
	StorageMaintenance StorageMaintenanceSchema           `json:"storage_maintenance"`
	Configuration      RuntimeConfigurationSchema         `json:"configuration"`
	LegacyMigration    LegacyMigrationRuntimeSchema       `json:"legacy_migration"`
	Coordinator        CoordinatorRuntimeSchema           `json:"coordinator"`
	ProviderAccounts   map[string]ProviderAccountSchema   `json:"provider_accounts"`
	// ProviderConnections 声明运行时 Provider 连接（协议、设置引用、能力
	// 可用性、错误/状态词表等）。它是服务端接线真源，禁止进入公开投影。
	ProviderConnections []ProviderConnectionSchema `json:"provider_connections"`
	HTTP                HTTPRuntimeSchema          `json:"http"`
	InputAssets         InputAssetRuntimeSchema    `json:"input_assets"`
	TaskDeletion        TaskDeletionRuntimeSchema  `json:"task_deletion"`
	// TaskEvents 是公开事件面白名单（§8.1；P3），服务端专用：
	// PublicRuntimeSchema 按构造不含本节，公开 Schema 投影字节不变。
	TaskEvents         TaskEventsRuntimeSchema           `json:"task_events"`
	TaskAdmission      TaskAdmissionRuntimeSchema        `json:"task_admission"`
	TaskCancellation   schemacontract.CancellationPolicy `json:"task_cancellation"`
	GenerationMessages schemacontract.ExecutionMessages  `json:"generation_messages"`
	TaskAudit          TaskAuditRuntimeSchema            `json:"task_audit"`
	Cache              CacheRuntimeSchema                `json:"cache"`
	AssetDelivery      AssetDeliveryRuntimeSchema        `json:"asset_delivery"`
	// Agent 为 AGENTS 模式生成策略（服务端专用，不入公开投影）。
	Frontend FrontendRuntimeSchema `json:"frontend"`
}

type ProviderConditionalCapabilitySchema struct {
	Capabilities  []schemacontract.Capability `json:"capabilities"`
	AllOfSettings []string                    `json:"all_of_settings"`
}

type ProviderConnectionSchema struct {
	ID                      string                                `json:"id"`
	Protocol                string                                `json:"protocol"`
	AdapterKey              string                                `json:"adapter_key"`
	BaseURLSetting          string                                `json:"base_url_setting,omitempty"`
	APIKeySetting           string                                `json:"api_key_setting,omitempty"`
	AuthHeader              string                                `json:"auth_header,omitempty"`
	AuthPrefix              string                                `json:"auth_prefix,omitempty"`
	RequestTimeoutSeconds   int                                   `json:"request_timeout_seconds,omitempty"`
	AllowHTTPAssets         bool                                  `json:"allow_http_assets,omitempty"`
	Capabilities            []schemacontract.Capability           `json:"capabilities,omitempty"`
	ConditionalCapabilities []ProviderConditionalCapabilitySchema `json:"conditional_capabilities,omitempty"`
	ErrorMapping            *schemacontract.ErrorMapping          `json:"error_mapping,omitempty"`
	StatusMapping           *schemacontract.StatusMapping         `json:"status_mapping,omitempty"`
	// ResultRefetch declares that an accepted result of this connection can be
	// fetched again from its upstream job after the Task failed to store it
	// (#295). Absent: the provider's results cannot be fetched again.
	ResultRefetch *schemacontract.ResultRefetchPolicy `json:"result_refetch,omitempty"`
}

// LegacyMigrationRuntimeSchema 只服务一次性旧生产数据迁移。它不进入公开
// Product Schema 投影，避免把历史数据清理规则变成浏览器运行时接口。
type LegacyMigrationRuntimeSchema struct {
	CacheOnlyTaskExclusions []LegacyTaskExclusionSchema    `json:"cache_only_task_exclusions"`
	TaskOutputExcludedRoles []string                       `json:"task_output_excluded_roles"`
	AuditAssetRecovery      LegacyAuditAssetRecoverySchema `json:"audit_asset_recovery"`
	ThumbnailBackfill       LegacyThumbnailBackfillSchema  `json:"thumbnail_backfill"`
}

type LegacyThumbnailBackfillSchema struct {
	SourceCacheSubdirectory  string                                 `json:"source_cache_subdirectory"`
	BatchSize                int                                    `json:"batch_size"`
	MaximumAssetBytes        int64                                  `json:"maximum_asset_bytes"`
	OfferingMappings         []LegacyThumbnailOfferingMappingSchema `json:"offering_mappings,omitempty"`
	AcknowledgedSourceLosses []LegacyThumbnailSourceLossSchema      `json:"acknowledged_source_losses,omitempty"`
}

type LegacyThumbnailOfferingMappingSchema struct {
	LegacyProviderID string                    `json:"legacy_provider_id"`
	Capability       schemacontract.Capability `json:"capability"`
	OfferingID       schemacontract.OfferingID `json:"offering_id"`
}

type LegacyThumbnailSourceLossSchema struct {
	TaskID string `json:"task_id"`
	Reason string `json:"reason"`
}

// LegacyAuditAssetRecoverySchema 描述旧 Next 运行时写入审计副本时使用的
// 私有存储约定。ETL 只按精确文件名恢复，不把目录布局或命名规则写死在代码中。
type LegacyAuditAssetRecoverySchema struct {
	RootSubdirectory string `json:"root_subdirectory"`
	FilenameTemplate string `json:"filename_template"`
}

// LegacyTaskExclusionSchema 精确声明一个不是用户业务任务的旧缓存哨兵。
// task_id 与 external_type 必须同时匹配才可排除，防止宽泛规则吞掉异常数据。
type LegacyTaskExclusionSchema struct {
	TaskID       string `json:"task_id"`
	ExternalType string `json:"external_type"`
	Reason       string `json:"reason"`
}

// ChatSchema 同时描述 Chat Agent 的运行策略、旧 HTTP 线协议、SSE 事件协议
// 和浏览器展示内容。Go 代码只实现稳定算法，不拥有产品字段名、状态字面量、
// Prompt 或可见文本。
type ChatSchema struct {
	API          ChatAPISchema          `json:"api"`
	Defaults     ChatDefaultsSchema     `json:"defaults"`
	Roles        ChatRolesSchema        `json:"roles"`
	State        ChatStateSchema        `json:"state"`
	Frontend     ChatFrontendSchema     `json:"frontend"`
	Agent        ChatAgentSchema        `json:"agent"`
	Context      ChatContextSchema      `json:"context"`
	DSML         ChatDSMLSchema         `json:"dsml"`
	Stream       ChatStreamSchema       `json:"stream"`
	Tools        []ChatToolSchema       `json:"tools"`
	AgentTools   []ChatAgentToolSchema  `json:"agent_tools,omitempty"`
	Skills       ChatSkillsSchema       `json:"skills"`
	Presentation ChatPresentationSchema `json:"presentation"`
	Audit        ChatAuditSchema        `json:"audit"`
	// ApprovalPolicy declares the per-session paid-step approval modes and
	// which capabilities still need a human in manual mode.
	ApprovalPolicy ChatApprovalPolicySchema `json:"approval_policy"`
	// PendingApprovals declares how long an approval that the Agent asked for
	// stays approvable after its turn ended, and the texts posted when it is
	// decided.
	PendingApprovals ChatPendingApprovalsSchema `json:"pending_approvals"`
	// ThreeView declares the three-view group: the recipe it is built from,
	// the view that stands in for the group when a 3D model is made from it,
	// and the texts the group projection and the tools return.
	ThreeView ChatThreeViewSchema `json:"three_view"`
	// AgentScope declares the scopes ("2D 模式 / 3D 模式 / 自动模式") a session
	// can put the Agent in, which tool classes each one exposes, and the texts
	// the Agent reads about its scope.
	AgentScope ChatAgentScopeSchema `json:"agent_scope"`
	Harness    ChatHarnessSchema    `json:"harness"`
	// Choice declares the in-chat choice card (#321): the Agent-facing texts of
	// an answered or pending card and its bounds. Server side only; the card's
	// visible words are content messages ui.chat.choice.*.
	Choice ChatChoiceSchema `json:"choice"`
	// ContextCompaction declares how a long conversation is summarized for
	// the model (#322): the automatic threshold, how many recent rounds stay
	// verbatim and the prompts. The visible words are content messages
	// ui.studio.context.* and ui.chat.context_compacted*.
	ContextCompaction ChatContextCompactionSchema `json:"context_compaction"`
	// AgentNotConfigured is runtime state, never read from the Schema file:
	// the application sets it at startup when the live Agent has no API key
	// or no model, and the public projection reports the Agent unavailable.
	AgentNotConfigured bool `json:"-"`
}

// ChatContextCompactionSchema is application.chat.context_compaction (#322).
// A round is one user message with the assistant replies and tool calls that
// follow it up to the next user message. Compaction summarizes every round
// before the newest KeepRecentRounds into one session summary, with the same
// model through the same gateway; later Agent requests send the system prompt,
// the summary and the rounds after it. Chat history is never changed.
type ChatContextCompactionSchema struct {
	Enabled bool `json:"enabled"`
	// AutoThresholdRatio starts an automatic compaction before the next turn
	// once the last reported usage reaches this share of the context window
	// (0 < ratio <= 1). WarningRatio colors the composer meter from this share
	// on (0 < warning <= threshold).
	AutoThresholdRatio float64 `json:"auto_threshold_ratio"`
	WarningRatio       float64 `json:"warning_ratio"`
	// KeepRecentRounds is how many of the newest finished rounds stay verbatim.
	KeepRecentRounds int `json:"keep_recent_rounds"`
	// AutoMessageThreshold bounds the unsummarized history when tokens are
	// not measured (#322, note 98983): while the turn's model declares no
	// window or the upstream reported no usage, a new turn compacts first
	// once the messages after the summary exceed it. With compaction on no
	// message ever leaves a request without being summarized first.
	AutoMessageThreshold int `json:"auto_message_threshold"`
	// RequestTimeoutSeconds bounds the summary call.
	RequestTimeoutSeconds int `json:"request_timeout_seconds"`
	// MaximumSummaryCharacters bounds the stored summary; a longer answer is cut.
	MaximumSummaryCharacters int `json:"maximum_summary_characters"`
	// ListLimit bounds how many compaction records one session read returns.
	ListLimit int `json:"list_limit"`
	// SummaryPrompt is the system message of the summary call. SourceTemplate
	// is its user message: {previous_summary} (PreviousSummaryTemplate around
	// the earlier summary, or nothing) and {transcript} (one
	// TranscriptLineTemplate per message: {role}, {content}).
	SummaryPrompt           string            `json:"summary_prompt"`
	SourceTemplate          string            `json:"source_template"`
	PreviousSummaryTemplate string            `json:"previous_summary_template"`
	TranscriptLineTemplate  string            `json:"transcript_line_template"`
	RoleLabels              map[string]string `json:"role_labels"`
	// ContextTemplate puts the summary into the system prompt between
	// OpenMarker and CloseMarker ({open}, {summary}, {close}); markers inside
	// the summary are removed first, like the session notes.
	ContextTemplate string `json:"context_template"`
	OpenMarker      string `json:"open_marker"`
	CloseMarker     string `json:"close_marker"`
	// CompactedHandleTemplate tells the model, on the newest user message,
	// what a handle it mentions names when the message that carries the handle
	// is in the summarized part ({labels}: the attachment labels).
	CompactedHandleTemplate string `json:"compacted_handle_template"`
	// MockSummaryTemplate is the mock Agent's summary ({rounds}, {handles}).
	MockSummaryTemplate string `json:"mock_summary_template"`
}

// ChatChoiceSchema is application.chat.choice.
type ChatChoiceSchema struct {
	// AnswerTemplate follows a user message that answered a card on the card
	// itself; PendingTemplate is added to the system context while a card is
	// open, so a typed answer maps to an exact option. {continuation} is the
	// card's JSON: its tool and, per option and for the default, the exact
	// arguments of the next call.
	AnswerTemplate  string `json:"answer_template"`
	PendingTemplate string `json:"pending_template"`
	// MockSummary is the mock Agent's closing line for a turn that opened a card.
	MockSummary string `json:"mock_summary"`
	// MaxOptions bounds the options of one card; ListLimit the cards one
	// session read returns (newest first).
	MaxOptions int `json:"max_options"`
	ListLimit  int `json:"list_limit"`
	// MentionNegations are the words that, right before a service name in the
	// person's message, mean they do not want it ("不要 Tripo"). A negated
	// name is never run without asking (#321 G).
	MentionNegations []string `json:"mention_negations,omitempty"`
}

// ChatAgentScopeSchema is application.chat.agent_scope. The scope of a session
// harnesses the Agent: each tool declares one class in its execution policy
// (execution.agent_scope: "2d", "3d", "cross" or "utility") and a mode exposes
// the classes it allows. The server filters the tool list of every turn by it
// and refuses an out-of-scope call; the Agent is told its scope in the system
// context. Only the mode ids and workspace domains reach the browser.
type ChatAgentScopeSchema struct {
	// DefaultMode is the scope of a new session and the fallback for a stored
	// value the Schema no longer offers.
	DefaultMode string               `json:"default_mode"`
	Modes       []ChatAgentScopeMode `json:"modes"`
	// ContextTemplate is appended to the Agent's system context on every turn.
	// Fields: {mode} (label), {description}, {allowed_tools}, {blocked_tools}.
	ContextTemplate string `json:"context_template"`
	// EmptyListText stands in for an empty tool list in ContextTemplate.
	EmptyListText string `json:"empty_list_text"`
	// RejectionTemplate is the tool result of a call the scope does not allow.
	// Fields: {tool}, {mode} (label).
	RejectionTemplate string `json:"rejection_template"`
	// MockRefusal is what the mock Agent answers when the user asks for
	// something the scope withholds. Field: {tool}.
	MockRefusal string `json:"mock_refusal"`
}

// ChatAgentScopeMode is one scope a session can use.
type ChatAgentScopeMode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Description tells the Agent what this scope means for the session.
	Description string `json:"description"`
	// Allows lists the tool classes the scope exposes. "utility" tools are
	// available in every scope.
	Allows []string `json:"allows"`
	// WorkspaceDomains lists the workspace domains ("image", "model-3d") whose
	// model picker the scope leaves usable; the browser greys the others.
	WorkspaceDomains []string `json:"workspace_domains"`
}

// ChatThreeViewSchema is application.chat.three_view. It is server-only: the
// browser reads the group's label and notes from the group projection.
type ChatThreeViewSchema struct {
	// RecipeID is the skill recipe whose runs form three-view groups. Every
	// step of the recipe is one view (slot) of the group.
	RecipeID string `json:"recipe_id"`
	// ModelSourceStepKey is the view used as the picture for a 3D model made
	// from the group; the other views take no part in it.
	ModelSourceStepKey string `json:"model_source_step_key"`
	// Label is the group's title on its card and in the version strip.
	Label string `json:"label"`
	// MaximumListed bounds one session's group projection.
	MaximumListed int `json:"maximum_listed"`
	// ModelSourceNote tells the user and the Agent that only the model-source
	// view is used for 3D.
	ModelSourceNote string                `json:"model_source_note"`
	Messages        ChatThreeViewMessages `json:"messages"`
}

// ChatThreeViewMessages are the texts of the three-view tools. Fields of
// AgentStarted: {run}, {view}, {approval}, {credits}, {task}.
type ChatThreeViewMessages struct {
	AgentStarted      string `json:"agent_started"`
	ApprovalHuman     string `json:"approval_human"`
	ApprovalPolicy    string `json:"approval_policy"`
	ModelSourceNotYet string `json:"model_source_not_yet"`
	ReferenceRequired string `json:"reference_required"`
}

// ChatPendingApprovalsSchema is application.chat.pending_approvals. It is
// server-only: the browser learns each approval's expiry from the projection.
type ChatPendingApprovalsSchema struct {
	// TTLSeconds is how long a pending approval stays approvable, counted from
	// the moment it started waiting.
	TTLSeconds int `json:"ttl_seconds"`
	// MaximumListed bounds one session's pending-approval projection.
	MaximumListed int `json:"maximum_listed"`
	// MonitorIntervalMilliseconds and MonitorBatchSize drive the background
	// sweep that posts results of approvals decided after their turn ended.
	MonitorIntervalMilliseconds int `json:"monitor_interval_milliseconds"`
	MonitorBatchSize            int `json:"monitor_batch_size"`
	// Messages are the assistant messages posted into the session. Fields:
	// {model}.
	Messages ChatPendingApprovalMessages `json:"messages"`
}

// ChatToolReferenceCapacityNote words what the turn's picked model does with
// reference pictures: Single for a model that takes one ({model}), Multiple for
// one that takes up to {count} pictures ({model}, {count}). Settings lists the
// settings the selected Offering declares for the person ({model},
// {settings}); the runtime fills {settings} from the Offering's parameters.
type ChatToolReferenceCapacityNote struct {
	Single   string `json:"single"`
	Multiple string `json:"multiple"`
	NoPrompt string `json:"no_prompt,omitempty"`
	Settings string `json:"settings,omitempty"`
}

// ChatPendingApprovalMessages are the texts of a late decision's messages.
type ChatPendingApprovalMessages struct {
	Started   string `json:"started"`
	Succeeded string `json:"succeeded"`
	// SucceededWithNote replaces Succeeded when the step used only some of the
	// reference pictures it was given (#329 E). Fields: {note} (the
	// reference-limit note, runtime.generation_messages, which already names
	// the model, so this text does not: #329 note 99647). Empty keeps
	// Succeeded.
	SucceededWithNote string `json:"succeeded_with_note,omitempty"`
	Denied            string `json:"denied"`
	SubmitFailed      string `json:"submit_failed"`
	// Expired is posted once when an approval waited past its expiry without a
	// decision; Cancelled when the turn was stopped before the approval card
	// could be handed over to the person.
	Expired   string `json:"expired"`
	Cancelled string `json:"cancelled"`
	// DeniedExtraCredits replaces Denied when the refused step also asked to
	// spend extra credits (extra export formats, billed options). It says the
	// step was not done and proposes an alternative. Fields: {model}, {label},
	// {items}, {extra}, {base}, {total}. ExtraItemsSeparator joins {items}.
	DeniedExtraCredits  string `json:"denied_extra_credits"`
	DeniedNativeExtra   string `json:"denied_native_extra,omitempty"`
	ExtraItemsSeparator string `json:"extra_items_separator"`
}

// ChatApprovalPolicySchema is application.chat.approval_policy. Modes are
// "auto" and "manual". Capabilities listed here need a human decision in
// manual mode; every other capability is approved by policy.
type ChatApprovalPolicySchema struct {
	DefaultMode                string                      `json:"default_mode"`
	Modes                      []string                    `json:"modes"`
	ManualApprovalCapabilities []schemacontract.Capability `json:"manual_approval_capabilities"`
}

type ChatAPISchema struct {
	Fields         ChatAPIFields              `json:"fields"`
	ResponseFields ChatAPIResponseFields      `json:"response_fields"`
	Attachment     ChatAttachmentAPIFields    `json:"attachment"`
	PromptEnhance  ChatPromptEnhanceAPISchema `json:"prompt_enhance"`
	// SessionList bounds the paged session list (server side only).
	SessionList ChatSessionListSchema `json:"session_list"`
	// MessageList bounds a paged read of one conversation (server side only).
	MessageList ChatMessageListSchema `json:"message_list"`
}

// ChatMessageListSchema is the paging contract of a conversation's messages:
// the page size used when a request asks for paging without a limit, and the
// largest page a request may ask for. A request without paging parameters
// still reads the whole conversation.
type ChatMessageListSchema struct {
	DefaultPageSize int `json:"default_page_size"`
	MaximumPageSize int `json:"maximum_page_size"`
}

// ChatSessionListSchema is the paging contract of the session list: the page
// size used when a request asks for paging without a limit, and the largest
// page a request may ask for.
type ChatSessionListSchema struct {
	DefaultPageSize int `json:"default_page_size"`
	MaximumPageSize int `json:"maximum_page_size"`
}

// ChatAttachmentAPIFields names the members of one attachment object, in
// message responses and in the attachment and selection request fields.
type ChatAttachmentAPIFields struct {
	Handle    string `json:"handle"`
	Kind      string `json:"kind"`
	Role      string `json:"role"`
	AssetID   string `json:"asset_id"`
	TaskID    string `json:"task_id"`
	CaptureID string `json:"capture_id"`
	ImageSlot string `json:"image_slot"`
	URL       string `json:"url"`
	// Token is the member of a mention that holds the text the person wrote
	// (a request-only member of the `mentions` field).
	Token string `json:"token"`
	// CanvasLayers is the request-only member of the artboard snapshot claim
	// that counts the visible layers it flattens (#272).
	CanvasLayers string `json:"canvas_layers"`
	// CanvasSources is the request-only member of the artboard snapshot claim
	// that lists the picture URLs of the layers it flattens (#189). Optional:
	// without it a snapshot names no source Tasks.
	CanvasSources string `json:"canvas_sources"`
}

type ChatPromptEnhanceAPISchema struct {
	PromptField               string                                    `json:"prompt_field"`
	CapabilityField           string                                    `json:"capability_field"`
	ModelField                string                                    `json:"model_field"`
	ResultField               string                                    `json:"result_field"`
	PromptResultField         string                                    `json:"prompt_result_field"`
	NegativePromptResultField string                                    `json:"negative_prompt_result_field"`
	ErrorField                string                                    `json:"error_field"`
	DefaultProfile            string                                    `json:"default_profile"`
	CapabilityProfiles        map[schemacontract.Capability]string      `json:"capability_profiles"`
	Profiles                  map[string]ChatPromptEnhanceProfileSchema `json:"profiles"`
	Temperature               float64                                   `json:"temperature"`
}

type ChatPromptEnhanceProfileSchema struct {
	SystemPrompt          string `json:"system_prompt"`
	LengthRulePlaceholder string `json:"length_rule_placeholder"`
	BoundedLengthRule     string `json:"bounded_length_rule"`
	UnboundedLengthRule   string `json:"unbounded_length_rule"`
	MaxLengthPlaceholder  string `json:"max_length_placeholder"`
	PromptField           string `json:"prompt_field"`
	NegativePromptField   string `json:"negative_prompt_field"`
	DefaultNegativePrompt string `json:"default_negative_prompt"`
}

type ChatAPIFields struct {
	Title                 string `json:"title"`
	CreativeDomain        string `json:"creative_domain"`
	Content               string `json:"content"`
	ImageURLs             string `json:"image_urls"`
	AspectRatio           string `json:"aspect_ratio"`
	PoseMode              string `json:"pose_mode"`
	AIModel               string `json:"ai_model"`
	Size                  string `json:"size"`
	ImageSize             string `json:"image_size"`
	Parameters            string `json:"parameters"`
	Skill                 string `json:"skill"`
	SkillID               string `json:"skill_id"`
	SkillName             string `json:"skill_name"`
	ReferenceImageIndices string `json:"reference_image_indices"`
	// DomainModels carries the model chosen per workspace domain, so a tool of
	// the other domain than the one in view still uses the user's choice.
	DomainModels string `json:"domain_models"`
	// Attachments carries structured claims about the images of the message
	// (capture lineage); Selection carries the objects selected on the
	// workspace. Both are verified server-side before use.
	Attachments string `json:"attachments"`
	Selection   string `json:"selection"`
	// Mentions carries the `@` references written in the message text; the
	// server verifies them, gives their objects handles and rewrites the text.
	Mentions string `json:"mentions"`
	// InputSource names the source the person chose for the turn (#266). The
	// only declared value is context.canvas_input_source: the selection is the
	// artboard snapshot taken at send time and the system binds it.
	InputSource  string `json:"input_source"`
	FromID       string `json:"from_id"`
	TurnCanceled string `json:"turn_canceled"`
	// ApprovalMode is the request field of the session approval-mode write.
	ApprovalMode string `json:"approval_mode"`
	// Notes is the request field of the session settings-summary write.
	Notes string `json:"notes"`
	// AgentScope is the request field of the session Agent-scope write.
	AgentScope string `json:"agent_scope"`
	// ChoiceAnswer carries the answer to an in-chat choice card with a turn
	// (#321): {"choice_id", "option_id"} or {"choice_id", "use_default": true}.
	ChoiceAnswer string `json:"choice_answer"`
	// ReasoningEffort is the request field of the session thinking-level
	// write (#322): one of the levels the session's model offers.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// ChatModel is the request field of the session chat-model write (#322
	// Scope 5): the ID of an available model, or "" to follow the default.
	ChatModel string `json:"chat_model,omitempty"`
}

type ChatAPIResponseFields struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Count     string `json:"count"`
	Messages  string `json:"messages"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	ImageURLs string `json:"image_urls"`
	// Attachments is the message member listing its attachment objects.
	Attachments   string `json:"attachments"`
	ToolCalls     string `json:"tool_calls"`
	ToolName      string `json:"tool_name"`
	ToolArguments string `json:"tool_arguments"`
	MessageID     string `json:"message_id"`
	SessionID     string `json:"session_id"`
	Error         string `json:"error"`
	IsPending     string `json:"is_pending"`
	PendingTypes  string `json:"pending_task_types"`
	Images        string `json:"images"`
	URL           string `json:"url"`
	TaskID        string `json:"task_id"`
	// ApprovalMode and EstimatedCredits carry the session approval mode and
	// the estimated credits of accepted tasks (session DTO) or of one step
	// (tool call DTO).
	ApprovalMode     string `json:"approval_mode"`
	EstimatedCredits string `json:"estimated_credits"`
	// Notes is the member carrying the session settings summary.
	Notes string `json:"notes"`
	// Pick is the member carrying the session's picked candidate.
	Pick string `json:"pick"`
	// AgentScope is the member carrying the session's Agent scope.
	AgentScope string `json:"agent_scope"`
	// ApprovalSource is the tool-call field saying who approved the step.
	ApprovalSource string `json:"approval_source"`
	// GenerationModels is the session member carrying the model last sent in
	// each workspace domain (#318), so another browser restores the picker.
	// Empty (historical Catalogs) leaves the member out.
	GenerationModels string `json:"generation_models,omitempty"`
	// GenerationModelsRetired is the session member naming, per domain, the
	// retired model the session last sent with when GenerationModels carries
	// its successor instead (#329 follow-up), so the workspace says it switched.
	GenerationModelsRetired string `json:"generation_models_retired,omitempty"`
	// Thinking level and context members of the session reads (#322):
	// ReasoningEffort is the level the next turn sends ("" when the model
	// offers none), ReasoningLevels the model's own level values and
	// ReasoningDefault its default. ContextTokens is the context the last
	// model request of the session used (null while unknown), ContextWindow
	// the model's window (null when not declared). Compactions lists the
	// session's compaction records, each with CreatedAt, CompactionTrigger
	// ("manual" or "auto"), CompactedRounds and ThroughMessageID (the last
	// message the summary covers).
	ReasoningEffort   string `json:"reasoning_effort,omitempty"`
	ReasoningLevels   string `json:"reasoning_levels,omitempty"`
	ReasoningDefault  string `json:"reasoning_default,omitempty"`
	ContextTokens     string `json:"context_tokens,omitempty"`
	ContextWindow     string `json:"context_window,omitempty"`
	Compactions       string `json:"compactions,omitempty"`
	CompactionTrigger string `json:"compaction_trigger,omitempty"`
	CompactedRounds   string `json:"compacted_rounds,omitempty"`
	ThroughMessageID  string `json:"through_message_id,omitempty"`
	// Chat model members of the session model read (#322 Scope 5): ChatModel
	// is the model ID the next turn uses, ChatModelChosen the session's own
	// choice ("" when it follows the default), ChatModelDefault the
	// administrator's default, ChatModelFallback whether the choice is no
	// longer available so the default is used, ChatModels the available
	// models (each with ID and ChatModelName, the upstream model name).
	ChatModel         string `json:"chat_model,omitempty"`
	ChatModelChosen   string `json:"chat_model_chosen,omitempty"`
	ChatModelDefault  string `json:"chat_model_default,omitempty"`
	ChatModelFallback string `json:"chat_model_fallback,omitempty"`
	ChatModels        string `json:"chat_models,omitempty"`
	ChatModelName     string `json:"chat_model_name,omitempty"`
}

type ChatDefaultsSchema struct {
	Title string `json:"title"`
}

type ChatRolesSchema struct {
	System    string `json:"system"`
	User      string `json:"user"`
	Assistant string `json:"assistant"`
	Tool      string `json:"tool"`
}

type ChatStateSchema struct {
	PendingContent               string `json:"pending_content"`
	Active                       string `json:"active"`
	Done                         string `json:"done"`
	Error                        string `json:"error"`
	TurnQueued                   string `json:"turn_queued"`
	TurnRunning                  string `json:"turn_running"`
	TurnWaitingTask              string `json:"turn_waiting_task"`
	TurnDone                     string `json:"turn_done"`
	TurnError                    string `json:"turn_error"`
	PlaceholderStaleMilliseconds int    `json:"placeholder_stale_milliseconds"`
	FailedPromptPreviewChars     int    `json:"failed_prompt_preview_characters"`
}

type ChatFrontendSchema struct {
	ConnectionTimeoutMilliseconds   int    `json:"connection_timeout_milliseconds"`
	FallbackPollMilliseconds        int    `json:"fallback_poll_milliseconds"`
	ReconnectCheckDelayMilliseconds int    `json:"reconnect_check_delay_milliseconds"`
	OptionStorageKey                string `json:"option_storage_key"`
	AddImagesEventName              string `json:"add_images_event_name"`
	ImageMIMEPrefix                 string `json:"image_mime_prefix"`
	ImageFileAccept                 string `json:"image_file_accept"`
	SendKey                         string `json:"send_key"`
	SendModifier                    string `json:"send_modifier"`
	LightboxCloseKey                string `json:"lightbox_close_key"`
	LightboxPreviousKey             string `json:"lightbox_previous_key"`
	LightboxNextKey                 string `json:"lightbox_next_key"`
	TextareaMaximumHeightPixels     int    `json:"textarea_maximum_height_pixels"`
	OptimisticMessageIDPrefix       string `json:"optimistic_message_id_prefix"`
	OptimisticUserMessageID         string `json:"optimistic_user_message_id"`
	OptimisticAssistantMessageID    string `json:"optimistic_assistant_message_id"`
	FilenameQueryParameter          string `json:"filename_query_parameter"`
	DownloadFilenameMaxCharacters   int    `json:"download_filename_max_characters"`
	DownloadTaskIDCharacters        int    `json:"download_task_id_characters"`
	DownloadExtension               string `json:"download_extension"`
	FallbackDownloadIdentifier      string `json:"fallback_download_identifier"`
	InternalAPIPathPrefix           string `json:"internal_api_path_prefix"`
	LegacyHiddenContentPrefix       string `json:"legacy_hidden_content_prefix"`
	LegacyHiddenContentSuffix       string `json:"legacy_hidden_content_suffix"`
	ProgressMaximum                 int    `json:"progress_maximum"`
}

type ChatThinkingSchema struct {
	// Enabled switches every vendor profile on or off. A model that matches
	// no profile is sent only standard OpenAI chat-completions fields (#253).
	Enabled bool `json:"enabled"`
	// ModelProfiles are the vendor extensions of models whose vendor declares
	// its own contract. The configured model name is matched
	// case-insensitively by prefix; the first matching profile wins.
	ModelProfiles []ChatThinkingModelProfile `json:"model_profiles,omitempty"`
}

type ChatThinkingModelProfile struct {
	ModelPrefixes []string `json:"model_prefixes"`
	// RequestFields are top-level request body fields sent verbatim, e.g. a
	// vendor's thinking switch. The thinking level is not one of them: it is
	// EffortField (#322).
	RequestFields map[string]json.RawMessage `json:"request_fields,omitempty"`
	// EffortField is the top-level request field the session's thinking level
	// is written to (for example "reasoning_effort"). EffortLevels are the
	// vendor's own level values for this model family, in display order, and
	// DefaultEffort is sent when the session chose none or chose a level this
	// profile does not offer (another model). The three are declared together
	// or not at all; without them the model offers no level choice (#322).
	EffortField   string   `json:"effort_field,omitempty"`
	EffortLevels  []string `json:"effort_levels,omitempty"`
	DefaultEffort string   `json:"default_effort,omitempty"`
	// ContextWindowTokens is the model family's context window in tokens, as
	// the vendor documents it; 0 means not declared, and the composer then
	// shows only the tokens used (#322).
	ContextWindowTokens int `json:"context_window_tokens,omitempty"`
	// StreamIncludeUsage overrides chat.agent.stream_include_usage for this
	// model family: whether a streamed request asks for token usage with
	// stream_options.include_usage. Nil keeps the agent-level value (#322).
	StreamIncludeUsage *bool `json:"stream_include_usage,omitempty"`
	// ReasoningPassback returns each assistant message's reasoning_content in
	// later requests of the same tool loop, as the vendor contract requires.
	ReasoningPassback bool `json:"reasoning_passback"`
	// DSMLToolCalls lets the orchestrator read DeepSeek-style DSML tool calls
	// written into the reply text (#253); chat.dsml stays off otherwise.
	DSMLToolCalls bool `json:"dsml_tool_calls,omitempty"`
}

type ChatAgentSchema struct {
	AdapterKey        string              `json:"adapter_key"`
	ConnectionID      string              `json:"connection_id,omitempty"`
	BaseURLSetting    string              `json:"base_url_setting"`
	APIKeySetting     string              `json:"api_key_setting"`
	ModelSetting      string              `json:"model_setting"`
	EndpointPath      string              `json:"endpoint_path"`
	AuthorizationName string              `json:"authorization_header"`
	AuthorizationType string              `json:"authorization_type"`
	Temperature       float64             `json:"temperature"`
	Thinking          *ChatThinkingSchema `json:"thinking,omitempty"`
	Stream            bool                `json:"stream"`
	// StreamIncludeUsage asks a streamed request for token usage with
	// stream_options.include_usage, unless the model's thinking profile says
	// otherwise (#322). The usage feeds the composer's context meter.
	StreamIncludeUsage bool `json:"stream_include_usage,omitempty"`
	// ContextWindowSetting names the runtime setting that may override the
	// context window of the configured model per deployment (#322); its empty
	// value keeps the profile's context_window_tokens.
	ContextWindowSetting  string `json:"context_window_setting,omitempty"`
	Vision                bool   `json:"vision"`
	RequestTimeoutSeconds int    `json:"request_timeout_seconds"`
	// ConsentWaitTimeoutSeconds 界定 request_consent 工具挂起等待用户
	// 批准决定的最长时长（AGT-12：挂起不占 worker；由 agenttools 与
	// orchestrator 消费，不得在代码中硬编码）。
	ConsentWaitTimeoutSeconds int `json:"consent_wait_timeout_seconds"`
	// MaxGenerationCallsPerTurn 限定单个对话轮次内付费生成工具的执行
	// 次数上限（服务端硬约束，防止 LLM 一次输出大量工具调用连击扣费）。
	MaxGenerationCallsPerTurn int `json:"max_generation_calls_per_turn"`
	// MaxListedModels bounds how many of the session's succeeded 3D tasks
	// the list_session_models tool returns (most recent first).
	MaxListedModels int `json:"max_listed_models"`
	// Consent*Content 是 request_consent 与付费硬门禁回给对话 LLM 的
	// 工具内容文案（B16：原代码常量迁入 Schema，禁止硬编码）。
	ConsentApprovedContent string `json:"consent_approved_content"`
	ConsentDeniedContent   string `json:"consent_denied_content"`
	ConsentWaitingContent  string `json:"consent_waiting_content"`
	// ConsentWaitingStatus is the state of a step that waits on its approval
	// card, in words the Agent uses as they are (#327: a reply called such a
	// step "resubmitted"). Tool results carry it as status_text; optional.
	ConsentWaitingStatus string `json:"consent_waiting_status,omitempty"`
	// ConsentDuplicateContent answers a tool call that asks for what an
	// approval card that is still pending already asks for.
	ConsentDuplicateContent string `json:"consent_duplicate_content"`
	// ConsentRechargeContent answers a generation call that the recharge guard
	// sent to an approval card (#294): an earlier Task of the same kind in the
	// session ({task_id}) failed after the upstream finished and charged, and
	// its result was not stored. Optional; consent_waiting_content stands in.
	ConsentRechargeContent string `json:"consent_recharge_content,omitempty"`
	// RefetchStartedContent answers recover_task_result when a refetch of the
	// failed Task's accepted upstream result started or already exists (#295);
	// {task_id} is the refetch Task. RefetchUnavailableContent maps each
	// refetch reason (not_applicable, not_supported, expired,
	// provider_unavailable) to the text the Agent relays. Both are required
	// when the recover_task_result tool is declared.
	RefetchStartedContent     string            `json:"refetch_started_content,omitempty"`
	RefetchUnavailableContent map[string]string `json:"refetch_unavailable_content,omitempty"`
	ConsentBlockedContent     string            `json:"consent_blocked_content"`
	ConsentRequiredContent    string            `json:"consent_required_content"`
	ConsentLimitContent       string            `json:"consent_limit_content"`
	ConsentReconcileContent   string            `json:"consent_reconcile_content"`
	// MaxToolCallsPerReply 限定一次 Agent 回复内实际执行的工具调用数
	// （>=1，#275，替代按 LLM 轮数计的 max_tool_rounds）。每次新回复从 0
	// 起算；恢复与续跑时已执行的调用计入同一次回复。一批调用超出剩余
	// 预算时只执行到上限，其余与总结轮仍写出的调用一起列给用户确认。
	// 每轮至少执行一个调用，所以它同时是工具循环的防死循环上限。
	// request_consent 批准后模型可在同一次回复内继续调用生成工具。
	MaxToolCallsPerReply int `json:"max_tool_calls_per_reply"`
	// MaxToolCallsPerReplySetting names the runtime setting (environment
	// variable) that may override MaxToolCallsPerReply per deployment; an
	// empty value keeps the Schema value. MaxToolCallsPerReplyMaximum bounds
	// both the Schema value and the override.
	MaxToolCallsPerReplySetting string            `json:"max_tool_calls_per_reply_setting"`
	MaxToolCallsPerReplyMaximum int               `json:"max_tool_calls_per_reply_maximum"`
	ScannerMaxBytes             int               `json:"scanner_max_bytes"`
	ErrorBodyMaxBytes           int64             `json:"error_body_max_bytes"`
	SystemPrompt                string            `json:"system_prompt"`
	DomainContext               map[string]string `json:"domain_context"`
	// PromptSections declares Agent-facing text that only applies while one of
	// its capabilities has an enabled Offering (#115). See
	// ChatAgentPromptSection.
	PromptSections []ChatAgentPromptSection `json:"prompt_sections,omitempty"`
	TitlePrompt    string                   `json:"title_prompt"`
	TitleMaxChars  int                      `json:"title_max_characters"`
	// TitleMaxCharsByLocale overrides title_max_characters for a UI locale
	// whose words take more characters than Chinese (an English title).
	TitleMaxCharsByLocale map[string]int `json:"title_max_characters_by_locale,omitempty"`
	// ReplyLanguageTemplate tells the Agent which language to answer in
	// ({language} is the locale's own name). It follows the system prompt
	// only in a turn whose UI locale is not the default one.
	ReplyLanguageTemplate string `json:"reply_language_template,omitempty"`
	// TitleTimeoutSeconds bounds the background title summary call.
	TitleTimeoutSeconds int    `json:"title_timeout_seconds"`
	WorkerCount         int    `json:"worker_count"`
	QueueSize           int    `json:"queue_size"`
	MockToolName        string `json:"mock_tool_name"`
	MockPromptArgument  string `json:"mock_prompt_argument"`
	MockSummary         string `json:"mock_summary"`
	// MockFailureSummary and MockWaitingSummary close a mock turn whose tool
	// failed ({error} is the tool's error text) or whose step waits for approval.
	MockFailureSummary string `json:"mock_failure_summary,omitempty"`
	MockWaitingSummary string `json:"mock_waiting_summary,omitempty"`
	// MockDeniedSummary closes a mock turn whose step the person refused on
	// the approval card while the turn was still attached: it says the step
	// was not done and proposes an alternative.
	MockDeniedSummary string `json:"mock_denied_summary,omitempty"`
}

type ChatContextSchema struct {
	MaximumVisionImages    int    `json:"maximum_vision_images"`
	RecentImageLimit       int    `json:"recent_image_limit"`
	AssetURLTemplate       string `json:"asset_url_template"`
	ToolPromptArgument     string `json:"tool_prompt_argument"`
	ToolReferenceArgument  string `json:"tool_reference_argument"`
	PreviousPromptTemplate string `json:"previous_prompt_template"`
	ReferenceFilename      string `json:"reference_filename"`
	MaximumMessages        int    `json:"maximum_messages"`
	ImageIndexBase         int    `json:"image_index_base"`
	ToolPromptPreviewChars int    `json:"tool_prompt_preview_characters"`
	TextPartKind           string `json:"text_part_kind"`
	ImagePartKind          string `json:"image_part_kind"`
	ImageDetail            string `json:"image_detail"`
	UserImageLabelTemplate string `json:"user_image_label_template"`
	// CanvasInputSource is the input_source value of a turn whose source is
	// the artboard snapshot sent as its selection (#266).
	CanvasInputSource    string `json:"canvas_input_source"`
	AIImageLabelTemplate string `json:"ai_image_label_template"`
	// CurrentUploadHint follows the latest user message when it carries
	// pictures: {count} is how many, {tool} the first declared tool and
	// {handles} the rendered CurrentUploadHandlesTemplate. It states facts
	// (which pictures are this turn's and that an edit uses them by default)
	// and must not push the Agent to generate: only looking at a picture
	// needs no tool (#293).
	CurrentUploadHint string `json:"current_upload_hint_template"`
	// CurrentUploadHandlesTemplate names the handles of those pictures
	// ({handles}, space-separated). It is left out when none of them has a
	// handle (an older message without structured attachments). Optional.
	CurrentUploadHandlesTemplate string `json:"current_upload_handles_template,omitempty"`
	// ToolCallNote 把历史 assistant.tool_calls 折叠为文本摘要的模板
	// （{tools}=工具名列表），避免悬挂 tool_calls 被严格上游整体 400。
	ToolCallNote         string `json:"tool_call_note_template"`
	VisionInlineMaxBytes int64  `json:"vision_inline_max_bytes"`
	// VisionDerivative is how a picture over VisionInlineMaxBytes still
	// reaches the model's vision context (#292): a downscaled re-encoding
	// made for the LLM only, never for a Provider.
	VisionDerivative ChatVisionDerivativeSchema `json:"vision_derivative"`

	// HandlePrefixes maps an attachment kind ("image", "model") to the prefix
	// of its handles ("@img3", "@model2"). SelectionAlias names the whole
	// current selection. ReferenceHandlesArgument is the tool argument that
	// carries handles; the index argument keeps working beside it.
	HandlePrefixes           map[string]string `json:"handle_prefixes"`
	SelectionAlias           string            `json:"selection_alias"`
	ReferenceHandlesArgument string            `json:"reference_handles_argument"`
	// ModelVersionTemplate is how tool results name a model's version
	// ({version}), the same label as the version strip and the approval
	// card ("3D v5", #326 L). Empty leaves versions out of tool results.
	ModelVersionTemplate string `json:"model_version_template,omitempty"`
	// AttachmentLabelTemplates are keyed "<kind>.<role>" and replace the
	// legacy per-image labels for attachments that have a handle. Fields:
	// {index} {handle} {task_id} {capture_id}.
	AttachmentLabelTemplates map[string]string `json:"attachment_label_templates"`
	// UnpicturedAttachmentTemplates are keyed by kind and label an attachment
	// that has no picture among the message's images (a model without a
	// thumbnail, an object that was only selected). Fields: {handle}
	// {task_id} {capture_id}. A "<kind>.<role>" key (image.canvas) is used
	// before the kind's own; it may also use {layout}.
	UnpicturedAttachmentTemplates map[string]string `json:"unpictured_attachment_templates"`
	// KindLabels names attachment kinds in the selection block.
	KindLabels map[string]string `json:"kind_labels"`
	// SelectionTemplate wraps the current-selection block appended to the
	// system prompt ({alias} {items}); SelectionItemTemplate renders one
	// object ({handle} {kind} {details}); the Task and capture templates fill
	// {details}.
	SelectionTemplate        string `json:"selection_template"`
	SelectionItemTemplate    string `json:"selection_item_template"`
	SelectionTaskTemplate    string `json:"selection_task_template"`
	SelectionCaptureTemplate string `json:"selection_capture_template"`
	// SelectionPickedTemplate is appended to the selection block when the
	// selection is the candidate the user picked ("选用").
	SelectionPickedTemplate string `json:"selection_picked_template"`
	// The artboard snapshot of a canvas turn (#272). CanvasLayoutTemplate
	// names the layout ({width} {height} {layers}); CanvasLayersTemplate
	// fills {layers} when the layer count is known ({layers}), else {layers}
	// is empty. SelectionCanvasTemplate is the snapshot's selection detail
	// ({layout}); SelectionCanvasNote follows the selection block and tells the
	// Agent to compose from the layout. MaximumCanvasLayers bounds the layer
	// count a client may claim. The output-size templates are the tool-result
	// note on a canvas turn: the size followed the canvas, or the model could
	// not produce it and the nearest legal size was used ({canvas} {size}).
	CanvasLayoutTemplate            string `json:"canvas_layout_template"`
	CanvasLayersTemplate            string `json:"canvas_layers_template"`
	SelectionCanvasTemplate         string `json:"selection_canvas_template"`
	SelectionCanvasNote             string `json:"selection_canvas_note"`
	MaximumCanvasLayers             int    `json:"maximum_canvas_layers"`
	CanvasOutputSizeTemplate        string `json:"canvas_output_size_template"`
	CanvasOutputSizeNearestTemplate string `json:"canvas_output_size_nearest_template"`
	// The tool-result note when the one reference picture set the output size
	// (#296, tools with reference_size "single"): the size kept the picture's
	// shape, or the model could not produce that shape and the nearest legal
	// size was used ({reference} {size}).
	ReferenceOutputSizeTemplate        string `json:"reference_output_size_template,omitempty"`
	ReferenceOutputSizeNearestTemplate string `json:"reference_output_size_nearest_template,omitempty"`
	// UnknownHandleTemplate and WrongHandleKindTemplate are the refusals the
	// Agent reads when a handle names nothing in this session or an object of
	// the wrong kind. Field: {handle}.
	UnknownHandleTemplate   string `json:"unknown_handle_template"`
	WrongHandleKindTemplate string `json:"wrong_handle_kind_template"`
	// MaximumAttachmentsPerMessage and MaximumSelection bound the structured
	// claims a client can send in one turn.
	MaximumAttachmentsPerMessage int `json:"maximum_attachments_per_message"`
	MaximumSelection             int `json:"maximum_selection"`

	// RecentGeneratedImageLimit is how many of the newest generated pictures
	// (for a 3D result its thumbnail) the Agent sees beside the user's own
	// pictures. They are shown to a vision model, not just named.
	RecentGeneratedImageLimit int `json:"recent_generated_image_limit"`
	// GeneratedVisionIntro precedes those pictures in the newest user message
	// (#327): they are earlier results of the session (a model's preview
	// among them), not pictures the person attached this turn. Empty adds
	// nothing.
	GeneratedVisionIntro string `json:"generated_vision_intro,omitempty"`
	// MaximumSessionNotesCharacters bounds the session's settings summary.
	// SessionNotesTemplate wraps it in the system prompt: {open} and {close}
	// are the two markers below around {notes}, and the template's own words
	// say the block is context written by the user or the Agent, not an
	// instruction. The markers are removed from the notes before framing, so
	// the text cannot close the frame early.
	// SessionNotesSavedMessage and SessionNotesTooLongTemplate ({maximum}
	// {length}) are what update_session_notes returns to the Agent.
	MaximumSessionNotesCharacters int    `json:"maximum_session_notes_characters"`
	SessionNotesTemplate          string `json:"session_notes_template"`
	SessionNotesOpenMarker        string `json:"session_notes_open_marker"`
	SessionNotesCloseMarker       string `json:"session_notes_close_marker"`
	SessionNotesSavedMessage      string `json:"session_notes_saved_message"`
	SessionNotesTooLongTemplate   string `json:"session_notes_too_long_template"`

	// History image lookup (#279). HistoryImageLookupLimit bounds how many
	// distinct pictures of the session the find_history_images tool may
	// surface (listed or shown) in one Agent reply; the server counts it.
	// HistoryImageBudgetTemplate ({limit}) is the tool's note once the bound
	// is reached. A shown picture travels in a message of its own that opens
	// with HistoryImageViewIntro; each picture is followed by
	// HistoryImageViewLabelTemplate ({handle}). A picture that cannot be read
	// is named with HistoryImageUnreadableTemplate ({handle}), and
	// HistoryImageVisionOffMessage says no picture is shown at all because the
	// Agent does not read images.
	HistoryImageLookupLimit        int    `json:"history_image_lookup_limit,omitempty"`
	HistoryImageBudgetTemplate     string `json:"history_image_budget_template,omitempty"`
	HistoryImageViewIntro          string `json:"history_image_view_intro,omitempty"`
	HistoryImageViewLabelTemplate  string `json:"history_image_view_label_template,omitempty"`
	HistoryImageUnreadableTemplate string `json:"history_image_unreadable_template,omitempty"`
	HistoryImageVisionOffMessage   string `json:"history_image_vision_off_message,omitempty"`
	// ReferencePurposeInvalidTemplate ({handle}) refuses a reference purpose
	// whose handle is not one of the pictures the call really sends.
	ReferencePurposeInvalidTemplate string `json:"reference_purpose_invalid_template,omitempty"`
}

type ChatDSMLSchema struct {
	Enabled bool `json:"enabled"`
	// Markers lists every accepted spelling of the marker that precedes an
	// element name, e.g. the official "｜DSML｜" and the doubled "｜｜DSML｜｜"
	// some relays emit (#275). Parse and the streaming filter accept each one.
	Markers []string `json:"markers"`
	// ToolCallsElements lists the accepted names of the outer element, e.g.
	// "function_calls" (official), "tool_calls" and "calls" (#275).
	ToolCallsElements []string `json:"tool_calls_elements"`
	// MarkerWhitespace accepts whitespace between a marker and the element
	// name, as in "<｜｜DSML｜｜ invoke" (#275).
	MarkerWhitespace bool   `json:"marker_whitespace"`
	InvokeElement    string `json:"invoke_element"`
	ParameterElement string `json:"parameter_element"`
	NameAttribute    string `json:"name_attribute"`
	StringAttribute  string `json:"string_attribute"`
	TrueValue        string `json:"true_value"`
	FalseValue       string `json:"false_value"`
	CallIDTemplate   string `json:"call_id_template"`
}

type ChatStreamSchema struct {
	IDFrameTemplate          string                `json:"id_frame_template"`
	WriteTimeoutMilliseconds int                   `json:"write_timeout_milliseconds"`
	RetryMilliseconds        int                   `json:"retry_milliseconds"`
	HeartbeatMilliseconds    int                   `json:"heartbeat_milliseconds"`
	CleanupTTLMilliseconds   int                   `json:"cleanup_ttl_milliseconds"`
	CompletionFlushMillis    int                   `json:"completion_flush_milliseconds"`
	StatusCheckMilliseconds  int                   `json:"status_check_milliseconds"`
	SubscriberBufferSize     int                   `json:"subscriber_buffer_size"`
	EventPrefix              string                `json:"event_prefix"`
	EventSuffix              string                `json:"event_suffix"`
	HeartbeatFrame           string                `json:"heartbeat_frame"`
	RetryFrameTemplate       string                `json:"retry_frame_template"`
	Headers                  map[string]string     `json:"headers"`
	EventFields              ChatStreamEventFields `json:"event_fields"`
	EventTypes               ChatStreamEventTypes  `json:"event_types"`
}

type ChatStreamEventFields struct {
	Type        string `json:"type"`
	Content     string `json:"content"`
	Percent     string `json:"percent"`
	TaskID      string `json:"task_id"`
	URLs        string `json:"urls"`
	MessageID   string `json:"message_id"`
	Capability  string `json:"capability"`
	Prompt      string `json:"prompt"`
	AIModel     string `json:"ai_model"`
	SessionID   string `json:"session_id"`
	FinalTaskID string `json:"final_task_id"`
	// EstimatedCredits is the estimated credits of the registered step;
	// ApprovalSource says whether policy or a human approved it.
	EstimatedCredits string `json:"estimated_credits"`
	ApprovalSource   string `json:"approval_source"`
	Status           string `json:"status"`
	Progress         string `json:"progress"`
	Result           string `json:"result"`
	ImageURLs        string `json:"image_urls"`
	Error            string `json:"error"`
}

type ChatStreamEventTypes struct {
	Text           string `json:"text"`
	Status         string `json:"status"`
	Progress       string `json:"progress"`
	Images         string `json:"images"`
	Title          string `json:"title"`
	MessageCreated string `json:"message_created"`
	TaskRegistered string `json:"task_registered"`
	TaskUpdate     string `json:"task_update"`
	Error          string `json:"error"`
	Done           string `json:"done"`
	// Thinking announces that the model is thinking before visible output
	// (#285). It carries only the Schema status text, never reasoning. Empty
	// turns the status off.
	Thinking string `json:"thinking,omitempty"`
}

type ChatToolSchema struct {
	// AcceptReferenceImages defaults to the historical reference-image behavior.
	// Explicit false declares a standalone tool: current/selected/history images
	// do not select its capability or rewrite its text prompt.
	AcceptReferenceImages *bool                   `json:"accept_reference_images,omitempty"`
	Name                  string                  `json:"name"`
	Execution             ChatToolExecutionSchema `json:"execution"`
	Description           string                  `json:"description"`
	RequireUserModel      bool                    `json:"require_user_model"`
	// IgnoreUserModel 声明该工具与用户所选模型无关（如抠图），
	// selectOffering 不按用户模型过滤，否则永远找不到可用 Offering。
	IgnoreUserModel bool `json:"ignore_user_model"`
	// RequireUniqueOffering refuses zero or multiple runtime-available choices
	// for a dedicated tool whose selection is independent of the workspace model.
	RequireUniqueOffering bool `json:"require_unique_offering,omitempty"`
	// OfferingArgument names the string argument whose value is the model_id
	// of the Offering to run, such as a Creative Lab template (#189). Its enum
	// lists the declared choices; the Agent is offered only those whose
	// Offering is enabled and runtime-available, and the executor refuses any
	// other value before submission. The selected Offering's own capability is
	// used. Requires ignore_user_model.
	OfferingArgument string `json:"offering_argument,omitempty"`
	// OfferingFromSource names the source-task argument (mapped with
	// source-task-from-image-handle) whose resolved Task picks the Offering:
	// the one enabled, available Offering of the tool's capabilities whose
	// source-task parameter lists that Task's Offering in task_offerings, such
	// as the build stage paired with a Creative Lab prototype (#189).
	OfferingFromSource   string                     `json:"offering_from_source,omitempty"`
	ModelRequiredMessage string                     `json:"model_required_message"`
	Capability           schemacontract.Capability  `json:"capability"`
	FallbackCapability   schemacontract.Capability  `json:"fallback_capability"`
	PendingCapability    string                     `json:"pending_capability"`
	Parameters           json.RawMessage            `json:"parameters"`
	ArgumentMappings     map[string]string          `json:"argument_mappings"`
	OverrideMappings     map[string]string          `json:"override_mappings"`
	DefaultParameters    map[string]json.RawMessage `json:"default_parameters"`
	ResultAssetRoles     []string                   `json:"result_asset_roles"`
	// ResultAttachmentKind is "image" (the historical default), "model" (a
	// model's thumbnail), or "none" (only the real Task handle). Only "none"
	// permits an empty ResultAssetRoles list and never projects task files as images.
	ResultAttachmentKind string `json:"result_attachment_kind,omitempty"`
	// DefaultToSelection makes a tool that was given no reference use the
	// images selected on the workspace ("this image"), when the turn itself
	// carries no picture.
	DefaultToSelection bool `json:"default_to_selection,omitempty"`
	// CanvasSource is what the tool does on a turn whose source is the
	// artboard snapshot (#266): "bind" puts the snapshot first among its
	// references whatever the Agent named, "refuse" turns the call down
	// before submission (the tool reads no image), "ignore" leaves the tool
	// as it is (processing and motion tools). Every tool declares one.
	CanvasSource string `json:"canvas_source"`
	// ReferenceSize is what sets the output size of a turn that is not a
	// canvas turn (#296). "single": when the step sends exactly one reference
	// picture and nobody chose a size (no turn, session, Agent or Skill value,
	// only the Offering's default), each output-size parameter a person may
	// choose takes the legal value closest to that picture's shape and pixel
	// count, by the canvas rule (#284). Empty leaves the size to the defaults.
	ReferenceSize string `json:"reference_size,omitempty"`
	// CrossDomainModelFallback lets the tool run with the Schema's default
	// (else recommended, else first available) Offering when the user chose no
	// model for the tool's own workspace domain while working in the other one.
	// The result then names the model and says it was a fallback.
	CrossDomainModelFallback    bool   `json:"cross_domain_model_fallback,omitempty"`
	PromptWithReferenceTemplate string `json:"prompt_with_reference_template"`
	// ReferenceCapacityNote is added to the tool's description for a turn: how
	// many reference pictures the model picked for the tool's domain uses
	// (#329 E'), so the Agent does not promise that a single-picture model uses
	// several pictures as views of one object. Empty adds nothing.
	ReferenceCapacityNote      ChatToolReferenceCapacityNote `json:"reference_capacity_note,omitzero"`
	PromptWithPreviousTemplate string                        `json:"prompt_with_previous_template"`
	// ReferencePurposesArgument names the tool argument that maps a reference
	// handle to what that picture is for ("keep the pose", "take the outfit",
	// #279). When the Agent gives purposes, the prompt is built with
	// PromptWithReferencePurposesTemplate ({purposes} {prompt}) instead of the
	// reference or previous template; {purposes} is one
	// ReferencePurposeItemTemplate ({position} {purpose}) per purposed picture,
	// in the order the pictures are really sent.
	ReferencePurposesArgument           string               `json:"reference_purposes_argument,omitempty"`
	PromptWithReferencePurposesTemplate string               `json:"prompt_with_reference_purposes_template,omitempty"`
	ReferencePurposeItemTemplate        string               `json:"reference_purpose_item_template,omitempty"`
	IdempotencyKeyTemplate              string               `json:"idempotency_key_template"`
	ResultFields                        ChatToolResultFields `json:"result_fields"`
	// Processing, when present, makes this a tool that processes an existing
	// 3D model of the session instead of creating a new one. It is
	// server-side only and omitted from public projections.
	Processing *ChatToolProcessingSchema `json:"processing,omitempty"`
}

// ChatToolProcessingSchema declares how a processing tool maps the model's
// arguments onto the Offering input contract. Which source fits which
// Offering still comes from the Offering's own parameters (source-task and
// source-asset semantics); this block only names the tool arguments and the
// LLM-facing rejection texts. Arguments the Offering does not declare are
// rejected, never dropped, so a paid step never silently differs from the
// request.
type ChatToolProcessingSchema struct {
	// Plan makes this tool propose an ordered Processing Plan instead of
	// submitting a single step. The plan service owns all later execution.
	Plan *ChatToolProcessingPlanSchema `json:"plan,omitempty"`
	// SourceTaskArgument carries a platform Task ID of the same session.
	SourceTaskArgument string `json:"source_task_argument"`
	// SourceAssetArgument optionally carries one saved model Asset ID of the
	// source Task (or, alone, of any succeeded Task of the session).
	SourceAssetArgument string `json:"source_asset_argument,omitempty"`
	// StepArgument selects one capability of Steps for a tool that performs
	// several kinds of processing. Without it the tool's Capability is used.
	StepArgument string                               `json:"step_argument,omitempty"`
	Steps        map[string]schemacontract.Capability `json:"steps,omitempty"`
	// PromptArgument fills the Offering's prompt-semantic parameter and
	// ReferenceArgument its reference-image-semantic parameter from the
	// chat's historical image indices (the refine_image mechanism).
	PromptArgument    string `json:"prompt_argument,omitempty"`
	ReferenceArgument string `json:"reference_argument,omitempty"`
	// ParameterArguments maps further tool arguments onto Offering parameter
	// names.
	ParameterArguments map[string]string `json:"parameter_arguments,omitempty"`
	// DeliveryFormatArgument names the ParameterArguments argument whose value
	// is a target model format. Its routes are then tried first in the order
	// of model_delivery.conversions, under the kernel's own input validation,
	// so chat converts a source exactly as the format coverage would.
	DeliveryFormatArgument string `json:"delivery_format_argument,omitempty"`
	// SourceVariantPreference orders the model file variants tried first
	// when a source is passed as an Asset.
	SourceVariantPreference []string                   `json:"source_variant_preference,omitempty"`
	Messages                ChatToolProcessingMessages `json:"messages"`
	// Choice makes the tool ask the person, on an in-chat choice card, which
	// service to use when two or more available Offerings accept the source
	// and the call names none (#321). Without it the first compatible one runs.
	Choice *ChatToolOfferingChoiceSchema `json:"choice,omitempty"`
}

// ChatToolOfferingChoiceSchema declares how a processing tool names its
// service and the texts the Agent reads about the choice card (#321).
type ChatToolOfferingChoiceSchema struct {
	// OfferingArgument names the argument that carries an exact Offering ID.
	// Single-step tools only: a plan tool carries offering_id in each step.
	OfferingArgument string `json:"offering_argument,omitempty"`
	// DefaultArgument names the boolean argument with which the person
	// accepts the default (the first compatible Offering in Schema order).
	DefaultArgument string `json:"default_argument"`
	// Content is the tool result text when a choice card was created.
	Content string `json:"content"`
	// UnknownOffering refuses an Offering ID that is not available or does
	// not accept the source. {offering} and {step} are substituted.
	UnknownOffering string `json:"unknown_offering"`
}

type ChatToolProcessingPlanSchema struct {
	StepsArgument   string `json:"steps_argument"`
	CreatedContent  string `json:"created_content"`
	WaitingContent  string `json:"waiting_content"`
	NotFoundContent string `json:"not_found_content"`
}

// ChatToolProcessingMessages are the LLM-facing rejection texts of a
// processing tool. {argument}, {step} and {detail} are substituted.
type ChatToolProcessingMessages struct {
	SourceRequired       string `json:"source_required"`
	SourceNotFound       string `json:"source_not_found"`
	SourceNotReady       string `json:"source_not_ready"`
	SourceIncompatible   string `json:"source_incompatible"`
	StepUnknown          string `json:"step_unknown"`
	ArgumentUnsupported  string `json:"argument_unsupported"`
	ReferenceUnsupported string `json:"reference_unsupported"`
	// ReferenceRequired refuses a step whose service requires one reference
	// image when the call, the current turn and the workspace selection
	// supplied none (or several), before anything is frozen (#92). Required
	// when the tool declares reference_argument.
	ReferenceRequired string `json:"reference_required,omitempty"`
	NoOffering        string `json:"no_offering"`
	InvalidInput      string `json:"invalid_input"`
	Unavailable       string `json:"unavailable"`
	// SourceConfirmation is the refusal of a paid step on another model than
	// the one selected on the workspace, before the person agreed (#321 D).
	// {selected}, {source}, {step} and {reason} are substituted; {reason} is
	// SelectedSourceIncompatible when no available service accepts the
	// selected model for the step, otherwise empty. Empty disables the check.
	SourceConfirmation         string `json:"source_confirmation,omitempty"`
	SelectedSourceIncompatible string `json:"selected_source_incompatible,omitempty"`
	// SelectedServiceRefused joins {reason} when the service the person (or
	// the call) asked for cannot take the selected model: {reasons} is each
	// such service's refusal (#326 K), so the person learns why whichever
	// model the Agent tried. With it, SourceConfirmation carries {options}:
	// only combinations that can be submitted (the asked service on the
	// proposed model, any service that takes the selected one). Optional.
	SelectedServiceRefused string `json:"selected_service_refused,omitempty"`
	// Service holds the texts of a call that would not run the service the
	// person named (#321 G). Without it the tool does not check named
	// services.
	Service *ChatToolServiceMessages `json:"service,omitempty"`
	// SourceExpired replaces SourceIncompatible when the only obstacle is
	// that the Provider no longer keeps the source's original task and no
	// available Offering takes the saved file instead (#325). {step} is
	// substituted. Empty falls back to SourceIncompatible.
	SourceExpired string `json:"source_expired,omitempty"`
}

// ChatToolServiceMessages are the LLM-facing texts a processing tool returns
// when the person named a service in their latest message (or answered a
// choice card) and this call cannot or would not run it (#321 G). Services
// are named by provider and model; Offering IDs travel only in the result's
// own fields, never in these texts.
type ChatToolServiceMessages struct {
	// Refused: no service the person named can run {step} on {source};
	// {requested} names them, {reasons} says why, {options} lists what works.
	Refused string `json:"refused"`
	// Confirmation: the call would run {service} instead of the {requested}
	// one(s) the person named; {reasons} says why those cannot (may be empty),
	// {cost} is the price of {service}, {options} lists what works.
	Confirmation string `json:"confirmation"`
	// Declined: the call would run {service}, which the person said not to
	// use ({step}, {source}, {cost}, {options}).
	Declined string `json:"declined"`
	// Refusal reasons: over the size limit ({service}, {size}, {limit}), a
	// file type it does not take, only results of a provider's
	// {capabilities}, not available now.
	TooLarge    string `json:"too_large"`
	Media       string `json:"media"`
	Task        string `json:"task"`
	Unavailable string `json:"unavailable"`
	// Expired: {service} needs the model's original task, which the provider
	// no longer keeps (#325), and takes no saved model file.
	Expired string `json:"expired"`
	// Options wraps the list of workable options ({options}).
	Options string `json:"options"`
	// OptionSource: a named service on the model the selected one was made
	// from ({service}, {handle}, {selected}, {cost}). OptionService: another
	// service on the same model ({service}, {source}, {cost}).
	OptionSource  string `json:"option_source"`
	OptionService string `json:"option_service"`
	// ReasonsSentence wraps the reasons inside Confirmation ({reasons});
	// ReasonSeparator and OptionSeparator join reasons and options.
	ReasonsSentence string `json:"reasons_sentence"`
	ReasonSeparator string `json:"reason_separator"`
	OptionSeparator string `json:"option_separator"`
	// Prices: {total} credits; with extras {base}, {extra} and {extra_for};
	// native units {amount} {unit}.
	CostCredits      string `json:"cost_credits"`
	CostCreditsExtra string `json:"cost_credits_extra"`
	CostNative       string `json:"cost_native"`
	// NoService is the refusal of a step no available service takes the
	// model for (#326), in the 加工 panel's terms: {step}, {source},
	// {reasons} (each service's reason), {prerequisite} (one of the
	// Prerequisite texts or empty) and {options}. Empty keeps the older
	// source_incompatible / source_expired texts.
	NoService string `json:"no_service,omitempty"`
	// PrerequisiteRuns: the step whose results the services take
	// ({prerequisite}) can run on {source} first with {services}, and its
	// result feeds {step} (the panel brings that step in).
	PrerequisiteRuns string `json:"prerequisite_runs,omitempty"`
	// PrerequisiteRefused: that step cannot run on {source} either; {reasons}.
	PrerequisiteRefused string `json:"prerequisite_refused,omitempty"`
	// OptionPrerequisite: {prerequisite} with {service} on {handle}, the
	// model {selected} was made from ({cost}), then {step}.
	OptionPrerequisite string `json:"option_prerequisite,omitempty"`
}

// ChatToolExecutionSchema declares the runtime implementation and safety
// contract for a tool. It remains server-side and is omitted from public DTOs.
type ChatToolExecutionSchema struct {
	ImplementationKey string                      `json:"implementation_key"`
	Effects           []string                    `json:"effects"`
	Scope             string                      `json:"scope"`
	Domains           []string                    `json:"domains"`
	Capabilities      []schemacontract.Capability `json:"capabilities"`
	Consent           string                      `json:"consent"`
	Idempotency       string                      `json:"idempotency"`
	Cancellation      string                      `json:"cancellation"`
	// AgentScope is the tool's class for the session Agent scope: "2d", "3d",
	// "cross" (moves between 2D and 3D) or "utility" (neither; always
	// available). Every tool declares one.
	AgentScope string `json:"agent_scope"`
}

type ChatToolResultFields struct {
	Success   string `json:"success"`
	TaskID    string `json:"task_id"`
	ImageURLs string `json:"image_urls"`
	Error     string `json:"error"`
	Warning   string `json:"warning"`
	// EstimatedCredits and Approval are optional keys of the tool result the
	// Agent reads: the step's estimated credits and who approved it
	// ("policy" or "human"). An empty key omits the value.
	EstimatedCredits string `json:"estimated_credits,omitempty"`
	Approval         string `json:"approval,omitempty"`
	// ExtraCredits is the optional key of the part of the estimate that is
	// extra credits (extra export formats, billed options). It is present only
	// for a step that spent some; the estimate above already includes it.
	ExtraCredits string `json:"extra_credits,omitempty"`
	// ModelFormats is the optional key listing the model file formats a
	// succeeded 3D step delivered, so the Agent can name them to the user.
	ModelFormats string `json:"model_formats,omitempty"`
	// Model names the model a step used; ModelFallback is true when the
	// Schema's fallback chose it because the user had picked none for this
	// domain. Empty keys omit the values.
	Model         string `json:"model,omitempty"`
	ModelFallback string `json:"model_fallback,omitempty"`
	// ModelNote is the optional key of chat.presentation.messages
	// retired_model_fallback: the model the person picked is retired and the
	// step ran on its successor (#329 follow-up). Present only then.
	ModelNote string `json:"model_note,omitempty"`
	// ReferenceNote is the optional key of a note about the reference picture
	// a step really used, such as "only the front view of a three-view group
	// is used", or, when the turn gave more pictures than the Offering takes,
	// the Offering's runtime.generation_messages reference-limit note (#329;
	// the same text reaches the approval card and the finished message). It
	// is present only when the note applies.
	ReferenceNote string `json:"reference_note,omitempty"`
	// SizeNote is the optional key of the note that, on a canvas turn, the
	// output size followed the artboard or used the nearest legal size
	// (#272), or that it followed the one reference picture (#296). It is
	// present only when the canvas or the reference set the size.
	SizeNote string `json:"size_note,omitempty"`
}

// ChatAgentToolSchema 声明非生成类 agent 控制工具（set_params /
// list_offerings / get_task / observe_task / upload_input_asset /
// request_consent）。与生成工具（ChatToolSchema）分离：控制工具无
// capability/idempotency/结果资产语义，仅 name/description/parameters。
type ChatAgentToolSchema struct {
	Name        string                  `json:"name"`
	Execution   ChatToolExecutionSchema `json:"execution"`
	Description string                  `json:"description"`
	Parameters  json.RawMessage         `json:"parameters"`
}

type ChatSkillsSchema struct {
	Feature    string                    `json:"feature"`
	Fields     ChatSkillCatalogFields    `json:"fields"`
	Creation   ChatSkillCreationSchema   `json:"creation"`
	Categories []ChatSkillCategorySchema `json:"categories"`
	Execution  ChatSkillExecutionSchema  `json:"execution"`
	Recipes    []ChatSkillRecipeSchema   `json:"recipes,omitempty"`
	// SystemSkills are Skills the Product Schema itself declares (as opposed to
	// the editable Skill catalog and personal Skills). A system Skill binds an
	// Agent tool (SkillSchema.Tool) instead of styling a generation prompt; it
	// is offered to every owner whatever Agent configuration version is live.
	SystemSkills []SkillSchema `json:"system_skills,omitempty"`
	// ToolDirectiveTemplate is the system-context block that tells the Agent a
	// tool-binding Skill was chosen. It uses {name}, {prompt} and {tool}.
	ToolDirectiveTemplate string `json:"tool_directive_template,omitempty"`
}

type ChatSkillCreationSchema struct {
	Route string `json:"route"`
	// ItemRoute deletes one of the caller's own personal Skills; it is Route
	// followed by "/{skillID}".
	ItemRoute         string `json:"item_route,omitempty"`
	MaxNameCharacters int    `json:"max_name_characters"`
	RecommendedSize   string `json:"recommended_size"`
}

// ChatSkillRecipeSchema is an executable, versioned Skill definition.
// Offering-specific parameter names are resolved from their semantic roles.
type ChatSkillRecipeSchema struct {
	ID       string                      `json:"id"`
	Revision string                      `json:"revision"`
	Steps    []ChatSkillRecipeStepSchema `json:"steps"`
}

type ChatSkillRecipeStepSchema struct {
	Key            string                     `json:"key"`
	Label          string                     `json:"label"`
	Capability     schemacontract.Capability  `json:"capability"`
	PromptTemplate string                     `json:"prompt_template"`
	OutputRole     string                     `json:"output_role"`
	SourceStepKey  string                     `json:"source_step_key,omitempty"`
	StaticInput    map[string]json.RawMessage `json:"static_input,omitempty"`
}

type ChatSkillExecutionSchema struct {
	PromptTemplateFields ChatSkillPromptTemplateFields                        `json:"prompt_template_fields"`
	PromptTemplates      map[schemacontract.Capability]string                 `json:"prompt_templates"`
	ParameterOverrides   map[schemacontract.ParameterSemantic]json.RawMessage `json:"parameter_overrides"`
}

type ChatSkillPromptTemplateFields struct {
	UserInput   string `json:"user_input"`
	SkillPrompt string `json:"skill_prompt"`
}

type ChatSkillCatalogFields struct {
	Revision             string `json:"revision"`
	Skills               string `json:"skills"`
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	NameZH               string `json:"name_zh"`
	Category             string `json:"category"`
	RecommendedSize      string `json:"recommended_size"`
	ThumbnailPath        string `json:"thumbnail_path"`
	RawImageURL          string `json:"raw_image_url"`
	Prompt               string `json:"prompt"`
	ImageToImageTemplate string `json:"image_to_image_template"`
	Tags                 string `json:"tags"`
	Tool                 string `json:"tool"`
	RequiresImage        string `json:"requires_image"`
	DefaultMessage       string `json:"default_message"`
	OneShot              string `json:"one_shot"`
	Error                string `json:"error"`
}

type ChatSkillCategorySchema struct {
	ID                 string                      `json:"id"`
	Label              string                      `json:"label"`
	HiddenCapabilities []schemacontract.Capability `json:"hidden_capabilities"`
}

type ChatPresentationSchema struct {
	Messages ChatMessageTexts `json:"messages"`
	Status   ChatStatusTexts  `json:"status"`
}

type ChatMessageTexts struct {
	TurnActive string `json:"turn_active"`
	// ChoiceAnswered refuses an answer to a choice card that is no longer open;
	// ChoiceRejected one whose option the card does not offer (#321).
	ChoiceAnswered                   string `json:"choice_answered"`
	ChoiceRejected                   string `json:"choice_rejected"`
	SessionNotFound                  string `json:"session_not_found"`
	MessageNotFound                  string `json:"message_not_found"`
	EmptyTitle                       string `json:"empty_title"`
	EmptyMessage                     string `json:"empty_message"`
	InvalidParameters                string `json:"invalid_parameters"`
	InvalidReferenceIndices          string `json:"invalid_reference_indices"`
	ReferenceIndexOutOfRangeTemplate string `json:"reference_index_out_of_range_template"`
	// InvalidInputSource rejects an undeclared input_source; CanvasSourceMissing
	// rejects a canvas turn whose snapshot did not arrive or did not verify;
	// CanvasSourceToolRefused tells the Agent that a tool which reads no image
	// cannot serve a canvas turn ({tools} lists the tools that can) (#266).
	InvalidInputSource      string `json:"invalid_input_source"`
	CanvasSourceMissing     string `json:"canvas_source_missing"`
	CanvasSourceToolRefused string `json:"canvas_source_tool_refused"`
	// RetiredModelFallback is the note a generation step's result carries
	// (result_fields.model_note) when the turn named a retired model and the
	// service ran its successor instead ({from}, {to}; #329 follow-up).
	RetiredModelFallback string `json:"retired_model_fallback,omitempty"`
	// OfferingChoiceUnavailable tells the Agent that the Offering named by a
	// tool's offering_argument is not enabled or available ({choices} lists
	// the ones that are); SourceOfferingUnpaired that the source Task of an
	// offering_from_source tool pairs with no available Offering (#189).
	// SourceOfferingCanvas replaces it when the source named is the turn's
	// artboard snapshot that shows no single pairable Task, and
	// SourceOfferingCandidates ({handles}) follows either one with the image
	// handles of this session that would pair.
	OfferingChoiceUnavailable string `json:"offering_choice_unavailable"`
	SourceOfferingUnpaired    string `json:"source_offering_unpaired"`
	SourceOfferingCanvas      string `json:"source_offering_canvas"`
	SourceOfferingCandidates  string `json:"source_offering_candidates"`
	Interrupted               string `json:"interrupted"`
	InterruptedPrompt         string `json:"interrupted_prompt"`
	GenerationFailed          string `json:"generation_failed"`
	GenerationError           string `json:"generation_error"`
	SummaryFailed             string `json:"summary_failed"`
	NoResult                  string `json:"no_result"`
	UnknownError              string `json:"unknown_error"`
	// AgentImagesUnsupported replaces UnknownError when the Agent model
	// refused a request that carried pictures (#253).
	AgentImagesUnsupported string `json:"agent_images_unsupported"`
	// AgentNotConfigured is what the person reads while the live Agent has no
	// API key or no model: Agent replies and prompt enhancement are off (#253).
	AgentNotConfigured string `json:"agent_not_configured"`
	// ErrorReference is appended to a failed reply with the assistant message
	// ID that the server log records, so users can quote it (#240).
	ErrorReference string `json:"error_reference,omitempty"`
	// ReasoningUnavailable refuses a thinking-level write while the session's
	// model offers no level. Compaction* answer a compaction request: a turn
	// is still running, nothing lies before the kept rounds ({rounds}), the
	// summary call failed (the session stays as it was), or the deployment
	// has compaction off (#322).
	ReasoningUnavailable  string `json:"reasoning_unavailable,omitempty"`
	CompactionTurnActive  string `json:"compaction_turn_active,omitempty"`
	CompactionNothing     string `json:"compaction_nothing,omitempty"`
	CompactionFailed      string `json:"compaction_failed,omitempty"`
	CompactionUnavailable string `json:"compaction_unavailable,omitempty"`
	// ChatModelUnavailable refuses a chat-model write while the deployment
	// keeps no per-session choice; ChatModelInvalid one naming a model that
	// is not available (#322 Scope 5).
	ChatModelUnavailable string `json:"chat_model_unavailable,omitempty"`
	ChatModelInvalid     string `json:"chat_model_invalid,omitempty"`
	SSEInterrupted       string `json:"sse_interrupted"`
	InternalError        string `json:"internal_error"`
	Unauthenticated      string `json:"unauthenticated"`
	AgentBusy            string `json:"agent_busy"`
	TurnStopped          string `json:"turn_stopped"`
	ToolRoundLimit       string `json:"tool_round_limit"`
	// ToolRoundLimitPending heads the list of tool calls the model still
	// wanted once the tool rounds ran out: the unexecuted batch and any DSML
	// the tool-less summary round wrote anyway (#275). Each call is one line
	// from ToolRoundLimitPendingItem ({tool}, {intent}: the call's prompt
	// argument shortened to context.tool_prompt_preview_characters), or from
	// ToolRoundLimitPendingTool ({tool}) when the call carries no prompt.
	ToolRoundLimitPending     string `json:"tool_round_limit_pending"`
	ToolRoundLimitPendingItem string `json:"tool_round_limit_pending_item"`
	ToolRoundLimitPendingTool string `json:"tool_round_limit_pending_tool"`
	// GenerationFailedByClass replaces GenerationFailed in the tool result
	// when the Task failed with one of these failure classes, so the Agent and
	// the user hear the real cause (for example an exhausted provider balance)
	// instead of a generic failure. Keys are failure classes.
	GenerationFailedByClass map[string]string `json:"generation_failed_by_class,omitempty"`
	// GenerationFailedMeteredByClass is the variant of GenerationFailedByClass
	// for an Offering that bills in credits (credits_metered). A class listed
	// here means: the upstream may already have finished and charged, so the
	// Agent must not resubmit on its own; the text states the cause and leaves
	// the retry to the user. It wins over GenerationFailedByClass.
	GenerationFailedMeteredByClass map[string]string `json:"generation_failed_metered_by_class,omitempty"`
}

type ChatStatusTexts struct {
	Generating string `json:"generating"`
	// Thinking is shown while a model round has no visible output yet (#285).
	Thinking string `json:"thinking,omitempty"`
	// Compacting is shown while a turn first compacts the context;
	// CompactionFailed when that failed and the turn goes on with the full
	// context; Compacted when it succeeded (#322).
	Compacting       string `json:"compacting,omitempty"`
	CompactionFailed string `json:"compaction_failed,omitempty"`
	Compacted        string `json:"compacted,omitempty"`
}

type ChatAuditSchema struct {
	CreateAction        string `json:"create_action"`
	DeleteAction        string `json:"delete_action"`
	SessionResourceType string `json:"session_resource_type"`
	MessageResourceType string `json:"message_resource_type"`
	// ApprovalModeAction records a change of the session approval mode.
	ApprovalModeAction string `json:"approval_mode_action"`
	// AgentScopeAction records a change of the session Agent scope.
	AgentScopeAction string `json:"agent_scope_action"`
}

type RuntimeConfigurationSchema struct {
	Settings map[string]RuntimeSettingSchema `json:"settings"`
	// RetiredEnvironment lists environment variables that earlier releases
	// read and this one ignores (#249). A deployment that still sets one
	// starts normally and logs one warning naming them, never their values.
	RetiredEnvironment []string `json:"retired_environment,omitempty"`
}

type RuntimeSettingSchema struct {
	Type                 string                    `json:"type"`
	Environment          []EnvironmentSourceSchema `json:"environment"`
	Default              string                    `json:"default"`
	DefaultByEnvironment map[string]string         `json:"default_by_environment,omitempty"`
	Separator            string                    `json:"separator,omitempty"`
	Secret               bool                      `json:"secret,omitempty"`
	Required             bool                      `json:"required,omitempty"`
	AllowedValues        []string                  `json:"allowed_values,omitempty"`
}

type EnvironmentSourceSchema struct {
	Name       string `json:"name"`
	TrimSuffix string `json:"trim_suffix,omitempty"`
	Append     string `json:"append,omitempty"`
	// Deprecated marks a historical alias that still works but is logged at
	// startup with the canonical name to use instead (#252).
	Deprecated bool `json:"deprecated,omitempty"`
}

type CoordinatorRuntimeSchema struct {
	PollIntervalMilliseconds int `json:"poll_interval_milliseconds"`
	LeaseSeconds             int `json:"lease_seconds"`
	BatchSize                int `json:"batch_size"`
	Concurrency              int `json:"concurrency"`
	LeaseRenewMilliseconds   int `json:"lease_renew_milliseconds"`
	MaxBackoffMilliseconds   int `json:"max_backoff_milliseconds"`
	// ShutdownGraceSeconds 界定停机排水窗口：停止领取新任务后，在飞
	// Provider 调用与租约续期最多再运行这么久，超时才强制中断（中断
	// 的提交按既有 SUBMISSION_UNKNOWN 语义处理）。0 视为未声明。
	ShutdownGraceSeconds int `json:"shutdown_grace_seconds,omitempty"`
	// DispatchCheckpointMaxBytes 声明私有派发意图（W4 §4.2）的大小上限；
	// 0 视为未声明，运行时按默认值处理。服务端内部调度参数，禁止进入
	// 公开投影（Public() 显式剥离）。
	DispatchCheckpointMaxBytes int64 `json:"dispatch_checkpoint_max_bytes,omitempty"`
	// DefaultRequestTimeoutSeconds 是 Offering 未声明 timeout_seconds 时
	// 的单次上游请求兜底时长（B17：原内核硬编码 30 分钟迁入 Schema）。
	// 0 视为未声明，运行时按默认值处理；服务端内部参数，禁止进入公开投影。
	DefaultRequestTimeoutSeconds int `json:"default_request_timeout_seconds,omitempty"`
}

type ProviderAccountSchema struct {
	ProviderID     string                `json:"provider_id"`
	BaseURLSetting string                `json:"base_url_setting"`
	APIKeySetting  string                `json:"api_key_setting"`
	Balance        ProviderBalanceSchema `json:"balance"`
}

type ProviderBalanceSchema struct {
	NativeUnit            string  `json:"native_unit,omitempty"`
	NativeUnitLabel       string  `json:"native_unit_label,omitempty"`
	MockNativeAmount      string  `json:"mock_native_amount,omitempty"`
	AdapterKey            string  `json:"adapter_key"`
	EndpointPath          string  `json:"endpoint_path"`
	AuthorizationHeader   string  `json:"authorization_header"`
	AuthorizationType     string  `json:"authorization_type"`
	UpstreamBalanceField  string  `json:"upstream_balance_field"`
	RequestTimeoutSeconds int     `json:"request_timeout_seconds"`
	ResponseMaxBytes      int64   `json:"response_max_bytes"`
	ErrorBodyMaxBytes     int64   `json:"error_body_max_bytes"`
	MockValue             float64 `json:"mock_value"`
}

type HTTPRuntimeSchema struct {
	ReadHeaderTimeoutSeconds int   `json:"read_header_timeout_seconds"`
	ReadTimeoutSeconds       int   `json:"read_timeout_seconds"`
	WriteTimeoutSeconds      int   `json:"write_timeout_seconds"`
	IdleTimeoutSeconds       int   `json:"idle_timeout_seconds"`
	ShutdownTimeoutSeconds   int   `json:"shutdown_timeout_seconds"`
	JSONBodyMaxBytes         int64 `json:"json_body_max_bytes"`
	MultipartBodyMaxBytes    int64 `json:"multipart_body_max_bytes"`
	// CSRFOriginEnforced 为 true 时，状态变更请求携带的 Origin 必须与
	// 站点同源（SEC-03）；SecurityHeaders 是全站响应安全头（SEC-04）。
	CSRFOriginEnforced bool                       `json:"csrf_origin_enforced"`
	SecurityHeaders    map[string]string          `json:"security_headers"`
	TaskAdmission      TaskAdmissionRuntimeSchema `json:"task_admission"`
	DefaultPageSize    int                        `json:"default_page_size"`
	MaximumPageSize    int                        `json:"maximum_page_size"`
	MaximumRecentTasks int                        `json:"maximum_recent_tasks"`
	// MaximumLineageTasks bounds how many tasks of one session the version
	// chain reads; older ones show as unknown parents.
	MaximumLineageTasks        int    `json:"maximum_lineage_tasks"`
	LineagePageSize            int    `json:"lineage_page_size"`
	PrivateCacheControl        string `json:"private_cache_control"`
	ImmutableAssetCacheControl string `json:"immutable_asset_cache_control"`
	SchemaCacheControl         string `json:"schema_cache_control"`
	// ThumbnailCacheControl lets the owner's browser reuse thumbnail bytes for
	// a short time (#225). It applies only to task assets whose role is in
	// ThumbnailAssetRoles and only after authorization; everything else keeps
	// PrivateCacheControl. ThumbnailCacheMaxAgeSeconds caps its max-age.
	ThumbnailCacheControl       string   `json:"thumbnail_cache_control"`
	ThumbnailCacheMaxAgeSeconds int      `json:"thumbnail_cache_max_age_seconds"`
	ThumbnailAssetRoles         []string `json:"thumbnail_asset_roles"`
	// SessionChangeClearSiteData is sent on login and logout so a browser drops
	// cached private bytes when the signed-in user changes.
	SessionChangeClearSiteData string                      `json:"session_change_clear_site_data,omitempty"`
	Routes                     CoreHTTPRoutesSchema        `json:"routes"`
	LegacyRoutes               LegacyHTTPRoutesSchema      `json:"legacy_routes"`
	LegacyProviderBalance      LegacyProviderBalanceSchema `json:"legacy_provider_balance"`
	CandidateProxy             CandidateProxySchema        `json:"candidate_proxy"`
	// ClientErrorReport bounds Routes.ClientErrors; server-only.
	ClientErrorReport ClientErrorReportSchema `json:"client_error_report"`
	// APIRateLimit caps requests per client address per minute under
	// PathPrefix (#256). Zero disables it (historical Catalogs).
	APIRateLimit APIRateLimitSchema `json:"api_rate_limit"`
}

type APIRateLimitSchema struct {
	PathPrefix        string `json:"path_prefix"`
	RequestsPerMinute int    `json:"requests_per_minute"`
}

type ClientErrorReportSchema struct {
	MaxBodyBytes      int64 `json:"max_body_bytes"`
	PerOwnerPerMinute int   `json:"per_owner_per_minute"`
	MaxTextCharacters int   `json:"max_text_characters"`
	// Anonymous* bound reports from signed-out pages (#317). Per client is
	// per address (an IPv6 /64); the global cap holds across all addresses.
	// AnonymousGlobalPerMinute 0 keeps the route signed-in only.
	AnonymousMaxBodyBytes       int64 `json:"anonymous_max_body_bytes,omitempty"`
	AnonymousPerClientPerMinute int   `json:"anonymous_per_client_per_minute,omitempty"`
	AnonymousGlobalPerMinute    int   `json:"anonymous_global_per_minute,omitempty"`
	AnonymousMaxTextCharacters  int   `json:"anonymous_max_text_characters,omitempty"`
}

type CoreHTTPRoutesSchema struct {
	Home             string `json:"home"`
	Liveness         string `json:"liveness"`
	Readiness        string `json:"readiness"`
	ProductSchema    string `json:"product_schema"`
	AccountUsage     string `json:"account_usage"`
	AccountResources string `json:"account_resources"`
	ChatSkills       string `json:"chat_skills"`
	// ClientErrors receives browser error reports for the server log (#233).
	ClientErrors          string `json:"client_errors,omitempty"`
	ChatSessionApproval   string `json:"chat_session_approval"`
	ChatSessionNotes      string `json:"chat_session_notes"`
	ChatSessionPick       string `json:"chat_session_pick"`
	ChatSessionChoices    string `json:"chat_session_choices"`
	ChatSessionAgentScope string `json:"chat_session_agent_scope"`
	// ChatSessionReasoning reads and writes the session thinking level;
	// ChatSessionContext reads the context usage and compaction records;
	// ChatSessionContextCompaction compacts the context now (#322).
	ChatSessionReasoning         string `json:"chat_session_reasoning,omitempty"`
	ChatSessionContext           string `json:"chat_session_context,omitempty"`
	ChatSessionContextCompaction string `json:"chat_session_context_compaction,omitempty"`
	// ChatSessionModel reads and writes the session's chat model (#322).
	ChatSessionModel            string `json:"chat_session_model,omitempty"`
	ChatSessionHarness          string `json:"chat_session_harness"`
	ChatSessionPendingApprovals string `json:"chat_session_pending_approvals"`
	ChatSessionThreeViews       string `json:"chat_session_three_views,omitempty"`
	ChatSessionThreeViewItem    string `json:"chat_session_three_view_item,omitempty"`
	ChatSessionLineage          string `json:"chat_session_lineage"`
	ProviderAsset               string `json:"provider_asset"`
	Offerings                   string `json:"offerings"`
	Assets                      string `json:"assets"`
	AssetItem                   string `json:"asset_item"`
	AssetModelPreview           string `json:"asset_model_preview,omitempty"`
	ModelCaptures               string `json:"model_captures,omitempty"`
	ModelCaptureItem            string `json:"model_capture_item,omitempty"`
	SceneObjects                string `json:"scene_objects,omitempty"`
	SceneObjectItem             string `json:"scene_object_item,omitempty"`
	SceneObjectRepresentations  string `json:"scene_object_representations,omitempty"`
	SceneObjectApply            string `json:"scene_object_apply,omitempty"`
	SceneSnapshot               string `json:"scene_snapshot,omitempty"`
	RenderManifests             string `json:"render_manifests,omitempty"`
	RenderManifestItem          string `json:"render_manifest_item,omitempty"`
	SkillRuns                   string `json:"skill_runs,omitempty"`
	SkillRunItem                string `json:"skill_run_item,omitempty"`
	SkillRunStep                string `json:"skill_run_step,omitempty"`
	SkillRunReview              string `json:"skill_run_review,omitempty"`
	Tasks                       string `json:"tasks"`
	TaskPrepare                 string `json:"task_prepare"`
	TaskGrants                  string `json:"task_grants"`
	TaskGrantDecision           string `json:"task_grant_decision"`
	TaskItem                    string `json:"task_item"`
	TaskRecovery                string `json:"task_recovery,omitempty"`
	TaskRecoveryPrepare         string `json:"task_recovery_prepare,omitempty"`
	// TaskRefetch: GET the owner's upstream refetch eligibility of a failed
	// Task, POST to start (or return) its refetch (#295).
	TaskRefetch             string `json:"task_refetch,omitempty"`
	ProcessingPlans         string `json:"processing_plans,omitempty"`
	ProcessingPlanItem      string `json:"processing_plan_item,omitempty"`
	ProcessingPlanRetry     string `json:"processing_plan_retry,omitempty"`
	ProcessingPlanResume    string `json:"processing_plan_resume,omitempty"`
	ProcessingPlanReconcile string `json:"processing_plan_reconcile,omitempty"`
	ModelFormats            string `json:"model_formats,omitempty"`
	ModelFormatsComplete    string `json:"model_formats_complete,omitempty"`
	ModelArchive            string `json:"model_archive,omitempty"`
	AgentParams             string `json:"agent_params"`
	AgentBind               string `json:"agent_bind"`
	AgentConsent            string `json:"agent_consent"`
	TaskCancel              string `json:"task_cancel"`
	TaskRetry               string `json:"task_retry"`
	TaskEvents              string `json:"task_events"`
	TaskAsset               string `json:"task_asset"`
}

type LegacyHTTPRoutesSchema struct {
	Health                  string `json:"health"`
	Providers               string `json:"providers"`
	Tasks                   string `json:"tasks"`
	Version                 string `json:"version"`
	AssetUpload             string `json:"asset_upload"`
	AssetItem               string `json:"asset_item"`
	PromptEnhance           string `json:"prompt_enhance"`
	AuthLogin               string `json:"auth_login"`
	AuthMock                string `json:"auth_mock"`
	AuthSession             string `json:"auth_session"`
	AuthPassword            string `json:"auth_password"`
	CacheStatus             string `json:"cache_status"`
	GeneratedImage          string `json:"generated_image"`
	ChatSessions            string `json:"chat_sessions"`
	ChatSession             string `json:"chat_session"`
	ChatMessages            string `json:"chat_messages"`
	ChatMessage             string `json:"chat_message"`
	ChatStream              string `json:"chat_stream"`
	ChatTurnCancel          string `json:"chat_turn_cancel"`
	ProviderBalance         string `json:"provider_balance"`
	CanvasProjects          string `json:"canvas_projects"`
	CanvasProject           string `json:"canvas_project"`
	CanvasNodes             string `json:"canvas_nodes"`
	CanvasMerge             string `json:"canvas_merge"`
	TaskStream              string `json:"task_stream"`
	AdminUsers              string `json:"admin_users"`
	AdminUser               string `json:"admin_user"`
	AdminAuditLogs          string `json:"admin_audit_logs"`
	AdminGenerations        string `json:"admin_generations"`
	AdminGeneration         string `json:"admin_generation"`
	AdminGenerationDownload string `json:"admin_generation_download"`
	AdminUsage              string `json:"admin_usage"`
	AdminBalanceSummary     string `json:"admin_balance_summary"`
	AdminUserAssets         string `json:"admin_user_assets"`
	AdminBackfillStatus     string `json:"admin_backfill_status"`
	MeshyProxyAsset         string `json:"meshy_proxy_asset"`
}

type ProviderBalanceConsumptionEstimate struct {
	Capability string  `json:"capability"`
	Credits    float64 `json:"credits"`
}

type ProviderBalanceAccountSchema struct {
	AccountID            string                               `json:"account_id"`
	Label                string                               `json:"label,omitempty"`
	ConsumptionEstimates []ProviderBalanceConsumptionEstimate `json:"consumption_estimates,omitempty"`
}

type LegacyProviderBalanceSchema struct {
	AccountsField string                         `json:"accounts_field"`
	ProviderField string                         `json:"provider_field"`
	BalanceField  string                         `json:"balance_field"`
	ErrorField    string                         `json:"error_field"`
	Accounts      []ProviderBalanceAccountSchema `json:"accounts"`
}

type CandidateProxySchema struct {
	TaskPrefix      string   `json:"task_prefix"`
	CorePrefix      string   `json:"core_prefix"`
	LegacyRouteKeys []string `json:"legacy_route_keys"`
}

type CacheRuntimeSchema struct {
	MillisecondsPerSecond int               `json:"milliseconds_per_second"`
	BytesPerMegabyte      int64             `json:"bytes_per_megabyte"`
	SizeDecimalPlaces     int               `json:"size_decimal_places"`
	Fields                CacheStatusFields `json:"fields"`
}

type CacheStatusFields struct {
	ActiveDownloads  string `json:"active_downloads"`
	ActiveItems      string `json:"active_items"`
	Filename         string `json:"filename"`
	Elapsed          string `json:"elapsed"`
	TaskID           string `json:"task_id"`
	TaskType         string `json:"task_type"`
	ProviderID       string `json:"provider_id"`
	AssetKind        string `json:"asset_kind"`
	TotalCachedFiles string `json:"total_cached_files"`
	TotalCacheSizeMB string `json:"total_cache_size_mb"`
}

type AssetDeliveryRuntimeSchema struct {
	LegacyGeneratedImage LegacyGeneratedImageRuntimeSchema `json:"legacy_generated_image"`
	TaskAssetAudit       TaskAssetAuditRuntimeSchema       `json:"task_asset_audit"`
	AssetPreview         AssetPreviewRuntimeSchema         `json:"asset_preview"`
	Proxy                AssetProxyRuntimeSchema           `json:"proxy"`
}

// AssetPreviewRuntimeSchema (#290) declares the downscaled copy of an owned picture (a task output or an
// input asset) that small views (chat result cards, the version strip, chips) ask for with
// Query=RequestValue instead of the original. Previews are cached under StorageRoot/CacheSubdirectory,
// keyed by the original's SHA-256; the route authorizes the asset before a preview is read or made.
type AssetPreviewRuntimeSchema struct {
	Query             string   `json:"query"`
	RequestValue      string   `json:"request_value"`
	CacheSubdirectory string   `json:"cache_subdirectory"`
	MaximumDimension  int      `json:"maximum_dimension"`
	MaximumInputBytes int64    `json:"maximum_input_bytes"`
	MaximumPixels     int64    `json:"maximum_pixels"`
	DecodeConcurrency int      `json:"decode_concurrency"`
	SourceMediaTypes  []string `json:"source_media_types"`
	OutputMediaType   string   `json:"output_media_type"`
}

type AssetProxyRuntimeSchema struct {
	API                 AssetProxyAPISchema       `json:"api"`
	Security            AssetProxySecuritySchema  `json:"security"`
	Cache               AssetProxyCacheSchema     `json:"cache"`
	HTTP                AssetProxyHTTPSchema      `json:"http"`
	Thumbnail           AssetProxyThumbnailSchema `json:"thumbnail"`
	Metadata            AssetProxyMetadataSchema  `json:"metadata"`
	Audit               AssetProxyAuditSchema     `json:"audit"`
	Messages            AssetProxyMessageSchema   `json:"messages"`
	MediaTypeExtensions map[string]string         `json:"media_type_extensions"`
	DefaultMediaType    string                    `json:"default_media_type"`
}

type AssetProxyAPISchema struct {
	Query    AssetProxyQuerySchema  `json:"query"`
	Fields   AssetProxyFieldSchema  `json:"fields"`
	Statuses AssetProxyStatusSchema `json:"statuses"`
}

type AssetProxyQuerySchema struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	TaskID   string `json:"task_id"`
	Size     string `json:"size"`
}

type AssetProxyFieldSchema struct {
	Error string `json:"error"`
}

type AssetProxyStatusSchema struct {
	Success    int `json:"success"`
	BadRequest int `json:"bad_request"`
	Forbidden  int `json:"forbidden"`
	NotFound   int `json:"not_found"`
	BadGateway int `json:"bad_gateway"`
}

type AssetProxySecuritySchema struct {
	HostSource     string   `json:"host_source"`
	AllowedSchemes []string `json:"allowed_schemes"`
	AllowedHosts   []string `json:"allowed_hosts,omitempty"`
}

type AssetProxyCacheSchema struct {
	RootSetting     string                        `json:"root_setting"`
	ShardCharacters int                           `json:"shard_characters"`
	MetadataSuffix  string                        `json:"metadata_suffix"`
	MetadataFields  AssetProxyMetadataFieldSchema `json:"metadata_fields"`
	Control         string                        `json:"control"`
	CORSOrigin      string                        `json:"cors_origin"`
	StatusHeader    string                        `json:"status_header"`
	HitValue        string                        `json:"hit_value"`
	MissValue       string                        `json:"miss_value"`
	RefreshedValue  string                        `json:"refreshed_value"`
}

type AssetProxyMetadataFieldSchema struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Size        string `json:"size"`
	CachedAt    string `json:"cached_at"`
}

type AssetProxyHTTPSchema struct {
	RequestTimeoutMilliseconds int64 `json:"request_timeout_milliseconds"`
	ResponseMaxBytes           int64 `json:"response_max_bytes"`
	MaximumRedirects           int   `json:"maximum_redirects"`
}

type AssetProxyThumbnailSchema struct {
	MaximumInputBytes int64  `json:"maximum_input_bytes"`
	MaximumPixels     int64  `json:"maximum_pixels"`
	DecodeConcurrency int    `json:"decode_concurrency"`
	RootSetting       string `json:"root_setting"`
	RequestValue      string `json:"request_value"`
	MaximumDimension  int    `json:"maximum_dimension"`
	OutputMediaType   string `json:"output_media_type"`
	FilenameSuffix    string `json:"filename_suffix"`
	Header            string `json:"header"`
	HeaderValue       string `json:"header_value"`
}

type AssetProxyMetadataSchema struct {
	SupportedMediaTypes []string `json:"supported_media_types"`
	CopyrightTemplate   string   `json:"copyright_template"`
	Artist              string   `json:"artist"`
	Software            string   `json:"software"`
}

type AssetProxyAuditSchema struct {
	Action                      string                           `json:"action"`
	ResourceType                string                           `json:"resource_type"`
	ResourceIDMaximumCharacters int                              `json:"resource_id_maximum_characters"`
	DetailFields                AssetProxyAuditDetailFieldSchema `json:"detail_fields"`
}

type AssetProxyAuditDetailFieldSchema struct {
	Filename string `json:"filename"`
	Size     string `json:"size"`
}

type AssetProxyMessageSchema struct {
	MissingURL    string `json:"missing_url"`
	InvalidURL    string `json:"invalid_url"`
	ForbiddenHost string `json:"forbidden_host"`
	FetchFailed   string `json:"fetch_failed"`
}

type TaskAssetAuditRuntimeSchema struct {
	DownloadAction string                          `json:"download_action"`
	ResourceType   string                          `json:"resource_type"`
	DetailFields   TaskAssetAuditDetailFieldSchema `json:"detail_fields"`
}

type TaskAssetAuditDetailFieldSchema struct {
	TaskID  string `json:"task_id"`
	AssetID string `json:"asset_id"`
}

type LegacyGeneratedImageRuntimeSchema struct {
	FilenameSeparator   string            `json:"filename_separator"`
	OrdinalBase         int               `json:"ordinal_base"`
	AssetRoles          []string          `json:"asset_roles"`
	ExtensionMediaTypes map[string]string `json:"extension_media_types"`
	DefaultMediaType    string            `json:"default_media_type"`
	CacheControl        string            `json:"cache_control"`
	NotFoundMessageKey  string            `json:"not_found_message_key"`
	AuditAction         string            `json:"audit_action"`
	AuditResourceType   string            `json:"audit_resource_type"`
}

type InputAssetRuntimeSchema struct {
	ModelProfile *ModelInputProfile `json:"model_profile,omitempty"`
	// GeneratedModelProfile bounds local conversions whose source is a model a
	// successful platform Task already stored. Server-only; never an upload
	// profile and never projected.
	GeneratedModelProfile    *GeneratedModelProfile `json:"generated_model_profile,omitempty"`
	UploadFieldName          string                 `json:"upload_field_name"`
	UploadMaxBytes           int64                  `json:"upload_max_bytes"`
	DefaultMediaType         string                 `json:"default_media_type"`
	DefaultAssetType         string                 `json:"default_asset_type"`
	StoredContentPolicy      string                 `json:"stored_content_policy"`
	JSONFormatParameter      string                 `json:"json_format_parameter"`
	JSONFormatValue          string                 `json:"json_format_value"`
	BinaryCacheControl       string                 `json:"binary_cache_control"`
	LegacyStorageRootSetting string                 `json:"legacy_storage_root_setting"`
	AllowedUploadMediaTypes  []string               `json:"allowed_upload_media_types"`
	InlineMediaTypes         []string               `json:"inline_media_types"`
	API                      InputAssetAPISchema    `json:"api"`
	Audit                    InputAssetAuditSchema  `json:"audit"`
	TypeRules                []InputAssetTypeRule   `json:"type_rules"`
	// GeneratedReferenceMaxBytes bounds copying an asset a platform Task
	// already generated into an input asset when it is used as a reference
	// (#276). upload_max_bytes is for files people upload and must not refuse
	// this platform's own results; the Offering a reference is submitted to
	// still checks its own limits. Messages and ImageFitting are the refusal
	// texts and the compression policy for input images over those limits.
	// Server-only; never projected.
	GeneratedReferenceMaxBytes int64                    `json:"generated_reference_max_bytes"`
	Messages                   InputAssetMessagesSchema `json:"messages"`
	ImageFitting               ImageFittingSchema       `json:"image_fitting"`
}

// InputAssetMessagesSchema is the text people and the Agent read when an input
// image cannot be sent within an Offering's declared limits (#276). Sizes
// ({size}, {compressed}, {limit}) are rendered with schema.SizeText; {label} is
// the refusing parameter's label. Each says the image, not the model or the
// service, is the cause.
type InputAssetMessagesSchema struct {
	GeneratedReferenceTooLargeTemplate string `json:"generated_reference_too_large_template"`
	ParameterTooLargeTemplate          string `json:"parameter_too_large_template"`
	CompressionInsufficientTemplate    string `json:"compression_insufficient_template"`
	TransparencyKeptTemplate           string `json:"transparency_kept_template"`
	TotalTooLargeTemplate              string `json:"total_too_large_template"`
	TooManyPixelsTemplate              string `json:"too_many_pixels_template"`
	TooSmallTemplate                   string `json:"too_small_template"`
	AspectRatioTemplate                string `json:"aspect_ratio_template"`
	UnreadableImageTemplate            string `json:"unreadable_image_template"`
}

// ImageFittingSchema declares how an input image over an Offering's byte
// limit is compressed before it is sent (#276). Steps run in order and stop at
// the first result within the limit; width and height never change. The
// result is a new input asset recording its source; the original is kept.
type ImageFittingSchema struct {
	// ParameterSemantics names the parameters whose images may be compressed
	// (reference images; never masks, whose pixels are exact).
	ParameterSemantics []string `json:"parameter_semantics"`
	// Steps: "lossless-png" (PNG at best compression) and "lossy" (JPEG for an
	// image without transparency at each of JPEGQualities, highest first).
	Steps         []string `json:"steps"`
	JPEGQualities []int    `json:"jpeg_qualities"`
	// MaxDecodePixels bounds the pixels decoded for compression.
	MaxDecodePixels int64 `json:"max_decode_pixels"`
}

type InputAssetAPISchema struct {
	Fields InputAssetAPIFieldSchema `json:"fields"`
}

type InputAssetAPIFieldSchema struct {
	ID               string `json:"id"`
	AssetID          string `json:"asset_id"`
	AssetType        string `json:"asset_type"`
	StoredSHA256     string `json:"stored_sha256"`
	OriginalFilename string `json:"original_filename"`
	MediaType        string `json:"media_type"`
	FileSize         string `json:"file_size"`
	DataURI          string `json:"data_uri"`
	CreatedAt        string `json:"created_at"`
}

type InputAssetAuditSchema struct {
	UploadAction string                           `json:"upload_action"`
	AccessAction string                           `json:"access_action"`
	ResourceType string                           `json:"resource_type"`
	DetailFields InputAssetAuditDetailFieldSchema `json:"detail_fields"`
}

type InputAssetAuditDetailFieldSchema struct {
	Filename  string `json:"filename"`
	MediaType string `json:"media_type"`
	FileSize  string `json:"file_size"`
}

type InputAssetTypeRule struct {
	AssetType  string   `json:"asset_type"`
	MediaTypes []string `json:"media_types,omitempty"`
	Extensions []string `json:"extensions,omitempty"`
}

// TaskAdmissionRuntimeSchema 声明任务提交准入策略（SEC-08）：按所有者
// 限制进行中（未终态）任务数量，防止付费生成被脚本滥用。
type TaskAdmissionRuntimeSchema struct {
	PerOwnerMaxInflightTasks int `json:"per_owner_max_inflight_tasks"`
}

// TaskDeletionRuntimeSchema 将外部 DELETE 契约拆为可独立演进的策略。
// 用户侧使用 tombstone 保持与物理删除相同的不可见性，同时保留任务事件供审计；
// 上游删除和生成资产回收则由独立策略决定。
type TaskDeletionRuntimeSchema struct {
	LocalRecordPolicy     string `json:"local_record_policy"`
	UpstreamPolicy        string `json:"upstream_policy"`
	UpstreamFailurePolicy string `json:"upstream_failure_policy"`
	GeneratedAssetPolicy  string `json:"generated_asset_policy"`
	AuditEventType        string `json:"audit_event_type"`
}

// TaskEventsRuntimeSchema 声明公开任务事件面的投影白名单（§8.1 SSE·事件
// 行；P3）：只有声明的事件类型可以进入公开事件面（未声明类型整体丢弃），
// 且类型内只有声明的字段可以出现（其余字段一律剥离；私有 Opaque/检查点/
// 去重与内部调度键/上游取证字段/原始定位符/原始错误内部永不声明）。
// 它是"什么事件数据是公开的"的唯一真源，服务端专用，禁止进入公开
// Schema 投影（PublicRuntimeSchema 按构造不含本节）。
type TaskEventsRuntimeSchema struct {
	PublicProjection []PublicEventProjectionSchema `json:"public_projection"`
}

// PublicEventProjectionSchema 是单个事件类型的公开字段白名单条目。
type PublicEventProjectionSchema struct {
	Type   string   `json:"type"`
	Fields []string `json:"fields"`
}

type TaskAuditRuntimeSchema struct {
	CreateAction string                     `json:"create_action"`
	DeleteAction string                     `json:"delete_action"`
	ResourceType string                     `json:"resource_type"`
	DetailFields TaskAuditDetailFieldSchema `json:"detail_fields"`
}

type TaskAuditDetailFieldSchema struct {
	Capability string `json:"capability"`
	OfferingID string `json:"offering_id"`
	Replayed   string `json:"replayed"`
}

type FrontendRuntimeSchema struct {
	ImageUpload                          ImageUploadSchema           `json:"image_upload"`
	ModelProcessingCapabilities          []schemacontract.Capability `json:"model_processing_capabilities,omitempty"`
	WebAssetPrefix                       string                      `json:"web_asset_prefix"`
	FaviconPath                          string                      `json:"favicon_path"`
	ClientRoutes                         FrontendClientRoutesSchema  `json:"client_routes"`
	ProviderMetadataCacheTTLMilliseconds int                         `json:"provider_metadata_cache_ttl_milliseconds"`
	TaskHistoryRefreshMilliseconds       int                         `json:"task_history_refresh_milliseconds"`
	TaskHistoryDedupingMilliseconds      int                         `json:"task_history_deduping_milliseconds"`
	TaskHistoryRevalidateOnFocus         bool                        `json:"task_history_revalidate_on_focus"`
	TaskHistoryKeepPreviousData          bool                        `json:"task_history_keep_previous_data"`
	BalanceRefreshMilliseconds           int                         `json:"balance_refresh_milliseconds"`
	CacheStatusRefreshMilliseconds       int                         `json:"cache_status_refresh_milliseconds"`
	DefaultPromptMaxLength               int                         `json:"default_prompt_max_length"`
	DefaultReferenceImageMin             int                         `json:"default_reference_image_min"`
	DefaultReferenceImageMax             int                         `json:"default_reference_image_max"`
	ProductVersionRefreshMilliseconds    int                         `json:"product_version_refresh_milliseconds"`
	TaskSubmissionCooldownSeconds        int                         `json:"task_submission_cooldown_seconds"`
	TaskQueueMaximum                     int                         `json:"task_queue_maximum"`
	BatchDownloadIntervalMilliseconds    int                         `json:"batch_download_interval_milliseconds"`
	TaskIdentifierPreviewCharacters      int                         `json:"task_identifier_preview_characters"`
	CountdownTickMilliseconds            int                         `json:"countdown_tick_milliseconds"`
	TaskPollingIntervalMilliseconds      int                         `json:"task_polling_interval_milliseconds"`
	HarnessRefreshMilliseconds           int                         `json:"harness_refresh_milliseconds"`
	HarnessSaveDebounceMilliseconds      int                         `json:"harness_save_debounce_milliseconds"`
	// SessionMenuPageSize is how many sessions the top-bar switcher shows per
	// page; a page is requested only when the person turns to it. The debounce
	// delays the search request after typing.
	// The title refresh pair controls how long the page re-reads a session
	// whose automatic title is still pending after a turn completes.
	SessionMenuPageSize                     int                       `json:"session_menu_page_size"`
	SessionMenuSearchDebounceMilliseconds   int                       `json:"session_menu_search_debounce_milliseconds"`
	SessionTitleRefreshAttempts             int                       `json:"session_title_refresh_attempts"`
	SessionTitleRefreshIntervalMilliseconds int                       `json:"session_title_refresh_interval_milliseconds"`
	DisplayLocale                           string                    `json:"display_locale"`
	TaskAPIPrefix                           string                    `json:"task_api_prefix"`
	SpinnerFrameMilliseconds                int                       `json:"spinner_frame_milliseconds"`
	PercentageMultiplier                    float64                   `json:"percentage_multiplier"`
	PercentageSuffix                        string                    `json:"percentage_suffix"`
	AdvancedModelFormFeature                string                    `json:"advanced_model_form_feature"`
	AdvancedModelDefaultMode                string                    `json:"advanced_model_default_mode"`
	TaskProgress                            TaskProgressRuntimeSchema `json:"task_progress"`
	Toaster                                 ToasterRuntimeSchema      `json:"toaster"`
	// ChatMessagePageSize is how many messages the chat column reads when it
	// opens a conversation and each time the person asks for earlier ones.
	ChatMessagePageSize int `json:"chat_message_page_size"`
	// RetiredGenerationModels maps a generation model that is no longer
	// offered to the model that replaces it (#329). A workspace whose saved
	// or restored pick is a retired model switches to the successor and says
	// so; frozen tasks keep the model they ran with.
	RetiredGenerationModels []RetiredGenerationModel `json:"retired_generation_models,omitempty"`
}

// RetiredGenerationModel is one retired provider model and its successor.
type RetiredGenerationModel struct {
	ProviderID string `json:"provider_id"`
	ModelID    string `json:"model_id"`
	// ModelName is the retired Offerings' model_name, for the notice.
	ModelName        string `json:"model_name"`
	SuccessorModelID string `json:"successor_model_id"`
}

type ImageUploadSchema struct {
	MaximumPixels int64                   `json:"maximum_pixels"`
	Conversions   []ImageUploadConversion `json:"conversions"`
}

type ImageUploadConversion struct {
	SourceMediaTypes []string `json:"source_media_types"`
	SourceExtensions []string `json:"source_extensions"`
	TargetMediaType  string   `json:"target_media_type"`
}

type FrontendClientRoutesSchema struct {
	Account string `json:"account"`
	Home    string `json:"home"`
	Login   string `json:"login"`
}

type TaskProgressRuntimeSchema struct {
	FallbackState         string `json:"fallback_state"`
	InitialState          string `json:"initial_state"`
	NotFoundState         string `json:"not_found_state"`
	MillisecondsPerSecond int    `json:"milliseconds_per_second"`
	SecondsPerMinute      int    `json:"seconds_per_minute"`
	MinutesPerHour        int    `json:"minutes_per_hour"`
	HourDecimalPlaces     int    `json:"hour_decimal_places"`
}

type ToasterRuntimeSchema struct {
	Position   string `json:"position"`
	RichColors bool   `json:"rich_colors"`
}

// CapabilityPresentation is the public presentation of one capability. Icon is a lucide kebab-case name for
// its button; it is required for the processing capabilities listed in
// runtime.frontend.model_processing_capabilities, because the stage toolbar draws one button per available one.
type CapabilityPresentation struct {
	// WorkspaceModelSelector defaults to true; false reserves this capability
	// for a dedicated action instead of the generic domain model picker.
	WorkspaceModelSelector *bool                           `json:"workspace_model_selector,omitempty"`
	ID                     schemacontract.Capability       `json:"id"`
	Domain                 schemacontract.CapabilityDomain `json:"domain"`
	Label                  string                          `json:"label"`
	LabelCN                string                          `json:"label_cn"`
	Icon                   string                          `json:"icon,omitempty"`
	Order                  int                             `json:"order"`
	Workflow               *CapabilityWorkflowSchema       `json:"workflow,omitempty"`
	LegacyAPI              *LegacyTaskAPISchema            `json:"legacy_api,omitempty"`
	PromptComposition      *PromptCompositionSchema        `json:"prompt_composition,omitempty"`
	ImageResultGroup       *ImageResultGroupSchema         `json:"image_result_group,omitempty"`
}

// ImageResultGroupSchema gives image assets a stable UI order and human labels.
// AssetRequirement.Group remains a delivery dependency and is not used here.
type ImageResultGroupSchema struct {
	Slots            []ImageResultSlotSchema `json:"slots"`
	SourceSelectable bool                    `json:"source_selectable,omitempty"`
	// PartialMessage declares that a result may deliver only some slots (an
	// edit of selected views) and names the message shown with it. A group
	// without it is complete: a result missing slots is shown as plain images.
	PartialMessage string `json:"partial_message,omitempty"`
}

type ImageResultSlotSchema struct {
	Variant string `json:"variant"`
	Label   string `json:"label"`
}

type CapabilityWorkflowSchema struct {
	InitialInputMode  string `json:"initial_input_mode"`
	InitialPhase      string `json:"initial_phase,omitempty"`
	DefaultAutoRemesh *bool  `json:"default_auto_remesh,omitempty"`
}

type PromptCompositionSchema struct {
	MinimumReferences  int    `json:"minimum_references"`
	TemplateMessageKey string `json:"template_message_key"`
}

type LegacyTaskAPISchema struct {
	Path              string                         `json:"path"`
	ModelParameter    string                         `json:"model_parameter"`
	OutputKind        string                         `json:"output_kind"`
	Fields            []LegacyFieldSchema            `json:"fields,omitempty"`
	AssetAliases      []LegacyAssetAliasSchema       `json:"asset_aliases,omitempty"`
	TaskAliases       []LegacyTaskAliasSchema        `json:"task_aliases,omitempty"`
	Multipart         *LegacyMultipartTaskSchema     `json:"multipart,omitempty"`
	AssetDownload     *LegacyAssetDownloadSchema     `json:"asset_download,omitempty"`
	OutputProjections []LegacyOutputProjectionSchema `json:"output_projections"`
	ResultField       string                         `json:"result_field,omitempty"`
	ResultAssets      []LegacyResultAssetSchema      `json:"result_assets,omitempty"`
	ResultCollections []LegacyResultCollectionSchema `json:"result_collections,omitempty"`
}

// LegacyOutputProjectionSchema 声明平台 Asset 如何还原成旧前端响应字段。
// role 使用 "*" 时选择全部输出 Asset；mode 只描述集合形态，不包含产品内容。
type LegacyOutputProjectionSchema struct {
	Name         string                    `json:"name"`
	Role         string                    `json:"role"`
	Mode         string                    `json:"mode"`
	RecordSource string                    `json:"record_source,omitempty"`
	DefaultKey   string                    `json:"default_key,omitempty"`
	RecordFields []LegacyRecordFieldSchema `json:"record_fields,omitempty"`
}

// LegacyRecordFieldSchema 是 records 输出投影的小型数据映射 DSL。
// Transport 只认识稳定的数据来源，不认识 task_id/status 等产品字段名或字面量。
type LegacyRecordFieldSchema struct {
	Name     string          `json:"name"`
	Semantic string          `json:"semantic"`
	Source   string          `json:"source,omitempty"`
	Literal  json.RawMessage `json:"literal,omitempty"`
}

// LegacyMultipartTaskSchema 把旧接口 multipart 线协议投影到稳定任务输入。
// 字段名、目标参数与所有可见错误文本都由 Product Schema 提供；解析器只实现协议算法。
type LegacyMultipartTaskSchema struct {
	// Empty preserves the legacy capability-wide contract.
	OfferingIDs                []schemacontract.OfferingID `json:"offering_ids,omitempty"`
	FileField                  string                      `json:"file_field"`
	ParamsField                string                      `json:"params_field"`
	TargetParameter            string                      `json:"target_parameter"`
	Cardinality                string                      `json:"cardinality"`
	MissingFileMessageKey      string                      `json:"missing_file_message_key"`
	InvalidCountMessageKey     string                      `json:"invalid_count_message_key,omitempty"`
	InvalidExtensionMessageKey string                      `json:"invalid_extension_message_key"`
	InvalidParamsMessageKey    string                      `json:"invalid_params_message_key"`
}

// LegacyAssetDownloadSchema 描述只有一个不透明 Asset token 的旧版下载路由。
type LegacyAssetDownloadSchema struct {
	Path                  string `json:"path"`
	TokenParameter        string `json:"token_parameter"`
	FilenameTemplate      string `json:"filename_template"`
	MediaType             string `json:"media_type"`
	CacheSubdirectory     string `json:"cache_subdirectory"`
	CacheFilenameTemplate string `json:"cache_filename_template"`
	CacheHeader           string `json:"cache_header,omitempty"`
	CacheValue            string `json:"cache_value,omitempty"`
}

type LegacyFieldSchema struct {
	// Empty preserves the legacy capability-wide contract.
	OfferingIDs     []schemacontract.OfferingID `json:"offering_ids,omitempty"`
	Name            string                      `json:"name"`
	TargetParameter string                      `json:"target_parameter,omitempty"`
	Required        bool                        `json:"required,omitempty"`
	Restore         bool                        `json:"restore,omitempty"`
	Forwarding      string                      `json:"forwarding"`
}

type LegacyAssetAliasSchema struct {
	// Empty preserves the legacy capability-wide contract.
	OfferingIDs         []schemacontract.OfferingID      `json:"offering_ids,omitempty"`
	Name                string                           `json:"name"`
	Semantic            schemacontract.ParameterSemantic `json:"semantic"`
	Cardinality         string                           `json:"cardinality"`
	Restore             bool                             `json:"restore,omitempty"`
	RestoreMinimumItems int                              `json:"restore_minimum_items,omitempty"`
}

type LegacyTaskAliasSchema struct {
	// Empty preserves the legacy capability-wide contract.
	OfferingIDs     []schemacontract.OfferingID `json:"offering_ids,omitempty"`
	Name            string                      `json:"name"`
	TargetParameter string                      `json:"target_parameter"`
	Restore         bool                        `json:"restore,omitempty"`
}

// LegacyResultAssetSchema 描述落盘资产到旧接口结果字段的通用投影。
// Variant 是 Adapter 提供的稳定产物语义；media_types 只用于旧数据或测试
// Adapter 尚无 Variant 时的兼容回退，不承载 Provider 特判。
type LegacyResultAssetSchema struct {
	// Empty preserves the legacy capability-wide contract.
	OfferingIDs []schemacontract.OfferingID `json:"offering_ids,omitempty"`
	Name        string                      `json:"name"`
	Role        string                      `json:"role"`
	Variant     string                      `json:"variant,omitempty"`
	MediaTypes  []string                    `json:"media_types,omitempty"`
	// An explicit Offering alias uses this exact variant and never MIME fallback.
	OfferingVariants map[schemacontract.OfferingID]string `json:"offering_variants,omitempty"`
}

// LegacyResultCollectionSchema 将具有稳定 Variant 前缀的多个资产投影为
// 旧接口中的动态键对象，例如 basic_animations.{idle,walk}。
type LegacyResultCollectionSchema struct {
	// Empty preserves the legacy capability-wide contract.
	OfferingIDs   []schemacontract.OfferingID `json:"offering_ids,omitempty"`
	Name          string                      `json:"name"`
	Role          string                      `json:"role"`
	VariantPrefix string                      `json:"variant_prefix"`
}

type TaskStatePresentation struct {
	InternalState string                        `json:"internal_state"`
	PublicState   string                        `json:"public_state"`
	Label         string                        `json:"label"`
	Color         string                        `json:"color"`
	Terminal      bool                          `json:"terminal"`
	Outcome       string                        `json:"outcome"`
	Progress      TaskStateProgressPresentation `json:"progress"`
}

type TaskStateProgressPresentation struct {
	Mode            string `json:"mode"`
	FixedValue      int    `json:"fixed_value,omitempty"`
	ShowError       bool   `json:"show_error,omitempty"`
	ErrorColor      string `json:"error_color,omitempty"`
	RetryBadgeColor string `json:"retry_badge_color,omitempty"`
	TrackColor      string `json:"track_color,omitempty"`
	CountdownColor  string `json:"countdown_color,omitempty"`
}

type ValueLabel struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type AuthenticationSessionSchema struct {
	LegacyAcceptUntil string `json:"legacy_accept_until"`
}

type AuthenticationSchema struct {
	Session         AuthenticationSessionSchema `json:"session"`
	Cookie          SessionCookieSchema         `json:"cookie"`
	API             AuthenticationAPI           `json:"api"`
	PublicPaths     []string                    `json:"public_paths"`
	SessionRetry    SessionRetrySchema          `json:"session_retry"`
	Permissions     []PermissionSchema          `json:"permissions"`
	Roles           []RoleSchema                `json:"roles"`
	DefaultRole     string                      `json:"default_role"`
	AdminRole       string                      `json:"admin_role"`
	DirectoryErrors string                      `json:"directory_errors"`
	Messages        AuthenticationTexts         `json:"messages"`
	Login           LoginPresentation           `json:"login"`
}

// SessionRetrySchema is how the browser retries a failed session request
// (429, 5xx, network): exponential backoff from InitialDelayMilliseconds up to
// MaxBackoffMilliseconds, never shorter than the server's Retry-After, which is
// itself capped at MaxRetryAfterMilliseconds.
type SessionRetrySchema struct {
	InitialDelayMilliseconds  int `json:"initial_delay_ms"`
	MaxBackoffMilliseconds    int `json:"max_backoff_ms"`
	MaxRetryAfterMilliseconds int `json:"max_retry_after_ms"`
}

type SessionCookieSchema struct {
	Name       string `json:"name"`
	Format     string `json:"format"`
	TTLSeconds int    `json:"ttl_seconds"`
	HTTPOnly   bool   `json:"http_only"`
	SameSite   string `json:"same_site"`
	Path       string `json:"path"`
	SecureMode string `json:"secure_mode"`
}

type AuthenticationAPI struct {
	IDField                    string `json:"id_field"`
	UsernameField              string `json:"username_field"`
	PasswordField              string `json:"password_field"`
	NameField                  string `json:"name_field"`
	EmailField                 string `json:"email_field"`
	ErrorField                 string `json:"error_field"`
	SuccessField               string `json:"success_field"`
	UserField                  string `json:"user_field"`
	IsAdminField               string `json:"is_admin_field"`
	MockIDPrefix               string `json:"mock_id_prefix"`
	PasswordHashAlgorithm      string `json:"password_hash_algorithm"`
	PasswordHashCost           int    `json:"password_hash_cost"`
	SessionSecretMissingPolicy string `json:"session_secret_missing_policy"`
	LoginAuditAction           string `json:"login_audit_action"`
	LogoutAuditAction          string `json:"logout_audit_action"`
	AuditResourceType          string `json:"audit_resource_type"`
	// AuthJSONBodyMaxBytes 界定鉴权端点请求体上限（SEC-05）：鉴权端点
	// 未认证即可达，不得与画布等大体积端点共用全局上限。
	AuthJSONBodyMaxBytes int64 `json:"auth_json_body_max_bytes"`
	// LoginMaxAttemptsPerMinute/LoginLockoutSeconds 界定登录失败限流
	// （SEC-01）：按 IP+用户名 维度的滑动窗口与锁定时长。
	LoginMaxAttemptsPerMinute int `json:"login_max_attempts_per_minute"`
	LoginLockoutSeconds       int `json:"login_lockout_seconds"`
	// 自助改密的请求字段、审计动作与密码下限。
	CurrentPasswordField      string `json:"current_password_field"`
	NewPasswordField          string `json:"new_password_field"`
	PasswordChangeAuditAction string `json:"password_change_audit_action"`
	PasswordMinimumCharacters int    `json:"password_minimum_characters"`
	// Audit actions of the startup administrator bootstrap (#319): creating a
	// missing administrator and an ADMIN_PASSWORD_RESET. Empty skips the
	// audit event; the service log line is written either way.
	BootstrapAdminCreatedAuditAction       string `json:"bootstrap_admin_created_audit_action,omitempty"`
	BootstrapAdminPasswordResetAuditAction string `json:"bootstrap_admin_password_reset_audit_action,omitempty"`
}

type PermissionSchema struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type RoleSchema struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Permissions []string `json:"permissions"`
}

type AuthenticationTexts struct {
	Unauthenticated        string `json:"unauthenticated"`
	Forbidden              string `json:"forbidden"`
	UserDeactivated        string `json:"user_deactivated"`
	MissingCredentials     string `json:"missing_credentials"`
	InvalidCredentials     string `json:"invalid_credentials"`
	PasswordChanged        string `json:"password_changed"`
	InvalidCurrentPassword string `json:"invalid_current_password"`
	WeakPassword           string `json:"weak_password"`
	LoginFailed            string `json:"login_failed"`
	RateLimited            string `json:"rate_limited"`
	NetworkError           string `json:"network_error"`
	InternalError          string `json:"internal_error"`
	MockDisabled           string `json:"mock_disabled"`
	MockUserMissing        string `json:"mock_user_missing"`
	MockUserInvalid        string `json:"mock_user_invalid"`
}

type LoginPresentation struct {
	Title               string `json:"title"`
	Description         string `json:"description"`
	UsernameLabel       string `json:"username_label"`
	UsernamePlaceholder string `json:"username_placeholder"`
	PasswordLabel       string `json:"password_label"`
	PasswordPlaceholder string `json:"password_placeholder"`
	SubmitLabel         string `json:"submit_label"`
	SubmittingLabel     string `json:"submitting_label"`
	ShowPassword        string `json:"show_password"`
	HidePassword        string `json:"hide_password"`
	LoadingLabel        string `json:"loading_label"`
	DefaultRedirect     string `json:"default_redirect"`
	// RedirectQuery names the login page query parameter that carries the
	// same-origin path to open after signing in.
	RedirectQuery string `json:"redirect_query"`
}

type rawOffering struct {
	NativePricing                 *schemacontract.NativePricing   `json:"native_pricing,omitempty"`
	ID                            schemacontract.OfferingID       `json:"id"`
	Revision                      string                          `json:"revision"`
	ProviderID                    string                          `json:"provider_id"`
	ProviderName                  string                          `json:"provider_name"`
	ProviderShortLabel            string                          `json:"provider_short_label"`
	ProviderOrder                 *int                            `json:"provider_order,omitempty"`
	ModelID                       string                          `json:"model_id"`
	ModelName                     string                          `json:"model_name"`
	Capability                    schemacontract.Capability       `json:"capability"`
	Domain                        schemacontract.CapabilityDomain `json:"domain"`
	AdapterKey                    string                          `json:"adapter_key"`
	Enabled                       bool                            `json:"enabled"`
	Features                      []string                        `json:"features"`
	Default                       bool                            `json:"default"`
	Recommended                   bool                            `json:"recommended"`
	DisplayOrder                  *int                            `json:"display_order"`
	CapabilityOrder               *int                            `json:"capability_order"`
	Priority                      int                             `json:"priority"`
	EstimatedCredits              int                             `json:"estimated_credits"`
	CreditsMetered                bool                            `json:"credits_metered"`
	EstimatedCreditsUnitParameter string                          `json:"estimated_credits_unit_parameter,omitempty"`
	ReferenceSelectionPolicy      string                          `json:"reference_selection_policy,omitempty"`
	IdempotentSubmission          bool                            `json:"idempotent_submission"`
	CancellationSupported         bool                            `json:"cancellation_supported"`
	TimeoutSeconds                int                             `json:"timeout_seconds"`
	// JobTimeoutSeconds 声明作业级总时长（attempt 从启动起的上限）；
	// 0 = 继承 timeout_seconds（B17：request/job 超时拆分）。
	JobTimeoutSeconds   int                               `json:"job_timeout_seconds,omitempty"`
	PollIntervalSeconds int                               `json:"poll_interval_seconds"`
	Retry               rawRetry                          `json:"retry"`
	MaterializeAttempts int                               `json:"materialize_attempts"`
	Parameters          []schemacontract.Parameter        `json:"parameters"`
	InputRules          []schemacontract.InputRule        `json:"input_rules"`
	Outputs             []schemacontract.AssetRequirement `json:"outputs"`
	AssetHosts          []string                          `json:"asset_hosts"`
	ProviderOptions     json.RawMessage                   `json:"provider_options"`
	Cost                *schemacontract.CostDeclaration   `json:"cost"`
	CostPolicy          *schemacontract.CostPolicy        `json:"cost_policy"`
	PostSuccess         *schemacontract.PostSuccessPolicy `json:"post_success"`
}

type rawRetry struct {
	MaxAttempts           int                           `json:"max_attempts"`
	InitialBackoffSeconds int                           `json:"initial_backoff_seconds"`
	MaxBackoffSeconds     int                           `json:"max_backoff_seconds"`
	Retryable             []schemacontract.FailureClass `json:"retryable"`
}

// ChatVisionDerivativeSchema declares the vision copy of a picture whose bytes
// exceed vision_inline_max_bytes (#292). The picture is decoded, scaled so its
// long side fits each step in turn, and encoded (PNG while transparency
// matters and it fits, else JPEG over FlattenColor at each quality) until the
// result is within vision_inline_max_bytes. It is used for the LLM's vision
// context only; generation references keep the no-resize fitting of #276.
type ChatVisionDerivativeSchema struct {
	LongSideSteps     []int  `json:"long_side_steps"`
	JPEGQualities     []int  `json:"jpeg_qualities"`
	FlattenColor      string `json:"flatten_color"`
	MaximumInputBytes int64  `json:"maximum_input_bytes"`
	MaximumPixels     int64  `json:"maximum_pixels"`
	DecodeConcurrency int    `json:"decode_concurrency"`
	// CacheMaximumBytes bounds the in-memory cache of made copies, keyed by
	// the source's SHA-256 and the policy, so later turns do not re-encode.
	CacheMaximumBytes int64 `json:"cache_maximum_bytes"`
}
