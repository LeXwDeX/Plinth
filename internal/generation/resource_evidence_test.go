package generation

// resource_evidence_test.go 锁定回顾性整改 P1/P2 的内核规则：
//   - READY 只由已提交的本地持久资产证据产生（Provider 的 ready 断言只是
//     "上游完成"声明）；
//   - 声明 variant_from 的 on-demand 槽位按冻结选中项**逐项建槽**，每个被
//     请求项都有独立身份与独立结果（混合结果可公开）；
//   - 历史聚合前缀槽位形状不被追溯改写（显式兼容，不做静默重解释）。

import (
	"encoding/json"
	"testing"
	"time"
)

func evidenceRequirement() AssetRequirement {
	return AssetRequirement{
		Role:          "converted_model",
		VariantPrefix: "converted.",
		VariantFrom:   "output_formats",
		VariantCase:   VariantCaseLower,
		Delivery:      AssetDeliveryOnDemand,
	}
}

// TestRetrospectiveReadyRequiresLocalAsset：零本地资产时 Provider 的 ready
// 断言不得把槽位推进 READY（旧行为：applyResourceUpdates 无条件接受 ready）。
func TestRetrospectiveReadyRequiresLocalAsset(t *testing.T) {
	req := AssetRequirement{Role: "converted_model", VariantPrefix: "converted.", Delivery: "on-demand"}
	key := resourceStateKey(req)
	states := map[string]ResourceState{key: {State: ResourcePending}}
	applyResourceUpdates(states, []AssetRequirement{req}, []ResourceUpdate{{
		Role:          req.Role,
		VariantPrefix: req.VariantPrefix,
		State:         ResourceReady,
	}}, nil)
	if states[key].State == ResourceReady {
		t.Fatal("resource declared READY with zero locally materialized assets")
	}
}

// TestSelectionVariantIsDeclarationBound：变体派生只按声明的大小写规则，
// 内核不含任何格式枚举。
func TestSelectionVariantIsDeclarationBound(t *testing.T) {
	cases := []struct {
		name        string
		requirement AssetRequirement
		value       string
		want        string
	}{
		{"lower", AssetRequirement{VariantPrefix: "converted.", VariantCase: VariantCaseLower}, "GLTF", "converted.gltf"},
		{"upper", AssetRequirement{VariantPrefix: "converted.", VariantCase: VariantCaseUpper}, "gltf", "converted.GLTF"},
		{"verbatim", AssetRequirement{VariantPrefix: "converted."}, "GLTF", "converted.GLTF"},
		{"no prefix", AssetRequirement{VariantCase: VariantCaseLower}, "GLTF", ""},
		{"empty value", AssetRequirement{VariantPrefix: "converted.", VariantCase: VariantCaseLower}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SelectionVariant(tc.requirement, tc.value); got != tc.want {
				t.Fatalf("SelectionVariant = %q; want %q", got, tc.want)
			}
		})
	}
}

// TestInitialResourceStatesExpandsEverySelectedItem：每个被请求的格式都获得
// 独立持久身份；未选择 → 零槽位（永不进入观察）。
func TestInitialResourceStatesExpandsEverySelectedItem(t *testing.T) {
	minimum := 0
	offering := Offering{
		Outputs: []AssetRequirement{
			{Role: "model", Minimum: &minimum},
			evidenceRequirement(),
		},
		Cost:        &CostDeclaration{SelectionParameter: "output_formats"},
		PostSuccess: &PostSuccessPolicy{Observe: true, IntervalSeconds: 60, DeadlineSeconds: 86400},
	}
	offering.Outputs[0].Minimum = nil
	offering.Outputs[0].Delivery = AssetDeliveryRequired
	selections := func(_ Offering, input json.RawMessage, parameter string) []string {
		return costSelectionItems(offering, input, parameter)
	}
	task := Task{
		Offering:  offering,
		Input:     json.RawMessage(`{"output_formats":["GLTF","FBX","OBJ"]}`),
		CreatedAt: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC),
	}
	states := initialResourceStates(task, selections)
	for _, variant := range []string{"converted.gltf", "converted.fbx", "converted.obj"} {
		key := "converted_model|" + variant + "|"
		state, ok := states[key]
		if !ok {
			t.Fatalf("缺少逐项槽位 %q：%#v", key, states)
		}
		if state.State != ResourcePending {
			t.Fatalf("%s = %s; want pending", variant, state.State)
		}
	}
	if _, exists := states["converted_model|converted.|"]; exists {
		t.Fatalf("逐项声明不得再建聚合槽位：%#v", states)
	}

	unselected := task
	unselected.Input = json.RawMessage(`{"output_formats":[]}`)
	if got := initialResourceStates(unselected, selections); len(got) != 1 {
		t.Fatalf("未选择时只应保留必需槽位：%#v", got)
	}
}

