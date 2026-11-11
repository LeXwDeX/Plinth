package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// validateConvergence checks the diverge-then-converge blocks (#242). A
// Catalog that declares none stays valid; when one is enabled its bounds,
// declared tools and texts must be complete, and the direction card's options
// must fit the choice card's own bounds.
func validateConvergence(schema ChatSchema) error {
	if err := validateConceptDirections(schema); err != nil {
		return err
	}
	if err := validateCandidates(schema); err != nil {
		return err
	}
	return validateReroll(schema)
}

func validateConceptDirections(schema ChatSchema) error {
	directions := schema.ConceptDirections
	if !directions.Enabled {
		return nil
	}
	if directions.MinCount < 2 || directions.MaxCount < directions.MinCount ||
		(schema.Choice.MaxOptions > 0 && directions.MaxCount > schema.Choice.MaxOptions) {
		return errors.New("concept_directions: 需要 2 <= min_count <= max_count <= choice.max_options")
	}
	tool, found := chatToolByName(schema, directions.SketchTool)
	if !found || tool.Execution.ImplementationKey != "generation.execute" {
		return fmt.Errorf("concept_directions.sketch_tool %q 不是已声明的生成工具", directions.SketchTool)
	}
	imageDomain := false
	for _, domain := range tool.Execution.Domains {
		imageDomain = imageDomain || domain == "image"
	}
	if !imageDomain {
		return fmt.Errorf("concept_directions.sketch_tool %q 不属于图像域", directions.SketchTool)
	}
	// #242: concept sketches are a 2D step; a 3D generation tool must never be
	// drafted into the image flow.
	if tool.Execution.AgentScope != "2d" {
		return fmt.Errorf("concept_directions.sketch_tool %q 必须属于 2D 工具", directions.SketchTool)
	}
	kind, found := chatChoiceKind(schema, directions.Kind)
	if !found || kind.OptionSource != "agent" {
		return fmt.Errorf("concept_directions.kind %q 必须是 option_source=agent 的选择卡", directions.Kind)
	}
	if _, declared := declaredToolParameters(schema, kind.ToolName); !declared || strings.TrimSpace(kind.ToolName) == "" {
		return fmt.Errorf("concept_directions.kind %q 的目标工具 %q 未声明", directions.Kind, kind.ToolName)
	}
	for name, value := range map[string]string{
		"question":         directions.Question,
		"message":          directions.Message,
		"context_template": directions.ContextTemplate,
		"limit_reached":    directions.LimitReached,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("concept_directions.%s 不能为空", name)
		}
	}
	for _, placeholder := range []string{"{minimum}", "{maximum}", "{sketch_tool}"} {
		if !strings.Contains(directions.ContextTemplate, placeholder) {
			return fmt.Errorf("concept_directions.context_template 缺少 %s", placeholder)
		}
	}
	if !strings.Contains(directions.LimitReached, "{maximum}") {
		return errors.New("concept_directions.limit_reached 缺少 {maximum}")
	}
	if len(directions.ParameterOverrides) > 0 && strings.TrimSpace(directions.ParameterReason) == "" {
		return errors.New("concept_directions.parameter_reason 不能为空：低成本覆盖需要随提交冻结的说明")
	}
	if directions.MaxLabelCharacters <= 0 || directions.MaxLabelCharacters > 120 ||
		directions.MaxDescriptionCharacters <= 0 || directions.MaxDescriptionCharacters > 300 ||
		directions.MaxArgumentsBytes <= 0 {
		return errors.New("concept_directions: 选项上限必须为正且不超过 ask_choice.options 的 120/300 字符")
	}
	properties, _ := declaredToolParameters(schema, "ask_choice")
	optionSchema := askChoiceOptionSchema(properties)
	if optionSchema == nil {
		return errors.New("concept_directions: ask_choice.options 必须声明每个选项的对象")
	}
	if _, ok := optionSchema["arguments"]; !ok {
		return errors.New("concept_directions: ask_choice.options 的选项必须声明 arguments")
	}
	if _, ok := optionSchema["thumbnail"]; !ok {
		return errors.New("concept_directions: ask_choice.options 的选项必须声明 thumbnail")
	}
	return nil
}

func validateCandidates(schema ChatSchema) error {
	candidates := schema.Candidates
	if !candidates.Enabled {
		return nil
	}
	if candidates.MaxCount < 2 {
		return errors.New("candidates.max_count 至少为 2")
	}
	// #242: expanded candidate steps count against the plan's own step bound,
	// so the plan the person approves can never exceed budget.max_steps.
	// A historical or minimal Catalog may not declare budget steps at all; the
	// runtime simply cannot expand candidates there, so keep it loadable.
	if schema.ApprovalPolicy.Budget.MaxSteps > 0 && candidates.MaxCount > schema.ApprovalPolicy.Budget.MaxSteps {
		return fmt.Errorf("candidates.max_count %d 超过 budget.max_steps %d",
			candidates.MaxCount, schema.ApprovalPolicy.Budget.MaxSteps)
	}
	if len(candidates.Sources) == 0 {
		return errors.New("candidates.sources 不能为空")
	}
	seen := map[string]bool{}
	for _, source := range candidates.Sources {
		switch source {
		case "provider", "seed", "input_image":
		default:
			return fmt.Errorf("candidates.sources 含未实现的来源 %q", source)
		}
		if seen[source] {
			return fmt.Errorf("candidates.sources 中 %q 重复", source)
		}
		seen[source] = true
	}
	if strings.TrimSpace(candidates.ContextTemplate) == "" ||
		!strings.Contains(candidates.ContextTemplate, "{maximum}") ||
		!strings.Contains(candidates.ContextTemplate, "{sources}") {
		return errors.New("candidates.context_template 不能为空且必须包含 {maximum} 与 {sources}")
	}
	return nil
}

