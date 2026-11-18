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

// mcpStatusNested lists, per status block property path, the property names
// the code writes inside it. A path may descend through objects ("inspection.
// purpose") and the lookup descends an array's items as well.
var mcpStatusNested = map[string][]string{
	MCPStatusAsset:                   {"id", "title", "used_steps", "steps_left"},
	MCPStatusBalances:                {"provider", "label", "state", "amount", "unit", "unit_label", "read_at"},
	MCPStatusStepCosts:               {"offering_id", "provider", "capability", "amount", "unit", "unit_label"},
	MCPStatusFailure:                 {"task_id", "category", "next_action", "retryable", "user_decision_required", "advice"},
	MCPStatusInspection:              {"task_id", "source", "verdict", "purpose", "failed", "waiver", "checked_at"},
	MCPStatusInspection + ".purpose": {"id", "label", "source"},
	MCPStatusInspection + ".failed":  {"check"},
}

// mcpStatusChild resolves a dotted property path in the parsed status block.
func mcpStatusChild(properties map[string]*toolschema.Schema, path string) (*toolschema.Schema, bool) {
	var node *toolschema.Schema
	for index, segment := range strings.Split(path, ".") {
		if index == 0 {
			found := false
			node, found = properties[segment]
			if !found {
				return nil, false
			}
			continue
		}
		if node == nil || node.Properties == nil {
			return nil, false
		}
		next, found := node.Properties[segment]
		if !found {
			return nil, false
		}
		node = next
	}
	return node, node != nil
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
	for name, route := range map[string]string{
		"path": service.Path, "download_path": service.DownloadPath, "signed_download_path": service.SignedDownloadPath,
	} {
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
	if !configuredRouteParametersMatch(service.SignedDownloadPath, []string{"token"}) {
		return errors.New("mcp_public.signed_download_path 必须恰好含路径参数 {token}")
	}
	if service.SignedDownloadPath == service.Path || service.SignedDownloadPath == service.DownloadPath {
		return errors.New("mcp_public.signed_download_path 必须与 path、download_path 不同")
	}
	if !strings.HasPrefix(service.SignedDownloadPath, service.Path+"/") {
		return errors.New("mcp_public.signed_download_path 必须是 path 的子路径")
	}
	if service.SignedDownloadTTLSeconds < 30 || service.SignedDownloadTTLSeconds > service.SignedDownloadMaxTTLSeconds ||
		service.SignedDownloadMaxTTLSeconds > 86400 {
		return errors.New("mcp_public 必须满足 30 <= signed_download_ttl_seconds <= signed_download_max_ttl_seconds <= 86400")
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
	if err := validateMCPGeneration(schema, service, capabilities); err != nil {
		return err
	}
	if err := validateMCPTools(service, schema.AccessKeys, capabilities); err != nil {
		return err
	}
	// inspect_model reports the same limits it enforces; a Catalog that serves
	// it must declare the platform inspection's bounds (#332 step 7a).
	for _, tool := range service.Tools {
		if tool.Name != MCPToolInspectModel {
			continue
		}
		limits := schema.Chat.ModelReview.Inspect.Limits
		if limits.MaxBytes <= 0 || limits.MaxTriangles <= 0 || limits.MaxVertices <= 0 ||
			limits.MaxTexturePixels <= 0 || limits.MaxNodes <= 0 || limits.MaxDepth <= 0 {
			return errors.New("application.mcp_public 声明了 inspect_model，需要 chat.model_review.inspect.limits（max_bytes、max_triangles、max_vertices 等均为正数）")
		}
		break
	}
	// inspect_image narrows the platform picture inspection's limits (#332 step
	// 7b): the byte cap is the platform's own, and the MCP pixel cap must stay
	// within it.
	for _, tool := range service.Tools {
		if tool.Name != MCPToolInspectImage {
			continue
		}
		limits := schema.Chat.ImageReview.Limits
		if limits.MaxBytes <= 0 || limits.MaxPixels <= 0 {
			return errors.New("application.mcp_public 声明了 inspect_image，需要 chat.image_review.limits 的 max_bytes 与 max_pixels 为正数")
		}
		if service.InspectImage.MaxPixels < 1 || service.InspectImage.MaxPixels > limits.MaxPixels {
			return errors.New("mcp_public.inspect_image.max_pixels 必须为正数，且不超过 chat.image_review.limits.max_pixels")
		}
		if err := validateMCPInspectImageChecklist(schema.Chat, tool); err != nil {
			return err
		}
		break
	}
	messages := service.Messages
	for name, text := range map[string]string{
		"unauthenticated": messages.Unauthenticated, "insufficient_scope": messages.InsufficientScope,
		"invalid_arguments": messages.InvalidArguments, "task_not_found": messages.TaskNotFound,
		"asset_not_found": messages.AssetNotFound, "not_found": messages.NotFound,
		"method_not_allowed": messages.MethodNotAllowed, "request_too_large": messages.RequestTooLarge,
		"internal":     messages.Internal,
		"rate_limited": messages.RateLimited, "auth_throttled": messages.AuthThrottled,
		"needs_person": messages.NeedsPerson, "service_unavailable": messages.ServiceUnavailable,
		"service_mismatch": messages.ServiceMismatch, "no_service": messages.NoService,
		"input_refused": messages.InputRefused, "source_not_found": messages.SourceNotFound,
		"source_not_ready": messages.SourceNotReady, "source_incompatible": messages.SourceIncompatible,
		"image_not_found": messages.ImageNotFound, "asset_name_invalid": messages.AssetNameInvalid,
		"asset_project_mismatch": messages.AssetProjectMismatch, "project_not_found": messages.ProjectNotFound,
		"idempotency_conflict": messages.IdempotencyConflict, "in_progress": messages.InProgress,
		"too_many_in_flight": messages.TooManyInFlight, "upload_too_large": messages.UploadTooLarge,
		"upload_invalid": messages.UploadInvalid, "upload_type_not_allowed": messages.UploadTypeNotAllowed,
		"mask_invalid": messages.MaskInvalid, "mask_size_mismatch": messages.MaskSizeMismatch,
		"mask_not_supported":          messages.MaskNotSupported,
		"signed_download_unavailable": messages.SignedDownloadUnavailable, "model_unavailable": messages.ModelUnavailable,
		"image_not_ready": messages.ImageNotReady, "image_unreadable": messages.ImageUnreadable,
		"image_too_large": messages.ImageTooLarge, "image_unavailable": messages.ImageUnavailable,
		"image_busy": messages.ImageBusy, "asset_not_image": messages.AssetNotImage,
	} {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("mcp_public.messages.%s 不能为空", name)
		}
	}
	if !strings.Contains(messages.InsufficientScope, "{scope}") || !strings.Contains(messages.InvalidArguments, "{detail}") {
		return errors.New("mcp_public.messages.insufficient_scope 必须含 {scope}，invalid_arguments 必须含 {detail}")
	}
	for name, check := range map[string]struct{ text, holder string }{
		"needs_person": {messages.NeedsPerson, "{task_id}"}, "service_unavailable": {messages.ServiceUnavailable, "{service}"},
		"service_mismatch": {messages.ServiceMismatch, "{service}"}, "too_many_in_flight": {messages.TooManyInFlight, "{seconds}"},
		"rate_limited": {messages.RateLimited, "{seconds}"}, "auth_throttled": {messages.AuthThrottled, "{seconds}"},
		"asset_name_invalid": {messages.AssetNameInvalid, "{max}"},
		"upload_too_large":   {messages.UploadTooLarge, "{max}"}, "upload_type_not_allowed": {messages.UploadTypeNotAllowed, "{types}"},
		"mask_invalid": {messages.MaskInvalid, "{max}"},
	} {
		if !strings.Contains(check.text, check.holder) {
			return fmt.Errorf("mcp_public.messages.%s 必须含 %s", name, check.holder)
		}
	}
	for _, holder := range []string{"{mask_width}", "{mask_height}", "{image_width}", "{image_height}"} {
		if !strings.Contains(messages.MaskSizeMismatch, holder) {
			return fmt.Errorf("mcp_public.messages.mask_size_mismatch 必须含 %s", holder)
		}
	}
	if !strings.Contains(messages.ImageTooLarge, "{bytes}") || !strings.Contains(messages.ImageTooLarge, "{pixels}") {
		return errors.New("mcp_public.messages.image_too_large 必须含 {bytes} 与 {pixels}")
	}
	if !strings.Contains(messages.ImageBusy, "{seconds}") {
		return errors.New("mcp_public.messages.image_busy 必须含 {seconds}")
	}
	return nil
}

// mcpImageChecklists are the checklist names inspect_image serves, in the
// platform tool's order. The MCP declaration must offer exactly these.
var mcpImageChecklists = []string{"delivery", "3d_input"}

// validateMCPInspectImageChecklist keeps the MCP inspect_image checklist enum
// equal to the platform tool's (#332 step 7b): the handler trusts the enum the
// Schema serves, and the platform tool remains the one vocabulary.
func validateMCPInspectImageChecklist(chat ChatSchema, tool MCPPublicTool) error {
	input, err := toolschema.Parse(tool.InputSchema, mcpArgumentOptions)
	if err != nil {
		return fmt.Errorf("mcp_public.tools[%s].input_schema 无效: %w", tool.Name, err)
	}
	property, declared := input.Properties["checklist"]
	if !declared || len(property.Enum) != len(mcpImageChecklists) {
		return fmt.Errorf("mcp_public.tools[%s].input_schema 的 checklist 必须用 enum 列出 %s", tool.Name, strings.Join(mcpImageChecklists, "、"))
	}
	for index, value := range property.Enum {
		if value != mcpImageChecklists[index] {
			return fmt.Errorf("mcp_public.tools[%s].input_schema 的 checklist enum 必须是 %s", tool.Name, strings.Join(mcpImageChecklists, "、"))
		}
	}
	parameters, found := declaredToolParameters(chat, tool.Name)
	if !found {
		return fmt.Errorf("chat.agent_tools 缺少 %s 的声明，无法核对 inspect_image 的 checklist", tool.Name)
	}
	properties, _ := parameters["properties"].(map[string]any)
	platform, _ := properties["checklist"].(map[string]any)
	values, _ := platform["enum"].([]any)
	if len(values) != len(mcpImageChecklists) {
		return fmt.Errorf("%s 的 checklist enum 必须是 %s", tool.Name, strings.Join(mcpImageChecklists, "、"))
	}
	for index, value := range values {
		if value != mcpImageChecklists[index] {
			return fmt.Errorf("%s 的 checklist enum 必须是 %s", tool.Name, strings.Join(mcpImageChecklists, "、"))
		}
	}
	return nil
}

// validateMCPGeneration checks the sections the tools that start work read:
// the assets, the key namespace, the upload cap and the audit marks.
func validateMCPGeneration(schema ApplicationSchema, service *MCPPublicSchema, capabilities map[schemacontract.Capability]struct{}) error {
	assets := service.Assets
	if assets.NameMaxLength < 1 || assets.NameMaxLength > 200 {
		return errors.New("mcp_public.assets.name_max_length 必须在 1 到 200 之间")
	}
	if !strings.Contains(assets.TitleTemplate, "{name}") || strings.TrimSpace(assets.DefaultTitle) == "" {
		return errors.New("mcp_public.assets.title_template 必须含 {name}，default_title 不能为空")
	}
	generation := service.Generation
	prefix := generation.IdempotencyKeyPrefix
	if prefix == "" || len(prefix) > 32 || strings.ContainsAny(prefix, " \t\r\n") {
		return errors.New("mcp_public.generation.idempotency_key_prefix 必须是 1 到 32 个字符、不含空白的前缀")
	}
	if len(generation.SourceVariantPreference) == 0 {
		return errors.New("mcp_public.generation.source_variant_preference 不能为空")
	}
	for _, variant := range generation.SourceVariantPreference {
		if strings.TrimSpace(variant) == "" {
			return errors.New("mcp_public.generation.source_variant_preference 含空名")
		}
	}
	upload := service.Upload
	// The base64 text of the picture and the rest of the request must fit the body bound.
	encoded := (upload.MaxBytes + 2) / 3 * 4
	if upload.MaxBytes < 1 || encoded+8192 > service.MaxRequestBytes {
		return errors.New("mcp_public.upload.max_bytes 必须为正，且它的 base64 文本加 8 KiB 要放进 max_request_bytes")
	}
	if upload.MaxBytes > schema.Runtime.InputAssets.UploadMaxBytes {
		return errors.New("mcp_public.upload.max_bytes 不能超过 runtime.input_assets.upload_max_bytes")
	}
	if upload.FilenameMaxLength < 1 || upload.FilenameMaxLength > 255 {
		return errors.New("mcp_public.upload.filename_max_length 必须在 1 到 255 之间")
	}
	if len(upload.MediaTypes) == 0 {
		return errors.New("mcp_public.upload.media_types 不能为空")
	}
	for _, mediaType := range upload.MediaTypes {
		if !strings.HasPrefix(mediaType, "image/") || strings.ToLower(mediaType) != mediaType {
			return fmt.Errorf("mcp_public.upload.media_types 只能是小写的 image/* 类型: %q", mediaType)
		}
		if allowed := schema.Runtime.InputAssets.AllowedUploadMediaTypes; len(allowed) > 0 && !slices.Contains(allowed, mediaType) {
			return fmt.Errorf("mcp_public.upload.media_types 的 %q 不在 runtime.input_assets.allowed_upload_media_types 中", mediaType)
		}
	}
	audit := service.Audit
	for name, text := range map[string]string{"entry_field": audit.EntryField, "entry_value": audit.EntryValue, "access_key_field": audit.AccessKeyField} {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("mcp_public.audit.%s 不能为空", name)
		}
	}
	return nil
}

