package catalog_test

// cost_consent_schema_test.go 覆盖 §5.2 成本声明的加载期校验与 §5.3
// 提交期同意裁决：声明缺形（未知参数/非布尔同意参数/无 options 的
// 格式参数/孤儿 cost_policy/负上限）必须加载失败；Normalize 对
// 缺同意、超上限的提交以 Schema 文案拒绝，授权内提交冻结用户选择。

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func loadCatalogWithConversionMutation(
	t *testing.T,
	mutate func(offering map[string]any),
) (*catalog.Catalog, error) {
	t.Helper()
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&doc); err != nil {
		t.Fatal(err)
	}
	offerings, _ := doc["offerings"].([]any)
	for index, item := range offerings {
		entry, _ := item.(map[string]any)
		if entry["id"] != "tripo/tripo-v3.1/format-conversion" {
			continue
		}
		entry["enabled"] = true // shipped disabled; contract tests need it live
		mutate(entry)
		offerings[index] = entry
		break
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return catalog.Load(bytes.NewReader(encoded))
}

func TestConversionOfferingDeclaresApprovedCostContract(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, exists := taskCatalog.Get("tripo/tripo-v3.1/format-conversion")
	if !exists {
		t.Fatal("缺少格式转换 offering")
	}
	if offering.Cost == nil || offering.Cost.BaseCredits != 5 || offering.Cost.ExtraCredits != 5 {
		t.Fatalf("cost = %#v（批准计划口径：基础 5、附加 +5，次级证据来源）", offering.Cost)
	}
	if offering.Cost.ConsentParameter != "authorize_extra_credits" ||
		offering.Cost.FormatParameter != "format" {
		t.Fatalf("consent/format 参数声明 = %#v", offering.Cost)
	}
	if offering.CostPolicy == nil ||
		offering.CostPolicy.MaxAuthorizedExtraCredits != 5 {
		t.Fatalf("cost_policy = %#v", offering.CostPolicy)
	}
	// 批准的六个格式；GLB 未获交叉验证，不得出现。
	formatParameter, _ := parameterNamed(offering, "format")
	if formatParameter == nil {
		t.Fatal("缺少 format 参数")
	}
	declared := map[string]bool{}
	for _, option := range formatParameter.Options {
		declared[option.Value] = true
	}
	for _, expected := range []string{"GLTF", "USDZ", "FBX", "OBJ", "STL", "3MF"} {
		if !declared[expected] {
			t.Errorf("format options 缺少 %s", expected)
		}
	}
	if declared["GLB"] {
		t.Error("GLB 未获交叉验证，不得声明为可选格式")
	}
	// 输出契约：显式 (role, variant) required。
	if len(offering.Outputs) != 1 ||
		offering.Outputs[0].Role != "model" ||
		offering.Outputs[0].Variant != "converted" ||
		offering.Outputs[0].Delivery != generation.AssetDeliveryRequired {
		t.Fatalf("outputs = %#v", offering.Outputs)
	}
}

func parameterNamed(
	offering generation.Offering,
	name string,
) (*generation.Parameter, bool) {
	for index, parameter := range offering.Parameters {
		if parameter.Name == name {
			return &offering.Parameters[index], true
		}
	}
	return nil, false
}

