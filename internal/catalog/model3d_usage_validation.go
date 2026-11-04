package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// model3dUsageShapes are the input shapes a purpose may recommend offerings
// for. multiview-to-3d is a selection class, not a Capability: it names the
// image-to-3d Offerings that declare the multiview views argument.
var model3dUsageShapes = []string{"text-to-3d", "image-to-3d", "multiview-to-3d"}

// validateModel3DUsageShape checks the parts of application.chat.model3d_usage
// that need no Offering: profiles, budgets, pipeline names, brief fields and
// templates, and the generic choice kinds. A Schema without purposes stays
// valid (historical Catalogs).
func validateModel3DUsageShape(schema ChatSchema, capabilityPresentations []CapabilityPresentation) error {
	usage := schema.Model3DUsage
	if len(usage.Profiles) == 0 {
		if strings.TrimSpace(usage.DefaultProfile) != "" || len(usage.Brief.Fields) > 0 {
			return fmt.Errorf("model3d_usage.profiles 不能为空")
		}
		return validateChoiceKinds(schema)
	}
	capabilities := make(map[string]bool, len(capabilityPresentations))
	for _, capability := range capabilityPresentations {
		capabilities[string(capability.ID)] = true
	}
	profiles := make(map[string]bool, len(usage.Profiles))
	for index, profile := range usage.Profiles {
		if strings.TrimSpace(profile.ID) == "" {
			return fmt.Errorf("model3d_usage.profiles[%d].id 不能为空", index)
		}
		if profiles[profile.ID] {
			return fmt.Errorf("model3d_usage.profiles 中 %q 重复", profile.ID)
		}
		profiles[profile.ID] = true
		for name, value := range map[string]string{
			"label":       profile.Label,
			"description": profile.Description,
			"reason":      profile.Reason,
		} {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("model3d_usage.profiles[%d].%s 不能为空", index, name)
			}
		}
		if len([]rune(profile.Reason)) > 4096 {
			return fmt.Errorf("model3d_usage.profiles[%d].reason 不能超过 4096 个字符", index)
		}
		for shape, offerings := range profile.RecommendedOfferings {
			if !containsString(model3dUsageShapes, shape) {
				return fmt.Errorf("model3d_usage.profiles[%d].recommended_offerings 含未知输入形态 %q", index, shape)
			}
			seen := map[string]bool{}
			for _, id := range offerings {
				if strings.TrimSpace(id) == "" {
					return fmt.Errorf("model3d_usage.profiles[%d].recommended_offerings[%q] 含空 Offering ID", index, shape)
				}
				if seen[id] {
					return fmt.Errorf("model3d_usage.profiles[%d].recommended_offerings[%q] 中 %q 重复", index, shape, id)
				}
				seen[id] = true
			}
		}
		if profile.Budget.MaxTriangles <= 0 {
			return fmt.Errorf("model3d_usage.profiles[%d].budget.max_triangles 必须大于 0", index)
		}
		if profile.Budget.MinTriangles != nil && (*profile.Budget.MinTriangles < 0 || *profile.Budget.MinTriangles > profile.Budget.MaxTriangles) {
			return fmt.Errorf("model3d_usage.profiles[%d].budget.min_triangles 必须在 0 与 max_triangles 之间", index)
		}
		if profile.Budget.MaxTextureSize != nil && *profile.Budget.MaxTextureSize <= 0 {
			return fmt.Errorf("model3d_usage.profiles[%d].budget.max_texture_size 必须大于 0", index)
		}
		if scale := profile.ScaleRangeM; scale != nil && (scale.Min <= 0 || scale.Max <= scale.Min) {
			return fmt.Errorf("model3d_usage.profiles[%d].scale_range_m 必须满足 0 < min < max", index)
		}
		for stepIndex, step := range profile.Pipeline {
			if !capabilities[step.Capability] {
				return fmt.Errorf("model3d_usage.profiles[%d].pipeline[%d].capability %q 未声明", index, stepIndex, step.Capability)
			}
		}
	}
	if !profiles[usage.DefaultProfile] {
		return fmt.Errorf("model3d_usage.default_profile %q 不存在", usage.DefaultProfile)
	}
	if err := validateProductionBrief(usage.Brief); err != nil {
		return err
	}
	return validateChoiceKinds(schema)
}

