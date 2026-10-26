package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

func TestChatReferenceValidationMessagesAreRequired(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		old  string
		new  string
	}{
		{"missing array message", `"invalid_reference_indices": "参考图序号必须是数组"`, `"invalid_reference_indices": ""`},
		{"missing index placeholder", `"reference_index_out_of_range_template": "参考图序号 {index} 无效；当前共有 {count} 张图片"`, `"reference_index_out_of_range_template": "当前共有 {count} 张图片"`},
		{"missing count placeholder", `"reference_index_out_of_range_template": "参考图序号 {index} 无效；当前共有 {count} 张图片"`, `"reference_index_out_of_range_template": "参考图序号 {index} 无效"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := bytes.Replace(source, []byte(test.old), []byte(test.new), 1)
			if bytes.Equal(changed, source) {
				t.Fatalf("missing fixture field %q", test.old)
			}
			if _, err := catalog.Load(bytes.NewReader(changed)); err == nil {
				t.Fatal("invalid chat reference message was accepted")
			}
		})
	}
}

func TestDefaultProductSchemaOwnsChatRuntimeWireAndPresentation(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := taskCatalog.Application().Chat

	if chat.Defaults.Title == "" ||
		chat.State.PendingContent == "" ||
		chat.Agent.AdapterKey == "" ||
		chat.Agent.ModelSetting == "" {
		t.Fatalf("incomplete chat schema: %#v", chat)
	}
	if chat.API.Fields.Content == "" ||
		chat.API.Fields.ImageURLs == "" ||
		chat.API.ResponseFields.MessageID == "" {
		t.Fatalf("incomplete chat wire schema: %#v", chat.API)
	}
	if chat.Stream.RetryMilliseconds <= 0 ||
		chat.Stream.HeartbeatMilliseconds <= 0 ||
		chat.Stream.EventFields.Type == "" {
		t.Fatalf("incomplete chat stream schema: %#v", chat.Stream)
	}
	if chat.Frontend.ConnectionTimeoutMilliseconds <= 0 ||
		chat.Frontend.FallbackPollMilliseconds <= 0 ||
		chat.Frontend.ReconnectCheckDelayMilliseconds <= 0 ||
		chat.Frontend.TextareaMaximumHeightPixels <= 0 ||
		chat.Frontend.DownloadFilenameMaxCharacters <= 0 ||
		chat.Frontend.DownloadTaskIDCharacters <= 0 ||
		chat.Frontend.ProgressMaximum <= 0 {
		t.Fatalf("incomplete chat frontend schema: %#v", chat.Frontend)
	}
	for name, value := range map[string]string{
		"option storage key":       chat.Frontend.OptionStorageKey,
		"add-images event":         chat.Frontend.AddImagesEventName,
		"image MIME prefix":        chat.Frontend.ImageMIMEPrefix,
		"image file accept":        chat.Frontend.ImageFileAccept,
		"skill feature":            chat.Skills.Feature,
		"send key":                 chat.Frontend.SendKey,
		"send modifier":            chat.Frontend.SendModifier,
		"lightbox close key":       chat.Frontend.LightboxCloseKey,
		"lightbox previous key":    chat.Frontend.LightboxPreviousKey,
		"lightbox next key":        chat.Frontend.LightboxNextKey,
		"optimistic ID prefix":     chat.Frontend.OptimisticMessageIDPrefix,
		"optimistic user ID":       chat.Frontend.OptimisticUserMessageID,
		"optimistic assistant ID":  chat.Frontend.OptimisticAssistantMessageID,
		"filename query parameter": chat.Frontend.FilenameQueryParameter,
		"download extension":       chat.Frontend.DownloadExtension,
		"fallback download ID":     chat.Frontend.FallbackDownloadIdentifier,
		"internal API path prefix": chat.Frontend.InternalAPIPathPrefix,
		"hidden content prefix":    chat.Frontend.LegacyHiddenContentPrefix,
		"hidden content suffix":    chat.Frontend.LegacyHiddenContentSuffix,
	} {
		if value == "" {
			t.Fatalf("missing chat.frontend %s", name)
		}
	}
	if len(chat.Tools) == 0 ||
		chat.Tools[0].Name == "" ||
		chat.Tools[0].Capability == "" {
		t.Fatalf("incomplete chat tool schema: %#v", chat.Tools)
	}
	if chat.Presentation.Messages.SessionNotFound == "" ||
		chat.Presentation.Messages.EmptyMessage == "" ||
		chat.Presentation.Status.Generating == "" {
		t.Fatalf(
			"incomplete chat presentation schema: %#v",
			chat.Presentation,
		)
	}
	for _, key := range []string{
		"ui.chat.pending.generation",
		"ui.chat.pending.edit",
		"ui.chat.pending.template",
		"ui.chat.toast.load_messages_failed",
		"ui.chat.toast.create_session_failed",
		"ui.chat.toast.request_failed",
		"ui.chat.toast.send_failed",
		"ui.chat.toast.delete_message_retry",
		"ui.chat.toast.delete_failed",
		"ui.chat.header.title",
		"ui.chat.header.subtitle",
		"ui.chat.header.close",
		"ui.chat.empty.title",
		"ui.chat.empty.description",
		"ui.chat.empty.example.1",
		"ui.chat.empty.example.2",
		"ui.chat.empty.example.3",
		"ui.chat.empty.example.4",
		"ui.chat.loading",
		"ui.chat.history.load_earlier",
		"ui.chat.history.loading_earlier",
		"ui.chat.history.load_earlier_failed",
		"ui.chat.sessions.create",
		"ui.chat.sessions.empty",
		"ui.chat.sessions.delete_title",
		"ui.chat.lightbox.close",
		"ui.chat.lightbox.previous",
		"ui.chat.lightbox.next",
		"ui.chat.lightbox.image_alt",
		"ui.chat.lightbox.jump",
		"ui.chat.message.image_basename",
		"ui.chat.message.resend",
		"ui.chat.message.delete",
		"ui.chat.message.generating",
		"ui.chat.message.uploaded_image",
		"ui.chat.message.generated_image",
		"ui.chat.message.processing",
		"ui.chat.input.upload_limit",
		"ui.chat.input.upload_limit_dropped",
		"ui.chat.input.images_added",
		"ui.chat.input.history_reference_limit",
		"ui.chat.input.enhance.enter_prompt",
		"ui.chat.input.enhance.failed",
		"ui.chat.input.enhance.no_change",
		"ui.chat.input.enhance.success",
		"ui.chat.input.enhance.generic_failed",
		"ui.chat.input.file.not_image",
		"ui.chat.input.file.too_large",
		"ui.chat.input.paste.too_large",
		"ui.chat.input.skill_locked",
		"ui.chat.input.attachment_alt",
		"ui.chat.input.remove_attachment",
		"ui.chat.input.multi_image_hint",
		"ui.chat.input.cancel_reference",
		"ui.chat.input.history_image_alt",
		"ui.chat.input.reference_chip",
		"ui.chat.input.clear",
		"ui.chat.input.upload_title",
		"ui.chat.input.upload",
		"ui.chat.input.history_reference_title",
		"ui.chat.input.history_reference",
		"ui.chat.input.history_reference_selected",
		"ui.chat.input.history_picker_title",
		"ui.chat.input.source.user",
		"ui.chat.input.source.generated",
		"ui.chat.input.legend.user_color",
		"ui.chat.input.legend.user_description",
		"ui.chat.input.legend.separator",
		"ui.chat.input.legend.generated_color",
		"ui.chat.input.legend.generated_description",
		"ui.chat.input.skill_title",
		"ui.chat.input.skill",
		"ui.chat.input.enhance_title",
		"ui.chat.input.enhance",
		"ui.chat.input.placeholder.edit",
		"ui.chat.input.placeholder.generate",
		"ui.chat.input.send",
		"ui.chat.input.model_selection",
		"ui.chat.input.recommended",
		"ui.chat.input.parameter.aspect_ratio",
		"ui.chat.input.parameter.size",
		"ui.chat.input.parameter.resolution",
		"ui.chat.input.parameter.pose_mode",
		"ui.chat.input.canvas_warning_title",
		"ui.chat.input.canvas_warning",
		"ui.chat.input.retry",
		"ui.chat.skills.category",
		"ui.chat.skills.loading",
		"ui.chat.skills.empty",
		"ui.chat.skills.load_failed",
		"ui.chat.skills.collapse",
		"ui.chat.skills.expand",
		"ui.chat.skills.use",
		"ui.chat.skills.badge",
		"ui.chat.skills.remove",
	} {
		if taskCatalog.Application().Content.Messages[key] == "" {
			t.Fatalf("missing configured Chat UI text %q", key)
		}
	}
}

func TestApplicationSchemaRejectsSkillPromptTemplateWithoutDeclaredFields(
	t *testing.T,
) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	application.Chat.Skills.Execution.PromptTemplates[generation.Capability("text-to-image")] = "template without configured fields"

	err = catalog.ValidateApplicationSchema(application)
	if err == nil || !strings.Contains(err.Error(), "skill prompt template") {
		t.Fatalf("validation error = %v", err)
	}
}

func TestDefaultProductSchemaOwnsSkillExecutionBehavior(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := taskCatalog.Application().Chat
	execution := chat.Skills.Execution
	if execution.PromptTemplateFields.UserInput != "{user_input}" ||
		execution.PromptTemplateFields.SkillPrompt != "{skill_prompt}" {
		t.Fatalf(
			"skill prompt template fields = %#v",
			execution.PromptTemplateFields,
		)
	}
	for _, capability := range []generation.Capability{generation.TextToImage, generation.ImageToImage} {
		template := execution.PromptTemplates[capability]
		if !strings.HasPrefix(template, execution.PromptTemplateFields.UserInput) ||
			!strings.HasSuffix(template, execution.PromptTemplateFields.SkillPrompt) {
			t.Fatalf("%s template lost user intent or selected Skill content", capability)
		}
		policy := strings.Index(template, "Composition priority:")
		reference := strings.Index(template, "Selected Skill reference:")
		if policy < 0 || reference <= policy ||
			!strings.Contains(template, "only when the user explicitly requests that scene") {
			t.Fatalf("%s template lacks subject priority before scene reference", capability)
		}
	}
	quality, exists := execution.ParameterOverrides[generation.SemanticQuality]
	if !exists || !json.Valid(quality) || string(quality) != `"max"` {
		t.Fatalf("skill parameter overrides = %#v", execution.ParameterOverrides)
	}
}

func TestApplicationSchemaRejectsSkillOverrideOfSourceSemantics(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	document, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for semantic, raw := range map[schemacontract.ParameterSemantic]json.RawMessage{
		schemacontract.SemanticSourceTask:  json.RawMessage(`"task-1"`),
		schemacontract.SemanticSourceAsset: json.RawMessage(`"asset-1"`),
		schemacontract.SemanticAction:      json.RawMessage(`1`),
	} {
		application := taskCatalog.Application()
		// Application() 的 clone 不深拷 Chat.Skills.Execution，这里显式换新
		// map，避免把越界 override 写回共享目录对象。
		overrides := map[schemacontract.ParameterSemantic]json.RawMessage{}
		for declared, declaredRaw := range application.Chat.Skills.Execution.ParameterOverrides {
			overrides[declared] = declaredRaw
		}
		overrides[semantic] = raw
		application.Chat.Skills.Execution.ParameterOverrides = overrides

		err = catalog.ValidateSkillCatalogAgainstProductSchema(
			document,
			application,
			taskCatalog.List(""),
		)
		if err == nil ||
			!strings.Contains(err.Error(), "skill 可覆盖闭包") {
			t.Fatalf("semantic %q 校验错误 = %v", semantic, err)
		}
	}
}

const defaultApprovalPolicyJSON = `"approval_policy": {
        "default_mode": "manual",
        "modes": ["auto", "manual"],
        "manual_approval_capabilities": ["text-to-3d", "image-to-3d", "image-to-splat", "retexture"]
      }`

func TestApprovalPolicyValidationRejectsUnsafeDeclarations(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	for name, testCase := range map[string]struct{ replacement, detail string }{
		"no modes":     {`"approval_policy": {"default_mode": "manual", "modes": [], "manual_approval_capabilities": []}`, "modes"},
		"unknown mode": {`"approval_policy": {"default_mode": "manual", "modes": ["manual", "yolo"], "manual_approval_capabilities": []}`, "yolo"},
		"manual mode missing": {
			`"approval_policy": {"default_mode": "auto", "modes": ["auto"], "manual_approval_capabilities": []}`, "manual"},
		"default not offered": {
			`"approval_policy": {"default_mode": "auto", "modes": ["manual"], "manual_approval_capabilities": []}`, "default_mode"},
		"unknown capability": {
			`"approval_policy": {"default_mode": "manual", "modes": ["auto", "manual"], "manual_approval_capabilities": ["text-to-video"]}`, "text-to-video"},
		"duplicate capability": {
			`"approval_policy": {"default_mode": "manual", "modes": ["auto", "manual"], "manual_approval_capabilities": ["text-to-3d", "text-to-3d"]}`, "重复"},
	} {
		changed := bytes.Replace(source, []byte(defaultApprovalPolicyJSON), []byte(testCase.replacement), 1)
		if bytes.Equal(changed, source) {
			t.Fatalf("%s: default approval policy fixture not found", name)
		}
		_, err := catalog.Load(bytes.NewReader(changed))
		if err == nil || !strings.Contains(err.Error(), testCase.detail) {
			t.Errorf("%s: error = %v, want detail %q", name, err, testCase.detail)
		}
	}
}

func TestDefaultCatalogApprovalPolicyMatchesProductRule(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	declared := application.Chat.ApprovalPolicy
	manual := make([]string, 0, len(declared.ManualApprovalCapabilities))
	for _, capability := range declared.ManualApprovalCapabilities {
		manual = append(manual, string(capability))
	}
	known := make([]string, 0, len(application.Capabilities))
	for _, capability := range application.Capabilities {
		known = append(known, string(capability.ID))
	}
	policy := approvalpolicy.New(declared.DefaultMode, declared.Modes, manual, known)
	if policy.DefaultMode != "manual" {
		t.Fatalf("default mode = %q, want manual", policy.DefaultMode)
	}
	humanCapabilities := map[string]bool{"text-to-3d": true, "image-to-3d": true, "image-to-splat": true, "retexture": true}
	for _, capability := range []string{"text-to-image", "image-to-image", "background-removal",
		"remesh", "rigging", "animation", "format-conversion", "text-to-3d", "image-to-3d", "image-to-splat", "retexture"} {
		if !slices.Contains(policy.KnownCapabilities, capability) {
			t.Fatalf("default Schema does not declare capability %s", capability)
		}
		if got := approvalpolicy.RequiresHuman(policy, "manual", capability); got != humanCapabilities[capability] {
			t.Errorf("manual %s requires human = %v, want %v", capability, got, humanCapabilities[capability])
		}
		if approvalpolicy.RequiresHuman(policy, "auto", capability) {
			t.Errorf("auto mode waits for a human on %s", capability)
		}
	}
}
