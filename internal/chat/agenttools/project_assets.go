package agenttools

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// listProjectAssets answers list_project_assets from the project snapshot
// frozen when the turn started (#240). It never reads the mutable project or
// accepts a project argument: the owner and project come only from the
// server-held command. A session outside a project has nothing to list.
func (e *Executor) listProjectAssets(_ context.Context, command chat.ToolCommand) chat.ToolResult {
	if e.project == nil || e.project.Agent == nil {
		return chat.ToolResult{Error: "project assets unavailable", NoSubmission: true}
	}
	agent := e.project.Agent
	context := command.Options.ProjectContext
	if command.Options.ProjectID == "" || context == nil {
		return chat.ToolResult{Error: e.project.Messages.UnknownProject, NoSubmission: true}
	}
	assets := context.Assets
	limit := agent.ListLimit
	if limit > 0 && len(assets) > limit {
		assets = assets[:limit]
	}
	items := make([]map[string]any, 0, len(assets))
	for _, asset := range assets {
		models := make([]map[string]string, 0, len(asset.Models))
		for _, model := range asset.Models {
			models = append(models, map[string]string{"task_id": model.TaskID, "asset_id": model.AssetID})
		}
		items = append(items, map[string]any{
			"handle":      asset.Handle,
			"kind":        asset.Kind,
			"name":        asset.Name,
			"description": asset.Description,
			"images":      append([]string(nil), asset.Images...),
			"model":       models,
		})
	}
	payload := map[string]any{
		"project_id": context.ID,
		"project":    context.Name,
		"assets":     items,
	}
	if limit > 0 && len(context.Assets) > limit {
		payload["note"] = strings.ReplaceAll(agent.ListLimitTemplate, "{limit}", strconv.Itoa(limit))
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return chat.ToolResult{Error: "project assets unavailable", NoSubmission: true}
	}
	return chat.ToolResult{AgentContent: string(encoded), NoSubmission: true}
}
