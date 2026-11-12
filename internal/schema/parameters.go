package schema

import "encoding/json"

type PresentationSurface string

const (
	SurfaceTask     PresentationSurface = "task"
	SurfaceProvider PresentationSurface = "provider"
	// SurfaceHidden 声明参数参与提交与默认值注入，但不渲染到参数面板。
	SurfaceHidden PresentationSurface = "hidden"
	// SurfaceReadOnly exposes a public fixed or advised value without a user editor.
	SurfaceReadOnly PresentationSurface = "readonly"
	// SurfaceCanvas 声明参数渲染在画板区（随展示语境就地调整，
	// 如 2D 宽高比）；与会话参数 store 同一读写路径。
	SurfaceCanvas PresentationSurface = "canvas"
)

func (surface PresentationSurface) Valid() bool {
	return surface == SurfaceTask ||
		surface == SurfaceProvider ||
		surface == SurfaceHidden ||
		surface == SurfaceReadOnly ||
		surface == SurfaceCanvas
}

type PresentationControl string

const (
	ControlSelect      PresentationControl = "select"
	ControlText        PresentationControl = "text"
	ControlBoolean     PresentationControl = "boolean"
	ControlNumber      PresentationControl = "number"
	ControlSlider      PresentationControl = "slider"
	ControlAsset       PresentationControl = "asset"
	ControlMultiSelect PresentationControl = "multi-select"
)

func (control PresentationControl) Valid() bool {
	switch control {
	case ControlSelect, ControlText, ControlBoolean, ControlNumber, ControlSlider, ControlAsset, ControlMultiSelect:
		return true
	default:
		return false
	}
}

type ParameterPresentation struct {
	Surface      PresentationSurface  `json:"surface"`
	Control      PresentationControl  `json:"control"`
	Step         *float64             `json:"step,omitempty"`
	DisabledWhen []ParameterCondition `json:"disabled_when,omitempty"`
	VisibleWhen  []ParameterCondition `json:"visible_when,omitempty"`
	Notices      []ParameterNotice    `json:"notices,omitempty"`
}

// ForcedValue 声明参数的强制取值：当 When 条件命中时，服务端规范化
// 阶段以 Value 覆盖用户输入（最终裁决），前端表单按同一声明预填。
type ForcedValue struct {
	When  ParameterCondition `json:"when"`
	Value json.RawMessage    `json:"value"`
}

// ConditionalMaximum narrows a number parameter's upper bound when the
// referenced parameter values match: When and every AlsoWhen condition (#139:
// Tripo's smart low poly with a quad mesh). Every matching entry applies, so
// the effective bound is the smallest matching Maximum; the static Minimum and
// Maximum still apply. A value the caller gave is only validated against it,
// never rewritten; a value the Schema supplied (the default) is moved into it
// (ConvergeDefaults).
type ConditionalMaximum struct {
	When     ParameterCondition   `json:"when"`
	AlsoWhen []ParameterCondition `json:"also_when,omitempty"`
	Maximum  float64              `json:"maximum"`
}

// ImageSizeConstraint validates pixel dimensions as well as named presets.
type ImageSizeConstraint struct {
	MinPixels int64   `json:"min_pixels"`
	MaxPixels int64   `json:"max_pixels"`
	MaxEdge   int64   `json:"max_edge,omitempty"`
	Multiple  int64   `json:"multiple,omitempty"`
	MaxRatio  float64 `json:"max_ratio"`
}

// ImageInputConstraint bounds decoded source-image dimensions. It is frozen
// with the parameter and does not describe an output size or generated quality.
type ImageInputConstraint struct {
	MinWidth  int64 `json:"min_width"`
	MinHeight int64 `json:"min_height"`
	MaxPixels int64 `json:"max_pixels"`
	// MaxAspectRatio bounds the longer side over the shorter side (16 means
	// [1/16, 16]); 0 declares no bound. Images are never resized to fit (#276).
	MaxAspectRatio float64 `json:"max_aspect_ratio,omitempty"`
}

// AlphaFlatten declares that the upstream of an image parameter does not take
// transparent pixels (#271). Before the wire projection a source that has any
// non-opaque pixel is composited over BackgroundColor (#rrggbb) and sent as an
// opaque PNG; an opaque source keeps its stored bytes. MaxPixels bounds the
// decoded grid. KeepWhen names the parameter values under which the upstream does take alpha (GPT
// Image with background=transparent): when any condition matches the normalized input, the source
// keeps its stored bytes. Absent keeps the stored bytes, alpha included.
type AlphaFlatten struct {
	BackgroundColor string               `json:"background_color"`
	MaxPixels       int64                `json:"max_pixels"`
	KeepWhen        []ParameterCondition `json:"keep_when,omitempty"`
}

// SourceSideDefault gives a number parameter a default that follows the size of
// the pictures a request submits (#284): a background removal wants its working
// resolution to match its source, since a larger picture is lost at a small one
// and a small picture gains nothing from a large one. Source names the asset
// parameter whose pictures decide it. The long side of a picture is the larger
// of its width and height; for several pictures the largest long side counts,
// because one value serves the whole batch. The first Tier whose MaxSide the long
// side does not exceed gives the value; the last Tier has no MaxSide and covers
// everything larger. A value the caller gave is never replaced, and pictures that
// cannot be measured leave the parameter's static Default. The kernel only
// measures and picks; the tiers are product configuration.
type SourceSideDefault struct {
	Source string           `json:"source"`
	Tiers  []SourceSideTier `json:"tiers"`
}

