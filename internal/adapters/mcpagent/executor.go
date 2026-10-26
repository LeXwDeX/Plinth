// Package mcpagent routes only registered read tools through MCP. Embedding the
// existing executor preserves paid-task recovery and consent helper interfaces.
package mcpagent

import (
	"context"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agenttools"
	"github.com/LeXwDeX/Plinth/internal/mcp"
)

type Executor struct {
	*agenttools.Executor
	service *mcp.Service
}

func Wrap(base *agenttools.Executor, service *mcp.Service) *Executor {
	return &Executor{Executor: base, service: service}
}
func (e *Executor) Execute(ctx context.Context, command chat.ToolCommand, observe func(chat.ToolObservation) error) (chat.ToolResult, error) {
	if e.service == nil || !e.service.HasTool(command.Call.Name) {
		return e.Executor.Execute(ctx, command, observe)
	}
	// Verify the frozen registration too; the global registry only maps names to
	// implementations and cannot grant tools omitted from the current turn.
	allowed := false
	if command.Configuration != nil {
		for _, tool := range command.Configuration.AgentTools {
			if tool.Name == command.Call.Name && tool.Execution.ImplementationKey == "mcp/read" {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return chat.ToolResult{}, mcp.ErrToolDenied
	}
	data, err := e.service.Call(ctx, mcp.Scope{OwnerID: command.OwnerID, SessionID: command.SessionID}, command.Call.Name, command.Call.Arguments)
	if err != nil {
		return chat.ToolResult{Error: err.Error(), NoSubmission: true}, nil
	}
	return chat.ToolResult{AgentContent: string(data), NoSubmission: true}, nil
}
