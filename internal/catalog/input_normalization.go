package catalog

// Offering input normalization and submitted parameter validation.
import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
	"math"
	"strconv"
	"strings"
)

func normalizeInput(offering schemacontract.Offering, raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var values map[string]any
	if err := decoder.Decode(&values); err != nil {
		return nil, fmt.Errorf("输入必须是 JSON 对象: %w", err)
	}
	if values == nil {
		return nil, errors.New("输入必须是 JSON 对象")
	}

	schemas := make(map[string]schemacontract.Parameter, len(offering.Parameters))
	for _, schema := range offering.Parameters {
		schemas[schema.Name] = schema
	}
	for name := range values {
		if _, ok := schemas[name]; !ok {
			return nil, fmt.Errorf("供给项 %q 不接受参数 %q", offering.ID, name)
		}
	}
	for _, schema := range offering.Parameters {
		// fixed 参数由后端固定：调用方取值一律以默认值覆盖。
		if _, exists := values[schema.Name]; (!exists || schema.Fixed) && len(schema.Default) > 0 {
			var defaultValue any
			decoder := json.NewDecoder(bytes.NewReader(schema.Default))
			decoder.UseNumber()
			if err := decoder.Decode(&defaultValue); err != nil {
				return nil, fmt.Errorf("参数 %q 默认值无效", schema.Name)
			}
			values[schema.Name] = defaultValue
		}
	}
	// forced_when 是服务端最终裁决：在默认值就位后、逐参数校验前应用，
	// 用户输入与后续 input_rules 都看到强制后的取值。
	schemacontract.ApplyForcedValues(offering.Parameters, values)
	for _, schema := range offering.Parameters {
		value, exists := values[schema.Name]
		if !exists {
			if schema.Required {
				return nil, fmt.Errorf("缺少必填参数 %q", schema.Name)
			}
			continue
		}
		normalized, err := validateValue(schema, value)
		if err != nil {
			return nil, referenceLimitRefusal(offering, err)
		}
		values[schema.Name] = normalized
	}
	for index, rule := range offering.InputRules {
		if len(rule.ForbiddenWhen) > 0 {
			matched := true
			for _, c := range rule.ForbiddenWhen {
				if !schemacontract.ParameterOmitted(schemacontract.Parameter{OmitWhen: []schemacontract.ParameterCondition{c}}, values) {
					matched = false
				}
			}
			if matched {
				return nil, fmt.Errorf("%s", rule.Message)
			}
			continue
		}
		if rule.MaxTotalItems > 0 {
			total := 0
			for _, name := range rule.Parameters {
				p, _ := offeringParameter(offering.Parameters, name)
				if schemacontract.ParameterOmitted(p, values) {
					continue
				}
				switch v := values[name].(type) {
				case []string:
					total += len(v)
				case []any:
					total += len(v)
				default:
					if n, ok := asFloat(v); ok {
						total += int(n)
					}
				}
			}
			if total > rule.MaxTotalItems {
				return nil, fmt.Errorf("%s", rule.Message)
			}
			continue
		}
		present := 0
		for _, name := range rule.Parameters {
			value, exists := values[name]
			if exists && inputValuePresent(value) {
				present++
			}
		}
		if present < rule.Minimum || present > rule.Maximum {
			return nil, fmt.Errorf(
				"输入规则 %d 要求参数 %v 至少出现 %d 个、至多出现 %d 个",
				index,
				rule.Parameters,
				rule.Minimum,
				rule.Maximum,
			)
		}
	}
	if err := deliverParametersThroughPrompt(offering, values); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("规范化输入失败: %w", err)
	}
	// §5.3 提交期成本同意校验（通用规则执行）：以共享声明式评估器计算
	// estimated_extra；>0 时要求显式同意参数为真值且不超过
	// cost_policy.max_authorized_extra_credits。拒绝文案来自 Schema。
	if _, err := AuthorizeExtraCredits(offering, canonical); err != nil {
		return nil, err
	}
	return canonical, nil
}

// arrayLimitError is the typed form of "more elements than max_items" so a
// reference-image list can be refused with the Schema's reason instead of
// the diagnostic parameter text (#329). Other arrays keep the same message.
type arrayLimitError struct {
	parameter schemacontract.Parameter
	actual    int
}

func (e *arrayLimitError) Error() string {
	return fmt.Sprintf("参数 %q 最多允许 %d 个元素", e.parameter.Name, e.parameter.MaxItems)
}

// referenceLimitRefusal turns an over-limit reference-image list into a
// requester-visible reason filled from the runtime.generation_messages
// template with the public label and both counts. Any other error is
// returned unchanged.
func referenceLimitRefusal(offering schemacontract.Offering, err error) error {
	var limit *arrayLimitError
	if !errors.As(err, &limit) || limit.parameter.Semantic != schemacontract.SemanticReferenceImage ||
		limit.parameter.Type != "asset-array" {
		return err
	}
	detail, values := offering.ExecutionMessages.ReferenceLimitExceededText(
		limit.parameter.Label, limit.actual, limit.parameter.MaxItems)
	if detail == "" {
		return err
	}
	return &schemacontract.Error{
		Code:           schemacontract.ErrInvalidInput,
		Operation:      "normalize_input",
		Detail:         detail,
		UserFacing:     true,
		DetailTemplate: offering.ExecutionMessages.ReferenceLimitExceeded,
		DetailValues:   values,
		Cause:          err,
	}
}