// SourceSideTier is one step of SourceSideDefault: the Value for pictures whose
// long side is at most MaxSide pixels, or for every larger picture when MaxSide
// is absent (the last tier only).
type SourceSideTier struct {
	MaxSide int64           `json:"max_source_side,omitempty"`
	Value   json.RawMessage `json:"value"`
}

// ValueFor is the tier value for pictures whose largest long side is side
// pixels; false when the rule has no tier for it (a side of zero or less, or no
// open-ended last tier).
func (rule SourceSideDefault) ValueFor(side int64) (json.RawMessage, bool) {
	if side <= 0 {
		return nil, false
	}
	for _, tier := range rule.Tiers {
		if tier.MaxSide == 0 || side <= tier.MaxSide {
			return tier.Value, true
		}
	}
	return nil, false
}

// PromptDelivery 声明参数改由提示词传递：参数照常展示与选择，但不上线；
// 规范化阶段把 Template 中的 {value} 替换为所选值（选项 value 的字符串），
// 另起一行追加到 Offering 的提示词参数（语义 prompt）末尾，并从输入中移除该参数。
// 用于上游没有对应线参数的模型（如尺寸只能写进提示词）。
type PromptDelivery struct {
	Template string `json:"template"`
}

// UploadedModel constrains real uploaded inputs on a declared model-source
// parameter. Delivered Task outputs retain their existing contract unless
// NativeOnly explicitly restricts the source to the owner's upload table.
// Catalog captures ProfileRevision and ProfileLimits together. Both absent
// retains the historical current-policy contract; a partial snapshot is invalid.
type UploadedModel struct {
	ProfileID                     string            `json:"profile_id"`
	ProfileRevision               string            `json:"profile_revision,omitempty"`
	ProfileLimits                 *ModelInputLimits `json:"profile_limits,omitempty"`
	NativeOnly                    bool              `json:"native_only,omitempty"`
	RequireTextures               bool              `json:"require_textures,omitempty"`
	RequireSingleBaseColorTexture bool              `json:"require_single_base_color_texture,omitempty"`
	Requirements                  string            `json:"requirements,omitempty"`
}

// TaskSourceKind distinguishes a provider continuation from owned stored bytes.
// The empty value preserves the original upstream-job dependency contract.
type TaskSourceKind string

const (
	TaskSourceUpstreamJob TaskSourceKind = "upstream_job"
	TaskSourceAssets      TaskSourceKind = "assets"
)

