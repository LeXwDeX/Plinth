package catalog

// Chat tools, execution contracts and workspace settings.
import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

func validateChat(schema ChatSchema, content ContentSchema, runtime RuntimeSchema, capabilityPresentations []CapabilityPresentation, maxPromptCharacters int) error {
	if runtime.HTTP.Routes.ChatSessionBranch != "" {
		if strings.Count(schema.Defaults.BranchTitleTemplate, "{title}") != 1 ||
			strings.TrimSpace(schema.Presentation.Messages.BranchTurnActive) == "" ||
			strings.TrimSpace(schema.Presentation.Messages.BranchPendingApproval) == "" ||
			strings.TrimSpace(schema.Presentation.Messages.BranchScene) == "" ||
			schema.API.ResponseFields.ParentSessionID == "" || schema.API.ResponseFields.ParentMessageID == "" ||
			schema.API.ResponseFields.ParentTitle == "" || schema.API.ResponseFields.ParentDeleted == "" ||
			schema.API.ResponseFields.BranchCanvasCopied == "" || schema.API.ResponseFields.BranchSceneCopied == "" ||
			schema.API.ResponseFields.BranchCapturesCopied == "" {
			return errors.New("chat: branch route requires title template, errors and parent fields")
		}
	}
	capabilityDomains := make(map[schemacontract.Capability]schemacontract.CapabilityDomain, len(capabilityPresentations))
	for _, capability := range capabilityPresentations {
		capabilityDomains[capability.ID] = capability.Domain
	}
	if connectionID := strings.TrimSpace(schema.Agent.ConnectionID); connectionID != "" {
		connection, exists := providerConnectionByID(
			runtime.ProviderConnections,
			connectionID,
		)
		if !exists {
			return fmt.Errorf(
				"chat.agent.connection_id 引用了未知 Provider 连接 %q",
				connectionID,
			)
		}
		if connection.Protocol != "openai-compatible" {
			return fmt.Errorf(
				"chat.agent.connection_id %q 的协议 %q 不是 openai-compatible",
				connectionID,
				connection.Protocol,
			)
		}
	}
	required := map[string]string{
		"api.fields.approval_mode":         schema.API.Fields.ApprovalMode,
		"api.fields.agent_scope":           schema.API.Fields.AgentScope,
		"api.response_fields.agent_scope":  schema.API.ResponseFields.AgentScope,
		"audit.agent_scope_action":         schema.Audit.AgentScopeAction,
		"api.fields.notes":                 schema.API.Fields.Notes,
		"api.response_fields.notes":        schema.API.ResponseFields.Notes,
		"api.fields.brief":                 schema.API.Fields.Brief,
		"api.response_fields.brief":        schema.API.ResponseFields.Brief,
		"api.fields.explicit_model_3d":     schema.API.Fields.ExplicitModel3D,
		"audit.production_brief_action":    schema.Audit.ProductionBriefAction,
		"api.response_fields.pick":         schema.API.ResponseFields.Pick,
		"api.fields.title":                 schema.API.Fields.Title,
		"api.fields.content":               schema.API.Fields.Content,
		"api.fields.image_urls":            schema.API.Fields.ImageURLs,
		"api.fields.skill_id":              schema.API.Fields.SkillID,
		"api.fields.skill_name":            schema.API.Fields.SkillName,
		"api.fields.from_id":               schema.API.Fields.FromID,
		"api.response_fields.id":           schema.API.ResponseFields.ID,
		"api.response_fields.title":        schema.API.ResponseFields.Title,
		"api.response_fields.message_id":   schema.API.ResponseFields.MessageID,
		"api.response_fields.session_id":   schema.API.ResponseFields.SessionID,
		"api.response_fields.error":        schema.API.ResponseFields.Error,
		"api.response_fields.tool_name":    schema.API.ResponseFields.ToolName,
		"api.response_fields.tool_args":    schema.API.ResponseFields.ToolArguments,
		"api.prompt_enhance.prompt":        schema.API.PromptEnhance.PromptField,
		"api.prompt_enhance.capability":    schema.API.PromptEnhance.CapabilityField,
		"api.prompt_enhance.model":         schema.API.PromptEnhance.ModelField,
		"api.prompt_enhance.result":        schema.API.PromptEnhance.ResultField,
		"api.prompt_enhance.prompt_result": schema.API.PromptEnhance.PromptResultField,
		"api.prompt_enhance.negative_result": schema.API.PromptEnhance.
			NegativePromptResultField,
		"api.prompt_enhance.error": schema.API.PromptEnhance.ErrorField,
		"api.prompt_enhance.default_profile": schema.API.PromptEnhance.
			DefaultProfile,
		"defaults.title":                        schema.Defaults.Title,
		"roles.system":                          schema.Roles.System,
		"roles.user":                            schema.Roles.User,
		"roles.assistant":                       schema.Roles.Assistant,
		"roles.tool":                            schema.Roles.Tool,
		"state.pending_content":                 schema.State.PendingContent,
		"state.active":                          schema.State.Active,
		"state.done":                            schema.State.Done,
		"state.error":                           schema.State.Error,
		"state.turn_queued":                     schema.State.TurnQueued,
		"state.turn_running":                    schema.State.TurnRunning,
		"state.turn_waiting_task":               schema.State.TurnWaitingTask,
		"state.turn_done":                       schema.State.TurnDone,
		"state.turn_error":                      schema.State.TurnError,
		"frontend.option_storage_key":           schema.Frontend.OptionStorageKey,
		"frontend.add_images_event_name":        schema.Frontend.AddImagesEventName,
		"frontend.image_mime_prefix":            schema.Frontend.ImageMIMEPrefix,
		"frontend.image_file_accept":            schema.Frontend.ImageFileAccept,
		"skills.feature":                        schema.Skills.Feature,
		"skills.fields.revision":                schema.Skills.Fields.Revision,
		"skills.fields.skills":                  schema.Skills.Fields.Skills,
		"skills.fields.id":                      schema.Skills.Fields.ID,
		"skills.fields.name":                    schema.Skills.Fields.Name,
		"skills.fields.name_zh":                 schema.Skills.Fields.NameZH,
		"skills.fields.category":                schema.Skills.Fields.Category,
		"skills.fields.recommended_size":        schema.Skills.Fields.RecommendedSize,
		"skills.fields.thumbnail_path":          schema.Skills.Fields.ThumbnailPath,
		"skills.fields.raw_image_url":           schema.Skills.Fields.RawImageURL,
		"skills.fields.prompt":                  schema.Skills.Fields.Prompt,
		"skills.fields.i2i_template":            schema.Skills.Fields.ImageToImageTemplate,
		"skills.fields.tags":                    schema.Skills.Fields.Tags,
		"skills.fields.tool":                    schema.Skills.Fields.Tool,
		"skills.fields.requires_image":          schema.Skills.Fields.RequiresImage,
		"skills.fields.default_message":         schema.Skills.Fields.DefaultMessage,
		"skills.fields.one_shot":                schema.Skills.Fields.OneShot,
		"skills.fields.error":                   schema.Skills.Fields.Error,
		"skills.execution.user_input":           schema.Skills.Execution.PromptTemplateFields.UserInput,
		"skills.execution.skill_prompt":         schema.Skills.Execution.PromptTemplateFields.SkillPrompt,
		"frontend.send_key":                     schema.Frontend.SendKey,
		"frontend.send_modifier":                schema.Frontend.SendModifier,
		"frontend.lightbox_close_key":           schema.Frontend.LightboxCloseKey,
		"frontend.lightbox_previous_key":        schema.Frontend.LightboxPreviousKey,
		"frontend.lightbox_next_key":            schema.Frontend.LightboxNextKey,
		"frontend.optimistic_id_prefix":         schema.Frontend.OptimisticMessageIDPrefix,
		"frontend.optimistic_user_id":           schema.Frontend.OptimisticUserMessageID,
		"frontend.optimistic_assistant_id":      schema.Frontend.OptimisticAssistantMessageID,
		"frontend.filename_query":               schema.Frontend.FilenameQueryParameter,
		"frontend.download_extension":           schema.Frontend.DownloadExtension,
		"frontend.fallback_download_id":         schema.Frontend.FallbackDownloadIdentifier,
		"frontend.internal_api_prefix":          schema.Frontend.InternalAPIPathPrefix,
		"frontend.hidden_content_prefix":        schema.Frontend.LegacyHiddenContentPrefix,
		"frontend.hidden_content_suffix":        schema.Frontend.LegacyHiddenContentSuffix,
		"agent.adapter_key":                     schema.Agent.AdapterKey,
		"agent.base_url_setting":                schema.Agent.BaseURLSetting,
		"agent.api_key_setting":                 schema.Agent.APIKeySetting,
		"agent.model_setting":                   schema.Agent.ModelSetting,
		"agent.endpoint_path":                   schema.Agent.EndpointPath,
		"agent.system_prompt":                   schema.Agent.SystemPrompt,
		"agent.title_prompt":                    schema.Agent.TitlePrompt,
		"agent.mock_tool_name":                  schema.Agent.MockToolName,
		"agent.mock_prompt_argument":            schema.Agent.MockPromptArgument,
		"agent.mock_summary":                    schema.Agent.MockSummary,
		"context.asset_url_template":            schema.Context.AssetURLTemplate,
		"context.tool_prompt_argument":          schema.Context.ToolPromptArgument,
		"context.tool_reference_argument":       schema.Context.ToolReferenceArgument,
		"context.reference_filename":            schema.Context.ReferenceFilename,
		"context.text_part_kind":                schema.Context.TextPartKind,
		"context.image_part_kind":               schema.Context.ImagePartKind,
		"context.image_detail":                  schema.Context.ImageDetail,
		"context.canvas_image_detail":           schema.Context.CanvasImageDetail,
		"context.user_image_label":              schema.Context.UserImageLabelTemplate,
		"context.ai_image_label":                schema.Context.AIImageLabelTemplate,
		"context.current_upload_hint":           schema.Context.CurrentUploadHint,
		"dsml.invoke_element":                   schema.DSML.InvokeElement,
		"dsml.parameter_element":                schema.DSML.ParameterElement,
		"dsml.name_attribute":                   schema.DSML.NameAttribute,
		"dsml.string_attribute":                 schema.DSML.StringAttribute,
		"dsml.true_value":                       schema.DSML.TrueValue,
		"dsml.false_value":                      schema.DSML.FalseValue,
		"dsml.call_id_template":                 schema.DSML.CallIDTemplate,
		"stream.event_fields.type":              schema.Stream.EventFields.Type,
		"stream.event_types.text":               schema.Stream.EventTypes.Text,
		"stream.event_types.done":               schema.Stream.EventTypes.Done,
		"presentation.session_not_found":        schema.Presentation.Messages.SessionNotFound,
		"presentation.empty_message":            schema.Presentation.Messages.EmptyMessage,
		"presentation.agent_images_unsupported": schema.Presentation.Messages.AgentImagesUnsupported,
		"presentation.agent_content_rejected":   schema.Presentation.Messages.AgentContentRejected,
		"presentation.agent_quota_exhausted":    schema.Presentation.Messages.AgentQuotaExhausted,
		"presentation.agent_not_configured":     schema.Presentation.Messages.AgentNotConfigured,
		"presentation.invalid_parameters":       schema.Presentation.Messages.InvalidParameters,
		"presentation.model_choice_conflict":    schema.Presentation.Messages.ModelChoiceConflict,
		"presentation.model_choice_unavailable": schema.Presentation.Messages.ModelChoiceUnavailable,
		"presentation.turn_active":              schema.Presentation.Messages.TurnActive,
		"presentation.agent_busy":               schema.Presentation.Messages.AgentBusy,
		"presentation.status.generating":        schema.Presentation.Status.Generating,
		"presentation.status.queued_submission": schema.Presentation.Status.QueuedSubmission,
		"audit.create_action":                   schema.Audit.CreateAction,
		"audit.delete_action":                   schema.Audit.DeleteAction,
		"audit.session_resource_type":           schema.Audit.SessionResourceType,
		"audit.message_resource_type":           schema.Audit.MessageResourceType,
		"audit.approval_mode_action":            schema.Audit.ApprovalModeAction,
		"api.response_fields.approval_mode":     schema.API.ResponseFields.ApprovalMode,
		"api.response_fields.estimated_credits": schema.API.ResponseFields.EstimatedCredits,
		"stream.event_fields.estimated_credits": schema.Stream.EventFields.EstimatedCredits,
		"api.response_fields.approval_source":   schema.API.ResponseFields.ApprovalSource,
		"stream.event_fields.approval_source":   schema.Stream.EventFields.ApprovalSource,
		"api.fields.attachments":                schema.API.Fields.Attachments,
		"api.fields.selection":                  schema.API.Fields.Selection,
		"api.fields.mentions":                   schema.API.Fields.Mentions,
		"api.fields.input_source":               schema.API.Fields.InputSource,
		"context.canvas_input_source":           schema.Context.CanvasInputSource,
		"presentation.invalid_input_source":     schema.Presentation.Messages.InvalidInputSource,
		"presentation.canvas_source_missing":    schema.Presentation.Messages.CanvasSourceMissing,
		"api.attachment.token":                  schema.API.Attachment.Token,
		"api.fields.domain_models":              schema.API.Fields.DomainModels,
		"api.response_fields.attachments":       schema.API.ResponseFields.Attachments,
		"api.attachment.handle":                 schema.API.Attachment.Handle,
		"api.attachment.kind":                   schema.API.Attachment.Kind,
		"api.attachment.role":                   schema.API.Attachment.Role,
		"api.attachment.asset_id":               schema.API.Attachment.AssetID,
		"api.attachment.task_id":                schema.API.Attachment.TaskID,
		"api.attachment.capture_id":             schema.API.Attachment.CaptureID,
		"api.attachment.image_slot":             schema.API.Attachment.ImageSlot,
		"api.attachment.url":                    schema.API.Attachment.URL,
		"api.attachment.canvas_layers":          schema.API.Attachment.CanvasLayers,

		"presentation.agent_inline_image_output": schema.Presentation.Messages.AgentInlineImageOutput,
	}
	// The preference members are required only when the Catalog declares
	// preference storage, so a historical Catalog without #241 stays valid.
	if schema.Preferences.MaximumEntries > 0 {
		for name, value := range map[string]string{
			"api.response_fields.preferences": schema.API.ResponseFields.Preferences,
			"api.preference.reason_preset":    schema.API.Preference.ReasonPreset,
			"api.preference.reason_text":      schema.API.Preference.ReasonText,
			"api.preference.signal":           schema.API.Preference.Signal,
			"api.preference.handles":          schema.API.Preference.Handles,
			"api.preference.direction":        schema.API.Preference.Direction,
			"api.preference.active":           schema.API.Preference.Active,
			"api.preference.kind":             schema.API.Preference.Kind,
			"api.preference.source":           schema.API.Preference.Source,
			"api.preference.alternatives":     schema.API.Preference.Alternatives,
			"api.preference.explicit":         schema.API.Preference.Explicit,
			"api.preference.user_edited":      schema.API.Preference.UserEdited,
		} {
			required[name] = value
		}
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s 不能为空", name)
		}
	}
	if schema.API.Fields.AttachmentPurposes != "" {
		if strings.TrimSpace(schema.API.Attachment.Purpose) == "" || schema.API.Attachment.Purpose == schema.API.Attachment.ImageSlot {
			return fmt.Errorf("api.attachment.purpose 无效")
		}
		for _, name := range []string{"style", "layout", "input"} {
			if strings.TrimSpace(schema.AttachmentPurposes[name].AgentText) == "" {
				return fmt.Errorf("attachment_purposes.%s.agent_text 不能为空", name)
			}
		}
	}
	if err := validateApprovalPolicy(schema.ApprovalPolicy, capabilityPresentations); err != nil {
		return err
	}
	if err := validateChatChoice(schema); err != nil {
		return err
	}
	if err := validateChatProcessing(schema.Processing); err != nil {
		return err
	}
	if err := validateModel3DUsageShape(schema, capabilityPresentations); err != nil {
		return err
	}
	if err := validateImage2DUsageShape(schema); err != nil {
		return err
	}
	if err := validateConvergence(schema); err != nil {
		return err
	}
	if err := validateHarness(schema, capabilityPresentations); err != nil {
		return err
	}
	if err := validateAgentScope(schema.AgentScope); err != nil {
		return err
	}
	if err := validatePromptSections(schema, capabilityDomains); err != nil {
		return err
	}
	if err := validateChatAttachments(schema); err != nil {
		return err
	}
	directStyleLockCapabilities := make(map[schemacontract.Capability]bool, len(schema.StyleLock.DirectTaskCapabilities))
	for _, capability := range schema.StyleLock.DirectTaskCapabilities {
		if !capability.Valid() || capabilityDomains[capability] != schemacontract.DomainImage {
			return errors.New("style_lock.direct_task_capabilities 只能声明图片域能力")
		}
		if directStyleLockCapabilities[capability] {
			return errors.New("style_lock.direct_task_capabilities 不能重复声明同一能力")
		}
		directStyleLockCapabilities[capability] = true
	}
	referenceCapabilities := make(map[schemacontract.Capability]bool, len(schema.StyleLock.ReferenceCapabilities))
	for _, capability := range schema.StyleLock.ReferenceCapabilities {
		if !capability.Valid() {
			return errors.New("style_lock.reference_capabilities 只能声明已知能力")
		}
		// Style pictures are reference inputs: a 3D geometry or motion
		// capability has no style reference slot, so only image and
		// processing steps may declare one (#250).
		if domain, known := capabilityDomains[capability]; !known ||
			(domain != schemacontract.DomainImage && domain != schemacontract.DomainProcessing) {
			return errors.New("style_lock.reference_capabilities 只能声明图片或处理能力")
		}
		if referenceCapabilities[capability] {
			return errors.New("style_lock.reference_capabilities 不能重复声明同一能力")
		}
		referenceCapabilities[capability] = true
	}
	styleLockDeclared := runtime.HTTP.Routes.ChatSessionStyleLock != "" ||
		schema.StyleLock.MaximumTextCharacters > 0 || schema.StyleLock.MaximumReferenceImages > 0 ||
		schema.API.StyleLock.Enabled != "" || schema.API.StyleLock.Text != "" || schema.API.StyleLock.References != "" ||
		schema.API.ResponseFields.StyleLock != ""
	if styleLockDeclared && (schema.StyleLock.MaximumTextCharacters <= 0 || schema.StyleLock.MaximumReferenceImages <= 0 ||
		len(schema.StyleLock.DirectTaskCapabilities) == 0 ||
		strings.TrimSpace(schema.StyleLock.PromptTemplate) == "" || !strings.Contains(schema.StyleLock.PromptTemplate, "{style}") ||
		strings.TrimSpace(schema.StyleLock.EditScopeTemplate) == "" ||
		strings.TrimSpace(schema.API.StyleLock.Enabled) == "" ||
		strings.TrimSpace(schema.API.StyleLock.Text) == "" ||
		strings.TrimSpace(schema.API.StyleLock.References) == "" ||
		strings.TrimSpace(schema.API.Fields.StyleLockSkipped) == "" ||
		strings.TrimSpace(schema.API.ResponseFields.StyleLock) == "" ||
		strings.TrimSpace(schema.Presentation.Messages.StyleLockTextTooLong) == "" ||
		strings.TrimSpace(schema.Presentation.Messages.StyleLockReferenceLimit) == "" ||
		strings.TrimSpace(schema.Presentation.Messages.StyleLockReferenceInvalid) == "" ||
		strings.TrimSpace(schema.Presentation.Messages.StyleLockReferenceUnavailable) == "" ||
		strings.TrimSpace(schema.Presentation.Messages.StyleLockReferencesTrimmed) == "" ||
		len(schema.StyleLock.ReferenceCapabilities) == 0 ||
		strings.TrimSpace(schema.StyleLock.AgentContextTemplate) == "" ||
		!strings.Contains(schema.StyleLock.AgentContextTemplate, "{enabled}") ||
		!strings.Contains(schema.StyleLock.AgentContextTemplate, "{text}") ||
		!strings.Contains(schema.StyleLock.AgentContextTemplate, "{references}") ||
		strings.TrimSpace(schema.StyleLock.EnabledText) == "" ||
		strings.TrimSpace(schema.StyleLock.DisabledText) == "" ||
		strings.TrimSpace(schema.StyleLock.ReferencePurposeTemplate) == "" ||
		strings.TrimSpace(schema.StyleLock.SkipNote) == "" ||
		strings.TrimSpace(schema.StyleLock.UnsupportedNote) == "") {
		return errors.New("style_lock 的上限、字段与错误文案必须完整")
	}
	if sources := schema.StyleLock.Sources; len(sources) > 0 {
		allowed := map[string]bool{"project": true, "session": true, "off": true}
		seen := make(map[string]bool, len(sources))
		for _, source := range sources {
			if !allowed[source] || seen[source] {
				return fmt.Errorf("style_lock.sources 只能是 project/session/off 且不重复: %q", source)
			}
			seen[source] = true
		}
		if !seen["project"] || !seen["session"] || !seen["off"] {
			return errors.New("style_lock.sources 必须声明 project、session 与 off 三态")
		}
	}
	if template := schema.StyleLock.ProjectPromptTemplate; template != "" &&
		(!strings.Contains(template, "{style}") || !strings.Contains(template, "{project}")) {
		return errors.New("style_lock.project_prompt_template 必须包含 {style} 与 {project}")
	}
	if err := validatePendingApprovals(schema.PendingApprovals); err != nil {
		return err
	}
	if err := validateThreeView(schema.ThreeView, schema.Skills.Recipes, schema.Tools, schema.AgentTools); err != nil {
		return err
	}
	if err := validateDSMLSpellings(schema.DSML); err != nil {
		return err
	}
	if err := validateInputGapTexts(schema.Presentation.Messages); err != nil {
		return err
	}
	if err := validateToolRoundLimitPending(schema.Presentation.Messages); err != nil {
		return err
	}
	if err := validateThinkingStatus(schema.Stream.EventTypes, schema.Presentation.Status); err != nil {
		return err
	}
	if reasons := schema.Presentation.Messages.ContentRejectedReasons; reasons != "" && !strings.Contains(reasons, "{reasons}") {
		return errors.New("presentation.content_rejected_reasons 须包含 {reasons}（#35）")
	}
	for reason, label := range schema.Presentation.Messages.ContentRejectedReasonLabels {
		if reason == "" || reason != strings.ToLower(reason) || strings.ContainsAny(reason, "-/") || strings.TrimSpace(label) == "" {
			return fmt.Errorf("presentation.content_rejected_reason_labels[%q] 须为小写、以 _ 分隔的原因并带文案（#35）", reason)
		}
	}
	for class, message := range schema.Presentation.Messages.GenerationFailedByClass {
		if !schemacontract.FailureClass(class).Valid() || strings.TrimSpace(message) == "" {
			return fmt.Errorf("presentation.generation_failed_by_class[%q] 必须是已声明的失败分类并带文案", class)
		}
	}
	for class, message := range schema.Presentation.Messages.GenerationFailedMeteredByClass {
		if !schemacontract.FailureClass(class).Valid() || strings.TrimSpace(message) == "" {
			return fmt.Errorf("presentation.generation_failed_metered_by_class[%q] 必须是已声明的失败分类并带文案", class)
		}
	}
	if !strings.Contains(schema.Presentation.Messages.CanvasSourceToolRefused, "{tools}") {
		return errors.New("presentation.canvas_source_tool_refused 必须包含 {tools}")
	}
	for _, tool := range schema.Tools {
		if tool.OfferingArgument != "" && !strings.Contains(schema.Presentation.Messages.OfferingChoiceUnavailable, "{choices}") {
			return errors.New("presentation.offering_choice_unavailable 必须包含 {choices}")
		}
		if tool.OfferingFromSource != "" && strings.TrimSpace(schema.Presentation.Messages.SourceOfferingUnpaired) == "" {
			return errors.New("presentation.source_offering_unpaired 不能为空")
		}
		if tool.OfferingFromSource != "" && strings.TrimSpace(schema.Presentation.Messages.SourceOfferingCanvas) == "" {
			return errors.New("presentation.source_offering_canvas 不能为空")
		}
		if tool.OfferingFromSource != "" && !strings.Contains(schema.Presentation.Messages.SourceOfferingCandidates, "{handles}") {
			return errors.New("presentation.source_offering_candidates 必须包含 {handles}")
		}
	}
	if strings.TrimSpace(schema.Presentation.Messages.InvalidReferenceIndices) == "" {
		return errors.New("presentation.invalid_reference_indices 不能为空")
	}
	if strings.TrimSpace(schema.Presentation.Messages.ReferenceIndexOutOfRangeTemplate) == "" {
		return errors.New("presentation.reference_index_out_of_range_template 不能为空")
	}
	for _, placeholder := range []string{"{index}", "{count}"} {
		if !strings.Contains(schema.Presentation.Messages.ReferenceIndexOutOfRangeTemplate, placeholder) {
			return fmt.Errorf("presentation.reference_index_out_of_range_template 缺少 %s", placeholder)
		}
	}
	promptEnhance := schema.API.PromptEnhance
	if promptEnhance.Temperature < 0 || promptEnhance.Temperature > 2 {
		return errors.New("api.prompt_enhance.temperature 必须在 0 到 2 之间")
	}
	if len(promptEnhance.Profiles) == 0 ||
		len(promptEnhance.CapabilityProfiles) == 0 {
		return errors.New("api.prompt_enhance 必须声明 profile 和 capability 映射")
	}
	if _, exists := promptEnhance.Profiles[promptEnhance.DefaultProfile]; !exists {
		return fmt.Errorf(
			"api.prompt_enhance.default_profile 引用了未知 profile %q",
			promptEnhance.DefaultProfile,
		)
	}
	for capability, profileID := range promptEnhance.CapabilityProfiles {
		if !capability.Valid() {
			return fmt.Errorf(
				"api.prompt_enhance capability 无效: %q",
				capability,
			)
		}
		if _, exists := promptEnhance.Profiles[profileID]; !exists {
			return fmt.Errorf(
				"api.prompt_enhance capability %q 引用了未知 profile %q",
				capability,
				profileID,
			)
		}
	}
	for profileID, profile := range promptEnhance.Profiles {
		if strings.TrimSpace(profileID) == "" ||
			strings.TrimSpace(profile.SystemPrompt) == "" ||
			strings.TrimSpace(profile.LengthRulePlaceholder) == "" ||
			strings.TrimSpace(profile.BoundedLengthRule) == "" ||
			strings.TrimSpace(profile.UnboundedLengthRule) == "" ||
			strings.TrimSpace(profile.MaxLengthPlaceholder) == "" ||
			strings.TrimSpace(profile.PromptField) == "" ||
			!strings.Contains(
				profile.SystemPrompt,
				profile.LengthRulePlaceholder,
			) ||
			!strings.Contains(
				profile.BoundedLengthRule,
				profile.MaxLengthPlaceholder,
			) {
			return fmt.Errorf(
				"api.prompt_enhance profile %q 的模板或字段配置无效",
				profileID,
			)
		}
	}
	if schema.State.PlaceholderStaleMilliseconds <= 0 ||
		schema.State.FailedPromptPreviewChars <= 0 {
		return errors.New("state 的超时和 prompt 预览长度必须为正数")
	}
	if schema.Frontend.ConnectionTimeoutMilliseconds <= 0 ||
		schema.Frontend.FallbackPollMilliseconds <= 0 ||
		schema.Frontend.ReconnectCheckDelayMilliseconds <= 0 ||
		schema.Frontend.TextareaMaximumHeightPixels <= 0 ||
		schema.Frontend.DownloadFilenameMaxCharacters <= 0 ||
		schema.Frontend.DownloadTaskIDCharacters <= 0 ||
		schema.Frontend.ProgressMaximum <= 0 ||
		schema.Frontend.HandlePreviewDelayMilliseconds <= 0 ||
		schema.Frontend.HandlePreviewMaximumPixels <= 0 {
		return errors.New("chat.frontend 数值配置必须为正数")
	}
	if list := schema.API.SessionList; list.DefaultPageSize <= 0 ||
		list.MaximumPageSize < list.DefaultPageSize ||
		runtime.Frontend.SessionMenuPageSize > list.MaximumPageSize {
		return errors.New("api.session_list 的默认页大小必须为正数且不超过上限，session_menu_page_size 不能超过上限")
	}
	if list := schema.API.MessageList; list.DefaultPageSize <= 0 ||
		list.MaximumPageSize < list.DefaultPageSize ||
		runtime.Frontend.ChatMessagePageSize > list.MaximumPageSize {
		return errors.New("api.message_list 的默认页大小必须为正数且不超过上限，chat_message_page_size 不能超过上限")
	}
	if schema.Agent.TitleMaxChars <= 0 || schema.Agent.TitleTimeoutSeconds <= 0 {
		return errors.New("agent.title_max_characters 与 title_timeout_seconds 必须为正数")
	}
	if !strings.Contains(schema.Agent.TitlePrompt, "{content}") ||
		!strings.Contains(schema.Agent.TitlePrompt, "{maximum}") {
		return errors.New("agent.title_prompt 必须包含 {content} 与 {maximum}")
	}
	if schema.Agent.RequestTimeoutSeconds <= 0 ||
		schema.Agent.ScannerMaxBytes <= 0 ||
		schema.Agent.ErrorBodyMaxBytes <= 0 {
		return errors.New("agent 的 timeout 和响应限制必须为正数")
	}
	if schema.Agent.ConsentWaitTimeoutSeconds <= 0 {
		return errors.New("agent 的 consent 等待时长必须为正数")
	}
	if schema.Agent.MaxGenerationCallsPerTurn <= 0 {
		return errors.New("agent 的 max_generation_calls_per_turn 必须为正数")
	}
	if schema.Agent.MaxListedModels <= 0 {
		return errors.New("agent 的 max_listed_models 必须为正数")
	}
	for name, value := range map[string]string{
		"consent_approved_content":  schema.Agent.ConsentApprovedContent,
		"consent_denied_content":    schema.Agent.ConsentDeniedContent,
		"consent_waiting_content":   schema.Agent.ConsentWaitingContent,
		"consent_duplicate_content": schema.Agent.ConsentDuplicateContent,
		"consent_blocked_content":   schema.Agent.ConsentBlockedContent,
		"consent_required_content":  schema.Agent.ConsentRequiredContent,
		"consent_limit_content":     schema.Agent.ConsentLimitContent,
		"consent_reconcile_content": schema.Agent.ConsentReconcileContent,
	} {
		if value == "" {
			return fmt.Errorf("agent 的 %s 不能为空", name)
		}
	}
	if value := schema.Agent.ConsentRechargeContent; value != "" && !strings.Contains(value, "{task_id}") {
		return errors.New("agent 的 consent_recharge_content 必须包含 {task_id}")
	}
	if err := validateToolCallBudget(schema.Agent, runtime); err != nil {
		return err
	}
	if err := validateBackgroundWait(schema, runtime); err != nil {
		return err
	}
	if limit := runtime.HTTP.TaskAdmission.PerOwnerMaxInflightTasks; schema.Agent.MaxParallelGenerationSubmissions < 1 ||
		schema.Agent.MaxParallelGenerationSubmissions > limit {
		return fmt.Errorf(
			"agent 的 max_parallel_generation_submissions 必须在 1..%d（http.task_admission.per_owner_max_inflight_tasks）之间",
			limit,
		)
	}
	if schema.Agent.WorkerCount <= 0 || schema.Agent.QueueSize <= 0 {
		return errors.New("agent 的 worker_count 和 queue_size 必须为正数")
	}
	if err := validateContextCompaction(schema.ContextCompaction); err != nil {
		return err
	}
	if schema.ContextCompaction.Enabled {
		status, messages := schema.Presentation.Status, schema.Presentation.Messages
		for name, text := range map[string]string{
			"presentation.status.compacting":               status.Compacting,
			"presentation.status.compaction_failed":        status.CompactionFailed,
			"presentation.status.compacted":                status.Compacted,
			"presentation.messages.compaction_turn_active": messages.CompactionTurnActive,
			"presentation.messages.compaction_nothing":     messages.CompactionNothing,
			"presentation.messages.compaction_failed":      messages.CompactionFailed,
			"presentation.messages.compaction_unavailable": messages.CompactionUnavailable,
			"presentation.messages.reasoning_unavailable":  messages.ReasoningUnavailable,
		} {
			if strings.TrimSpace(text) == "" {
				return fmt.Errorf("context_compaction 开启时须声明 %s", name)
			}
		}
	}
	if runtime.HTTP.Routes.ChatSessionModel != "" {
		fields, messages := schema.API.ResponseFields, schema.Presentation.Messages
		for name, value := range map[string]string{
			"api.fields.chat_model":                        schema.API.Fields.ChatModel,
			"api.response_fields.chat_model":               fields.ChatModel,
			"api.response_fields.chat_model_chosen":        fields.ChatModelChosen,
			"api.response_fields.chat_model_default":       fields.ChatModelDefault,
			"api.response_fields.chat_model_fallback":      fields.ChatModelFallback,
			"api.response_fields.chat_models":              fields.ChatModels,
			"api.response_fields.chat_model_name":          fields.ChatModelName,
			"presentation.messages.chat_model_unavailable": messages.ChatModelUnavailable,
			"presentation.messages.chat_model_invalid":     messages.ChatModelInvalid,
		} {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("chat_session_model 路由已声明时须声明 %s", name)
			}
		}
	}
	if setting := schema.Agent.ContextWindowSetting; setting != "" {
		if _, exists := runtime.Configuration.Settings[setting]; !exists {
			return fmt.Errorf("agent.context_window_setting 引用了未知运行设置 %q", setting)
		}
	}
	if thinking := schema.Agent.Thinking; thinking != nil {
		if err := validateChatThinkingProfiles(thinking); err != nil {
			return err
		}
	}
	for _, setting := range []string{
		schema.Agent.BaseURLSetting,
		schema.Agent.APIKeySetting,
		schema.Agent.ModelSetting,
	} {
		if _, exists := runtime.Configuration.Settings[setting]; !exists {
			return fmt.Errorf("agent 引用了未知运行设置 %q", setting)
		}
	}
	if schema.Context.MaximumVisionImages <= 0 ||
		schema.Context.RecentImageLimit <= 0 ||
		schema.Context.MaximumMessages <= 0 ||
		schema.Context.ImageIndexBase <= 0 ||
		schema.Context.ToolPromptPreviewChars <= 0 ||
		!strings.Contains(schema.Context.AssetURLTemplate, "{task_id}") ||
		!strings.Contains(schema.Context.AssetURLTemplate, "{asset_id}") {
		return errors.New(
			"context 必须声明正数图片限制和 task_id/asset_id URL 模板",
		)
	}
	if schema.Context.VisionInlineMaxBytes <= 0 {
		return errors.New(
			"context.vision_inline_max_bytes 必须为正数（0 会静默关闭视觉内联）",
		)
	}
	if err := validateVisionDerivative(schema.Context.VisionDerivative); err != nil {
		return err
	}
	if schema.DSML.Enabled &&
		(!strings.Contains(schema.DSML.CallIDTemplate, "{index}") ||
			schema.DSML.TrueValue == schema.DSML.FalseValue) {
		return errors.New(
			"dsml.call_id_template 必须包含 index 且布尔字面量不能相同",
		)
	}
	if schema.Stream.RetryMilliseconds <= 0 ||
		schema.Stream.HeartbeatMilliseconds <= 0 ||
		schema.Stream.CleanupTTLMilliseconds <= 0 ||
		schema.Stream.StatusCheckMilliseconds <= 0 ||
		schema.Stream.SubscriberBufferSize <= 0 ||
		schema.Stream.WriteTimeoutMilliseconds <= 0 ||
		!strings.Contains(schema.Stream.IDFrameTemplate, "{id}") ||
		len(schema.Stream.EventPrefix) == 0 ||
		len(schema.Stream.EventSuffix) == 0 ||
		len(schema.Stream.HeartbeatFrame) == 0 ||
		len(schema.Stream.RetryFrameTemplate) == 0 ||
		len(schema.Stream.Headers) == 0 ||
		!strings.Contains(
			schema.Stream.RetryFrameTemplate,
			"{milliseconds}",
		) {
		return errors.New("stream 的时间、Headers 或 retry 模板无效")
	}
	if len(schema.Tools) == 0 {
		return errors.New("tools 不能为空")
	}
	if len(schema.Skills.Categories) == 0 {
		return errors.New("skills.categories 不能为空")
	}
	if schema.Skills.Creation.Route != "" || schema.Skills.Creation.MaxNameCharacters != 0 {
		if !strings.HasPrefix(schema.Skills.Creation.Route, "/") ||
			strings.HasPrefix(schema.Skills.Creation.Route, "//") ||
			strings.ContainsAny(schema.Skills.Creation.Route, "?#\\\r\n") ||
			schema.Skills.Creation.MaxNameCharacters <= 0 ||
			strings.TrimSpace(schema.Skills.Creation.RecommendedSize) == "" {
			return errors.New("skills.creation 配置无效")
		}
		if item := schema.Skills.Creation.ItemRoute; item != "" && item != schema.Skills.Creation.Route+"/{skillID}" {
			return errors.New("skills.creation.item_route 必须是 route 后接 /{skillID}")
		}
	}
	if err := validateSkillRecipes(schema.Skills.Recipes); err != nil {
		return err
	}
	templateFields := schema.Skills.Execution.PromptTemplateFields
	if templateFields.UserInput == templateFields.SkillPrompt {
		return errors.New("skill prompt template fields 不能相同")
	}
	if len(schema.Skills.Execution.PromptTemplates) == 0 {
		return errors.New("skill prompt templates 不能为空")
	}
	for capability, template := range schema.Skills.Execution.PromptTemplates {
		if !capability.Valid() ||
			!strings.Contains(template, templateFields.UserInput) ||
			!strings.Contains(template, templateFields.SkillPrompt) {
			return fmt.Errorf(
				"skill prompt template %q 必须声明有效 capability 并包含配置字段",
				capability,
			)
		}
	}
	if len(schema.Skills.Execution.ParameterOverrides) == 0 {
		return errors.New("skill parameter overrides 不能为空")
	}
	for semantic, value := range schema.Skills.Execution.ParameterOverrides {
		if !semantic.Valid() || len(value) == 0 || !json.Valid(value) {
			return fmt.Errorf(
				"skill parameter override %q 无效",
				semantic,
			)
		}
	}
	categoryIDs := map[string]struct{}{}
	for index, category := range schema.Skills.Categories {
		if strings.TrimSpace(category.ID) == "" ||
			strings.TrimSpace(category.Label) == "" {
			return fmt.Errorf("skills.categories[%d] 配置不完整", index)
		}
		if _, exists := categoryIDs[category.ID]; exists {
			return fmt.Errorf("skill category 重复: %q", category.ID)
		}
		categoryIDs[category.ID] = struct{}{}
		for _, capability := range category.HiddenCapabilities {
			if !capability.Valid() {
				return fmt.Errorf(
					"skills.categories[%d] 引用了未知 capability %q",
					index,
					capability,
				)
			}
		}
	}
	toolNames := map[string]struct{}{}
	for index, agentTool := range schema.AgentTools {
		if strings.TrimSpace(agentTool.Name) == "" ||
			strings.TrimSpace(agentTool.Description) == "" ||
			len(agentTool.Parameters) == 0 ||
			!json.Valid(agentTool.Parameters) {
			return fmt.Errorf("chat.agent_tools[%d] 声明无效", index)
		}
		if err := validateChatToolExecution(agentTool.Name, agentTool.Execution, nil, nil, nil, nil); err != nil {
			return fmt.Errorf("chat.agent_tools[%d] execution: %w", index, err)
		}
		if _, exists := toolNames[agentTool.Name]; exists {
			return fmt.Errorf("tool 名称重复: %q", agentTool.Name)
		}
		toolNames[agentTool.Name] = struct{}{}
		if agentTool.Execution.ImplementationKey == "history.images" {
			if err := validateHistoryImageLookup(schema.Context); err != nil {
				return fmt.Errorf("chat.agent_tools[%d]: %w", index, err)
			}
		}
		if agentTool.Execution.ImplementationKey == "task.refetch" {
			if err := validateRefetchToolTexts(schema.Agent); err != nil {
				return fmt.Errorf("chat.agent_tools[%d]: %w", index, err)
			}
		}
	}
	if err := validateSystemSkills(schema.Skills, toolNames); err != nil {
		return err
	}
	if err := validateModelReview(schema, capabilityPresentations); err != nil {
		return err
	}
	if err := validateImageReview(schema); err != nil {
		return err
	}
	if err := validateSelfCheck(schema); err != nil {
		return err
	}
	if err := validateImageMask(schema); err != nil {
		return err
	}
	if err := validateWorkLedger(schema); err != nil {
		return err
	}
	if err := validateVisualAnchors(schema); err != nil {
		return err
	}
	if err := validateChatPreferences(schema, content); err != nil {
		return err
	}
	for index, tool := range schema.Tools {
		if tool.RequireUniqueOffering && (!tool.IgnoreUserModel || tool.RequireUserModel || tool.CrossDomainModelFallback) {
			return fmt.Errorf("tools[%d] require_unique_offering requires independent model selection", index)
		}
		if tool.CrossDomainModelFallback {
			selectsModel := tool.RequireUserModel || tool.ModelArgument != ""
			if !selectsModel || len(tool.Execution.Domains) != 1 ||
				strings.TrimSpace(tool.ResultFields.Model) == "" || strings.TrimSpace(tool.ResultFields.ModelFallback) == "" {
				return fmt.Errorf("tools[%d] cross_domain_model_fallback 需要 require_user_model 或 model_argument、单一 domain 与 result_fields.model/model_fallback", index)
			}
		}
		switch tool.ResultAttachmentKind {
		case "", "image", "model", "none":
		default:
			return fmt.Errorf("tools[%d] result_attachment_kind 无效: %q", index, tool.ResultAttachmentKind)
		}
		if (tool.ResultAttachmentKind == "none") != (len(tool.ResultAssetRoles) == 0) {
			return fmt.Errorf("tools[%d] only result_attachment_kind none requires empty result_asset_roles", index)
		}
		acceptReferences := tool.AcceptReferenceImages == nil || *tool.AcceptReferenceImages
		// #266: every tool says what it does on a canvas-sourced turn. Only a
		// generation tool that reads images can bind the snapshot.
		switch tool.CanvasSource {
		case "bind":
			if !acceptReferences || tool.Processing != nil {
				return fmt.Errorf("tools[%d] canvas_source bind 需要读取参考图的生成工具", index)
			}
		case "refuse":
			if tool.Processing != nil {
				return fmt.Errorf("tools[%d] canvas_source refuse 不适用于加工工具", index)
			}
		case "ignore":
		default:
			return fmt.Errorf("tools[%d] canvas_source 必须是 bind、refuse 或 ignore: %q", index, tool.CanvasSource)
		}
		// #296: a reference sets the output size only for a generation tool
		// that sends reference pictures, and its note needs both templates.
		// #244: every tool says what it does with the frozen selection mask of
		// a canvas turn. Only a generation tool that declares the mask mapping
		// and its tool argument can bind it; anything else must ignore it.
		switch tool.SelectionMask {
		case "bind":
			if !acceptReferences || tool.Processing != nil ||
				tool.ArgumentMappings["mask"] != "mask" || tool.OverrideMappings["mask"] != "mask" ||
				!toolSchemaDeclaresParameter(tool, "mask") {
				return fmt.Errorf("tools[%d] selection_mask bind 需要读取参考图、声明 mask 参数映射且不加工模型的生成工具", index)
			}
		case "", "ignore":
			// A Catalog written before #244 declares no mask binding; it
			// keeps its old whole-image behavior. The shipped Catalog declares
			// one on every tool (checked by its own tests).
		default:
			return fmt.Errorf("tools[%d] selection_mask 必须是 bind 或 ignore: %q", index, tool.SelectionMask)
		}
		switch tool.ReferenceSize {
		case "":
		case "single":
			if !acceptReferences || tool.Processing != nil {
				return fmt.Errorf("tools[%d] reference_size single 需要读取参考图的生成工具", index)
			}
			for name, template := range map[string]string{
				"reference_output_size_template":         schema.Context.ReferenceOutputSizeTemplate,
				"reference_output_size_nearest_template": schema.Context.ReferenceOutputSizeNearestTemplate,
			} {
				if !strings.Contains(template, "{reference}") || !strings.Contains(template, "{size}") {
					return fmt.Errorf("tools[%d] reference_size single 需要 context.%s 包含 {reference} 与 {size}", index, name)
				}
			}
		default:
			return fmt.Errorf("tools[%d] reference_size 只能为空或 single: %q", index, tool.ReferenceSize)
		}
		// #272: only a tool that binds the snapshot has its output size set by
		// the canvas; #296: or one whose single reference sets it.
		if tool.ResultFields.SizeNote != "" && tool.CanvasSource != "bind" && tool.ReferenceSize == "" {
			return fmt.Errorf("tools[%d] result_fields.size_note 只适用于 canvas_source bind 或 reference_size single 的工具", index)
		}
		if !acceptReferences {
			if tool.DefaultToSelection || tool.Capability != tool.FallbackCapability ||
				(tool.Processing != nil && tool.Processing.ReferenceArgument != "") {
				return fmt.Errorf("tools[%d] reference-disabled tool cannot select references or switch capabilities", index)
			}
			for _, mapping := range []map[string]string{tool.ArgumentMappings, tool.OverrideMappings} {
				for argument, semantic := range mapping {
					for _, reference := range []string{schema.Context.ToolReferenceArgument, schema.Context.ReferenceHandlesArgument, "reference-indices", "reference-handles"} {
						if reference != "" && (argument == reference || semantic == reference) {
							return fmt.Errorf("tools[%d] reference-disabled tool declares reference mapping", index)
						}
					}
				}
			}
		}
		if strings.TrimSpace(tool.Name) == "" ||
			strings.TrimSpace(tool.Description) == "" ||
			!tool.Capability.Valid() ||
			!tool.FallbackCapability.Valid() ||
			strings.TrimSpace(tool.PendingCapability) == "" ||
			len(tool.Parameters) == 0 ||
			!json.Valid(tool.Parameters) ||
			strings.TrimSpace(tool.IdempotencyKeyTemplate) == "" ||
			!strings.Contains(tool.IdempotencyKeyTemplate, "{session_id}") ||
			!strings.Contains(tool.IdempotencyKeyTemplate, "{call_id}") ||
			strings.TrimSpace(tool.ResultFields.Success) == "" ||
			strings.TrimSpace(tool.ResultFields.TaskID) == "" ||
			strings.TrimSpace(tool.ResultFields.ImageURLs) == "" ||
			strings.TrimSpace(tool.ResultFields.Error) == "" {
			return fmt.Errorf("tools[%d] 配置不完整", index)
		}
		var stepCapabilities []schemacontract.Capability
		if tool.Processing != nil {
			steps, err := validateChatToolProcessing(tool, capabilityDomains)
			if err != nil {
				return fmt.Errorf("tools[%d] processing: %w", index, err)
			}
			stepCapabilities = steps
		} else if len(tool.ArgumentMappings) == 0 ||
			(len(tool.OverrideMappings) == 0 && !(acceptReferences && tool.DefaultToSelection && tool.RequireUniqueOffering &&
				len(tool.ArgumentMappings) == 1 && tool.ArgumentMappings[schema.Context.ToolReferenceArgument] == "reference-indices")) ||
			(acceptReferences && (!strings.Contains(tool.PromptWithReferenceTemplate, "{prompt}") ||
				!strings.Contains(tool.PromptWithPreviousTemplate, "{previous_prompt}") ||
				!strings.Contains(tool.PromptWithPreviousTemplate, "{prompt}"))) {
			return fmt.Errorf("tools[%d] 配置不完整", index)
		}
		if err := validateReferencePurposes(tool, acceptReferences, schema.Context); err != nil {
			return fmt.Errorf("tools[%d]: %w", index, err)
		}
		if err := validateAnchorArgument(tool, acceptReferences); err != nil {
			return fmt.Errorf("tools[%d]: %w", index, err)
		}
		if note := tool.ReferenceCapacityNote; note != (ChatToolReferenceCapacityNote{}) && (!acceptReferences ||
			strings.TrimSpace(note.Single) == "" || !strings.Contains(note.Multiple, "{count}") ||
			(note.NoPrompt != "" && strings.TrimSpace(note.NoPrompt) == "") ||
			(note.Settings != "" && !strings.Contains(note.Settings, "{settings}"))) {
			return fmt.Errorf("tools[%d] reference_capacity_note 需要接受参考图，single 不能为空，multiple 须含 {count}，settings 须含 {settings}", index)
		}
		if err := validateChatToolExecution(tool.Name, tool.Execution, &tool.Capability, &tool.FallbackCapability, capabilityDomains, stepCapabilities); err != nil {
			return fmt.Errorf("tools[%d] execution: %w", index, err)
		}
		if _, exists := toolNames[tool.Name]; exists {
			return fmt.Errorf("tool 名称重复: %q", tool.Name)
		}
		toolNames[tool.Name] = struct{}{}
	}
	if err := validateCraftPrompts(schema.Agent.CraftPrompts, toolNames, maxPromptCharacters, schema.Model3DUsage.Brief.Fields); err != nil {
		return err
	}
	if err := validateCraftPromptOfferings(schema.Agent.CraftPrompts, capabilityDomains); err != nil {
		return err
	}
	if err := validateOfferingKnowledgeSchema(schema.Agent.OfferingKnowledge); err != nil {
		return err
	}
	if err := validatePromptReportSchema(schema.Agent.PromptReport); err != nil {
		return err
	}
	return nil
}

