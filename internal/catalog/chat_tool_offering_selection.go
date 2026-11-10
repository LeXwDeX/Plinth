package catalog

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// sourceTaskFromImageHandle is the argument mapping that resolves an image
// handle or a public Task ID to the owner's Task (see the generation tool).
const sourceTaskFromImageHandle = "source-task-from-image-handle"

// validateChatToolOfferingSelection checks the two argument-driven ways a
// generation tool picks its Offering (#189). Both are independent of the
// workspace model, name only declared Offerings of the tool's capabilities,
// and leave the final enabled/available check to execution time. When the
// Catalog declares Offering knowledge (#249), a ModelArgument tool must also
// derive its enum from the Catalog instead of writing one.
func validateChatToolOfferingSelection(schema ChatSchema, offerings generationMap) error {
	tools := schema.Tools
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
	return validateChatToolModelSelection(tools, offerings,
		len(schema.Agent.OfferingKnowledge.RequiredCapabilities) > 0)
}

// validateChatToolModelSelection checks the optional model argument of a tool
// whose Agent may choose the 3D model (#103). The argument is a declared
// string; every declared enum value names at most one Offering per capability
// by model_id or Offering ID, and at least one Offering overall. Execution
// picks within the call's capability, so a name shared by the text and image
// Offering of one model is unambiguous; two matches in the same capability are
// not. The Agent can therefore only name Offerings the tool can run.
func validateChatToolModelSelection(tools []ChatToolSchema, offerings generationMap, knowledgeDeclared bool) error {
	for index, tool := range tools {
		if tool.ModelArgument == "" {
			if tool.ModelChoices != nil {
				return fmt.Errorf("chat.tools[%d] 声明了 model_choices 却没有 model_argument", index)
			}
			continue
		}
		if tool.OfferingArgument != "" || tool.OfferingFromSource != "" {
			return fmt.Errorf("chat.tools[%d] model_argument 与 offering_argument/offering_from_source 不能同时声明", index)
		}
		var parameters struct {
			Properties map[string]struct {
				Type string   `json:"type"`
				Enum []string `json:"enum"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
			return fmt.Errorf("chat.tools[%d] parameters 无效: %w", index, err)
		}
		property, exists := parameters.Properties[tool.ModelArgument]
		if !exists || property.Type != "string" {
			return fmt.Errorf("chat.tools[%d] model_argument %q 必须是 string 参数", index, tool.ModelArgument)
		}
		if _, mapped := tool.ArgumentMappings[tool.ModelArgument]; mapped {
			return fmt.Errorf("chat.tools[%d] model_argument %q 不是 Offering 参数，不能映射", index, tool.ModelArgument)
		}
		if tool.ModelChoices == nil {
			if knowledgeDeclared {
				return fmt.Errorf("chat.tools[%d] 新式 Catalog 的 model_argument 必须声明 model_choices 而不是手写 enum", index)
			}
			return validateLegacyModelEnum(index, tool, property.Enum, offerings)
		}
		if len(property.Enum) > 0 {
			return fmt.Errorf("chat.tools[%d] model_argument %q 声明了 model_choices 时参数不得再写 enum", index, tool.ModelArgument)
		}
		if tool.ModelChoices.From != "available_offerings" {
			return fmt.Errorf("chat.tools[%d] model_choices.from 必须是 available_offerings: %q", index, tool.ModelChoices.From)
		}
		seen := map[string]bool{}
		for _, semantic := range tool.ModelChoices.RequireParameters {
			if !semantic.Valid() {
				return fmt.Errorf("chat.tools[%d] model_choices.require_parameters 含未知语义 %q", index, semantic)
			}
			if seen[string(semantic)] {
				return fmt.Errorf("chat.tools[%d] model_choices.require_parameters 重复: %q", index, semantic)
			}
			seen[string(semantic)] = true
		}
		for _, semantic := range tool.ModelChoices.ExcludeParameters {
			if !semantic.Valid() {
				return fmt.Errorf("chat.tools[%d] model_choices.exclude_parameters 含未知语义 %q", index, semantic)
			}
			if seen[string(semantic)] {
				return fmt.Errorf("chat.tools[%d] model_choices 的语义 %q 不能同时出现在 require 与 exclude", index, semantic)
			}
			seen[string(semantic)] = true
		}
		for _, id := range tool.ModelChoices.ExcludeOfferings {
			if _, exists := offerings[schemacontract.OfferingID(id)]; !exists {
				return fmt.Errorf("chat.tools[%d] model_choices.exclude_offerings 的 Offering %q 不存在", index, id)
			}
		}
		if len(DerivedModelChoices(tool, offeringsList(offerings), nil)) == 0 {
			return fmt.Errorf("chat.tools[%d] model_choices 没有任何已启用且匹配的 Offering", index)
		}
	}
	return nil
}

// validateLegacyModelEnum keeps the historical handwritten enum valid for a
// Catalog without Offering knowledge: every value names at most one Offering
// per capability and at least one overall.
func validateLegacyModelEnum(index int, tool ChatToolSchema, values []string, offerings generationMap) error {
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return fmt.Errorf("chat.tools[%d] model_argument 选项重复: %q", index, value)
		}
		seen[value] = true
		matched := false
		for _, capability := range tool.Execution.Capabilities {
			matches := 0
			for _, offering := range offerings {
				if offering.Capability != capability {
					continue
				}
				if offering.ModelID == value || string(offering.ID) == value {
					matches++
				}
			}
			if matches > 1 {
				return fmt.Errorf("chat.tools[%d] model_argument 选项 %q 在能力 %q 内对应 %d 个 Offering", index, value, capability, matches)
			}
			if matches == 1 {
				matched = true
			}
		}
		if !matched {
			return fmt.Errorf("chat.tools[%d] model_argument 选项 %q 没有对应的 Offering", index, value)
		}
	}
	return nil
}

// DerivedModelChoices is the runtime enum of a ModelArgument tool (#249):
// the model_ids of the Offerings that are enabled, available when a check is
// given, serve one of the tool's capabilities, and declare every required
// parameter semantic while declaring none excluded. Order follows the given
// slice (the Catalog order), and duplicates are removed. A tool without
// ModelChoices derives nothing; callers keep its declared enum.
func DerivedModelChoices(
	tool ChatToolSchema,
	offerings []schemacontract.Offering,
	available func(schemacontract.OfferingID) bool,
) []string {
	if tool.ModelChoices == nil {
		return nil
	}
	choices := make([]string, 0, len(offerings))
	seen := map[string]bool{}
	for _, offering := range offerings {
		if !offering.Enabled || offering.ModelID == "" {
			continue
		}
		if available != nil && !available(offering.ID) {
			continue
		}
		if !slices.Contains(tool.Execution.Capabilities, offering.Capability) {
			continue
		}
		if slices.Contains(tool.ModelChoices.ExcludeOfferings, string(offering.ID)) {
			continue
		}
		if !offeringMatchesModelChoices(offering, *tool.ModelChoices) {
			continue
		}
		if seen[offering.ModelID] {
			continue
		}
		seen[offering.ModelID] = true
		choices = append(choices, offering.ModelID)
	}
	return choices
}

func offeringMatchesModelChoices(offering schemacontract.Offering, choices ChatToolModelChoicesSchema) bool {
	declared := make(map[schemacontract.ParameterSemantic]bool, len(offering.Parameters))
	for _, parameter := range offering.Parameters {
		declared[parameter.Semantic] = true
	}
	for _, semantic := range choices.RequireParameters {
		if !declared[semantic] {
			return false
		}
	}
	for _, semantic := range choices.ExcludeParameters {
		if declared[semantic] {
			return false
		}
	}
	if choices.RequireMultiview && !offeringTakesSeveralViews(offering) {
		return false
	}
	return true
}

// offeringTakesSeveralViews reports whether an Offering takes several labelled
// views of one object (#243): it declares the reference-views semantic, or its
// provider endpoint is the order-only multi-image contract, where the pictures
// are views by position. A plain multi-reference Offering (several pictures of
// one object with no view keys) is not a multiview step.
func offeringTakesSeveralViews(offering schemacontract.Offering) bool {
	for _, parameter := range offering.Parameters {
		if parameter.Semantic == schemacontract.SemanticReferenceViews {
			return true
		}
	}
	var options struct {
		Endpoint string `json:"endpoint"`
	}
	if len(offering.ProviderOptions) > 0 && json.Unmarshal(offering.ProviderOptions, &options) == nil {
		return options.Endpoint == "multi-image-to-3d"
	}
	return false
}

func offeringsList(offerings generationMap) []schemacontract.Offering {
	list := make([]schemacontract.Offering, 0, len(offerings))
	ids := make([]string, 0, len(offerings))
	for id := range offerings {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, id := range ids {
		list = append(list, offerings[schemacontract.OfferingID(id)])
	}
	return list
}
