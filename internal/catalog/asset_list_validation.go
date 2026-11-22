package catalog

import (
	"errors"
	"fmt"
	"strings"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// 清单项类型与状态词表（#327）。Schema 拥有展示文案，服务端拥有这些 id；
// 校验要求声明恰好覆盖词表，避免 #328/#329 的写入边界出现未命名状态。
const assetListCoverage = "asset_list"

var (
	assetListKinds        = []string{"character", "weapon", "prop", "vehicle", "scene", "image"}
	assetListStateIDs     = []string{"draft", "confirmed", "rejected", "expired", "superseded"}
	assetListItemStateIDs = []string{"todo", "working", "review", "accepted", "returned", "failed", "skipped"}
	assetListProjectKinds = []string{"character", "prop", "style_reference", "other"}
	assetListItemBlocks   = []string{"awaiting_approval", "paused", "step_cap"}
)

// validateAssetList 校验 application.asset_list（#327）。未声明该段时直接放行，
// 旧 Catalog 仍可加载。
func validateAssetList(
	schema ApplicationSchema,
	capabilities map[schemacontract.Capability]struct{},
) error {
	list := schema.AssetList
	if list == nil {
		return nil
	}
	// 声明清单就必须声明步数策略：否则 3D 计划步数无从计算，会静默为 0。
	if schema.StepPolicy == nil {
		return errors.New("application.asset_list 需要同时声明 application.step_policy（3D 计划步数口径）")
	}

	limits := list.Limits
	if limits.MaxListsPerProject <= 0 ||
		limits.DraftTTLSeconds <= 0 ||
		limits.NameMaxLength <= 0 ||
		limits.DescriptionMaxLength <= 0 {
		return errors.New("asset_list.limits 的数量、草稿时长与名称/描述上限必须为正数")
	}
	if limits.MaxReferencesPerItem < 1 || limits.MaxReferencesPerItem > 8 {
		return errors.New("asset_list.limits.max_references_per_item 必须在 1 到 8 之间")
	}
	// #459: 0 means "not declared" so historical Catalogs keep loading; a
	// declared cap must be a usable scheduling bound.
	if list.DeliverySettleMaxSeconds < 0 || list.DeliverySettleMaxSeconds > 3600 {
		return errors.New("asset_list.delivery_settle_max_seconds 必须在 0 到 3600 之间")
	}
	if list.DeliverySettleMaxSeconds > 0 && list.DeliverySettleMaxSeconds < 30 {
		return errors.New("asset_list.delivery_settle_max_seconds 不能小于 30 秒")
	}

	declaredKinds := make(map[string]AssetListKindSchema, len(list.Kinds))
	if len(list.Kinds) == 0 {
		return errors.New("asset_list.kinds 不能为空")
	}
	allowedKinds := stringSet(assetListKinds)
	allowedProjectKinds := stringSet(assetListProjectKinds)
	for index, kind := range list.Kinds {
		if _, allowed := allowedKinds[kind.ID]; !allowed {
			return fmt.Errorf("asset_list.kinds[%d].id 非法: %q", index, kind.ID)
		}
		if _, duplicate := declaredKinds[kind.ID]; duplicate {
			return fmt.Errorf("asset_list.kinds 的 id 重复: %q", kind.ID)
		}
		if strings.TrimSpace(kind.Label) == "" {
			return fmt.Errorf("asset_list.kinds[%d].label 不能为空", index)
		}
		if _, allowed := allowedProjectKinds[kind.ProjectKind]; !allowed {
			return fmt.Errorf("asset_list.kinds[%d].project_kind 非法: %q", index, kind.ProjectKind)
		}
		declaredKinds[kind.ID] = kind
	}

	declaredRoutes := make(map[string]AssetListRouteSchema, len(list.Routes))
	if len(list.Routes) == 0 {
		return errors.New("asset_list.routes 不能为空")
	}
	for index, route := range list.Routes {
		if strings.TrimSpace(route.ID) == "" || strings.TrimSpace(route.Label) == "" {
			return fmt.Errorf("asset_list.routes[%d] 的 id 和 label 不能为空", index)
		}
		if _, duplicate := declaredRoutes[route.ID]; duplicate {
			return fmt.Errorf("asset_list.routes 的 id 重复: %q", route.ID)
		}
		if len(route.Kinds) == 0 {
			return fmt.Errorf("asset_list.routes[%d].kinds 不能为空", index)
		}
		for _, kind := range route.Kinds {
			if _, exists := declaredKinds[kind]; !exists {
				return fmt.Errorf("asset_list.routes[%d].kinds 引用了未知清单项类型 %q", index, kind)
			}
		}
		if len(route.Steps) == 0 {
			return fmt.Errorf("asset_list.routes[%d].steps 不能为空", index)
		}
		for stepIndex, step := range route.Steps {
			if _, known := capabilities[step.Capability]; !known {
				return fmt.Errorf(
					"asset_list.routes[%d].steps[%d] 引用了未知能力 %q",
					index,
					stepIndex,
					step.Capability,
				)
			}
		}
		declaredRoutes[route.ID] = route
	}
	for index, kind := range list.Kinds {
		route, exists := declaredRoutes[kind.DefaultRoute]
		if !exists {
			return fmt.Errorf(
				"asset_list.kinds[%d].default_route 引用了未知路线 %q",
				index,
				kind.DefaultRoute,
			)
		}
		if !containsString(route.Kinds, kind.ID) {
			return fmt.Errorf(
				"asset_list.kinds[%d].default_route %q 的 kinds 不含该类型 %q",
				index,
				kind.DefaultRoute,
				kind.ID,
			)
		}
	}

	if err := validateAssetListDecompose(list.Decompose, declaredRoutes, schema.Chat.Context); err != nil {
		return err
	}

	if err := validateAssetListStates("asset_list.states", list.States, assetListStateIDs); err != nil {
		return err
	}
	if err := validateAssetListStates("asset_list.item_states", list.ItemStates, assetListItemStateIDs); err != nil {
		return err
	}
	if err := validateValueLabels("asset_list.item_blocks", list.ItemBlocks); err != nil {
		return err
	}
	allowedBlocks := stringSet(assetListItemBlocks)
	for index, block := range list.ItemBlocks {
		if _, allowed := allowedBlocks[block.Value]; !allowed {
			return fmt.Errorf("asset_list.item_blocks[%d] 只允许预留值 %v: %q", index, assetListItemBlocks, block.Value)
		}
	}

	for field, text := range map[string]string{
		"confirmed":                            list.Messages.Confirmed,
		"rejected":                             list.Messages.Rejected,
		"replaced":                             list.Messages.Replaced,
		"expired":                              list.Messages.Expired,
		"limit_reached":                        list.Messages.LimitReached,
		"list_not_found":                       list.Messages.ListNotFound,
		"empty_list":                           list.Messages.EmptyList,
		"unavailable_item":                     list.Messages.UnavailableItem,
		"step_cap_warning":                     list.Messages.StepCapWarning,
		"all_2d_note":                          list.Messages.All2DNote,
		"project_asset_limit_warning":          list.Messages.ProjectAssetLimitWarning,
		"proposed":                             list.Messages.Proposed,
		"propose_session_not_in_project":       list.Messages.ProposeSessionNotInProject,
		"propose_invalid_items":                list.Messages.ProposeInvalidItems,
		"propose_reference_unresolved":         list.Messages.ProposeReferenceUnresolved,
		"propose_failed":                       list.Messages.ProposeFailed,
		"pause_presence":                       list.Messages.PausePresence,
		"pause_user":                           list.Messages.PauseUser,
		"pause_step_cap":                       list.Messages.PauseStepCap,
		"pause_failure":                        list.Messages.PauseFailure,
		"resumed":                              list.Messages.Resumed,
		"stopped":                              list.Messages.Stopped,
		"skipped":                              list.Messages.Skipped,
		"round_limit":                          list.Messages.RoundLimit,
		"awaiting_review":                      list.Messages.AwaitingReview,
		"needs_you":                            list.Messages.NeedsYou,
		"item_failed":                          list.Messages.ItemFailed,
		"item_failed_input":                    list.Messages.ItemFailedInput,
		"item_failed_capacity":                 list.Messages.ItemFailedCapacity,
		"item_failed_refused":                  list.Messages.ItemFailedRefused,
		"item_failed_internal":                 list.Messages.ItemFailedInternal,
		"step_cap_blocked":                     list.Messages.StepCapBlocked,
		"needs_approval":                       list.Messages.NeedsApproval,
		"run_waiting":                          list.Messages.RunWaiting,
		"run_summary":                          list.Messages.RunSummary,
		"run_summary_item":                     list.Messages.RunSummaryItem,
		"run_summary_item_failed":              list.Messages.RunSummaryItemFailed,
		"run_summary_reason_input_invalid":     list.Messages.RunSummaryReasonInputInvalid,
		"run_summary_reason_parameter_invalid": list.Messages.RunSummaryReasonParameterInvalid,
		"run_summary_reason_upstream_capacity": list.Messages.RunSummaryReasonUpstreamCapacity,
		"run_summary_reason_upstream_refused":  list.Messages.RunSummaryReasonUpstreamRefused,
		"run_summary_reason_internal":          list.Messages.RunSummaryReasonInternal,
		"run_summary_reason_round_limit":       list.Messages.RunSummaryReasonRoundLimit,
		"item_reported_done":                   list.Messages.ItemReportedDone,
		"item_reported_failed":                 list.Messages.ItemReportedFailed,
		"item_not_in_session":                  list.Messages.ItemNotInSession,
		"item_not_working":                     list.Messages.ItemNotWorking,
		"item_no_successful_task":              list.Messages.ItemNoSuccessfulTask,
		"item_report_failed":                   list.Messages.ItemReportFailed,
		"item_delivery_pending":                list.Messages.ItemDeliveryPending,
	} {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("asset_list.messages.%s 不能为空", field)
		}
	}
	for field, text := range map[string]string{
		"card_title":     list.UI.CardTitle,
		"panel_title":    list.UI.PanelTitle,
		"confirm_button": list.UI.ConfirmButton,
		"reject_button":  list.UI.RejectButton,
		"replace_button": list.UI.ReplaceButton,
		"empty_text":     list.UI.EmptyText,
	} {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("asset_list.ui.%s 不能为空", field)
		}
	}

	if err := validateAssetListExecution(list.Execution); err != nil {
		return err
	}
	if err := validateAssetListReview(list.Review, declaredKinds, schema.AuditActions); err != nil {
		return err
	}

	// 路由键只为 #327 PR3 使用；声明清单时这几条都必须存在，注册与否由 HTTP
	// 层决定。
	routes := schema.Runtime.HTTP.Routes
	for name, route := range map[string]string{
		"project_asset_lists":         routes.ProjectAssetLists,
		"project_asset_list":          routes.ProjectAssetList,
		"project_asset_list_decision": routes.ProjectAssetListDecision,
		// #329a: the read-only acceptance board of one list.
		"project_asset_list_board": routes.ProjectAssetListBoard,
		"chat_session_asset_lists": routes.ChatSessionAssetLists,
	} {
		if strings.TrimSpace(route) == "" {
			return fmt.Errorf("application.asset_list 需要 runtime.http.routes.%s", name)
		}
	}
	// #329b：声明 review 段就必须声明验收决定路由；路由的注册仍由 HTTP 层
	// 决定，未声明 review 的旧 Catalog 保持只读看板。
	if list.Review != nil {
		if strings.TrimSpace(routes.ProjectAssetListItemReview) == "" {
			return errors.New("application.asset_list 声明 review 时需要 runtime.http.routes.project_asset_list_item_review")
		}
		// #329c：声明整批 ZIP 段就必须声明归档路由。
		if list.Review.Archive != nil && strings.TrimSpace(routes.ProjectAssetListArchive) == "" {
			return errors.New("application.asset_list.review.archive 需要 runtime.http.routes.project_asset_list_archive")
		}
	}
	// #328：开启自动制作时，在场与运行/单项控制路由必须声明；路由的注册
	// 仍由 HTTP 层决定。
	if list.Execution != nil && list.Execution.Enabled {
		for name, route := range map[string]string{
			"project_presence":        routes.ProjectPresence,
			"project_asset_list_run":  routes.ProjectAssetListRun,
			"project_asset_list_item": routes.ProjectAssetListItem,
		} {
			if strings.TrimSpace(route) == "" {
				return fmt.Errorf("asset_list.execution.enabled 需要 runtime.http.routes.%s", name)
			}
		}
	}
	return nil
}

