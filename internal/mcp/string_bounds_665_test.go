package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #665: string arguments of an MCP peer tool are bounded by the Schema's
// policy. A string parameter declared without maxLength takes the policy's
// bound, an over-long value never reaches the peer, and a declaration whose
// maxLength exceeds the bound is refused at assembly.
func TestMCPStringArgumentsAreBoundedByThePolicy665(t *testing.T) {
	parameters := json.RawMessage(`{"type":"object","properties":{"capability":{"type":"string"}},"additionalProperties":false}`)
	preset := catalog.AgentMCPPresetSchema{ID: "workspace", Tools: []catalog.AgentMCPToolSchema{{Name: "list", RemoteName: "list",
		Parameters: parameters, Execution: catalog.ChatToolExecutionSchema{ImplementationKey: "mcp/read", Effects: []string{"READ"}, Scope: "session"}}}}
	policy := testPolicy()
	policy.MaxStringArgumentCharacters = 8
	peer := &testPeer{tools: []Tool{{Name: "list", InputSchema: parameters}},
		result: json.RawMessage(`{"resultType":"complete","content":[{"type":"text","text":"ok"}]}`)}
	service, err := New([]catalog.AgentMCPPresetSchema{preset}, policy,
		func(context.Context, catalog.AgentMCPPresetSchema, Scope) (Peer, error) { return peer, nil })
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{"owner", "session"}
	if _, err := service.Call(context.Background(), scope, "list", json.RawMessage(`{"capability":"`+strings.Repeat("长", 9)+`"}`)); !errors.Is(err, ErrInvalidArguments) || peer.calls != 0 {
		t.Fatalf("an over-long string reached the peer: %v calls=%d", err, peer.calls)
	}
	if _, err := service.Call(context.Background(), scope, "list", json.RawMessage(`{"capability":"`+strings.Repeat("长", 8)+`"}`)); err != nil || peer.calls != 1 {
		t.Fatalf("a string within the bound failed: %v calls=%d", err, peer.calls)
	}
	preset.Tools[0].Parameters = json.RawMessage(`{"type":"object","properties":{"capability":{"type":"string","maxLength":9}},"additionalProperties":false}`)
	if _, err := New([]catalog.AgentMCPPresetSchema{preset}, policy,
		func(context.Context, catalog.AgentMCPPresetSchema, Scope) (Peer, error) { return peer, nil }); !errors.Is(err, ErrToolDenied) {
		t.Fatalf("a maxLength above the policy bound was accepted: %v", err)
	}
}
