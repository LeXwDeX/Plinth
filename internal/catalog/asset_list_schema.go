package catalog

import schemacontract "github.com/LeXwDeX/Plinth/internal/schema"

// AssetListExecutionEnabled 报告 Catalog 是否开启了清单的自动制作闭环：
// application.asset_list.execution 且 enabled=true。未声明该段或 enabled=false
// 时保持 #327 行为——清单可以提出、可以读，但没有 run 能推进它，项也进不到
// working（#494）。
func (s ApplicationSchema) AssetListExecutionEnabled() bool {
	return s.AssetList != nil && s.AssetList.Execution != nil && s.AssetList.Execution.Enabled
}

// IsAssetListTool 报告 name 是否是推动清单自动闭环的 Agent 工具：#327 的
// propose_asset_list、#328 的 submit_asset_list_item 与 #329f 的 get_asset_list。
// Catalog 仍在 chat.agent_tools 里声明它们（能力不删），但闭环关闭时它们做不成
// 事，所以注入层把这三个名字留在默认工具表之外（#494）。
func IsAssetListTool(name string) bool {
	switch name {
	case "propose_asset_list", "submit_asset_list_item", "get_asset_list":
		return true
	}
	return false
}

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
	// Review is the acceptance board's decision contract (#329b), optional so
	// older Catalogs keep loading: without it the board can still be read and
	// items accepted, but no item may be returned for rework.
	Review *AssetListReviewSchema `json:"review,omitempty"`
	UI     AssetListUISchema      `json:"ui"`
	// Execution 是 #328 的自动制作段，可选。未声明时不启动 runner、不注册
	// 在场/运行路由，清单保持 #327 行为；声明但 enabled=false 时 runner 不
	// 启动，运行控制返回不可用。
	Execution *AssetListExecutionSchema `json:"execution,omitempty"`
	// DeliverySettleMaxSeconds 是 #459 的交付稳定检查上限：一个报告完成、
	// 但仍有格式在后台转换的清单项按 Offering 的成功后观察间隔
	// （post_success.interval_seconds）安排下一次检查，并以这个秒数封顶，
	// 避免长间隔把项一直留在 working。0（未声明）表示不封顶；只用于服务端，
	// 不进公开投影。
	DeliverySettleMaxSeconds int `json:"delivery_settle_max_seconds,omitempty"`
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
// 源图拆出多个清单项的边界与文案。maximum_items_per_image 是产品口径的每图
// 项数上限，提案与编辑按它拒绝；裁剪服务另有按它四倍配置的存储护栏，用来
// 容纳反复重框累积的旧裁图。crop 的区域像素与字节数值只用于服务端，不进
// 公开投影。
type AssetListDecomposeSchema struct {
	// MaximumItemsPerImage 是一张源图最多被清单引用的资产项（默认 24）：场景
	// 图常见 10–20 个物体，每张裁图都是独立输入资产，需要限制存储与编码成本。
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
	// ImageTooLarge 拒绝超过字节或像素上限的源图、区域或裁图。
	ImageTooLarge string `json:"image_too_large"`
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
	// #329f 清单总结：一次运行全部结束后，平台把一段总结写进清单会话（不是
	// Agent 的一回合，不出卡片、不计步）。RunSummary 是首行，占位符
	// {title} {accepted} {returned} {failed} {skipped} {steps}；{returned} 是
	// 至少被打回过一次的项数（清单结束时没有项还停在 returned），{steps} 是
	// 逐项实际消耗之和。RunSummaryItem 与 RunSummaryItemFailed 是逐项行，占位符
	// {name} {state} {steps} 与 {reason}；失败原因取 RunSummaryReason* 里与该
	// state_reason 同名的那一条，round_limit（#328 的轮数上限）也是一条原因。
	RunSummary                       string `json:"run_summary"`
	RunSummaryItem                   string `json:"run_summary_item"`
	RunSummaryItemFailed             string `json:"run_summary_item_failed"`
	RunSummaryReasonInputInvalid     string `json:"run_summary_reason_input_invalid"`
	RunSummaryReasonParameterInvalid string `json:"run_summary_reason_parameter_invalid"`
	RunSummaryReasonUpstreamCapacity string `json:"run_summary_reason_upstream_capacity"`
	RunSummaryReasonUpstreamRefused  string `json:"run_summary_reason_upstream_refused"`
	RunSummaryReasonInternal         string `json:"run_summary_reason_internal"`
	RunSummaryReasonRoundLimit       string `json:"run_summary_reason_round_limit"`
	// #328 完成工具 submit_asset_list_item 的结果与拒绝文案。
	ItemReportedDone     string `json:"item_reported_done"`
	ItemReportedFailed   string `json:"item_reported_failed"`
	ItemNotInSession     string `json:"item_not_in_session"`
	ItemNotWorking       string `json:"item_not_working"`
	ItemNoSuccessfulTask string `json:"item_no_successful_task"`
	// ItemDeliveryPending is the completion tool's answer when the reported
	// Task still owes formats as post-success resources (#459): the item keeps
	// its state until the delivery is stable, and the report is stored.
	ItemDeliveryPending string `json:"item_delivery_pending,omitempty"`
	ItemReportFailed    string `json:"item_report_failed"`
}