// validateProductionBrief checks the brief field declarations and the words
// the Agent reads in the system context. Fields are validated by their type,
// not by a fixed name list: the service locates the purpose and the triangle
// override by type, so a renamed Schema field needs no code change (#103
// review C3).
func validateProductionBrief(brief ChatProductionBriefSchema) error {
	if len(brief.Fields) == 0 {
		return fmt.Errorf("model3d_usage.brief.fields 不能为空")
	}
	declared := map[string]ChatProductionBriefField{}
	profiles, integers := 0, 0
	for index, field := range brief.Fields {
		if _, exists := declared[field.Name]; exists {
			return fmt.Errorf("model3d_usage.brief.fields 中 %q 重复", field.Name)
		}
		declared[field.Name] = field
		if strings.TrimSpace(field.Label) == "" {
			return fmt.Errorf("model3d_usage.brief.fields[%d].label 不能为空", index)
		}
		switch field.Type {
		case "profile":
			profiles++
		case "boolean":
			// A boolean field has no other declaration.
		case "integer":
			integers++
			if field.Minimum <= 0 || field.Maximum <= 0 || field.Minimum > field.Maximum {
				return fmt.Errorf("model3d_usage.brief.fields[%s] 必须是带有效最小/最大值的 integer", field.Name)
			}
		case "text":
			if field.MaximumCharacters <= 0 {
				return fmt.Errorf("model3d_usage.brief.fields[%s] 必须是带 maximum_characters 的 text", field.Name)
			}
		default:
			return fmt.Errorf("model3d_usage.brief.fields[%s].type %q 未实现", field.Name, field.Type)
		}
	}
	// The service reads exactly one profile (the purpose) and one integer (the
	// triangle override); a second one would be silently ignored.
	if profiles != 1 {
		return fmt.Errorf("model3d_usage.brief.fields 必须声明且只声明一个 profile 字段")
	}
	if integers != 1 {
		return fmt.Errorf("model3d_usage.brief.fields 必须声明且只声明一个 integer 字段")
	}
	templates := map[string]string{
		"context_template":         brief.ContextTemplate,
		"empty_text":               brief.EmptyText,
		"field_template":           brief.FieldTemplate,
		"flag_template":            brief.FlagTemplate,
		"separator":                brief.Separator,
		"purpose_template":         brief.PurposeTemplate,
		"budget_template":          brief.BudgetTemplate,
		"open_budget_template":     brief.OpenBudgetTemplate,
		"override_budget_template": brief.OverrideBudgetTemplate,
		"requirements_template":    brief.RequirementsTemplate,
		"requirement_separator":    brief.RequirementSeparator,
		"uv_text":                  brief.UVText,
		"watertight_text":          brief.WatertightText,
		"skin_text":                brief.SkinText,
		"saved_message":            brief.SavedMessage,
		"invalid_template":         brief.InvalidTemplate,
	}
	for name, value := range templates {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("model3d_usage.brief.%s 不能为空", name)
		}
	}
	placeholders := map[string][]string{
		"context_template":         {"{brief}"},
		"field_template":           {"{label}", "{value}"},
		"flag_template":            {"{label}"},
		"purpose_template":         {"{label}", "{budget}"},
		"budget_template":          {"{minimum}", "{maximum}", "{requirements}"},
		"open_budget_template":     {"{maximum}", "{requirements}"},
		"override_budget_template": {"{value}", "{requirements}"},
		"requirements_template":    {"{items}"},
		"invalid_template":         {"{reason}"},
	}
	for name, required := range placeholders {
		for _, placeholder := range required {
			if !strings.Contains(templates[name], placeholder) {
				return fmt.Errorf("model3d_usage.brief.%s 必须包含 %s", name, placeholder)
			}
		}
	}
	return validateProductionBriefMessages(brief.Messages)
}

