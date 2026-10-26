package generation

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// resource_state.go 落实 §6.5 的 0022 资源态内核规则：槽位键、成功提交
// 初始化、pending 判定、deadline finalizer 与 Provider 状态断言的通用
// 合法性校验。内核不解释 Provider 私有转换语义——Provider 只能经
// Observation.ResourceUpdates 断言非必需槽位的终态。
//
// 回顾性整改 P1/P2 的两条内核证据规则（新批准行为，取代旧验收口径）：
//   - READY 只由已提交的本地持久资产证据产生：Provider 的 ready 断言只是
//     "上游工作已完成"的声明，零本地资产时保守忽略（槽位保持 pending）；
//   - 每个被请求的资源项都以通用 Schema 绑定键持久化身份与结果：声明
//     variant_from 的 on-demand 槽位按冻结选中项逐项建槽，内核不含任何
//     格式枚举，也不为 Provider 子作业建表。

// resourceStateKey 生成 "<role>|<variant 或 variant_prefix>|<group>" 槽位键
// （§6.5 结构；与公开 resources[] 投影共用同一键序）。
func resourceStateKey(requirement AssetRequirement) string {
	scope := requirement.Variant
	if scope == "" {
		scope = requirement.VariantPrefix
	}
	return resourceSlotKey(requirement.Role, scope, requirement.Group)
}

// resourceSlotKey 组装槽位键（唯一实现；逐项展开与断言解析共用）。
func resourceSlotKey(role, scope, group string) string {
	return role + "|" + scope + "|" + group
}

// ParseResourceStateKey 拆解槽位键（读取路径/公开投影使用）。
func ParseResourceStateKey(key string) (role, scope, group string, err error) {
	return parseResourceStateKey(key)
}

func parseResourceStateKey(key string) (role, scope, group string, err error) {
	parts := strings.Split(key, "|")
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("资源态键 %q 无效", key)
	}
	return parts[0], parts[1], parts[2], nil
}