// processingFailureCategories and processingFailureActions mirror the runtime
// vocabulary in internal/generation (#251); a catalog test keeps them aligned.
var (
	processingFailureCategories = []string{"input_invalid", "parameter_invalid", "upstream_capacity", "upstream_refused", "internal"}
	processingFailureActions    = []string{"retry", "retry_later", "fix_input", "fix_parameters", "ask_user", "contact_admin"}
)

// validateChatProcessing checks the server-only processing wording and the
// revision bounds the Agent's tool relies on (#251). It never reaches the
// browser, so it has no public projection to align.
func validateChatProcessing(processing ChatProcessingSchema) error {
	failures := processing.Failures
	// A historical Catalog declares no processing block; it stays readable.
	if len(failures.Categories) == 0 && len(failures.Actions) == 0 && len(failures.Advice) == 0 &&
		strings.TrimSpace(failures.CheckpointFailed) == "" && strings.TrimSpace(failures.RevisionApplied) == "" &&
		strings.TrimSpace(failures.ReasonRequired) == "" {
		return nil
	}
	for _, category := range processingFailureCategories {
		if strings.TrimSpace(failures.Categories[category]) == "" ||
			strings.TrimSpace(failures.Advice[category]) == "" {
			return fmt.Errorf("chat.processing.failures 缺少类别或建议动作 %q", category)
		}
	}
	for _, action := range processingFailureActions {
		if strings.TrimSpace(failures.Actions[action]) == "" {
			return fmt.Errorf("chat.processing.failures.actions 缺少动作 %q", action)
		}
	}
	if strings.TrimSpace(failures.CheckpointFailed) == "" ||
		strings.TrimSpace(failures.RevisionApplied) == "" ||
		strings.TrimSpace(failures.ReasonRequired) == "" {
		return errors.New("chat.processing.failures 的检查点与修订文案不能为空")
	}
	return nil
}