// AssetListReviewSchema 是 application.asset_list.review（#329b）：验收看板的
// 打回原因、重做上限与文案。打回原因按清单项类型可见：kinds 为空表示对全部
// 类型可见；显式列出时只对其中类型可见。max_rework_rounds 是同一项可被打回
// 的次数（版次从 1 起，round 超过它后只能通过或跳过）。
type AssetListReviewSchema struct {
	ReturnReasons   []AssetListReturnReasonSchema `json:"return_reasons"`
	MaxReworkRounds int                           `json:"max_rework_rounds"`
	Messages        AssetListReviewMessagesSchema `json:"messages"`
	// Archive is the batch delivery of accepted items (#329c), optional so a
	// Catalog without it keeps the route unregistered: without it there is no
	// server-side packaging, no limits and no error copy.
	Archive *AssetListArchiveSchema `json:"archive,omitempty"`
}

// AssetListArchiveSchema 是已通过项的整批 ZIP（#329c）：文件名模板、每个清单项
// 一个目录的命名边界、包内清单文件与文件数/字节上限。上限在预检阶段生效，超出
// 时返回 413 并提示用 item_ids 分批下载。
type AssetListArchiveSchema struct {
	// FilenameTemplate uses {list_id}.
	FilenameTemplate string `json:"filename_template"`
	MediaType        string `json:"media_type"`
	// ManifestFilename and ReadmeFilename are the two root entries of the ZIP.
	ManifestFilename string `json:"manifest_filename"`
	ReadmeFilename   string `json:"readme_filename"`
	// DirectoryNameMaxBytes bounds one item directory's name (after the
	// sanitized item name), so a long Agent-written name cannot exceed filesystem
	// limits after extraction.
	DirectoryNameMaxBytes int `json:"directory_name_max_bytes"`
	// MaxEntries counts every ZIP entry including manifest and README.
	MaxEntries int   `json:"max_entries"`
	MaxBytes   int64 `json:"max_bytes"`
	// StoredMediaTypes are already compressed and are stored, not deflated.
	StoredMediaTypes []string                       `json:"stored_media_types"`
	Readme           AssetListArchiveReadmeSchema   `json:"readme"`
	Messages         AssetListArchiveMessagesSchema `json:"messages"`
	Audit            AssetListArchiveAuditSchema    `json:"audit"`
}

// AssetListArchiveReadmeSchema 是整批 ZIP 根目录 README.txt 的文案：每个
// 清单项一个块，块内是该产物的目录、来源、格式、验收与文件清单。占位符
// {title} {list_id} {generated_at} {items} {files} {position} {name} {kind}
// {round} {directory} {task_id} {formats} {triangles} {texture} {checks}
// {check} {status} {budget} {path} {role} {format} {size} {sha256}
// {asset_id} {media_type} 由服务端填充。
type AssetListArchiveReadmeSchema struct {
	// TitleLine、ListLine、GeneratedLine 与 Note 是包头的四行。
	TitleLine     string `json:"title_line"`
	ListLine      string `json:"list_line"`
	GeneratedLine string `json:"generated_line"`
	Note          string `json:"note"`
	// SummaryLine 收尾包头。{items} 是项数、{files} 是文件数。
	SummaryLine string `json:"summary_line"`
	// ItemLine 与 ItemKindLine 是一项的标题：声明了 kind 时用后者。
	ItemLine     string `json:"item_line"`
	ItemKindLine string `json:"item_kind_line"`
	// FormatsLine、TrianglesLine 与 TextureLine 是一项的归档事实；没写的
	// 事实不输出对应行。FormatSeparator 是 FormatsLine 里多个格式之间的分隔。
	DirectoryLine   string `json:"directory_line"`
	TaskLine        string `json:"task_line"`
	FormatsLine     string `json:"formats_line"`
	FormatSeparator string `json:"format_separator"`
	TrianglesLine   string `json:"triangles_line"`
	TextureLine     string `json:"texture_line"`
	// ChecksLine 与 CheckLine/CheckBudgetLine 是验收结论：一条检查一行文案，
	// 带阈值时用带 {budget} 的那条。CheckSeparator 是两条之间的分隔。
	ChecksLine      string `json:"checks_line"`
	CheckLine       string `json:"check_line"`
	CheckBudgetLine string `json:"check_budget_line"`
	CheckSeparator  string `json:"check_separator"`
	// FilesHeading 与 FileLine 是一项的文件清单；MissingHeading 与
	// MissingLine 是快照记录了、但存储里已经没有的文件。
	FilesHeading   string `json:"files_heading"`
	FileLine       string `json:"file_line"`
	MissingHeading string `json:"missing_heading"`
	MissingLine    string `json:"missing_line"`
	// UnknownRole 是缺失文件既没有 role 也没有 variant 时的角色词。
	UnknownRole string `json:"unknown_role"`
}

