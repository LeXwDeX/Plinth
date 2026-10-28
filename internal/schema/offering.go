package schema

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

type OfferingID string
type RetryPolicy struct {
	MaxAttempts    int            `json:"max_attempts"`
	InitialBackoff time.Duration  `json:"initial_backoff_ns"`
	MaxBackoff     time.Duration  `json:"max_backoff_ns"`
	Retryable      []FailureClass `json:"retryable"`
}

func (p RetryPolicy) Allows(class FailureClass, nextOrdinal int) bool {
	if p.MaxAttempts <= 1 || nextOrdinal > p.MaxAttempts {
		return false
	}
	for _, allowed := range p.Retryable {
		if allowed == class {
			return true
		}
	}
	return false
}

// 资源交付类别：空串为旧语义（仅 role，任意变体，等效 required 阻断）。
const (
	AssetDeliveryRequired   = "required"
	AssetDeliveryBackground = "background"
	AssetDeliveryOnDemand   = "on-demand"
)

// AssetRequirement 声明一类公开产物。旧语义（Variant/VariantPrefix/
// Delivery 均空）按 role 匹配任意变体，Minimum 缺省等效 1；显式
// delivery 必须带显式变体范围与显式 minimum，由 catalog 加载期校验。
//
// VariantFrom/VariantCase 声明"逐项展开"的 on-demand 槽位（回顾性整改
// P2）：槽位身份逐个派生自某个多选参数的选中项，成功提交时每个选中项
// 展开为一个独立槽位（变体 = VariantPrefix + 规范化选中值）。这是通用
// 声明式规则——内核不含任何格式枚举，也不为 Provider 子作业建表；
// Provider 与内核共用 SelectionVariant 的同一实现（无第二真源）。
type AssetRequirement struct {
	// RetainDependencyIdentity preserves private claim evidence bound to actual stored bytes.
	RetainDependencyIdentity bool `json:"retain_dependency_identity,omitempty"`
	// DeliveryProfile freezes an opt-in byte/container identity boundary. Empty preserves legacy delivery.
	DeliveryProfile string `json:"delivery_profile,omitempty"`
	Role            string `json:"role"`
	Variant         string `json:"variant,omitempty"`
	VariantPrefix   string `json:"variant_prefix,omitempty"`
	Delivery        string `json:"delivery,omitempty"`
	Group           string `json:"group,omitempty"`
	Minimum         *int   `json:"minimum,omitempty"`
	// RequiredWhen makes an exact required slot conditional on the frozen,
	// normalized input. An unmatched slot has minimum zero; supplied bytes must
	// still pass the declared delivery checks.
	RequiredWhen *ParameterCondition `json:"required_when,omitempty"`
	MediaTypes   []string            `json:"media_types,omitempty"`
	MaxBytes     int64               `json:"max_bytes,omitempty"`
	// Fidelity is a structural delivery gate; animation also requires a skeleton.
	Fidelity string `json:"fidelity,omitempty"`
	// VariantFrom 指向驱动逐项展开的多选参数名（string-array + options）。
	VariantFrom string `json:"variant_from,omitempty"`
	// VariantCase 声明选中值到变体的大小写规范化：""（原样）/"lower"/"upper"。
	VariantCase string `json:"variant_case,omitempty"`
}

// 变体大小写规范化枚举（VariantCase 的合法取值）。
const (
	VariantCaseVerbatim = ""
	VariantCaseLower    = "lower"
	VariantCaseUpper    = "upper"
)

const (
	DeliveryProfileBinaryFBXV1  = "binary-fbx-v1"
	DeliveryProfile3MFPackageV1 = "3mf-package-v1"
)

// DeliveryProfileFormat returns the exact format identity of a supported byte
// profile. It does not establish animation, topology, printability or color.
func DeliveryProfileFormat(profile string) (variant, mediaType string, known bool) {
	switch profile {
	case DeliveryProfileBinaryFBXV1:
		return "fbx", "model/fbx", true
	case DeliveryProfile3MFPackageV1:
		return "3mf", "model/3mf", true
	case DeliveryProfileGaussianSplat32V1:
		return GaussianSplatVariant, GaussianSplatMediaType, true
	default:
		return "", "", false
	}
}