// TestApplyResourceUpdatesTargetsPerItemSlots：逐项断言只作用于自身槽位，
// 绝不落到聚合前缀槽位；失败族断言在已有本地资产时被拒（事实优先）。
func TestApplyResourceUpdatesTargetsPerItemSlots(t *testing.T) {
	requirement := evidenceRequirement()
	outputs := []AssetRequirement{requirement}
	states := map[string]ResourceState{
		"converted_model|converted.gltf|": {State: ResourcePending},
		"converted_model|converted.fbx|":  {State: ResourcePending},
	}
	applied := applyResourceUpdates(states, outputs, []ResourceUpdate{
		{Role: "converted_model", Variant: "converted.gltf", State: ResourceFailed, Reason: "post_success.conversion_failed"},
		{Role: "converted_model", VariantPrefix: "converted.", State: ResourceFailed},
	}, nil)
	if len(applied) != 1 {
		t.Fatalf("applied = %#v; want 只应用逐项断言（聚合断言在逐项形状下无对应槽位）", applied)
	}
	if states["converted_model|converted.gltf|"].State != ResourceFailed {
		t.Fatalf("gltf = %#v", states["converted_model|converted.gltf|"])
	}
	if states["converted_model|converted.fbx|"].State != ResourcePending {
		t.Fatalf("fbx 被无关断言终结：%#v", states["converted_model|converted.fbx|"])
	}

	// 已有本地资产的槽位不接受降级断言。
	withAsset := map[string]ResourceState{"converted_model|converted.fbx|": {State: ResourcePending}}
	assets := []Asset{{Role: "converted_model", Variant: "converted.fbx", Size: 10}}
	if applied := applyResourceUpdates(withAsset, outputs, []ResourceUpdate{
		{Role: "converted_model", Variant: "converted.fbx", State: ResourceFailed},
	}, assets); len(applied) != 0 {
		t.Fatalf("降级断言在资产事实面前被应用：%#v", applied)
	}
	// 有证据时 ready 断言可落地。
	if applied := applyResourceUpdates(withAsset, outputs, []ResourceUpdate{
		{Role: "converted_model", Variant: "converted.fbx", State: ResourceReady},
	}, assets); len(applied) != 1 || withAsset["converted_model|converted.fbx|"].State != ResourceReady {
		t.Fatalf("有本地证据的 ready 断言未落地：%#v %#v", applied, withAsset)
	}
}

// TestMarkMergedResourcesUsesPerItemEvidence：本地资产落地即 READY（逐项
// 槽位）；聚合 selection 槽位仍由 Provider 断言推进（历史形状）。
func TestMarkMergedResourcesUsesPerItemEvidence(t *testing.T) {
	requirement := evidenceRequirement()
	offering := Offering{
		Outputs: []AssetRequirement{requirement},
		Cost:    &CostDeclaration{SelectionParameter: "output_formats"},
	}
	states := map[string]ResourceState{
		"converted_model|converted.gltf|": {State: ResourcePending},
		"converted_model|converted.fbx|":  {State: ResourcePending},
	}
	markMergedResources(states, offering, offering.Outputs, []Asset{
		{Role: "converted_model", Variant: "converted.gltf", Size: 12},
	})
	if states["converted_model|converted.gltf|"].State != ResourceReady {
		t.Fatalf("已落盘槽位未 READY：%#v", states["converted_model|converted.gltf|"])
	}
	if states["converted_model|converted.fbx|"].State != ResourcePending {
		t.Fatalf("未落盘槽位被误判 READY：%#v", states["converted_model|converted.fbx|"])
	}

	// 聚合形状（无 variant_from）：资产落地不得自行 READY（完成时机由
	// Provider 全部私有工作结束后断言）。
	aggregate := AssetRequirement{Role: "converted_model", VariantPrefix: "converted.", Delivery: AssetDeliveryOnDemand}
	aggregateOffering := Offering{
		Outputs: []AssetRequirement{aggregate},
		Cost:    &CostDeclaration{SelectionParameter: "output_formats"},
	}
	aggregateStates := map[string]ResourceState{"converted_model|converted.|": {State: ResourcePending}}
	markMergedResources(aggregateStates, aggregateOffering, aggregateOffering.Outputs, []Asset{
		{Role: "converted_model", Variant: "converted.gltf", Size: 12},
	})
	if aggregateStates["converted_model|converted.|"].State != ResourcePending {
		t.Fatalf("聚合 selection 槽位被单个资产推进：%#v", aggregateStates)
	}
}

// TestFinalizeExpiredResourcesIsSlotScoped：deadline finalizer 按槽位身份
// 判定资产证据（逐项槽位不得因同前缀的其他资产被误判 ready）。
func TestFinalizeExpiredResourcesIsSlotScoped(t *testing.T) {
	outputs := []AssetRequirement{evidenceRequirement()}
	states := map[string]ResourceState{
		"converted_model|converted.gltf|": {State: ResourcePending},
		"converted_model|converted.fbx|":  {State: ResourcePending},
	}
	if !finalizeExpiredResources(states, outputs, []Asset{
		{Role: "converted_model", Variant: "converted.gltf", Size: 12},
	}, ResourceReasonMessageKey) {
		t.Fatal("finalizer 未产生变化")
	}
	if states["converted_model|converted.gltf|"].State != ResourceReady {
		t.Fatalf("有资产的槽位 = %#v; want ready", states["converted_model|converted.gltf|"])
	}
	incomplete := states["converted_model|converted.fbx|"]
	if incomplete.State != ResourceIncomplete || incomplete.Reason != ResourceReasonMessageKey {
		t.Fatalf("无资产的槽位 = %#v; want incomplete", incomplete)
	}
}

