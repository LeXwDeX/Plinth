package catalog

// Provider discovery and legacy model projections.
import (
	"errors"
	"fmt"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
	"slices"
	"strings"
)

// Asset-backed Task dependencies must publish their source restrictions to
// discovery consumers as well as the native Product Schema.
func validateTaskSourceDiscovery(discovery ProviderDiscoverySchema, offerings generationMap) error {
	for _, offering := range offerings {
		for _, parameter := range offering.Parameters {
			if parameter.SameSession && strings.TrimSpace(discovery.API.InputFields.SameSession) == "" {
				return errors.New("same_session discovery field required")
			}
			if parameter.TaskSourceBinding != "" && strings.TrimSpace(discovery.API.InputFields.TaskSourceBinding) == "" {
				return errors.New("task_source_binding discovery field required")
			}
			if parameter.ImageInput != nil && strings.TrimSpace(discovery.API.InputFields.ImageInput) == "" {
				return errors.New("application.provider_discovery.api.input_fields.image_input: 来源图片需要公开尺寸约束")
			}
		}
	}
	for _, offering := range offerings {
		for _, parameter := range offering.Parameters {
			if parameter.TaskSourceKind != schemacontract.TaskSourceAssets {
				continue
			}
			fields := discovery.API.InputFields
			for _, field := range []struct{ name, value string }{
				{"task_source_kind", fields.TaskSourceKind},
				{"task_offerings", fields.TaskOfferings},
				{"task_capabilities", fields.TaskCapabilities},
				{"same_provider", fields.SameProvider},
			} {
				if strings.TrimSpace(field.value) == "" {
					return fmt.Errorf("application.provider_discovery.api.input_fields.%s: assets Task 来源需要公开该约束", field.name)
				}
			}
			return nil
		}
	}
	return nil
}

func validateProviderDiscoveryLegacyModels(
	configured []ProviderDiscoveryLegacyModel,
	offerings generationMap,
) error {
	available := make(map[string]map[schemacontract.Capability][]string)
	configuredModels := make(map[string]ProviderDiscoveryLegacyModel, len(configured))
	for _, model := range configured {
		configuredModels[model.ModelID] = model
	}
	for _, offering := range offerings {
		configuredModel, configured := configuredModels[offering.ModelID]
		if !offering.Enabled || (offering.NativePricing != nil && (!configured || !configuredModel.NativePricing)) {
			continue
		}
		capabilities := available[offering.ModelID]
		if capabilities == nil {
			capabilities = map[schemacontract.Capability][]string{}
			available[offering.ModelID] = capabilities
		}
		var parameters []string
		for _, parameter := range offering.Parameters {
			if parameter.Presentation == nil ||
				parameter.Presentation.Surface == schemacontract.SurfaceTask {
				continue
			}
			parameters = append(parameters, parameter.Name)
		}
		capabilities[offering.Capability] = parameters
	}
	if len(configured) != len(available) {
		return fmt.Errorf(
			"legacy_models 数量为 %d，启用模型数量为 %d",
			len(configured),
			len(available),
		)
	}
	for index, model := range configured {
		if _, exists := available[model.ModelID]; !exists {
			return fmt.Errorf(
				"legacy_models[%d].model_id 未引用启用 Offering: %q",
				index,
				model.ModelID,
			)
		}
		capabilities := available[model.ModelID]
		for capability, projection := range model.ParameterProjections {
			parameters, exists := capabilities[capability]
			if !exists {
				return fmt.Errorf(
					"legacy_models[%d].parameter_projections 引用未知能力: %q",
					index,
					capability,
				)
			}
			if err := validateLegacyParameterProjectionTargets(
				projection,
				parameters,
			); err != nil {
				return fmt.Errorf(
					"legacy_models[%d].parameter_projections.%s: %w",
					index,
					capability,
					err,
				)
			}
		}
		for capability := range model.ConstraintProjections {
			if _, exists := capabilities[capability]; !exists {
				return fmt.Errorf(
					"legacy_models[%d].constraint_projections 引用未知能力: %q",
					index,
					capability,
				)
			}
		}
	}
	return nil
}