// InputRule 描述跨参数的存在性约束。参数本身负责类型和值域校验，
// Rule 只负责声明一组参数至少/至多允许出现几个。
type InputRule struct {
	MaxTotalItems int                  `json:"max_total_items,omitempty"`
	ForbiddenWhen []ParameterCondition `json:"forbidden_when,omitempty"`
	Message       string               `json:"message,omitempty"`
	Parameters    []string             `json:"parameters"`
	Minimum       int                  `json:"minimum"`
	Maximum       int                  `json:"maximum"`
}
type Offering struct {
	NativePricing *NativePricing `json:"native_pricing,omitempty"`
	ID            OfferingID     `json:"id"`
	Revision      string         `json:"revision"`
	ProviderID    string         `json:"provider_id"`
	ProviderName  string         `json:"provider_name"`
	// ProviderShortLabel is the vendor's short name for compact controls (such as "GPT");
	// every Offering of one provider declares the same value.
	ProviderShortLabel string           `json:"provider_short_label"`
	ModelID            string           `json:"model_id"`
	ModelName          string           `json:"model_name"`
	Capability         Capability       `json:"capability"`
	Domain             CapabilityDomain `json:"domain"`
	AdapterKey         string           `json:"adapter_key"`
	Enabled            bool             `json:"enabled"`
	Features           []string         `json:"features,omitempty"`
	Default            bool             `json:"default,omitempty"`
	Recommended        bool             `json:"recommended,omitempty"`
	DisplayOrder       int              `json:"display_order"`
	CapabilityOrder    int              `json:"capability_order"`
	Priority           int              `json:"priority,omitempty"`
	EstimatedCredits   int              `json:"estimated_credits,omitempty"`
	// CreditsMetered 声明该 Offering 的 Provider 按积分计费；未声明的
	// Offering 不向用户展示积分（estimated_credits 仍用于内部审批与预估）。
	CreditsMetered bool `json:"credits_metered,omitempty"`
	// EstimatedCreditsUnitParameter 声明"按数量计费"的数值参数
	// （如 n / max_images）：预计积分 = estimated_credits × 参数值
	// （值 clamp 到 >=1，缺失或不可解码按单件计）。加载期校验该参数
	// 必须在 Offering 参数中声明。
	EstimatedCreditsUnitParameter string             `json:"estimated_credits_unit_parameter,omitempty"`
	ReferenceSelectionPolicy      string             `json:"reference_selection_policy,omitempty"` // Empty keeps legacy rewriting; preserve retains the selected image sequence.
	ExecutionMessages             ExecutionMessages  `json:"execution_messages"`
	Cancellation                  CancellationPolicy `json:"cancellation"`
	IdempotentSubmission          bool               `json:"idempotent_submission,omitempty"`
	Timeout                       time.Duration      `json:"timeout_ns"`
	// JobTimeout 作业级总时长（attempt 启动起的上限）；0 = 继承 Timeout。
	// （B17：request 超时管单次上游 HTTP 调用，job 超时管整个作业。）
	JobTimeout          time.Duration      `json:"job_timeout_ns,omitempty"`
	PollInterval        time.Duration      `json:"poll_interval_ns"`
	Retry               RetryPolicy        `json:"retry"`
	MaterializeAttempts int                `json:"materialize_attempts"`
	Parameters          []Parameter        `json:"parameters"`
	InputRules          []InputRule        `json:"input_rules,omitempty"`
	Outputs             []AssetRequirement `json:"outputs"`
	AssetHosts          []string           `json:"asset_hosts,omitempty"`
	ProviderOptions     json.RawMessage    `json:"provider_options,omitempty"`
	// Cost 声明本供给项的积分成本结构（§5.2 有限原语）：金额计算只允许
	// 存在于共享声明式评估器一处（internal/catalog.EvaluateExtraCredits），
	// 提交期校验与 Provider 私有规划器都必须调用它。
	Cost *CostDeclaration `json:"cost,omitempty"`
	// CostPolicy 声明既有提交同意内的额外积分授权上限与拒绝文案；
	// 公开投影禁止泄漏其原始值（§8.1）。
	CostPolicy *CostPolicy `json:"cost_policy,omitempty"`
	// PostSuccess 声明成功后观察（§6.1，W6）；nil = 现状（零观察）。
	PostSuccess *PostSuccessPolicy `json:"post_success,omitempty"`
}

