package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// validateProject validates application.project (#240). A Catalog without the
// section stays valid: every check is skipped so historical documents load
// unchanged.
func validateProject(
	project *ProjectSchema,
	chat ChatSchema,
	runtime RuntimeSchema,
	content ContentSchema,
) error {
	if project == nil {
		return nil
	}
	limits := project.Limits
	for name, value := range map[string]int{
		"maximum_projects_per_owner":           limits.MaximumProjectsPerOwner,
		"maximum_name_characters":              limits.MaximumNameCharacters,
		"maximum_assets_per_project":           limits.MaximumAssetsPerProject,
		"maximum_asset_name_characters":        limits.MaximumAssetNameCharacters,
		"maximum_asset_description_characters": limits.MaximumAssetDescriptionCharacters,
		"maximum_asset_images":                 limits.MaximumAssetImages,
		"maximum_asset_models":                 limits.MaximumAssetModels,
		"maximum_list_items":                   limits.MaximumListItems,
	} {
		if value <= 0 {
			return fmt.Errorf("project.limits.%s 必须为正数", name)
		}
	}
	style := project.StyleGuide
	if style.MaximumTextCharacters <= 0 ||
		style.MaximumReferences <= 0 ||
		style.MaximumPaletteColors <= 0 ||
		strings.TrimSpace(style.PaletteColorPattern) == "" {
		return errors.New("project.style_guide 的文本、参考图、色板上限必须为正数，并声明色板颜色规则")
	}
	if _, err := regexp.Compile(style.PaletteColorPattern); err != nil {
		return fmt.Errorf("project.style_guide.palette_color_pattern 无法编译: %w", err)
	}

	kinds := make(map[string]bool, len(project.Assets.Kinds))
	kindPattern := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	for index, kind := range project.Assets.Kinds {
		if !kindPattern.MatchString(kind.ID) {
			return fmt.Errorf("project.assets.kinds[%d].id 必须为小写标识: %q", index, kind.ID)
		}
		if kinds[kind.ID] {
			return fmt.Errorf("project.assets.kinds 种类重复: %q", kind.ID)
		}
		kinds[kind.ID] = true
		if strings.TrimSpace(kind.LabelKey) == "" || strings.TrimSpace(content.Messages[kind.LabelKey]) == "" {
			return fmt.Errorf("project.assets.kinds[%d].label_key 不是有文案的 content.messages key: %q", index, kind.LabelKey)
		}
	}
	if len(project.Assets.Kinds) == 0 {
		return errors.New("project.assets.kinds 不能为空")
	}

	briefFields := make(map[string]string, len(chat.Model3DUsage.Brief.Fields))
	for _, field := range chat.Model3DUsage.Brief.Fields {
		briefFields[field.Name] = field.Type
	}
	conventions := project.Conventions
	fieldNames := make(map[string]bool, len(conventions.Fields))
	for index, field := range conventions.Fields {
		if strings.TrimSpace(field.Name) == "" || strings.TrimSpace(field.Type) == "" {
			return fmt.Errorf("project.conventions.fields[%d] 缺少 name/type", index)
		}
		if fieldNames[field.Name] {
			return fmt.Errorf("project.conventions.fields 字段重复: %q", field.Name)
		}
		declared, exists := briefFields[field.Name]
		if !exists || declared != field.Type {
			return fmt.Errorf("project.conventions.fields[%d] 不是 chat.model3d_usage.brief.fields 的同名同类型子集: %q/%q", index, field.Name, field.Type)
		}
		fieldNames[field.Name] = true
	}
	textNames := make(map[string]bool, len(conventions.Texts))
	for index, text := range conventions.Texts {
		if !kindPattern.MatchString(text.Name) {
			return fmt.Errorf("project.conventions.texts[%d].name 必须为小写标识: %q", index, text.Name)
		}
		if textNames[text.Name] || fieldNames[text.Name] {
			return fmt.Errorf("project.conventions.texts 名称重复或与简报字段重名: %q", text.Name)
		}
		textNames[text.Name] = true
		if text.MaximumCharacters <= 0 {
			return fmt.Errorf("project.conventions.texts[%d].maximum_characters 必须为正数", index)
		}
		if strings.TrimSpace(text.LabelKey) == "" || strings.TrimSpace(content.Messages[text.LabelKey]) == "" {
			return fmt.Errorf("project.conventions.texts[%d].label_key 不是有文案的 content.messages key: %q", index, text.LabelKey)
		}
	}
	if len(conventions.Fields) == 0 && len(conventions.Texts) == 0 {
		return errors.New("project.conventions 必须声明至少一个字段或文本约定")
	}

	if agent := project.Agent; agent != nil {
		if err := validateProjectAgent(*agent, chat, runtime); err != nil {
			return err
		}
	}
	audit := project.Audit
	for name, action := range map[string]string{
		"create_action":         audit.CreateAction,
		"update_action":         audit.UpdateAction,
		"delete_action":         audit.DeleteAction,
		"promote_action":        audit.PromoteAction,
		"project_resource_type": audit.ProjectResourceType,
		"asset_resource_type":   audit.AssetResourceType,
	} {
		if strings.TrimSpace(action) == "" {
			return fmt.Errorf("project.audit.%s 不能为空", name)
		}
	}
	messages := project.Messages
	for name, message := range map[string]string{
		"name_required":            messages.NameRequired,
		"name_too_long":            messages.NameTooLong,
		"project_limit":            messages.ProjectLimit,
		"asset_limit":              messages.AssetLimit,
		"style_text_too_long":      messages.StyleTextTooLong,
		"palette_color_invalid":    messages.PaletteColorInvalid,
		"reference_limit":          messages.ReferenceLimit,
		"reference_invalid":        messages.ReferenceInvalid,
		"unknown_project":          messages.UnknownProject,
		"asset_not_found":          messages.AssetNotFound,
		"promotion_source_invalid": messages.PromotionSourceInvalid,
		"style_guide_inherited":    messages.StyleGuideInherited,
		"style_guide_overridden":   messages.StyleGuideOverridden,
		"style_guide_off":          messages.StyleGuideOff,
	} {
		if strings.TrimSpace(message) == "" {
			return fmt.Errorf("project.messages.%s 不能为空", name)
		}
	}
	return validateProjectRoutes(runtime.HTTP.Routes, project)
}