func validateChatToolExecution(name string, execution ChatToolExecutionSchema, capability, fallback *schemacontract.Capability, capabilityDomains map[schemacontract.Capability]schemacontract.CapabilityDomain, stepCapabilities []schemacontract.Capability) error {
	// domains lists the workspaces a control tool must be offered in.
	knownControl := map[string]struct {
		implementation, effect string
		domains                []string
	}{
		"set_params":              {"params.set", "SESSION_WRITE", []string{"image", "model-3d"}},
		"list_offerings":          {"catalog.list", "READ", []string{"image", "model-3d"}},
		"get_task":                {"task.read", "READ", []string{"image", "model-3d"}},
		"observe_task":            {"task.read", "READ", []string{"image", "model-3d"}},
		"request_consent":         {"consent.request", "SESSION_WRITE", []string{"image", "model-3d"}},
		"create_three_view":       {"skill.prepare", "PAID_TASK", []string{"image"}},
		"list_session_models":     {"model.list", "READ", []string{"model-3d"}},
		"get_processing_plan":     {"processing.read", "READ", []string{"model-3d"}},
		"revise_processing_plan":  {"processing.revise", "SESSION_WRITE", []string{"model-3d"}},
		"update_session_notes":    {"session.notes", "SESSION_WRITE", []string{"image", "model-3d"}},
		"update_production_brief": {"session.brief", "SESSION_WRITE", []string{"image", "model-3d"}},
		"propose_production_plan": {"plan.propose", "SESSION_WRITE", []string{"image", "model-3d"}},
		"ask_choice":              {"choice.ask", "SESSION_WRITE", []string{"image", "model-3d"}},
		"find_history_images":     {"history.images", "READ", []string{"image", "model-3d"}},
		"recover_task_result":     {"task.refetch", "SESSION_WRITE", []string{"image", "model-3d"}},
		"inspect_image":           {"image.inspect", "READ", []string{"image", "model-3d"}},
		"create_selection_mask":   {"image.mask", "READ", []string{"image"}},
		"inspect_model":           {"model.inspect", "READ", []string{"model-3d"}},
		"render_model_views":      {"model.render", "READ", []string{"model-3d"}},
		"compare_models":          {"model.compare", "READ", []string{"model-3d"}},
		"compare_model_reference": {"model.compare_reference", "READ", []string{"model-3d"}},
		"record_self_check":       {"self_check.record", "SESSION_WRITE", []string{"model-3d"}},
		"get_production_step":     {"ledger.read", "READ", []string{"image", "model-3d"}},
		"set_visual_anchor":       {"anchor.write", "SESSION_WRITE", []string{"image", "model-3d"}},
		"list_project_assets":     {"project.list", "READ", []string{"image", "model-3d"}},
	}
	var controlDomains []string
	var expectedEffect string
	if capability != nil {
		if execution.ImplementationKey != "generation.execute" {
			return fmt.Errorf("generation tool %q implementation_key 必须为 generation.execute", name)
		}
		expectedEffect = "PAID_TASK"
	} else {
		expected, ok := knownControl[name]
		if !ok || execution.ImplementationKey != expected.implementation {
			return fmt.Errorf("未知控制工具或 implementation_key 不匹配: %q/%q", name, execution.ImplementationKey)
		}
		expectedEffect = expected.effect
		controlDomains = expected.domains
	}
	if len(execution.Effects) != 1 || execution.Effects[0] != expectedEffect {
		return fmt.Errorf("tool %q effects 必须为 [%s]", name, expectedEffect)
	}
	if execution.Scope != "session" {
		return fmt.Errorf("tool %q scope 必须为 session", name)
	}
	if execution.Consent != "human" && execution.Consent != "none" {
		return fmt.Errorf("tool %q consent 必须为 human 或 none", name)
	}
	if expectedEffect == "PAID_TASK" && execution.Consent != "human" {
		return fmt.Errorf("paid tool %q 必须要求 human consent", name)
	}
	if execution.Idempotency != "required" && execution.Idempotency != "none" {
		return fmt.Errorf("tool %q idempotency 必须为 required 或 none", name)
	}
	if err := validateToolAgentScope(name, execution); err != nil {
		return err
	}
	if execution.Cancellation != "task" && execution.Cancellation != "none" {
		return fmt.Errorf("tool %q cancellation 必须为 task 或 none", name)
	}
	if expectedEffect == "PAID_TASK" && (execution.Idempotency != "required" || execution.Cancellation != "task") {
		return fmt.Errorf("paid tool %q 必须支持幂等和任务取消", name)
	}
	if len(execution.Domains) == 0 {
		return fmt.Errorf("tool %q domains 不能为空", name)
	}
	domains := map[string]bool{}
	for _, domain := range execution.Domains {
		if domain != "image" && domain != "model-3d" || domains[domain] {
			return fmt.Errorf("tool %q domain 无效或重复: %q", name, domain)
		}
		domains[domain] = true
	}
	if capability == nil {
		if len(domains) != len(controlDomains) {
			return fmt.Errorf("control tool %q domains 必须为 %v", name, controlDomains)
		}
		for _, domain := range controlDomains {
			if !domains[domain] {
				return fmt.Errorf("control tool %q domains 必须为 %v", name, controlDomains)
			}
		}
	} else if len(domains) != 1 {
		return fmt.Errorf("generation tool %q 必须只声明一个 domain", name)
	}
	if capability == nil {
		if len(execution.Capabilities) != 0 {
			return fmt.Errorf("control tool %q 不得声明 generation capabilities", name)
		}
		return nil
	}
	if len(execution.Capabilities) == 0 {
		return fmt.Errorf("generation tool %q capabilities 不能为空", name)
	}
	want := map[schemacontract.Capability]bool{*capability: true, *fallback: true}
	for _, step := range stepCapabilities {
		want[step] = true
	}
	got := map[schemacontract.Capability]bool{}
	for _, item := range execution.Capabilities {
		if !item.Valid() || got[item] {
			return fmt.Errorf("generation tool %q capability 无效或重复: %q", name, item)
		}
		got[item] = true
	}
	if len(got) != len(want) {
		return fmt.Errorf("generation tool %q capabilities 未覆盖 capability/fallback", name)
	}
	for item := range want {
		if !got[item] {
			return fmt.Errorf("generation tool %q capabilities 未覆盖 %q", name, item)
		}
		_, exists := capabilityDomains[item]
		if !exists {
			return fmt.Errorf("generation tool %q capability %q 未在 capability catalog 登记", name, item)
		}
	}
	return nil
}

// validateApprovalPolicy rejects an approval policy that could silently widen
// automatic approval: unknown modes or capabilities, a missing manual mode,
// or a default mode the Schema does not offer.
func validateApprovalPolicy(policy ChatApprovalPolicySchema, capabilities []CapabilityPresentation) error {
	if len(policy.Modes) == 0 {
		return errors.New("approval_policy.modes 不能为空")
	}
	known := map[string]bool{approvalModeAuto: true, approvalModeManual: true, approvalModeBudget: true}
	offered := make(map[string]bool, len(policy.Modes))
	for _, mode := range policy.Modes {
		if !known[mode] {
			return fmt.Errorf("approval_policy.modes 含未知模式 %q", mode)
		}
		if offered[mode] {
			return fmt.Errorf("approval_policy.modes 重复声明 %q", mode)
		}
		offered[mode] = true
	}
	if !offered[approvalModeManual] {
		return errors.New("approval_policy.modes 必须包含 manual")
	}
	if !offered[policy.DefaultMode] {
		return fmt.Errorf("approval_policy.default_mode %q 未在 modes 中声明", policy.DefaultMode)
	}
	declared := make(map[schemacontract.Capability]bool, len(capabilities))
	for _, capability := range capabilities {
		declared[capability.ID] = true
	}
	seen := make(map[schemacontract.Capability]bool, len(policy.ManualApprovalCapabilities))
	for _, capability := range policy.ManualApprovalCapabilities {
		if !declared[capability] {
			return fmt.Errorf("approval_policy.manual_approval_capabilities 引用了未知能力 %q", capability)
		}
		if seen[capability] {
			return fmt.Errorf("approval_policy.manual_approval_capabilities 重复声明 %q", capability)
		}
		seen[capability] = true
	}
	if err := validateApprovalPrompt(policy.Prompt, offered); err != nil {
		return err
	}
	// Historical Catalogs that do not offer budget mode stay readable; a
	// Catalog that offers it must declare a usable plan budget.
	if offered[approvalModeBudget] {
		return validateApprovalBudget(policy.Budget)
	}
	return nil
}

// validateApprovalBudget checks the production-plan bounds and texts (#103).
// The state IDs are fixed by internal/budget; the labels and messages are the
// product's words. A budget mode without a usable budget must not load.
func validateApprovalBudget(budget ChatApprovalBudgetSchema) error {
	if budget.MaxCredits <= 0 || budget.MaxSteps <= 0 ||
		budget.MaxTitleCharacters <= 0 || budget.MaxPurposeCharacters <= 0 {
		return errors.New("approval_policy.budget 的上限必须为正数")
	}
	// #251: the plan's acceptance/checkpoint/contingency bounds and templates.
	if budget.MaxAcceptanceCriteria <= 0 || budget.MaxChecksPerCheckpoint <= 0 ||
		budget.MaxContingencies < 0 || budget.MaxWaivedCheckpoints < 0 {
		return errors.New("approval_policy.budget 的验收/检查点/预案上限无效")
	}
	if strings.TrimSpace(budget.Acceptance.PurposeLine) == "" ||
		!strings.Contains(budget.Acceptance.PurposeLine, "{purpose}") ||
		strings.TrimSpace(budget.Acceptance.Report) == "" {
		return errors.New("approval_policy.budget.acceptance 的用途文案与报告指令不能为空")
	}
	for check, template := range budget.Acceptance.Criteria {
		if strings.TrimSpace(check) == "" || strings.TrimSpace(template) == "" {
			return errors.New("approval_policy.budget.acceptance.criteria 含空项")
		}
	}
	for check, template := range budget.Acceptance.RangeCriteria {
		if strings.TrimSpace(check) == "" || strings.TrimSpace(template) == "" ||
			budget.Acceptance.Criteria[check] == "" {
			return errors.New("approval_policy.budget.acceptance.range_criteria 含空项或没有对应的 criteria")
		}
	}
	if strings.TrimSpace(budget.ContextTemplate) == "" || strings.TrimSpace(budget.NoPlanState) == "" ||
		strings.TrimSpace(budget.NoPlanText) == "" {
		return errors.New("approval_policy.budget 的上下文文案不能为空")
	}
	// #120: amounts are listed per unit; the legacy bucket's name and the
	// separator between units are product copy, not code.
	if strings.TrimSpace(budget.LegacyUnitLabel) == "" || strings.TrimSpace(budget.UnitSeparator) == "" {
		return errors.New("approval_policy.budget 的 legacy_unit_label 与 unit_separator 不能为空")
	}
	for _, field := range []string{"{state}", "{limit}", "{spent}", "{reserved}", "{remaining}", "{plan}",
		"{acceptance}", "{checkpoints}", "{contingencies}", "{report}"} {
		if !strings.Contains(budget.ContextTemplate, field) {
			return fmt.Errorf("approval_policy.budget.context_template 缺少占位符 %s", field)
		}
	}
	// The role of each state follows what the server does with it (#120): a
	// role the browser acts on must not contradict the state machine, or an
	// active budget could be hidden or a refused plan shown as spendable.
	knownStates := map[string]string{
		"pending":   ChatApprovalBudgetRolePending,
		"active":    ChatApprovalBudgetRoleActive,
		"exhausted": ChatApprovalBudgetRoleEnded,
		"closed":    ChatApprovalBudgetRoleEnded,
		"denied":    ChatApprovalBudgetRoleDenied,
	}
	seen := make(map[string]bool, len(budget.States))
	for _, state := range budget.States {
		role, known := knownStates[state.ID]
		if !known || seen[state.ID] {
			return fmt.Errorf("approval_policy.budget.states 含未知或重复的状态 %q", state.ID)
		}
		if strings.TrimSpace(state.Label) == "" {
			return fmt.Errorf("approval_policy.budget.states[%s] 缺少 label", state.ID)
		}
		if state.Role != role {
			return fmt.Errorf("approval_policy.budget.states[%s] 的 role 必须是 %q", state.ID, role)
		}
		seen[state.ID] = true
	}
	for id := range knownStates {
		if !seen[id] {
			return fmt.Errorf("approval_policy.budget.states 缺少状态 %q", id)
		}
	}
	for name, text := range map[string]string{
		"approved": budget.Messages.Approved, "denied": budget.Messages.Denied,
		"expired": budget.Messages.Expired, "exhausted": budget.Messages.Exhausted,
	} {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("approval_policy.budget.messages.%s 不能为空", name)
		}
	}
	for _, field := range []string{"{title}", "{limit}", "{remaining}"} {
		if !strings.Contains(budget.Messages.Approved, field) {
			return fmt.Errorf("approval_policy.budget.messages.approved 缺少占位符 %s", field)
		}
	}
	// #134: approving a plan starts the turn that runs it.
	if strings.TrimSpace(budget.Continuation.Message) == "" || strings.TrimSpace(budget.Continuation.AgentTemplate) == "" {
		return errors.New("approval_policy.budget.continuation 的 message 与 agent_template 不能为空")
	}
	return nil
}

// validateApprovalPrompt checks the Agent-facing texts of the approval modes
// (#7): the template carries both placeholders, and every mode the policy
// offers has exactly one label and description, so the Agent is never given a
// blank or foreign mode.
func validateApprovalPrompt(prompt ChatApprovalPromptSchema, offered map[string]bool) error {
	if strings.TrimSpace(prompt.ContextTemplate) == "" {
		return errors.New("approval_policy.prompt.context_template 不能为空")
	}
	for _, field := range []string{"{mode}", "{description}"} {
		if !strings.Contains(prompt.ContextTemplate, field) {
			return fmt.Errorf("approval_policy.prompt.context_template 缺少占位符 %s", field)
		}
	}
	described := make(map[string]bool, len(prompt.Modes))
	for _, mode := range prompt.Modes {
		if !offered[mode.ID] || described[mode.ID] {
			return fmt.Errorf("approval_policy.prompt.modes 含未声明或重复的模式 %q", mode.ID)
		}
		described[mode.ID] = true
		if strings.TrimSpace(mode.Label) == "" || strings.TrimSpace(mode.Description) == "" {
			return fmt.Errorf("approval_policy.prompt.modes[%s] 缺少 label 或 description", mode.ID)
		}
	}
	for id := range offered {
		if !described[id] {
			return fmt.Errorf("approval_policy.prompt.modes 缺少模式 %q 的说明", id)
		}
	}
	return nil
}

// Approval modes the Schema may offer. approvalpolicy owns their meaning; the
// catalog only checks the declared names.
const (
	approvalModeAuto   = "auto"
	approvalModeManual = "manual"
	approvalModeBudget = "budget"
)

// validateChatToolProcessing checks that a processing tool's argument names
// are declared in its LLM-facing parameter schema and that it can only reach
// processing-domain capabilities. It returns the capabilities its Steps
// select so the execution contract can be matched against them.
func validateChatToolProcessing(tool ChatToolSchema, capabilityDomains map[schemacontract.Capability]schemacontract.CapabilityDomain) ([]schemacontract.Capability, error) {
	processing := tool.Processing
	var declared struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(tool.Parameters, &declared); err != nil || declared.Properties == nil {
		return nil, errors.New("parameters 必须是带 properties 的 JSON Schema 对象")
	}
	inParameters := func(label, argument string) error {
		if strings.TrimSpace(argument) == "" {
			return fmt.Errorf("%s 不能为空", label)
		}
		if _, exists := declared.Properties[argument]; !exists {
			return fmt.Errorf("%s %q 未在 parameters.properties 中声明", label, argument)
		}
		return nil
	}
	if err := inParameters("source_task_argument", processing.SourceTaskArgument); err != nil {
		return nil, err
	}
	used := map[string]string{processing.SourceTaskArgument: "source_task_argument"}
	claim := func(label, argument string) error {
		if previous, exists := used[argument]; exists {
			return fmt.Errorf("参数 %q 同时用作 %s 和 %s", argument, previous, label)
		}
		used[argument] = label
		return nil
	}
	for label, argument := range map[string]string{
		"source_asset_argument": processing.SourceAssetArgument,
		"prompt_argument":       processing.PromptArgument,
		"reference_argument":    processing.ReferenceArgument,
	} {
		if argument == "" {
			continue
		}
		if err := inParameters(label, argument); err != nil {
			return nil, err
		}
		if err := claim(label, argument); err != nil {
			return nil, err
		}
	}
	if argument := processing.SourceAssetsArgument; argument != "" {
		if processing.Plan != nil {
			return nil, errors.New("source_assets_argument 不能用于加工计划工具")
		}
		if err := inParameters("source_assets_argument", argument); err != nil {
			return nil, err
		}
		if err := claim("source_assets_argument", argument); err != nil {
			return nil, err
		}
	}
	for argument, parameter := range processing.ParameterArguments {
		if err := inParameters("parameter_arguments", argument); err != nil {
			return nil, err
		}
		if strings.TrimSpace(parameter) == "" {
			return nil, fmt.Errorf("parameter_arguments %q 的目标参数不能为空", argument)
		}
		if err := claim("parameter_arguments", argument); err != nil {
			return nil, err
		}
	}
	if argument := processing.DeliveryFormatArgument; argument != "" {
		if _, mapped := processing.ParameterArguments[argument]; !mapped || processing.StepArgument != "" || processing.Plan != nil {
			return nil, fmt.Errorf("delivery_format_argument %q 必须是单步工具 parameter_arguments 中的参数", argument)
		}
	}
	var steps []schemacontract.Capability
	if processing.Plan != nil {
		plan := processing.Plan
		if processing.StepArgument != "" || len(processing.ParameterArguments) != 0 ||
			processing.PromptArgument != "" || processing.ReferenceArgument != "" || len(processing.Steps) == 0 {
			return nil, errors.New("plan 不能声明单步参数映射且必须声明 steps")
		}
		if err := inParameters("steps_argument", plan.StepsArgument); err != nil {
			return nil, err
		}
		if err := claim("steps_argument", plan.StepsArgument); err != nil {
			return nil, err
		}
		if !slices.Contains(declared.Required, plan.StepsArgument) {
			return nil, errors.New("steps_argument 必须是必填参数")
		}
		if strings.TrimSpace(plan.CreatedContent) == "" || strings.TrimSpace(plan.WaitingContent) == "" || strings.TrimSpace(plan.NotFoundContent) == "" {
			return nil, errors.New("plan 结果文案不能为空")
		}
		// #251: the Agent revision's reason bound and waived-checkpoint limit.
		// A historical plan tool without the block falls back to the runtime
		// constants, so it stays readable.
		if plan.Revision.MaxReasonCharacters < 0 || plan.Revision.MaxWaivedCheckpoints < 0 {
			return nil, errors.New("plan.revision 的上限无效")
		}
		for step, capability := range processing.Steps {
			if step != string(capability) || !capability.Valid() {
				return nil, fmt.Errorf("plan steps %q 必须与能力一致", step)
			}
			steps = append(steps, capability)
		}
	} else if processing.StepArgument != "" {
		if err := inParameters("step_argument", processing.StepArgument); err != nil {
			return nil, err
		}
		if err := claim("step_argument", processing.StepArgument); err != nil {
			return nil, err
		}
		if !slices.Contains(declared.Required, processing.StepArgument) {
			return nil, errors.New("step_argument 必须是必填参数")
		}
		if len(processing.Steps) == 0 {
			return nil, errors.New("声明 step_argument 时必须声明 steps")
		}
		for step, capability := range processing.Steps {
			if strings.TrimSpace(step) == "" || !capability.Valid() {
				return nil, fmt.Errorf("steps %q 引用了无效能力 %q", step, capability)
			}
			steps = append(steps, capability)
		}
	} else if len(processing.Steps) > 0 {
		return nil, errors.New("steps 只能与 step_argument 一起声明")
	}
	capabilities := append([]schemacontract.Capability{tool.Capability, tool.FallbackCapability}, steps...)
	for _, capability := range capabilities {
		if capabilityDomains[capability] != schemacontract.DomainProcessing {
			return nil, fmt.Errorf("处理工具只能使用 processing 域能力: %q", capability)
		}
	}
	if len(steps) == 0 && tool.Capability != tool.FallbackCapability {
		return nil, errors.New("单步处理工具的 capability 与 fallback_capability 必须相同")
	}
	if choice := processing.Choice; choice != nil {
		if processing.DeliveryFormatArgument != "" {
			return nil, errors.New("choice 不能用于按格式选路的工具")
		}
		if err := inParameters("choice.default_argument", choice.DefaultArgument); err != nil {
			return nil, err
		}
		if err := claim("choice.default_argument", choice.DefaultArgument); err != nil {
			return nil, err
		}
		switch {
		case processing.Plan != nil && choice.OfferingArgument != "":
			return nil, errors.New("plan 工具的 offering_id 写在每一步里，choice.offering_argument 必须为空")
		case processing.Plan == nil:
			if err := inParameters("choice.offering_argument", choice.OfferingArgument); err != nil {
				return nil, err
			}
			if err := claim("choice.offering_argument", choice.OfferingArgument); err != nil {
				return nil, err
			}
		}
		// #321: the Agent repeats refusal texts, so an Offering ID it passed
		// is never echoed into one; the result carries it in its own field.
		if strings.TrimSpace(choice.Content) == "" || strings.TrimSpace(choice.UnknownOffering) == "" ||
			strings.Contains(choice.UnknownOffering, "{offering}") {
			return nil, errors.New("choice.content 与 choice.unknown_offering 不能为空，choice.unknown_offering 不得含 {offering}")
		}
		if strings.TrimSpace(choice.UnavailableOffering) == "" || strings.Contains(choice.UnavailableOffering, "{offering}") {
			return nil, errors.New("choice.unavailable_offering 不能为空且不得含 {offering}（#128）")
		}
		if processing.Messages.Service == nil {
			return nil, errors.New("声明 choice 的处理工具必须声明 messages.service（#321 G）")
		}
	}
	if service := processing.Messages.Service; service != nil {
		if err := validateServiceMessages(*service); err != nil {
			return nil, err
		}
	}
	messages := processing.Messages
	for name, text := range map[string]string{
		"source_required": messages.SourceRequired, "source_not_found": messages.SourceNotFound,
		"source_not_ready": messages.SourceNotReady, "source_incompatible": messages.SourceIncompatible,
		"step_unknown": messages.StepUnknown, "argument_unsupported": messages.ArgumentUnsupported,
		"reference_unsupported": messages.ReferenceUnsupported, "no_offering": messages.NoOffering,
		"invalid_input": messages.InvalidInput, "unavailable": messages.Unavailable,
	} {
		if strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("messages.%s 不能为空", name)
		}
	}
	if processing.ReferenceArgument != "" && strings.TrimSpace(messages.ReferenceRequired) == "" {
		return nil, errors.New("声明 reference_argument 的处理工具必须声明 messages.reference_required（#92）")
	}
	if confirmation := messages.SourceConfirmation; confirmation != "" &&
		(!strings.Contains(confirmation, "{selected}") || !strings.Contains(confirmation, "{source}") ||
			!strings.Contains(confirmation, "{reason}") || strings.TrimSpace(messages.SelectedSourceIncompatible) == "") {
		return nil, errors.New("messages.source_confirmation 须包含 {selected}、{source} 与 {reason}，并声明 selected_source_incompatible")
	}
	if refused := messages.SelectedServiceRefused; refused != "" &&
		(!strings.Contains(refused, "{reasons}") || !strings.Contains(messages.SourceConfirmation, "{options}") || messages.Service == nil) {
		return nil, errors.New("messages.selected_service_refused 须包含 {reasons}，source_confirmation 须包含 {options}，并声明 service")
	}
	if messages.SourceExpired != "" && !strings.Contains(messages.SourceExpired, "{step}") {
		return nil, errors.New("messages.source_expired 须包含 {step}")
	}
	return steps, nil
}

