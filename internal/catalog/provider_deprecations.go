package catalog

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// Provider deprecation baseline (#329). Each entry records what a Provider's
// current documentation has deprecated or retired; an Offering of that
// Provider may not send any of it, so a later edit cannot quietly bring an
// old model or field back. The check reads the generic wire declarations an
// Offering carries: parameter wire names and values, and the provider_options
// keys endpoint, model_parameter, model_parameter_value, request_template,
// refine_after_preview.set and input_paths.

func cloneProviderDeprecations(source []ProviderDeprecationSchema) []ProviderDeprecationSchema {
	if source == nil {
		return nil
	}
	result := make([]ProviderDeprecationSchema, len(source))
	for index, entry := range source {
		entry.Sources = append([]string(nil), entry.Sources...)
		entry.ModelFields = append([]string(nil), entry.ModelFields...)
		entry.HistoryOfferings = append([]schemacontract.OfferingID(nil), entry.HistoryOfferings...)
		entry.Models = append([]DeprecatedWireValue(nil), entry.Models...)
		entry.Parameters = append([]DeprecatedWireField(nil), entry.Parameters...)
		entry.Values = append([]DeprecatedWireFieldValue(nil), entry.Values...)
		entry.Exemptions = append([]DeprecationExemption(nil), entry.Exemptions...)
		for i := range entry.Models {
			entry.Models[i].Endpoints = append([]string(nil), entry.Models[i].Endpoints...)
		}
		for i := range entry.Parameters {
			entry.Parameters[i].Endpoints = append([]string(nil), entry.Parameters[i].Endpoints...)
		}
		for i := range entry.Values {
			entry.Values[i].Endpoints = append([]string(nil), entry.Values[i].Endpoints...)
		}
		result[index] = entry
	}
	return result
}

// offeringWire is what one Offering can put on the wire.
type offeringWire struct {
	endpoint string
	models   map[string]struct{}
	fields   map[string]struct{}
	values   map[string]map[string]struct{}
}

func (wire *offeringWire) addValue(field string, raw any) {
	text, ok := raw.(string)
	if !ok {
		return
	}
	if wire.values[field] == nil {
		wire.values[field] = map[string]struct{}{}
	}
	wire.values[field][text] = struct{}{}
}

func collectOfferingWire(offering schemacontract.Offering, modelFields []string) (offeringWire, error) {
	wire := offeringWire{models: map[string]struct{}{}, fields: map[string]struct{}{}, values: map[string]map[string]struct{}{}}
	var options struct {
		Endpoint            string                        `json:"endpoint"`
		ModelParameter      string                        `json:"model_parameter"`
		ModelParameterValue string                        `json:"model_parameter_value"`
		RequestTemplate     map[string]any                `json:"request_template"`
		InputPaths          map[string][]string           `json:"input_paths"`
		RefineAfterPreview  *struct{ Set map[string]any } `json:"refine_after_preview"`
	}
	if len(offering.ProviderOptions) > 0 {
		if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
			return wire, fmt.Errorf("provider_options: %w", err)
		}
	}
	wire.endpoint = strings.TrimSpace(options.Endpoint)
	for _, parameter := range offering.Parameters {
		name := strings.TrimSpace(parameter.WireName)
		if name == "" {
			name = parameter.Name
		}
		wire.fields[name] = struct{}{}
		var value any
		if len(parameter.Default) > 0 && json.Unmarshal(parameter.Default, &value) == nil {
			wire.addValue(name, value)
		}
		for _, option := range parameter.Options {
			wire.addValue(name, option.Value)
		}
		for _, mapped := range parameter.WireValues {
			var mappedValue any
			if json.Unmarshal(mapped, &mappedValue) == nil {
				wire.addValue(name, mappedValue)
			}
		}
	}
	for _, path := range options.InputPaths {
		for _, segment := range path {
			wire.fields[segment] = struct{}{}
		}
	}
	fixed := map[string]any{}
	for key, value := range options.RequestTemplate {
		fixed[key] = value
	}
	if options.RefineAfterPreview != nil {
		for key, value := range options.RefineAfterPreview.Set {
			fixed[key] = value
		}
	}
	for key, value := range fixed {
		wire.fields[key] = struct{}{}
		if text, ok := value.(string); ok && text == "$model" {
			value = offering.ModelID
		} else if ok && strings.HasPrefix(text, "$") {
			continue
		}
		wire.addValue(key, value)
	}
	if parameter := strings.TrimSpace(options.ModelParameter); parameter != "" {
		value := strings.TrimSpace(options.ModelParameterValue)
		if value == "" {
			value = offering.ModelID
		}
		wire.fields[parameter] = struct{}{}
		wire.addValue(parameter, value)
	}
	for _, field := range modelFields {
		for value := range wire.values[field] {
			wire.models[value] = struct{}{}
		}
	}
	return wire, nil
}

