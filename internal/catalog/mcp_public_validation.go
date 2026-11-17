package catalog

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
	"github.com/LeXwDeX/Plinth/internal/schema/toolschema"
)

var (
	mcpRoutePattern     = regexp.MustCompile(`^/[A-Za-z0-9._/-]*[A-Za-z0-9._-]$`)
	mcpStatusKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	mcpGroupIDPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

// mcpStatusNested lists, per status block property, the property names the
// code writes inside it (the object of "asset", the items of the arrays).
var mcpStatusNested = map[string][]string{
	MCPStatusAsset:     {"id", "title", "used_steps", "steps_left"},
	MCPStatusBalances:  {"provider", "label", "state", "amount", "unit", "unit_label", "read_at"},
	MCPStatusStepCosts: {"offering_id", "provider", "capability", "amount", "unit", "unit_label"},
	MCPStatusFailure:   {"task_id", "category", "next_action", "retryable", "user_decision_required", "advice"},
}

// mcpForbiddenArguments are argument names a tool may never declare: the
// person is the key's owner, never an argument.
var mcpForbiddenArguments = []string{"owner", "owner_id", "user", "user_id", "principal", "scope"}

// validateMCPPublic validates application.mcp_public (#332). A Catalog without
// the section stays valid: every check is skipped and no listener starts.
func validateMCPPublic(schema ApplicationSchema, capabilities map[schemacontract.Capability]struct{}) error {
	service := schema.MCPPublic
	if service == nil {
		return nil
	}
	if schema.AccessKeys == nil || schema.StepPolicy == nil {
		return errors.New("application.mcp_public 需要 application.access_keys 与 application.step_policy")
	}
	policy := schema.StepPolicy
	if policy.BalanceCacheSeconds < 1 || policy.BalanceCacheSeconds > 3600 ||
		policy.BalanceReadTimeoutSeconds < 1 || policy.BalanceReadTimeoutSeconds > 60 {
		return errors.New("application.mcp_public 需要 step_policy.balance_cache_seconds（1 到 3600）与 balance_read_timeout_seconds（1 到 60）")
	}
	for name, route := range map[string]string{"path": service.Path, "download_path": service.DownloadPath} {
		if len(route) < 2 || !mcpRoutePattern.MatchString(strings.ReplaceAll(strings.ReplaceAll(route, "{", "a"), "}", "a")) {
			return fmt.Errorf("mcp_public.%s 必须是以 / 开头、不以 / 结尾的路径: %q", name, route)
		}
	}
	if strings.ContainsAny(service.Path, "{}") {
		return errors.New("mcp_public.path 不能含路径参数")
	}
	if !configuredRouteParametersMatch(service.DownloadPath, []string{"taskID", "assetID"}) {
		return errors.New("mcp_public.download_path 必须恰好含路径参数 {taskID} 与 {assetID}")
	}
	if service.Path == service.DownloadPath {
		return errors.New("mcp_public.path 与 download_path 必须不同")
	}
	for name, text := range map[string]string{
		"protocol_version": service.ProtocolVersion, "server_name": service.ServerName, "instructions": service.Instructions,
	} {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("mcp_public.%s 不能为空", name)
		}
	}
	if service.MaxRequestBytes < 1 || service.MaxRequestBytes > 16<<20 ||
		service.MaxResponseBytes < 1 || service.MaxResponseBytes > 64<<20 {
		return errors.New("mcp_public.max_request_bytes 必须在 1 到 16 MiB，max_response_bytes 在 1 到 64 MiB")
	}
	for name, seconds := range map[string]int{
		"read_header_timeout_seconds": service.ReadHeaderTimeoutSeconds,
		"read_timeout_seconds":        service.ReadTimeoutSeconds,
		"write_timeout_seconds":       service.WriteTimeoutSeconds,
		"idle_timeout_seconds":        service.IdleTimeoutSeconds,
	} {
		if seconds < 1 || seconds > 86400 {
			return fmt.Errorf("mcp_public.%s 必须在 1 到 86400 之间", name)
		}
	}
	if service.PollIntervalSeconds < 1 || service.PollIntervalSeconds > 3600 {
		return errors.New("mcp_public.poll_interval_seconds 必须在 1 到 3600 之间")
	}
	if service.ListPageSize < 1 || service.ListMaxPageSize < service.ListPageSize || service.ListMaxPageSize > 200 {
		return errors.New("mcp_public 必须满足 1 <= list_page_size <= list_max_page_size <= 200")
	}
	if err := validateMCPServiceGroups(service.ServiceGroups, capabilities); err != nil {
		return err
	}
	if err := validateMCPStatusBlock(service.StatusBlock); err != nil {
		return err
	}
	if err := validateMCPTools(service, schema.AccessKeys); err != nil {
		return err
	}
	messages := service.Messages
	for name, text := range map[string]string{
		"unauthenticated": messages.Unauthenticated, "insufficient_scope": messages.InsufficientScope,
		"invalid_arguments": messages.InvalidArguments, "task_not_found": messages.TaskNotFound,
		"asset_not_found": messages.AssetNotFound, "not_found": messages.NotFound,
		"method_not_allowed": messages.MethodNotAllowed, "request_too_large": messages.RequestTooLarge,
		"internal": messages.Internal,
	} {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("mcp_public.messages.%s 不能为空", name)
		}
	}
	if !strings.Contains(messages.InsufficientScope, "{scope}") || !strings.Contains(messages.InvalidArguments, "{detail}") {
		return errors.New("mcp_public.messages.insufficient_scope 必须含 {scope}，invalid_arguments 必须含 {detail}")
	}
	return nil
}