// validateServiceMessages checks the texts of a call that would not run the
// service the person named (#321 G): each is present and carries the
// placeholders the tool fills.
func validateServiceMessages(service ChatToolServiceMessages) error {
	required := map[string]struct {
		text         string
		placeholders []string
	}{
		"refused":            {service.Refused, []string{"{requested}", "{reasons}", "{options}"}},
		"confirmation":       {service.Confirmation, []string{"{requested}", "{service}", "{reasons}", "{options}"}},
		"declined":           {service.Declined, []string{"{service}", "{options}"}},
		"too_large":          {service.TooLarge, []string{"{service}", "{size}", "{limit}"}},
		"media":              {service.Media, []string{"{service}"}},
		"task":               {service.Task, []string{"{service}", "{capabilities}"}},
		"unavailable":        {service.Unavailable, []string{"{service}"}},
		"expired":            {service.Expired, []string{"{service}"}},
		"options":            {service.Options, []string{"{options}"}},
		"option_source":      {service.OptionSource, []string{"{service}", "{handle}"}},
		"option_service":     {service.OptionService, []string{"{service}"}},
		"reasons_sentence":   {service.ReasonsSentence, []string{"{reasons}"}},
		"reason_separator":   {service.ReasonSeparator, nil},
		"option_separator":   {service.OptionSeparator, nil},
		"cost_credits":       {service.CostCredits, []string{"{total}"}},
		"cost_credits_extra": {service.CostCreditsExtra, []string{"{total}", "{base}", "{extra}"}},
		"cost_native":        {service.CostNative, []string{"{amount}"}},
	}
	if service.NoService != "" {
		required["no_service"] = struct {
			text         string
			placeholders []string
		}{service.NoService, []string{"{step}", "{source}", "{reasons}", "{prerequisite}", "{options}"}}
		required["prerequisite_runs"] = struct {
			text         string
			placeholders []string
		}{service.PrerequisiteRuns, []string{"{prerequisite}", "{services}"}}
		required["prerequisite_refused"] = struct {
			text         string
			placeholders []string
		}{service.PrerequisiteRefused, []string{"{prerequisite}", "{reasons}"}}
		required["option_prerequisite"] = struct {
			text         string
			placeholders []string
		}{service.OptionPrerequisite, []string{"{prerequisite}", "{service}", "{handle}"}}
	}
	for name, entry := range required {
		if entry.text == "" {
			return fmt.Errorf("messages.service.%s 不能为空", name)
		}
		for _, placeholder := range entry.placeholders {
			if !strings.Contains(entry.text, placeholder) {
				return fmt.Errorf("messages.service.%s 必须包含 %s", name, placeholder)
			}
		}
	}
	return nil
}

// validateChatAttachments rejects handle syntax and prompt templates that
// could make two objects share a handle or leave a placeholder unfilled.
func validateChatAttachments(schema ChatSchema) error {
	context := schema.Context
	if len(context.HandlePrefixes) == 0 {
		return errors.New("context.handle_prefixes 不能为空")
	}
	for kind, prefix := range context.HandlePrefixes {
		if kind != "image" && kind != "model" {
			return fmt.Errorf("context.handle_prefixes 包含未知类型 %q", kind)
		}
		if strings.TrimSpace(prefix) == "" || strings.ContainsAny(prefix, " \t\r\n0123456789") {
			return fmt.Errorf("context.handle_prefixes[%s] 无效: %q", kind, prefix)
		}
		for otherKind, other := range context.HandlePrefixes {
			if kind != otherKind && (strings.HasPrefix(strings.ToLower(other), strings.ToLower(prefix)) ||
				strings.HasPrefix(strings.ToLower(prefix), strings.ToLower(other))) {
				return fmt.Errorf("context.handle_prefixes 的前缀互相包含: %q 与 %q", prefix, other)
			}
		}
	}
	for _, kind := range []string{"image", "model"} {
		if _, ok := context.HandlePrefixes[kind]; !ok {
			return fmt.Errorf("context.handle_prefixes 缺少 %s", kind)
		}
	}
	alias := strings.ToLower(strings.TrimSpace(context.SelectionAlias))
	if alias == "" {
		return errors.New("context.selection_alias 不能为空")
	}
	for _, prefix := range context.HandlePrefixes {
		if strings.HasPrefix(alias, strings.ToLower(prefix)) || strings.HasPrefix(strings.ToLower(prefix), alias) {
			return errors.New("context.selection_alias 不能与句柄前缀冲突")
		}
	}
	if template := context.ModelVersionTemplate; template != "" && !strings.Contains(template, "{version}") {
		return errors.New("context.model_version_template 须包含 {version}")
	}
	if strings.TrimSpace(context.ReferenceHandlesArgument) == "" {
		return errors.New("context.reference_handles_argument 不能为空")
	}
	for key, template := range context.AttachmentLabelTemplates {
		kind, _, found := strings.Cut(key, ".")
		if !found || context.HandlePrefixes[kind] == "" || !strings.Contains(template, "{handle}") {
			return fmt.Errorf("context.attachment_label_templates[%q] 无效", key)
		}
	}
	for key, template := range context.UnpicturedAttachmentTemplates {
		kind, _, _ := strings.Cut(key, ".")
		if context.HandlePrefixes[kind] == "" || !strings.Contains(template, "{handle}") {
			return fmt.Errorf("context.unpictured_attachment_templates[%q] 无效", key)
		}
	}
	if err := validateCanvasLayout(context); err != nil {
		return err
	}
	if err := validateSelectionMask(context); err != nil {
		return err
	}
	for _, kind := range []string{"image", "model"} {
		if !strings.Contains(context.UnpicturedAttachmentTemplates[kind], "{handle}") {
			return fmt.Errorf("context.unpictured_attachment_templates 缺少含 {handle} 的 %s 模板", kind)
		}
		if strings.TrimSpace(context.KindLabels[kind]) == "" {
			return fmt.Errorf("context.kind_labels 缺少 %s", kind)
		}
	}
	if !strings.Contains(context.SelectionTemplate, "{items}") ||
		!strings.Contains(context.SelectionTemplate, "{alias}") ||
		!strings.Contains(context.SelectionItemTemplate, "{handle}") ||
		!strings.Contains(context.SelectionItemTemplate, "{kind}") ||
		!strings.Contains(context.SelectionItemTemplate, "{details}") ||
		!strings.Contains(context.SelectionTaskTemplate, "{task_id}") ||
		!strings.Contains(context.SelectionCaptureTemplate, "{capture_id}") ||
		strings.TrimSpace(context.SelectionPickedTemplate) == "" ||
		!strings.Contains(context.UnknownHandleTemplate, "{handle}") ||
		!strings.Contains(context.WrongHandleKindTemplate, "{handle}") {
		return errors.New("context 的选中对象或句柄提示模板缺少占位符")
	}
	if context.MaximumAttachmentsPerMessage <= 0 || context.MaximumSelection <= 0 {
		return errors.New("context.maximum_attachments_per_message 与 maximum_selection 必须为正数")
	}
	if context.RecentGeneratedImageLimit < 0 || context.RecentGeneratedImageLimit > context.MaximumVisionImages {
		return errors.New("context.recent_generated_image_limit 必须在 0 与 maximum_vision_images 之间")
	}
	if context.MaxResultPictures < 0 || context.MaxResultPictures > context.MaximumVisionImages {
		return errors.New("context.max_result_pictures 必须在 0 与 maximum_vision_images 之间")
	}
	if err := validateChatVision(schema); err != nil {
		return err
	}
	if context.MaximumSessionNotesCharacters <= 0 ||
		!strings.Contains(context.SessionNotesTemplate, "{notes}") ||
		!notesFrameOrdered(context.SessionNotesTemplate, context.SessionNotesOpenMarker, context.SessionNotesCloseMarker) ||
		strings.TrimSpace(context.SessionNotesSavedMessage) == "" ||
		!strings.Contains(context.SessionNotesTooLongTemplate, "{maximum}") {
		return errors.New("context 的设定摘要上限必须为正数，且模板与文案必须完整")
	}
	return nil
}

// validateCanvasLayout checks the texts that describe an artboard snapshot to
// the Agent and the tool-result notes of the canvas output size (#272). They
// are required whenever the Schema declares a canvas input source.
func validateCanvasLayout(context ChatContextSchema) error {
	if strings.TrimSpace(context.CanvasInputSource) == "" {
		return nil
	}
	canvasLabel := context.UnpicturedAttachmentTemplates["image.canvas"]
	if !strings.Contains(context.CanvasLayoutTemplate, "{width}") ||
		!strings.Contains(context.CanvasLayoutTemplate, "{height}") ||
		!strings.Contains(context.CanvasLayoutTemplate, "{layers}") ||
		!strings.Contains(context.CanvasLayersTemplate, "{layers}") ||
		!strings.Contains(context.SelectionCanvasTemplate, "{layout}") ||
		strings.TrimSpace(context.SelectionCanvasNote) == "" ||
		!strings.Contains(canvasLabel, "{layout}") {
		return errors.New("context 的画板版面文案缺少占位符（canvas_layout_template、canvas_layers_template、selection_canvas_template、selection_canvas_note、unpictured_attachment_templates[image.canvas]）")
	}
	if context.MaximumCanvasLayers <= 0 {
		return errors.New("context.maximum_canvas_layers 必须为正数")
	}
	for name, template := range map[string]string{
		"canvas_output_size_template":         context.CanvasOutputSizeTemplate,
		"canvas_output_size_nearest_template": context.CanvasOutputSizeNearestTemplate,
	} {
		if !strings.Contains(template, "{canvas}") || !strings.Contains(template, "{size}") {
			return fmt.Errorf("context.%s 必须包含 {canvas} 与 {size}", name)
		}
	}
	return nil
}

// validateSelectionMask checks the texts and bounds of the frozen selection
// mask of a canvas turn (#244). They are required whenever the Schema declares
// a canvas input source: the mask only rides with a canvas turn.
func validateSelectionMask(context ChatContextSchema) error {
	if strings.TrimSpace(context.CanvasInputSource) == "" {
		return nil
	}
	// A minimal Catalog may declare a canvas source without selection masks
	// (#244); the feature is complete only when one of its texts or the preview
	// style is declared, and then every part is required.
	preview := context.SelectionMaskPreview
	if strings.TrimSpace(context.SelectionMaskItemTemplate) == "" &&
		strings.TrimSpace(context.SelectionMaskNote) == "" &&
		strings.TrimSpace(context.SelectionMaskPreviewLabel) == "" &&
		preview.MaxPixels == 0 && strings.TrimSpace(preview.MediaType) == "" {
		return nil
	}
	if !strings.Contains(context.SelectionMaskItemTemplate, "{handle}") ||
		!strings.Contains(context.SelectionMaskItemTemplate, "{bbox}") ||
		!strings.Contains(context.SelectionMaskItemTemplate, "{coverage}") ||
		strings.TrimSpace(context.SelectionMaskNote) == "" ||
		strings.TrimSpace(context.SelectionMaskPreviewLabel) == "" {
		return errors.New("context 的选区蒙版文案缺少占位符或为空（selection_mask_item_template 需要 {handle} {bbox} {coverage}，selection_mask_note 与 selection_mask_preview_label 不能为空）")
	}
	if preview.MaxPixels <= 0 || strings.TrimSpace(preview.MediaType) == "" ||
		preview.DimOpacity < 0 || preview.DimOpacity > 1 ||
		preview.TintOpacity < 0 || preview.TintOpacity > 1 ||
		!visionFlattenColour.MatchString(preview.TintColor) {
		return errors.New("context.selection_mask_preview 的上限必须为正数，dim_opacity 与 tint_opacity 在 0..1，tint_color 为 #rrggbb，media_type 不能为空")
	}
	return nil
}

// validateImageMask checks the read-only create_selection_mask declaration
// (#244): the tool it names exists as the declared control tool, its bounds
// and modes are usable and its Agent-facing texts are present.
func validateImageMask(schema ChatSchema) error {
	mask := schema.ImageMask
	if strings.TrimSpace(mask.Tool) == "" {
		return nil
	}
	declared := false
	for _, tool := range schema.AgentTools {
		if tool.Name == mask.Tool && tool.Execution.ImplementationKey == "image.mask" &&
			len(tool.Execution.Effects) == 1 && tool.Execution.Effects[0] == "READ" {
			declared = true
			break
		}
	}
	if !declared {
		return fmt.Errorf("chat.image_mask.tool 必须指向声明为 image.mask/READ 的控制工具: %q", mask.Tool)
	}
	if mask.Limits.MaxBytes <= 0 || mask.Limits.MaxPixels <= 0 || mask.Preview.MaxPixels <= 0 ||
		strings.TrimSpace(mask.Preview.MediaType) == "" {
		return errors.New("chat.image_mask 的 limits 与 preview.max_pixels 必须为正数，preview.media_type 不能为空")
	}
	seen := map[string]bool{}
	for _, mode := range mask.Modes {
		if mode != "subject" && mode != "region" && mode != "invert" {
			return fmt.Errorf("chat.image_mask.modes 包含未知模式 %q", mode)
		}
		if seen[mode] {
			return fmt.Errorf("chat.image_mask.modes 重复: %q", mode)
		}
		seen[mode] = true
	}
	if !seen["subject"] || !seen["region"] {
		return errors.New("chat.image_mask.modes 必须包含 subject 与 region")
	}
	for name, text := range map[string]string{
		"unreadable_template": mask.Texts.UnreadableTemplate,
		"invalid_region":      mask.Texts.InvalidRegion,
		"no_subject":          mask.Texts.NoSubject,
		"result_note":         mask.Texts.ResultNote,
	} {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("chat.image_mask.texts.%s 不能为空", name)
		}
	}
	for _, placeholder := range []string{"{url}", "{width}", "{height}", "{coverage}"} {
		if !strings.Contains(mask.Texts.ResultNote, placeholder) {
			return fmt.Errorf("chat.image_mask.texts.result_note 必须包含 %s", placeholder)
		}
	}
	return nil
}

// toolSchemaDeclaresParameter reports whether the tool's parameter object
// declares a property with the given name.
func toolSchemaDeclaresParameter(tool ChatToolSchema, name string) bool {
	var parameters struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
		return false
	}
	_, exists := parameters.Properties[name]
	return exists
}

// notesFrameOrdered reports whether the session-notes template frames {notes}
// between {open} and {close}, in that order, and the two markers are present
// and differ. Without a closing marker a note could not be told from the
// prompt text that follows it.
func notesFrameOrdered(template, open, close string) bool {
	if strings.TrimSpace(open) == "" || strings.TrimSpace(close) == "" || open == close {
		return false
	}
	first := strings.Index(template, "{open}")
	notes := strings.Index(template, "{notes}")
	last := strings.LastIndex(template, "{close}")
	return first >= 0 && notes > first && last > notes
}

// validatePendingApprovals rejects a pending-approval declaration that could
// leave an approval open forever, spin the sweep, or post an empty message.
func validatePendingApprovals(schema ChatPendingApprovalsSchema) error {
	if schema.TTLSeconds <= 0 || schema.MaximumListed <= 0 ||
		schema.MonitorIntervalMilliseconds <= 0 || schema.MonitorBatchSize <= 0 {
		return errors.New("pending_approvals 的有效期、列表上限、巡检间隔与批量必须为正数")
	}
	for name, value := range map[string]string{
		"started":               schema.Messages.Started,
		"succeeded":             schema.Messages.Succeeded,
		"denied":                schema.Messages.Denied,
		"submit_failed":         schema.Messages.SubmitFailed,
		"expired":               schema.Messages.Expired,
		"cancelled":             schema.Messages.Cancelled,
		"denied_extra_credits":  schema.Messages.DeniedExtraCredits,
		"extra_items_separator": schema.Messages.ExtraItemsSeparator,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("pending_approvals.messages.%s 不能为空", name)
		}
	}
	if !strings.Contains(schema.Messages.Started, "{model}") ||
		!strings.Contains(schema.Messages.Succeeded, "{model}") {
		return errors.New("pending_approvals.messages 的 started 与 succeeded 必须包含 {model}")
	}
	if !strings.Contains(schema.Messages.DeniedExtraCredits, "{extra}") {
		return errors.New("pending_approvals.messages.denied_extra_credits 必须包含 {extra}")
	}
	// The note names the model itself; naming it here too repeats it (#329 note 99647).
	if note := schema.Messages.SucceededWithNote; note != "" &&
		(strings.Contains(note, "{model}") || !strings.Contains(note, "{note}")) {
		return errors.New("pending_approvals.messages.succeeded_with_note 必须包含 {note}，且不含 {model}（备注已写出模型名）")
	}
	return nil
}

// threeViewQueueLimitMaximumSeconds keeps a page from resubmitting a refused
// view for more than a day.
const threeViewQueueLimitMaximumSeconds = 86400

// validateThreeView accepts an absent declaration and rejects a three-view declaration that names a recipe or a
// model-source view the Schema does not have, or that leaves a text empty.
func validateThreeView(schema ChatThreeViewSchema, recipes []ChatSkillRecipeSchema, tools []ChatToolSchema,
	agentTools []ChatAgentToolSchema) error {
	if schema.RecipeID == "" && schema.Label == "" && schema.ModelSourceStepKey == "" &&
		schema.MaximumListed == 0 && schema.QueueLimitSeconds == 0 {
		return nil // a deployment without the three-view recipe declares none
	}
	if strings.TrimSpace(schema.Label) == "" || strings.TrimSpace(schema.ModelSourceNote) == "" ||
		schema.MaximumListed <= 0 {
		return errors.New("three_view 的标题、3D 来源说明与列表上限必须声明")
	}
	if schema.QueueLimitSeconds <= 0 || schema.QueueLimitSeconds > threeViewQueueLimitMaximumSeconds {
		return fmt.Errorf("three_view.queue_limit_seconds 必须在 1..%d 之间", threeViewQueueLimitMaximumSeconds)
	}
	var recipe *ChatSkillRecipeSchema
	for i := range recipes {
		if recipes[i].ID == schema.RecipeID {
			recipe = &recipes[i]
		}
	}
	if recipe == nil || len(recipe.Steps) < 2 {
		return fmt.Errorf("three_view.recipe_id %q 必须是至少有两步的已声明 skill recipe", schema.RecipeID)
	}
	// #243: the first view uses the original source; every later view uses the
	// accepted model-source view (front) as its identity anchor. A historical
	// recipe with neither subject templates nor a source chain keeps working.
	source := false
	subjectTemplates := recipeUsesSubjectPlaceholders(*recipe)
	sourceChain := recipeUsesSourceChain(*recipe)
	for index, step := range recipe.Steps {
		if step.Key == schema.ModelSourceStepKey {
			source = true
		}
		if !subjectTemplates && !sourceChain {
			continue
		}
		if index == 0 {
			if step.SourceStepKey != "" {
				return errors.New("three_view 的第一个视图必须取自原图，不能依赖前一步")
			}
			continue
		}
		if step.SourceStepKey != schema.ModelSourceStepKey {
			return fmt.Errorf("three_view 的后续视图 %q 必须以 %q 为 source_step_key", step.Key, schema.ModelSourceStepKey)
		}
	}
	if !source {
		return fmt.Errorf("three_view.model_source_step_key %q 不是该 recipe 的步骤", schema.ModelSourceStepKey)
	}
	messages := schema.Messages
	required := map[string]string{
		"agent_started": messages.AgentStarted, "admission_full": messages.AdmissionFull,
		"approval_human":  messages.ApprovalHuman,
		"approval_policy": messages.ApprovalPolicy, "model_source_not_yet": messages.ModelSourceNotYet,
		"reference_required": messages.ReferenceRequired, "group_not_found": messages.GroupNotFound,
		"group_not_ready": messages.GroupNotReady, "views_changed": messages.ViewsChanged,
		"views_invalid": messages.ViewsInvalid,
	}
	if schema.Continuation.Enabled || subjectTemplates || len(schema.SubjectTypes) > 0 {
		required["agent_completed"] = messages.AgentCompleted
		required["agent_paused"] = messages.AgentPaused
		required["consistency_retry"] = messages.ConsistencyRetry
		required["consistency_failed"] = messages.ConsistencyFailed
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("three_view.messages.%s 不能为空", name)
		}
	}
	for _, field := range []string{"{run}", "{view}", "{approval}", "{credits}", "{task}"} {
		if !strings.Contains(messages.AgentStarted, field) {
			return fmt.Errorf("three_view.messages.agent_started 必须包含 %s", field)
		}
	}
	for _, required := range []struct {
		name   string
		text   string
		fields string
	}{
		{"agent_completed", messages.AgentCompleted, "{run}{views}"},
		{"agent_paused", messages.AgentPaused, "{run}{view}{reason}"},
		{"consistency_retry", messages.ConsistencyRetry, "{view}{maximum}{reasons}"},
		{"consistency_failed", messages.ConsistencyFailed, "{view}{reasons}"},
	} {
		for _, field := range splitPlaceholders(required.fields) {
			if !strings.Contains(required.text, field) {
				return fmt.Errorf("three_view.messages.%s 必须包含 %s", required.name, field)
			}
		}
	}
	if recipe.Steps[0].Optional || recipe.Steps[0].Key != schema.ModelSourceStepKey {
		return errors.New("three_view 的第一个视图必须是 3D 来源视图，且不能是可选视图")
	}
	if err := validateThreeViewSubjectTypes(schema, *recipe, agentTools); err != nil {
		return err
	}
	if err := validateThreeViewConsistency(schema, *recipe); err != nil {
		return err
	}
	if err := validateThreeViewFrame(schema); err != nil {
		return err
	}
	if err := validateThreeViewReview(schema, *recipe); err != nil {
		return err
	}
	return validateThreeViewMultiview(schema.Multiview, *recipe, tools)
}

