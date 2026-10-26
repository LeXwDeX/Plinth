package mcpagent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/mcp"
)

type readPeer struct{ calls int }

func (*readPeer) ListTools(context.Context) ([]mcp.Tool, error) {
	return []mcp.Tool{{Name: "remote_read", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`)}}, nil
}
func (p *readPeer) CallTool(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	p.calls++
	return json.RawMessage(`{"resultType":"complete","content":[{"type":"text","text":"facts"}]}`), nil
}
func TestExecutorRequiresFrozenRegistrationAndKeepsReadSideEffectFree(t *testing.T) {
	execution := catalog.ChatToolExecutionSchema{ImplementationKey: "mcp/read", Effects: []string{"READ"}, Scope: "session", Consent: "none"}
	preset := catalog.AgentMCPPresetSchema{ID: "read-preset", Tools: []catalog.AgentMCPToolSchema{{Name: "mcp_read", RemoteName: "remote_read", Parameters: json.RawMessage(`{"type":"object","additionalProperties":false}`), Execution: execution}}}
	peer := &readPeer{}
	service, err := mcp.New([]catalog.AgentMCPPresetSchema{preset}, catalog.AgentMCPPolicySchema{TimeoutMilliseconds: 100, MaxResponseBytes: 4096, MaxTools: 5}, func(_ context.Context, _ catalog.AgentMCPPresetSchema, scope mcp.Scope) (mcp.Peer, error) {
		if scope.OwnerID != "owner" || scope.SessionID != "session" {
			t.Fatal("scope changed")
		}
		return peer, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	executor := Wrap(nil, service)
	command := chat.ToolCommand{OwnerID: "owner", SessionID: "session", Call: agents.ToolCall{Name: "mcp_read", Arguments: []byte(`{}`)}}
	if _, err := executor.Execute(context.Background(), command, nil); !errors.Is(err, mcp.ErrToolDenied) || peer.calls != 0 {
		t.Fatal("unfrozen registration called")
	}
	command.Configuration = &chat.AgentConfigurationSnapshot{AgentTools: []catalog.ChatAgentToolSchema{{Name: "another", Execution: execution}}}
	if _, err := executor.Execute(context.Background(), command, nil); !errors.Is(err, mcp.ErrToolDenied) || peer.calls != 0 {
		t.Fatal("omitted registration called")
	}
	declarations, err := service.Declarations([]string{preset.ID})
	if err != nil {
		t.Fatal(err)
	}
	command.Configuration.AgentTools = declarations
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil || peer.calls != 1 || !result.NoSubmission || result.TaskID != "" || result.AgentContent == "" {
		t.Fatalf("read result: %+v %v", result, err)
	}
}