func validateLegacyParameterProjectionTargets(
	projection ProviderDiscoveryLegacyParameterProjection,
	parameters []string,
) error {
	available := make(map[string]struct{}, len(parameters))
	for _, parameter := range parameters {
		available[parameter] = struct{}{}
	}
	omitted := make(map[string]struct{}, len(projection.Omit))
	for _, parameter := range projection.Omit {
		if _, exists := available[parameter]; !exists {
			return fmt.Errorf("omit 引用未知参数: %q", parameter)
		}
		omitted[parameter] = struct{}{}
	}
	for parameter := range projection.Fields {
		if _, exists := available[parameter]; !exists {
			return fmt.Errorf("fields 引用未知参数: %q", parameter)
		}
	}
	if len(projection.Order) == 0 {
		return nil
	}
	expected := len(parameters) - len(omitted)
	if len(projection.Order) != expected {
		return fmt.Errorf(
			"order 数量为 %d，投影后参数数量为 %d",
			len(projection.Order),
			expected,
		)
	}
	seen := make(map[string]struct{}, len(projection.Order))
	for _, parameter := range projection.Order {
		if _, exists := available[parameter]; !exists {
			return fmt.Errorf("order 引用未知参数: %q", parameter)
		}
		if _, exists := omitted[parameter]; exists {
			return fmt.Errorf("order 引用了已省略参数: %q", parameter)
		}
		if _, exists := seen[parameter]; exists {
			return fmt.Errorf("order 参数重复: %q", parameter)
		}
		seen[parameter] = struct{}{}
	}
	return nil
}