// validateProductionBriefMessages checks the refusal wording of the brief
// write. A historical Catalog may declare none of it; a Catalog that declares
// any of it must declare all of it, so no refusal silently falls back to a
// hardcoded sentence in one language (#103 review C3).
func validateProductionBriefMessages(messages ChatProductionBriefMessages) error {
	all := map[string]string{
		"unknown_field":      messages.UnknownField,
		"not_object":         messages.NotObject,
		"not_string":         messages.NotString,
		"not_boolean":        messages.NotBoolean,
		"not_integer":        messages.NotInteger,
		"profile_undeclared": messages.ProfileUndeclared,
		"text_too_long":      messages.TextTooLong,
		"out_of_range":       messages.OutOfRange,
		"unsupported_type":   messages.UnsupportedType,
	}
	any := false
	for _, value := range all {
		if strings.TrimSpace(value) != "" {
			any = true
			break
		}
	}
	if !any {
		return nil
	}
	for name, value := range all {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("model3d_usage.brief.messages.%s 不能为空", name)
		}
	}
	placeholders := map[string][]string{
		"unknown_field":      {"{field}"},
		"not_string":         {"{field}"},
		"not_boolean":        {"{field}"},
		"not_integer":        {"{field}"},
		"profile_undeclared": {"{value}"},
		"text_too_long":      {"{field}", "{maximum}"},
		"out_of_range":       {"{field}", "{minimum}", "{maximum}"},
		"unsupported_type":   {"{field}", "{type}"},
	}
	for name, required := range placeholders {
		for _, placeholder := range required {
			if !strings.Contains(all[name], placeholder) {
				return fmt.Errorf("model3d_usage.brief.messages.%s 必须包含 %s", name, placeholder)
			}
		}
	}
	return nil
}

// validateChoiceKinds checks the generic choice kinds declared by
// application.chat.choice.kinds. Every kind must take its options from the
// usage profiles, the only option source the service builds, and name a
// declared tool and one of its string arguments. The usage-profile kind may
// sit at any position (#103 review C3).
func validateChoiceKinds(schema ChatSchema) error {
	if len(schema.Choice.Kinds) == 0 {
		return nil
	}
	ids := make([]string, 0, len(schema.Choice.Kinds))
	seen := map[string]bool{}
	for index, kind := range schema.Choice.Kinds {
		if strings.TrimSpace(kind.ID) == "" {
			return fmt.Errorf("choice.kinds[%d].id 不能为空", index)
		}
		if seen[kind.ID] {
			return fmt.Errorf("choice.kinds 中 %q 重复", kind.ID)
		}
		seen[kind.ID] = true
		ids = append(ids, kind.ID)
		if kind.OptionSource != "usage_profiles" {
			return fmt.Errorf("choice.kinds[%d].option_source %q 未实现", index, kind.OptionSource)
		}
		for name, value := range map[string]string{
			"tool_name": kind.ToolName,
			"argument":  kind.Argument,
			"question":  kind.Question,
			"message":   kind.Message,
		} {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("choice.kinds[%d].%s 不能为空", index, name)
			}
		}
		parameters, found := declaredToolParameters(schema, kind.ToolName)
		if !found {
			return fmt.Errorf("choice.kinds[%d].tool_name %q 不是已声明的工具", index, kind.ToolName)
		}
		properties, ok := parameters["properties"].(map[string]any)
		if !ok {
			return fmt.Errorf("choice.kinds[%d] 的目标工具 %q 没有参数声明", index, kind.ToolName)
		}
		property, ok := properties[kind.Argument].(map[string]any)
		if !ok {
			return fmt.Errorf("choice.kinds[%d].argument %q 不在目标工具的参数中", index, kind.Argument)
		}
		if property["type"] != "string" {
			return fmt.Errorf("choice.kinds[%d].argument %q 必须是字符串参数", index, kind.Argument)
		}
	}
	parameters, found := declaredToolParameters(schema, "ask_choice")
	if !found {
		return fmt.Errorf("choice.kinds 需要已声明的 ask_choice 工具")
	}
	properties, ok := parameters["properties"].(map[string]any)
	if !ok {
		return fmt.Errorf("ask_choice 没有参数声明")
	}
	kindProperty, ok := properties["kind"].(map[string]any)
	if !ok || kindProperty["type"] != "string" {
		return fmt.Errorf("ask_choice.kind 必须是字符串参数")
	}
	values, ok := kindProperty["enum"].([]any)
	if !ok || len(values) != len(ids) {
		return fmt.Errorf("ask_choice.kind 的 enum 必须与 choice.kinds 一一对应")
	}
	for index, value := range values {
		if value != ids[index] {
			return fmt.Errorf("ask_choice.kind 的 enum 第 %d 项必须是 %q", index+1, ids[index])
		}
	}
	return nil
}