func TestCostDeclarationValidationRejectsMalformedShapes(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(offering map[string]any)
		error  string
	}{
		{
			name: "extra_when 引用未知参数",
			mutate: func(offering map[string]any) {
				cost, _ := offering["cost"].(map[string]any)
				cost["extra_when"] = []any{
					map[string]any{"parameter": "nonexistent", "operator": "truthy"},
				}
			},
			error: "未知参数",
		},
		{
			name: "extra_when 非空但 extra_credits 为 0",
			mutate: func(offering map[string]any) {
				cost, _ := offering["cost"].(map[string]any)
				cost["extra_credits"] = 0
			},
			error: "extra_credits 必须大于 0",
		},
		{
			name: "consent_parameter 非布尔参数",
			mutate: func(offering map[string]any) {
				cost, _ := offering["cost"].(map[string]any)
				cost["consent_parameter"] = "format"
			},
			error: "必须是 boolean 参数",
		},
		{
			name: "format_parameter 非 select 参数",
			mutate: func(offering map[string]any) {
				cost, _ := offering["cost"].(map[string]any)
				cost["format_parameter"] = "quad"
			},
			error: "带 options 的 select 参数",
		},
		{
			name: "孤儿 cost_policy",
			mutate: func(offering map[string]any) {
				delete(offering, "cost")
			},
			error: "却缺少 cost 声明",
		},
		{
			name: "负上限",
			mutate: func(offering map[string]any) {
				policy, _ := offering["cost_policy"].(map[string]any)
				policy["max_authorized_extra_credits"] = -1
			},
			error: "不能为负数",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := loadCatalogWithConversionMutation(t, testCase.mutate)
			if err == nil || !strings.Contains(err.Error(), testCase.error) {
				t.Fatalf("err = %v; want 包含 %q", err, testCase.error)
			}
		})
	}
}

func TestNormalizeEnforcesConversionConsent(t *testing.T) {
	// The default Schema keeps conversion disabled; the consent contract of
	// the adapter-backed Offering is still exercised with it enabled.
	taskCatalog := catalogtest.LoadDefaultWithTripoConversion(t)
	source := "task-source-1"
	cases := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name:  "基础转换无需同意参数",
			input: `{"source_task":"` + source + `","format":"GLTF"}`,
		},
		{
			name:  "授权内附加选项 + 显式同意",
			input: `{"source_task":"` + source + `","format":"FBX","quad":true,"authorize_extra_credits":true}`,
		},
		{
			name:    "附加选项缺同意",
			input:   `{"source_task":"` + source + `","format":"FBX","quad":true}`,
			wantErr: "请先勾选额外积分消耗同意",
		},
		{
			name:    "同意为假值仍拒绝",
			input:   `{"source_task":"` + source + `","format":"FBX","quad":true,"authorize_extra_credits":false}`,
			wantErr: "请先勾选额外积分消耗同意",
		},
		{
			name:    "未知格式拒绝",
			input:   `{"source_task":"` + source + `","format":"GLB"}`,
			wantErr: "不在允许范围内",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			offering, normalized, err := taskCatalog.Normalize(
				generation.FormatConversion,
				"tripo/tripo-v3.1/format-conversion",
				json.RawMessage(testCase.input),
			)
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("err = %v; want 包含 %q", err, testCase.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			// 授权内提交：用户选择连同同意证据冻结进任务输入。
			var frozen map[string]any
			if err := json.Unmarshal(normalized, &frozen); err != nil {
				t.Fatal(err)
			}
			if frozen["format"] == nil || frozen["source_task"] != source {
				t.Fatalf("frozen input = %#v", frozen)
			}
			if offering.Cost == nil {
				t.Fatal("返回的 offering 必须携带冻结 cost 声明（规划器 parity 前提）")
			}
		})
	}
}

func TestNormalizeRejectsOverLimitSelection(t *testing.T) {
	// 把授权上限压到 0：任何附加选项都超界。
	taskCatalog, err := loadCatalogWithConversionMutation(t, func(offering map[string]any) {
		policy, _ := offering["cost_policy"].(map[string]any)
		policy["max_authorized_extra_credits"] = 0
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = taskCatalog.Normalize(
		generation.FormatConversion,
		"tripo/tripo-v3.1/format-conversion",
		json.RawMessage(`{"source_task":"task-source-1","format":"FBX","quad":true,"authorize_extra_credits":true}`),
	)
	if err == nil || !strings.Contains(err.Error(), "超出本次提交的授权上限") {
		t.Fatalf("err = %v; want 超上限以 Schema 文案拒绝", err)
	}
	// 基础转换不受影响。
	_, _, err = taskCatalog.Normalize(
		generation.FormatConversion,
		"tripo/tripo-v3.1/format-conversion",
		json.RawMessage(`{"source_task":"task-source-1","format":"FBX"}`),
	)
	if err != nil {
		t.Fatalf("基础转换不应被上限拒绝: %v", err)
	}
}
