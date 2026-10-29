package generation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type TaskID string
type AttemptID string
type AssetID string
type OwnerID string
type OfferingID = schema.OfferingID
type NativeQuote = schema.NativeQuote
type NativeAmount = schema.NativeAmount

// B5 契约解耦：叶子词表迁入 internal/schema，此处别名再导出保持
// 既有调用面（generation.X = schema.X，同一底层类型）。
type Capability = schema.Capability

const (
	TextToImage      = schema.TextToImage
	ImageToImage     = schema.ImageToImage
	TextTo3D         = schema.TextTo3D
	ImageTo3D        = schema.ImageTo3D
	ImageToMultiview = schema.ImageToMultiview
	EditMultiview    = schema.EditMultiview
	Remesh           = schema.Remesh
	Retexture        = schema.Retexture
	Rigging          = schema.Rigging
	Animation        = schema.Animation
	BackgroundRemove = schema.BackgroundRemove
	FormatConversion = schema.FormatConversion
)

type CapabilityDomain = schema.CapabilityDomain

const (
	DomainImage      = schema.DomainImage
	DomainModel3D    = schema.DomainModel3D
	DomainProcessing = schema.DomainProcessing
)

type TaskState = schema.TaskState

const (
	TaskQueued        = schema.TaskQueued
	TaskRunning       = schema.TaskRunning
	TaskMaterializing = schema.TaskMaterializing
	TaskSucceeded     = schema.TaskSucceeded
	TaskFailed        = schema.TaskFailed
	TaskCanceled      = schema.TaskCanceled
)

type AttemptState string

const (
	AttemptQueued            AttemptState = "QUEUED"
	AttemptSubmitting        AttemptState = "SUBMITTING"
	AttemptRunning           AttemptState = "RUNNING"
	AttemptUpstreamSucceeded AttemptState = "UPSTREAM_SUCCEEDED"
	AttemptFailed            AttemptState = "FAILED"
	AttemptSubmissionUnknown AttemptState = "SUBMISSION_UNKNOWN"
	AttemptCanceled          AttemptState = "CANCELED"
	// AttemptDeliveryCompleted is a local terminal Attempt (#330): the
	// failed Task's own bytes were validated and registered from a related
	// delivered conversion. It has no Job, Opaque, dispatch or observation.
	AttemptDeliveryCompleted AttemptState = "DELIVERY_COMPLETED"
)

type UpstreamPhase string

const (
	// UpstreamPreparing persists a private prerequisite receipt before a real Job exists.
	UpstreamPreparing UpstreamPhase = "PREPARING"
	UpstreamAccepted  UpstreamPhase = "ACCEPTED"
	UpstreamRunning   UpstreamPhase = "RUNNING"
	UpstreamCompleted UpstreamPhase = "COMPLETED"
	UpstreamFailed    UpstreamPhase = "FAILED"
	UpstreamCanceled  UpstreamPhase = "CANCELED"
)

type FailureClass = schema.FailureClass

const (
	FailureInvalidInput        = schema.FailureInvalidInput
	FailureAuthentication      = schema.FailureAuthentication
	FailureRateLimited         = schema.FailureRateLimited
	FailureTransport           = schema.FailureTransport
	FailureTimedOut            = schema.FailureTimedOut
	FailureRejected            = schema.FailureRejected
	FailureInsufficientBalance = schema.FailureInsufficientBalance
	FailureCredentialQuota     = schema.FailureCredentialQuota
	FailureUpstreamUnavailable = schema.FailureUpstreamUnavailable
	FailureSubmissionUnknown   = schema.FailureSubmissionUnknown
	FailureIntegrity           = schema.FailureIntegrity
	FailureProtocol            = schema.FailureProtocol
	FailureInternal            = schema.FailureInternal
	FailureSourceExpired       = schema.FailureSourceExpired
)

type Failure struct {
	Class      FailureClass `json:"class"`
	Retryable  bool         `json:"retryable"`
	Ambiguous  bool         `json:"ambiguous,omitempty"`
	SafeDetail string       `json:"detail"`
	// UpstreamStatus/UpstreamCode/UpstreamExcerpt 是内部取证字段：仅落库、
	// 日志与管理侧诊断使用，禁止进入面向用户的文案投影。
	UpstreamStatus  int           `json:"upstream_status,omitempty"`
	UpstreamCode    string        `json:"upstream_code,omitempty"`
	UpstreamExcerpt string        `json:"upstream_excerpt,omitempty"`
	RetryAfter      time.Duration `json:"-"`
}

func (f *Failure) Error() string {
	if f == nil {
		return ""
	}
	return f.SafeDetail
}

type RetryPolicy = schema.RetryPolicy

type PresentationSurface = schema.PresentationSurface

const (
	SurfaceTask     = schema.SurfaceTask
	SurfaceProvider = schema.SurfaceProvider
	SurfaceHidden   = schema.SurfaceHidden
	SurfaceReadOnly = schema.SurfaceReadOnly
)

type PresentationControl = schema.PresentationControl