// mcpToolArguments are the argument names the code of each tool that starts
// work reads. A Schema that leaves one out would declare a tool whose
// arguments the code never sees.
var mcpToolArguments = map[string][]string{
	MCPToolUploadImage:      {"data", "filename"},
	MCPToolGenerateImage:    {"asset", "project", "service", "prompt", "images", "mask", "parameters", "parameter_reasons", "idempotency_key"},
	MCPToolRemoveBackground: {"asset", "project", "service", "image", "parameters", "parameter_reasons", "idempotency_key"},
	MCPToolCreateModel:      {"asset", "project", "service", "prompt", "images", "parameters", "parameter_reasons", "idempotency_key"},
	MCPToolProcessModel: {"asset", "project", "service", "operation", "source_task_id", "source_asset_id", "prompt", "images",
		"parameters", "parameter_reasons", "idempotency_key"},
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
		MCPStatusTotalUsedSteps, MCPStatusBalances, MCPStatusStepCosts, MCPStatusFailure, MCPStatusInspection,
	} {
		if _, declared := parsed.Properties[name]; !declared {
			return fmt.Errorf("mcp_public.status_block.schema 缺少属性 %q（服务端会写出它）", name)
		}
	}
	for path, nested := range mcpStatusNested {
		node, found := mcpStatusChild(parsed.Properties, path)
		if !found {
			return fmt.Errorf("mcp_public.status_block.schema 缺少属性 %q（服务端会写出它）", path)
		}
		if node.Items != nil {
			node = node.Items
		}
		for _, field := range nested {
			if _, declared := node.Properties[field]; !declared {
				return fmt.Errorf("mcp_public.status_block.schema 的 %s 缺少属性 %q（服务端会写出它）", path, field)
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

func validateMCPTools(service *MCPPublicSchema, keys *AccessKeysSchema, capabilities map[schemacontract.Capability]struct{}) error {
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
		input, err := toolschema.Parse(tool.InputSchema, mcpArgumentOptions)
		if err != nil {
			return fmt.Errorf("mcp_public.tools[%s].input_schema 无效: %w", tool.Name, err)
		}
		if err := validateMCPToolContract(service, tool, input, capabilities); err != nil {
			return err
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

// mcpArgumentOptions is how a tool's arguments are parsed: closed objects, but
// a deliberately free-form map (a caller's service parameters) is allowed.
var mcpArgumentOptions = toolschema.Options{ClosedObjects: true, RootObject: true, FreeFormObjects: true}

// validateMCPToolContract checks what the code expects of a tool that starts
// work: the arguments it reads, the generate scope, and, for a tool that
// starts a Task, the declared capabilities and a required idempotency key
// that fits the Task's key once the namespace prefix is put before it.
func validateMCPToolContract(service *MCPPublicSchema, tool MCPPublicTool, input *toolschema.Schema, capabilities map[schemacontract.Capability]struct{}) error {
	names, startsWork := mcpToolArguments[tool.Name]
	if !startsWork {
		if len(tool.Capabilities) > 0 {
			return fmt.Errorf("mcp_public.tools[%s] 不启动任务，不能声明 capabilities", tool.Name)
		}
		return nil
	}
	if tool.Scope != "generate" {
		return fmt.Errorf("mcp_public.tools[%s].scope 必须是 generate", tool.Name)
	}
	for _, name := range names {
		if _, declared := input.Properties[name]; !declared {
			return fmt.Errorf("mcp_public.tools[%s].input_schema 缺少参数 %q（服务端会读取它）", tool.Name, name)
		}
	}
	if tool.Name == MCPToolUploadImage {
		if len(tool.Capabilities) > 0 {
			return fmt.Errorf("mcp_public.tools[%s] 不启动任务，不能声明 capabilities", tool.Name)
		}
		return nil
	}
	if len(tool.Capabilities) == 0 {
		return fmt.Errorf("mcp_public.tools[%s].capabilities 不能为空", tool.Name)
	}
	grouped := map[schemacontract.Capability]bool{}
	for _, group := range service.ServiceGroups {
		for _, capability := range group.Capabilities {
			grouped[capability] = true
		}
	}
	seen := map[schemacontract.Capability]bool{}
	for _, capability := range tool.Capabilities {
		if _, declared := capabilities[capability]; !declared || !grouped[capability] || seen[capability] {
			return fmt.Errorf("mcp_public.tools[%s].capabilities 含未知、重复或不在 service_groups 里的能力 %q", tool.Name, capability)
		}
		seen[capability] = true
	}
	key := input.Properties["idempotency_key"]
	required := slices.Contains(input.Required, "idempotency_key")
	if !required || !slices.Contains(key.Types, "string") || len(key.Types) != 1 || key.MinLength == nil || *key.MinLength < 1 ||
		key.MaxLength == nil || len(service.Generation.IdempotencyKeyPrefix)+*key.MaxLength > 200 {
		return fmt.Errorf("mcp_public.tools[%s].input_schema 的 idempotency_key 必须是必填字符串，声明 minLength（至少 1）与 maxLength，且前缀加上它不超过 200 字节", tool.Name)
	}
	if tool.Name == MCPToolProcessModel {
		operation := input.Properties["operation"]
		if len(operation.Enum) == 0 {
			return fmt.Errorf("mcp_public.tools[%s].input_schema 的 operation 必须用 enum 列出能力", tool.Name)
		}
		for _, value := range operation.Enum {
			text, _ := value.(string)
			if !slices.Contains(tool.Capabilities, schemacontract.Capability(text)) {
				return fmt.Errorf("mcp_public.tools[%s].input_schema 的 operation 含不在 capabilities 里的值 %v", tool.Name, value)
			}
		}
	}
	return nil
}

// ParsedInputSchema returns the parsed argument schema of a declared tool.
func (m MCPPublicSchema) ParsedInputSchema(name string) (*toolschema.Schema, bool, error) {
	for _, tool := range m.Tools {
		if tool.Name == name {
			parsed, err := toolschema.Parse(tool.InputSchema, mcpArgumentOptions)
			return parsed, true, err
		}
	}
	return nil, false, nil
}