// assetListDecompose2DCapabilities 是可以作为拆解参考路线第一步的 2D 能力：
// 先把裁图重画成干净单主体图，再进入三视图或 3D。
var assetListDecompose2DCapabilities = []schemacontract.Capability{
	schemacontract.TextToImage,
	schemacontract.ImageToImage,
}

// validateAssetListDecompose 校验可选的拆解段（#331）。未声明时放行；声明后
// 数值必须在既定范围内，缺省路线必须已声明且第一步是 2D 能力，五条拒绝文案
// 与 Agent 拆解要领都必须完整；子会话的裁图/源图标签也必须在 chat.context
// 中成对声明，否则清单项会被读成「用户上传」。
func validateAssetListDecompose(
	decompose *AssetListDecomposeSchema,
	declaredRoutes map[string]AssetListRouteSchema,
	context ChatContextSchema,
) error {
	if decompose == nil {
		return nil
	}
	if decompose.MaximumItemsPerImage < 1 || decompose.MaximumItemsPerImage > 100 {
		return errors.New("asset_list.decompose.maximum_items_per_image 必须在 1 到 100 之间")
	}
	crop := decompose.Crop
	if crop.MinimumShortEdge < 1 || crop.MinimumShortEdge > 4096 {
		return errors.New("asset_list.decompose.crop.minimum_short_edge 必须在 1 到 4096 之间")
	}
	minimumArea := int64(crop.MinimumShortEdge) * int64(crop.MinimumShortEdge)
	if crop.MaximumRegionPixels < minimumArea || crop.MaximumRegionPixels > 100_000_000 {
		return fmt.Errorf("asset_list.decompose.crop.maximum_region_pixels 必须在 %d 到 100000000 之间", minimumArea)
	}
	if crop.MaximumPNGBytes < 1 || crop.MaximumPNGBytes > 256<<20 {
		return errors.New("asset_list.decompose.crop.maximum_png_bytes 必须在 1 到 268435456 之间")
	}
	route, exists := declaredRoutes[decompose.DefaultRouteWithReference]
	if !exists {
		return fmt.Errorf(
			"asset_list.decompose.default_route_with_reference 引用了未知路线 %q",
			decompose.DefaultRouteWithReference,
		)
	}
	first := route.Steps[0].Capability
	if !containsCapability(assetListDecompose2DCapabilities, first) {
		return fmt.Errorf(
			"asset_list.decompose.default_route_with_reference %q 的第一步必须是 2D 能力 %v，实际是 %q",
			route.ID,
			assetListDecompose2DCapabilities,
			first,
		)
	}
	for field, text := range map[string]string{
		"region_invalid":    decompose.Messages.RegionInvalid,
		"region_too_small":  decompose.Messages.RegionTooSmall,
		"image_too_large":   decompose.Messages.ImageTooLarge,
		"image_item_limit":  decompose.Messages.ImageItemLimit,
		"reference_invalid": decompose.Messages.ReferenceInvalid,
	} {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("asset_list.decompose.messages.%s 不能为空", field)
		}
	}
	if strings.TrimSpace(decompose.AgentPrompt) == "" || !strings.Contains(decompose.AgentPrompt, "{maximum}") {
		return errors.New("asset_list.decompose.agent_prompt 必须声明并包含 {maximum}")
	}
	for _, role := range []string{"image.crop", "image.source_scene"} {
		if !strings.Contains(context.AttachmentLabelTemplates[role], "{handle}") {
			return fmt.Errorf("asset_list.decompose 需要 context.attachment_label_templates[%q]（含 {handle}）", role)
		}
		if !strings.Contains(context.UnpicturedAttachmentTemplates[role], "{handle}") {
			return fmt.Errorf("asset_list.decompose 需要 context.unpictured_attachment_templates[%q]（含 {handle}）", role)
		}
	}
	return nil
}

