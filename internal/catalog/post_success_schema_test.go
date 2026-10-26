package catalog_test

// post_success_schema_test.go 落实 §6.1/§5.2 的加载期校验用例（W6）：
// post_success 声明形状、成本声明加固（可产生额外积分必须同时声明
// consent+policy；extra_when 与 selection_parameter 互斥）、
// multi-select 控件形状、多选取值 options 校验、公开投影剥离调度字段。

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// loadCatalogWithOfferingMutation 修改默认目录中某 offering 后整体加载。
func loadCatalogWithOfferingMutation(
	t *testing.T,
	id string,
	mutate func(offering map[string]any),
) error {
	t.Helper()
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	offerings, _ := doc["offerings"].([]any)
	for index, item := range offerings {
		entry, _ := item.(map[string]any)
		if entry["id"] != id {
			continue
		}
		mutate(entry)
		offerings[index] = entry
		break
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	_, loadErr := catalog.Load(bytes.NewReader(encoded))
	return loadErr
}

func TestPostSuccessDeclarationValidation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(map[string]any)
		wantErr string
	}{
		{
			name:    "interval 缺失拒绝",
			mutate:  func(o map[string]any) { o["post_success"] = map[string]any{"observe": true, "deadline_seconds": 100} },
			wantErr: "interval_seconds",
		},
		{
			name: "deadline 小于 interval 拒绝",
			mutate: func(o map[string]any) {
				o["post_success"] = map[string]any{"observe": true, "interval_seconds": 120, "deadline_seconds": 60}
			},
			wantErr: "deadline_seconds",
		},
		{
			name:    "未 observe 却携带调度字段拒绝",
			mutate:  func(o map[string]any) { o["post_success"] = map[string]any{"observe": false, "interval_seconds": 60} },
			wantErr: "post_success",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := loadCatalogWithOfferingMutation(
				t,
				"tripo/tripo-v3.1/text-to-3d",
				testCase.mutate,
			)
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("err = %v; want containing %q", err, testCase.wantErr)
			}
		})
	}
}

func TestCostShapeHardeningRequiresConsentAndPolicy(t *testing.T) {
	// extra_when 存在却删除 consent_parameter → 拒绝。
	if err := loadCatalogWithOfferingMutation(
		t,
		"tripo/tripo-v3.1/format-conversion",
		func(o map[string]any) {
			cost := o["cost"].(map[string]any)
			delete(cost, "consent_parameter")
		},
	); err == nil || !strings.Contains(err.Error(), "consent_parameter") {
		t.Fatalf("err = %v; want consent_parameter 缺失拒绝", err)
	}

	// extra credits without a display name for the approval card → 拒绝。
	if err := loadCatalogWithOfferingMutation(
		t,
		"tripo/tripo-v3.1/format-conversion",
		func(o map[string]any) {
			cost := o["cost"].(map[string]any)
			delete(cost, "extra_label")
		},
	); err == nil || !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("err = %v; want extra_label 缺失拒绝", err)
	}

	// extra_when 存在却删除 cost_policy → 拒绝。
	if err := loadCatalogWithOfferingMutation(
		t,
		"tripo/tripo-v3.1/format-conversion",
		func(o map[string]any) { delete(o, "cost_policy") },
	); err == nil || !strings.Contains(err.Error(), "cost_policy") {
		t.Fatalf("err = %v; want cost_policy 缺失拒绝", err)
	}

	// selection_parameter 与 extra_when 同时声明 → 拒绝。
	if err := loadCatalogWithOfferingMutation(
		t,
		"tripo/tripo-v3.1/format-conversion",
		func(o map[string]any) {
			cost := o["cost"].(map[string]any)
			cost["selection_parameter"] = "format"
		},
	); err == nil || !strings.Contains(err.Error(), "selection_parameter") {
		t.Fatalf("err = %v; want 互斥拒绝", err)
	}
}

func TestGenerationOfferingSelectionShapeIsValid(t *testing.T) {
	// W6 目录实绩：tripo 生成 Offering 的 selection 形态自检（合法形状
	// 必须通过默认加载——由 LoadDefault 隐式验证；这里显式断言关键结构）。
	source, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, exists := source.Get("tripo/tripo-v3.1/text-to-3d")
	if !exists {
		t.Fatal("offering missing")
	}
	if offering.NativePricing == nil || offering.Cost != nil || offering.PriceSelectionParameter() != "output_formats" ||
		offering.ExtraConsentParameter() != "authorize_extra_credits" ||
		offering.NativePricing.Selection == nil || offering.NativePricing.Selection.Amount != "5" ||
		offering.NativePricing.MaxAuthorizedExtraAmount != "125" {
		t.Fatalf("native price/selection = %+v", offering.NativePricing)
	}
	if offering.PostSuccess == nil || !offering.PostSuccess.Observe ||
		offering.PostSuccess.IntervalSeconds != 60 ||
		offering.PostSuccess.DeadlineSeconds != 86400 {
		t.Fatalf("post_success = %#v", offering.PostSuccess)
	}
}

func TestAdvisedMultiSelectRejectsUnknownCallerValues(t *testing.T) {
	source, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, _ := source.Get("tripo/tripo-v3.1-full/text-to-3d")
	if _, _, err := source.Normalize(generation.TextTo3D, offering.ID, json.RawMessage(`{"prompt":"x","output_formats":["GLTF","WOFF"],"authorize_extra_credits":true}`)); err == nil {
		t.Fatal("unknown advised format accepted")
	}
}

func TestPublicSchemaStripsPostSuccessSchedulingFields(t *testing.T) {
	source, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(source.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	// 调度字段（interval/deadline）不得出现在公开面；"post_success."
	// 前缀的 message key 是公开文案（资源态 reason 的解析真源），
	// 不属于调度内部，不在哨兵之列。
	for _, sentinel := range []string{
		"interval_seconds", "deadline_seconds",
	} {
		if strings.Contains(string(encoded), sentinel) {
			t.Fatalf("public schema leaked %q", sentinel)
		}
	}
}