type Parameter struct {
	// AdjustmentPolicy separates Agent authority from a read-only presentation.
	// Empty preserves historical rules. Advised parameters retain a recommended
	// Default but permit a legal Agent override with a frozen explanation.
	AdjustmentPolicy string               `json:"adjustment_policy,omitempty"`
	AdjustmentReason string               `json:"adjustment_reason,omitempty"`
	UploadedModel    *UploadedModel       `json:"uploaded_model,omitempty"`
	PromptDelivery   *PromptDelivery      `json:"prompt_delivery,omitempty"`
	OmitWhen         []ParameterCondition `json:"omit_when,omitempty"`
	ForcedWhen       []ForcedValue        `json:"forced_when,omitempty"`
	// LegacyValues maps retired values (no longer offered) to the value a new
	// request uses instead, so a stored pin or old caller still names an
	// explicit target (#267: output size "auto" -> 2048x2048). Applied before
	// forced_when and validation; never projected as a choice.
	LegacyValues map[string]json.RawMessage `json:"legacy_values,omitempty"`
	// Fixed 声明参数由后端固定：服务端规范化时总以 Default 覆盖调用方取值
	// （forced_when 仍可在其后覆盖），UI 可按 readonly 声明展示，UI 与 Agent 都不能选择该值。用于把
	// 模型版本、质量档位等固定为最高质量（产品设计宗旨见
	// docs/provider-schema-architecture.md「最新 API 与最高质量」）。
	Fixed bool `json:"fixed,omitempty"`
	// DefaultBySourceSide makes the default follow the size of the submitted
	// pictures (#284); the static Default stays the value when they cannot be
	// measured. Only a number parameter declares it.
	DefaultBySourceSide *SourceSideDefault         `json:"default_by_source_side,omitempty"`
	Integer             bool                       `json:"integer,omitempty"`
	ImageSize           *ImageSizeConstraint       `json:"image_size,omitempty"`
	ImageInput          *ImageInputConstraint      `json:"image_input,omitempty"`
	AlphaFlatten        *AlphaFlatten              `json:"alpha_flatten,omitempty"`
	WireTemplate        json.RawMessage            `json:"wire_template,omitempty"`
	WireValues          map[string]json.RawMessage `json:"wire_values,omitempty"`

	Name              string                 `json:"name"`
	Label             string                 `json:"label"`
	Type              string                 `json:"type"`
	WireName          string                 `json:"wire_name,omitempty"`
	Required          bool                   `json:"required,omitempty"`
	Default           json.RawMessage        `json:"default,omitempty"`
	Options           []ParameterOption      `json:"options,omitempty"`
	Minimum           *float64               `json:"minimum,omitempty"`
	Maximum           *float64               `json:"maximum,omitempty"`
	MaximumWhen       []ConditionalMaximum   `json:"maximum_when,omitempty"`
	MaxLength         int                    `json:"max_length,omitempty"`
	MinItems          int                    `json:"min_items,omitempty"`
	MaxItems          int                    `json:"max_items,omitempty"`
	MediaTypes        []string               `json:"media_types,omitempty"`
	Extensions        []string               `json:"extensions,omitempty"`
	MaxBytes          int64                  `json:"max_bytes,omitempty"`
	TaskCapabilities  []Capability           `json:"task_capabilities,omitempty"`
	TaskSourceKind    TaskSourceKind         `json:"task_source_kind,omitempty"`
	TaskOfferings     []OfferingID           `json:"task_offerings,omitempty"`
	SameProvider      bool                   `json:"same_provider,omitempty"`
	SameSession       bool                   `json:"same_session,omitempty"`
	TaskSourceBinding string                 `json:"task_source_binding,omitempty"`
	Description       string                 `json:"description,omitempty"`
	Presentation      *ParameterPresentation `json:"presentation,omitempty"`
	Semantic          ParameterSemantic      `json:"semantic,omitempty"`
	// MaxTotalEncodedBytes bounds all assets of this parameter together after
	// base64 encoding, for upstreams that limit the whole inline request
	// rather than each image (#276: Gemini). 0 declares no total bound.
	MaxTotalEncodedBytes int64 `json:"max_total_encoded_bytes,omitempty"`
	// UpstreamJobMaxAgeSeconds declares how long, counted from the source
	// Task's creation, the Provider still keeps the job an upstream_job task
	// parameter refers to (#325: Meshy keeps API tasks at most three days).
	// Past it the reference is treated as gone: binders take a saved-file
	// route instead and the kernel refuses the dependency before dispatch.
	// 0 declares no limit. Only valid on a type "task" upstream_job parameter.
	UpstreamJobMaxAgeSeconds int64 `json:"upstream_job_max_age_seconds,omitempty"`
	// TaskSourceAlternative names what to do instead when a Task is not an
	// accepted source of this parameter (#276): the Task of a service the step
	// does not take, with the route that does work (generate with the service
	// it takes first, or another processing service). The kernel says it in the
	// refusal it gives before anything is paid (input_source_task_unsupported
	// {alternative}). Schema copy, localized with the Offering's other texts; only
	// valid on a type "task" parameter.
	TaskSourceAlternative string `json:"task_source_alternative,omitempty"`
	// SourceInputRules refuse a Task as the source of this parameter by what the
	// Task's own frozen input says, together with what the request asks for
	// (#294): some limits belong to the model that was made, not to the step
	// that takes it (Meshy Rigging adds no finger bones to a quad-topology
	// model, and the topology is a parameter of the Task that made the model).
	// The kernel applies them where it resolves the Task dependency, before
	// anything is submitted or paid. Only valid on a type "task" parameter.
	SourceInputRules []SourceInputRule `json:"source_input_rules,omitempty"`
	// RequiredItems lists options every valid selection of a string-array
	// parameter holds, so a smaller choice than the full set stays legal without
	// dropping what the platform itself needs (Tripo's GLTF conversion delivers
	// the self-contained GLB of the model role). A selection without one is
	// refused where the input is normalized, before anything is quoted or paid.
	RequiredItems []string `json:"required_items,omitempty"`
}

// SourceInputRule refuses a source Task when two facts hold together: its own
// frozen input meets every SourceConditions entry, and the request's input meets
// every RequestConditions entry (#294). The conditions are the ones parameters
// already use (ParameterCondition); SourceConditions name parameters of the
// Offerings the source parameter lists in task_offerings.
type SourceInputRule struct {
	// SourceConditions all hold for the frozen input of the source Task.
	SourceConditions []ParameterCondition `json:"source_conditions"`
	// RequestConditions all hold for the input of this request; empty means the
	// source alone decides.
	RequestConditions []ParameterCondition `json:"request_conditions,omitempty"`
	// Message is the Schema's reason for the requester, complete text localized
	// with the Offering's other copy.
	Message string `json:"message"`
}

// Refuses reports whether the rule refuses a source Task whose frozen input is
// source, for a request whose input is request. A rule without source
// conditions refuses nothing.
func (rule SourceInputRule) Refuses(source, request map[string]any) bool {
	if len(rule.SourceConditions) == 0 {
		return false
	}
	for _, condition := range rule.SourceConditions {
		if !ConditionMatches(condition, source) {
			return false
		}
	}
	for _, condition := range rule.RequestConditions {
		if !ConditionMatches(condition, request) {
			return false
		}
	}
	return true
}

type ParameterOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Group string `json:"group,omitempty"`
}
