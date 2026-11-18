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
		"confirmed":                      list.Messages.Confirmed,
		"rejected":                       list.Messages.Rejected,
		"replaced":                       list.Messages.Replaced,
		"expired":                        list.Messages.Expired,
		"limit_reached":                  list.Messages.LimitReached,
		"list_not_found":                 list.Messages.ListNotFound,
		"empty_list":                     list.Messages.EmptyList,
		"unavailable_item":               list.Messages.UnavailableItem,
		"step_cap_warning":               list.Messages.StepCapWarning,
		"all_2d_note":                    list.Messages.All2DNote,
		"project_asset_limit_warning":    list.Messages.ProjectAssetLimitWarning,
		"proposed":                       list.Messages.Proposed,
		"propose_session_not_in_project": list.Messages.ProposeSessionNotInProject,
		"propose_invalid_items":          list.Messages.ProposeInvalidItems,
		"propose_reference_unresolved":   list.Messages.ProposeReferenceUnresolved,
		"propose_failed":                 list.Messages.ProposeFailed,
		"pause_presence":                 list.Messages.PausePresence,
		"pause_user":                     list.Messages.PauseUser,
		"pause_step_cap":                 list.Messages.PauseStepCap,
		"pause_failure":                  list.Messages.PauseFailure,
		"resumed":                        list.Messages.Resumed,
		"stopped":                        list.Messages.Stopped,
		"skipped":                        list.Messages.Skipped,
		"round_limit":                    list.Messages.RoundLimit,
		"awaiting_review":                list.Messages.AwaitingReview,
		"needs_you":                      list.Messages.NeedsYou,
		"item_failed":                    list.Messages.ItemFailed,
		"item_failed_input":              list.Messages.ItemFailedInput,
		"item_failed_capacity":           list.Messages.ItemFailedCapacity,
		"item_failed_refused":            list.Messages.ItemFailedRefused,
		"item_failed_internal":           list.Messages.ItemFailedInternal,
		"step_cap_blocked":               list.Messages.StepCapBlocked,
		"needs_approval":                 list.Messages.NeedsApproval,
		"run_waiting":                    list.Messages.RunWaiting,
		"item_reported_done":             list.Messages.ItemReportedDone,
		"item_reported_failed":           list.Messages.ItemReportedFailed,
		"item_not_in_session":            list.Messages.ItemNotInSession,
		"item_not_working":               list.Messages.ItemNotWorking,
		"item_no_successful_task":        list.Messages.ItemNoSuccessfulTask,
		"item_report_failed":             list.Messages.ItemReportFailed,
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
