package catalog

// Application schema validation orchestration.
import (
	"errors"
	"fmt"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
	"strings"
)

func ValidateApplicationSchema(schema ApplicationSchema) error {
	return validateApplication(schema)
}

func validateApplication(schema ApplicationSchema) error {
	if err := validateAgentConfiguration(schema.AgentConfiguration); err != nil {
		return err
	}
	if err := validateAgentConfigurationConnections(schema); err != nil {
		return err
	}
	if err := validateLLMSettings(schema); err != nil {
		return err
	}
	if strings.TrimSpace(schema.Revision) == "" {
		return errors.New("revision 不能为空")
	}
	if err := validateTerminologySchema(schema.Terminology); err != nil {
		return fmt.Errorf("terminology: %w", err)
	}
	if err := validateRuntime(schema.Runtime); err != nil {
		return fmt.Errorf("runtime: %w", err)
	}
	if err := validateContent(schema.Content); err != nil {
		return fmt.Errorf("content: %w", err)
	}
	if err := validateChat(schema.Chat, schema.Runtime, schema.Capabilities); err != nil {
		return fmt.Errorf("chat: %w", err)
	}
	if err := validateCanvas(schema.Canvas, schema.Runtime, schema.Content); err != nil {
		return fmt.Errorf("canvas: %w", err)
	}
	if err := validateProviderWebhooks(
		schema.ProviderWebhooks,
		schema.Runtime,
	); err != nil {
		return fmt.Errorf("provider_webhooks: %w", err)
	}
	if err := validateAdministration(
		schema.Administration,
		schema.Runtime,
		schema.Authentication,
	); err != nil {
		return fmt.Errorf("administration: %w", err)
	}
	if err := validateProviderDiscovery(schema.ProviderDiscovery); err != nil {
		return fmt.Errorf("provider_discovery: %w", err)
	}
	if err := validateTaskHistory(schema.TaskHistory); err != nil {
		return fmt.Errorf("task_history: %w", err)
	}
	if err := validateModelDelivery(schema); err != nil {
		return fmt.Errorf("model_delivery: %w", err)
	}
	if len(schema.Capabilities) == 0 {
		return errors.New("capabilities 不能为空")
	}
	capabilities := map[schemacontract.Capability]struct{}{}
	capabilityOrders := map[int]schemacontract.Capability{}
	legacyPaths := map[string]schemacontract.Capability{}
	for index, capability := range schema.Capabilities {
		if !capability.ID.Valid() {
			return fmt.Errorf("capabilities[%d].id 无效: %q", index, capability.ID)
		}
		if !capability.Domain.Valid() {
			return fmt.Errorf("capabilities[%d].domain 无效: %q", index, capability.Domain)
		}
		if strings.TrimSpace(capability.Label) == "" ||
			strings.TrimSpace(capability.LabelCN) == "" {
			return fmt.Errorf("capabilities[%d] 的展示文本不能为空", index)
		}
		if capability.Order < 0 {
			return fmt.Errorf("capabilities[%d].order 不能为负数", index)
		}
		if _, exists := capabilities[capability.ID]; exists {
			return fmt.Errorf("capability 重复: %q", capability.ID)
		}
		if previous, exists := capabilityOrders[capability.Order]; exists {
			return fmt.Errorf(
				"capability order %d 重复: %q 与 %q",
				capability.Order,
				previous,
				capability.ID,
			)
		}
		capabilities[capability.ID] = struct{}{}
		capabilityOrders[capability.Order] = capability.ID
		if group := capability.ImageResultGroup; group != nil {
			if capability.Domain != schemacontract.DomainImage || len(group.Slots) < 2 || len(group.Slots) > 8 {
				return fmt.Errorf("capabilities[%d].image_result_group 需要 2 到 8 个图片槽位", index)
			}
			seen := map[string]bool{}
			for _, slot := range group.Slots {
				if !validateOutputContractIdentifier("variant", slot.Variant) || slot.Variant == "" || strings.TrimSpace(slot.Label) == "" || seen[slot.Variant] {
					return fmt.Errorf("capabilities[%d].image_result_group 槽位无效或重复", index)
				}
				seen[slot.Variant] = true
			}
			if group.PartialMessage != "" && (group.SourceSelectable || strings.TrimSpace(schema.Content.Messages[group.PartialMessage]) == "") {
				return fmt.Errorf("capabilities[%d].image_result_group.partial_message 需要已声明的文案，且不能用于整组来源", index)
			}
		}
		workflow := capability.Workflow
		if workflow != nil &&
			strings.TrimSpace(workflow.InitialInputMode) == "" {
			return fmt.Errorf(
				"capabilities[%d].workflow.initial_input_mode 不能为空",
				index,
			)
		}
		if capability.LegacyAPI != nil {
			if err := validateLegacyTaskAPI(*capability.LegacyAPI); err != nil {
				return fmt.Errorf("capabilities[%d].legacy_api: %w", index, err)
			}
			if err := validateLegacyTaskMessageKeys(
				schema.Content,
				*capability.LegacyAPI,
			); err != nil {
				return fmt.Errorf("capabilities[%d].legacy_api: %w", index, err)
			}
			if previous, exists := legacyPaths[capability.LegacyAPI.Path]; exists {
				return fmt.Errorf(
					"legacy API path %q 重复: %q 与 %q",
					capability.LegacyAPI.Path,
					previous,
					capability.ID,
				)
			}
			legacyPaths[capability.LegacyAPI.Path] = capability.ID
		}
		if capability.PromptComposition != nil {
			composition := capability.PromptComposition
			template := schema.Content.Messages[composition.TemplateMessageKey]
			if composition.MinimumReferences < 2 ||
				!strings.Contains(template, "{prompt}") {
				return fmt.Errorf(
					"capabilities[%d].prompt_composition 必须声明至少 2 张参考图且 template_message_key 指向包含 {prompt} 的文本",
					index,
				)
			}
		}
	}
	configuredProcessingCapabilities := map[schemacontract.Capability]struct{}{}
	for _, configured := range schema.Runtime.Frontend.ModelProcessingCapabilities {
		if _, duplicate := configuredProcessingCapabilities[configured]; duplicate {
			return fmt.Errorf("frontend.model_processing_capabilities 重复: %q", configured)
		}
		found := false
		for _, capability := range schema.Capabilities {
			if capability.ID == configured && capability.Domain == schemacontract.DomainProcessing {
				found = true
				if strings.TrimSpace(capability.Icon) == "" {
					return fmt.Errorf(
						"frontend.model_processing_capabilities 的处理能力 %q 必须声明 icon（舞台工具栏按钮图标）",
						configured,
					)
				}
				break
			}
		}
		if !found {
			return fmt.Errorf("frontend.model_processing_capabilities 未声明处理能力: %q", configured)
		}
		configuredProcessingCapabilities[configured] = struct{}{}
	}
	for index, tool := range schema.Chat.Tools {
		if _, exists := capabilities[tool.Capability]; !exists {
			return fmt.Errorf(
				"chat.tools[%d].capability 引用了未知能力 %q",
				index,
				tool.Capability,
			)
		}
	}
	if err := validateTaskStream(
		schema.TaskStream,
		schema.Runtime,
		capabilities,
	); err != nil {
		return fmt.Errorf("task_stream: %w", err)
	}

	requiredStates := []schemacontract.TaskState{
		schemacontract.TaskQueued,
		schemacontract.TaskRunning,
		schemacontract.TaskMaterializing,
		schemacontract.TaskSucceeded,
		schemacontract.TaskFailed,
		schemacontract.TaskCanceled,
	}
	states := map[string]struct{}{}
	retryCountdownStates := 0
	for index, state := range schema.TaskStates {
		if strings.TrimSpace(state.InternalState) == "" ||
			strings.TrimSpace(state.PublicState) == "" ||
			strings.TrimSpace(state.Label) == "" ||
			strings.TrimSpace(state.Color) == "" {
			return fmt.Errorf("task_states[%d] 的状态和展示文本不能为空", index)
		}
		switch state.Outcome {
		case "pending", "running", "success", "failure", "canceled":
		default:
			return fmt.Errorf(
				"task_states[%d].outcome 非法: %q",
				index,
				state.Outcome,
			)
		}
		if state.Terminal != (state.Outcome == "success" ||
			state.Outcome == "failure" ||
			state.Outcome == "canceled") {
			return fmt.Errorf(
				"task_states[%d] 的 terminal 与 outcome %q 不一致",
				index,
				state.Outcome,
			)
		}
		switch state.Progress.Mode {
		case "hidden", "reported":
			if state.Progress.FixedValue != 0 {
				return fmt.Errorf(
					"task_states[%d] 的 %q 进度模式不得声明 fixed_value",
					index,
					state.Progress.Mode,
				)
			}
		case "fixed", "retry-countdown":
			if state.Progress.FixedValue <= 0 || state.Progress.FixedValue > 100 {
				return fmt.Errorf(
					"task_states[%d].progress.fixed_value 必须在 1 到 100 之间",
					index,
				)
			}
		default:
			return fmt.Errorf(
				"task_states[%d].progress.mode 非法: %q",
				index,
				state.Progress.Mode,
			)
		}
		if state.Progress.ShowError &&
			(state.Outcome != "failure" ||
				strings.TrimSpace(state.Progress.ErrorColor) == "") {
			return fmt.Errorf(
				"task_states[%d] 的错误展示必须属于 failure 且声明 error_color",
				index,
			)
		}
		if state.Progress.Mode == "retry-countdown" &&
			(strings.TrimSpace(state.Progress.RetryBadgeColor) == "" ||
				strings.TrimSpace(state.Progress.TrackColor) == "" ||
				strings.TrimSpace(state.Progress.CountdownColor) == "") {
			return fmt.Errorf(
				"task_states[%d] 的 retry-countdown 展示样式不完整",
				index,
			)
		}
		if state.Progress.Mode == "retry-countdown" {
			retryCountdownStates++
		}
		if _, exists := states[state.InternalState]; exists {
			return fmt.Errorf("task state 重复: %q", state.InternalState)
		}
		states[state.InternalState] = struct{}{}
	}
	for _, state := range requiredStates {
		if _, exists := states[string(state)]; !exists {
			return fmt.Errorf("task_states 缺少 Go 运行态 %q", state)
		}
	}
	if _, exists := states[schema.Runtime.Frontend.TaskProgress.FallbackState]; !exists {
		return fmt.Errorf(
			"frontend.task_progress.fallback_state 不存在: %q",
			schema.Runtime.Frontend.TaskProgress.FallbackState,
		)
	}
	for name, state := range map[string]string{
		"initial_state":   schema.Runtime.Frontend.TaskProgress.InitialState,
		"not_found_state": schema.Runtime.Frontend.TaskProgress.NotFoundState,
	} {
		if _, exists := states[state]; !exists {
			return fmt.Errorf(
				"frontend.task_progress.%s 不存在: %q",
				name,
				state,
			)
		}
	}
	if retryCountdownStates != 1 {
		return fmt.Errorf(
			"task_states 必须且只能声明一个 retry-countdown，当前为 %d",
			retryCountdownStates,
		)
	}

	if err := validateValueLabels("audit_actions", schema.AuditActions); err != nil {
		return err
	}
	auditActions := make(map[string]struct{}, len(schema.AuditActions))
	for _, action := range schema.AuditActions {
		auditActions[action.Value] = struct{}{}
	}
	for action := range schema.Administration.AuditLogs.Presentation.
		ActionBadgeClasses {
		if _, exists := auditActions[action]; !exists {
			return fmt.Errorf(
				"administration.audit_logs.presentation.action_badge_classes 引用了未知 audit action %q",
				action,
			)
		}
	}
	for _, action := range schema.Administration.UserAssets.Filters.
		DownloadActions {
		if _, exists := auditActions[action.Action]; !exists {
			return fmt.Errorf(
				"administration.user_assets.filters.download_actions 引用了未知 audit action %q",
				action.Action,
			)
		}
	}
	for field, action := range map[string]string{
		"login_audit_action":   schema.Authentication.API.LoginAuditAction,
		"logout_audit_action":  schema.Authentication.API.LogoutAuditAction,
		"chat_create_action":   schema.Chat.Audit.CreateAction,
		"chat_delete_action":   schema.Chat.Audit.DeleteAction,
		"canvas_create_action": schema.Canvas.Audit.CreateAction,
		"canvas_delete_action": schema.Canvas.Audit.DeleteAction,
		"canvas_merge_action":  schema.Canvas.Audit.MergeAction,
		"task_create_action":   schema.Runtime.TaskAudit.CreateAction,
		"task_delete_action":   schema.Runtime.TaskAudit.DeleteAction,
		"input_upload_action":  schema.Runtime.InputAssets.Audit.UploadAction,
		"input_access_action":  schema.Runtime.InputAssets.Audit.AccessAction,
		"task_asset_download": schema.Runtime.AssetDelivery.TaskAssetAudit.
			DownloadAction,
		"admin_user_create": schema.Administration.Users.Audit.
			CreateAction,
		"admin_user_update": schema.Administration.Users.Audit.
			UpdateAction,
		"admin_user_deactivate": schema.Administration.Users.Audit.
			DeactivateAction,
		"admin_user_delete": schema.Administration.Users.Audit.
			DeleteAction,
		"admin_generation_download": schema.Administration.Generations.Audit.
			DownloadAction,
		"asset_proxy_download": schema.Runtime.AssetDelivery.Proxy.Audit.Action,
	} {
		if _, exists := auditActions[action]; !exists {
			return fmt.Errorf(
				"authentication.api.%s 引用了未知 audit action %q",
				field,
				action,
			)
		}
	}
	generatedImage := schema.Runtime.AssetDelivery.LegacyGeneratedImage
	if strings.TrimSpace(
		schema.Content.Messages[generatedImage.NotFoundMessageKey],
	) == "" {
		return fmt.Errorf(
			"asset_delivery.legacy_generated_image 引用了未知 message key %q",
			generatedImage.NotFoundMessageKey,
		)
	}
	if _, exists := auditActions[generatedImage.AuditAction]; !exists {
		return fmt.Errorf(
			"asset_delivery.legacy_generated_image 引用了未知 audit action %q",
			generatedImage.AuditAction,
		)
	}
	if err := validateAuthentication(schema.Authentication); err != nil {
		return err
	}
	if err := validateLocalization(schema); err != nil {
		return fmt.Errorf("localization: %w", err)
	}
	if err := validateApplicationTerminology(schema); err != nil {
		return fmt.Errorf("terminology: %w", err)
	}
	return nil
}
