package httpapi

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// taskInputResponse 是任务冻结输入的公开投影，只给任务所有者看：提示词和
// Offering 声明为公开的普通参数。资产/任务引用、隐藏面参数、遮罩、来源 URL 和
// 任何 Offering 未声明的键都不输出；运行配置与密钥从不在任务输入里。
type taskInputResponse struct {
	Prompt     string                     `json:"prompt,omitempty"`
	Parameters map[string]json.RawMessage `json:"parameters,omitempty"`
}

// referenceParameterTypes 引用平台资产或任务，值是内部 ID 或地址。
var referenceParameterTypes = map[string]bool{"asset": true, "asset-array": true, "task": true}

var withheldSemantics = map[schema.ParameterSemantic]bool{
	schema.SemanticMask:           true,
	schema.SemanticReferenceImage: true,
	schema.SemanticSourceTask:     true,
	schema.SemanticSourceAsset:    true,
	schema.SemanticSourceURL:      true,
	schema.SemanticAction:         true,
}

func publicTaskInput(task generation.Task) *taskInputResponse {
	if len(task.Input) == 0 {
		return nil
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(task.Input, &input); err != nil || len(input) == 0 {
		return nil
	}
	result := &taskInputResponse{}
	consent := extraCreditConsentParameter(task.Offering)
	for _, parameter := range task.Offering.Parameters {
		value, present := input[parameter.Name]
		if !present || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			continue
		}
		if parameter.Semantic == schema.SemanticPrompt {
			var text string
			if json.Unmarshal(value, &text) == nil && result.Prompt == "" {
				result.Prompt = text
			}
			continue
		}
		if consent != "" && parameter.Name == consent {
			continue // an approval switch, not a request setting; it would also expose the cost policy
		}
		if referenceParameterTypes[parameter.Type] || withheldSemantics[parameter.Semantic] {
			continue
		}
		// A remote address (e.g. texture_image_url) may carry credentials or private hosts, whatever its
		// semantic says; the semantic cannot be source-url there because that would block freezing the input.
		if strings.HasSuffix(parameter.Name, "_url") {
			continue
		}
		if parameter.Presentation != nil && parameter.Presentation.Surface == schema.SurfaceHidden {
			continue
		}
		if result.Parameters == nil {
			result.Parameters = map[string]json.RawMessage{}
		}
		result.Parameters[parameter.Name] = value
	}
	if result.Prompt == "" && len(result.Parameters) == 0 {
		return nil
	}
	return result
}

// publicCreditsCharged projects the Offering's declared estimate for the frozen input
// (base price, unit multiplier and extra credits), not the Provider's settled charge.
// Only a SUCCEEDED credits-metered task reports an amount, including an explicit zero.
// Nil means "not applicable" and the UI shows the Schema's mark.
func publicCreditsCharged(task generation.Task) *int {
	if task.NativeQuote != nil || task.Offering.NativePricing != nil || task.State != schema.TaskSucceeded || !task.Offering.CreditsMetered {
		return nil
	}
	credits := task.Offering.EstimateTotalCredits(task.Input)
	if credits < 0 {
		return nil
	}
	return &credits
}