// CostDeclaration 是 Offering 级成本声明（§5.2）：基础积分 + 额外积分
// 的有限触发条件（复用 ParameterCondition 算子）。任一 extra_when 条件
// 命中即计一次 extra_credits（不按命中数累加）。
type CostDeclaration struct {
	BaseCredits  int                  `json:"base_credits"`
	ExtraCredits int                  `json:"extra_credits,omitempty"`
	ExtraWhen    []ParameterCondition `json:"extra_when,omitempty"`
	// ConsentParameter 指向承担"额外积分显式同意"语义的布尔参数；
	// 额外积分 >0 时该参数必须为真值，否则提交期拒绝（§5.3）。
	ConsentParameter string `json:"consent_parameter,omitempty"`
	// FormatParameter 指向承担"目标格式"语义的 select 参数；Provider
	// 私有规划器按冻结 options 对其取值 fail-closed（未知格式零发送）。
	FormatParameter string `json:"format_parameter,omitempty"`
	// SelectionParameter 指向承担"多选转换"语义的 string-array 参数
	//（W6 生成期同意形态）：评估器按选中项数计算 estimated_extra =
	// extra_credits × len(selection)；与 extra_when 互斥（加载期校验），
	// 每个选中项至多计费一次（不按 extra 条件重复累计）。
	SelectionParameter string `json:"selection_parameter,omitempty"`
	// ExtraLabel is the display name of what the extra credits pay for (for
	// example "附加导出格式"). Approval cards word the extra-credit line with it.
	ExtraLabel string `json:"extra_label,omitempty"`
}

// CostPolicy 是 Offering 级同意边界（§5.2/§5.3）：额外积分超出上限的
// 转换必须走新的显式公开任务；Messages 是拒绝/拒绝发送时的用户可见
// 文案（产品内容归 Schema）。
type CostPolicy struct {
	MaxAuthorizedExtraCredits int                `json:"max_authorized_extra_credits"`
	Messages                  CostPolicyMessages `json:"messages,omitempty"`
}
type CostPolicyMessages struct {
	ConsentRequired string `json:"consent_required,omitempty"`
	OverLimit       string `json:"over_limit,omitempty"`
	UnknownCost     string `json:"unknown_cost,omitempty"`
}
type ExecutionMessages struct {
	InlineStoreFailed  string `json:"inline_store_failed"`
	StoredAssetInvalid string `json:"stored_asset_invalid"`
	SubmissionUnknown  string `json:"submission_unknown"`
	// RefetchSourceUnavailable / RefetchResultUnavailable are the failure texts
	// of an upstream result refetch (#295): the failed source Task or its
	// accepted job is no longer readable, or the upstream no longer returns a
	// complete result for it. Required when a connection declares
	// result_refetch.
	RefetchSourceUnavailable string `json:"refetch_source_unavailable,omitempty"`
	RefetchResultUnavailable string `json:"refetch_result_unavailable,omitempty"`
	// UpstreamJobConflict is the failure text when the upstream returned a job
	// ID already recorded for another Task (#298). The upstream may have
	// accepted and charged the submission, so it is not sent again. Empty
	// falls back to SubmissionUnknown.
	UpstreamJobConflict string `json:"upstream_job_conflict,omitempty"`
	// RetiredModel is the reason a direct generation request naming a retired
	// model's Offering is refused before anything is frozen or sent ({from},
	// {to}; #329 follow-up). ReferenceLimit is the note that only the first
	// {count} of {total} reference pictures are used because the Offering
	// ({model}) takes no more; ReferenceLimitOne is the same note for a model
	// that takes exactly one, worded for one picture in every language ("第 1
	// 张", "only the first is used"). The pair is the one text for this (#329
	// note 99647): the task API's answer, the Agent's tool result, the
	// approval card, the finished message and the composer all read it.
	RetiredModel      string `json:"retired_model,omitempty"`
	ReferenceLimit    string `json:"reference_limit,omitempty"`
	ReferenceLimitOne string `json:"reference_limit_one,omitempty"`
	// ReferenceSelectionNote is the neutral count for a step whose pictures
	// were replaced or de-duplicated, where "only the first N" would be false
	// ({model}, {count}; #80). It never names a source that was not retained.
	ReferenceSelectionNote string `json:"reference_selection_note,omitempty"`
	// ReferenceCapacity is the composer's pre-send statement of the model's
	// reference-picture capacity ({model}, {count}, {total}). It never predicts
	// which pictures will be used (#80).
	ReferenceCapacity string `json:"reference_capacity,omitempty"`
	// ReferenceLimitExceeded is the reason a step that keeps its selected
	// reference pictures is refused because the Offering takes fewer
	// ({label} {max} {actual}). It is a refusal, not the cut reported by
	// ReferenceLimit: no input is frozen and nothing is sent (#329).
	ReferenceLimitExceeded string `json:"reference_limit_exceeded,omitempty"`
	// ExtraCreditsConsentRequired / ExtraCreditsOverLimit are the reasons a
	// step whose quote shows extra credits is refused before preparation when
	// the explicit consent is missing or the extra exceeds the authorized
	// limit (#329). Per-Offering cost_policy messages win over them.
	ExtraCreditsConsentRequired string `json:"extra_credits_consent_required,omitempty"`
	ExtraCreditsOverLimit       string `json:"extra_credits_over_limit,omitempty"`
	// InputSource* are the reasons a Provider's synchronous input check
	// refuses a step before admission (#358): the source no longer matches the
	// frozen input, the server cannot read it now, or it is too large, of an
	// unsupported type, outside the image dimensions, unreadable as the
	// declared format, or not a native same-Provider Task for Rig Check. Each
	// is Schema copy with the values named in InputRefusal, so the Agent's
	// tool result follows the turn's language. Empty means the Provider keeps
	// its own diagnostic.
	InputSourceMismatch    string `json:"input_source_mismatch,omitempty"`
	InputSourceUnavailable string `json:"input_source_unavailable,omitempty"`
	InputSourceTooLarge    string `json:"input_source_too_large,omitempty"`
	InputSourceMediaType   string `json:"input_source_media_type,omitempty"`
	InputSourceDimensions  string `json:"input_source_dimensions,omitempty"`
	InputSourceInvalid     string `json:"input_source_invalid,omitempty"`
	InputSourceRigRequired string `json:"input_source_rig_required,omitempty"`
	// InputSourcePixels is the pixel-limit refusal for an image; it names the
	// actual dimensions and the maximum pixel count without inventing a
	// minimum width or height.
	InputSourcePixels string `json:"input_source_pixels,omitempty"`
	// InputParameterInvalid is the refusal for one input parameter with an
	// invalid value; {label} is the parameter's public Schema label.
	// InputParametersInvalid is the same refusal when no public label exists.
	InputParameterInvalid  string `json:"input_parameter_invalid,omitempty"`
	InputParametersInvalid string `json:"input_parameters_invalid,omitempty"`
	// InputSourceTaskExpired / InputSourceTaskUnsupported are the reasons a
	// Task-dependency source is refused at the kernel boundary; {label} is the
	// source parameter's public Schema label. They keep the two common causes
	// distinct instead of returning a raw internal dependency diagnostic.
	InputSourceTaskExpired     string `json:"input_source_task_expired,omitempty"`
	InputSourceTaskUnsupported string `json:"input_source_task_unsupported,omitempty"`
}