// declaredToolParameters returns a tool's parameters as a generic JSON object.
func declaredToolParameters(schema ChatSchema, name string) (map[string]any, bool) {
	for _, tool := range schema.Tools {
		if tool.Name == name {
			var parameters map[string]any
			if json.Unmarshal(tool.Parameters, &parameters) == nil {
				return parameters, true
			}
			return nil, true
		}
	}
	for _, tool := range schema.AgentTools {
		if tool.Name == name {
			var parameters map[string]any
			if json.Unmarshal(tool.Parameters, &parameters) == nil {
				return parameters, true
			}
			return nil, true
		}
	}
	return nil, false
}

// validateModel3DUsageOfferings checks the Offering IDs, capabilities and
// parameter overrides once the catalog's Offerings are built. Every override
// is frozen the way a real submission would freeze it, on an otherwise
// minimal valid input, so an override that forced_when rewrites, that breaks a
// cross-field rule or that costs extra credits without consent is refused at
// load time rather than at the person's first paid step.
func validateModel3DUsageOfferings(usage ChatModel3DUsageSchema, byID generationMap, viewsArgument string) error {
	if len(usage.Profiles) == 0 {
		return nil
	}
	for index, profile := range usage.Profiles {
		shapes := make([]string, 0, len(profile.RecommendedOfferings))
		for shape := range profile.RecommendedOfferings {
			shapes = append(shapes, shape)
		}
		sort.Strings(shapes)
		for _, shape := range shapes {
			for _, id := range profile.RecommendedOfferings[shape] {
				offering, exists := byID[schemacontract.OfferingID(id)]
				if !exists {
					return fmt.Errorf("model3d_usage.profiles[%d].recommended_offerings[%q] 的 Offering %q 不存在", index, shape, id)
				}
				if err := checkRecommendedShape(offering, shape, viewsArgument); err != nil {
					return fmt.Errorf("model3d_usage.profiles[%d].recommended_offerings[%q]: %w", index, shape, err)
				}
			}
		}
		overrideOfferings := make([]string, 0, len(profile.ParameterOverrides))
		for id := range profile.ParameterOverrides {
			overrideOfferings = append(overrideOfferings, id)
		}
		sort.Strings(overrideOfferings)
		for _, id := range overrideOfferings {
			offering, exists := byID[schemacontract.OfferingID(id)]
			if !exists {
				return fmt.Errorf("model3d_usage.profiles[%d].parameter_overrides 的 Offering %q 不存在", index, id)
			}
			if offering.Domain != schemacontract.DomainModel3D {
				return fmt.Errorf("model3d_usage.profiles[%d].parameter_overrides 的 Offering %q 不属于 3D 域", index, id)
			}
			if err := simulateProfileParameters(offering, profile.ParameterOverrides[id]); err != nil {
				return fmt.Errorf("model3d_usage.profiles[%d].parameter_overrides[%q]: %w", index, id, err)
			}
		}
		for stepIndex, step := range profile.Pipeline {
			if strings.TrimSpace(step.OfferingID) == "" {
				if len(step.Parameters) > 0 {
					return fmt.Errorf("model3d_usage.profiles[%d].pipeline[%d] 未指定 Offering 却声明了参数", index, stepIndex)
				}
				continue
			}
			offering, exists := byID[schemacontract.OfferingID(step.OfferingID)]
			if !exists {
				return fmt.Errorf("model3d_usage.profiles[%d].pipeline[%d].offering_id %q 不存在", index, stepIndex, step.OfferingID)
			}
			if string(offering.Capability) != step.Capability {
				return fmt.Errorf("model3d_usage.profiles[%d].pipeline[%d].offering_id %q 的能力是 %q，不是 %q", index, stepIndex, step.OfferingID, offering.Capability, step.Capability)
			}
			if err := simulateProfileParameters(offering, step.Parameters); err != nil {
				return fmt.Errorf("model3d_usage.profiles[%d].pipeline[%d]: %w", index, stepIndex, err)
			}
		}
	}
	return nil
}

