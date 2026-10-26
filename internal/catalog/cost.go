// cost.go 实现 §5.3 的共享声明式成本评估器：这是全仓唯一允许计算
// 额外积分金额的代码位（R7 单源约束）。提交期服务端校验（Normalize）
// 与 Provider 私有发送前规划器（如 Tripo 转换）都必须调用本函数，
// 以同一冻结声明 + 同一用户选择得到同一数字（输入 parity）。
// 任何第二处金额逻辑（复制公式、独立常量或第二账本）即评审拒收项。
package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// EvaluateExtraCredits 按冻结 Offering 的 cost 声明与规范化后的用户
// 选择计算预计额外积分。实现位于 internal/schema（Offering 预计积分
// 与提交期校验共用同一函数，仍是全仓唯一的金额计算位）；此处只是
// catalog 调用面的入口，不含第二份公式。
func EvaluateExtraCredits(
	cost schemacontract.CostDeclaration,
	selection json.RawMessage,
) (int, error) {
	return schemacontract.EvaluateExtraCredits(cost, selection)
}

// CostConsentAuthorization 汇总一次提交的成本同意裁决（通用规则执行，
// 不含任何 per-provider/per-format 分支）。
type CostConsentAuthorization struct {
	NativeQuote    *schemacontract.NativeQuote
	EstimatedExtra int
}

// AuthorizeExtraCredits 执行 §5.3 提交期三段校验的第二段：
//  1. 以共享评估器计算 estimated_extra（与发送前规划器同函数同输入）；
//  2. estimated_extra > 0 时要求 consent 参数为真值（显式同意）；
//  3. estimated_extra 超出 cost_policy.max_authorized_extra_credits 时
//     拒绝（超出部分须走新的显式公开任务）。
//
// 拒绝文案来自 Schema（cost_policy.messages）；声明缺失对应的消息时
// 返回通用兜底文案。不做任何余额/账本承诺。
func AuthorizeExtraCredits(
	offering schemacontract.Offering,
	normalized json.RawMessage,
) (CostConsentAuthorization, error) {
	if offering.NativePricing != nil {
		q, err := authorizeNativeQuote(offering, normalized)
		return CostConsentAuthorization{NativeQuote: q}, err
	}
	if offering.Cost == nil {
		return CostConsentAuthorization{}, nil
	}
	estimated, err := EvaluateExtraCredits(*offering.Cost, normalized)
	if err != nil {
		return CostConsentAuthorization{}, err
	}
	if estimated <= 0 {
		return CostConsentAuthorization{EstimatedExtra: 0}, nil
	}
	if offering.Cost.ConsentParameter != "" {
		var values map[string]any
		decoder := json.NewDecoder(bytes.NewReader(normalized))
		decoder.UseNumber()
		if err := decoder.Decode(&values); err != nil {
			return CostConsentAuthorization{}, fmt.Errorf("成本同意输入不是 JSON 对象: %w", err)
		}
		consent := schemacontract.ParameterCondition{
			Parameter: offering.Cost.ConsentParameter,
			Operator:  "truthy",
		}
		if !schemacontract.ConditionMatches(consent, values) {
			return CostConsentAuthorization{}, consentError(offering, "consent_required")
		}
	}
	if offering.CostPolicy != nil &&
		estimated > offering.CostPolicy.MaxAuthorizedExtraCredits {
		return CostConsentAuthorization{}, consentError(offering, "over_limit")
	}
	return CostConsentAuthorization{EstimatedExtra: estimated}, nil
}

// consentError 取 Offering 的 cost_policy.messages 或运行时
// runtime.generation_messages 中的文案；缺省兜底为不含任何金额的通用
// 措辞（不向用户伪造账目）。声明文案是 Schema 产品文案，标记为
// UserFacing，直接路径的 400 与 Agent 工具结果都能说明下一步 (#329)。
func consentError(offering schemacontract.Offering, key string) error {
	template := offering.ExecutionMessages.ExtraCreditsConsentRequired
	if key == "over_limit" {
		template = offering.ExecutionMessages.ExtraCreditsOverLimit
	}
	if offering.CostPolicy != nil {
		switch key {
		case "over_limit":
			if offering.CostPolicy.Messages.OverLimit != "" {
				template = offering.CostPolicy.Messages.OverLimit
			}
		default:
			if offering.CostPolicy.Messages.ConsentRequired != "" {
				template = offering.CostPolicy.Messages.ConsentRequired
			}
		}
	}
	if template == "" {
		return fmt.Errorf("%s", "本次提交包含需要显式同意的额外积分消耗")
	}
	return &schemacontract.Error{
		Code:           schemacontract.ErrInvalidInput,
		Operation:      "authorize_extra_credits",
		Detail:         template,
		UserFacing:     true,
		DetailTemplate: template,
	}
}

// UnknownCostError 返回发送前规划器的 fail-closed 文案（cost 声明缺失
// 或不可评估时使用；文案同样来自 Schema，缺省兜底）。
func UnknownCostError(offering schemacontract.Offering) error {
	detail := "该供给项缺少可核对的成本声明，已按未知成本拒绝发送"
	if offering.CostPolicy != nil && offering.CostPolicy.Messages.UnknownCost != "" {
		detail = offering.CostPolicy.Messages.UnknownCost
	}
	return fmt.Errorf("%s", detail)
}
