package catalog_test

// variant_derivation_schema_test.go 锁定逐项展开声明（回顾性整改 P2）的
// 加载期校验与默认目录形状：variant_from 必须指向带 options 的
// string-array 参数、必须与 cost.selection_parameter 一致、只能用于带
// variant_prefix 的 on-demand 声明；variant_case 限定枚举。默认目录的两个
// tripo 生成 Offering 必须声明该形状——内核据此为每个被请求项建立独立
// 持久身份，代码中不含任何格式枚举。

import (
	"bytes"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// variantDerivationDocument 构造一个带多选参数 + 成本同意 + 逐项 on-demand
// 输出的最小合法 Offering 文档；outputs 与 parameters 可被用例替换。
func variantDerivationDocument(outputs string, parameters string) []byte {
	if parameters == "" {
		parameters = `[
				{"name":"prompt","label":"提示词","type":"text","required":true},
				{"name":"output_formats","label":"附加导出格式","type":"string-array",
				 "options":[{"value":"GLTF","label":"GLTF"},{"value":"FBX","label":"FBX"}]},
				{"name":"authorize_extra_credits","label":"同意","type":"boolean"}
			]`
	}
	return testProductDocument(`[
		{
			"id":"one","revision":"1","provider_id":"p","provider_name":"P","provider_short_label":"P",
			"model_id":"m","model_name":"M","capability":"text-to-image",
			"domain":"image","display_order":0,"capability_order":0,
			"adapter_key":"a","enabled":true,"timeout_seconds":1,
			"poll_interval_seconds":1,
			"retry":{"max_attempts":1},
			"materialize_attempts":1,
			"parameters":` + parameters + `,
			"cost":{"extra_credits":5,"selection_parameter":"output_formats",
				"consent_parameter":"authorize_extra_credits","extra_label":"extra formats"},
			"cost_policy":{"max_authorized_extra_credits":10,
				"messages":{"consent_required":"c","over_limit":"o","unknown_cost":"u"}},
			"outputs":` + outputs + `
		}
	]`)
}

func TestVariantDerivationSchemaAcceptsDeclaredExpansion(t *testing.T) {
	taskCatalog, err := catalog.Load(bytes.NewReader(variantDerivationDocument(`[
		{"role":"model","minimum":1},
		{"role":"converted_model","variant_prefix":"converted.","variant_from":"output_formats",
		 "variant_case":"lower","delivery":"on-demand","minimum":0}
	]`, "")))
	if err != nil {
		t.Fatalf("合法逐项声明被拒绝: %v", err)
	}
	offering, ok := taskCatalog.Get("one")
	if !ok {
		t.Fatal("offering missing")
	}
	var converted *generation.AssetRequirement
	for index := range offering.Outputs {
		if offering.Outputs[index].Role == "converted_model" {
			converted = &offering.Outputs[index]
		}
	}
	if converted == nil {
		t.Fatal("converted_model 声明缺失")
	}
	if converted.VariantFrom != "output_formats" || converted.VariantCase != generation.VariantCaseLower {
		t.Fatalf("逐项声明未透传：%#v", converted)
	}
	if variant := generation.SelectionVariant(*converted, "GLTF"); variant != "converted.gltf" {
		t.Fatalf("SelectionVariant = %q; want converted.gltf", variant)
	}
}

func TestVariantDerivationSchemaRejectsInvalidDeclarations(t *testing.T) {
	cases := []struct {
		name    string
		outputs string
		want    string
	}{
		{
			name: "unknown parameter",
			outputs: `[{"role":"model","minimum":1},
				{"role":"converted_model","variant_prefix":"converted.","variant_from":"nope",
				 "delivery":"on-demand","minimum":0}]`,
			want: "未知参数",
		},
		{
			name: "missing variant_prefix",
			outputs: `[{"role":"model","minimum":1},
				{"role":"converted_model","variant_from":"output_formats",
				 "delivery":"on-demand","minimum":0}]`,
			want: "variant_prefix",
		},
		{
			name: "wrong delivery",
			outputs: `[{"role":"model","minimum":1},
				{"role":"converted_model","variant_prefix":"converted.","variant_from":"output_formats",
				 "delivery":"background","minimum":0}]`,
			want: "on-demand",
		},
		{
			name: "unknown variant_case",
			outputs: `[{"role":"model","minimum":1},
				{"role":"converted_model","variant_prefix":"converted.","variant_from":"output_formats",
				 "variant_case":"title","delivery":"on-demand","minimum":0}]`,
			want: "variant_case",
		},
		{
			name: "variant_case without variant_from",
			outputs: `[{"role":"model","minimum":1},
				{"role":"converted_model","variant_prefix":"converted.","variant_case":"lower",
				 "delivery":"on-demand","minimum":0}]`,
			want: "variant_from",
		},
		{
			name: "required delivery cannot expand",
			outputs: `[{"role":"model","minimum":1},
				{"role":"converted_model","variant_prefix":"converted.","variant_from":"output_formats",
				 "delivery":"required","minimum":1}]`,
			want: "on-demand",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := catalog.Load(bytes.NewReader(variantDerivationDocument(tc.outputs, "")))
			if err == nil {
				t.Fatalf("非法逐项声明被接受（want 含 %q）", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v; want 含 %q", err, tc.want)
			}
		})
	}
}

// TestVariantDerivationSchemaRejectsSelectionParameterDivergence：variant_from
// 与 cost.selection_parameter 不一致即拒绝（选中项身份不得出现第二真源）。
func TestVariantDerivationSchemaRejectsSelectionParameterDivergence(t *testing.T) {
	parameters := `[
		{"name":"prompt","label":"提示词","type":"text","required":true},
		{"name":"output_formats","label":"附加导出格式","type":"string-array",
		 "options":[{"value":"GLTF","label":"GLTF"},{"value":"FBX","label":"FBX"}]},
		{"name":"other_formats","label":"其他格式","type":"string-array",
		 "options":[{"value":"OBJ","label":"OBJ"}]},
		{"name":"authorize_extra_credits","label":"同意","type":"boolean"}
	]`
	_, err := catalog.Load(bytes.NewReader(variantDerivationDocument(`[
		{"role":"model","minimum":1},
		{"role":"converted_model","variant_prefix":"converted.","variant_from":"other_formats",
		 "delivery":"on-demand","minimum":0}
	]`, parameters)))
	if err == nil {
		t.Fatal("variant_from 与 cost.selection_parameter 不一致却被接受")
	}
	if !strings.Contains(err.Error(), "selection_parameter") {
		t.Fatalf("error = %v; want 提及 selection_parameter", err)
	}
}

// TestDefaultCatalogDeclaresPerItemConversionSlots：默认目录的两个 tripo
// 生成 Offering 必须声明逐项展开（内核不含格式枚举的前提）。
func TestDefaultCatalogDeclaresPerItemConversionSlots(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []generation.OfferingID{
		"tripo/tripo-v3.1/text-to-3d",
		"tripo/tripo-v3.1/image-to-3d",
	} {
		offering, ok := taskCatalog.Get(id)
		if !ok {
			t.Fatalf("missing offering %q", id)
		}
		if offering.PriceSelectionParameter() == "" {
			t.Fatalf("%q 缺少 selection_parameter 声明", id)
		}
		found := false
		for _, output := range offering.Outputs {
			if output.Role != "converted_model" {
				continue
			}
			found = true
			if output.VariantFrom != offering.PriceSelectionParameter() {
				t.Fatalf("%q 的 converted_model.variant_from = %q; want %q",
					id, output.VariantFrom, offering.PriceSelectionParameter())
			}
			if output.VariantCase != generation.VariantCaseLower {
				t.Fatalf("%q 的 variant_case = %q; want lower", id, output.VariantCase)
			}
			if output.VariantPrefix == "" {
				t.Fatalf("%q 缺少 variant_prefix", id)
			}
			if generation.SelectionVariant(output, "GLTF") != output.VariantPrefix+"gltf" {
				t.Fatalf("%q 的变体派生不符合声明", id)
			}
		}
		if !found {
			t.Fatalf("%q 缺少 converted_model 输出声明", id)
		}
	}
}
