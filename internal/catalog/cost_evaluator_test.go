package catalog_test

// cost_evaluator_test.go 落实 §5.5 parity 用例：共享声明式评估器是
// 全仓唯一金额计算实现；提交期校验与发送前规划器经同一函数、同一
// 冻结声明 + 同一用户选择必须得到同一数字。这里同时覆盖边界形状
//（缺省/空输入/不可解码输入 fail-closed/任一命中只计一次）。

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func conversionCostFixture() generation.CostDeclaration {
	return generation.CostDeclaration{
		BaseCredits:  5,
		ExtraCredits: 5,
		ExtraWhen: []generation.ParameterCondition{
			{Parameter: "quad", Operator: "truthy"},
			{Parameter: "face_limit", Operator: "present"},
			{Parameter: "texture_format", Operator: "present"},
			{Parameter: "flatten_bottom", Operator: "truthy"},
		},
		ConsentParameter: "authorize_extra_credits",
		FormatParameter:  "format",
	}
}

func TestEvaluateExtraCreditsMatrix(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected int
	}{
		{"empty input", `{}`, 0},
		{"nil input", ``, 0},
		{"format only", `{"format":"GLTF"}`, 0},
		{"quad true", `{"format":"FBX","quad":true}`, 5},
		{"quad false is falsy", `{"format":"FBX","quad":false}`, 0},
		{"face_limit present", `{"face_limit":100000}`, 5},
		{"face_limit zero still present", `{"face_limit":0}`, 5},
		{"texture_format present", `{"texture_format":"PNG"}`, 5},
		{"flatten_bottom truthy", `{"flatten_bottom":true}`, 5},
		{"multiple hits charge once", `{"quad":true,"face_limit":1,"texture_format":"PNG"}`, 5},
		{"consent alone is not extra", `{"authorize_extra_credits":true}`, 0},
		{"unknown keys ignored", `{"unknown_option":true}`, 0},
	}
	cost := conversionCostFixture()
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			estimated, err := catalog.EvaluateExtraCredits(cost, json.RawMessage(testCase.input))
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if estimated != testCase.expected {
				t.Fatalf("estimated = %d; want %d", estimated, testCase.expected)
			}
		})
	}
}

func TestEvaluateExtraCreditsFailsClosedOnUndecodableInput(t *testing.T) {
	cost := conversionCostFixture()
	if _, err := catalog.EvaluateExtraCredits(cost, json.RawMessage(`{not json`)); err == nil {
		t.Fatal("不可解码输入必须返回错误（fail-closed），不得猜测金额")
	}
	// 非 JSON 数组同样拒绝。
	if _, err := catalog.EvaluateExtraCredits(cost, json.RawMessage(`[1,2]`)); err == nil {
		t.Fatal("非对象输入必须返回错误")
	}
}

// TestAuthorizeExtraCreditsParityWithEvaluator 锁定提交期裁决与评估器
// 数字的 parity：AuthorizeExtraCredits 的 EstimatedExtra 必须与
// EvaluateExtraCredits 完全一致（同一冻结声明 + 同一输入）。
func TestAuthorizeExtraCreditsParityWithEvaluator(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, exists := taskCatalog.Get("tripo/tripo-v3.1/format-conversion")
	if !exists {
		t.Fatal("默认目录缺少格式转换 offering")
	}
	if offering.Cost == nil {
		t.Fatal("格式转换 offering 缺少 cost 声明")
	}
	inputs := []string{
		`{}`,
		`{"format":"GLTF"}`,
		`{"format":"FBX","quad":true,"authorize_extra_credits":true}`,
		`{"format":"OBJ","face_limit":150000,"authorize_extra_credits":true}`,
		`{"format":"STL","texture_format":"PNG","authorize_extra_credits":false}`,
		`{"format":"USDZ","flatten_bottom":true,"authorize_extra_credits":true}`,
		`{"format":"3MF","quad":true,"face_limit":1,"texture_size":4096,"texture_format":"JPEG","flatten_bottom":true,"pivot_to_center_bottom":true,"authorize_extra_credits":true}`,
	}
	for _, input := range inputs {
		estimated, err := catalog.EvaluateExtraCredits(*offering.Cost, json.RawMessage(input))
		if err != nil {
			t.Fatalf("evaluate %s: %v", input, err)
		}
		authorization, err := catalog.AuthorizeExtraCredits(offering, json.RawMessage(input))
		if err != nil {
			// 拒绝只允许发生在 estimated>0 的同意/上限裁决上。
			if estimated == 0 {
				t.Fatalf("authorize %s 拒绝了 estimated=0 的输入: %v", input, err)
			}
			continue
		}
		if authorization.EstimatedExtra != estimated {
			t.Fatalf(
				"parity broken for %s: authorize=%d evaluate=%d",
				input,
				authorization.EstimatedExtra,
				estimated,
			)
		}
	}
}