func inputValuePresent(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case []string:
		return len(typed) > 0
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

func validateValue(schema schemacontract.Parameter, value any) (any, error) {
	switch schema.Type {
	case "text", "select", "asset", "task":
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("参数 %q 必须是字符串", schema.Name)
		}
		if schema.Required && strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("参数 %q 不能为空", schema.Name)
		}
		if schema.MaxLength > 0 && len([]rune(text)) > schema.MaxLength {
			return nil, fmt.Errorf("参数 %q 最多允许 %d 个字符", schema.Name, schema.MaxLength)
		}
		if schema.ImageSize != nil && (strings.Contains(text, "x") || !optionAllowed(schema.Options, text)) {
			if err := validateImageSize(*schema.ImageSize, text); err != nil {
				return nil, fmt.Errorf("参数 %q: %w", schema.Name, err)
			}
		} else if schema.Type == "select" && !optionAllowed(schema.Options, text) {
			return nil, fmt.Errorf("参数 %q 的值 %q 不在允许范围内", schema.Name, text)
		}
		return text, nil
	case "boolean":
		boolean, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("参数 %q 必须是布尔值", schema.Name)
		}
		return boolean, nil
	case "number":
		number, ok := asFloat(value)
		if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, fmt.Errorf("参数 %q 必须是有效数字", schema.Name)
		}
		if schema.Integer && math.Trunc(number) != number {
			return nil, fmt.Errorf("参数 %q 必须是整数", schema.Name)
		}
		if schema.Minimum != nil && number < *schema.Minimum {
			return nil, fmt.Errorf("参数 %q 不能小于 %v", schema.Name, *schema.Minimum)
		}
		if schema.Maximum != nil && number > *schema.Maximum {
			return nil, fmt.Errorf("参数 %q 不能大于 %v", schema.Name, *schema.Maximum)
		}
		// 带 options 的 number 是封闭枚举（如 Meshy 动作库 action_id，编号
		// 不连续）：取值必须等于某个声明选项，未声明的编号在付费提交前拒绝。
		if len(schema.Options) > 0 && !numberOptionAllowed(schema.Options, number) {
			return nil, fmt.Errorf("参数 %q 的值 %v 不在允许范围内", schema.Name, number)
		}
		return number, nil
	case "string-array", "asset-array":
		items, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("参数 %q 必须是数组", schema.Name)
		}
		result := make([]string, 0, len(items))
		seen := make(map[string]struct{}, len(items))
		for _, item := range items {
			text, ok := item.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("参数 %q 的数组元素必须是非空字符串", schema.Name)
			}
			// 带选项的 string-array 是受限多选（如格式多选）：每个元素
			// 必须落在声明 options 内（未知取值 fail-closed，与服务端
			// 转换规划器同一冻结词表）。
			if schema.Type == "string-array" && len(schema.Options) > 0 &&
				!optionAllowed(schema.Options, text) {
				return nil, fmt.Errorf("参数 %q 的取值 %q 不在声明的可选范围内", schema.Name, text)
			}
			if schema.Type == "string-array" {
				if _, duplicate := seen[text]; duplicate {
					return nil, fmt.Errorf("参数 %q 的取值 %q 不能重复", schema.Name, text)
				}
				seen[text] = struct{}{}
			}
			result = append(result, text)
		}
		if schema.Required && len(result) == 0 {
			return nil, fmt.Errorf("参数 %q 至少需要一个元素", schema.Name)
		}
		if schema.MinItems > 0 && len(result) < schema.MinItems {
			return nil, fmt.Errorf("参数 %q 至少需要 %d 个元素", schema.Name, schema.MinItems)
		}
		if schema.MaxItems > 0 && len(result) > schema.MaxItems {
			return nil, &arrayLimitError{parameter: schema, actual: len(result)}
		}
		return result, nil
	default:
		return nil, fmt.Errorf("参数 %q 使用了未知类型 %q", schema.Name, schema.Type)
	}
}

func asFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	default:
		return 0, false
	}
}

func numberOptionAllowed(options []schemacontract.ParameterOption, value float64) bool {
	for _, option := range options {
		if parsed, err := strconv.ParseFloat(option.Value, 64); err == nil && parsed == value {
			return true
		}
	}
	return false
}

func optionAllowed(options []schemacontract.ParameterOption, value string) bool {
	for _, option := range options {
		if option.Value == value {
			return true
		}
	}
	return false
}

// deliverParametersThroughPrompt 执行 prompt_delivery 声明：已校验的取值按模板
// 渲染后另起一行追加到提示词参数，追加后重新校验提示词（max_length 等仍成立）。
// 被传递参数留在规范化输入里，由 schema.WireInput 在交给 Provider 前移除。
// 规范化必须幂等：准备输入和提交时会再次规范化并比较结果，所以提示词已以该行
// 结尾时不再追加。
func deliverParametersThroughPrompt(offering schemacontract.Offering, values map[string]any) error {
	var promptSchema *schemacontract.Parameter
	for index := range offering.Parameters {
		if offering.Parameters[index].Semantic == schemacontract.SemanticPrompt {
			promptSchema = &offering.Parameters[index]
			break
		}
	}
	if promptSchema == nil {
		return nil
	}
	for _, schema := range offering.Parameters {
		if schema.PromptDelivery == nil {
			continue
		}
		value, exists := values[schema.Name]
		if !exists || schemacontract.ParameterOmitted(schema, values) {
			continue
		}
		rendered := strings.ReplaceAll(schema.PromptDelivery.Template, "{value}", fmt.Sprint(value))
		prompt, _ := values[promptSchema.Name].(string)
		switch {
		case strings.TrimSpace(prompt) == "":
			prompt = rendered
		case prompt == rendered || strings.HasSuffix(prompt, "\n"+rendered):
			continue
		default:
			prompt += "\n" + rendered
		}
		normalized, err := validateValue(*promptSchema, prompt)
		if err != nil {
			return err
		}
		values[promptSchema.Name] = normalized
	}
	return nil
}