func endpointMatches(endpoints []string, endpoint string) bool {
	return len(endpoints) == 0 || slices.Contains(endpoints, endpoint)
}

func validateProviderDeprecations(entries []ProviderDeprecationSchema, offerings map[schemacontract.OfferingID]schemacontract.Offering) error {
	seenProviders := map[string]struct{}{}
	for index, entry := range entries {
		prefix := fmt.Sprintf("application.provider_deprecations[%d]", index)
		provider := strings.TrimSpace(entry.ProviderID)
		if provider == "" {
			return fmt.Errorf("%s 缺少 provider_id", prefix)
		}
		if _, duplicate := seenProviders[provider]; duplicate {
			return fmt.Errorf("%s provider_id %q 重复", prefix, provider)
		}
		seenProviders[provider] = struct{}{}
		if _, err := time.Parse("2006-01-02", entry.VerifiedAt); err != nil {
			return fmt.Errorf("%s verified_at 必须是 YYYY-MM-DD", prefix)
		}
		if len(entry.Sources) == 0 {
			return fmt.Errorf("%s 至少需要一个官方来源", prefix)
		}
		for _, source := range entry.Sources {
			parsed, err := url.Parse(source)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
				return fmt.Errorf("%s 来源 %q 不是 https 链接", prefix, source)
			}
		}
		if len(entry.ModelFields) == 0 {
			return fmt.Errorf("%s 缺少 model_fields", prefix)
		}
		for _, model := range entry.Models {
			if strings.TrimSpace(model.Value) == "" || strings.TrimSpace(model.Reason) == "" {
				return fmt.Errorf("%s models 每项需要 value 与 reason", prefix)
			}
		}
		for _, field := range entry.Parameters {
			if strings.TrimSpace(field.Name) == "" || strings.TrimSpace(field.Reason) == "" {
				return fmt.Errorf("%s parameters 每项需要 name 与 reason", prefix)
			}
		}
		for _, value := range entry.Values {
			if strings.TrimSpace(value.Field) == "" || strings.TrimSpace(value.Value) == "" || strings.TrimSpace(value.Reason) == "" {
				return fmt.Errorf("%s values 每项需要 field、value 与 reason", prefix)
			}
		}
		history := map[schemacontract.OfferingID]struct{}{}
		for _, id := range entry.HistoryOfferings {
			offering, exists := offerings[id]
			if !exists || offering.ProviderID != provider {
				return fmt.Errorf("%s history_offerings 引用了不存在或不属于 %s 的 Offering %q", prefix, provider, id)
			}
			if offering.Enabled {
				return fmt.Errorf("%s history_offerings 中的 %q 只保留历史身份，不能启用", prefix, id)
			}
			history[id] = struct{}{}
		}
		exempt := map[schemacontract.OfferingID]map[string]struct{}{}
		for _, exemption := range entry.Exemptions {
			offering, exists := offerings[exemption.OfferingID]
			listed := slices.ContainsFunc(entry.Parameters, func(field DeprecatedWireField) bool { return field.Name == exemption.Field })
			if !exists || offering.ProviderID != provider || !listed || !strings.HasPrefix(exemption.Issue, "#") || strings.TrimSpace(exemption.Reason) == "" {
				return fmt.Errorf("%s exemptions 须引用 %s 的 Offering、parameters 中列出的字段、跟踪 Issue（#N）与理由", prefix, provider)
			}
			if exempt[exemption.OfferingID] == nil {
				exempt[exemption.OfferingID] = map[string]struct{}{}
			}
			exempt[exemption.OfferingID][exemption.Field] = struct{}{}
		}
		ids := make([]schemacontract.OfferingID, 0, len(offerings))
		for id, offering := range offerings {
			if offering.ProviderID == provider {
				ids = append(ids, id)
			}
		}
		slices.Sort(ids)
		for _, id := range ids {
			if _, exempt := history[id]; exempt {
				continue
			}
			offering := offerings[id]
			wire, err := collectOfferingWire(offering, entry.ModelFields)
			if err != nil {
				return fmt.Errorf("%s: Offering %q %w", prefix, id, err)
			}
			for _, model := range entry.Models {
				if _, sends := wire.models[model.Value]; sends && endpointMatches(model.Endpoints, wire.endpoint) {
					return fmt.Errorf("%s: Offering %q 发送已弃用的模型 %q（%s）", prefix, id, model.Value, model.Reason)
				}
			}
			for _, field := range entry.Parameters {
				if _, allowed := exempt[id][field.Name]; allowed {
					continue
				}
				if _, sends := wire.fields[field.Name]; sends && endpointMatches(field.Endpoints, wire.endpoint) {
					return fmt.Errorf("%s: Offering %q 发送已弃用的字段 %q（%s）", prefix, id, field.Name, field.Reason)
				}
			}
			for _, value := range entry.Values {
				if _, sends := wire.values[value.Field][value.Value]; sends && endpointMatches(value.Endpoints, wire.endpoint) {
					return fmt.Errorf("%s: Offering %q 发送已弃用的取值 %s=%q（%s）", prefix, id, value.Field, value.Value, value.Reason)
				}
			}
		}
	}
	return nil
}

