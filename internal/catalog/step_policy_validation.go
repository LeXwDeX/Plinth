package catalog

import (
	"errors"
	"fmt"
	"strings"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// The three 3D step modes a person can pick. The ids are fixed by the step
// approval vocabulary (internal/approvalpolicy); the Schema owns the labels.
// A catalog test keeps these in step with that package.
const (
	stepModeAgent     = "agent"
	stepModeUnlimited = "unlimited"
	stepModeCap       = "cap"
)

// validateStepPolicy validates application.step_policy (#330). A Catalog
// without the section stays valid: every check is skipped, the steps mode does
// not exist and the settings route is not registered.
func validateStepPolicy(
	schema ApplicationSchema,
	capabilities map[schemacontract.Capability]struct{},
	auditActions map[string]struct{},
) error {
	policy := schema.StepPolicy
	if policy == nil {
		return nil
	}
	if err := validateValueLabels("step_policy.modes", policy.Modes); err != nil {
		return err
	}
	offered := make(map[string]bool, len(policy.Modes))
	for _, mode := range policy.Modes {
		switch mode.Value {
		case stepModeAgent, stepModeUnlimited, stepModeCap:
		default:
			return fmt.Errorf("step_policy.modes 含未知模式 %q", mode.Value)
		}
		offered[mode.Value] = true
	}
	for entry, mode := range map[string]string{
		"list": policy.DefaultMode.List,
		"mcp":  policy.DefaultMode.MCP,
	} {
		if !offered[mode] {
			return fmt.Errorf("step_policy.default_mode.%s %q 未在 modes 中声明", entry, mode)
		}
	}
	limits := policy.Cap
	if limits.Minimum < 1 || limits.Maximum < limits.Minimum ||
		limits.Default < limits.Minimum || limits.Default > limits.Maximum {
		return errors.New("step_policy.cap 必须满足 1 <= minimum <= default <= maximum")
	}
	extra := policy.ExtraSteps
	if extra.Default < 1 || extra.Maximum < extra.Default || extra.TotalMaximum < extra.Maximum {
		return errors.New("step_policy.extra_steps 必须满足 1 <= default <= maximum <= total_maximum")
	}
	if len(policy.CountedCapabilities) == 0 {
		return errors.New("step_policy.counted_capabilities 不能为空")
	}
	counted := make(map[schemacontract.Capability]bool, len(policy.CountedCapabilities))
	for _, capability := range policy.CountedCapabilities {
		if _, declared := capabilities[capability]; !declared {
			return fmt.Errorf("step_policy.counted_capabilities 引用了未知能力 %q", capability)
		}
		if counted[capability] {
			return fmt.Errorf("step_policy.counted_capabilities 重复声明 %q", capability)
		}
		counted[capability] = true
	}
	if policy.BalanceCacheSeconds < 0 || policy.BalanceCacheSeconds > 3600 ||
		policy.BalanceReadTimeoutSeconds < 0 || policy.BalanceReadTimeoutSeconds > 60 {
		return errors.New("step_policy.balance_cache_seconds 必须在 0 到 3600，balance_read_timeout_seconds 在 0 到 60")
	}
	if err := validateStepPolicyPrompt(policy.Prompt); err != nil {
		return err
	}
	if err := validateStepPolicyResult(policy); err != nil {
		return err
	}
	message := policy.Messages.CapReached
	if strings.TrimSpace(message) == "" ||
		!strings.Contains(message, "{used}") || !strings.Contains(message, "{cap}") {
		return errors.New("step_policy.messages.cap_reached 必须非空并包含 {used} 与 {cap}")
	}
	if extraLimit := policy.Messages.ExtraStepsLimit; strings.TrimSpace(extraLimit) == "" ||
		!strings.Contains(extraLimit, "{maximum}") || !strings.Contains(extraLimit, "{extra}") {
		return errors.New("step_policy.messages.extra_steps_limit 必须非空并包含 {maximum} 与 {extra}")
	}
	if strings.TrimSpace(policy.Messages.ExtraStepsUnavailable) == "" {
		return errors.New("step_policy.messages.extra_steps_unavailable 不能为空")
	}
	if single := policy.Messages.ExtraStepsSingleLimit; strings.TrimSpace(single) == "" ||
		!strings.Contains(single, "{maximum}") {
		return errors.New("step_policy.messages.extra_steps_single_limit 必须非空并包含 {maximum}")
	}
	if strings.TrimSpace(policy.Audit.ResourceType) == "" {
		return errors.New("step_policy.audit.resource_type 不能为空")
	}
	if _, declared := auditActions[policy.Audit.UpdateAction]; !declared {
		return fmt.Errorf("step_policy.audit.update_action 引用了未知 audit action %q", policy.Audit.UpdateAction)
	}
	if strings.TrimSpace(policy.Audit.ExtraStepsResourceType) == "" {
		return errors.New("step_policy.audit.extra_steps_resource_type 不能为空")
	}
	if _, declared := auditActions[policy.Audit.ExtraStepsAction]; !declared {
		return fmt.Errorf("step_policy.audit.extra_steps_action 引用了未知 audit action %q", policy.Audit.ExtraStepsAction)
	}
	route := schema.Runtime.HTTP.Routes.AccountStepPolicy
	if strings.TrimSpace(route) == "" {
		return errors.New("application.step_policy 需要 runtime.http.routes.account_step_policy")
	}
	if !configuredRouteParametersMatch(route, nil) {
		return errors.New("http step policy route account_step_policy 不能含路径参数")
	}
	// "Give it a few more steps" is a list-item action (#330 step 5C): a
	// Catalog that runs lists must declare the route where the person grants
	// them, or the browser would have no way to continue a capped item.
	if list := schema.AssetList; list != nil && list.Execution != nil && list.Execution.Enabled {
		stepsRoute := schema.Runtime.HTTP.Routes.ProjectAssetListItemSteps
		if strings.TrimSpace(stepsRoute) == "" {
			return errors.New("asset_list.execution.enabled 需要 runtime.http.routes.project_asset_list_item_steps")
		}
		if !configuredRouteParametersMatch(stepsRoute, []string{"projectID", "listID", "itemID"}) {
			return errors.New("http route project_asset_list_item_steps 参数必须恰好为 [projectID listID itemID]")
		}
	}
	return nil
}

// validateStepPolicyPrompt validates application.step_policy.prompt (#330).
// Every template is required and names the placeholders the renderer fills;
// a missing one would leave a literal placeholder in the Agent's context.
func validateStepPolicyPrompt(prompt StepPolicyPromptSchema) error {
	required := func(name, text string, placeholders ...string) error {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("step_policy.prompt.%s 不能为空", name)
		}
		for _, placeholder := range placeholders {
			if !strings.Contains(text, placeholder) {
				return fmt.Errorf("step_policy.prompt.%s 缺少占位符 %s", name, placeholder)
			}
		}
		return nil
	}
	if err := required("context_template", prompt.ContextTemplate,
		"{mode_rule}", "{usage}", "{balances}", "{costs}", "{guidance}"); err != nil {
		return err
	}
	if !strings.Contains(prompt.ContextTemplate, "3D 步数") {
		return errors.New("step_policy.prompt.context_template 必须写明「3D 步数」段名")
	}
	rules := make(map[string]bool, len(prompt.ModeRules))
	for _, mode := range prompt.ModeRules {
		switch mode.ID {
		case stepModeAgent, stepModeUnlimited, stepModeCap:
		default:
			return fmt.Errorf("step_policy.prompt.mode_rules 含未知模式 %q", mode.ID)
		}
		if rules[mode.ID] {
			return fmt.Errorf("step_policy.prompt.mode_rules 重复声明 %q", mode.ID)
		}
		rules[mode.ID] = true
		if err := required("mode_rules[id="+mode.ID+"].rule", mode.Rule, "{mode}"); err != nil {
			return err
		}
	}
	for _, mode := range []string{stepModeAgent, stepModeUnlimited, stepModeCap} {
		if !rules[mode] {
			return fmt.Errorf("step_policy.prompt.mode_rules 缺少模式 %q 的说明", mode)
		}
	}
	if err := required("mode_unknown_rule", prompt.ModeUnknownRule); err != nil {
		return err
	}
	if err := required("usage_line", prompt.UsageLine, "{used}", "{cap}", "{remaining}"); err != nil {
		return err
	}
	if err := required("usage_line_no_cap", prompt.UsageLineNoCap, "{used}"); err != nil {
		return err
	}
	for name, text := range map[string]string{
		"usage_unknown":    prompt.UsageUnknown,
		"balances_unknown": prompt.BalancesUnknown,
		"costs_unknown":    prompt.CostsUnknown,
		"guidance":         prompt.Guidance,
	} {
		if err := required(name, text); err != nil {
			return err
		}
	}
	if err := required("balance_item", prompt.BalanceItem, "{label}", "{amount}", "{unit_label}"); err != nil {
		return err
	}
	if err := required("balance_value_item", prompt.BalanceValueItem, "{label}", "{amount}"); err != nil {
		return err
	}
	if err := required("balance_unknown", prompt.BalanceUnknown, "{label}"); err != nil {
		return err
	}
	if err := required("balance_separator", prompt.BalanceSeparator); err != nil {
		return err
	}
	if err := required("balances_line", prompt.BalancesLine, "{balances}"); err != nil {
		return err
	}
	if err := required("cost_item", prompt.CostItem,
		"{capability}", "{offering_id}", "{amount}", "{unit_label}"); err != nil {
		return err
	}
	if err := required("cost_separator", prompt.CostSeparator); err != nil {
		return err
	}
	if err := required("costs_line", prompt.CostsLine, "{costs}"); err != nil {
		return err
	}
	return nil
}
