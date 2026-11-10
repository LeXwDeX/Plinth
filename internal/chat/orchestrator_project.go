package chat

import (
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// projectPrompt renders the project block of the system prompt (#240). It is
// built only from the turn's frozen project snapshot; a project edit applies
// to the next turn. The block states the guide, the conventions and one line
// per asset handle, bounded by the Schema's list limit. Full details stay in
// the list_project_assets tool.
func (orchestrator *Orchestrator) projectPrompt(options TurnOptions) string {
	context := options.ProjectContext
	if context == nil || orchestrator.project == nil || orchestrator.project.Agent == nil {
		return ""
	}
	agent := *orchestrator.project.Agent
	style := "未启用"
	if context.StyleGuide.Enabled {
		if text := strings.TrimSpace(context.StyleGuide.Text); text != "" {
			style = text
		} else if len(context.StyleGuide.References) > 0 {
			style = "（仅参考图）"
		} else {
			style = "未启用"
		}
	}
	messages := orchestrator.project.Messages
	switch options.StyleLock.Source {
	case StyleLockSourceOff:
		style += "\n" + messages.StyleGuideOff
	case StyleLockSourceSession:
		style += "\n" + messages.StyleGuideOverridden
	default:
		style += "\n" + strings.ReplaceAll(messages.StyleGuideInherited, "{project}", context.Name)
	}
	conventions := orchestrator.projectConventionsText(context)
	assets := orchestrator.projectAssetLines(agent, context)
	return strings.NewReplacer(
		"{project}", context.Name,
		"{handle_prefix}", agent.HandlePrefix,
		"{style}", style,
		"{conventions}", conventions,
		"{assets}", assets,
		"{tool}", agent.ToolName,
	).Replace(agent.ContextTemplate)
}

// projectConventionsText renders the project's production defaults with the
// same brief field table a session uses, plus the declared free-form texts.
func (orchestrator *Orchestrator) projectConventionsText(context *ProjectContext) string {
	var parts []string
	if brief := orchestrator.productionBriefText(context.Conventions.Brief,
		orchestrator.schema.Model3DUsage.Brief); brief != "" {
		parts = append(parts, brief)
	}
	names := make([]string, 0, len(context.Conventions.Texts))
	if orchestrator.project != nil {
		for _, declared := range orchestrator.project.Conventions.Texts {
			if value := strings.TrimSpace(context.Conventions.Texts[declared.Name]); value != "" {
				names = append(names, declared.Name+"："+value)
			}
		}
	}
	parts = append(parts, names...)
	if len(parts) == 0 {
		return "无"
	}
	return strings.Join(parts, "；")
}

// projectAssetLines names each frozen asset by the handle the Agent may pass
// to a generation tool. Only the summary is rendered; a long project does not
// push the conversation out of the context window.
func (orchestrator *Orchestrator) projectAssetLines(agent catalog.ProjectAgentSchema, context *ProjectContext) string {
	assets := context.Assets
	limit := agent.ListLimit
	if limit > 0 && len(assets) > limit {
		assets = assets[:limit]
	}
	lines := make([]string, 0, len(assets))
	for _, asset := range assets {
		description := ""
		if asset.Description != "" {
			description = "（" + asset.Description + "）"
		}
		lines = append(lines, strings.NewReplacer(
			"{handle}", asset.Handle,
			"{kind}", asset.Kind,
			"{name}", asset.Name,
			"{description}", description,
		).Replace(agent.AssetLineTemplate))
	}
	if len(lines) == 0 {
		return "无"
	}
	if limit > 0 && len(context.Assets) > limit {
		lines = append(lines, strings.ReplaceAll(agent.ListLimitTemplate,
			"{limit}", strconv.Itoa(limit)))
	}
	return strings.Join(lines, "\n")
}