func validateReroll(schema ChatSchema) error {
	reroll := schema.Reroll
	if !reroll.Enabled {
		return nil
	}
	if len(reroll.SeedParameters) == 0 {
		return errors.New("reroll.seed_parameters 不能为空")
	}
	seen := map[string]bool{}
	for _, name := range reroll.SeedParameters {
		if strings.TrimSpace(name) == "" {
			return errors.New("reroll.seed_parameters 含空参数名")
		}
		if seen[name] {
			return fmt.Errorf("reroll.seed_parameters 中 %q 重复", name)
		}
		seen[name] = true
		// #242: a seed is a number the reroll replaces; a text or enum
		// parameter declared by the same name must not be silently rewritten.
		if !declaredIntegerParameter(schema, name) {
			return fmt.Errorf("reroll.seed_parameters 的 %q 不是任何已声明工具的整数参数", name)
		}
	}
	if reroll.MinSeed > reroll.MaxSeed {
		return errors.New("reroll.min_seed 不能大于 max_seed")
	}
	for name, value := range map[string]string{
		"messages.not_succeeded":        reroll.Messages.NotSucceeded,
		"messages.no_frozen_input":      reroll.Messages.NoFrozenInput,
		"messages.in_progress":          reroll.Messages.InProgress,
		"messages.declined":             reroll.Messages.Declined,
		"messages.unavailable":          reroll.Messages.Unavailable,
		"messages.idempotency_required": reroll.Messages.IdempotencyRequired,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("reroll.%s 不能为空", name)
		}
	}
	return nil
}

// validateConceptDirectionOfferings checks the cheap sketch parameters once
// the Catalog's Offerings exist (#242). Each override must name an image
// Offering and freeze exactly the value it declares, like a real submission.
func validateConceptDirectionOfferings(directions ChatConceptDirectionsSchema, byID generationMap) error {
	ids := make([]string, 0, len(directions.ParameterOverrides))
	for id := range directions.ParameterOverrides {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		offering, exists := byID[schemacontract.OfferingID(id)]
		if !exists {
			return fmt.Errorf("concept_directions.parameter_overrides 的 Offering %q 不存在", id)
		}
		if offering.Domain != schemacontract.DomainImage {
			return fmt.Errorf("concept_directions.parameter_overrides 的 Offering %q 不属于图像域", id)
		}
		overrides := map[string]json.RawMessage{}
		for name, value := range directions.ParameterOverrides[id] {
			fixed := false
			for _, parameter := range offering.Parameters {
				if parameter.Name == name && parameter.Fixed {
					fixed = true
					break
				}
			}
			// A fixed parameter wins over any profile override; the value is
			// simply not applied, so it must not make the Catalog unloadable.
			if !fixed {
				overrides[name] = value
			}
		}
		if len(overrides) > 0 {
			if err := simulateProfileParameters(offering, overrides); err != nil {
				return fmt.Errorf("concept_directions.parameter_overrides[%q]: %w", id, err)
			}
		}
	}
	return nil
}

// declaredIntegerParameter reports whether any declared Agent or generation
// tool takes a parameter of this name typed as an integer. A reroll only
// rewrites numeric seeds (#242).
func declaredIntegerParameter(schema ChatSchema, name string) bool {
	integer := func(parameters json.RawMessage) bool {
		var document map[string]any
		if json.Unmarshal(parameters, &document) != nil {
			return false
		}
		properties, _ := document["properties"].(map[string]any)
		property, _ := properties[name].(map[string]any)
		return property["type"] == "integer"
	}
	for _, tool := range schema.Tools {
		if integer(tool.Parameters) {
			return true
		}
	}
	for _, tool := range schema.AgentTools {
		if integer(tool.Parameters) {
			return true
		}
	}
	return false
}

// chatToolByName finds a declared generation or Agent tool.
func chatToolByName(schema ChatSchema, name string) (ChatToolSchema, bool) {
	for _, tool := range schema.Tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return ChatToolSchema{}, false
}

// chatChoiceKind finds one declared choice kind.
func chatChoiceKind(schema ChatSchema, id string) (ChatChoiceKindSchema, bool) {
	for _, kind := range schema.Choice.Kinds {
		if kind.ID == id {
			return kind, true
		}
	}
	return ChatChoiceKindSchema{}, false
}

// askChoiceOptionSchema returns the item object of ask_choice.options, or nil.
func askChoiceOptionSchema(parameters map[string]any) map[string]any {
	properties, _ := parameters["properties"].(map[string]any)
	options, _ := properties["options"].(map[string]any)
	items, _ := options["items"].(map[string]any)
	if items == nil || items["type"] != "object" {
		return nil
	}
	optionProperties, _ := items["properties"].(map[string]any)
	return optionProperties
}