// validateModelReviewProductionOfferings checks the fix capabilities declared
// by model_review.inspect.production against the built Offerings. A capability
// must exist in at least one Offering; a named parameter must be public (or
// fixed to the requested value) and accept the value. Disabled Offerings count
// here because they are declarations; the runtime hint filters them out until
// they are enabled.
func validateModelReviewProductionOfferings(review ChatModelReviewSchema, byID generationMap) error {
	for _, fix := range review.Inspect.Production.Fixes {
		for optionIndex, option := range fix.Options {
			for candidateIndex, candidate := range option.Capabilities {
				if !modelReviewFixCapabilityDeclared(candidate, byID) {
					return fmt.Errorf(
						"model_review.inspect.production.fixes[%s].options[%d].capabilities[%d] 的能力 %q 没有支持该修复的 Offering",
						fix.Check, optionIndex, candidateIndex, candidate.Capability,
					)
				}
			}
		}
	}
	return nil
}

func modelReviewFixCapabilityDeclared(candidate ChatModelInspectFixCapabilitySchema, byID generationMap) bool {
	for _, offering := range byID {
		if string(offering.Capability) != candidate.Capability {
			continue
		}
		if candidate.Parameter == "" {
			return true
		}
		parameter, exists := offeringParameter(offering.Parameters, candidate.Parameter)
		if !exists {
			continue
		}
		if parameter.Fixed {
			if modelReviewParameterDefaultMatches(parameter, candidate.Value) {
				return true
			}
			continue
		}
		if !offering.AgentParameterAllowed(parameter) {
			continue
		}
		if modelReviewParameterAccepts(parameter, candidate.Value) {
			return true
		}
	}
	return false
}

func modelReviewParameterAccepts(parameter schemacontract.Parameter, value string) bool {
	switch parameter.Type {
	case "select":
		for _, option := range parameter.Options {
			if option.Value == value {
				return true
			}
		}
		return false
	case "boolean":
		return value == "true" || value == "false"
	default:
		return true
	}
}

func modelReviewParameterDefaultMatches(parameter schemacontract.Parameter, value string) bool {
	switch parameter.Type {
	case "select", "text":
		var text string
		return json.Unmarshal(parameter.Default, &text) == nil && text == value
	case "boolean":
		var flag bool
		return json.Unmarshal(parameter.Default, &flag) == nil && strconv.FormatBool(flag) == value
	default:
		return false
	}
}

func checkRecommendedShape(offering schemacontract.Offering, shape, viewsArgument string) error {
	switch shape {
	case "text-to-3d":
		if offering.Capability != schemacontract.TextTo3D {
			return fmt.Errorf("Offering %q 的能力是 %q，不是 text-to-3d", offering.ID, offering.Capability)
		}
	case "image-to-3d":
		if offering.Capability != schemacontract.ImageTo3D {
			return fmt.Errorf("Offering %q 的能力是 %q，不是 image-to-3d", offering.ID, offering.Capability)
		}
	case "multiview-to-3d":
		if offering.Capability != schemacontract.ImageTo3D {
			return fmt.Errorf("Offering %q 的能力是 %q，不是 image-to-3d", offering.ID, offering.Capability)
		}
		parameter, exists := offeringParameter(offering.Parameters, viewsArgument)
		if viewsArgument == "" || !exists || parameter.Type != "string-array" {
			return fmt.Errorf("Offering %q 未声明多视图参数 %q", offering.ID, viewsArgument)
		}
	default:
		return fmt.Errorf("未知输入形态 %q", shape)
	}
	return nil
}