func containsCapability(values []schemacontract.Capability, target schemacontract.Capability) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// validateAssetListExecution 校验可选的自动制作段（#328）。未声明时放行；
// 声明后数值必须在既定范围内，续轮文案必须带齐四个占位符。
func validateAssetListExecution(execution *AssetListExecutionSchema) error {
	if execution == nil {
		return nil
	}
	if execution.PollIntervalMilliseconds < 500 || execution.PollIntervalMilliseconds > 60000 {
		return errors.New("asset_list.execution.poll_interval_milliseconds 必须在 500 到 60000 之间")
	}
	if execution.MaxParallelItems < 1 || execution.MaxParallelItems > 3 {
		return errors.New("asset_list.execution.max_parallel_items 必须在 1 到 3 之间")
	}
	if execution.MaxRoundsPerItem < 1 || execution.MaxRoundsPerItem > 10 {
		return errors.New("asset_list.execution.max_rounds_per_item 必须在 1 到 10 之间")
	}
	if execution.MaxCapacityRetries < 0 || execution.MaxCapacityRetries > 3 {
		return errors.New("asset_list.execution.max_capacity_retries 必须在 0 到 3 之间")
	}
	heartbeat := execution.Presence.HeartbeatSeconds
	if heartbeat < 5 || heartbeat > 60 {
		return errors.New("asset_list.execution.presence.heartbeat_seconds 必须在 5 到 60 之间")
	}
	grace := execution.Presence.GraceSeconds
	if grace < 30 || grace > 600 {
		return errors.New("asset_list.execution.presence.grace_seconds 必须在 30 到 600 之间")
	}
	if grace < 2*heartbeat {
		return errors.New("asset_list.execution.presence.grace_seconds 必须至少是 heartbeat_seconds 的两倍")
	}
	message := execution.Continuation.Message
	if strings.TrimSpace(message) == "" {
		return errors.New("asset_list.execution.continuation.message 不能为空")
	}
	placeholders := placeholderSet(message)
	for _, name := range []string{"name", "description", "route", "round"} {
		if !placeholders[name] {
			return fmt.Errorf("asset_list.execution.continuation.message 缺少占位符 {%s}", name)
		}
	}
	return nil
}

