package catalog_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// resourceContractOffering 生成一份只带给定 outputs 的最小合法 Offering 文档。
func resourceContractOffering(outputs string) []byte {
	return testProductDocument(`[
		{
			"id":"one","revision":"1","provider_id":"p","provider_name":"P","provider_short_label":"P",
			"model_id":"m","model_name":"M","capability":"text-to-image",
			"domain":"image","display_order":0,"capability_order":0,
			"adapter_key":"a","enabled":true,"timeout_seconds":1,
			"poll_interval_seconds":1,
			"retry":{"max_attempts":1},
			"materialize_attempts":1,
			"parameters":[],
			"outputs":` + outputs + `
		}
	]`)
}

func loadResourceContractCatalog(
	t *testing.T,
	outputs string,
) (*catalog.Catalog, error) {
	t.Helper()
	document := resourceContractOffering(outputs)
	return catalog.Load(bytes.NewReader(document))
}

// TestResourceContractSchemaAcceptsExplicitDeclarations 覆盖 §3.2 规则 2 的
// 合法面：同 role 下精确变体、互不重叠前缀、background 集合并存，且旧语义
// 单独存在与 minimum 缺省（等效 1）均应加载通过。
func TestResourceContractSchemaAcceptsExplicitDeclarations(t *testing.T) {
	cases := map[string]string{
		"prefix required with background collection": `[
			{"role":"image","variant_prefix":"hero_","delivery":"required","minimum":1},
			{"role":"image","variant_prefix":"extras.","delivery":"background","minimum":0}
		]`,
		"exact variants same role": `[
			{"role":"image","variant":"hero_glb","delivery":"required","minimum":1},
			{"role":"image","variant":"side_glb","delivery":"required","minimum":2}
		]`,
		"disjoint prefixes same role": `[
			{"role":"image","variant_prefix":"rigged_character","delivery":"required","minimum":1},
			{"role":"image","variant_prefix":"basic_animations.","delivery":"background","minimum":0}
		]`,
		"legacy role only": `[
			{"role":"image","minimum":1}
		]`,
		"legacy minimum omitted defaults to one": `[
			{"role":"image"}
		]`,
		"on demand collection": `[
			{"role":"image","variant":"hero_glb","delivery":"required","minimum":1},
			{"role":"image","variant":"preview_mp4","delivery":"on-demand","minimum":0}
		]`,
		"disjoint exact and prefix same role": `[
			{"role":"image","variant":"hero_glb","delivery":"required","minimum":1},
			{"role":"image","variant_prefix":"extras.","delivery":"background","minimum":0}
		]`,
		"required group": `[
			{"role":"image","variant":"hero_a","delivery":"required","minimum":1,"group":"pair"},
			{"role":"image","variant":"hero_b","delivery":"required","minimum":1,"group":"pair"}
		]`,
		"background group": `[
			{"role":"image","variant_prefix":"extras.","delivery":"background","minimum":0,"group":"extras"}
		]`,
	}
	for name, outputs := range cases {
		if _, err := loadResourceContractCatalog(t, outputs); err != nil {
			t.Errorf("%s rejected: %v", name, err)
		}
	}
}

// TestResourceContractSchemaRejectsAmbiguousDeclarations 覆盖 §3.2 规则 2 的
// 全部歧义拒绝路径：旧语义共存、重复、前缀重叠、精确值落入前缀。
func TestResourceContractSchemaRejectsAmbiguousDeclarations(t *testing.T) {
	cases := map[string]struct {
		outputs string
		detail  string
	}{
		"legacy coexists with prefix": {
			outputs: `[
				{"role":"image","minimum":1},
				{"role":"image","variant_prefix":"hero_","delivery":"required","minimum":1}
			]`,
			detail: "旧语义",
		},
		"legacy role duplicated": {
			outputs: `[
				{"role":"image","minimum":1},
				{"role":"image","minimum":2}
			]`,
			detail: "旧语义",
		},
		"duplicate exact variant": {
			outputs: `[
				{"role":"image","variant":"hero_glb","delivery":"required","minimum":1},
				{"role":"image","variant":"hero_glb","delivery":"required","minimum":1}
			]`,
			detail: "重复",
		},
		"duplicate prefix": {
			outputs: `[
				{"role":"image","variant_prefix":"hero_","delivery":"required","minimum":1},
				{"role":"image","variant_prefix":"hero_","delivery":"background","minimum":0}
			]`,
			detail: "重叠",
		},
		"overlapping prefixes": {
			outputs: `[
				{"role":"image","variant_prefix":"anim","delivery":"required","minimum":1},
				{"role":"image","variant_prefix":"animation_","delivery":"required","minimum":1}
			]`,
			detail: "重叠",
		},
		"exact variant inside prefix": {
			outputs: `[
				{"role":"image","variant_prefix":"animation_","delivery":"required","minimum":1},
				{"role":"image","variant":"animation_glb_url","delivery":"required","minimum":1}
			]`,
			detail: "重叠",
		},
	}
	for name, testCase := range cases {
		_, err := loadResourceContractCatalog(t, testCase.outputs)
		if err == nil || !strings.Contains(err.Error(), testCase.detail) {
			t.Errorf("%s: error = %v, want detail %q", name, err, testCase.detail)
		}
	}
}

