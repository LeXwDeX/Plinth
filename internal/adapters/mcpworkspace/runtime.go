// Package mcpworkspace binds declared MCP reads to existing owner-scoped
// application services. It does not own another task or asset repository.
package mcpworkspace

import (
	"context"
	"encoding/json"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/mcp"
)

type Reads struct {
	Offerings  func(context.Context, mcp.Scope, string) (json.RawMessage, error)
	Tasks      func(context.Context, mcp.Scope, int) (json.RawMessage, error)
	TaskAssets func(context.Context, mcp.Scope, string) (json.RawMessage, error)
}
type Runtime struct {
	scope mcp.Scope
	tools []catalog.AgentMCPToolSchema
	reads Reads
}

func New(scope mcp.Scope, tools []catalog.AgentMCPToolSchema, reads Reads) *Runtime {
	return &Runtime{scope: scope, tools: tools, reads: reads}
}
func (r *Runtime) ListTools(context.Context) ([]mcp.Tool, error) {
	result := make([]mcp.Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		result = append(result, mcp.Tool{Name: tool.RemoteName, Description: tool.Description, InputSchema: append(json.RawMessage(nil), tool.Parameters...)})
	}
	return result, nil
}
func (r *Runtime) CallTool(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error) {
	if r.scope.OwnerID == "" || r.scope.SessionID == "" {
		return nil, mcp.ErrToolDenied
	}
	var selected *catalog.AgentMCPToolSchema
	for i := range r.tools {
		if r.tools[i].RemoteName == name {
			selected = &r.tools[i]
			break
		}
	}
	if selected == nil {
		return nil, mcp.ErrToolDenied
	}
	if err := mcp.ValidateArguments(selected.Parameters, arguments); err != nil {
		return nil, err
	}
	var value json.RawMessage
	var err error
	switch name {
	case "mcp_list_offerings":
		if r.reads.Offerings == nil {
			return nil, mcp.ErrUnavailable
		}
		var params struct {
			Capability string `json:"capability"`
		}
		_ = json.Unmarshal(arguments, &params)
		value, err = r.reads.Offerings(ctx, r.scope, params.Capability)
	case "mcp_list_session_tasks":
		if r.reads.Tasks == nil {
			return nil, mcp.ErrUnavailable
		}
		var params struct {
			Limit int `json:"limit"`
		}
		_ = json.Unmarshal(arguments, &params)
		value, err = r.reads.Tasks(ctx, r.scope, params.Limit)
	case "mcp_get_task_assets":
		if r.reads.TaskAssets == nil {
			return nil, mcp.ErrUnavailable
		}
		var params struct {
			TaskID string `json:"task_id"`
		}
		_ = json.Unmarshal(arguments, &params)
		if params.TaskID == "" {
			return nil, mcp.ErrInvalidArguments
		}
		value, err = r.reads.TaskAssets(ctx, r.scope, params.TaskID)
	default:
		return nil, mcp.ErrToolDenied
	}
	if err != nil {
		return nil, mcp.ErrUnavailable
	}
	if !json.Valid(value) {
		return nil, mcp.ErrProtocol
	}
	return json.Marshal(map[string]any{"resultType": "complete", "isError": false, "structuredContent": value, "content": []map[string]string{{"type": "text", "text": string(value)}}})
}