// simulateProfileParameters validates each override as a public Agent
// parameter and freezes the whole Offering input with the override applied.
// Missing required runtime inputs are filled with typed placeholders; the
// extra-credit consent is set the way the Agent path sets it, because the
// approval card, not this table, is where the person agrees to the cost.
func simulateProfileParameters(offering schemacontract.Offering, overrides map[string]json.RawMessage) error {
	if len(overrides) == 0 {
		return nil
	}
	values := make(map[string]json.RawMessage, len(overrides))
	for name, raw := range overrides {
		parameter, exists := offeringParameter(offering.Parameters, name)
		if !exists {
			return fmt.Errorf("参数 %q 不属于 Offering %q", name, offering.ID)
		}
		if !offering.AgentParameterAllowed(parameter) {
			return fmt.Errorf("参数 %q 不是可公开调整的参数", name)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("参数 %q 不能用 null 覆盖", name)
		}
		normalized, err := ValidateHarnessParameter(parameter, raw)
		if err != nil {
			return fmt.Errorf("参数 %q: %w", name, err)
		}
		values[name] = normalized
	}
	simulated := make(map[string]json.RawMessage, len(values)+len(offering.Parameters))
	for name, raw := range values {
		simulated[name] = raw
	}
	for _, parameter := range offering.Parameters {
		if _, exists := simulated[parameter.Name]; exists || parameter.Fixed {
			continue
		}
		if !parameter.Required || len(bytes.TrimSpace(parameter.Default)) > 0 {
			continue
		}
		placeholder, err := requiredParameterPlaceholder(parameter)
		if err != nil {
			return err
		}
		simulated[parameter.Name] = placeholder
	}
	if consent := offering.ExtraConsentParameter(); consent != "" {
		simulated[consent] = json.RawMessage(`true`)
	}
	raw, err := json.Marshal(simulated)
	if err != nil {
		return err
	}
	frozen, err := normalizeInput(offering, raw)
	if err != nil {
		return fmt.Errorf("模拟冻结失败: %w", err)
	}
	var actual map[string]json.RawMessage
	if err := json.Unmarshal(frozen, &actual); err != nil {
		return err
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !sameJSONValue(actual[name], values[name]) {
			return fmt.Errorf("参数 %q 的覆盖被 Offering 规则改写或丢弃", name)
		}
	}
	return nil
}

// requiredParameterPlaceholder makes a value that passes the parameter's own
// declaration, so a required runtime input does not mask the override check.
func requiredParameterPlaceholder(parameter schemacontract.Parameter) (json.RawMessage, error) {
	switch parameter.Type {
	case "text", "select", "asset", "task":
		if parameter.Type == "select" && len(parameter.Options) > 0 {
			return json.Marshal(parameter.Options[0].Value)
		}
		return json.Marshal("simulated")
	case "boolean":
		return json.RawMessage(`false`), nil
	case "number":
		if len(parameter.Options) > 0 {
			number, err := strconv.ParseFloat(parameter.Options[0].Value, 64)
			if err != nil {
				return nil, fmt.Errorf("参数 %q 的选项 %q 不是数字", parameter.Name, parameter.Options[0].Value)
			}
			return json.Marshal(number)
		}
		number := 1.0
		if parameter.Minimum != nil {
			number = *parameter.Minimum
		}
		if parameter.Integer {
			number = float64(int(number))
		}
		return json.Marshal(number)
	case "string-array", "asset-array":
		count := parameter.MinItems
		if count < 1 {
			count = 1
		}
		items := make([]string, 0, count)
		for index := 0; index < count; index++ {
			items = append(items, fmt.Sprintf("simulated-%d", index+1))
		}
		return json.Marshal(items)
	default:
		return nil, fmt.Errorf("参数 %q 使用无法模拟的类型 %q", parameter.Name, parameter.Type)
	}
}

// sameJSONValue compares two encoded JSON values by shape and value, so
// 20000 and 20000.0 are the same override.
func sameJSONValue(left, right json.RawMessage) bool {
	var a, b any
	decoder := json.NewDecoder(bytes.NewReader(left))
	decoder.UseNumber()
	if decoder.Decode(&a) != nil {
		return false
	}
	decoder = json.NewDecoder(bytes.NewReader(right))
	decoder.UseNumber()
	if decoder.Decode(&b) != nil {
		return false
	}
	if reflect.DeepEqual(a, b) {
		return true
	}
	leftNumber, leftOK := a.(json.Number)
	rightNumber, rightOK := b.(json.Number)
	if !leftOK || !rightOK {
		return false
	}
	leftValue, leftErr := leftNumber.Float64()
	rightValue, rightErr := rightNumber.Float64()
	return leftErr == nil && rightErr == nil && leftValue == rightValue
}