// TestResourceContractSchemaRejectsInvalidDeliveryAndScope 覆盖 delivery
// 枚举、标识符与"显式 delivery 必须显式变体范围"的加载期拒绝。
func TestResourceContractSchemaRejectsInvalidDeliveryAndScope(t *testing.T) {
	cases := map[string]struct {
		outputs string
		detail  string
	}{
		"unknown delivery": {
			outputs: `[{"role":"image","variant":"hero","delivery":"sometimes","minimum":1}]`,
			detail:  "delivery",
		},
		"delivery without variant scope": {
			outputs: `[{"role":"image","delivery":"required","minimum":1}]`,
			detail:  "变体",
		},
		"variant and prefix together": {
			outputs: `[{"role":"image","variant":"hero","variant_prefix":"hero_","delivery":"required","minimum":1}]`,
			detail:  "同时",
		},
		"variant with whitespace": {
			outputs: `[{"role":"image","variant":"hero glb","delivery":"required","minimum":1}]`,
			detail:  "标识符",
		},
		"prefix with leading space": {
			outputs: `[{"role":"image","variant_prefix":" hero_","delivery":"required","minimum":1}]`,
			detail:  "标识符",
		},
		"group with whitespace": {
			outputs: `[{"role":"image","variant":"hero","delivery":"required","minimum":1,"group":"a b"}]`,
			detail:  "标识符",
		},
		"empty role": {
			outputs: `[{"role":"","minimum":1}]`,
			detail:  "role",
		},
	}
	for name, testCase := range cases {
		_, err := loadResourceContractCatalog(t, testCase.outputs)
		if err == nil || !strings.Contains(err.Error(), testCase.detail) {
			t.Errorf("%s: error = %v, want detail %q", name, err, testCase.detail)
		}
	}
}

