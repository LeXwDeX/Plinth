package catalog

import schemacontract "github.com/LeXwDeX/Plinth/internal/schema"

// AssetListSchema 是 application.asset_list（#327）：项目下的一份制作清单。
//
// 清单是“工作”，不是素材库：Agent 提出一组待做资产，用户确认后逐项推进。
// 每项有状态、路线（能力序列）、共享简报与结果引用；只有验收通过的项才会在
// #329 登记为项目资产。这里的步数只是计划值：它与 step_policy.counted_capabilities
// 同一份口径，不设预算、不显示已用；实际计步仍由 #328 的执行授权负责。
//
// 本段可选：未声明时旧 Catalog 仍可加载，清单能力与路由都不存在。声明时要求
// application.step_policy 同时存在，否则 3D 计划步数会静默为 0。
//
// 项数上限不在这里重复声明：复用 application.project.limits.maximum_list_items，
// 避免出现两个同名上限。
type AssetListSchema struct {
	Limits AssetListLimitsSchema `json:"limits"`
	// Decompose 是 #331 的单图拆解段：一张源图最多拆出多少资产项、裁图尺寸
	// 限制、带参考图时的缺省路线与拒绝文案。可选：未声明时拆解与参考图校验
	// 仍按服务端默认值工作。
	Decompose *AssetListDecomposeSchema `json:"decompose,omitempty"`
	// Kinds 是清单项的类型词表（character|weapon|prop|vehicle|scene|image）。
	// 每个 kind 的 default_route 就是缺省路线，不再另设 default_route_by_kind
	// 映射：一处声明、一处校验，公开投影直接读取它。
	Kinds []AssetListKindSchema `json:"kinds"`
	// Routes 是可选路线：每条路线是一串能力 id，例如文生 3D 后重拓扑。
	// Agent 只能选路线 id，不能自己拼任意能力链。
	Routes []AssetListRouteSchema `json:"routes"`
	// States 是清单状态；ItemStates 是清单项状态。role 是浏览器据此渲染的
	// 平台既有角色词汇（pending/active/denied/expired/replaced）。
	States     []AssetListStateSchema `json:"states"`
	ItemStates []AssetListStateSchema `json:"item_states"`
	// ItemBlocks 是 #328 的第二轴（暂停/审批/步数上限），本步只预留声明，
	// 状态机不可达。
	ItemBlocks []ValueLabel            `json:"item_blocks"`
	Messages   AssetListMessagesSchema `json:"messages"`
	UI         AssetListUISchema       `json:"ui"`
	// Execution 是 #328 的自动制作段，可选。未声明时不启动 runner、不注册
	// 在场/运行路由，清单保持 #327 行为；声明但 enabled=false 时 runner 不
	// 启动，运行控制返回不可用。
	Execution *AssetListExecutionSchema `json:"execution,omitempty"`
}

// AssetListExecutionSchema 是自动制作的调度与在场参数。poll_interval、
// max_*、grace 与 continuation 都是 server-only；浏览器只拿到心跳间隔。
type AssetListExecutionSchema struct {
	Enabled                  bool                        `json:"enabled"`
	PollIntervalMilliseconds int                         `json:"poll_interval_milliseconds"`
	MaxParallelItems         int                         `json:"max_parallel_items"`
	MaxRoundsPerItem         int                         `json:"max_rounds_per_item"`
	MaxCapacityRetries       int                         `json:"max_capacity_retries"`
	Presence                 AssetListPresenceSchema     `json:"presence"`
	Continuation             AssetListContinuationSchema `json:"continuation"`
}

// AssetListPresenceSchema 是前端心跳与在场宽限：heartbeat 公开，grace 只用于
// 服务端判断“用户已离开”。
type AssetListPresenceSchema struct {
	HeartbeatSeconds int `json:"heartbeat_seconds"`
	GraceSeconds     int `json:"grace_seconds"`
}

// AssetListContinuationSchema 是开轮/续轮时写给 Agent 的文案，必须带
// {name}、{description}、{route}、{round} 四个占位符。
type AssetListContinuationSchema struct {
	Message string `json:"message"`
}

// AssetListLimitsSchema 是清单的数量与文本边界。每个项目保留的清单数只计
// draft+confirmed，终态不占额度。
type AssetListLimitsSchema struct {
	MaxListsPerProject int `json:"max_lists_per_project"`
	// DraftTTLSeconds 是草稿的存活时间（7 天）。读取时派生过期状态，确认/
	// 拒绝事务内才落库。
	DraftTTLSeconds      int `json:"draft_ttl_seconds"`
	NameMaxLength        int `json:"name_max_length"`
	DescriptionMaxLength int `json:"description_max_length"`
	// MaxReferencesPerItem 是单项引用的上限：接受本会话/分支的 @imgN 与本
	// 项目的 @projectN，提案时解析成项目引用只存 ID。
	MaxReferencesPerItem int `json:"max_references_per_item"`
}