// validateRetiredGenerationModels checks runtime.frontend.retired_generation_models
// (#329): the retired model has Offerings but none enabled, and its successor
// is offered by the same Provider for every capability the retired model had.
func validateRetiredGenerationModels(entries []RetiredGenerationModel, offerings map[schemacontract.OfferingID]schemacontract.Offering) error {
	seen := map[string]struct{}{}
	for index, entry := range entries {
		prefix := fmt.Sprintf("application.runtime.frontend.retired_generation_models[%d]", index)
		if entry.ProviderID == "" || entry.ModelID == "" || entry.SuccessorModelID == "" || entry.ModelID == entry.SuccessorModelID {
			return fmt.Errorf("%s 需要 provider_id、model_id 与不同的 successor_model_id", prefix)
		}
		key := entry.ProviderID + "\x00" + entry.ModelID
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("%s 重复登记 %s/%s", prefix, entry.ProviderID, entry.ModelID)
		}
		seen[key] = struct{}{}
		retired := map[schemacontract.Capability]struct{}{}
		successor := map[schemacontract.Capability]struct{}{}
		for _, offering := range offerings {
			if offering.ProviderID != entry.ProviderID {
				continue
			}
			switch offering.ModelID {
			case entry.ModelID:
				if offering.Enabled {
					return fmt.Errorf("%s 的模型 %q 仍有启用的 Offering %q", prefix, entry.ModelID, offering.ID)
				}
				if offering.ModelName != entry.ModelName {
					return fmt.Errorf("%s 的 model_name %q 与 Offering %q 的 %q 不一致", prefix, entry.ModelName, offering.ID, offering.ModelName)
				}
				retired[offering.Capability] = struct{}{}
			case entry.SuccessorModelID:
				if offering.Enabled {
					successor[offering.Capability] = struct{}{}
				}
			}
		}
		if len(retired) == 0 {
			return fmt.Errorf("%s 的模型 %q 没有任何 Offering", prefix, entry.ModelID)
		}
		for capability := range retired {
			if _, offered := successor[capability]; !offered {
				return fmt.Errorf("%s 的后继模型 %q 没有启用的 %s Offering", prefix, entry.SuccessorModelID, capability)
			}
		}
	}
	return nil
}

