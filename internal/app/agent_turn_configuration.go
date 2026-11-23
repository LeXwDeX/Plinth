package app

import (
	"context"

	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/mcp"
)

// agentTurnConfiguration freezes the Agent configuration a turn talks to the
// model with: the published selection (or the Schema defaults when nothing is
// published), its enabled built-in tools, and the tools of the MCP presets the
// administrator enabled (#445). A preset a new configuration does not enable
// by default is simply absent here; a published configuration that selected
// it keeps it. The production list's completion tool is the one exception a
// published selection cannot keep: without an enabled asset_list.execution it is
// left out of every turn's table (#494); proposing and reading a list need no
// run and stay in.
func agentTurnConfiguration(
	ctx context.Context,
	owner string,
	service *agentconfiguration.Service,
	application catalog.ApplicationSchema,
	mcpService *mcp.Service,
) (chat.AgentConfigurationSnapshot, error) {
	snapshot, err := service.SnapshotForOwner(ctx, owner)
	if err != nil {
		return chat.AgentConfigurationSnapshot{}, err
	}
	frozen := chat.AgentConfigurationSnapshot{Revision: snapshot.Revision, SystemPrompt: snapshot.Configuration.SystemPrompt, DomainContext: snapshot.Configuration.DomainContext, Skills: snapshot.Configuration.Skills}
	enabled := map[string]bool{}
	for _, name := range snapshot.Configuration.EnabledToolNames {
		enabled[name] = true
	}
	for _, tool := range application.Chat.Tools {
		if enabled[tool.Name] {
			frozen.Tools = append(frozen.Tools, tool)
		}
	}
	for _, tool := range withoutAssetListExecutionTools(application.Chat.AgentTools, application.AssetListExecutionEnabled()) {
		if enabled[tool.Name] {
			frozen.AgentTools = append(frozen.AgentTools, tool)
		}
	}
	mcpTools, err := mcpService.Declarations(snapshot.Configuration.EnabledMCPPresetIDs)
	if err != nil {
		return chat.AgentConfigurationSnapshot{}, err
	}
	frozen.AgentTools = append(frozen.AgentTools, mcpTools...)
	return frozen, nil
}