// validateThreeViewSubjectTypes checks the subject constraint templates
// (#243): the Agent argument and its enum match the declared types, and each
// view's prompt carries the placeholder its role substitutes.
func validateThreeViewSubjectTypes(schema ChatThreeViewSchema, recipe ChatSkillRecipeSchema,
	agentTools []ChatAgentToolSchema) error {
	if len(schema.SubjectTypes) == 0 {
		// A historical declaration without subject templates keeps working;
		// prompts then carry no subject placeholder.
		for index, step := range recipe.Steps {
			if strings.Contains(step.PromptTemplate, "{subject_front_constraints}") ||
				strings.Contains(step.PromptTemplate, "{subject_view_constraints}") {
				return fmt.Errorf("three_view 没有声明 subject_types，步骤 %q 不能使用主体占位符", step.Key)
			}
			if index == 0 && strings.Contains(step.PromptTemplate, "{subject_view_constraints}") {
				return errors.New("three_view 的首视图不能使用后续视图约束")
			}
		}
		return nil
	}
	if strings.TrimSpace(schema.SubjectArgument) == "" || strings.TrimSpace(schema.DefaultSubjectType) == "" {
		return errors.New("three_view 声明 subject_types 时必须声明 subject_argument 与 default_subject_type")
	}
	if strings.TrimSpace(schema.Messages.SubjectInvalid) == "" ||
		!strings.Contains(schema.Messages.SubjectInvalid, "{subject}") ||
		!strings.Contains(schema.Messages.SubjectInvalid, "{choices}") {
		return errors.New("three_view.messages.subject_invalid 必须包含 {subject} 与 {choices}")
	}
	seen := map[string]bool{}
	for _, subject := range schema.SubjectTypes {
		id := strings.TrimSpace(subject.ID)
		if id == "" || seen[id] || strings.TrimSpace(subject.Label) == "" ||
			strings.TrimSpace(subject.FrontConstraints) == "" || strings.TrimSpace(subject.ViewConstraints) == "" {
			return fmt.Errorf("three_view.subject_types 含无效或重复的类型 %q", subject.ID)
		}
		seen[id] = true
	}
	if !seen[schema.DefaultSubjectType] {
		return fmt.Errorf("three_view.default_subject_type %q 不在 subject_types 中", schema.DefaultSubjectType)
	}
	var subjectTool *ChatAgentToolSchema
	for index := range agentTools {
		if agentTools[index].Name == "create_three_view" {
			subjectTool = &agentTools[index]
			break
		}
	}
	if subjectTool == nil {
		return errors.New("three_view 需要已声明的 create_three_view 工具")
	}
	var parameters struct {
		Properties map[string]struct {
			Type string   `json:"type"`
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(subjectTool.Parameters, &parameters); err != nil {
		return fmt.Errorf("three_view.subject_argument 的工具参数无法解析: %w", err)
	}
	property, declared := parameters.Properties[schema.SubjectArgument]
	if !declared || property.Type != "string" {
		return fmt.Errorf("create_three_view 缺少参数 %q", schema.SubjectArgument)
	}
	enumerated := map[string]bool{}
	for _, value := range property.Enum {
		enumerated[value] = true
	}
	if len(enumerated) != len(seen) {
		return errors.New("create_three_view.subject_type 的 enum 必须与 three_view.subject_types 一致")
	}
	for id := range seen {
		if !enumerated[id] {
			return fmt.Errorf("create_three_view.subject_type 缺少类型 %q", id)
		}
	}
	if !recipeUsesSubjectPlaceholders(recipe) {
		// A historical recipe has no subject templates; its separately declared
		// types and tool enum may still grow without rewriting old runs.
		return nil
	}
	for index, step := range recipe.Steps {
		if index == 0 {
			if !strings.Contains(step.PromptTemplate, "{subject_front_constraints}") {
				return errors.New("three_view 的首视图提示词必须包含 {subject_front_constraints}")
			}
			if strings.Contains(step.PromptTemplate, "{subject_view_constraints}") {
				return errors.New("three_view 的首视图提示词不能使用 {subject_view_constraints}")
			}
			continue
		}
		if !strings.Contains(step.PromptTemplate, "{subject_view_constraints}") {
			return fmt.Errorf("three_view 的视图 %q 提示词必须包含 {subject_view_constraints}", step.Key)
		}
	}
	return nil
}

func recipeUsesSubjectPlaceholders(recipe ChatSkillRecipeSchema) bool {
	for _, step := range recipe.Steps {
		if strings.Contains(step.PromptTemplate, "{subject_front_constraints}") ||
			strings.Contains(step.PromptTemplate, "{subject_view_constraints}") {
			return true
		}
	}
	return false
}

func recipeUsesSourceChain(recipe ChatSkillRecipeSchema) bool {
	for _, step := range recipe.Steps {
		if step.SourceStepKey != "" {
			return true
		}
	}
	return false
}

// validateThreeViewMultiview checks the binding to the multiview tool (#65):
// the tool takes the group and views arguments, and the required view is a
// step of the recipe.
// splitPlaceholders turns "{run}{views}" into its two field names.
func splitPlaceholders(fields string) []string {
	var result []string
	for _, part := range strings.Split(fields, "}") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		result = append(result, part+"}")
	}
	return result
}

func validateThreeViewMultiview(multiview ChatThreeViewMultiviewSchema, recipe ChatSkillRecipeSchema, tools []ChatToolSchema) error {
	for name, value := range map[string]string{
		"tool": multiview.Tool, "group_argument": multiview.GroupArgument, "views_argument": multiview.ViewsArgument,
		"required_view": multiview.RequiredView, "group_directive": multiview.GroupDirective,
		"upload_directive": multiview.UploadDirective, "views_note": multiview.ViewsNote,
		"view_separator": multiview.ViewSeparator,
	} {
		if value == "" {
			return fmt.Errorf("three_view.multiview.%s 不能为空", name)
		}
	}
	if multiview.MinimumViews < 2 {
		return errors.New("three_view.multiview.minimum_views 至少为 2")
	}
	required := false
	for _, step := range recipe.Steps {
		required = required || (step.Key == multiview.RequiredView && !step.Optional)
	}
	if !required {
		return fmt.Errorf("three_view.multiview.required_view %q 必须是该 recipe 的必需步骤", multiview.RequiredView)
	}
	// #243: an Offering without view labels receives the views in this order,
	// as a contiguous prefix that starts with the required view. A historical
	// recipe without subject templates keeps its old view keys; the order is
	// only required for the new contract.
	if recipeUsesSubjectPlaceholders(recipe) {
		if len(multiview.ViewOrder) == 0 || multiview.ViewOrder[0] != multiview.RequiredView {
			return fmt.Errorf("three_view.multiview.view_order 必须以 required_view %q 开头", multiview.RequiredView)
		}
		known := map[string]bool{}
		for _, step := range recipe.Steps {
			known[step.Key] = true
		}
		seenViews := map[string]bool{}
		for _, view := range multiview.ViewOrder {
			if !known[view] || seenViews[view] {
				return fmt.Errorf("three_view.multiview.view_order 含未知或重复的视图 %q", view)
			}
			seenViews[view] = true
		}
	}
	for field, text := range map[string]string{
		"{tool}": multiview.GroupDirective, "{group}": multiview.GroupDirective, "{model}": multiview.GroupDirective,
	} {
		if !strings.Contains(text, field) {
			return fmt.Errorf("three_view.multiview.group_directive 必须包含 %s", field)
		}
	}
	if !strings.Contains(multiview.UploadDirective, "{tool}") || !strings.Contains(multiview.UploadDirective, "{model}") ||
		!strings.Contains(multiview.ViewsNote, "{views}") {
		return errors.New("three_view.multiview 的 upload_directive 须含 {tool}、{model}，views_note 须含 {views}")
	}
	for _, tool := range tools {
		if tool.Name != multiview.Tool {
			continue
		}
		var parameters struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
			return fmt.Errorf("three_view.multiview.tool %q 的参数无法解析: %w", tool.Name, err)
		}
		// The model the multiview run is frozen to is named by the tool's model
		// argument (#103); a historical catalog may still carry it as the exact
		// Offering argument (#189).
		modelArgument := tool.ModelArgument
		if modelArgument == "" {
			modelArgument = tool.OfferingArgument
		}
		for _, argument := range []string{multiview.GroupArgument, multiview.ViewsArgument, modelArgument} {
			if _, declared := parameters.Properties[argument]; !declared || argument == "" {
				return fmt.Errorf("three_view.multiview.tool %q 缺少参数 %q", tool.Name, argument)
			}
		}
		return nil
	}
	return fmt.Errorf("three_view.multiview.tool %q 不是已声明的 Agent 工具", multiview.Tool)
}

// agentScopeModes and agentToolClasses are the identifiers the Agent scope
// speaks. The browser has a label per mode, and the code names only "auto".
var (
	agentScopeModes   = []string{"2d", "3d", "auto"}
	agentToolClasses  = []string{"2d", "3d", "cross", "utility"}
	agentScopeDomains = []string{"image", "model-3d"}
)

// validateToolAgentScope requires every Agent tool to declare its class, and
// rejects a class that contradicts the workspace domains of the tool: a "2d"
// tool works in the image workspace only, a "3d" tool in the 3D one only.
func validateToolAgentScope(name string, execution ChatToolExecutionSchema) error {
	if !slices.Contains(agentToolClasses, execution.AgentScope) {
		return fmt.Errorf("tool %q agent_scope 必须为 %v 之一，实际 %q", name, agentToolClasses, execution.AgentScope)
	}
	want := map[string]string{"2d": "image", "3d": "model-3d"}[execution.AgentScope]
	if want == "" {
		return nil
	}
	for _, domain := range execution.Domains {
		if domain != want {
			return fmt.Errorf("tool %q agent_scope %q 与 domain %q 矛盾", name, execution.AgentScope, domain)
		}
	}
	return nil
}

// validateAgentScope checks the scope table: every mode the browser can offer
// is declared once, "auto" exposes every tool class, every mode keeps the
// utility tools, and the workspace domains a mode leaves usable agree with
// the tool classes it allows.
func validateAgentScope(scope ChatAgentScopeSchema) error {
	if len(scope.Modes) == 0 {
		return errors.New("agent_scope.modes 不能为空")
	}
	offered := make(map[string]bool, len(scope.Modes))
	for _, mode := range scope.Modes {
		if !slices.Contains(agentScopeModes, mode.ID) || offered[mode.ID] {
			return fmt.Errorf("agent_scope.modes 含未知或重复的模式 %q", mode.ID)
		}
		offered[mode.ID] = true
		if strings.TrimSpace(mode.Label) == "" || strings.TrimSpace(mode.Description) == "" {
			return fmt.Errorf("agent_scope.modes[%s] 缺少 label 或 description", mode.ID)
		}
		allows := make(map[string]bool, len(mode.Allows))
		for _, class := range mode.Allows {
			if !slices.Contains(agentToolClasses, class) || allows[class] {
				return fmt.Errorf("agent_scope.modes[%s].allows 含未知或重复的类别 %q", mode.ID, class)
			}
			allows[class] = true
		}
		if !allows["utility"] {
			return fmt.Errorf("agent_scope.modes[%s].allows 必须包含 utility", mode.ID)
		}
		domains := make(map[string]bool, len(mode.WorkspaceDomains))
		for _, domain := range mode.WorkspaceDomains {
			if !slices.Contains(agentScopeDomains, domain) || domains[domain] {
				return fmt.Errorf("agent_scope.modes[%s].workspace_domains 含未知或重复的领域 %q", mode.ID, domain)
			}
			domains[domain] = true
		}
		if domains["image"] != allows["2d"] || domains["model-3d"] != allows["3d"] {
			return fmt.Errorf("agent_scope.modes[%s] 的 workspace_domains 与 allows 不一致", mode.ID)
		}
		if mode.ID == "auto" && len(allows) != len(agentToolClasses) {
			return errors.New("agent_scope 的 auto 模式必须开放所有工具类别")
		}
	}
	if !offered["auto"] {
		return errors.New("agent_scope.modes 必须包含 auto")
	}
	if !offered[scope.DefaultMode] {
		return fmt.Errorf("agent_scope.default_mode %q 未在 modes 中声明", scope.DefaultMode)
	}
	for name, template := range map[string]struct {
		text   string
		fields []string
	}{
		"context_template":   {scope.ContextTemplate, []string{"{mode}", "{description}", "{allowed_tools}", "{blocked_tools}"}},
		"rejection_template": {scope.RejectionTemplate, []string{"{tool}", "{mode}"}},
		"mock_refusal":       {scope.MockRefusal, []string{"{tool}"}},
		"empty_list_text":    {scope.EmptyListText, nil},
	} {
		if strings.TrimSpace(template.text) == "" {
			return fmt.Errorf("agent_scope.%s 不能为空", name)
		}
		for _, field := range template.fields {
			if !strings.Contains(template.text, field) {
				return fmt.Errorf("agent_scope.%s 缺少占位符 %s", name, field)
			}
		}
	}
	return nil
}

// validateSystemSkills checks the Schema-declared Skills that bind an Agent
// tool. They carry no style prompt, thumbnail or size, so they are held to
// their own contract instead of the editable Skill catalog's.
func validateSystemSkills(skills ChatSkillsSchema, agentTools map[string]struct{}) error {
	if len(skills.SystemSkills) == 0 {
		return nil
	}
	directive := skills.ToolDirectiveTemplate
	for _, placeholder := range []string{"{name}", "{prompt}", "{tool}"} {
		if !strings.Contains(directive, placeholder) {
			return fmt.Errorf("skills.tool_directive_template 必须包含 %s", placeholder)
		}
	}
	seen := map[string]struct{}{}
	for index, skill := range skills.SystemSkills {
		if strings.TrimSpace(skill.ID) == "" || strings.TrimSpace(skill.Name) == "" ||
			strings.TrimSpace(skill.Prompt) == "" || strings.TrimSpace(skill.DefaultMessage) == "" ||
			skill.ThumbnailPath != "" || skill.RawImageURL != "" || skill.ImageToImageTemplate != "" {
			return fmt.Errorf("skills.system_skills[%d] 配置不完整或带有风格 Skill 字段", index)
		}
		if _, exists := agentTools[skill.Tool]; !exists {
			return fmt.Errorf("skills.system_skills[%d] 绑定了未声明的 Agent 工具 %q", index, skill.Tool)
		}
		if _, exists := seen[skill.ID]; exists {
			return fmt.Errorf("system skill id 重复: %q", skill.ID)
		}
		seen[skill.ID] = struct{}{}
	}
	return nil
}

// chatThinkingReservedFields are owned by the adapter's request contract; a
// thinking profile must not override them.
var chatThinkingReservedFields = []string{
	"model", "messages", "tools", "stream", "temperature", "stream_options",
}

func validateChatThinkingProfiles(thinking *ChatThinkingSchema) error {
	if len(thinking.ModelProfiles) > 0 && !thinking.Enabled {
		return errors.New("agent.thinking 禁用时不能声明 model_profiles")
	}
	seen := map[string]bool{}
	var earlier []string
	for index, profile := range thinking.ModelProfiles {
		if len(profile.ModelPrefixes) == 0 {
			return fmt.Errorf("agent.thinking.model_profiles[%d] 必须声明 model_prefixes", index)
		}
		if len(profile.RequestFields) == 0 && profile.EffortField == "" && len(profile.EffortLevels) == 0 &&
			profile.DefaultEffort == "" && profile.ContextWindowTokens == 0 {
			return fmt.Errorf(
				"agent.thinking.model_profiles[%d] 必须声明 request_fields、effort_field 或 context_window_tokens",
				index,
			)
		}
		for _, prefix := range profile.ModelPrefixes {
			if prefix == "" || prefix != strings.ToLower(strings.TrimSpace(prefix)) {
				return fmt.Errorf("agent.thinking.model_profiles[%d] 的模型前缀必须为非空小写: %q", index, prefix)
			}
			if seen[prefix] {
				return fmt.Errorf("agent.thinking.model_profiles 重复声明模型前缀 %q", prefix)
			}
			// The first matching profile wins, so a prefix that an earlier,
			// shorter one already covers could never be chosen.
			for _, previous := range earlier {
				if strings.HasPrefix(prefix, previous) {
					return fmt.Errorf("agent.thinking.model_profiles 的前缀 %q 被更早的 %q 覆盖，永远不会命中；把更长的前缀放在前面", prefix, previous)
				}
			}
			seen[prefix] = true
		}
		earlier = append(earlier, profile.ModelPrefixes...)
		for field, value := range profile.RequestFields {
			if field == "" || slices.Contains(chatThinkingReservedFields, field) {
				return fmt.Errorf("agent.thinking.model_profiles[%d] 不能声明请求字段 %q", index, field)
			}
			if !json.Valid(value) {
				return fmt.Errorf("agent.thinking.model_profiles[%d] 的请求字段 %q 不是有效 JSON", index, field)
			}
		}
		if err := validateThinkingEffort(index, profile); err != nil {
			return err
		}
		if profile.ContextWindowTokens < 0 {
			return fmt.Errorf("agent.thinking.model_profiles[%d] 的 context_window_tokens 必须为正数（不声明时省略）", index)
		}
	}
	return nil
}

// validateThinkingEffort checks a profile's thinking levels (#322): the field,
// the levels and the default are declared together, the field is the
// profile's own (not a reserved or fixed field), the levels are distinct
// non-blank vendor values and the default is one of them.
func validateThinkingEffort(index int, profile ChatThinkingModelProfile) error {
	if profile.EffortField == "" && len(profile.EffortLevels) == 0 && profile.DefaultEffort == "" {
		return nil
	}
	switch {
	case profile.EffortField == "":
		return fmt.Errorf("agent.thinking.model_profiles[%d] 声明了档位却没有 effort_field", index)
	case len(profile.EffortLevels) == 0:
		return fmt.Errorf("agent.thinking.model_profiles[%d] 声明了 effort_field 却没有 effort_levels", index)
	}
	if strings.TrimSpace(profile.EffortField) != profile.EffortField ||
		slices.Contains(chatThinkingReservedFields, profile.EffortField) {
		return fmt.Errorf("agent.thinking.model_profiles[%d] 的 effort_field 不能是 %q", index, profile.EffortField)
	}
	if _, fixed := profile.RequestFields[profile.EffortField]; fixed {
		return fmt.Errorf("agent.thinking.model_profiles[%d] 的 effort_field %q 不能同时写在 request_fields 里", index, profile.EffortField)
	}
	levels := map[string]bool{}
	for _, level := range profile.EffortLevels {
		if level == "" || strings.TrimSpace(level) != level || strings.ContainsAny(level, " \t\n\r") {
			return fmt.Errorf("agent.thinking.model_profiles[%d] 的 effort_levels 含空白档位 %q", index, level)
		}
		if levels[level] {
			return fmt.Errorf("agent.thinking.model_profiles[%d] 的 effort_levels 重复声明 %q", index, level)
		}
		levels[level] = true
	}
	if !levels[profile.DefaultEffort] {
		return fmt.Errorf("agent.thinking.model_profiles[%d] 的 default_effort %q 不在 effort_levels 里", index, profile.DefaultEffort)
	}
	return nil
}

// validateContextCompaction checks chat.context_compaction (#322) when it is
// on: the ratios are shares of the window with the warning at or below the
// automatic threshold, at least one round stays verbatim, the call is bounded
// and every template carries the placeholders the code fills.
func validateContextCompaction(compaction ChatContextCompactionSchema) error {
	if !compaction.Enabled {
		return nil
	}
	if compaction.AutoThresholdRatio <= 0 || compaction.AutoThresholdRatio > 1 {
		return fmt.Errorf("context_compaction.auto_threshold_ratio 必须在 (0, 1] 内: %v", compaction.AutoThresholdRatio)
	}
	if compaction.WarningRatio <= 0 || compaction.WarningRatio > compaction.AutoThresholdRatio {
		return fmt.Errorf("context_compaction.warning_ratio 必须在 (0, auto_threshold_ratio] 内: %v", compaction.WarningRatio)
	}
	for name, value := range map[string]int{
		"keep_recent_rounds":         compaction.KeepRecentRounds,
		"auto_message_threshold":     compaction.AutoMessageThreshold,
		"request_timeout_seconds":    compaction.RequestTimeoutSeconds,
		"maximum_summary_characters": compaction.MaximumSummaryCharacters,
		"list_limit":                 compaction.ListLimit,
	} {
		if value <= 0 {
			return fmt.Errorf("context_compaction.%s 必须为正数", name)
		}
	}
	// Past the threshold there must be rounds to summarize beyond the kept ones.
	if compaction.AutoMessageThreshold <= 2*compaction.KeepRecentRounds {
		return fmt.Errorf("context_compaction.auto_message_threshold 必须大于 keep_recent_rounds 的两倍")
	}
	for _, template := range []struct{ name, text, placeholder string }{
		{"source_template", compaction.SourceTemplate, "{transcript}"},
		{"source_template", compaction.SourceTemplate, "{previous_summary}"},
		{"previous_summary_template", compaction.PreviousSummaryTemplate, "{summary}"},
		{"transcript_line_template", compaction.TranscriptLineTemplate, "{content}"},
		{"transcript_line_template", compaction.TranscriptLineTemplate, "{role}"},
		{"context_template", compaction.ContextTemplate, "{summary}"},
		{"context_template", compaction.ContextTemplate, "{open}"},
		{"context_template", compaction.ContextTemplate, "{close}"},
		{"compacted_handle_template", compaction.CompactedHandleTemplate, "{labels}"},
		{"mock_summary_template", compaction.MockSummaryTemplate, "{handles}"},
	} {
		if !strings.Contains(template.text, template.placeholder) {
			return fmt.Errorf("context_compaction.%s 须含 %s", template.name, template.placeholder)
		}
	}
	if strings.TrimSpace(compaction.SummaryPrompt) == "" {
		return errors.New("context_compaction.summary_prompt 不能为空")
	}
	open, close := strings.TrimSpace(compaction.OpenMarker), strings.TrimSpace(compaction.CloseMarker)
	if open == "" || close == "" || open == close {
		return errors.New("context_compaction 的 open_marker 与 close_marker 须为两个不同的非空标记")
	}
	return nil
}

