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
		"confirmed":                   list.Messages.Confirmed,
		"rejected":                    list.Messages.Rejected,
		"replaced":                    list.Messages.Replaced,
		"expired":                     list.Messages.Expired,
		"limit_reached":               list.Messages.LimitReached,
		"list_not_found":              list.Messages.ListNotFound,
		"empty_list":                  list.Messages.EmptyList,
		"unavailable_item":            list.Messages.UnavailableItem,
		"step_cap_warning":            list.Messages.StepCapWarning,
		"all_2d_note":                 list.Messages.All2DNote,
		"project_asset_limit_warning": list.Messages.ProjectAssetLimitWarning,
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

	// 路由键只为 #327 PR3 使用；声明清单时四条都必须存在，注册与否由 HTTP
	// 层决定。
	routes := schema.Runtime.HTTP.Routes
	for name, route := range map[string]string{
		"project_asset_lists":         routes.ProjectAssetLists,
		"project_asset_list":          routes.ProjectAssetList,
		"project_asset_list_decision": routes.ProjectAssetListDecision,
		"chat_session_asset_lists":    routes.ChatSessionAssetLists,
	} {
		if strings.TrimSpace(route) == "" {
			return fmt.Errorf("application.asset_list 需要 runtime.http.routes.%s", name)
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