// RetiredModelSuccessor is one retired generation model and the model that
// replaces it, with both display names (#329 follow-up).
type RetiredModelSuccessor struct {
	ProviderID         string
	ModelID            string
	ModelName          string
	SuccessorModelID   string
	SuccessorModelName string
}

// RetiredModelSuccessors lists runtime.frontend.retired_generation_models
// with the successor's display name. The chat service uses it to move a
// turn's model pick off a retired model; Catalog loading has checked that
// every successor is offered for every capability the retired model had.
func (c *Catalog) RetiredModelSuccessors() []RetiredModelSuccessor {
	var result []RetiredModelSuccessor
	for _, entry := range c.application.Runtime.Frontend.RetiredGenerationModels {
		if name, ok := c.successorName(entry, ""); ok {
			result = append(result, RetiredModelSuccessor{
				ProviderID: entry.ProviderID, ModelID: entry.ModelID, ModelName: entry.ModelName,
				SuccessorModelID: entry.SuccessorModelID, SuccessorModelName: name,
			})
		}
	}
	return result
}

// successorName is the display name of an enabled Offering of the entry's
// successor model (of the capability, when one is given).
func (c *Catalog) successorName(entry RetiredGenerationModel, capability schemacontract.Capability) (string, bool) {
	ids := make([]schemacontract.OfferingID, 0, len(c.byID))
	for id := range c.byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		offering := c.byID[id]
		if offering.Enabled && offering.ProviderID == entry.ProviderID && offering.ModelID == entry.SuccessorModelID &&
			(capability == "" || offering.Capability == capability) {
			name := offering.ModelName
			if name == "" {
				name = offering.ModelID
			}
			return name, true
		}
	}
	return "", false
}

// retiredOfferingRefusal is the refusal of a direct request for a disabled
// Offering whose model is retired: invalid input, with the Schema reason
// naming the successor (#329 follow-up). Nil when the Offering's model is not
// retired or no reason is declared; the request then gets the plain unknown
// Offering answer.
func (c *Catalog) retiredOfferingRefusal(offering schemacontract.Offering) error {
	template := c.application.Runtime.GenerationMessages.RetiredModel
	if template == "" {
		return nil
	}
	for _, entry := range c.application.Runtime.Frontend.RetiredGenerationModels {
		if entry.ProviderID != offering.ProviderID || entry.ModelID != offering.ModelID {
			continue
		}
		successor, ok := c.successorName(entry, offering.Capability)
		if !ok {
			return nil
		}
		values := []string{"{from}", entry.ModelName, "{to}", successor}
		return &schemacontract.Error{
			Code:           schemacontract.ErrInvalidInput,
			Operation:      "select_offering",
			Detail:         strings.NewReplacer(values...).Replace(template),
			UserFacing:     true,
			DetailTemplate: template,
			DetailValues:   values,
		}
	}
	return nil
}

// validateRetiredModelTexts requires the texts the server writes about a
// retired model once any model is retired, each naming both models.
func validateRetiredModelTexts(application ApplicationSchema) error {
	if len(application.Runtime.Frontend.RetiredGenerationModels) == 0 {
		return nil
	}
	for name, template := range map[string]string{
		"runtime.generation_messages.retired_model":         application.Runtime.GenerationMessages.RetiredModel,
		"chat.presentation.messages.retired_model_fallback": application.Chat.Presentation.Messages.RetiredModelFallback,
	} {
		if !strings.Contains(template, "{from}") || !strings.Contains(template, "{to}") {
			return fmt.Errorf("%s 需要含 {from} 与 {to}（声明了 retired_generation_models）", name)
		}
	}
	for index, tool := range application.Chat.Tools {
		if tool.ResultFields.ModelNote != "" && tool.ResultFields.Model == "" {
			return fmt.Errorf("chat.tools[%d] result_fields.model_note 需要同时声明 result_fields.model", index)
		}
	}
	return nil
}