// ReferenceLimitText is the reference-limit template for a model that takes
// count pictures: the one-picture text when count is 1 and it is declared,
// otherwise the general text.
func (messages ExecutionMessages) ReferenceLimitText(count int) string {
	if count == 1 && messages.ReferenceLimitOne != "" {
		return messages.ReferenceLimitOne
	}
	return messages.ReferenceLimit
}

// ReferenceLimitExceededText fills the refusal text for a reference-image
// list of actual pictures when the Offering takes at most maximum. It returns
// "" when the Schema declares no text; the pairs are DetailTemplate's
// {name}/value replacements for a requester-visible reason (#329).
func (messages ExecutionMessages) ReferenceLimitExceededText(label string, actual, maximum int) (string, []string) {
	if messages.ReferenceLimitExceeded == "" {
		return "", nil
	}
	values := []string{"{label}", label, "{actual}", strconv.Itoa(actual), "{max}", strconv.Itoa(maximum)}
	return strings.NewReplacer(values...).Replace(messages.ReferenceLimitExceeded), values
}

type CancellationPolicy struct {
	Supported      bool   `json:"supported"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	LocalOnly      string `json:"local_only"`
	Requesting     string `json:"requesting"`
	Confirmed      string `json:"confirmed"`
	Failed         string `json:"failed"`
	Unsupported    string `json:"unsupported"`
	Unknown        string `json:"unknown"`
}

// EstimateTotalCredits 按用户输入计算本次提交的预计积分：声明了
// estimated_credits_unit_parameter 时按参数值（>=1）乘算单件价；
// 缺失或不可解码按单件价。声明了 cost 的 Offering 另加该输入触发的
// 额外积分（同一共享评估器 EvaluateExtraCredits；输入不可评估时只计
// 基础价，提交期校验仍 fail-closed）。这是展示口径，不是结算。
func (o Offering) EstimateTotalCredits(input json.RawMessage) int {
	total := o.estimateBaseCredits(input)
	if o.Cost == nil || o.Cost.ExtraCredits <= 0 {
		return total
	}
	if extra, err := EvaluateExtraCredits(*o.Cost, input); err == nil && extra > 0 {
		total += extra
	}
	return total
}

// MaximumEstimatedCredits is the highest estimate the user's optional billed
// choices can reach, or 0 when no choice changes the price. It is a derived
// number for clients that cannot (and must not) read the cost declaration.
func (o Offering) MaximumEstimatedCredits() int {
	if o.Cost == nil || o.Cost.ExtraCredits <= 0 {
		return 0
	}
	multiplier := 1
	if o.Cost.SelectionParameter != "" {
		for _, parameter := range o.Parameters {
			if parameter.Name == o.Cost.SelectionParameter && len(parameter.Options) > 0 {
				multiplier = len(parameter.Options)
			}
		}
	}
	return o.EstimatedCredits + o.Cost.ExtraCredits*multiplier
}

// QuotedCredits is the estimate an Offering is quoted at before the person
// makes any optional billed choice: the base price plus the extra credits its
// fixed parameter values always incur (#324: Tripo's complete export delivery
// always includes every listed format, so segmentation is quoted at 90, not at
// its 40 base). Clients show it as the price, or as the low end of a range up
// to MaximumEstimatedCredits. It is evaluated by EstimateTotalCredits, so it is
// the same number a submission with only the fixed values is estimated at.
func (o Offering) QuotedCredits() int {
	if o.Cost == nil || o.Cost.ExtraCredits <= 0 {
		return o.EstimatedCredits
	}
	fixed := map[string]json.RawMessage{}
	for _, parameter := range o.Parameters {
		if parameter.Fixed && len(parameter.Default) > 0 {
			fixed[parameter.Name] = parameter.Default
		}
	}
	if len(fixed) == 0 {
		return o.EstimatedCredits
	}
	encoded, err := json.Marshal(fixed)
	if err != nil {
		return o.EstimatedCredits
	}
	return o.EstimateTotalCredits(encoded)
}

func (o Offering) estimateBaseCredits(input json.RawMessage) int {
	if o.EstimatedCreditsUnitParameter == "" || o.EstimatedCredits <= 0 {
		return o.EstimatedCredits
	}
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 {
		return o.EstimatedCredits
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var values map[string]any
	if err := decoder.Decode(&values); err != nil {
		return o.EstimatedCredits
	}
	number, ok := values[o.EstimatedCreditsUnitParameter].(json.Number)
	if !ok {
		return o.EstimatedCredits
	}
	units, err := number.Int64()
	if err != nil || units < 1 {
		return o.EstimatedCredits
	}
	if units > 1000 {
		units = 1000
	}
	return o.EstimatedCredits * int(units)
}

// PostSuccessPolicy 声明成功后观察生命周期（§6.1）：快照内嵌（Offering），
// 缺省 = 现状（成功后零观察）。公开投影禁止泄漏 interval/deadline 原始
// 调度字段（§8.1）；只有声明了 observe 时成功提交才初始化 0022 资源态。
type PostSuccessPolicy struct {
	Observe         bool `json:"observe"`
	IntervalSeconds int  `json:"interval_seconds,omitempty"`
	DeadlineSeconds int  `json:"deadline_seconds,omitempty"`
}

type TaskState string

const (
	TaskQueued        TaskState = "QUEUED"
	TaskRunning       TaskState = "RUNNING"
	TaskMaterializing TaskState = "MATERIALIZING"
	TaskSucceeded     TaskState = "SUCCEEDED"
	TaskFailed        TaskState = "FAILED"
	TaskCanceled      TaskState = "CANCELED"
)

func (s TaskState) Terminal() bool {
	return s == TaskSucceeded || s == TaskFailed || s == TaskCanceled
}

func (s TaskState) Valid() bool {
	switch s {
	case TaskQueued, TaskRunning, TaskMaterializing, TaskSucceeded, TaskFailed, TaskCanceled:
		return true
	default:
		return false
	}
}

type ImageProtocol struct {
	MaxResponseBytes int64           `json:"max_response_bytes"`
	AuthHeader       string          `json:"auth_header,omitempty"`
	AuthPrefix       string          `json:"auth_prefix,omitempty"`
	Endpoint         string          `json:"endpoint"`
	RequestEncoding  string          `json:"request_encoding,omitempty"`
	RequestTemplate  json.RawMessage `json:"request_template"`
	Response         struct {
		ImagesPath       string `json:"images_path"`
		DefaultMediaType string `json:"default_media_type"`
		Base64Field      string `json:"base64_field"`
		URLField         string `json:"url_field"`
		MediaTypeField   string `json:"media_type_field"`
		Role             string `json:"role"`
	} `json:"response"`
}