// AssetListDecomposeSchema 是 application.asset_list.decompose（#331）：从一张
// 源图拆出多个清单项的边界与文案。maximum_items_per_image 就是裁剪服务的
// 每图上限，后续提案阶段读同一个 Catalog 值；crop 的数值只用于服务端，不进
// 公开投影。
type AssetListDecomposeSchema struct {
	// MaximumItemsPerImage 是一张源图最多拆出的资产项（默认 24）：场景图常见
	// 10–20 个物体，每张裁图都是独立输入资产，需要限制存储与编码成本。
	MaximumItemsPerImage int                          `json:"maximum_items_per_image"`
	Crop                 AssetListDecomposeCropSchema `json:"crop"`
	// DefaultRouteWithReference 是清单项带裁剪参考图时采用的路线：必须先重画
	// 成干净单主体图再进 3D，所以它必须是本段已声明的路线且第一步为 2D 能力。
	DefaultRouteWithReference string `json:"default_route_with_reference"`
	// AgentPrompt 是拆解要领，由服务端注入 Agent 系统提示（与 step_policy 同类
	// 注入，不承担用户/工具结果文案）；{maximum} 由 maximum_items_per_image
	// 填充。它只进 Schema，不进公开投影。
	AgentPrompt string `json:"agent_prompt"`
	// Messages 是拆解与参考图校验的拒绝文案。占位符 {minimum} 是区域最短边，
	// {maximum} 是单图资产项上限。
	Messages AssetListDecomposeMessagesSchema `json:"messages"`
}

// AssetListDecomposeCropSchema 是裁图的尺寸限制：区域最短边像素、单块区域
// 最大像素与编码后 PNG 字节上限。
type AssetListDecomposeCropSchema struct {
	MinimumShortEdge    int   `json:"minimum_short_edge"`
	MaximumRegionPixels int64 `json:"maximum_region_pixels"`
	MaximumPNGBytes     int64 `json:"maximum_png_bytes"`
}

// AssetListDecomposeMessagesSchema 是单图拆解的拒绝文案。
type AssetListDecomposeMessagesSchema struct {
	// RegionInvalid 拒绝越界或没有面积的区域。
	RegionInvalid string `json:"region_invalid"`
	// RegionTooSmall 拒绝最短边不足的区域。{minimum}
	RegionTooSmall string `json:"region_too_small"`
	// ImageItemLimit 拒绝超过单图资产项上限的拆解。{maximum}
	ImageItemLimit string `json:"image_item_limit"`
	// ReferenceInvalid 拒绝不存在、非图片或违反每项一张/同一源图规则的裁剪
	// 参考图。
	ReferenceInvalid string `json:"reference_invalid"`
}

// AssetListKindSchema 是清单项类型到项目库类型的映射：character→character，
// weapon/prop→prop，vehicle/scene/image→other。更细的项目库分类留给 #329。
type AssetListKindSchema struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	ProjectKind string `json:"project_kind"`
	// DefaultRoute 是新建该项时缺省采用的路线 id，必须在本段Routes 中声明。
	DefaultRoute string `json:"default_route"`
}

// AssetListRouteSchema 是一条能力路线：Kinds 列出它适用的清单项类型，
// Steps 是计划中的能力序列（顺序即执行顺序）。
type AssetListRouteSchema struct {
	ID    string                `json:"id"`
	Label string                `json:"label"`
	Kinds []string              `json:"kinds"`
	Steps []AssetListStepSchema `json:"steps"`
}

// AssetListStepSchema 是路线中的一步。能力 id 必须出现在 Catalog 的已知
// 能力集合中；路线只描述“计划”，不绑定具体供给项——供给项在提案时解析
// 可用性并快照，不可用就标出，不静默换模型。
type AssetListStepSchema struct {
	Capability schemacontract.Capability `json:"capability"`
}

// AssetListStateSchema 是一个清单或清单项状态的展示文案与角色。
type AssetListStateSchema struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Role  string `json:"role"`
}