// validateDSMLSpellings checks the accepted DSML spellings (#275). They are
// checked even while chat.dsml.enabled is off, because a model profile with
// dsml_tool_calls turns parsing on at run time.
func validateDSMLSpellings(schema ChatDSMLSchema) error {
	lists := []struct {
		name   string
		values []string
	}{
		{"dsml.markers", schema.Markers},
		{"dsml.tool_calls_elements", schema.ToolCallsElements},
	}
	for _, list := range lists {
		if len(list.values) == 0 {
			return fmt.Errorf("%s 至少声明一种写法", list.name)
		}
		seen := make(map[string]struct{}, len(list.values))
		for _, value := range list.values {
			if value == "" || strings.TrimSpace(value) != value ||
				strings.ContainsAny(value, "<>/\"\t\n\r ") {
				return fmt.Errorf("%s 含空写法、空白或标签字符：%q", list.name, value)
			}
			if _, duplicate := seen[value]; duplicate {
				return fmt.Errorf("%s 重复声明 %q", list.name, value)
			}
			seen[value] = struct{}{}
		}
	}
	return nil
}

// validateInputGapTexts checks that the sentences of the missing-input report
// carry the placeholders their code fills (A3, #121).
func validateInputGapTexts(messages ChatMessageTexts) error {
	for name, check := range map[string]struct {
		text         string
		placeholders []string
	}{
		"input_gap_missing":            {messages.InputGapMissing, []string{"{name}", "{type}", "{options}", "{supply}"}},
		"input_gap_reference_image":    {messages.InputGapReferenceImage, nil},
		"input_gap_text_limit":         {messages.InputGapTextLimit, []string{"{max}"}},
		"input_gap_labeled":            {messages.InputGapLabeled, []string{"{label}", "{type}"}},
		"input_gap_options":            {messages.InputGapOptions, []string{"{values}"}},
		"input_gap_supply_reference":   {messages.InputGapSupplyReference, []string{"{argument}"}},
		"input_gap_supply_argument":    {messages.InputGapSupplyArgument, []string{"{argument}"}},
		"input_gap_supply_unavailable": {messages.InputGapSupplyUnavailable, nil},
		"input_shape":                  {messages.InputShape, []string{"{fields}"}},
		"input_shape_text":             {messages.InputShapeText, []string{"{name}", "{count}"}},
		"input_shape_list":             {messages.InputShapeList, []string{"{name}", "{count}"}},
		"input_shape_boolean":          {messages.InputShapeBoolean, []string{"{name}"}},
		"input_shape_number":           {messages.InputShapeNumber, []string{"{name}"}},
		"input_shape_value":            {messages.InputShapeValue, []string{"{name}"}},
	} {
		if strings.TrimSpace(check.text) == "" {
			return fmt.Errorf("presentation.%s 不能为空", name)
		}
		for _, placeholder := range check.placeholders {
			if !strings.Contains(check.text, placeholder) {
				return fmt.Errorf("presentation.%s 须含 %s", name, placeholder)
			}
		}
	}
	return nil
}

// validateToolRoundLimitPending checks the texts that list the tool calls the
// model still wanted once the tool rounds ran out (#275).
func validateToolRoundLimitPending(messages ChatMessageTexts) error {
	if !strings.Contains(messages.ToolRoundLimit, "{limit}") {
		return errors.New("presentation.tool_round_limit 须含 {limit}（本次回复的工具调用次数上限）")
	}
	if strings.TrimSpace(messages.ToolRoundLimitPending) == "" ||
		!strings.Contains(messages.ToolRoundLimitPendingItem, "{tool}") ||
		!strings.Contains(messages.ToolRoundLimitPendingItem, "{intent}") ||
		!strings.Contains(messages.ToolRoundLimitPendingTool, "{tool}") {
		return errors.New(
			"presentation.tool_round_limit_pending 不能为空，pending_item 须含 {tool} 与 {intent}，pending_tool 须含 {tool}",
		)
	}
	return nil
}

// validateThinkingStatus checks the optional thinking status (#285): the
// event type and its text are declared together, and the event type does not
// reuse another event's name (the client would read it as that event).
func validateThinkingStatus(types ChatStreamEventTypes, status ChatStatusTexts) error {
	event := strings.TrimSpace(types.Thinking)
	text := strings.TrimSpace(status.Thinking)
	if event == "" && text == "" {
		return nil
	}
	if event == "" || text == "" {
		return errors.New("stream.event_types.thinking 与 presentation.status.thinking 须同时声明")
	}
	for _, other := range []string{
		types.Text, types.Status, types.Progress, types.Images, types.Title,
		types.MessageCreated, types.TaskRegistered, types.TaskUpdate, types.Error, types.Done,
	} {
		if event == other {
			return fmt.Errorf("stream.event_types.thinking %q 与其他事件类型重名", event)
		}
	}
	return nil
}

// validateToolCallBudget checks the per-reply tool call budget (#275): the
// Schema value and any declared default of its runtime override lie within
// 1..max_tool_calls_per_reply_maximum.
func validateToolCallBudget(agent ChatAgentSchema, runtime RuntimeSchema) error {
	maximum := agent.MaxToolCallsPerReplyMaximum
	if maximum <= 0 || agent.MaxToolCallsPerReply <= 0 || agent.MaxToolCallsPerReply > maximum {
		return fmt.Errorf(
			"agent 的 max_tool_calls_per_reply 必须在 1..%d（max_tool_calls_per_reply_maximum，须为正数）之间",
			maximum,
		)
	}
	setting, exists := runtime.Configuration.Settings[agent.MaxToolCallsPerReplySetting]
	if !exists {
		return fmt.Errorf("agent 的 max_tool_calls_per_reply_setting 引用了未知运行设置 %q", agent.MaxToolCallsPerReplySetting)
	}
	defaults := []string{setting.Default}
	for _, value := range setting.DefaultByEnvironment {
		defaults = append(defaults, value)
	}
	for _, value := range defaults {
		if _, err := ParseToolCallBudget(value, maximum); err != nil {
			return fmt.Errorf("运行设置 %s 的默认值无效: %w", agent.MaxToolCallsPerReplySetting, err)
		}
	}
	return nil
}

// ParseToolCallBudget reads a per-reply tool call budget override (#275). An
// empty value means "keep the Schema value" and returns 0; otherwise the value
// must be an integer in 1..maximum.
func ParseToolCallBudget(value string, maximum int) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 || number > maximum {
		return 0, fmt.Errorf("工具调用次数上限必须是 1..%d 的整数: %q", maximum, value)
	}
	return number, nil
}

// validateHistoryImageLookup checks the bound and the texts of the history
// image lookup tool (#279). They are required only when the tool is declared.
func validateHistoryImageLookup(context ChatContextSchema) error {
	if context.HistoryImageLookupLimit <= 0 {
		return errors.New("context.history_image_lookup_limit 必须为正数")
	}
	if !strings.Contains(context.HistoryImageBudgetTemplate, "{limit}") ||
		strings.TrimSpace(context.HistoryImageViewIntro) == "" ||
		!strings.Contains(context.HistoryImageViewLabelTemplate, "{handle}") ||
		!strings.Contains(context.HistoryImageUnreadableTemplate, "{handle}") ||
		strings.TrimSpace(context.HistoryImageVisionOffMessage) == "" {
		return errors.New("context 的历史图片查找文案不完整或缺少占位符")
	}
	return nil
}

// validateReferencePurposes checks a tool that lets the Agent say what each
// reference picture is for (#279): the argument must be one of the tool's
// declared parameters and the templates must carry their placeholders.
func validateReferencePurposes(tool ChatToolSchema, acceptReferences bool, context ChatContextSchema) error {
	if tool.ReferencePurposesArgument == "" {
		if tool.PromptWithReferencePurposesTemplate != "" || tool.ReferencePurposeItemTemplate != "" {
			return errors.New("参考图用途模板需要 reference_purposes_argument")
		}
		return nil
	}
	if !acceptReferences || tool.Processing != nil {
		return errors.New("reference_purposes_argument 只适用于读取参考图的生成工具")
	}
	var parameters struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(tool.Parameters, &parameters) != nil || parameters.Properties[tool.ReferencePurposesArgument] == nil {
		return fmt.Errorf("reference_purposes_argument %q 不是工具参数", tool.ReferencePurposesArgument)
	}
	if !strings.Contains(tool.PromptWithReferencePurposesTemplate, "{purposes}") ||
		!strings.Contains(tool.PromptWithReferencePurposesTemplate, "{prompt}") ||
		!strings.Contains(tool.ReferencePurposeItemTemplate, "{position}") ||
		!strings.Contains(tool.ReferencePurposeItemTemplate, "{purpose}") ||
		!strings.Contains(context.ReferencePurposeInvalidTemplate, "{handle}") {
		return errors.New("参考图用途模板缺少占位符")
	}
	return nil
}

// validateVisionDerivative checks the vision copy policy (#292): every bound
// positive, the long-side steps and JPEG qualities strictly decreasing (so the
// copy only ever gets smaller), qualities within 1–100 and an #rrggbb colour.
func validateVisionDerivative(policy ChatVisionDerivativeSchema) error {
	if len(policy.LongSideSteps) == 0 || len(policy.JPEGQualities) == 0 ||
		policy.MaximumInputBytes <= 0 || policy.MaximumPixels <= 0 ||
		policy.DecodeConcurrency <= 0 || policy.CacheMaximumBytes <= 0 {
		return errors.New("context.vision_derivative 必须声明尺寸档、质量档和正数的输入、像素、并发与缓存上限")
	}
	for index, side := range policy.LongSideSteps {
		if side <= 0 || (index > 0 && side >= policy.LongSideSteps[index-1]) {
			return errors.New("context.vision_derivative.long_side_steps 必须为递减的正数")
		}
	}
	for index, quality := range policy.JPEGQualities {
		if quality < 1 || quality > 100 || (index > 0 && quality >= policy.JPEGQualities[index-1]) {
			return errors.New("context.vision_derivative.jpeg_qualities 必须为 1–100 内递减的值")
		}
	}
	if !visionFlattenColour.MatchString(policy.FlattenColor) {
		return errors.New("context.vision_derivative.flatten_color 必须是 #rrggbb")
	}
	return nil
}

var visionFlattenColour = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// refetchReasons are the generation kernel's refetch reasons (#295) that the
// recover_task_result texts must cover.
var refetchReasons = []string{"not_applicable", "not_supported", "expired", "provider_unavailable", "repeated_failure", "delivered"}

func validateRefetchToolTexts(agent ChatAgentSchema) error {
	if !strings.Contains(agent.RefetchStartedContent, "{task_id}") {
		return errors.New("agent 的 refetch_started_content 必须包含 {task_id}")
	}
	for _, reason := range refetchReasons {
		if strings.TrimSpace(agent.RefetchUnavailableContent[reason]) == "" {
			return fmt.Errorf("agent 的 refetch_unavailable_content 缺少 %q", reason)
		}
	}
	// The repeated failure is named in the text (#295 acceptance finding 1).
	if !strings.Contains(agent.RefetchUnavailableContent["repeated_failure"], "{cause}") {
		return errors.New("agent 的 refetch_unavailable_content.repeated_failure 必须包含 {cause}")
	}
	// The delivered refetch is named, so the Agent follows that Task (#330).
	if !strings.Contains(agent.RefetchUnavailableContent["delivered"], "{task_id}") {
		return errors.New("agent 的 refetch_unavailable_content.delivered 必须包含 {task_id}")
	}
	return nil
}

// validateChatChoice checks the in-chat choice card (#321) once any processing
// tool declares a service choice. Older Schemas without one stay valid.
func validateChatChoice(schema ChatSchema) error {
	used := false
	for _, tool := range schema.Tools {
		used = used || (tool.Processing != nil && tool.Processing.Choice != nil)
	}
	if !used {
		return nil
	}
	for name, value := range map[string]string{
		"presentation.choice_answered": schema.Presentation.Messages.ChoiceAnswered,
		"presentation.choice_rejected": schema.Presentation.Messages.ChoiceRejected,
		"api.fields.choice_answer":     schema.API.Fields.ChoiceAnswer,
		"choice.mock_summary":          schema.Choice.MockSummary,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s 不能为空", name)
		}
	}
	if !strings.Contains(schema.Choice.AnswerTemplate, "{continuation}") || !strings.Contains(schema.Choice.PendingTemplate, "{continuation}") {
		return errors.New("choice.answer_template 与 choice.pending_template 必须包含 {continuation}")
	}
	if schema.Choice.MaxOptions < 2 || schema.Choice.ListLimit < 1 {
		return errors.New("choice.max_options 至少为 2，choice.list_limit 至少为 1")
	}
	return nil
}

// validateVisualAnchors checks application.chat.visual_anchors (#250) once the
// set_visual_anchor tool is declared. Bounds are positive, every kind is
// unique with a label and a purpose template, the Agent block carries the
// placeholders its renderer fills, and the tool's parameters agree with the
// declared kinds and action vocabulary. A Catalog without the tool stays
// valid.
func validateVisualAnchors(schema ChatSchema) error {
	declared := false
	for _, tool := range schema.AgentTools {
		if tool.Execution.ImplementationKey == "anchor.write" {
			declared = true
			break
		}
	}
	anchors := schema.VisualAnchors
	if !declared {
		if len(anchors.Kinds) > 0 || anchors.MaximumAnchors > 0 {
			return errors.New("chat.visual_anchors 需要已声明的 set_visual_anchor 工具")
		}
		return nil
	}
	if anchors.MaximumAnchors <= 0 || anchors.MaximumReferences <= 0 ||
		anchors.MaximumNameCharacters <= 0 || anchors.MaximumDescriptionCharacters <= 0 {
		return errors.New("chat.visual_anchors 的上限必须为正数")
	}
	if len(anchors.Kinds) == 0 {
		return errors.New("chat.visual_anchors.kinds 不能为空")
	}
	kindIDs := make([]string, 0, len(anchors.Kinds))
	seen := map[string]bool{}
	for index, kind := range anchors.Kinds {
		switch kind.ID {
		case "identity", "style":
		default:
			return fmt.Errorf("chat.visual_anchors.kinds[%d].id 无效: %q", index, kind.ID)
		}
		if seen[kind.ID] {
			return fmt.Errorf("chat.visual_anchors.kinds[%d].id 重复: %q", index, kind.ID)
		}
		seen[kind.ID] = true
		kindIDs = append(kindIDs, kind.ID)
		if strings.TrimSpace(kind.Label) == "" || strings.TrimSpace(kind.ReferencePurposeTemplate) == "" ||
			!strings.Contains(kind.ReferencePurposeTemplate, "{name}") {
			return fmt.Errorf("chat.visual_anchors.kinds[%d] 的 label 与 reference_purpose_template 不完整", index)
		}
	}
	for name, template := range map[string]string{
		"context_template":               anchors.ContextTemplate,
		"item_template":                  anchors.ItemTemplate,
		"prompt_hint":                    anchors.PromptHint,
		"note_template":                  anchors.NoteTemplate,
		"invalid_template":               anchors.InvalidTemplate,
		"precedence_note":                anchors.PrecedenceNote,
		"reference_count_template":       anchors.ReferenceCountTemplate,
		"reference_label_template":       anchors.ReferenceLabelTemplate,
		"dropped_label_template":         anchors.DroppedLabelTemplate,
		"reference_unavailable_template": anchors.ReferenceUnavailableTemplate,
		"saved_message":                  anchors.SavedMessage,
		"removed_message":                anchors.RemovedMessage,
		"declined_message":               anchors.DeclinedMessage,
	} {
		if strings.TrimSpace(template) == "" {
			return fmt.Errorf("chat.visual_anchors.%s 不能为空", name)
		}
	}
	if !strings.Contains(anchors.ContextTemplate, "{open}") ||
		!strings.Contains(anchors.ContextTemplate, "{anchors}") ||
		!strings.Contains(anchors.ContextTemplate, "{style_lock}") ||
		!strings.Contains(anchors.ContextTemplate, "{precedence}") ||
		!strings.Contains(anchors.ContextTemplate, "{close}") {
		return errors.New("chat.visual_anchors.context_template 缺少占位符")
	}
	if !strings.Contains(anchors.ItemTemplate, "{kind}") ||
		!strings.Contains(anchors.ItemTemplate, "{name}") ||
		!strings.Contains(anchors.ItemTemplate, "{description}") ||
		!strings.Contains(anchors.ItemTemplate, "{references}") {
		return errors.New("chat.visual_anchors.item_template 缺少占位符")
	}
	if !strings.Contains(anchors.PromptHint, "{argument}") || !strings.Contains(anchors.PromptHint, "{names}") ||
		!strings.Contains(anchors.NoteTemplate, "{argument}") || !strings.Contains(anchors.NoteTemplate, "{names}") ||
		!strings.Contains(anchors.InvalidTemplate, "{reason}") ||
		!strings.Contains(anchors.ReferenceCountTemplate, "{count}") ||
		!strings.Contains(anchors.ReferenceLabelTemplate, "{name}") ||
		!strings.Contains(anchors.ReferenceLabelTemplate, "{index}") ||
		!strings.Contains(anchors.DroppedLabelTemplate, "{name}") ||
		!strings.Contains(anchors.DroppedLabelTemplate, "{index}") ||
		!strings.Contains(anchors.ReferenceUnavailableTemplate, "{handle}") {
		return errors.New("chat.visual_anchors 的模板缺少占位符")
	}
	if anchors.OpenMarker == "" || anchors.CloseMarker == "" || anchors.OpenMarker == anchors.CloseMarker {
		return errors.New("chat.visual_anchors 的 open_marker 与 close_marker 须为两个不同的非空标记")
	}
	parameters, found := declaredToolParameters(schema, "set_visual_anchor")
	if !found {
		return errors.New("chat.visual_anchors 需要已声明的 set_visual_anchor 工具")
	}
	properties, _ := parameters["properties"].(map[string]any)
	action, _ := properties["action"].(map[string]any)
	if action == nil || action["type"] != "string" {
		return errors.New("set_visual_anchor.action 必须是字符串参数")
	}
	values, _ := action["enum"].([]any)
	wantActions := []string{"create", "update", "delete", "decline"}
	if len(values) != len(wantActions) {
		return errors.New("set_visual_anchor.action 的 enum 必须覆盖 create/update/delete/decline")
	}
	for index, value := range values {
		if value != wantActions[index] {
			return errors.New("set_visual_anchor.action 的 enum 必须覆盖 create/update/delete/decline")
		}
	}
	kind, _ := properties["kind"].(map[string]any)
	kindValues, _ := kind["enum"].([]any)
	if kind == nil || len(kindValues) != len(kindIDs) {
		return errors.New("set_visual_anchor.kind 的 enum 必须与 chat.visual_anchors.kinds 一致")
	}
	for index, value := range kindValues {
		if value != kindIDs[index] {
			return errors.New("set_visual_anchor.kind 的 enum 必须与 chat.visual_anchors.kinds 一致")
		}
	}
	required, _ := parameters["required"].([]any)
	if len(required) == 0 || required[0] != "action" {
		return errors.New("set_visual_anchor.required 必须包含 action")
	}
	// The tool's target must be reachable by an Agent choice card: a
	// visual_anchor kind whose options come from the Agent's proposal.
	choiceKind := false
	for _, declaredKind := range schema.Choice.Kinds {
		if declaredKind.ID == "visual_anchor" && declaredKind.ToolName == "set_visual_anchor" &&
			declaredKind.OptionSource == "agent_proposal" && declaredKind.Argument == "action" {
			choiceKind = true
		}
	}
	if !choiceKind {
		return errors.New("chat.choice.kinds 必须声明 visual_anchor 确认卡")
	}
	for _, field := range []string{"reference_priority_trimmed", "visual_anchor_saved", "visual_anchor_removed", "visual_anchor_declined"} {
		value := map[string]string{
			"reference_priority_trimmed": schema.Presentation.Messages.ReferencePriorityTrimmed,
			"visual_anchor_saved":        schema.Presentation.Messages.VisualAnchorSaved,
			"visual_anchor_removed":      schema.Presentation.Messages.VisualAnchorRemoved,
			"visual_anchor_declined":     schema.Presentation.Messages.VisualAnchorDeclined,
		}[field]
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("chat.presentation.messages.%s 不能为空", field)
		}
	}
	if !strings.Contains(schema.Presentation.Messages.ReferencePriorityTrimmed, "{maximum}") ||
		!strings.Contains(schema.Presentation.Messages.ReferencePriorityTrimmed, "{anchors}") ||
		!strings.Contains(schema.Presentation.Messages.ReferencePriorityTrimmed, "{dropped}") {
		return errors.New("chat.presentation.messages.reference_priority_trimmed 缺少占位符")
	}
	return nil
}

// chatPreferenceReasonMessagePrefix names the UI copy of one declared rejection
// reason. The workspace renders the preset by this key, so a Catalog that
// declares the preset without the text would show an empty choice.
const chatPreferenceReasonMessagePrefix = "ui.workspace.preferences.reason."

// validateChatPreferences checks application.chat.preferences (#241). A
// Catalog that declares no preference storage stays valid. When it does, the
// bounds are positive, the prompt cap fits the entry cap, the context template
// frames its items between two distinct markers that are not the settings
// summary's, every item and reason template carries the placeholders its
// renderer fills, and every reason preset has both a label and the UI copy the
// workspace shows. Reason presets are unique stable ids with a label, because a
// stored record keeps the id across schema edits.
func validateChatPreferences(schema ChatSchema, content ContentSchema) error {
	preferences := schema.Preferences
	// A zero entry cap disables the feature (the #238 "before" Catalog keeps the
	// rest of the block, so only the cap says off). It is valid and injects
	// nothing; the routes answer an empty list and refuse writes.
	if preferences.MaximumEntries <= 0 {
		return nil
	}
	if preferences.MaximumPromptItems <= 0 ||
		preferences.MaximumPromptItems > preferences.MaximumEntries ||
		preferences.MaximumDirectionCharacters <= 0 || preferences.MaximumReasonCharacters <= 0 {
		return errors.New("chat.preferences 的上限必须为正数，且 maximum_prompt_items 不得大于 maximum_entries")
	}
	if strings.TrimSpace(preferences.ContextTemplate) == "" ||
		!preferencesFrameOrdered(preferences.ContextTemplate, preferences.OpenMarker, preferences.CloseMarker) {
		return errors.New("chat.preferences.context_template 必须按 {open}/{items}/{close} 顺序框住条目")
	}
	if strings.TrimSpace(preferences.OpenMarker) == "" || strings.TrimSpace(preferences.CloseMarker) == "" ||
		preferences.OpenMarker == preferences.CloseMarker {
		return errors.New("chat.preferences 的 open_marker 与 close_marker 须为两个不同的非空标记")
	}
	for _, marker := range []string{schema.Context.SessionNotesOpenMarker, schema.Context.SessionNotesCloseMarker} {
		if marker != "" && (marker == preferences.OpenMarker || marker == preferences.CloseMarker) {
			return errors.New("chat.preferences 的标记不能与设定摘要的标记相同")
		}
	}
	for name, template := range map[string]string{
		"selected_item_template": preferences.SelectedItemTemplate,
		"signal_item_template":   preferences.SignalItemTemplate,
	} {
		if strings.TrimSpace(template) == "" || !strings.Contains(template, "{direction}") ||
			!strings.Contains(template, "{handle}") {
			return fmt.Errorf("chat.preferences.%s 必须包含 {direction} 与 {handle}", name)
		}
	}
	if strings.TrimSpace(preferences.RejectedItemTemplate) == "" ||
		!strings.Contains(preferences.RejectedItemTemplate, "{direction}") ||
		!strings.Contains(preferences.RejectedItemTemplate, "{handle}") ||
		!strings.Contains(preferences.RejectedItemTemplate, "{reason}") {
		return errors.New("chat.preferences.rejected_item_template 必须包含 {direction}、{handle} 与 {reason}")
	}
	if strings.TrimSpace(preferences.ReasonJoinTemplate) == "" ||
		!strings.Contains(preferences.ReasonJoinTemplate, "{preset}") ||
		!strings.Contains(preferences.ReasonJoinTemplate, "{text}") {
		return errors.New("chat.preferences.reason_join_template 必须包含 {preset} 与 {text}")
	}
	if len(preferences.ReasonPresets) == 0 {
		return errors.New("chat.preferences.reason_presets 不能为空")
	}
	seen := map[string]bool{}
	presetPattern := regexp.MustCompile(`^[a-z0-9_]+$`)
	for index, preset := range preferences.ReasonPresets {
		if !presetPattern.MatchString(preset.ID) || strings.TrimSpace(preset.Label) == "" {
			return fmt.Errorf("chat.preferences.reason_presets[%d] 需要小写稳定 id 与非空 label", index)
		}
		if seen[preset.ID] {
			return fmt.Errorf("chat.preferences.reason_presets[%d].id 重复: %q", index, preset.ID)
		}
		if strings.TrimSpace(content.Messages[chatPreferenceReasonMessagePrefix+preset.ID]) == "" {
			return fmt.Errorf("chat.preferences.reason_presets[%d] 缺少 %s%s 文案",
				index, chatPreferenceReasonMessagePrefix, preset.ID)
		}
		seen[preset.ID] = true
	}
	return nil
}