// TestResourceSlotStatesExposeIdentityOnly：只读槽位视图只暴露身份与状态
// （无 deadline/reason 等调度与文案内部）。
func TestResourceSlotStatesExposeIdentityOnly(t *testing.T) {
	outputs := []AssetRequirement{
		evidenceRequirement(),
		{Role: "thumbnail", Variant: "rendered_image", Delivery: AssetDeliveryBackground},
		{Role: "texture", VariantPrefix: "texture.", Delivery: AssetDeliveryBackground},
	}
	states := map[string]ResourceState{
		"converted_model|converted.gltf|": {State: ResourcePending, Deadline: time.Now(), Reason: "internal"},
		"thumbnail|rendered_image|":       {State: ResourceReady},
		"texture|texture.|":               {State: ResourcePending},
	}
	slots := resourceSlotStates(states, outputs)
	if len(slots) != 3 {
		t.Fatalf("slots = %#v", slots)
	}
	byRole := map[string]ResourceSlotState{}
	for _, slot := range slots {
		key := slot.Role
		if slot.Variant != "" {
			key += "|" + slot.Variant
		}
		if slot.VariantPrefix != "" {
			key += "|" + slot.VariantPrefix
		}
		byRole[key] = slot
	}
	if slot := byRole["converted_model|converted.gltf"]; slot.State != ResourcePending || slot.VariantPrefix != "" {
		t.Fatalf("逐项槽位身份错误：%#v", slot)
	}
	if slot := byRole["texture|texture."]; slot.VariantPrefix != "texture." || slot.Variant != "" {
		t.Fatalf("聚合前缀槽位身份错误：%#v", slot)
	}
	if slots[0].Variant == "" && slots[0].VariantPrefix == "" {
		t.Fatalf("槽位缺少身份：%#v", slots[0])
	}
}

// TestLegacyAggregateSnapshotIsNotReinterpreted：历史聚合快照（无 variant_from
// 声明）保持单槽位形状；Provider 的逐项断言无对应槽位时被保守忽略，
// 聚合断言在全部选中项终态且本地有证据时才落 READY。
func TestLegacyAggregateSnapshotIsNotReinterpreted(t *testing.T) {
	legacy := AssetRequirement{Role: "converted_model", VariantPrefix: "converted.", Delivery: AssetDeliveryOnDemand}
	outputs := []AssetRequirement{legacy}
	offering := Offering{
		Outputs:     outputs,
		Cost:        &CostDeclaration{SelectionParameter: "output_formats"},
		PostSuccess: &PostSuccessPolicy{Observe: true, IntervalSeconds: 60, DeadlineSeconds: 86400},
	}
	task := Task{
		Offering:  offering,
		Input:     json.RawMessage(`{"output_formats":["GLTF","FBX"]}`),
		CreatedAt: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC),
	}
	states := initialResourceStates(task, costSelectionItems)
	if len(states) != 1 {
		t.Fatalf("历史形状必须保持单聚合槽位：%#v", states)
	}
	if _, ok := states["converted_model|converted.|"]; !ok {
		t.Fatalf("聚合槽位键错误：%#v", states)
	}
	if aggregateSelectionSlot(offering, legacy, "converted.") != true {
		t.Fatal("聚合 selection 槽位判定错误")
	}
	// 逐项断言在聚合形状下没有对应槽位 → 保守忽略（零突变）。
	if applied := applyResourceUpdates(states, outputs, []ResourceUpdate{
		{Role: "converted_model", Variant: "converted.gltf", State: ResourceFailed},
	}, nil); len(applied) != 0 {
		t.Fatalf("逐项断言被误应用到聚合槽位：%#v", applied)
	}
	if states["converted_model|converted.|"].State != ResourcePending {
		t.Fatalf("聚合槽位被改写：%#v", states["converted_model|converted.|"])
	}
	// 聚合 ready 断言仍需本地资产证据。
	if applied := applyResourceUpdates(states, outputs, []ResourceUpdate{
		{Role: "converted_model", VariantPrefix: "converted.", State: ResourceReady},
	}, nil); len(applied) != 0 {
		t.Fatalf("零证据的聚合 ready 断言被应用：%#v", applied)
	}
	assets := []Asset{
		{Role: "converted_model", Variant: "converted.gltf", Size: 10},
		{Role: "converted_model", Variant: "converted.fbx", Size: 10},
	}
	if applied := applyResourceUpdates(states, outputs, []ResourceUpdate{
		{Role: "converted_model", VariantPrefix: "converted.", State: ResourceReady},
	}, assets); len(applied) != 1 || states["converted_model|converted.|"].State != ResourceReady {
		t.Fatalf("有证据的聚合 ready 断言未落地：%#v %#v", applied, states)
	}
}
