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
	message := policy.Messages.CapReached
	if strings.TrimSpace(message) == "" ||
		!strings.Contains(message, "{used}") || !strings.Contains(message, "{cap}") {
		return errors.New("step_policy.messages.cap_reached 必须非空并包含 {used} 与 {cap}")
	}
	if strings.TrimSpace(policy.Audit.ResourceType) == "" {
		return errors.New("step_policy.audit.resource_type 不能为空")
	}
	if _, declared := auditActions[policy.Audit.UpdateAction]; !declared {
		return fmt.Errorf("step_policy.audit.update_action 引用了未知 audit action %q", policy.Audit.UpdateAction)
	}
	route := schema.Runtime.HTTP.Routes.AccountStepPolicy
	if strings.TrimSpace(route) == "" {
		return errors.New("application.step_policy 需要 runtime.http.routes.account_step_policy")
	}
	if !configuredRouteParametersMatch(route, nil) {
		return errors.New("http step policy route account_step_policy 不能含路径参数")
	}
	return nil
}