// preferencesFrameOrdered reports whether the preference template frames its
// items between an opening and a closing marker in that order.
func preferencesFrameOrdered(template, open, close string) bool {
	if strings.TrimSpace(open) == "" || strings.TrimSpace(close) == "" || open == close {
		return false
	}
	first := strings.Index(template, "{open}")
	items := strings.Index(template, "{items}")
	last := strings.LastIndex(template, "{close}")
	return first >= 0 && items > first && last > items
}

// validateAnchorArgument checks a tool's anchor_argument (#250): only a
// reference-reading generation tool may declare one, and it must be a declared
// array-of-strings parameter.
func validateAnchorArgument(tool ChatToolSchema, acceptReferences bool) error {
	if tool.AnchorArgument == "" {
		return nil
	}
	if !acceptReferences || tool.Processing != nil {
		return errors.New("anchor_argument 只适用于读取参考图的生成工具")
	}
	var parameters struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(tool.Parameters, &parameters) != nil || parameters.Properties[tool.AnchorArgument] == nil {
		return fmt.Errorf("anchor_argument %q 不是工具参数", tool.AnchorArgument)
	}
	var property struct {
		Type  string `json:"type"`
		Items struct {
			Type string `json:"type"`
		} `json:"items"`
	}
	if json.Unmarshal(parameters.Properties[tool.AnchorArgument], &property) != nil ||
		property.Type != "array" || property.Items.Type != "string" {
		return fmt.Errorf("anchor_argument %q 必须是字符串数组参数", tool.AnchorArgument)
	}
	return nil
}

// Catalog validates declarations without importing the renderer, so the
// rendering vocabulary is repeated here. TestModelReviewVocabularyMatchesRenderer
// keeps these copies equal to internal/modelinspect.
const modelReviewMaxRenderDimension = 2048

// modelReviewViews and modelReviewModes are the rendering vocabulary of
// internal/modelinspect. The Schema may offer a subset of them, never a name
// the renderer does not know.
var (
	modelReviewViews = []string{"front", "back", "left", "right", "top", "three_quarter"}
	modelReviewModes = []string{"textured", "clay", "normals", "wireframe"}
	// modelReviewPlaceholder finds the braces the executor fills. Any other
	// brace left in a template would reach the Agent verbatim.
	modelReviewPlaceholder = regexp.MustCompile(`\{[a-z_]+\}`)
)

// validateImageReview checks application.chat.image_review (#245) once the
// inspect_image tool is declared: bounds, checklist ranges and the texts the
// executor fills. Schemas without the tool stay valid.
func validateImageReview(schema ChatSchema) error {
	used := false
	for _, tool := range schema.AgentTools {
		if tool.Execution.ImplementationKey == "image.inspect" {
			used = true
			break
		}
	}
	if !used {
		return nil
	}
	review := schema.ImageReview
	if review.Limits.MaxBytes <= 0 || review.Limits.MaxPixels <= 0 {
		return errors.New("chat.image_review.limits 的字节与像素上限必须为正数")
	}
	for name, checklist := range map[string]ChatImageChecklistSchema{"delivery": review.Delivery, "input_3d": review.Input3D} {
		if checklist.MaxBackgroundSpread < 0 || checklist.MinSubjectRatio < 0 || checklist.MaxSubjectRatio < 0 ||
			checklist.MinSubjectRatio > 1 || checklist.MaxSubjectRatio > 1 || checklist.MaxComponents < 0 {
			return fmt.Errorf("chat.image_review.%s 的比例或范围不合法", name)
		}
		if checklist.MaxSubjectRatio > 0 && checklist.MinSubjectRatio > checklist.MaxSubjectRatio {
			return fmt.Errorf("chat.image_review.%s 的主体占比下限高于上限", name)
		}
	}
	if review.MaxRounds <= 0 || strings.TrimSpace(review.OverrideArgument) == "" ||
		!strings.Contains(review.RefusedTemplate, "{reasons}") ||
		!strings.Contains(review.RefusedTemplate, "{override_argument}") ||
		!strings.Contains(review.OverrideNote, "{reason}") ||
		!strings.Contains(review.UnreadableTemplate, "{handle}") ||
		!strings.Contains(review.UnreadableTemplate, "{reason}") ||
		!strings.Contains(review.RoundsExhaustedTemplate, "{maximum}") ||
		strings.TrimSpace(review.DeliveryChecklist) == "" || strings.TrimSpace(review.Input3DChecklist) == "" {
		return errors.New("chat.image_review 的自检轮数、放行参数或文案不完整")
	}
	if err := validateImageVisualItems(schema); err != nil {
		return err
	}
	return validateImageUsageReview(schema)
}

// imageUsageReviewChecks are the checks the 2D purpose adds to the delivery
// checklist (#282, #284, #287). Each needs a hint for the Agent.
var imageUsageReviewChecks = []string{"short_edge", "seamless", "alpha", "edge_fringe"}

// imageUsageHintPlaceholder finds the braces a hint can carry; any other brace
// left in a hint would reach the Agent verbatim.
var imageUsageHintPlaceholder = regexp.MustCompile(`\{[a-z_]+\}`)

// validateImageUsageReview checks application.chat.image_review.usage: the
// thresholds of the purpose checks and the words of their next steps. A Schema
// that declares 2D purposes must declare it, so a purpose's minimum resolution,
// tiling and transparency are never promised without a check; a Schema with no
// purposes may omit it (historical Catalogs).
func validateImageUsageReview(schema ChatSchema) error {
	usage := schema.ImageReview.Usage
	if usage == nil {
		if len(schema.Image2DUsage.Profiles) > 0 {
			return errors.New("chat.image_review.usage 必须声明：application.chat.image2d_usage 已声明 2D 用途")
		}
		return nil
	}
	tiling, alpha, fringe := usage.Tiling, usage.Alpha, usage.EdgeFringe
	if tiling.Segments < 1 || tiling.Segments > 64 || tiling.MaxSeamRatio <= 1 ||
		tiling.MinSeamDelta < 0 || tiling.MinSeamDelta > 255 {
		return errors.New("chat.image_review.usage.tiling 的分段数需在 1..64、max_seam_ratio 需大于 1、min_seam_delta 需在 0..255")
	}
	if alpha.MinTransparentRatio < 0 || alpha.MinTransparentRatio >= 1 || alpha.MaxBackgroundSpread < 0 {
		return errors.New("chat.image_review.usage.alpha 的透明占比需在 [0,1)、背景扩散上限不能为负")
	}
	if fringe.BandRatio <= 0 || fringe.BandRatio > 0.05 || fringe.LightLuma <= 0 || fringe.LightLuma > 255 ||
		fringe.LumaGap < 0 || fringe.LumaGap > 255 || fringe.MaxRatio < 0 || fringe.MaxRatio >= 1 {
		return errors.New("chat.image_review.usage.edge_fringe 的 band_ratio 需在 (0,0.05]、亮度阈值需在 0..255、max_ratio 需在 [0,1)")
	}
	for _, name := range []string{usage.BriefPurposeNote, usage.DefaultPurposeNote} {
		if !strings.Contains(name, "{purpose}") {
			return errors.New("chat.image_review.usage 的 brief_purpose_note 与 default_purpose_note 必须包含 {purpose}")
		}
	}
	if strings.TrimSpace(usage.ModelsSeparator) == "" || strings.TrimSpace(usage.NoModelsText) == "" ||
		!strings.Contains(usage.ModelsTemplate, "{models}") {
		return errors.New("chat.image_review.usage 的 models_template（含 {models}）、models_separator 与 no_models_text 不能为空")
	}
	allowed := map[string]bool{"{actual}": true, "{expected}": true, "{purpose}": true, "{minimum}": true, "{models}": true}
	for _, name := range imageUsageReviewChecks {
		hint := usage.Hints[name]
		if strings.TrimSpace(hint) == "" {
			return fmt.Errorf("chat.image_review.usage.hints.%s 不能为空", name)
		}
		for _, placeholder := range imageUsageHintPlaceholder.FindAllString(hint, -1) {
			if !allowed[placeholder] {
				return fmt.Errorf("chat.image_review.usage.hints.%s 含未知占位符 %s", name, placeholder)
			}
		}
	}
	for name := range usage.Hints {
		if !containsString(imageUsageReviewChecks, name) {
			return fmt.Errorf("chat.image_review.usage.hints 含未知检查 %q", name)
		}
	}
	if !strings.Contains(usage.Hints["short_edge"], "{models}") {
		return errors.New("chat.image_review.usage.hints.short_edge 必须包含 {models}：分辨率不达标时要点名可行的模型")
	}
	if field := usage.TransparentField; field != "" {
		declared := false
		for _, candidate := range schema.Model3DUsage.Brief.Fields {
			if candidate.Name == field && candidate.Type == "boolean" {
				declared = true
				break
			}
		}
		if !declared {
			return fmt.Errorf("chat.image_review.usage.transparent_field %q 不是制作简报里声明的 boolean 字段", field)
		}
	}
	return nil
}

// validateWorkLedger checks application.chat.work_ledger (#248) once the
// get_production_step tool is declared. Every bound must be positive, every
// Agent-facing template must carry the placeholders the renderer fills, the
// block markers must differ, and the step handle prefix must not collide with
// the attachment handle prefixes or the selection alias. A Catalog without the
// tool stays valid.
func validateWorkLedger(schema ChatSchema) error {
	used := false
	for _, tool := range schema.AgentTools {
		if tool.Execution.ImplementationKey == "ledger.read" {
			used = true
			break
		}
	}
	if !used {
		return nil
	}
	ledger := schema.WorkLedger
	for name, value := range map[string]int{
		"maximum_steps":              ledger.MaximumSteps,
		"summary_maximum_characters": ledger.SummaryMaximumCharacters,
		"prompt_preview_characters":  ledger.PromptPreviewCharacters,
		"entry_maximum_characters":   ledger.EntryMaximumCharacters,
	} {
		if value <= 0 {
			return fmt.Errorf("chat.work_ledger.%s 必须为正数", name)
		}
	}
	// generation.Module.List caps one page at 100; a larger bound would be
	// silently truncated.
	if ledger.MaximumSteps > 100 {
		return errors.New("chat.work_ledger.maximum_steps 不能超过 100")
	}
	for _, template := range []struct {
		name         string
		text         string
		placeholders []string
	}{
		{"summary_template", ledger.SummaryTemplate, []string{"{open}", "{steps}", "{rejected}", "{omissions}", "{close}"}},
		{"step_line_template", ledger.StepLineTemplate, []string{"{step}", "{task_id}", "{capability}", "{model}", "{status}", "{prompt}", "{self_check}", "{decision}"}},
		{"prompt_line_template", ledger.PromptLineTemplate, []string{"{preview}"}},
		{"self_check_line_template", ledger.SelfCheckLineTemplate, []string{"{tool}", "{passed}", "{unknown}", "{failed}", "{leftovers}"}},
		{"leftover_template", ledger.LeftoverTemplate, []string{"{checks}"}},
		{"unknown_step_template", ledger.UnknownStepTemplate, []string{"{step}"}},
	} {
		if strings.TrimSpace(template.text) == "" {
			return fmt.Errorf("chat.work_ledger.%s 不能为空", template.name)
		}
		for _, placeholder := range template.placeholders {
			if !strings.Contains(template.text, placeholder) {
				return fmt.Errorf("chat.work_ledger.%s 须含 %s", template.name, placeholder)
			}
		}
	}
	for name, text := range map[string]string{
		"empty_text":            ledger.EmptyText,
		"not_readable_template": ledger.NotReadableTemplate,
		"none_text":             ledger.NoneText,
		"prompt_omitted_text":   ledger.PromptOmittedText,
		"omissions_note":        ledger.OmissionsNote,
	} {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("chat.work_ledger.%s 不能为空", name)
		}
	}
	if !strings.Contains(ledger.OmissionsNote, "{count}") {
		return errors.New("chat.work_ledger.omissions_note 须含 {count}")
	}
	if !strings.Contains(ledger.FailedViewsTemplate, "{views}") {
		return errors.New("chat.work_ledger.failed_views_template 须含 {views}")
	}
	if ledger.TruncatedNote != "" && !strings.Contains(ledger.TruncatedNote, "{count}") {
		return errors.New("chat.work_ledger.truncated_note 须含 {count}")
	}
	open, close := strings.TrimSpace(ledger.OpenMarker), strings.TrimSpace(ledger.CloseMarker)
	if open == "" || close == "" || open == close {
		return errors.New("chat.work_ledger 的 open_marker 与 close_marker 须为两个不同的非空标记")
	}
	prefix := strings.ToLower(strings.TrimSpace(ledger.StepPrefix))
	if prefix == "" {
		return errors.New("chat.work_ledger.step_prefix 不能为空")
	}
	alias := strings.ToLower(strings.TrimSpace(schema.Context.SelectionAlias))
	if alias != "" && (strings.HasPrefix(alias, prefix) || strings.HasPrefix(prefix, alias)) {
		return errors.New("chat.work_ledger.step_prefix 不能与 selection_alias 冲突")
	}
	for _, handlePrefix := range schema.Context.HandlePrefixes {
		lowered := strings.ToLower(strings.TrimSpace(handlePrefix))
		if lowered != "" && (strings.HasPrefix(lowered, prefix) || strings.HasPrefix(prefix, lowered)) {
			return errors.New("chat.work_ledger.step_prefix 不能与 handle_prefixes 冲突")
		}
	}
	rejected := ledger.Rejected
	if rejected.MaximumItems <= 0 {
		return errors.New("chat.work_ledger.rejected.maximum_items 必须为正数")
	}
	if !strings.Contains(rejected.ContextTemplate, "{items}") {
		return errors.New("chat.work_ledger.rejected.context_template 须含 {items}")
	}
	for _, placeholder := range []string{"{step}", "{reason}"} {
		if !strings.Contains(rejected.LineTemplate, placeholder) {
			return fmt.Errorf("chat.work_ledger.rejected.line_template 须含 %s", placeholder)
		}
	}
	if strings.TrimSpace(rejected.SupersededReason) == "" {
		return errors.New("chat.work_ledger.rejected.superseded_reason 不能为空")
	}
	anchors := ledger.Anchors
	if anchors.MaximumTextCharacters <= 0 || strings.TrimSpace(anchors.StyleLabel) == "" {
		return errors.New("chat.work_ledger.anchors 的样本文本上限与 style_label 必须声明")
	}
	for _, template := range []struct {
		name         string
		text         string
		placeholders []string
	}{
		{"context_template", anchors.ContextTemplate, []string{"{open}", "{anchors}", "{close}"}},
		{"entry_template", anchors.EntryTemplate, []string{"{label}", "{text}", "{references}"}},
		{"reference_template", anchors.ReferenceTemplate, []string{"{handles}"}},
	} {
		for _, placeholder := range template.placeholders {
			if !strings.Contains(template.text, placeholder) {
				return fmt.Errorf("chat.work_ledger.anchors.%s 须含 %s", template.name, placeholder)
			}
		}
	}
	if !strings.Contains(anchors.MissingReferencesTemplate, "{count}") {
		return errors.New("chat.work_ledger.anchors.missing_references_template 须含 {count}")
	}
	anchorOpen, anchorClose := strings.TrimSpace(anchors.OpenMarker), strings.TrimSpace(anchors.CloseMarker)
	if anchorOpen == "" || anchorClose == "" || anchorOpen == anchorClose {
		return errors.New("chat.work_ledger.anchors 的 open_marker 与 close_marker 须为两个不同的非空标记")
	}
	// The style anchor is the session style lock injected verbatim; a smaller
	// anchor bound would cut the lock the person wrote.
	if schema.StyleLock.MaximumTextCharacters > anchors.MaximumTextCharacters {
		return errors.New("chat.work_ledger.anchors.maximum_text_characters 不能小于 style_lock.maximum_text_characters")
	}
	return nil
}

// validateSelfCheck checks application.chat.self_check (#246) once the record
// writer or the reference comparison tool is declared: the states and their
// roles, the counters, the reference view subset and every Agent-facing text
// with the placeholders the executor fills. A Catalog without either tool stays
// valid.
func validateSelfCheck(schema ChatSchema) error {
	used := false
	for _, tool := range schema.AgentTools {
		switch tool.Execution.ImplementationKey {
		case "model.compare_reference", "self_check.record":
			used = true
		}
	}
	if !used {
		return nil
	}
	selfCheck := schema.SelfCheck
	limits := selfCheck.Limits
	if limits.MaxRoundsPerResult <= 0 || limits.MaxCorrectionsPerResult <= 0 || limits.MaxRendersPerRound <= 0 {
		return errors.New("chat.self_check.limits 的轮次、修正与每轮渲染上限必须为正数")
	}
	roles := map[string]bool{}
	ids := map[string]bool{}
	for _, status := range selfCheck.Statuses {
		if strings.TrimSpace(status.ID) == "" || strings.TrimSpace(status.Label) == "" {
			return errors.New("chat.self_check.statuses 的 id 与 label 不能为空")
		}
		switch status.Role {
		case "unchecked", "passed", "issues", "uncheckable":
		default:
			return fmt.Errorf("chat.self_check.statuses 含未知 role %q", status.Role)
		}
		if ids[status.ID] {
			return fmt.Errorf("chat.self_check.statuses 的 id %q 重复", status.ID)
		}
		if roles[status.Role] {
			return fmt.Errorf("chat.self_check.statuses 的 role %q 重复", status.Role)
		}
		ids[status.ID] = true
		roles[status.Role] = true
	}
	for _, role := range []string{"unchecked", "passed", "issues"} {
		if !roles[role] {
			return fmt.Errorf("chat.self_check.statuses 缺少 %s 状态", role)
		}
	}
	if !selfCheck.RequiredChecks.Inspect || !selfCheck.RequiredChecks.RenderOrReference {
		return errors.New("chat.self_check.required_checks 必须至少要求一次检查与一次渲染或对照")
	}
	conclusion := selfCheck.Conclusion
	if conclusion.MaxItems <= 0 || conclusion.MaxItemCharacters <= 0 || conclusion.MaxSummaryCharacters <= 0 {
		return errors.New("chat.self_check.conclusion 的上限必须为正数")
	}
	reference := selfCheck.Reference
	render := schema.ModelReview.Render
	if len(reference.Views) == 0 || strings.TrimSpace(reference.DefaultView) == "" ||
		strings.TrimSpace(reference.Mode) == "" || reference.Size <= 0 || reference.Supersample <= 0 ||
		reference.MaxPairsPerCall <= 0 || reference.SubjectThreshold <= 0 || reference.NormalizedSide <= 0 {
		return errors.New("chat.self_check.reference 的视角、模式、尺寸与指标参数不能为空或非正数")
	}
	renderViews := map[string]bool{}
	for _, view := range render.Views {
		renderViews[view] = true
	}
	views := map[string]bool{}
	for _, view := range reference.Views {
		if !renderViews[view] || views[view] {
			return fmt.Errorf("chat.self_check.reference.views 含未声明或重复的视角 %q", view)
		}
		views[view] = true
	}
	if !views[reference.DefaultView] {
		return fmt.Errorf("chat.self_check.reference.default_view %q 未在 views 中声明", reference.DefaultView)
	}
	if !slices.Contains(render.Modes, reference.Mode) {
		return fmt.Errorf("chat.self_check.reference.mode %q 未在 model_review.render.modes 中声明", reference.Mode)
	}
	if render.MaxSize > 0 && reference.Size > render.MaxSize {
		return errors.New("chat.self_check.reference.size 不能超过 model_review.render.max_size")
	}
	if render.Supersample > 0 && reference.Supersample > render.Supersample {
		return errors.New("chat.self_check.reference.supersample 不能超过 model_review.render.supersample")
	}
	if schema.ModelReview.Compare.MaxViewsPerCall > 0 &&
		reference.MaxPairsPerCall > schema.ModelReview.Compare.MaxViewsPerCall {
		return errors.New("chat.self_check.reference.max_pairs_per_call 不能超过 model_review.compare.max_views_per_call")
	}
	if reference.Layout != "side_by_side" {
		return errors.New("chat.self_check.reference.layout 必须是 side_by_side")
	}
	if reference.AssetType == "" || reference.AssetType != render.AssetType {
		return errors.New("chat.self_check.reference.asset_type 必须等于 model_review.render.asset_type")
	}
	context := selfCheck.Context
	if context.MaxResults <= 0 || context.MaxCharacters <= 0 ||
		!strings.Contains(context.ContextTemplate, "{results}") ||
		!strings.Contains(context.ContextTemplate, "{rule}") ||
		!strings.Contains(context.TaskLineTemplate, "{task_id}") ||
		!strings.Contains(context.TaskLineTemplate, "{status}") ||
		!strings.Contains(context.TaskLineTemplate, "{rounds_used}") ||
		!strings.Contains(context.TaskLineTemplate, "{rounds_limit}") ||
		!strings.Contains(context.TaskLineTemplate, "{renders_in_round}") ||
		!strings.Contains(context.TaskLineTemplate, "{renders_limit}") ||
		!strings.Contains(context.TaskLineTemplate, "{corrections_used}") ||
		!strings.Contains(context.TaskLineTemplate, "{corrections_limit}") ||
		!strings.Contains(context.TaskLineTemplate, "{background}") ||
		!strings.Contains(context.TaskLineTemplate, "{summary}") ||
		strings.TrimSpace(context.UncheckedRuleText) == "" ||
		strings.TrimSpace(context.BackgroundText) == "" {
		return errors.New("chat.self_check.context 的提示模板与文案不完整")
	}
	texts := []struct {
		name     string
		value    string
		required []string
	}{
		{"check_recorded", selfCheck.Texts.CheckRecorded, []string{"{round}", "{kind}"}},
		{"rounds_remaining", selfCheck.Texts.RoundsRemaining, []string{"{remaining}", "{limit}"}},
		{"renders_remaining", selfCheck.Texts.RendersRemaining, []string{"{remaining}", "{limit}"}},
		{"corrections_remaining", selfCheck.Texts.CorrectionsRemaining, []string{"{remaining}", "{limit}"}},
		{"limit_reached", selfCheck.Texts.LimitReached, nil},
		{"record_saved", selfCheck.Texts.RecordSaved, nil},
		{"record_needs_check", selfCheck.Texts.RecordNeedsCheck, nil},
		{"record_unknown_status", selfCheck.Texts.RecordUnknownStatus, []string{"{status}", "{statuses}"}},
		{"record_not_writable", selfCheck.Texts.RecordNotWritable, []string{"{status}", "{statuses}"}},
		{"record_remaining_required", selfCheck.Texts.RecordRemainingRequired, nil},
		{"record_remaining_forbidden", selfCheck.Texts.RecordRemainingForbidden, nil},
		{"record_too_long", selfCheck.Texts.RecordTooLong, []string{"{field}", "{length}", "{maximum}"}},
		{"record_invalid_arguments", selfCheck.Texts.RecordInvalidArguments, []string{"{detail}"}},
		{"record_unknown_task", selfCheck.Texts.RecordUnknownTask, []string{"{model}"}},
		{"reference_ready", selfCheck.Texts.ReferenceReady, []string{"{model}", "{image}", "{view}", "{iou}"}},
		{"reference_unreadable", selfCheck.Texts.ReferenceUnreadable, []string{"{handle}", "{reason}"}},
		{"reference_no_reference", selfCheck.Texts.ReferenceNoReference, nil},
		{"caveats", selfCheck.Texts.Caveats, nil},
	}
	if selfCheck.RequiredChecks.UsageClean {
		texts = append(texts,
			struct {
				name     string
				value    string
				required []string
			}{"record_usage_failed", selfCheck.Texts.RecordUsageFailed, []string{"{checks}"}},
			struct {
				name     string
				value    string
				required []string
			}{"record_default_purpose", selfCheck.Texts.RecordDefaultPurpose, []string{"{purpose}"}},
		)
	}
	if roles["uncheckable"] {
		texts = append(texts, struct {
			name     string
			value    string
			required []string
		}{"record_uncheckable_unproven", selfCheck.Texts.RecordUncheckableUnproven, []string{"{status}"}})
	}
	for _, text := range texts {
		if strings.TrimSpace(text.value) == "" {
			return fmt.Errorf("chat.self_check.texts.%s 不能为空", text.name)
		}
		for _, placeholder := range text.required {
			if !strings.Contains(text.value, placeholder) {
				return fmt.Errorf("chat.self_check.texts.%s 必须包含 %s", text.name, placeholder)
			}
		}
	}
	if len(selfCheck.Checkpoints) == 0 {
		return errors.New("chat.self_check.checkpoints 至少声明一个检查点")
	}
	checkpoints := map[string]bool{}
	for _, checkpoint := range selfCheck.Checkpoints {
		if strings.TrimSpace(checkpoint.ID) == "" || strings.TrimSpace(checkpoint.Label) == "" {
			return errors.New("chat.self_check.checkpoints 的 id 与 label 不能为空")
		}
		if checkpoints[checkpoint.ID] {
			return fmt.Errorf("chat.self_check.checkpoints 的 id %q 重复", checkpoint.ID)
		}
		checkpoints[checkpoint.ID] = true
	}
	return nil
}