// validateAssetListReview 校验可选的验收段（#329b）。未声明时放行：看板只读，
// 通过仍可用，打回不可用。声明后原因 id 唯一、label 非空、kinds 必须是已声明
// 的清单项类型，重做上限在 1 到 10 之间，三条文案必须完整且带各自占位符。
func validateAssetListReview(review *AssetListReviewSchema, kinds map[string]AssetListKindSchema, auditActions []ValueLabel) error {
	if review == nil {
		return nil
	}
	if len(review.ReturnReasons) == 0 {
		return errors.New("asset_list.review.return_reasons 不能为空")
	}
	seen := make(map[string]bool, len(review.ReturnReasons))
	for index, reason := range review.ReturnReasons {
		id := strings.TrimSpace(reason.ID)
		if id == "" || strings.TrimSpace(reason.Label) == "" {
			return fmt.Errorf("asset_list.review.return_reasons[%d] 的 id 和 label 不能为空", index)
		}
		if seen[id] {
			return fmt.Errorf("asset_list.review.return_reasons 的 id 重复: %q", id)
		}
		seen[id] = true
		for _, kind := range reason.Kinds {
			if _, declared := kinds[kind]; !declared {
				return fmt.Errorf("asset_list.review.return_reasons[%d].kinds 引用了未知类型 %q", index, kind)
			}
		}
	}
	if review.MaxReworkRounds < 1 || review.MaxReworkRounds > 10 {
		return errors.New("asset_list.review.max_rework_rounds 必须在 1 到 10 之间")
	}
	for field, text := range map[string]string{
		"rework_message": review.Messages.ReworkMessage,
		"return_limit":   review.Messages.ReturnLimit,
		"asset_limit":    review.Messages.AssetLimit,
		// 逐项通过结果的两条：整批里有一项不该被通过时，该项自己的说明。
		"item_not_found":           review.Messages.ItemNotFound,
		"item_not_awaiting_review": review.Messages.ItemNotAwaitingReview,
		// 快照里的阈值措辞：一条阈值可能由最小/最大/比例/必填组合而成。
		"budget_minimum":       review.Messages.BudgetMinimum,
		"budget_maximum":       review.Messages.BudgetMaximum,
		"budget_ratio_maximum": review.Messages.BudgetRatioMaximum,
		"budget_required":      review.Messages.BudgetRequired,
	} {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("asset_list.review.messages.%s 不能为空", field)
		}
	}
	for field, text := range map[string]string{
		"budget_minimum":       review.Messages.BudgetMinimum,
		"budget_maximum":       review.Messages.BudgetMaximum,
		"budget_ratio_maximum": review.Messages.BudgetRatioMaximum,
	} {
		if !placeholderSet(text)["value"] {
			return fmt.Errorf("asset_list.review.messages.%s 缺少占位符 {value}", field)
		}
	}
	for _, name := range []string{"name", "description", "round", "reasons", "note"} {
		if !placeholderSet(review.Messages.ReworkMessage)[name] {
			return fmt.Errorf("asset_list.review.messages.rework_message 缺少占位符 {%s}", name)
		}
	}
	if !placeholderSet(review.Messages.ReturnLimit)["round"] {
		return errors.New("asset_list.review.messages.return_limit 缺少占位符 {round}")
	}
	if !placeholderSet(review.Messages.AssetLimit)["maximum"] {
		return errors.New("asset_list.review.messages.asset_limit 缺少占位符 {maximum}")
	}
	return validateAssetListArchive(review.Archive, auditActions)
}

