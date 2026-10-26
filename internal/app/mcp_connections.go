package app

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/adapters/mcphttp"
	"github.com/LeXwDeX/Plinth/internal/adapters/mcpworkspace"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/mcp"
)

func buildWorkspaceMCP(taskCatalog *catalog.Catalog, kernel *generation.Module, sessions *chat.Service) (*mcp.Service, error) {
	application := taskCatalog.Application()
	policy := application.AgentConfiguration.MCPPolicy
	transportPolicy := mcphttp.Policy{MaxResponseBytes: policy.MaxResponseBytes, MaxTools: policy.MaxTools}
	owned := func(ctx context.Context, scope mcp.Scope) error {
		ok, err := sessions.OwnsSession(ctx, scope.OwnerID, scope.SessionID)
		if err != nil || !ok {
			return mcp.ErrToolDenied
		}
		return nil
	}
	reads := mcpworkspace.Reads{
		Offerings: func(ctx context.Context, scope mcp.Scope, capability string) (json.RawMessage, error) {
			if err := owned(ctx, scope); err != nil {
				return nil, err
			}
			capabilities := application.Capabilities
			if capability != "" {
				filtered := []catalog.CapabilityPresentation{}
				for _, item := range capabilities {
					if string(item.ID) == capability {
						filtered = append(filtered, item)
					}
				}
				if len(filtered) == 0 {
					return nil, mcp.ErrInvalidArguments
				}
				capabilities = filtered
			}
			return buildAgentOfferingsProjection(capabilities, taskCatalog, kernel.OfferingAvailable), nil
		},
		Tasks: func(ctx context.Context, scope mcp.Scope, limit int) (json.RawMessage, error) {
			if err := owned(ctx, scope); err != nil {
				return nil, err
			}
			// A zero limit delegates to the kernel's Schema-backed page default.
			page, err := kernel.List(ctx, generation.OwnTasks(generation.OwnerID(scope.OwnerID)), generation.ListQuery{SessionID: scope.SessionID, Limit: limit})
			if err != nil {
				return nil, err
			}
			views := make([]agentTaskView, 0, len(page.Tasks))
			for _, task := range page.Tasks {
				views = append(views, buildAgentTaskView(task, application.Chat))
			}
			return json.Marshal(map[string]any{"tasks": views, "next_cursor": page.NextCursor, "total": page.Total})
		},
		TaskAssets: func(ctx context.Context, scope mcp.Scope, taskID string) (json.RawMessage, error) {
			if err := owned(ctx, scope); err != nil {
				return nil, err
			}
			task, err := kernel.Get(ctx, generation.OwnerID(scope.OwnerID), generation.TaskID(taskID))
			if err != nil || task.SessionID != scope.SessionID {
				return nil, mcp.ErrToolDenied
			}
			view := buildAgentTaskView(task, application.Chat)
			// Only locally cached task assets receive same-origin routes.
			assets := []map[string]any{}
			for _, asset := range task.Assets {
				path := strings.NewReplacer("{taskID}", url.PathEscape(string(task.ID)), "{assetID}", url.PathEscape(string(asset.ID))).Replace(application.Runtime.HTTP.Routes.TaskAsset)
				assets = append(assets, map[string]any{"asset_id": asset.ID, "role": asset.Role, "variant": asset.Variant, "media_type": asset.MediaType, "size": asset.Size, "sha256": asset.SHA256, "url": path})
			}
			return json.Marshal(map[string]any{"task": view, "assets": assets})
		},
	}
	return mcp.New(application.AgentConfiguration.MCPPresets, policy, func(ctx context.Context, preset catalog.AgentMCPPresetSchema, scope mcp.Scope) (mcp.Peer, error) {
		if err := owned(ctx, scope); err != nil {
			return nil, err
		}
		switch preset.Transport {
		case "local":
			return mcphttp.NewLocal(mcpworkspace.New(scope, preset.Tools, reads), transportPolicy)
		case "streamable_http":
			// A shared remote credential and generic text output cannot establish
			// tenant isolation. Keep the tested transport seam unavailable until
			// a scoped port and explicit public output DTO have been supplied.
			return nil, mcp.ErrUnavailable
		default:
			return nil, mcp.ErrUnavailable
		}
	}, func(preset catalog.AgentMCPPresetSchema) bool {
		if preset.Transport != "local" || preset.ConnectionRef != "workspace-read" || preset.ProtocolVersion != mcphttp.ProtocolVersion {
			return false
		}
		for _, tool := range preset.Tools {
			switch tool.RemoteName {
			case "mcp_list_offerings", "mcp_list_session_tasks", "mcp_get_task_assets":
			default:
				return false
			}
		}
		return true
	})
}