// validateModelReview checks application.chat.model_review (#103) once any
// model review tool is declared: every bound positive, the render vocabulary
// renderable, defaults inside their enumerations, and every Agent-facing text
// present with the placeholders the executor fills. Schemas without the tools
// stay valid.
func validateModelReview(schema ChatSchema, capabilityPresentations []CapabilityPresentation) error {
	used := false
	for _, tool := range schema.AgentTools {
		switch tool.Execution.ImplementationKey {
		case "model.inspect", "model.render", "model.compare", "model.compare_reference":
			used = true
		}
	}
	if !used {
		return nil
	}
	review := schema.ModelReview
	limits := review.Inspect.Limits
	if limits.MaxBytes <= 0 || limits.MaxTriangles <= 0 || limits.MaxVertices <= 0 ||
		limits.MaxTexturePixels <= 0 || limits.MaxNodes <= 0 || limits.MaxDepth <= 0 ||
		review.Inspect.WeldEpsilonRatio <= 0 || review.Inspect.DebrisRatio <= 0 ||
		review.Inspect.MaxListedParts <= 0 {
		return errors.New("chat.model_review.inspect 的限额、比例与部件上限必须为正数")
	}
	if err := validateModelInspectProduction(review.Inspect.Production, schema.Model3DUsage, capabilityPresentations); err != nil {
		return err
	}

	render := review.Render
	if render.Size <= 0 || render.MaxSize < render.Size || render.Supersample <= 0 ||
		render.WireframeMaxTriangles <= 0 || render.Workers <= 0 ||
		render.MaxConcurrentRenders <= 0 || render.MaxViewsPerCall <= 0 ||
		render.MaxRenderCallsPerTurn <= 0 {
		return errors.New("chat.model_review.render 的尺寸、并发与额度必须为正数，size 不能超过 max_size")
	}
	if render.MaxSize*render.Supersample > modelReviewMaxRenderDimension {
		return fmt.Errorf(
			"chat.model_review.render 的 max_size × supersample 不能超过 %d",
			modelReviewMaxRenderDimension,
		)
	}
	if len(render.Views) == 0 || len(render.Modes) == 0 || len(render.DefaultViews) == 0 {
		return errors.New("chat.model_review.render 必须声明 views、modes 与 default_views")
	}
	views := make(map[string]bool, len(render.Views))
	for _, view := range render.Views {
		if !slices.Contains(modelReviewViews, view) || views[view] {
			return fmt.Errorf("chat.model_review.render.views 含未知或重复的视角 %q", view)
		}
		views[view] = true
	}
	modes := make(map[string]bool, len(render.Modes))
	for _, mode := range render.Modes {
		if !slices.Contains(modelReviewModes, mode) || modes[mode] {
			return fmt.Errorf("chat.model_review.render.modes 含未知或重复的模式 %q", mode)
		}
		modes[mode] = true
	}
	for _, view := range render.DefaultViews {
		if !views[view] {
			return fmt.Errorf("chat.model_review.render.default_views 含未声明的视角 %q", view)
		}
	}
	if !modes[render.DefaultMode] {
		return fmt.Errorf("chat.model_review.render.default_mode %q 未在 modes 中声明", render.DefaultMode)
	}
	if strings.TrimSpace(render.AssetType) == "" || !visionFlattenColour.MatchString(render.Background) {
		return errors.New("chat.model_review.render 的 asset_type 不能为空，background 必须是 #rrggbb")
	}

	if review.Compare.MaxViewsPerCall <= 0 || review.Compare.MaxViewsPerCall > render.MaxViewsPerCall ||
		review.Compare.Layout != "side_by_side" {
		return errors.New("chat.model_review.compare 需要正数且不超过 render 上限的 max_views_per_call 与 layout side_by_side")
	}
	if review.Cache.MaxModels <= 0 || review.Cache.MaxBytes <= 0 {
		return errors.New("chat.model_review.cache 的上限必须为正数")
	}
	if strings.TrimSpace(review.ResultViewsField) == "" {
		return errors.New("chat.model_review.result_views_field 不能为空")
	}

	source := review.Source
	if strings.TrimSpace(source.TaskArgument) == "" || strings.TrimSpace(source.AssetArgument) == "" ||
		strings.TrimSpace(source.SourceRequired) == "" || strings.TrimSpace(source.SourceNotFound) == "" ||
		strings.TrimSpace(source.SourceNotReady) == "" || strings.TrimSpace(source.SourceNotModel) == "" ||
		strings.TrimSpace(source.SourceNotGLB) == "" {
		return errors.New("chat.model_review.source 的参数名与文案不能为空")
	}
	if !strings.Contains(source.SourceNotModel, "{asset}") {
		return errors.New("chat.model_review.source.source_not_model 必须包含 {asset}")
	}
	if !strings.Contains(source.SourceNotGLB, "{asset}") || !strings.Contains(source.SourceNotGLB, "{media}") {
		return errors.New("chat.model_review.source.source_not_glb 必须包含 {asset} 与 {media}")
	}

	texts := []struct {
		name     string
		value    string
		required []string
	}{
		{"inspect_ready", review.Texts.InspectReady, []string{"{model}"}},
		{"render_ready", review.Texts.RenderReady, []string{"{count}", "{views}"}},
		{"compare_ready", review.Texts.CompareReady, []string{"{count}", "{views}"}},
		{"images_attached", review.Texts.ImagesAttached, []string{"{count}"}},
		{"vision_off", review.Texts.VisionOff, nil},
		{"budget_remaining", review.Texts.BudgetRemaining, []string{"{remaining}", "{limit}"}},
		{"budget_exhausted", review.Texts.BudgetExhausted, []string{"{limit}"}},
		{"too_large", review.Texts.TooLarge, []string{"{bytes}", "{limit}"}},
		{"unsupported", review.Texts.Unsupported, []string{"{reason}"}},
		{"compressed", review.Texts.Compressed, []string{"{extension}"}},
		{"source_read_failed", review.Texts.SourceReadFailed, []string{"{reason}"}},
		{"upload_failed", review.Texts.UploadFailed, []string{"{reason}"}},
		{"render_failed", review.Texts.RenderFailed, []string{"{view}", "{reason}"}},
		{"wireframe_too_dense", review.Texts.WireframeTooDense, []string{"{triangles}", "{limit}"}},
		{"too_many_views", review.Texts.TooManyViews, []string{"{count}", "{limit}"}},
		{"unknown_view", review.Texts.UnknownView, []string{"{view}", "{views}"}},
		{"unknown_mode", review.Texts.UnknownMode, []string{"{mode}", "{modes}"}},
		{"invalid_arguments", review.Texts.InvalidArguments, []string{"{detail}"}},
		{"parts_required", review.Texts.PartsRequired, nil},
		{"unknown_part", review.Texts.UnknownPart, []string{"{part}"}},
		{"too_many_models", review.Texts.TooManyModels, []string{"{count}"}},
	}
	for _, text := range texts {
		if strings.TrimSpace(text.value) == "" {
			return fmt.Errorf("chat.model_review.texts.%s 不能为空", text.name)
		}
		for _, placeholder := range text.required {
			if !strings.Contains(text.value, placeholder) {
				return fmt.Errorf("chat.model_review.texts.%s 必须包含 %s", text.name, placeholder)
			}
		}
	}
	return nil
}

// validateModelInspectProduction checks model_review.inspect.production: the
// thresholds of the purpose check, its Agent-facing sentences and the fix
// options. Every check has exactly one fix; a dynamic fix names only declared
// capabilities and public parameter values, while a static fix carries no
// placeholder the executor would leave behind.
func validateModelInspectProduction(
	production ChatModelInspectProductionSchema,
	usage ChatModel3DUsageSchema,
	capabilityPresentations []CapabilityPresentation,
) error {
	if production.PivotBottomCenterRatio <= 0 || production.PivotBottomCenterRatio > 1 {
		return errors.New("chat.model_review.inspect.production.pivot_bottom_center_ratio 必须在 (0, 1] 内")
	}
	if production.YUpFlatRatio <= 0 || production.YUpFlatRatio >= 1 {
		return errors.New("chat.model_review.inspect.production.y_up_flat_ratio 必须在 (0, 1) 内")
	}
	uprightProfiles := make(map[string]bool, len(production.YUpUprightProfiles))
	for _, id := range production.YUpUprightProfiles {
		declared := false
		for _, profile := range usage.Profiles {
			declared = declared || profile.ID == id
		}
		if !declared {
			return fmt.Errorf("chat.model_review.inspect.production.y_up_upright_profiles 含未声明的用途 %q", id)
		}
		if uprightProfiles[id] {
			return fmt.Errorf("chat.model_review.inspect.production.y_up_upright_profiles 中 %q 重复", id)
		}
		uprightProfiles[id] = true
	}
	if production.DebrisRatio < 0 || production.DebrisRatio > 1 {
		return errors.New("chat.model_review.inspect.production.debris_ratio 必须在 [0, 1] 内")
	}
	if production.DegenerateRatio < 0 || production.DegenerateRatio > 1 {
		return errors.New("chat.model_review.inspect.production.degenerate_ratio 必须在 [0, 1] 内")
	}

	texts := []struct {
		name     string
		value    string
		required []string
	}{
		{"default_purpose_text", production.DefaultPurposeText, []string{"{purpose}"}},
		{"brief_purpose_text", production.BriefPurposeText, []string{"{purpose}"}},
		{"triangle_override_text", production.TriangleOverrideText, []string{"{value}"}},
		{"no_capability_text", production.NoCapabilityText, nil},
		{"capability_joiner", production.CapabilityJoiner, nil},
		{"option_separator", production.OptionSeparator, nil},
	}
	for _, text := range texts {
		if strings.TrimSpace(text.value) == "" {
			return fmt.Errorf("chat.model_review.inspect.production.%s 不能为空", text.name)
		}
		for _, placeholder := range text.required {
			if !strings.Contains(text.value, placeholder) {
				return fmt.Errorf("chat.model_review.inspect.production.%s 必须包含 %s", text.name, placeholder)
			}
		}
		for _, placeholder := range modelReviewPlaceholder.FindAllString(text.value, -1) {
			if !slices.Contains(text.required, placeholder) {
				return fmt.Errorf("chat.model_review.inspect.production.%s 含未填充的占位符 %s", text.name, placeholder)
			}
		}
	}

	capabilities := make(map[string]bool, len(capabilityPresentations))
	for _, capability := range capabilityPresentations {
		capabilities[string(capability.ID)] = true
	}
	checkIDs := ModelReviewUsageCheckIDs()
	declared := make(map[string]bool, len(production.Fixes))
	for _, fix := range production.Fixes {
		if !slices.Contains(checkIDs, fix.Check) {
			return fmt.Errorf("chat.model_review.inspect.production.fixes 含未知检查 %q", fix.Check)
		}
		if declared[fix.Check] {
			return fmt.Errorf("chat.model_review.inspect.production.fixes 中 %q 重复", fix.Check)
		}
		declared[fix.Check] = true
		if strings.TrimSpace(fix.Template) == "" {
			return fmt.Errorf("chat.model_review.inspect.production.fixes[%s].template 不能为空", fix.Check)
		}
		allowed := map[string]bool{}
		if len(fix.Options) > 0 {
			allowed["{capabilities}"] = true
			if !strings.Contains(fix.Template, "{capabilities}") {
				return fmt.Errorf("chat.model_review.inspect.production.fixes[%s].template 必须包含 {capabilities}", fix.Check)
			}
		}
		if fix.Check == ModelReviewUsageCheckPlausibleScale && len(fix.Options) > 0 {
			allowed["{minimum}"] = true
			allowed["{maximum}"] = true
			for _, placeholder := range []string{"{minimum}", "{maximum}"} {
				if !strings.Contains(fix.Template, placeholder) {
					return fmt.Errorf("chat.model_review.inspect.production.fixes[%s].template 必须包含 %s", fix.Check, placeholder)
				}
			}
		}
		for _, placeholder := range modelReviewPlaceholder.FindAllString(fix.Template, -1) {
			if !allowed[placeholder] {
				return fmt.Errorf("chat.model_review.inspect.production.fixes[%s].template 含未填充的占位符 %s", fix.Check, placeholder)
			}
		}
		for optionIndex, option := range fix.Options {
			if len(option.Capabilities) == 0 {
				return fmt.Errorf("chat.model_review.inspect.production.fixes[%s].options[%d] 没有能力", fix.Check, optionIndex)
			}
			for candidateIndex, candidate := range option.Capabilities {
				if strings.TrimSpace(candidate.Capability) == "" || !capabilities[candidate.Capability] {
					return fmt.Errorf(
						"chat.model_review.inspect.production.fixes[%s].options[%d].capabilities[%d] 的能力 %q 未声明",
						fix.Check, optionIndex, candidateIndex, candidate.Capability,
					)
				}
				if (candidate.Parameter == "") != (candidate.Value == "") {
					return fmt.Errorf(
						"chat.model_review.inspect.production.fixes[%s].options[%d].capabilities[%d] 的 parameter 与 value 必须同时为空或同时非空",
						fix.Check, optionIndex, candidateIndex,
					)
				}
			}
		}
	}
	for _, check := range checkIDs {
		if !declared[check] {
			return fmt.Errorf("chat.model_review.inspect.production.fixes 缺少检查 %q", check)
		}
	}
	return nil
}

// validateChatVision checks the purpose-based vision budget (#247). The whole
// block is optional: a Catalog without it keeps the legacy behavior.
func validateChatVision(schema ChatSchema) error {
	vision := schema.Context.Vision
	if vision == nil {
		return nil
	}
	if vision.MaximumImages <= 0 || vision.MaximumTokens <= 0 || vision.MaximumBytes <= 0 {
		return errors.New("context.vision 的 maximum_images、maximum_tokens 与 maximum_bytes 必须为正数")
	}
	if len(vision.Roles) == 0 {
		return errors.New("context.vision.roles 不能为空")
	}
	roleIDs := map[string]bool{}
	seenSources := map[string]bool{}
	for index, role := range vision.Roles {
		if strings.TrimSpace(role.ID) == "" || roleIDs[role.ID] {
			return fmt.Errorf("context.vision.roles[%d] 的 id 为空或重复", index)
		}
		roleIDs[role.ID] = true
		if len(role.Sources) == 0 {
			return fmt.Errorf("context.vision.roles[%d] 没有 sources", index)
		}
		for _, source := range role.Sources {
			switch source {
			case ChatVisionSourceCanvas, ChatVisionSourceTurn, ChatVisionSourceStyle,
				ChatVisionSourceRender, ChatVisionSourceGenerated, ChatVisionSourceHistory:
			default:
				return fmt.Errorf("context.vision.roles[%d] 的 source %q 无效", index, source)
			}
			if seenSources[source] {
				return fmt.Errorf("context.vision 的 source %q 被多个角色声明", source)
			}
			seenSources[source] = true
		}
		if role.MaximumImages <= 0 {
			return fmt.Errorf("context.vision.roles[%d].maximum_images 必须为正数", index)
		}
		if !validVisionDetail(role.Detail) {
			return fmt.Errorf("context.vision.roles[%d].detail %q 无效", index, role.Detail)
		}
		if role.TokenCost <= 0 {
			return fmt.Errorf("context.vision.roles[%d].token_cost 必须为正数", index)
		}
	}
	if !seenSources[ChatVisionSourceGenerated] || !seenSources[ChatVisionSourceHistory] {
		return errors.New("context.vision.roles 必须声明 generated 与 history 角色")
	}
	for name, template := range map[string]string{
		"overflow_template":             vision.OverflowTemplate,
		"vision_off_note":               vision.VisionOffNote,
		"style_anchor_intro":            vision.StyleAnchorIntro,
		"style_anchor_label_template":   vision.StyleAnchorLabelTemplate,
		"style_anchor_dropped_template": vision.StyleAnchorDroppedTemplate,
		"unnamed_picture_label":         vision.UnnamedPictureLabel,
	} {
		if strings.TrimSpace(template) == "" {
			return fmt.Errorf("context.vision.%s 不能为空", name)
		}
	}
	if !strings.Contains(vision.OverflowTemplate, "{handles}") {
		return errors.New("context.vision.overflow_template 必须包含 {handles}")
	}
	if !strings.Contains(vision.StyleAnchorLabelTemplate, "{index}") {
		return errors.New("context.vision.style_anchor_label_template 必须包含 {index}")
	}
	if !strings.Contains(vision.StyleAnchorDroppedTemplate, "{index}") {
		return errors.New("context.vision.style_anchor_dropped_template 必须包含 {index}")
	}
	if err := validateVisionContactSheet(vision.ContactSheet); err != nil {
		return err
	}
	if err := validateVisionCrop(schema, vision.Crop); err != nil {
		return err
	}
	prefixes := map[string]bool{}
	for index, profile := range vision.ModelProfiles {
		if len(profile.ModelPrefixes) == 0 {
			return fmt.Errorf("context.vision.model_profiles[%d] 没有 model_prefixes", index)
		}
		for _, prefix := range profile.ModelPrefixes {
			prefix = strings.ToLower(strings.TrimSpace(prefix))
			if prefix == "" || prefixes[prefix] {
				return fmt.Errorf("context.vision.model_profiles[%d] 的 model_prefixes 为空或重复", index)
			}
			prefixes[prefix] = true
		}
		if profile.MaximumImages < 0 || profile.MaximumImages > vision.MaximumImages {
			return fmt.Errorf("context.vision.model_profiles[%d].maximum_images 必须在 0 与 maximum_images 之间", index)
		}
		if profile.MaximumTokens < 0 || profile.MaximumTokens > vision.MaximumTokens {
			return fmt.Errorf("context.vision.model_profiles[%d].maximum_tokens 必须在 0 与 maximum_tokens 之间", index)
		}
	}
	return nil
}

func validVisionDetail(detail string) bool {
	switch detail {
	case "low", "high", "auto":
		return true
	default:
		return false
	}
}

func validateVisionContactSheet(sheet ChatVisionContactSheetSchema) error {
	if !sheet.Enabled {
		return nil
	}
	if sheet.MaximumTiles <= 0 || sheet.Columns <= 0 || sheet.TilePixels <= 0 || sheet.LabelHeight <= 0 ||
		sheet.MaximumBytes <= 0 || !validVisionDetail(sheet.Detail) || sheet.TokenCost <= 0 {
		return errors.New("context.vision.contact_sheet 的启用配置无效")
	}
	if len(sheet.JPEGQualities) == 0 || strings.TrimSpace(sheet.Intro) == "" {
		return errors.New("context.vision.contact_sheet 的 jpeg_qualities 与 intro 不能为空")
	}
	for _, quality := range sheet.JPEGQualities {
		if quality <= 0 || quality > 100 {
			return errors.New("context.vision.contact_sheet.jpeg_qualities 必须在 1 与 100 之间")
		}
	}
	if !strings.Contains(sheet.LabelTemplate, "{handle}") {
		return errors.New("context.vision.contact_sheet.label_template 必须包含 {handle}")
	}
	return nil
}

func validateVisionCrop(schema ChatSchema, crop ChatVisionCropSchema) error {
	if !crop.Enabled {
		return nil
	}
	if !validVisionDetail(crop.Detail) || crop.TokenCost <= 0 ||
		strings.TrimSpace(crop.RegionArgument) == "" || strings.TrimSpace(crop.ShowPictureArgument) == "" ||
		crop.RegionArgument == crop.ShowPictureArgument ||
		crop.MinimumSidePixels <= 0 || crop.MaximumPixels <= 0 {
		return errors.New("context.vision.crop 的启用配置无效")
	}
	if !strings.Contains(crop.TooLargeTemplate, "{maximum}") {
		return errors.New("context.vision.crop.too_large_template 必须包含 {maximum}")
	}
	if !strings.Contains(crop.PictureLabelTemplate, "{handle}") ||
		!strings.Contains(crop.PictureLabelTemplate, "{region}") {
		return errors.New("context.vision.crop.picture_label_template 必须包含 {handle} 与 {region}")
	}
	if !strings.Contains(crop.OriginalPictureLabelTemplate, "{handle}") ||
		!strings.Contains(crop.OriginalPictureLabelTemplate, "{width}") ||
		!strings.Contains(crop.OriginalPictureLabelTemplate, "{height}") {
		return errors.New("context.vision.crop.original_picture_label_template 必须包含 {handle}、{width} 与 {height}")
	}
	check := func(parameters json.RawMessage) error {
		var declared struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if len(parameters) == 0 || json.Unmarshal(parameters, &declared) != nil {
			return errors.New("context.vision.crop 启用时 inspect_image 必须声明参数")
		}
		for _, name := range []string{crop.RegionArgument, crop.ShowPictureArgument} {
			if _, exists := declared.Properties[name]; !exists {
				return fmt.Errorf("context.vision.crop 启用时 inspect_image 必须声明参数 %q", name)
			}
		}
		return nil
	}
	for _, tool := range schema.Tools {
		if tool.Name == "inspect_image" {
			return check(tool.Parameters)
		}
	}
	for _, tool := range schema.AgentTools {
		if tool.Name == "inspect_image" {
			return check(tool.Parameters)
		}
	}
	return errors.New("context.vision.crop 启用时必须声明 inspect_image 工具")
}
