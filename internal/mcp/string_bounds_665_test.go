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
		func(context.Context, catalog.AgentMCPPresetSchema, Scope) (Peer, error) { return peer, nil }); !errors.Is(err, ErrToolDenied) ||
		!strings.Contains(err.Error(), `"list"`) || !strings.Contains(err.Error(), "capability") {
		t.Fatalf("a maxLength above the policy bound was accepted or not named: %v", err)
	}
}

// #665 (wave 3): the bound covers nested strings and arrays, in a
// declaration and in arguments, and the refusal names the tool and the path.
func TestMCPStringBoundsWalkNestedValues665(t *testing.T) {
	nested := json.RawMessage(`{"type":"object","properties":{"filter":{"type":"object","properties":{"tags":{"type":"array","items":{"type":"string","maxLength":9}}}}}}`)
	if err := validateStringBounds(nested, 8); !errors.Is(err, ErrToolDenied) || !strings.Contains(err.Error(), "filter.tags[]") {
		t.Fatalf("a nested maxLength above the bound: %v", err)
	}
	if err := validateStringBounds(nested, 9); err != nil {
		t.Fatalf("a nested maxLength within the bound: %v", err)
	}
	long := strings.Repeat("长", 9)
	for _, arguments := range []string{
		`{"filter":{"name":"` + long + `"}}`,
		`{"tags":["ok","` + long + `"]}`,
		`{"deep":[{"inner":["` + long + `"]}]}`,
	} {
		if err := validateStringArgumentLengths(json.RawMessage(arguments), 8); !errors.Is(err, ErrInvalidArguments) {
			t.Errorf("%s: err = %v", arguments, err)
		}
	}
	if err := validateStringArgumentLengths(json.RawMessage(`{"tags":["`+strings.Repeat("长", 8)+`"],"n":3}`), 8); err != nil {
		t.Fatalf("values within the bound: %v", err)
	}
}