// validateAssetListArchive 校验可选的整批 ZIP 段（#329c）。未声明时放行：服务端
// 不注册归档路由。声明后文件名模板必须带 {list_id}，两个根文件必须是不含路径
// 的独立名字，目录名上限与包上限必须为正数，两条拒绝文案必须完整，审计动作必须
// 在 audit_actions 中声明。
func validateAssetListArchive(archive *AssetListArchiveSchema, auditActions []ValueLabel) error {
	if archive == nil {
		return nil
	}
	if !strings.Contains(archive.FilenameTemplate, "{list_id}") {
		return errors.New("asset_list.review.archive.filename_template 缺少占位符 {list_id}")
	}
	if strings.TrimSpace(archive.MediaType) == "" {
		return errors.New("asset_list.review.archive.media_type 不能为空")
	}
	for field, name := range map[string]string{
		"manifest_filename": archive.ManifestFilename,
		"readme_filename":   archive.ReadmeFilename,
	} {
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			return fmt.Errorf("asset_list.review.archive.%s 必须是不含路径的文件名", field)
		}
	}
	if strings.EqualFold(strings.TrimSpace(archive.ManifestFilename), strings.TrimSpace(archive.ReadmeFilename)) {
		return errors.New("asset_list.review.archive 的 manifest_filename 与 readme_filename 不能相同")
	}
	if archive.DirectoryNameMaxBytes < 1 || archive.DirectoryNameMaxBytes > 255 {
		return errors.New("asset_list.review.archive.directory_name_max_bytes 必须在 1 到 255 之间")
	}
	if archive.MaxEntries < 1 || archive.MaxEntries > 10000 {
		return errors.New("asset_list.review.archive.max_entries 必须在 1 到 10000 之间")
	}
	if archive.MaxBytes < 1 || archive.MaxBytes > 8<<30 {
		return errors.New("asset_list.review.archive.max_bytes 必须在 1 到 8589934592 之间")
	}
	if strings.TrimSpace(archive.Messages.Empty) == "" {
		return errors.New("asset_list.review.archive.messages.empty 不能为空")
	}
	if !placeholderSet(archive.Messages.TooLarge)["maximum_entries"] ||
		!placeholderSet(archive.Messages.TooLarge)["maximum_bytes"] {
		return errors.New("asset_list.review.archive.messages.too_large 需要 {maximum_entries} 与 {maximum_bytes}")
	}
	if err := validateAssetListArchiveReadme(archive.Readme); err != nil {
		return err
	}
	audit := archive.Audit
	if strings.TrimSpace(audit.Action) == "" || strings.TrimSpace(audit.ResourceType) == "" ||
		audit.DetailFields.ListID == "" || audit.DetailFields.Items == "" ||
		audit.DetailFields.Entries == "" || audit.DetailFields.Bytes == "" {
		return errors.New("asset_list.review.archive.audit 不完整")
	}
	declaredActions := make(map[string]struct{}, len(auditActions))
	for _, action := range auditActions {
		declaredActions[action.Value] = struct{}{}
	}
	if _, exists := declaredActions[audit.Action]; !exists {
		return fmt.Errorf("asset_list.review.archive.audit.action 引用了未知 audit action %q", audit.Action)
	}
	return nil
}