func validateMCPServiceGroups(groups []MCPPublicServiceGroup, capabilities map[schemacontract.Capability]struct{}) error {
	if len(groups) == 0 {
		return errors.New("mcp_public.service_groups 不能为空")
	}
	ids := map[string]bool{}
	grouped := map[schemacontract.Capability]bool{}
	for _, group := range groups {
		if !mcpGroupIDPattern.MatchString(group.ID) || ids[group.ID] || strings.TrimSpace(group.Label) == "" || len(group.Capabilities) == 0 {
			return fmt.Errorf("mcp_public.service_groups 的分组 %q 无效（id 为小写字母、数字和连字符且不重复，需要 label 与能力）", group.ID)
		}
		ids[group.ID] = true
		for _, capability := range group.Capabilities {
			if _, declared := capabilities[capability]; !declared {
				return fmt.Errorf("mcp_public.service_groups 引用了未知能力 %q", capability)
			}
			if grouped[capability] {
				return fmt.Errorf("mcp_public.service_groups 重复声明能力 %q", capability)
			}
			grouped[capability] = true
		}
	}
	return nil
}

func validateMCPStatusBlock(block MCPPublicStatusBlock) error {
	if !mcpStatusKeyPattern.MatchString(block.Key) {
		return fmt.Errorf("mcp_public.status_block.key 必须是小写字母、数字和下划线组成的名字: %q", block.Key)
	}
	parsed, err := toolschema.Parse(block.Schema, toolschema.Options{RootObject: true})
	if err != nil {
		return fmt.Errorf("mcp_public.status_block.schema 无效: %w", err)
	}
	for _, name := range []string{
		MCPStatusStepMode, MCPStatusStepModeLabel, MCPStatusStepCap, MCPStatusAsset,
		MCPStatusTotalUsedSteps, MCPStatusBalances, MCPStatusStepCosts, MCPStatusFailure,
	} {
		if _, declared := parsed.Properties[name]; !declared {
			return fmt.Errorf("mcp_public.status_block.schema 缺少属性 %q（服务端会写出它）", name)
		}
	}
	for name, nested := range mcpStatusNested {
		node := parsed.Properties[name]
		if node.Items != nil {
			node = node.Items
		}
		for _, field := range nested {
			if _, declared := node.Properties[field]; !declared {
				return fmt.Errorf("mcp_public.status_block.schema 的 %s 缺少属性 %q（服务端会写出它）", name, field)
			}
		}
	}
	if len(block.BalanceAccounts) == 0 {
		return errors.New("mcp_public.status_block.balance_accounts 不能为空")
	}
	// The list names accounts of runtime.provider_accounts in display order. An
	// account a deployment does not declare is skipped when the block is
	// assembled (a Catalog may leave a provider out), so it is not a reference
	// error here.
	seen := map[string]bool{}
	for _, account := range block.BalanceAccounts {
		if strings.TrimSpace(account) == "" || seen[account] {
			return fmt.Errorf("mcp_public.status_block.balance_accounts 含空名或重复的 Provider 账户 %q", account)
		}
		seen[account] = true
	}
	if block.MaxAssets < 1 || block.MaxAssets > 100 || block.CostTaskWindow < 1 || block.CostTaskWindow > 200 {
		return errors.New("mcp_public.status_block 必须满足 1 <= max_assets <= 100 且 1 <= cost_task_window <= 200")
	}
	summary := block.Summary
	for name, check := range map[string]struct {
		text    string
		holders []string
	}{
		"template":          {summary.Template, []string{"{mode}", "{asset_steps}", "{total_steps}", "{balances}"}},
		"template_no_asset": {summary.TemplateNoAsset, []string{"{mode}", "{total_steps}", "{balances}"}},
		"balance_item":      {summary.BalanceItem, []string{"{label}", "{amount}", "{unit_label}"}},
		"balance_unknown":   {summary.BalanceUnknown, []string{"{label}"}},
	} {
		for _, holder := range check.holders {
			if !strings.Contains(check.text, holder) {
				return fmt.Errorf("mcp_public.status_block.summary.%s 必须含 %s", name, holder)
			}
		}
	}
	if strings.TrimSpace(summary.Unknown) == "" || summary.Separator == "" {
		return errors.New("mcp_public.status_block.summary.unknown 与 separator 不能为空")
	}
	return nil
}

