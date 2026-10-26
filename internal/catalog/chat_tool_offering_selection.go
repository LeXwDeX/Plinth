package catalog

import (
	"encoding/json"
	"fmt"
	"slices"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// sourceTaskFromImageHandle is the argument mapping that resolves an image
// handle or a public Task ID to the owner's Task (see the generation tool).
const sourceTaskFromImageHandle = "source-task-from-image-handle"

// validateChatToolOfferingSelection checks the two argument-driven ways a
// generation tool picks its Offering (#189). Both are independent of the
// workspace model, name only declared Offerings of the tool's capabilities,
// and leave the final enabled/available check to execution time.
func validateChatToolOfferingSelection(tools []ChatToolSchema, offerings generationMap) error {
	for index, tool := range tools {
		if tool.OfferingArgument == "" && tool.OfferingFromSource == "" {
			continue
		}
		if tool.OfferingArgument != "" && tool.OfferingFromSource != "" {
			return fmt.Errorf("chat.tools[%d] offering_argument 与 offering_from_source 不能同时声明", index)
		}
		if !tool.IgnoreUserModel || tool.RequireUserModel || tool.RequireUniqueOffering || tool.CrossDomainModelFallback || tool.Processing != nil {
			return fmt.Errorf("chat.tools[%d] 按参数选择 Offering 需要 ignore_user_model，且不能声明 require_user_model、require_unique_offering、cross_domain_model_fallback 或 processing", index)
		}
		var parameters struct {
			Properties map[string]struct {
				Type string   `json:"type"`
				Enum []string `json:"enum"`
			} `json:"properties"`
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
			return fmt.Errorf("chat.tools[%d] parameters 无效: %w", index, err)
		}
		inTool := func(capability schemacontract.Capability) bool {
			return slices.Contains(tool.Execution.Capabilities, capability)
		}
		if argument := tool.OfferingArgument; argument != "" {
			property, exists := parameters.Properties[argument]
			if !exists || property.Type != "string" || len(property.Enum) == 0 || !slices.Contains(parameters.Required, argument) {
				return fmt.Errorf("chat.tools[%d] offering_argument %q 必须是必填的 string enum 参数", index, argument)
			}
			if _, mapped := tool.ArgumentMappings[argument]; mapped {
				return fmt.Errorf("chat.tools[%d] offering_argument %q 不是 Offering 参数，不能映射", index, argument)
			}
			seen := map[string]bool{}
			for _, value := range property.Enum {
				if seen[value] {
					return fmt.Errorf("chat.tools[%d] offering_argument 选项重复: %q", index, value)
				}
				seen[value] = true
				matches := 0
				for _, offering := range offerings {
					if offering.ModelID == value && inTool(offering.Capability) {
						matches++
					}
				}
				if matches != 1 {
					return fmt.Errorf("chat.tools[%d] offering_argument 选项 %q 必须唯一对应工具能力内的一个 Offering，实际 %d 个", index, value, matches)
				}
			}
			continue
		}
		argument := tool.OfferingFromSource
		if tool.ArgumentMappings[argument] != sourceTaskFromImageHandle || !slices.Contains(parameters.Required, argument) {
			return fmt.Errorf("chat.tools[%d] offering_from_source %q 必须是必填参数并映射为 %s", index, argument, sourceTaskFromImageHandle)
		}
		if _, exists := parameters.Properties[argument]; !exists {
			return fmt.Errorf("chat.tools[%d] offering_from_source %q 未在 parameters 声明", index, argument)
		}
		target := tool.OverrideMappings[sourceTaskFromImageHandle]
		claimed := map[schemacontract.OfferingID]schemacontract.OfferingID{}
		for _, offering := range offerings {
			if !inTool(offering.Capability) {
				continue
			}
			for _, parameter := range offering.Parameters {
				if parameter.Name != target || parameter.Type != "task" || parameter.Semantic != schemacontract.SemanticSourceTask {
					continue
				}
				if len(parameter.TaskOfferings) == 0 {
					return fmt.Errorf("chat.tools[%d] offering_from_source 需要 %s 的来源参数声明 task_offerings", index, offering.ID)
				}
				for _, source := range parameter.TaskOfferings {
					if previous, exists := claimed[source]; exists && previous != offering.ID {
						return fmt.Errorf("chat.tools[%d] 来源 Offering %q 同时对应 %q 与 %q，无法唯一选择", index, source, previous, offering.ID)
					}
					claimed[source] = offering.ID
				}
			}
		}
		if len(claimed) == 0 {
			return fmt.Errorf("chat.tools[%d] offering_from_source 没有可配对的 Offering", index)
		}
	}
	return nil
}