// AssetListMessagesSchema 是服务端与 Agent 读到的清单文案。占位符由服务端
// 填充；{title}/{count}/{steps}/{cap}/{maximum}/{days}/{position}/{name}/{detail}
// 都保留原样。
type AssetListMessagesSchema struct {
	Confirmed                  string `json:"confirmed"`
	Rejected                   string `json:"rejected"`
	Replaced                   string `json:"replaced"`
	Expired                    string `json:"expired"`
	LimitReached               string `json:"limit_reached"`
	ListNotFound               string `json:"list_not_found"`
	EmptyList                  string `json:"empty_list"`
	UnavailableItem            string `json:"unavailable_item"`
	StepCapWarning             string `json:"step_cap_warning"`
	All2DNote                  string `json:"all_2d_note"`
	ProjectAssetLimitWarning   string `json:"project_asset_limit_warning"`
	Proposed                   string `json:"proposed"`
	ProposeSessionNotInProject string `json:"propose_session_not_in_project"`
	ProposeInvalidItems        string `json:"propose_invalid_items"`
	ProposeReferenceUnresolved string `json:"propose_reference_unresolved"`
	ProposeFailed              string `json:"propose_failed"`
	// #328 自动制作：暂停/恢复/停止、续轮与失败分类。
	PausePresence      string `json:"pause_presence"`
	PauseUser          string `json:"pause_user"`
	PauseStepCap       string `json:"pause_step_cap"`
	PauseFailure       string `json:"pause_failure"`
	Resumed            string `json:"resumed"`
	Stopped            string `json:"stopped"`
	Skipped            string `json:"skipped"`
	RoundLimit         string `json:"round_limit"`
	AwaitingReview     string `json:"awaiting_review"`
	NeedsYou           string `json:"needs_you"`
	ItemFailed         string `json:"item_failed"`
	ItemFailedInput    string `json:"item_failed_input"`
	ItemFailedCapacity string `json:"item_failed_capacity"`
	ItemFailedRefused  string `json:"item_failed_refused"`
	ItemFailedInternal string `json:"item_failed_internal"`
	StepCapBlocked     string `json:"step_cap_blocked"`
	NeedsApproval      string `json:"needs_approval"`
	RunWaiting         string `json:"run_waiting"`
	// #328 完成工具 submit_asset_list_item 的结果与拒绝文案。
	ItemReportedDone     string `json:"item_reported_done"`
	ItemReportedFailed   string `json:"item_reported_failed"`
	ItemNotInSession     string `json:"item_not_in_session"`
	ItemNotWorking       string `json:"item_not_working"`
	ItemNoSuccessfulTask string `json:"item_no_successful_task"`
	ItemReportFailed     string `json:"item_report_failed"`
}

// AssetListUISchema 是清单卡与项目面板的固定标题和按钮文案。
type AssetListUISchema struct {
	CardTitle     string `json:"card_title"`
	PanelTitle    string `json:"panel_title"`
	ConfirmButton string `json:"confirm_button"`
	RejectButton  string `json:"reject_button"`
	ReplaceButton string `json:"replace_button"`
	EmptyText     string `json:"empty_text"`
}

// PublicAssetListSchema 是 application.asset_list 的浏览器投影：只放清单卡
// 与项目面板需要的 limits、kinds（id/label/project_kind/default_route）、
// routes（id/label/kinds/steps 的能力 id）与 states/item_states（id/label/role）。
// 服务端文案 messages、UI 文案、审计与预留的 item_blocks 不公开。
type PublicAssetListSchema struct {
	Limits     PublicAssetListLimitsSchema  `json:"limits"`
	Kinds      []PublicAssetListKindSchema  `json:"kinds"`
	Routes     []PublicAssetListRouteSchema `json:"routes"`
	States     []PublicAssetListStateSchema `json:"states"`
	ItemStates []PublicAssetListStateSchema `json:"item_states"`
	// Presence 只在声明 execution 时出现：浏览器需要心跳间隔；grace、
	// 轮询间隔、并行/轮数/重试与续轮文案都不公开。
	Presence *PublicAssetListPresenceSchema `json:"presence,omitempty"`
}

// PublicAssetListLimitsSchema 是浏览器可见的清单上限：清单自身的数量与文本
// 边界，加上可公开的单图拆解上限（浏览器据此解释拆解卡）。裁图几何与字节
// 限制留在私有 decompose 段。
type PublicAssetListLimitsSchema struct {
	AssetListLimitsSchema
	MaximumItemsPerImage int `json:"maximum_items_per_image"`
}

// PublicAssetListPresenceSchema 是公开的在场参数：只有心跳间隔。
type PublicAssetListPresenceSchema struct {
	HeartbeatSeconds int `json:"heartbeat_seconds"`
}

// PublicAssetListKindSchema 是公开的清单项类型：id、label、映射到的项目库
// 类型与缺省路线 id。
type PublicAssetListKindSchema struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	ProjectKind  string `json:"project_kind"`
	DefaultRoute string `json:"default_route"`
}

// PublicAssetListRouteSchema 是公开的路线：id、label、适用的清单项类型与
// 计划能力序列。
type PublicAssetListRouteSchema struct {
	ID    string                `json:"id"`
	Label string                `json:"label"`
	Kinds []string              `json:"kinds"`
	Steps []AssetListStepSchema `json:"steps"`
}

// PublicAssetListStateSchema 是公开的状态：浏览器只按 role 渲染，不自行
// 维护状态含义。
type PublicAssetListStateSchema struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Role  string `json:"role"`
}