func validateProviderDiscovery(schema ProviderDiscoverySchema) error {
	groups := map[string]map[string]string{
		"api.query": {
			"capability": schema.API.Query.Capability,
			"domain":     schema.API.Query.Domain,
			"projection": schema.API.Query.Projection,
		},
		"api.envelope_fields": {
			"providers":     schema.API.EnvelopeFields.Providers,
			"default_model": schema.API.EnvelopeFields.DefaultModel,
		},
		"api.provider_fields": {
			"id":                schema.API.ProviderFields.ID,
			"name":              schema.API.ProviderFields.Name,
			"capabilities":      schema.API.ProviderFields.Capabilities,
			"domain":            schema.API.ProviderFields.Domain,
			"features":          schema.API.ProviderFields.Features,
			"parameter_schemas": schema.API.ProviderFields.ParameterSchemas,
			"input_schemas":     schema.API.ProviderFields.InputSchemas,
			"constraints":       schema.API.ProviderFields.Constraints,
			"recommended":       schema.API.ProviderFields.Recommended,
			"estimated_credits": schema.API.ProviderFields.EstimatedCredits,
			"provider_key":      schema.API.ProviderFields.ProviderKey,
		},
		"api.parameter_fields": {
			"name":          schema.API.ParameterFields.Name,
			"label":         schema.API.ParameterFields.Label,
			"type":          schema.API.ParameterFields.Type,
			"options":       schema.API.ParameterFields.Options,
			"default":       schema.API.ParameterFields.Default,
			"required":      schema.API.ParameterFields.Required,
			"minimum":       schema.API.ParameterFields.Minimum,
			"maximum":       schema.API.ParameterFields.Maximum,
			"step":          schema.API.ParameterFields.Step,
			"description":   schema.API.ParameterFields.Description,
			"disabled_when": schema.API.ParameterFields.DisabledWhen,
			"visible_when":  schema.API.ParameterFields.VisibleWhen,
			"forced_when":   schema.API.ParameterFields.ForcedWhen,
			"notices":       schema.API.ParameterFields.Notices,
		},
		"api.option_fields": {
			"value": schema.API.OptionFields.Value,
			"label": schema.API.OptionFields.Label,
			"group": schema.API.OptionFields.Group,
		},
		"api.condition_fields": {
			"parameter": schema.API.ConditionFields.Parameter,
			"operator":  schema.API.ConditionFields.Operator,
			"value":     schema.API.ConditionFields.Value,
		},
		"api.notice_fields": {
			"when":        schema.API.NoticeFields.When,
			"message_key": schema.API.NoticeFields.MessageKey,
			"tone":        schema.API.NoticeFields.Tone,
		},
		"api.forced_value_fields": {
			"when":  schema.API.ForcedValueFields.When,
			"value": schema.API.ForcedValueFields.Value,
		},
		"api.input_fields": {
			"name":        schema.API.InputFields.Name,
			"label":       schema.API.InputFields.Label,
			"type":        schema.API.InputFields.Type,
			"media_types": schema.API.InputFields.MediaTypes,
			"extensions":  schema.API.InputFields.Extensions,
			"max_bytes":   schema.API.InputFields.MaxBytes,
			"min_items":   schema.API.InputFields.MinItems,
			"max_items":   schema.API.InputFields.MaxItems,
		},
		"api.constraint_fields": {
			"prompt_max_length": schema.API.ConstraintFields.
				PromptMaxLength,
			"negative_prompt_max_length": schema.API.ConstraintFields.
				NegativePromptMaxLength,
			"reference_image_min": schema.API.ConstraintFields.
				ReferenceImageMin,
			"reference_image_max": schema.API.ConstraintFields.
				ReferenceImageMax,
			"reference_image_formats": schema.API.ConstraintFields.
				ReferenceImageFormats,
			"reference_image_max_size": schema.API.ConstraintFields.
				ReferenceImageMaxSize,
			"polycount_min": schema.API.ConstraintFields.PolycountMin,
			"polycount_max": schema.API.ConstraintFields.PolycountMax,
			"output_sizes":  schema.API.ConstraintFields.OutputSizes,
		},
	}
	for name, value := range map[string]string{
		"same_session":        schema.API.InputFields.SameSession,
		"task_source_binding": schema.API.InputFields.TaskSourceBinding,
		"image_input":         schema.API.InputFields.ImageInput,
		"task_source_kind":    schema.API.InputFields.TaskSourceKind,
		"task_offerings":      schema.API.InputFields.TaskOfferings,
		"task_capabilities":   schema.API.InputFields.TaskCapabilities,
		"same_provider":       schema.API.InputFields.SameProvider,
	} {
		if value != "" {
			groups["api.input_fields"][name] = value
		}
	}
	for scope, fields := range groups {
		if err := validateUniqueWireNames(scope, fields); err != nil {
			return err
		}
	}
	// 可选派生线名（W5 安全成本估算）：声明时才校验（非空、字符合法、
	// 不与既有 provider/parameter 线名重复）。
	optionalWireNames := []struct {
		scope string
		value string
		taken map[string]string
	}{
		{
			"api.provider_fields.native_quotes",
			strings.TrimSpace(schema.API.ProviderFields.NativeQuotes),
			groups["api.provider_fields"],
		},
		{
			"api.provider_fields.estimated_extra_credits",
			strings.TrimSpace(schema.API.ProviderFields.EstimatedExtraCredits),
			groups["api.provider_fields"],
		},
		{
			"api.parameter_fields.cost_extra",
			strings.TrimSpace(schema.API.ParameterFields.CostExtra),
			groups["api.parameter_fields"],
		},
	}
	for _, optional := range optionalWireNames {
		if optional.value == "" {
			continue
		}
		if strings.ContainsAny(optional.value, " ?&=/#{}") {
			return fmt.Errorf("%s 无效", optional.scope)
		}
		if previous, exists := optional.taken[optional.value]; exists {
			return fmt.Errorf(
				"%s 与既有线名 %s 重复为 %q",
				optional.scope,
				previous,
				optional.value,
			)
		}
	}
	if extended := strings.TrimSpace(
		schema.API.ProjectionValues.Extended,
	); extended == "" || strings.ContainsAny(extended, " ?&=/#") {
		return errors.New("api.projection_values.extended 无效")
	}
	if len(schema.LegacyModels) == 0 {
		return errors.New("legacy_models 不能为空")
	}
	modelIDs := make(map[string]struct{}, len(schema.LegacyModels))
	for index, model := range schema.LegacyModels {
		if model.NativePricing && strings.TrimSpace(schema.API.ProviderFields.NativeQuotes) == "" {
			return fmt.Errorf("legacy_models[%d] native pricing requires api.provider_fields.native_quotes", index)
		}
		modelID := strings.TrimSpace(model.ModelID)
		providerKey := strings.TrimSpace(model.ProviderKey)
		if modelID == "" || providerKey == "" {
			return fmt.Errorf("legacy_models[%d] 字段不能为空", index)
		}
		if _, exists := modelIDs[modelID]; exists {
			return fmt.Errorf("legacy_models[%d].model_id 重复: %q", index, modelID)
		}
		if err := validateProviderDiscoveryLegacyModel(model); err != nil {
			return fmt.Errorf("legacy_models[%d]: %w", index, err)
		}
		modelIDs[modelID] = struct{}{}
	}
	for modelID, labels := range schema.LegacyConstraintOptionLabels {
		if _, exists := modelIDs[modelID]; !exists {
			return fmt.Errorf(
				"legacy_constraint_option_labels 引用未知模型: %q",
				modelID,
			)
		}
		if len(labels) == 0 {
			return fmt.Errorf(
				"legacy_constraint_option_labels.%s 不能为空",
				modelID,
			)
		}
		for value, label := range labels {
			if strings.TrimSpace(value) == "" || strings.TrimSpace(label) == "" {
				return fmt.Errorf(
					"legacy_constraint_option_labels.%s 含空值或标签",
					modelID,
				)
			}
		}
	}
	return nil
}