const (
	ControlSelect      = schema.ControlSelect
	ControlText        = schema.ControlText
	ControlBoolean     = schema.ControlBoolean
	ControlNumber      = schema.ControlNumber
	ControlSlider      = schema.ControlSlider
	ControlAsset       = schema.ControlAsset
	ControlMultiSelect = schema.ControlMultiSelect
)

type ParameterPresentation = schema.ParameterPresentation

type ParameterCondition = schema.ParameterCondition
type ParameterNotice = schema.ParameterNotice

type ParameterSemantic = schema.ParameterSemantic

const (
	SemanticPrompt         = schema.SemanticPrompt
	SemanticMask           = schema.SemanticMask
	SemanticNegativePrompt = schema.SemanticNegativePrompt
	SemanticReferenceImage = schema.SemanticReferenceImage
	SemanticPolycount      = schema.SemanticPolycount
	SemanticOutputSize     = schema.SemanticOutputSize
	SemanticQuality        = schema.SemanticQuality

	SemanticSourceTask  = schema.SemanticSourceTask
	SemanticSourceAsset = schema.SemanticSourceAsset
	SemanticSourceURL   = schema.SemanticSourceURL
	SemanticAction      = schema.SemanticAction
)

type ImageSizeConstraint = schema.ImageSizeConstraint

type Parameter = schema.Parameter

type ParameterOption = schema.ParameterOption

// 资源交付类别与产物槽位契约（B5 迁入 internal/schema）。
type AssetRequirement = schema.AssetRequirement

const (
	AssetDeliveryRequired   = schema.AssetDeliveryRequired
	AssetDeliveryBackground = schema.AssetDeliveryBackground
	AssetDeliveryOnDemand   = schema.AssetDeliveryOnDemand
	VariantCaseVerbatim     = schema.VariantCaseVerbatim
	VariantCaseLower        = schema.VariantCaseLower
	VariantCaseUpper        = schema.VariantCaseUpper
)

// SelectionVariant 按声明从选中值派生逐项槽位的变体（唯一实现：内核槽位
// 展开与 Provider 产物/断言身份共用）。未声明 VariantPrefix 时返回空串
// （不可派生 → 调用方按未声明处理）。
func SelectionVariant(requirement AssetRequirement, value string) string {
	if requirement.VariantPrefix == "" || value == "" {
		return ""
	}
	switch requirement.VariantCase {
	case VariantCaseLower:
		return requirement.VariantPrefix + strings.ToLower(value)
	case VariantCaseUpper:
		return requirement.VariantPrefix + strings.ToUpper(value)
	default:
		return requirement.VariantPrefix + value
	}
}

type PostSuccessPolicy = schema.PostSuccessPolicy

// 0022 资源态（内核所有，§6.5）。状态机：pending → ready（合并落地）|
// incomplete（deadline finalizer）| failed/denied/abandoned（Provider 经
// Observation.ResourceUpdates 断言，内核只做通用合法性校验，不解释
// Provider 私有语义）；终态资源态不可逆。
type ResourceStateCode string

const (
	ResourcePending    ResourceStateCode = "pending"
	ResourceReady      ResourceStateCode = "ready"
	ResourceIncomplete ResourceStateCode = "incomplete"
	ResourceFailed     ResourceStateCode = "failed"
	ResourceDenied     ResourceStateCode = "denied"
	ResourceAbandoned  ResourceStateCode = "abandoned"
)

// ResourceState 是单个声明槽位的持久状态；Reason 是文案键（公开投影只
// 输出解析后的安全文本，不泄漏原始键调度语义），Deadline 为 RFC3339。
type ResourceState struct {
	State    ResourceStateCode `json:"state"`
	Reason   string            `json:"reason,omitempty"`
	Deadline time.Time         `json:"deadline,omitempty"`
}

// ResourceUpdate 是 Provider 对非必需资源槽位的通用状态断言（有限枚举，
// 无 per-format 语义）：目标按 (Role, Variant 或 VariantPrefix) 指向
// 冻结快照声明的 background/on-demand 槽位；失败族（failed/denied/
// abandoned）是 Provider 可独立断言的降级事实；ready 只是"上游工作已
// 完成"的声明——内核仍要求已提交的本地持久资产证据才落 READY（回顾性
// 整改 P1：上游完成 ≠ 本地 READY），无证据时保守忽略（槽位保持 pending，
// 由后续轮次或 deadline finalizer 依据资产事实收尾）。内核校验声明存在
// 与 pending → 终态的单向转移后落库。
type ResourceUpdate struct {
	Role          string            `json:"role"`
	Variant       string            `json:"variant,omitempty"`
	VariantPrefix string            `json:"variant_prefix,omitempty"`
	State         ResourceStateCode `json:"state"`
	Reason        string            `json:"reason,omitempty"`
}

// ResourceSlotState 是成功后观察期内核持有的公开资源槽位身份与状态
// （§6.5 通用键；回顾性整改 P1/P2）。只读地告知 Provider 哪些期望项仍未
// 兑现，使其能只对未决槽位继续私有工作（重发产物声明/刷新签名定位符），
// 而不必猜测内核是否已落盘；不含任何 Provider 私有语义，也不允许
// Provider 反向驱动内核状态（断言仍只经 Observation.ResourceUpdates）。
type ResourceSlotState struct {
	Role          string            `json:"role"`
	Variant       string            `json:"variant,omitempty"`
	VariantPrefix string            `json:"variant_prefix,omitempty"`
	Group         string            `json:"group,omitempty"`
	State         ResourceStateCode `json:"state"`
}