func validateMCPTools(service *MCPPublicSchema, keys *AccessKeysSchema) error {
	if len(service.Tools) == 0 {
		return errors.New("mcp_public.tools 不能为空")
	}
	known := MCPPublicToolNames()
	scopes := make([]string, 0, len(keys.Scopes))
	for _, scope := range keys.Scopes {
		scopes = append(scopes, scope.Value)
	}
	seen := map[string]bool{}
	for _, tool := range service.Tools {
		if !slices.Contains(known, tool.Name) || seen[tool.Name] {
			return fmt.Errorf("mcp_public.tools 含未知或重复的工具 %q", tool.Name)
		}
		seen[tool.Name] = true
		if strings.TrimSpace(tool.Description) == "" {
			return fmt.Errorf("mcp_public.tools[%s].description 不能为空", tool.Name)
		}
		if !slices.Contains(scopes, tool.Scope) {
			return fmt.Errorf("mcp_public.tools[%s].scope %q 未在 access_keys.scopes 中声明", tool.Name, tool.Scope)
		}
		input, err := toolschema.Parse(tool.InputSchema, toolschema.Options{ClosedObjects: true, RootObject: true})
		if err != nil {
			return fmt.Errorf("mcp_public.tools[%s].input_schema 无效: %w", tool.Name, err)
		}
		for _, name := range mcpForbiddenArguments {
			if _, declared := input.Properties[name]; declared {
				return fmt.Errorf("mcp_public.tools[%s].input_schema 不能声明参数 %q（身份只来自 Key）", tool.Name, name)
			}
		}
		output, err := toolschema.Parse(tool.OutputSchema, toolschema.Options{RootObject: true})
		if err != nil {
			return fmt.Errorf("mcp_public.tools[%s].output_schema 无效: %w", tool.Name, err)
		}
		if _, clash := output.Properties[service.StatusBlock.Key]; clash {
			return fmt.Errorf("mcp_public.tools[%s].output_schema 不能自己声明状态块的键 %q", tool.Name, service.StatusBlock.Key)
		}
		served, _, err := service.ToolOutputSchema(tool.Name)
		if err != nil {
			return fmt.Errorf("mcp_public.tools[%s].output_schema 无法加上状态块: %w", tool.Name, err)
		}
		if _, err := toolschema.Parse(served, toolschema.Options{RootObject: true}); err != nil {
			return fmt.Errorf("mcp_public.tools[%s] 的输出 Schema 无效: %w", tool.Name, err)
		}
	}
	return nil
}

// ParsedInputSchema returns the parsed argument schema of a declared tool.
func (m MCPPublicSchema) ParsedInputSchema(name string) (*toolschema.Schema, bool, error) {
	for _, tool := range m.Tools {
		if tool.Name == name {
			parsed, err := toolschema.Parse(tool.InputSchema, toolschema.Options{ClosedObjects: true, RootObject: true})
			return parsed, true, err
		}
	}
	return nil, false, nil
}