// assetListArchiveReadmeHolders names the holders each README line must carry;
// a line without an entry needs none.
var assetListArchiveReadmeHolders = map[string][]string{
	"title_line":        {"title"},
	"list_line":         {"list_id"},
	"generated_line":    {"generated_at"},
	"summary_line":      {"items", "files"},
	"item_line":         {"position", "name"},
	"item_kind_line":    {"position", "name", "kind", "round"},
	"directory_line":    {"directory"},
	"task_line":         {"task_id"},
	"formats_line":      {"formats"},
	"triangles_line":    {"triangles"},
	"texture_line":      {"texture"},
	"checks_line":       {"checks"},
	"check_line":        {"check", "status"},
	"check_budget_line": {"check", "status", "budget"},
	"file_line":         {"path", "size", "sha256"},
	"missing_line":      {"asset_id", "media_type"},
}

// validateAssetListArchiveReadme 校验整批 ZIP 的 README 文案：每行都要有，
// 带占位符的行要真的带齐。README 只由 Schema 拼装，缺一行就少一句人话。
func validateAssetListArchiveReadme(readme AssetListArchiveReadmeSchema) error {
	for field, text := range map[string]string{
		"title_line":        readme.TitleLine,
		"list_line":         readme.ListLine,
		"generated_line":    readme.GeneratedLine,
		"note":              readme.Note,
		"summary_line":      readme.SummaryLine,
		"item_line":         readme.ItemLine,
		"item_kind_line":    readme.ItemKindLine,
		"directory_line":    readme.DirectoryLine,
		"task_line":         readme.TaskLine,
		"formats_line":      readme.FormatsLine,
		"format_separator":  readme.FormatSeparator,
		"triangles_line":    readme.TrianglesLine,
		"texture_line":      readme.TextureLine,
		"checks_line":       readme.ChecksLine,
		"check_line":        readme.CheckLine,
		"check_budget_line": readme.CheckBudgetLine,
		"check_separator":   readme.CheckSeparator,
		"files_heading":     readme.FilesHeading,
		"file_line":         readme.FileLine,
		"missing_heading":   readme.MissingHeading,
		"missing_line":      readme.MissingLine,
		"unknown_role":      readme.UnknownRole,
	} {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("asset_list.review.archive.readme.%s 不能为空", field)
		}
		present := placeholderSet(text)
		for _, name := range assetListArchiveReadmeHolders[field] {
			if !present[name] {
				return fmt.Errorf("asset_list.review.archive.readme.%s 缺少占位符 {%s}", field, name)
			}
		}
	}
	return nil
}

// validateAssetListStates 要求声明的 id 恰好覆盖词表，label 与 role 非空。
func validateAssetListStates(name string, states []AssetListStateSchema, required []string) error {
	requiredSet := stringSet(required)
	if len(states) != len(required) {
		return fmt.Errorf("%s 必须恰好声明 %v", name, required)
	}
	seen := make(map[string]bool, len(states))
	for index, state := range states {
		if _, allowed := requiredSet[state.ID]; !allowed {
			return fmt.Errorf("%s[%d].id 非法: %q", name, index, state.ID)
		}
		if seen[state.ID] {
			return fmt.Errorf("%s 的 id 重复: %q", name, state.ID)
		}
		seen[state.ID] = true
		if strings.TrimSpace(state.Label) == "" || strings.TrimSpace(state.Role) == "" {
			return fmt.Errorf("%s[%d] 的 label 和 role 不能为空", name, index)
		}
	}
	return nil
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}