type InputRule = schema.InputRule

type Offering = schema.Offering

type CostDeclaration = schema.CostDeclaration

type CostPolicy = schema.CostPolicy

type CostPolicyMessages = schema.CostPolicyMessages

type ExecutionMessages = schema.ExecutionMessages

type CancellationPolicy = schema.CancellationPolicy

type Task struct {
	NativeQuote    *NativeQuote    `json:"native_quote,omitempty"`
	ID             TaskID          `json:"id"`
	OwnerID        OwnerID         `json:"owner_id"`
	SessionID      string          `json:"session_id,omitempty"`
	Capability     Capability      `json:"capability"`
	Offering       Offering        `json:"offering"`
	Input          json.RawMessage `json:"input"`
	RequestHash    string          `json:"-"`
	IdempotencyKey string          `json:"-"`
	// Admission is a server-constructed, single-operation grant reference.
	// The repository verifies it against its persisted human decision and the
	// frozen PreparedInput in the same transaction that creates this Task.
	Admission *ExecutionAdmission `json:"-"`
	// Conversion names the declared paid format conversion this new Task runs,
	// when the frozen Offering and input match one. It is not persisted: the
	// repository re-reads each stored Task through the same Schema rule. A
	// guarded repository refuses a new conversion whose exact source and
	// target formats already belong to an active Task of the same owner and
	// session (#330).
	Conversion          *ConversionIntent `json:"-"`
	State               TaskState         `json:"state"`
	Progress            int               `json:"progress"`
	Failure             *Failure          `json:"failure,omitempty"`
	CurrentAttemptID    AttemptID         `json:"-"`
	MaterializeFailures int               `json:"-"`
	NextActionAt        time.Time         `json:"-"`
	Version             int64             `json:"version"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
	FinishedAt          *time.Time        `json:"finished_at,omitempty"`
	Assets              []Asset           `json:"assets"`
	ProviderData        json.RawMessage   `json:"-"`
	// Resources 是 0022 资源态（generation_tasks.resource_state_json，
	// §6.5）：nil = 无成功后观察（旧任务/未声明 observe）。内核所有，
	// 不进入任务 JSON 序列化，公开面只走 resources[] 投影。
	Resources map[string]ResourceState `json:"-"`
	// UpstreamJobCreatedAt is when the Task that holds this Task's upstream
	// job was created, when that is another Task (#326: a #295 refetch is
	// delivered by its failed source's job); zero means CreatedAt. Read
	// only, filled by the kernel's task reads.
	UpstreamJobCreatedAt time.Time `json:"-"`
	// UpstreamJobGone: a later step naming this Task's upstream job failed
	// because the Provider no longer keeps it (source_expired, #326). Read
	// only, filled by the kernel's task reads.
	UpstreamJobGone bool `json:"-"`
	// DeliveryCompleted reports that the current Attempt is a local delivery
	// completion (#330). Read only; ordinary execution never sets it.
	DeliveryCompleted bool `json:"-"`
}

// UpstreamJobFacts are the read-only facts about a Task's upstream job that
// the age rule needs (#326).
type UpstreamJobFacts struct {
	CreatedAt time.Time
	Gone      bool
}

// UpstreamJobFactsReader is implemented by the durable repository: for each
// listed Task of owner, when the Task holding its upstream job was created
// (a refetch's source) and whether a step naming it failed as
// source_expired. Tasks with neither are left out.
type UpstreamJobFactsReader interface {
	UpstreamJobFacts(ctx context.Context, owner OwnerID, ids []TaskID) (map[TaskID]UpstreamJobFacts, error)
}

type Attempt struct {
	ID                      AttemptID       `json:"id"`
	TaskID                  TaskID          `json:"task_id"`
	Ordinal                 int             `json:"ordinal"`
	Offering                Offering        `json:"offering"`
	State                   AttemptState    `json:"state"`
	SubmissionKey           string          `json:"submission_key"`
	UpstreamJob             *UpstreamJob    `json:"upstream_job,omitempty"`
	Failure                 *Failure        `json:"failure,omitempty"`
	Opaque                  json.RawMessage `json:"opaque,omitempty"`
	LastObservationKey      string          `json:"-"`
	ObservationFailures     int             `json:"-"`
	LastObservationSequence int64           `json:"-"`
	LastObservationAt       time.Time       `json:"-"`
	LastObservationPhase    UpstreamPhase   `json:"-"`
	StartedAt               time.Time       `json:"started_at"`
	UpdatedAt               time.Time       `json:"updated_at"`
	FinishedAt              *time.Time      `json:"finished_at,omitempty"`
	// DeliveryCompletion is the provenance of a local terminal Attempt that
	// completed a failed conversion from an exactly related delivered Task.
	// Ordinary Attempts leave it nil.
	DeliveryCompletion *DeliveryCompletion `json:"delivery_completion,omitempty"`
}

// Delivery completion relation kinds.
const (
	DeliveryCompletionRefetch   = "refetch"
	DeliveryCompletionPlanRetry = "plan_retry"
)

// DeliveryCompletion is the immutable provenance of one local completion of a
// failed conversion Task. It records only the facts needed to audit the
// relation, the original failure and charge, and the bytes registered.
type DeliveryCompletion struct {
	Kind                 string                 `json:"kind"`
	Refetch              *DeliveryRefetch       `json:"refetch,omitempty"`
	PlanRetry            *DeliveryPlanRetry     `json:"plan_retry,omitempty"`
	OriginalAttemptID    AttemptID              `json:"original_attempt_id"`
	DeliveryTaskID       TaskID                 `json:"delivery_task_id"`
	DeliveryAttemptID    AttemptID              `json:"delivery_attempt_id"`
	OriginalFailureClass FailureClass           `json:"original_failure_class"`
	OriginalFinishedAt   time.Time              `json:"original_finished_at"`
	ChargedCredits       int                    `json:"charged_credits,omitempty"`
	CreditsMetered       bool                   `json:"credits_metered,omitempty"`
	ContractSHA256       string                 `json:"contract_sha256"`
	Assets               []DeliveryAssetMapping `json:"assets"`
}

type DeliveryRefetch struct {
	Ordinal         int       `json:"ordinal"`
	SourceAttemptID AttemptID `json:"source_attempt_id"`
}

type DeliveryPlanRetry struct {
	PlanID            string          `json:"plan_id"`
	StepIndex         int             `json:"step_index"`
	Retry             int             `json:"retry"`
	GrantRequestID    string          `json:"grant_request_id"`
	PreparedInputID   PreparedInputID `json:"prepared_input_id"`
	PreparedInputHash string          `json:"prepared_input_hash"`
}

type DeliveryAssetMapping struct {
	SourceAssetID AssetID `json:"source_asset_id"`
	AssetID       AssetID `json:"asset_id"`
}

type UpstreamJob struct {
	ProviderID string `json:"provider_id"`
	RemoteID   string `json:"remote_id"`
}

type ArtifactClaim struct {
	StoredAsset *Asset          `json:"-"`
	ID          string          `json:"id"`
	Role        string          `json:"role"`
	Variant     string          `json:"variant,omitempty"`
	Locator     string          `json:"locator,omitempty"`
	Inline      []byte          `json:"-"`
	MediaType   string          `json:"media_type,omitempty"`
	ExpiresAt   *time.Time      `json:"expires_at,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}

type Asset struct {
	DeliveryProfile string    `json:"delivery_profile,omitempty"`
	ID              AssetID   `json:"id"`
	TaskID          TaskID    `json:"task_id"`
	AttemptID       AttemptID `json:"attempt_id"`
	Role            string    `json:"role"`
	Variant         string    `json:"variant,omitempty"`
	Required        bool      `json:"required"`
	Ordinal         int       `json:"ordinal"`
	StorageKey      string    `json:"storage_key"`
	MediaType       string    `json:"media_type"`
	Size            int64     `json:"size"`
	SHA256          string    `json:"sha256"`
	CreatedAt       time.Time `json:"created_at"`
}

// DispatchReceipt 是 Provider 对一次私有派发检查点的回执：回带当次授予的
// sequence/ack，向内核声明该次许可的实际派发结果。只在获得 ack 的同一调用
// 栈内有效；重复、伪造或陈旧回执由内核保守丢弃（§4.3）。
type DispatchReceipt struct {
	Sequence int64  `json:"sequence"`
	AckToken string `json:"ack_token"`
	Result   string `json:"result"` // "dispatched" | "not_dispatched"
}

// 私有派发回执结果（有限枚举，非 per-provider 语义）。
const (
	DispatchResultDispatched    = "dispatched"
	DispatchResultNotDispatched = "not_dispatched"
)

type Observation struct {
	// CheckpointBaseSHA256 opts a leased polling response into a private-only
	// checkpoint update at unchanged phase/progress. It binds the update to the
	// exact Opaque received by the adapter and is never a public wire field.
	CheckpointBaseSHA256 string `json:"-"`

	Sequence   int64            `json:"sequence,omitempty"`
	ObservedAt time.Time        `json:"observed_at,omitempty"`
	Phase      UpstreamPhase    `json:"phase"`
	Job        *UpstreamJob     `json:"job,omitempty"`
	Progress   int              `json:"progress,omitempty"`
	Artifacts  []ArtifactClaim  `json:"artifacts,omitempty"`
	Failure    *Failure         `json:"failure,omitempty"`
	PollAfter  time.Duration    `json:"-"`
	DedupeKey  string           `json:"dedupe_key,omitempty"`
	Opaque     json.RawMessage  `json:"opaque,omitempty"`
	Dispatch   *DispatchReceipt `json:"dispatch,omitempty"`
	// ResourceUpdates 是 Provider 对非必需资源槽位的通用状态断言
	//（§6.2 触点 3 的唯一 Provider → 资源态通道；W6）。内核不解释
	// 内容，只校验声明匹配与 pending → 终态的单向转移。
	ResourceUpdates []ResourceUpdate `json:"resources,omitempty"`
}

type SubmitCommand struct {
	OwnerID        OwnerID
	SessionID      string
	Capability     Capability
	OfferingID     OfferingID
	Input          json.RawMessage
	IdempotencyKey string
	Admission      *ExecutionAdmission
}

type ExecutionAdmission struct {
	Harness           harness.Binding `json:"harness,omitempty"`
	RequestID         string
	PreparedInputID   PreparedInputID
	PreparedInputHash string
	OperationID       string
	PlanStepID        string
}

type TaskDeletionPolicy struct {
	LocalRecordPolicy     string
	UpstreamPolicy        string
	UpstreamFailurePolicy string
	GeneratedAssetPolicy  string
	AuditEventType        string
}

type DeleteCommand struct {
	Scope              TaskListScope
	ActorID            OwnerID
	TaskID             TaskID
	ExpectedCapability Capability
	Policy             TaskDeletionPolicy
}

type Accepted struct {
	TaskID   TaskID `json:"task_id"`
	Replayed bool   `json:"replayed"`
}

type ListQuery struct {
	SessionID      string
	States         []TaskState
	ExcludedStates []TaskState
	// OfferingIDs restricts the page to Tasks whose frozen Offering is one of
	// these. The format projection reads only declared conversions, so a long
	// session is not scanned Task by Task.
	OfferingIDs []OfferingID
	Capability  Capability
	SearchID    string
	Cursor      string
	Offset      int
	Limit       int
	ActiveFirst bool
}

type TaskPage struct {
	Tasks      []Task `json:"tasks"`
	Total      int    `json:"total"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type TaskListScope struct {
	ownerID   OwnerID
	allOwners bool
}

func OwnTasks(owner OwnerID) TaskListScope {
	return TaskListScope{ownerID: owner}
}

func AllTasks() TaskListScope {
	return TaskListScope{allOwners: true}
}

func (scope TaskListScope) OwnerID() OwnerID {
	return scope.ownerID
}

func (scope TaskListScope) AllOwners() bool {
	return scope.allOwners
}

type Event struct {
	Sequence  int64           `json:"sequence"`
	TaskID    TaskID          `json:"task_id"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

type ErrorCode = schema.ErrorCode

const (
	ErrInvalidCapability     = schema.ErrInvalidCapability
	ErrInvalidInput          = schema.ErrInvalidInput
	ErrInvalidModelInput     = schema.ErrInvalidModelInput
	ErrUnsupportedModelInput = schema.ErrUnsupportedModelInput
	ErrModelInputLimit       = schema.ErrModelInputLimit
	ErrUnknownOffering       = schema.ErrUnknownOffering
	ErrNoOffering            = schema.ErrNoOffering
	ErrIdempotencyConflict   = schema.ErrIdempotencyConflict
	ErrNotFound              = schema.ErrNotFound
	ErrForbidden             = schema.ErrForbidden
	ErrConflict              = schema.ErrConflict
	ErrUnavailable           = schema.ErrUnavailable
	ErrTooManyRequests       = schema.ErrTooManyRequests
	ErrInternal              = schema.ErrInternal
)

type Error = schema.Error

func IsCode(err error, code ErrorCode) bool { return schema.IsCode(err, code) }

// UserFacingDetail is the Schema-sourced reason a refusal carries for the
// requester, or "" (#288). See schema.Error.UserFacing.
func UserFacingDetail(err error) string { return schema.UserFacingDetail(err) }

// UserFacingError is the refusal in err's chain that carries that reason, or
// nil; its LocalizedDetail writes the reason in the requester's language.
func UserFacingError(err error) *Error { return schema.UserFacingError(err) }

type StartRequest struct {
	SessionID        string
	OwnerID          OwnerID
	TaskID           TaskID
	AttemptID        AttemptID
	SubmissionKey    string
	Offering         Offering
	Input            json.RawMessage
	InputAssets      map[string][]InputAsset
	TaskDependencies map[string]TaskDependency
}

// InputValidationRequest contains only already resolved, owned inputs. All
// fields are read-only. Validation may inspect those local assets, but must not
// submit work, request a dispatch permit, mutate a Task, or contact upstream.
type InputValidationRequest struct {
	SessionID        string
	OwnerID          OwnerID
	Offering         Offering
	Input            json.RawMessage
	InputAssets      map[string][]InputAsset
	TaskDependencies map[string]TaskDependency
}

// ProviderInputValidator is an optional pre-admission validation capability.
// Providers without it retain the existing preparation and submission contract.
type ProviderInputValidator interface {
	ValidateInput(context.Context, InputValidationRequest) error
}

// PreparationRequest resumes the frozen private preparation checkpoint. It is
// not a new Task submission and cannot change the admitted input or Offering.
type PreparationRequest struct {
	TaskID        TaskID
	AttemptID     AttemptID
	SubmissionKey string
	OwnerID       OwnerID
	Offering      Offering
	Input         json.RawMessage
	Opaque        json.RawMessage
}

// ProviderPreparationResumer is optional. Only a leased PREPARING Attempt with
// no committed Job can call it; once a Job is committed normal Observe resumes.
type ProviderPreparationResumer interface {
	ResumePreparation(context.Context, PreparationRequest) (Observation, error)
}

type TaskDependency struct {
	// SessionID is populated only for an explicit same_session source constraint.
	SessionID       string
	ProviderBinding json.RawMessage
	TaskID          TaskID
	Capability      Capability
	OfferingID      OfferingID
	ProviderID      string
	ModelID         string
	// UpstreamJob references the delivered result, not necessarily the source submission.
	UpstreamJob UpstreamJob
	Assets      []Asset
}

// ErrWebhookNotApplicable 由 Provider 的 DecodeWebhook 返回，表示该载荷
// 不能单独推进此任务（例如多阶段私有子作业只由轮询驱动）。内核确认收到
// 并丢弃它，不改任务状态，也不当作非法载荷。
var ErrWebhookNotApplicable = errors.New("provider webhook not applicable")

type ObserveRequest struct {
	TaskID    TaskID
	AttemptID AttemptID
	Offering  Offering
	Job       UpstreamJob
	Opaque    json.RawMessage
	// Input 是冻结的任务输入（W6）：成功后观察期的 Provider 私有规划器
	// 需要读取冻结的同意选择（如 output_formats）。加性字段；不需要
	// 输入的 Provider 忽略即可。
	Input json.RawMessage
	// PostSuccess 标记本次观察来自独立的成功后观察入口（observePostSuccess，
	// §6.2 触点 2）：父任务已终态，Provider 只可能推进可选资源/私有子
	// 转换，绝不能把父任务当运行中处理。
	PostSuccess bool
	// ResourceSlots 是内核当前持有的公开资源槽位身份与状态（仅成功后
	// 观察期填充；回顾性整改 P1/P2）。Provider 只读消费：据此判断哪些
	// 期望项仍未兑现（例如上游已完成但本地下载失败的转换产物），只对
	// 未决槽位继续私有工作；不得据此回写内核状态。
	ResourceSlots []ResourceSlotState
}

type WebhookDecodeRequest struct {
	TaskID    TaskID
	AttemptID AttemptID
	Offering  Offering
	Job       UpstreamJob
	Payload   []byte
}

type OpenAssetRequest struct {
	TaskID    TaskID
	AttemptID AttemptID
	Offering  Offering
	Job       *UpstreamJob
	Claim     ArtifactClaim
}

type DeleteProviderRequest struct {
	TaskID    TaskID
	AttemptID AttemptID
	Offering  Offering
	Job       UpstreamJob
}

type AssetMetadata struct {
	MediaType string
	Size      int64
}

type InputAsset struct {
	ID               AssetID
	OwnerID          OwnerID
	StorageKey       string
	OriginalFilename string
	AssetType        string
	MediaType        string
	Size             int64
	SHA256           string
	CreatedAt        time.Time
	// Role and Variant identify an output projected from a successful Task.
	// Direct uploads leave both empty. Consumers of model-source parameters
	// use Role to keep non-model artifacts (splat, texture, converted files)
	// out of model inputs.
	Role    string
	Variant string
}

type InputAssetUpload struct {
	Body             io.Reader
	OriginalFilename string
	AssetType        string
	MediaType        string
	MaxBytes         int64
}

// InputAssetDerivation records how a derived input asset was made from its
// source (#276: an image re-encoded to fit an Offering's limit). The source
// asset is never changed.
type InputAssetDerivation struct {
	SourceAssetID AssetID
	Method        string
	SourceSize    int64
}

// InputFitter rewrites a normalized input before it is resolved so each input
// image fits its Offering parameter, replacing an image that does not with a
// derived input asset of the same width and height. It returns the input
// unchanged when nothing needs fitting, and a domain ErrInvalidInput naming
// the sizes when an image cannot fit. The same source and limits always yield
// the same derived asset, so a repeated submission is idempotent.
type InputFitter interface {
	Fit(context.Context, OwnerID, Offering, json.RawMessage) (json.RawMessage, error)
}

type InputAssetResolver interface {
	Resolve(
		context.Context,
		OwnerID,
		Offering,
		json.RawMessage,
	) (map[string][]InputAsset, error)
	Open(context.Context, InputAsset) (io.ReadCloser, error)
}

// AcceptedTaskInputResolver may preserve the input contract of an already
// persisted Task when new submissions use a narrower contract. Only the
// coordinator calls it with the leased Task; PrepareInput and SubmitPrepared
// always use InputAssetResolver.Resolve. It does not grant execution admission.
type AcceptedTaskInputResolver interface {
	ResolveAccepted(context.Context, Task) (map[string][]InputAsset, error)
}

type InputAssetPublisher interface {
	ExternalURL(context.Context, InputAsset, time.Duration) (string, error)
}

type InputAssetRepository interface {
	CreateInputAsset(context.Context, InputAsset) error
	GetInputAsset(context.Context, AssetID) (InputAsset, error)
	LoadInputAssets(context.Context, OwnerID, []AssetID) (map[AssetID]InputAsset, error)
}

type AssetContent struct {
	Body      io.ReadCloser
	MediaType string
	Size      int64
	SHA256    string
	// Role is the asset's declared output role (model, thumbnail, ...); the
	// delivery layer uses it to pick a cache policy, never a URL hint.
	Role string
	// Variant is the asset's saved variant; with Role and MediaType it lets
	// the delivery layer name a model file by its format.
	Variant string
}

type ProviderPort interface {
	Start(context.Context, StartRequest) (Observation, error)
	Observe(context.Context, ObserveRequest) (Observation, error)
	OpenAsset(context.Context, OpenAssetRequest) (io.ReadCloser, AssetMetadata, error)
}

// DependencyJobResolver is a pure, optional projection of a successful source's
// frozen snapshot. Implementations must not perform I/O or mutate the snapshot.
type DependencyJobResolver interface {
	ResolveDependencyJob(DependencyJobRequest) (UpstreamJob, error)
}

type DependencyJobRequest struct {
	Offering Offering
	Job      UpstreamJob
	Opaque   json.RawMessage
}

type ProviderWebhookDecoder interface {
	DecodeWebhook(context.Context, WebhookDecodeRequest) (Observation, error)
}

// ProviderTaskDeleter 是可选能力；没有远端任务删除协议的 Provider
// 仍可执行本地 tombstone，而无需伪造一个空实现。
type ProviderTaskCanceler interface {
	CancelProviderTask(context.Context, DeleteProviderRequest) error
}

type ProviderTaskDeleter interface {
	DeleteProviderTask(context.Context, DeleteProviderRequest) error
}

type ProviderRegistry interface {
	Provider(Offering) (ProviderPort, bool)
}

type Catalog interface {
	Normalize(capability Capability, offeringID OfferingID, input json.RawMessage) (Offering, json.RawMessage, error)
	Get(OfferingID) (Offering, bool)
	List(Capability) []Offering
}

type AssetWrite struct {
	DeliveryProfile string
	AssetID         AssetID
	OwnerID         OwnerID
	TaskID          TaskID
	AttemptID       AttemptID
	Role            string
	Variant         string
	Ordinal         int
	Required        bool
	MediaType       string
	MaxBytes        int64
	Body            io.Reader
}

type ProviderError struct {
	Failure *Failure
	Cause   error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	if e.Failure != nil && e.Failure.SafeDetail != "" {
		return e.Failure.SafeDetail
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "上游调用失败"
}

func (e *ProviderError) Unwrap() error { return e.Cause }

type AssetStore interface {
	Put(context.Context, AssetWrite) (Asset, error)
	Open(context.Context, Asset) (io.ReadCloser, error)
	Delete(context.Context, Asset) error
}

// AssetFidelityChecker observes local bytes only. Generation owns the decision
// to fail a Task. A declared requirement fails closed if the store lacks it.
type AssetFidelityChecker interface {
	CheckFidelity(context.Context, Asset, string) error
}

type Aggregate struct {
	Task           Task
	CurrentAttempt *Attempt
	Claims         []ArtifactClaim
}

type Lease struct {
	Aggregate
	Token string
}

type Commit struct {
	LeaseToken      string
	ExpectedVersion int64
	Task            Task
	Attempt         *Attempt
	ReplaceClaims   bool
	Claims          []ArtifactClaim
	NewAssets       []Asset
	Events          []Event
	ClearLease      bool
	// ValidatedDeliveryCompletions complete failed conversion Tasks in the
	// same transaction as the ordinary Task commit. Only the delivery
	// completion preparation path may fill them.
	ValidatedDeliveryCompletions []ValidatedDeliveryCompletion
	// DispatchReceipt 非空时，本事务在校验检查点状态后将
	// dispatch_checkpoint_json 推进为 received（或 not_dispatched 清回
	// idle）；谓词不匹配则整个事务回滚（无生命周期突变）。只有回执
	// 路径可携带；普通提交不触碰该列（§4.2 非覆盖纪律）。
	DispatchReceipt *DispatchReceiptSettlement
	// PersistResources 为 true 时，本事务把 Task.Resources 写入
	// generation_tasks.resource_state_json（0022，§6.5）；false 时该列
	// 保持不变（非覆盖纪律：只有资源态写者——成功提交初始化、合并
	// 更新、deadline finalizer——可置 true）。
	PersistResources bool
}

// DispatchReceiptSettlement 是回执结算的内核侧描述。
// ValidatedDeliveryCompletion is one already validated local completion of a
// failed Task. It is committed atomically with the ordinary Task that
// delivered the result.
type ValidatedDeliveryCompletion struct {
	Task            Task
	ExpectedVersion int64
	Attempt         Attempt
	Assets          []Asset
	Events          []Event
}

// TaskGrant is the durable bound grant of a Task, as the conversion delivery
// relation reads it.
type TaskGrant struct {
	RequestID         string
	OperationID       string
	PlanStepID        string
	PreparedInputID   PreparedInputID
	PreparedInputHash string
	IdempotencyKey    string
	OfferingID        OfferingID
	OfferingRevision  string
	Status            string
	TaskID            TaskID
}

// TaskGrantReader optionally reads the bound grant of a Task.
type TaskGrantReader interface {
	BoundGrantForTask(context.Context, OwnerID, TaskID) (TaskGrant, bool, error)
}

// AttemptReader optionally reads one historical Attempt of a Task.
type AttemptReader interface {
	LoadAttemptForTask(context.Context, OwnerID, TaskID, AttemptID) (Attempt, error)
}

// ConversionIntent names the exact model source and target formats of a
// declared paid format conversion. The Product Schema decides it; generation
// never names a conversion Offering or Provider itself.
type ConversionIntent struct {
	// SourceTaskID and SourceAssetID are the frozen source parameter. A
	// source-asset intent still resolves to one source Task when compared:
	// the same saved file and its Task are the same model source.
	SourceTaskID  TaskID
	SourceAssetID AssetID
	Formats       []string
}

// DeliveryConversionRule recognizes a Task whose frozen Offering and input run
// a Schema-declared format conversion, and names its source and formats.
type DeliveryConversionRule interface {
	IsConversion(OfferingID, json.RawMessage) bool
	ConversionIntent(Offering, json.RawMessage) (ConversionIntent, bool)
}

// ConversionConflictChecker reads the declared conversion of an active Task
// of the same owner and session. It is called inside the Task creation
// transaction. An error aborts creation: a read failure must never admit a
// second paid conversion.
type ConversionConflictChecker func(Task) (ConversionIntent, bool, error)

// ConversionGuardedRepository creates a Task after checking, in the same
// write transaction, that no active Task of the same owner and session runs a
// declared conversion with the same exact source and an overlapping target
// format. A repository without it cannot admit a paid conversion safely.
type ConversionGuardedRepository interface {
	CreateConversionGuarded(context.Context, Task, Event, int, ConversionConflictChecker) (Task, bool, error)
}

type DispatchReceiptSettlement struct {
	AttemptID     AttemptID
	Sequence      int64
	AckToken      string
	NotDispatched bool // result == "not_dispatched"：有证明未派发，清回 idle
}

type Tombstone struct {
	TaskID          TaskID
	ActorID         OwnerID
	ExpectedVersion int64
	DeletedAt       time.Time
	Event           Event
}

type Repository interface {
	// Create atomically checks idempotency and the owner's inflight admission limit
	// before inserting the task and its submitted event. A zero limit disables it.
	Create(context.Context, Task, Event, int) (Task, bool, error)
	Get(context.Context, OwnerID, TaskID) (Task, error)
	// GetByIdempotency recovers a committed public Task after its caller lost
	// the Submit response. Lookup is owner-scoped and never starts work.
	GetByIdempotency(context.Context, OwnerID, string) (Task, error)
	List(context.Context, TaskListScope, ListQuery) (TaskPage, error)
	Load(context.Context, TaskID) (Aggregate, error)
	LoadByUpstreamJob(context.Context, string, string) (Aggregate, error)
	ClaimReady(context.Context, string, time.Time, time.Duration, int) ([]Lease, error)
	RenewLease(context.Context, TaskID, string, time.Time, time.Duration) error
	Commit(context.Context, Commit) error
	Tombstone(context.Context, Tombstone) error
	// TaskTombstoned 报告任务是否已被删除（tombstone，GEN-02）：
	// legacy 旧图回退用它区分"从未入库"与"已删除任务"。
	TaskTombstoned(context.Context, TaskID) (bool, error)
	Events(context.Context, OwnerID, TaskID, int64, int) ([]Event, error)
	Close() error
}

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	TaskID() TaskID
	AttemptID() AttemptID
	AssetID() AssetID
	Token() string
}

func eventData(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}

func validateObservation(obs Observation) error {
	switch obs.Phase {
	case UpstreamPreparing:
		if obs.Job != nil || obs.Progress != 0 || len(obs.Artifacts) != 0 || len(obs.ResourceUpdates) != 0 || obs.Failure != nil || len(obs.Opaque) == 0 || !json.Valid(obs.Opaque) || strings.TrimSpace(string(obs.Opaque)) == "null" {
			return errors.New("准备 observation 必须只有私有检查点，不能携带作业、进度或产物")
		}
		if obs.Dispatch == nil || obs.Dispatch.Result != DispatchResultDispatched {
			return errors.New("准备 observation 缺少已派发回执")
		}
	case UpstreamAccepted, UpstreamRunning:
		if obs.Job == nil || obs.Job.RemoteID == "" || obs.Job.ProviderID == "" {
			return fmt.Errorf("阶段 %s 缺少上游作业身份", obs.Phase)
		}
	case UpstreamCompleted:
		if obs.Failure != nil {
			return errors.New("完成 observation 不能包含 failure")
		}
	case UpstreamFailed, UpstreamCanceled:
		if obs.Failure == nil {
			return fmt.Errorf("阶段 %s 缺少 failure", obs.Phase)
		}
	default:
		return fmt.Errorf("未知上游阶段 %q", obs.Phase)
	}
	if obs.Progress < 0 || obs.Progress > 100 {
		return fmt.Errorf("进度必须在 0 到 100 之间")
	}
	return nil
}

type ImageProtocol = schema.ImageProtocol
