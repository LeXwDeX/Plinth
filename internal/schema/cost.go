package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
)

// EvaluateExtraCredits 按冻结 Offering 的 cost 声明与规范化后的用户
// 选择计算预计额外积分。两种互斥形态（加载期校验）：
//   - extra_when（标量形态）：任一条件命中即计一次 extra_credits
//     （不按命中数累加；与"任一附加参数 +5"的公开口径一致）；
//   - selection_parameter（多选形态，W6 生成期同意）：每个选中项计
//     一次 extra_credits（total = extra_credits × 选中项数；每个选中
//     转换至多计费一次，不与基础/标量额外重复累计）。
//
// 无可触发声明时恒为 0。输入不可解码时返回错误（fail-closed），
// 调用方必须放弃发送/拒绝提交，不得猜测金额。
//
// 这是全仓唯一允许计算额外积分金额的代码位（R7 单源约束）：提交期
// 服务端校验（catalog.Normalize）、Provider 私有发送前规划器和
// Offering.EstimateTotalCredits 的展示口径都调用它，以同一冻结声明 +
// 同一用户选择得到同一数字（输入 parity）。
func EvaluateExtraCredits(cost CostDeclaration, selection json.RawMessage) (int, error) {
	values, err := costValues(selection)
	if err != nil {
		return 0, err
	}
	if cost.SelectionParameter != "" {
		items, _ := values[cost.SelectionParameter].([]any)
		count := 0
		for _, item := range items {
			if text, ok := item.(string); ok && text != "" {
				count++
			}
		}
		return cost.ExtraCredits * count, nil
	}
	for _, condition := range cost.ExtraWhen {
		if ConditionMatches(condition, values) {
			return cost.ExtraCredits, nil
		}
	}
	return 0, nil
}

func costValues(selection json.RawMessage) (map[string]any, error) {
	trimmed := bytes.TrimSpace(selection)
	if len(trimmed) == 0 {
		trimmed = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var values map[string]any
	if err := decoder.Decode(&values); err != nil {
		return nil, fmt.Errorf("成本评估输入不是 JSON 对象: %w", err)
	}
	if values == nil {
		values = map[string]any{}
	}
	return values, nil
}

// ExtraCreditDetail says what the extra credits of one frozen input are for.
// Credits comes from EvaluateExtraCredits, the only place an amount is
// computed; Label and Items are display names read from the Offering's own
// declaration, so a client can word the line without knowing any model.
type ExtraCreditDetail struct {
	// Credits are the extra credits the input incurs (0 when it incurs none).
	Credits int `json:"credits"`
	// Label names the kind of option that costs extra, for example the extra
	// export formats. It is the cost declaration's own display name.
	Label string `json:"label,omitempty"`
	// Items are the chosen billed options by display name.
	Items []string `json:"items,omitempty"`
}

// ExtraCreditDetail evaluates the Offering's cost declaration against a
// normalized input. An Offering without extra-credit pricing, or an input that
// cannot be evaluated, yields the zero detail; submit-time validation stays
// fail-closed on its own.
func (o Offering) ExtraCreditDetail(input json.RawMessage) ExtraCreditDetail {
	if o.Cost == nil || o.Cost.ExtraCredits <= 0 {
		return ExtraCreditDetail{}
	}
	credits, err := EvaluateExtraCredits(*o.Cost, input)
	if err != nil || credits <= 0 {
		return ExtraCreditDetail{}
	}
	detail := ExtraCreditDetail{Credits: credits, Label: o.Cost.ExtraLabel}
	values, err := costValues(input)
	if err != nil {
		return detail
	}
	named := func(name string) (Parameter, bool) {
		for _, parameter := range o.Parameters {
			if parameter.Name == name {
				return parameter, true
			}
		}
		return Parameter{}, false
	}
	if o.Cost.SelectionParameter != "" {
		parameter, _ := named(o.Cost.SelectionParameter)
		if detail.Label == "" {
			detail.Label = parameter.Label
		}
		chosen, _ := values[o.Cost.SelectionParameter].([]any)
		for _, item := range chosen {
			value, ok := item.(string)
			if !ok || value == "" {
				continue
			}
			label := value
			for _, option := range parameter.Options {
				if option.Value == value && option.Label != "" {
					label = option.Label
				}
			}
			detail.Items = append(detail.Items, label)
		}
		return detail
	}
	for _, condition := range o.Cost.ExtraWhen {
		if !ConditionMatches(condition, values) {
			continue
		}
		parameter, ok := named(condition.Parameter)
		label := condition.Parameter
		if ok && parameter.Label != "" {
			label = parameter.Label
		}
		if !slices.Contains(detail.Items, label) {
			detail.Items = append(detail.Items, label)
		}
	}
	return detail
}