func validateProjectAgent(agent ProjectAgentSchema, chat ChatSchema, runtime RuntimeSchema) error {
	if strings.TrimSpace(agent.ToolName) == "" {
		return errors.New("project.agent.tool_name 不能为空")
	}
	var tool *ChatAgentToolSchema
	for index := range chat.AgentTools {
		if chat.AgentTools[index].Name == agent.ToolName {
			tool = &chat.AgentTools[index]
			break
		}
	}
	if tool == nil {
		return fmt.Errorf("project.agent.tool_name %q 不是已声明的 agent_tool", agent.ToolName)
	}
	if tool.Execution.ImplementationKey != "project.list" ||
		tool.Execution.AgentScope != "utility" ||
		len(tool.Execution.Effects) != 1 || tool.Execution.Effects[0] != "READ" {
		return fmt.Errorf("project.agent.tool_name %q 必须是 project.list/READ/utility 工具", agent.ToolName)
	}
	var parameters map[string]any
	if json.Unmarshal(tool.Parameters, &parameters) != nil || len(parameters) == 0 {
		return fmt.Errorf("project.agent.tool_name %q 必须声明空对象参数", agent.ToolName)
	}
	for name, template := range map[string]string{
		"context_template":       agent.ContextTemplate,
		"asset_line_template":    agent.AssetLineTemplate,
		"citation_template":      agent.CitationTemplate,
		"unknown_asset_template": agent.UnknownAssetTemplate,
		"list_limit_template":    agent.ListLimitTemplate,
		"style_prompt_template":  agent.StylePromptTemplate,
	} {
		if strings.TrimSpace(template) == "" {
			return fmt.Errorf("project.agent.%s 不能为空", name)
		}
	}
	if !strings.Contains(agent.ContextTemplate, "{assets}") {
		return errors.New("project.agent.context_template 必须包含 {assets}")
	}
	if !strings.Contains(agent.StylePromptTemplate, "{style}") {
		return errors.New("project.agent.style_prompt_template 必须包含 {style}")
	}
	if strings.TrimSpace(agent.HandlePrefix) == "" {
		return errors.New("project.agent.handle_prefix 不能为空")
	}
	for kind, prefix := range chat.Context.HandlePrefixes {
		if prefix == agent.HandlePrefix {
			return fmt.Errorf("project.agent.handle_prefix 与会话 %s 句柄前缀冲突: %q", kind, prefix)
		}
	}
	if agent.ListLimit <= 0 {
		return errors.New("project.agent.list_limit 必须为正数")
	}
	return nil
}

func validateProjectRoutes(routes CoreHTTPRoutesSchema, project *ProjectSchema) error {
	declared := map[string]struct {
		route      string
		parameters []string
	}{
		"project":              {routes.Project, nil},
		"project_item":         {routes.ProjectItem, []string{"projectID"}},
		"project_assets":       {routes.ProjectAssets, []string{"projectID"}},
		"project_asset_item":   {routes.ProjectAssetItem, []string{"projectID", "assetID"}},
		"project_style_guide":  {routes.ProjectStyleGuide, []string{"projectID"}},
		"project_conventions":  {routes.ProjectConventions, []string{"projectID"}},
		"project_promote":      {routes.ProjectPromote, []string{"projectID"}},
		"chat_session_project": {routes.ChatSessionProject, []string{"sessionID"}},
	}
	for name, route := range declared {
		if strings.TrimSpace(route.route) == "" {
			return fmt.Errorf("application.project 需要 runtime.http.routes.%s", name)
		}
		if !configuredRouteParametersMatch(route.route, route.parameters) {
			return fmt.Errorf("http project route %s 参数必须恰好为 %v", name, route.parameters)
		}
	}
	return nil
}