func validateProviderDiscoveryLegacyModel(
	model ProviderDiscoveryLegacyModel,
) error {
	parameterFields := map[string]struct{}{
		"options":     {},
		"default":     {},
		"required":    {},
		"minimum":     {},
		"maximum":     {},
		"step":        {},
		"description": {},
	}
	for capability, projection := range model.ParameterProjections {
		if strings.TrimSpace(string(capability)) == "" {
			return errors.New("parameter_projections 能力不能为空")
		}
		if err := validateUniqueSchemaValues(
			"parameter_projections."+string(capability)+".order",
			projection.Order,
			nil,
		); err != nil {
			return err
		}
		if err := validateUniqueSchemaValues(
			"parameter_projections."+string(capability)+".omit",
			projection.Omit,
			nil,
		); err != nil {
			return err
		}
		for parameter, fields := range projection.Fields {
			if strings.TrimSpace(parameter) == "" {
				return fmt.Errorf(
					"parameter_projections.%s.fields 参数名不能为空",
					capability,
				)
			}
			if err := validateUniqueSchemaValues(
				"parameter_projections."+string(capability)+
					".fields."+parameter+".omit",
				fields.Omit,
				parameterFields,
			); err != nil {
				return err
			}
			if fields.Required != nil &&
				slices.Contains(fields.Omit, "required") {
				return fmt.Errorf(
					"parameter_projections.%s.fields.%s 同时覆盖并省略 required",
					capability,
					parameter,
				)
			}
		}
	}
	constraintFields := map[string]struct{}{
		"prompt_max_length":          {},
		"negative_prompt_max_length": {},
		"reference_image_min":        {},
		"reference_image_max":        {},
		"reference_image_formats":    {},
		"reference_image_max_size":   {},
		"polycount_min":              {},
		"polycount_max":              {},
		"output_sizes":               {},
	}
	for capability, projection := range model.ConstraintProjections {
		if strings.TrimSpace(string(capability)) == "" {
			return errors.New("constraint_projections 能力不能为空")
		}
		if err := validateUniqueSchemaValues(
			"constraint_projections."+string(capability)+".omit",
			projection.Omit,
			constraintFields,
		); err != nil {
			return err
		}
		if err := validateLegacyConstraintOverrides(
			capability,
			projection.Overrides,
		); err != nil {
			return err
		}
	}
	return nil
}