// AssetListArchiveMessagesSchema 是整批 ZIP 的拒绝文案。empty 是没有任何已通过
// 且带产物的项；too_large 超过上限并用 {maximum_entries}/{maximum_bytes} 指明
// 边界。
type AssetListArchiveMessagesSchema struct {
	Empty    string `json:"empty"`
	TooLarge string `json:"too_large"`
}

// AssetListArchiveAuditSchema 是整批下载的审计事件：动作值必须在
// application.audit_actions 中声明。
type AssetListArchiveAuditSchema struct {
	Action       string                            `json:"action"`
	ResourceType string                            `json:"resource_type"`
	DetailFields AssetListArchiveAuditDetailFields `json:"detail_fields"`
}

type AssetListArchiveAuditDetailFields struct {
	ListID  string `json:"list_id"`
	Items   string `json:"items"`
	Entries string `json:"entries"`
	Bytes   string `json:"bytes"`
}

// AssetListReturnReasonSchema 是一条打回原因。id 是固定的服务端值，label 由
// 浏览器展示，kinds 限定可见的清单项类型。
type AssetListReturnReasonSchema struct {
	ID    string   `json:"id"`
	Label string   `json:"label"`
	Kinds []string `json:"kinds"`
}

// AssetListReviewMessagesSchema 是验收流程的服务端文案。占位符 {name}、
// {description}、{round}、{reasons}、{note}、{maximum} 由服务端填充；打回
// 续轮消息会作为该子会话下一轮的用户消息。
type AssetListReviewMessagesSchema struct {
	// ReworkMessage 是打回后同一子会话续轮的文案。
	ReworkMessage string `json:"rework_message"`
	// ReturnLimit 拒绝超过重做上限的打回。{name}、{round}
	ReturnLimit string `json:"return_limit"`
	// AssetLimit 是项目资产达到上限时的通过失败提示。{name}、{maximum}
	AssetLimit string `json:"asset_limit"`
	// ItemNotFound 是整批通过里一个 id 不属于这份清单时该单项的结果。
	ItemNotFound string `json:"item_not_found"`
	// ItemNotAwaitingReview 是整批通过里一项已不在待验收时该单项的结果。
	ItemNotAwaitingReview string `json:"item_not_awaiting_review"`
	// Budget* 是验收快照里一条冻结阈值的措辞：{value} 是阈值本身。它们
	// 进快照，所以是被检查项的门禁文案，不随读取时的语言变化。
	BudgetMinimum      string `json:"budget_minimum"`
	BudgetMaximum      string `json:"budget_maximum"`
	BudgetRatioMaximum string `json:"budget_ratio_maximum"`
	BudgetRequired     string `json:"budget_required"`
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
	// Review 只在声明 review 时出现：浏览器要列出打回原因并显示重做上限。
	Review *PublicAssetListReviewSchema `json:"review,omitempty"`
}

// PublicAssetListReviewSchema 是验收看板的公开参数：打回原因、重做上限与整批
// 下载的文件数/字节上限（浏览器据此提示分批下载）。续轮、拒绝文案与审计留在
// 服务端。
type PublicAssetListReviewSchema struct {
	ReturnReasons   []PublicAssetListReturnReasonSchema `json:"return_reasons"`
	MaxReworkRounds int                                 `json:"max_rework_rounds"`
	Archive         *PublicAssetListArchiveSchema       `json:"archive,omitempty"`
}

// PublicAssetListArchiveSchema 是整批 ZIP 的公开上限。
type PublicAssetListArchiveSchema struct {
	MaxEntries int   `json:"max_entries"`
	MaxBytes   int64 `json:"max_bytes"`
}

// PublicAssetListReturnReasonSchema 是一条公开的打回原因。
type PublicAssetListReturnReasonSchema struct {
	ID    string   `json:"id"`
	Label string   `json:"label"`
	Kinds []string `json:"kinds"`
}

// PublicAssetListLimitsSchema 是浏览器可见的清单上限：清单自身的数量与文本
// 边界，加上可公开的单图拆解上限（浏览器据此解释拆解卡）。裁图区域的像素与
// 字节上限留在私有 decompose 段；裁图的最短边是编辑器在本地先拒绝过小框选
// 所需的公开值。
type PublicAssetListLimitsSchema struct {
	AssetListLimitsSchema
	MaximumItemsPerImage int `json:"maximum_items_per_image"`
	MinimumCropShortEdge int `json:"minimum_crop_short_edge"`
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