// TestResourceContractSchemaMinimumSemantics 覆盖 §3.2 规则 5：
// 显式 delivery 均允许 0 但不为负；required+0 表示出现时须落盘；
// 旧语义显式值保持既有严格性（≥1），缺省等效 1。
func TestResourceContractSchemaMinimumSemantics(t *testing.T) {
	rejected := map[string]string{
		"required without minimum": `[{"role":"image","variant":"hero","delivery":"required"}]`,
		"required negative":        `[{"role":"image","variant":"hero","delivery":"required","minimum":-1}]`,
		"background negative":      `[{"role":"image","variant":"hero","delivery":"background","minimum":-1}]`,
		"on-demand negative":       `[{"role":"image","variant":"hero","delivery":"on-demand","minimum":-1}]`,
		"legacy zero minimum":      `[{"role":"image","minimum":0}]`,
	}
	for name, outputs := range rejected {
		if _, err := loadResourceContractCatalog(t, outputs); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	accepted := map[string]string{
		"background zero":            `[{"role":"image","variant":"hero","delivery":"background","minimum":0}]`,
		"on-demand zero":             `[{"role":"image","variant":"hero","delivery":"on-demand","minimum":0}]`,
		"required minimum":           `[{"role":"image","variant":"hero","delivery":"required","minimum":1}]`,
		"required optional presence": `[{"role":"image","variant":"hero","delivery":"required","minimum":0}]`,
	}
	for name, outputs := range accepted {
		if _, err := loadResourceContractCatalog(t, outputs); err != nil {
			t.Errorf("%s rejected: %v", name, err)
		}
	}
}

// TestResourceContractSchemaRejectsGroupDeliveryMixing 覆盖 §3.2 规则 6：
// 同一 group 内 required 与 background/on-demand 混用必须加载期拒绝。
func TestResourceContractSchemaRejectsGroupDeliveryMixing(t *testing.T) {
	_, err := loadResourceContractCatalog(t, `[
		{"role":"image","variant":"hero_a","delivery":"required","minimum":1,"group":"pair"},
		{"role":"image","variant":"hero_b","delivery":"background","minimum":0,"group":"pair"}
	]`)
	if err == nil || !strings.Contains(err.Error(), "group") {
		t.Fatalf("mixed group error = %v", err)
	}
}

// TestDefaultCatalogReplacesRiggingAnimationPrimaryDeclarations 覆盖 §3.3
// 替换验收：rigging/animation 的旧 {role:"model", minimum:1} 必须被
// required 主产物槽位替换（非共存），并将出现的 basic_animations 全部落盘。
func TestDefaultCatalogReplacesRiggingAnimationPrimaryDeclarations(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	expectations := map[generation.OfferingID]struct {
		requiredVariant string
		maxBytes        int64
	}{
		"meshy/processing/animation": {
			requiredVariant: "animation_glb_url",
			maxBytes:        1073741824,
		},
	}
	for offeringID, expected := range expectations {
		offering, exists := taskCatalog.Get(offeringID)
		if !exists {
			t.Fatalf("offering %s missing", offeringID)
		}
		if len(offering.Outputs) != 3 {
			t.Fatalf(
				"offering %s outputs = %#v, want required primary + background collection",
				offeringID,
				offering.Outputs,
			)
		}
		primary := offering.Outputs[0]
		if primary.Role != "model" ||
			primary.Variant != expected.requiredVariant ||
			primary.VariantPrefix != "" || primary.Fidelity != "animation" ||
			primary.Delivery != generation.AssetDeliveryRequired ||
			primary.Minimum == nil || *primary.Minimum != 1 ||
			primary.MaxBytes != expected.maxBytes {
			t.Fatalf("offering %s primary = %#v", offeringID, primary)
		}
		fbx := offering.Outputs[1]
		if fbx.Variant != "animation_fbx_url" || fbx.Fidelity != "" || fbx.Minimum == nil || *fbx.Minimum != 0 {
			t.Fatalf("offering %s FBX = %#v", offeringID, fbx)
		}
		background := offering.Outputs[2]
		if background.Role != "model" ||
			background.VariantPrefix != "basic_animations." ||
			background.Delivery != generation.AssetDeliveryRequired ||
			background.Minimum == nil || *background.Minimum != 0 {
			t.Fatalf("offering %s background = %#v", offeringID, background)
		}
		for _, output := range offering.Outputs {
			if output.Variant == "" && output.VariantPrefix == "" {
				t.Fatalf(
					"offering %s keeps legacy role-only declaration: %#v",
					offeringID,
					output,
				)
			}
		}
	}
	rigging, exists := taskCatalog.Get("meshy/processing/rigging")
	if !exists || len(rigging.Outputs) != 3 {
		t.Fatalf("rigging outputs = %#v", rigging.Outputs)
	}
	for index, variant := range []string{"rigged_character_fbx_url", "rigged_character_glb_url"} {
		output := rigging.Outputs[index]
		if output.Role != "model" || output.Variant != variant ||
			output.Delivery != generation.AssetDeliveryRequired ||
			output.Minimum == nil || *output.Minimum != 1 {
			t.Fatalf("rigging output %s = %#v", variant, output)
		}
	}
	animations := rigging.Outputs[2]
	if rigging.Outputs[1].Fidelity != "skeleton" || rigging.Outputs[0].Fidelity != "" {
		t.Fatal("rigging fidelity must apply only to GLB")
	}
	if animations.Role != "model" || animations.VariantPrefix != "basic_animations." ||
		animations.Delivery != generation.AssetDeliveryRequired ||
		animations.Minimum == nil || *animations.Minimum != 0 {
		t.Fatalf("rigging basic animations = %#v", animations)
	}
}

// TestDefaultCatalogKeepsOtherOfferingsLegacy 覆盖 W1 边界：除
// rigging/animation 与 W5 新增的格式转换 offering 外，其余 offering
// 输出保持旧语义（0 行为变化）。
func TestDefaultCatalogKeepsOtherOfferingsLegacy(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	replaced := map[generation.OfferingID]bool{
		"meshy/processing/rigging":   true,
		"meshy/processing/animation": true,
		// W5 新增的格式转换 offering 自出生即用显式 (role, variant) 契约。
		"tripo/tripo-v3.1/format-conversion": true,
		// #207 recovery preserves every source slot using an exact canonical variant.
		"local/existing-assets/recover": true,
		// #183 narrow conversions each require one exact model variant.
		"local/static-glb/to-gltf":                    true,
		"local/static-gltf/to-glb":                    true,
		"tripo/format-conversion/uploaded-glb-to-stl": true,
		// #264 the Task-output glTF package has one exact model/gltf ZIP slot.
		"local/generated-glb/to-gltf": true,
		// #184 qualified routes use explicit required model/report/archive/clip slots.
		"meshy/processing/uv-unwrap":            true,
		"meshy/processing/resize":               true,
		"meshy/processing/analyze-printability": true,
		"meshy/processing/multi-color-print":    true,
		"meshy/prime/text-to-motion":            true,
		// #186 Rig Check returns one required bounded report slot.
		"tripo/processing/rig-check": true,
		// #190 uses a dedicated required representation profile, never a legacy model slot.
		"tripo/image-to-splat": true,
		// #186 native processing retains exact slots (Rig v3/Retarget/Texture enabled under route B).
		"tripo/processing/texture":    true,
		"tripo/processing/rigging":    true,
		"tripo/processing/rigging-v3": true,
		"tripo/processing/animation":  true,
		// W6 起合法显式声明的 offerings（缩略图 background / 转换 on-demand，
		// 见 §11 W6 交付记录）：meshy 缩略图 variant "primary"（result_assets
		// 已核），tripo 缩略图 "rendered_image"（响应别名已核）+ converted_model
		// 前缀槽位（生成期格式多选消费者）。
		"meshy/meshy-7/text-to-3d":              true,
		"meshy/meshy-7/image-to-3d":             true,
		"meshy/meshy-7.1/text-to-3d":            true,
		"meshy/meshy-7.1/image-to-3d":           true,
		"meshy/meshy-t2.5/text-to-3d":           true,
		"meshy/meshy-t2.5/image-to-3d":          true,
		"meshy/meshy-7.1-multiview/image-to-3d": true,
		// #191 four-view image groups: front/left/back/right required slots.
		"tripo/image-to-multiview":   true,
		"tripo/edit-multiview":       true,
		"meshy/processing/remesh":    true,
		"meshy/processing/retexture": true,
		// #330 Meshy Convert: the six required model files, one per format.
		"meshy/processing/convert":          true,
		"tripo/tripo-v3.1/text-to-3d":       true,
		"tripo/tripo-v3.1/image-to-3d":      true,
		"tripo/tripo-v3.1-full/text-to-3d":  true,
		"tripo/tripo-v3.1-full/image-to-3d": true,
		// #188 Rodin: one required PBR GLB plus the background preview.
		"hyper3d/rodin-gen-2.5/text-to-3d":  true,
		"hyper3d/rodin-gen-2.5/image-to-3d": true,
		// #185 geometry: original GLB plus five required exports; Smart
		// Segmentation adds its mask slot.
		"tripo/geometry/decimate-triangle":  true,
		"tripo/geometry/decimate-quad":      true,
		"tripo/geometry/segment":            true,
		"tripo/geometry/complete":           true,
		"tripo/geometry/quick-cap":          true,
		"tripo/geometry/smartsegment-image": true,
		"tripo/geometry/smartsegment-model": true,
		// #187 Bang and Texture: one required PBR GLB each.
		"hyper3d/bang/rodin-task":     true,
		"hyper3d/bang/local-model":    true,
		"hyper3d/texture/local-model": true,
		// #189 Creative Lab first batch: exact image, model, material, texture
		// and print slots from the current official stage contracts.
		"meshy/creative-lab/fridge-magnet/prototype":     true,
		"meshy/creative-lab/fridge-magnet/build":         true,
		"meshy/creative-lab/figure/prototype":            true,
		"meshy/creative-lab/figure/build":                true,
		"meshy/creative-lab/vinyl-figure/prototype":      true,
		"meshy/creative-lab/vinyl-figure/build":          true,
		"meshy/creative-lab/brick-figure/prototype":      true,
		"meshy/creative-lab/brick-figure/build":          true,
		"meshy/creative-lab/fidget-collapsible/generate": true,
		"meshy/creative-lab/keychain/prototype":          true,
		"meshy/creative-lab/keychain/build":              true,
		"meshy/creative-lab/lamp/prototype":              true,
		"meshy/creative-lab/lamp/build":                  true,
		"meshy/creative-lab/fidget-pixel/prototype":      true,
		"meshy/creative-lab/fidget-pixel/build":          true,
		"meshy/creative-lab/keycap/prototype":            true,
		"meshy/creative-lab/keycap/build":                true,
	}
	for _, offering := range taskCatalog.List("") {
		if replaced[offering.ID] {
			continue
		}
		for _, output := range offering.Outputs {
			if output.Variant != "" || output.VariantPrefix != "" ||
				output.Delivery != "" || output.Group != "" {
				t.Fatalf(
					"offering %s declares W1 semantics outside scope: %#v",
					offering.ID,
					output,
				)
			}
		}
	}
}