// encodeResourceStates 序列化 0022 列（键序稳定，便于测试断言）。
func encodeResourceStates(states map[string]ResourceState) (json.RawMessage, error) {
	if len(states) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(states)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

// decodeResourceStates 解析 0022 列；空串/NULL 返回 nil（无成功后观察）。
func decodeResourceStates(raw []byte) (map[string]ResourceState, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var states map[string]ResourceState
	if err := json.Unmarshal([]byte(trimmed), &states); err != nil {
		return nil, fmt.Errorf("解析 resource_state_json: %w", err)
	}
	return states, nil
}

// postSuccessObservationActive 判定任务是否处于成功后观察生命周期：
// 声明 observe 且存在 0022 资源态（§6.2 触点 2 的进入条件前半）。
func postSuccessObservationActive(task Task) bool {
	return task.Offering.PostSuccess != nil &&
		task.Offering.PostSuccess.Observe &&
		len(task.Resources) > 0
}

// pendingResourceKeys 返回仍处于 pending 的槽位键（稳定序）。
func pendingResourceKeys(states map[string]ResourceState) []string {
	pending := make([]string, 0, len(states))
	for key, state := range states {
		if state.State == ResourcePending {
			pending = append(pending, key)
		}
	}
	sort.Strings(pending)
	return pending
}

// ResourcesPending 报告公开 resources_pending（§6.2 触点 9）：
// 任一槽位 pending 即 true。
func ResourcesPending(states map[string]ResourceState) bool {
	return len(pendingResourceKeys(states)) > 0
}

// resourceSlotStates 以稳定序导出槽位身份与状态（成功后观察期只读地告知
// Provider 哪些期望项仍未兑现；不含调度字段/文案键/任何私有语义）。身份
// 按冻结声明解析：聚合前缀槽位输出 variant_prefix，逐项/精确槽位输出
// variant（不做字符串形状猜测）。
func resourceSlotStates(
	states map[string]ResourceState,
	outputs []AssetRequirement,
) []ResourceSlotState {
	if len(states) == 0 {
		return nil
	}
	keys := make([]string, 0, len(states))
	for key := range states {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	slots := make([]ResourceSlotState, 0, len(keys))
	for _, key := range keys {
		role, scope, group, err := parseResourceStateKey(key)
		if err != nil {
			continue
		}
		slot := ResourceSlotState{Role: role, Group: group, State: states[key].State}
		requirement, declared := requirementForSlot(outputs, role, scope, group)
		if declared && requirement.VariantPrefix != "" && scope == requirement.VariantPrefix {
			slot.VariantPrefix = scope
		} else {
			slot.Variant = scope
		}
		slots = append(slots, slot)
	}
	return slots
}

// requirementForSlot 按槽位身份解析冻结声明（精确 variant > 前缀 > 旧语义
// role 通配；§3.2 加载期校验已排除重叠歧义）。
func requirementForSlot(
	outputs []AssetRequirement,
	role string,
	scope string,
	group string,
) (AssetRequirement, bool) {
	fallback := AssetRequirement{}
	found := false
	for _, requirement := range outputs {
		if requirement.Role != role || requirement.Group != group {
			continue
		}
		switch {
		case requirement.Variant != "":
			if requirement.Variant == scope {
				return requirement, true
			}
		case requirement.VariantPrefix != "":
			if scope == requirement.VariantPrefix ||
				strings.HasPrefix(scope, requirement.VariantPrefix) {
				return requirement, true
			}
		default:
			if !found {
				fallback = requirement
				found = true
			}
		}
	}
	return fallback, found
}

// countSlotAssets 统计满足单个槽位身份的已落盘资产数（本地持久证据的
// 唯一判定）：聚合前缀槽位按前缀匹配（W6 既有语义），逐项/精确槽位按
// 变体精确匹配（P2），旧语义 role 通配按 role 匹配。
func countSlotAssets(requirement AssetRequirement, scope string, assets []Asset) int {
	count := 0
	for _, asset := range assets {
		if asset.Role != requirement.Role {
			continue
		}
		switch {
		case requirement.Variant == "" && requirement.VariantPrefix == "":
			// 旧语义：role 通配。
		case requirement.VariantPrefix != "" && scope == requirement.VariantPrefix:
			if !strings.HasPrefix(asset.Variant, requirement.VariantPrefix) {
				continue
			}
		default:
			if asset.Variant != scope {
				continue
			}
		}
		count++
	}
	return count
}

// postSuccessDeadline 计算成功后观察截止时刻：FinishedAt + deadline_seconds
// （§6.4）。FinishedAt 缺失（异常路径）退化为 CreatedAt + deadline。
func postSuccessDeadline(task Task) time.Time {
	deadlineSeconds := 0
	if task.Offering.PostSuccess != nil {
		deadlineSeconds = task.Offering.PostSuccess.DeadlineSeconds
	}
	base := task.CreatedAt
	if task.FinishedAt != nil {
		base = *task.FinishedAt
	}
	return base.Add(time.Duration(deadlineSeconds) * time.Second)
}

// postSuccessInterval 取观察间隔（声明缺省/非法时 60s 兜底；加载期校验
// 已拒绝非法声明，兜底只服务历史快照防御）。
func postSuccessInterval(task Task) time.Duration {
	if task.Offering.PostSuccess != nil && task.Offering.PostSuccess.IntervalSeconds > 0 {
		return time.Duration(task.Offering.PostSuccess.IntervalSeconds) * time.Second
	}
	return 60 * time.Second
}

// initialResourceStates 在成功提交时初始化 0022 资源态（§6.2 触点 1）：
//   - 阻断成功的声明（required/旧语义）→ ready（成功门槛已保证落地）；
//   - background/on-demand → 已有匹配资产则 ready，否则 pending；
//   - 声明 variant_from 的 on-demand → 按冻结选中项**逐项建槽**（P2：
//     每个被请求的资源项都有持久身份与独立结果），未选择 → 零槽位；
//   - 其余 on-demand 且 Offering 声明 cost.selection_parameter 而冻结输入
//     的选择为空 → 不初始化（未选择的按需输出永不进入观察，§5.3/W6）。
//
// 只有声明 post_success.observe 的 Offering 才初始化；否则返回 nil
// （现状零行为变化）。deadline 以槽位粒度写入（= FinishedAt + deadline）。
// 初始化只发生在成功提交时——已持久化的历史快照永不被追溯改写（不产生
// 新的付费义务，也不重解释既有形状）。
func initialResourceStates(
	task Task,
	selectionItems func(offering Offering, input json.RawMessage, parameter string) []string,
) map[string]ResourceState {
	if task.Offering.PostSuccess == nil || !task.Offering.PostSuccess.Observe {
		return nil
	}
	deadline := postSuccessDeadline(task)
	selected := func(parameter string) []string {
		if selectionItems == nil || parameter == "" {
			return nil
		}
		return selectionItems(task.Offering, task.Input, parameter)
	}
	states := map[string]ResourceState{}
	for _, requirement := range task.Offering.Outputs {
		if requirementBlocksSuccess(requirement) {
			states[resourceStateKey(requirement)] = ResourceState{
				State:    ResourceReady,
				Deadline: deadline,
			}
			continue
		}
		if requirement.Delivery == AssetDeliveryOnDemand {
			if requirement.VariantFrom != "" {
				for _, value := range selected(requirement.VariantFrom) {
					variant := SelectionVariant(requirement, value)
					if variant == "" {
						continue
					}
					key := resourceSlotKey(requirement.Role, variant, requirement.Group)
					state := ResourceState{State: ResourcePending, Deadline: deadline}
					if countSlotAssets(requirement, variant, task.Assets) > 0 {
						state.State = ResourceReady
					}
					states[key] = state
				}
				continue
			}
			if task.Offering.Cost != nil &&
				task.Offering.Cost.SelectionParameter != "" &&
				len(selected(task.Offering.Cost.SelectionParameter)) == 0 {
				continue
			}
		}
		state := ResourceState{State: ResourcePending, Deadline: deadline}
		if countMatchingAssets(requirement, task.Assets) > 0 {
			state.State = ResourceReady
		}
		states[resourceStateKey(requirement)] = state
	}
	if len(states) == 0 {
		return nil
	}
	return states
}

// countMatchingAssets 统计满足声明匹配规则（§3.2 规则 1）的已落盘资产数。
func countMatchingAssets(requirement AssetRequirement, assets []Asset) int {
	count := 0
	for _, asset := range assets {
		if asset.Role != requirement.Role {
			continue
		}
		if !requirementMatchesVariant(requirement, asset.Variant) {
			continue
		}
		count++
	}
	return count
}

// markMergedResources 合并落地后把匹配的**非必需槽位**按本地资产事实推进
// ready（幂等；仅 pending → ready，终态不可逆）。逐槽位判定：
//   - 逐项槽位（variant_from 展开）与精确/前缀槽位：本地已有匹配资产即
//     ready——READY 的唯一来源是已提交的本地持久证据（P1）；
//   - selection 驱动的**聚合前缀槽位**（未声明 variant_from 的历史形状）
//     除外：单个前缀槽位无法表达 K 个选中项的混合结果，完成时机由
//     Provider 在全部私有工作结束后断言（内核不解释其私有状态），
//     deadline finalizer 按资产存在性兜底收尾。
func markMergedResources(
	states map[string]ResourceState,
	offering Offering,
	outputs []AssetRequirement,
	assets []Asset,
) {
	if states == nil {
		return
	}
	for key, state := range states {
		if state.State != ResourcePending {
			continue
		}
		role, scope, group, err := parseResourceStateKey(key)
		if err != nil {
			continue
		}
		requirement, declared := requirementForSlot(outputs, role, scope, group)
		if !declared || requirementBlocksSuccess(requirement) {
			continue
		}
		if aggregateSelectionSlot(offering, requirement, scope) {
			continue
		}
		if countSlotAssets(requirement, scope, assets) == 0 {
			continue
		}
		state.State = ResourceReady
		state.Reason = ""
		states[key] = state
	}
}

// aggregateSelectionSlot 判定槽位是否为 selection 驱动 on-demand 声明的
// **聚合前缀槽位**（Schema 驱动的通用规则，非 per-format 分支）：只有未
// 声明 variant_from 的历史形状才会产生聚合槽位；逐项槽位（P2）由本地
// 资产事实收尾，不属于 Provider 私有完成时机。
func aggregateSelectionSlot(
	offering Offering,
	requirement AssetRequirement,
	scope string,
) bool {
	if requirement.VariantFrom != "" {
		return false
	}
	return requirement.Delivery == AssetDeliveryOnDemand &&
		requirement.VariantPrefix != "" &&
		scope == requirement.VariantPrefix &&
		offering.Cost != nil &&
		offering.Cost.SelectionParameter != ""
}

// applyResourceUpdates 校验并应用 Provider 状态断言（§6.3/§6.5 + P1）：
//   - 目标必须命中冻结快照声明的非必需槽位：带 variant 的断言按声明匹配
//     规则解析到**逐项槽位**（role|variant|group），带 variant_prefix 的
//     断言只解析到聚合前缀槽位——两种形状互不串扰（历史快照兼容显式化，
//     不做静默重解释）；
//   - ready 只是 Provider 的"上游工作已完成"声明：仅在本地已有匹配资产
//     证据时落 READY，零证据保守忽略（槽位保持 pending，等后续轮次或
//     deadline finalizer 依据资产事实收尾）；
//   - failed/denied/abandoned 是降级断言，已有匹配资产的槽位不允许降级
//     （合并事实优先）；
//   - 只有 pending 槽位可转移（终态不可逆）。
//
// 返回实际应用的更新（用于事件）；非法/无证据断言保守忽略（零突变）。
func applyResourceUpdates(
	states map[string]ResourceState,
	outputs []AssetRequirement,
	updates []ResourceUpdate,
	assets []Asset,
) []ResourceUpdate {
	applied := make([]ResourceUpdate, 0, len(updates))
	if states == nil {
		return applied
	}
	for _, update := range updates {
		switch update.State {
		case ResourceReady, ResourceFailed, ResourceDenied, ResourceAbandoned:
		default:
			continue
		}
		if update.Variant == "" && update.VariantPrefix == "" {
			continue
		}
		matched := false
		for _, requirement := range outputs {
			if requirement.Role != update.Role || requirementBlocksSuccess(requirement) {
				continue
			}
			scope, ok := updateSlotScope(requirement, update)
			if !ok {
				continue
			}
			key := resourceSlotKey(requirement.Role, scope, requirement.Group)
			state, exists := states[key]
			if !exists || state.State != ResourcePending {
				continue
			}
			evidence := countSlotAssets(requirement, scope, assets)
			if update.State == ResourceReady {
				if evidence == 0 {
					// 上游完成 ≠ 本地 READY（P1）：零本地证据不断言终态。
					continue
				}
			} else if evidence > 0 {
				continue
			}
			state.State = update.State
			state.Reason = update.Reason
			states[key] = state
			matched = true
			break
		}
		if matched {
			applied = append(applied, update)
		}
	}
	return applied
}

// updateSlotScope 解析断言指向的槽位变体范围：variant 断言必须落在声明的
// 变体匹配范围内（精确或前缀），prefix 断言只能指向同一前缀的聚合槽位。
func updateSlotScope(requirement AssetRequirement, update ResourceUpdate) (string, bool) {
	if update.Variant != "" {
		if !requirementMatchesVariant(requirement, update.Variant) {
			return "", false
		}
		return update.Variant, true
	}
	if update.VariantPrefix == "" || requirement.VariantPrefix != update.VariantPrefix {
		return "", false
	}
	return update.VariantPrefix, true
}

// finalizeExpiredResources 是幂等 deadline finalizer（§6.4）：全部
// pending 槽位一次性收尾——本地已有该槽位匹配资产的转 ready（资产事实
// 优先），否则 incomplete（reason 文案键）；返回是否发生变化。只写一次由
// "仅 pending 可转移"结构性保证——ready/incomplete 都是终态。
func finalizeExpiredResources(
	states map[string]ResourceState,
	outputs []AssetRequirement,
	assets []Asset,
	reason string,
) bool {
	changed := false
	for key, state := range states {
		if state.State != ResourcePending {
			continue
		}
		ready := false
		role, scope, group, err := parseResourceStateKey(key)
		if err == nil {
			if requirement, declared := requirementForSlot(outputs, role, scope, group); declared {
				ready = countSlotAssets(requirement, scope, assets) > 0
			}
		}
		if ready {
			state.State = ResourceReady
			state.Reason = ""
		} else {
			state.State = ResourceIncomplete
			state.Reason = reason
		}
		states[key] = state
		changed = true
	}
	return changed
}

// ResourceReasonMessageKey 是 incomplete 槽位的缺省文案键。
const ResourceReasonMessageKey = "post_success.resource_incomplete"

// EncodeResourceStatesForColumn 序列化 0022 列（仓储写入路径）：
// nil/空态写 NULL（无成功后观察）。
func EncodeResourceStatesForColumn(states map[string]ResourceState) (any, error) {
	if len(states) == 0 {
		return nil, nil
	}
	encoded, err := encodeResourceStates(states)
	if err != nil {
		return nil, err
	}
	return string(encoded), nil
}

// DecodeResourceStatesFromColumn 解析 0022 列（仓储读取路径）。
func DecodeResourceStatesFromColumn(raw []byte) (map[string]ResourceState, error) {
	return decodeResourceStates(raw)
}
