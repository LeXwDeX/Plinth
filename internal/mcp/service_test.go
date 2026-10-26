package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

type testPeer struct {
	tools  []Tool
	result json.RawMessage
	calls  int
}

func (p *testPeer) ListTools(context.Context) ([]Tool, error) { return p.tools, nil }
func (p *testPeer) CallTool(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	p.calls++
	return p.result, nil
}
func testPreset() catalog.AgentMCPPresetSchema {
	return catalog.AgentMCPPresetSchema{ID: "workspace", Tools: []catalog.AgentMCPToolSchema{{Name: "read_tasks", RemoteName: "tasks", Parameters: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","minimum":1,"maximum":5}},"additionalProperties":false}`), Execution: catalog.ChatToolExecutionSchema{ImplementationKey: "mcp/read", Effects: []string{"READ"}, Scope: "session", Consent: "none"}}}}
}
func testPolicy() catalog.AgentMCPPolicySchema {
	return catalog.AgentMCPPolicySchema{TimeoutMilliseconds: 100, MaxResponseBytes: 4096, MaxTools: 5}
}

func TestServiceChecksScopeRegistrationArgumentsAndPeerContractBeforeCall(t *testing.T) {
	preset := testPreset()
	peer := &testPeer{tools: []Tool{{Name: "tasks", InputSchema: preset.Tools[0].Parameters}}, result: json.RawMessage(`{"resultType":"complete","content":[{"type":"text","text":"ready"}]}`)}
	factories := 0
	service, err := New([]catalog.AgentMCPPresetSchema{preset}, testPolicy(), func(_ context.Context, _ catalog.AgentMCPPresetSchema, scope Scope) (Peer, error) {
		factories++
		if scope != (Scope{"owner", "session"}) {
			t.Fatalf("scope changed: %#v", scope)
		}
		return peer, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		scope      Scope
		name, args string
	}{{Scope{}, "read_tasks", `{}`}, {Scope{"owner", "session"}, "undeclared", `{}`}, {Scope{"owner", "session"}, "read_tasks", `{"owner_id":"other"}`}, {Scope{"owner", "session"}, "read_tasks", `{"limit":0}`}, {Scope{"owner", "session"}, "read_tasks", `{"limit":1.5}`}} {
		if _, err := service.Call(context.Background(), input.scope, input.name, json.RawMessage(input.args)); err == nil {
			t.Fatalf("accepted %#v", input)
		}
	}
	if factories != 0 || peer.calls != 0 {
		t.Fatal("invalid input reached peer")
	}
	peer.tools[0].InputSchema = json.RawMessage(`{"type":"object","additionalProperties":true}`)
	if _, err := service.Call(context.Background(), Scope{"owner", "session"}, "read_tasks", json.RawMessage(`{}`)); !errors.Is(err, ErrToolDenied) || peer.calls != 0 {
		t.Fatalf("schema drift permitted call: %v", err)
	}
	peer.tools[0].InputSchema = preset.Tools[0].Parameters
	peer.tools = append(peer.tools, peer.tools[0])
	if _, err := service.Call(context.Background(), Scope{"owner", "session"}, "read_tasks", json.RawMessage(`{}`)); !errors.Is(err, ErrProtocol) || peer.calls != 0 {
		t.Fatalf("duplicate declaration accepted: %v", err)
	}
	peer.tools = peer.tools[:1]
	if _, err := service.Call(context.Background(), Scope{"owner", "session"}, "read_tasks", json.RawMessage(`{"limit":3}`)); err != nil || peer.calls != 1 {
		t.Fatalf("valid read failed: %v", err)
	}
	filtered, err := service.ScopedPeer(nil, Scope{"owner", "session"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := filtered.CallTool(context.Background(), "read_tasks", json.RawMessage(`{}`)); !errors.Is(err, ErrToolDenied) {
		t.Fatal("disabled tool callable")
	}
}

func TestServiceRejectsWriteDeclarationsAndUnsupportedSchemas(t *testing.T) {
	for _, schema := range []string{`{"type":"object","additionalProperties":false,"properties":{"owner_id":{"type":"string"}}}`, `{"type":"object","additionalProperties":false,"properties":{"input":{"type":"string","pattern":".*"}}}`, `{"type":"object","additionalProperties":false} {}`, `{"type":"object","additionalProperties":false,"properties":{"n":{"type":"integer","minimum":3,"maximum":1}}}`, `{"type":"object","additionalProperties":false,"properties":{"n":{"type":"boolean","enum":["true"]}}}`} {
		preset := testPreset()
		preset.Tools[0].Parameters = json.RawMessage(schema)
		if _, err := New([]catalog.AgentMCPPresetSchema{preset}, testPolicy(), func(context.Context, catalog.AgentMCPPresetSchema, Scope) (Peer, error) { return nil, nil }); err == nil {
			t.Fatalf("accepted schema: %s", schema)
		}
	}
	preset := testPreset()
	preset.Tools[0].Execution.Effects = []string{"PAID_TASK"}
	if _, err := New([]catalog.AgentMCPPresetSchema{preset}, testPolicy(), func(context.Context, catalog.AgentMCPPresetSchema, Scope) (Peer, error) { return nil, nil }); err == nil {
		t.Fatal("write tool accepted")
	}
}

func TestUnavailablePresetNeverRegistersOrCreatesPeer(t *testing.T) {
	preset := testPreset()
	created := 0
	service, err := New([]catalog.AgentMCPPresetSchema{preset}, testPolicy(), func(context.Context, catalog.AgentMCPPresetSchema, Scope) (Peer, error) {
		created++
		return nil, ErrUnavailable
	}, func(catalog.AgentMCPPresetSchema) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	declarations, err := service.Declarations([]string{preset.ID})
	if err != nil || len(declarations) != 0 || service.PresetAvailable(preset.ID) {
		t.Fatal("unavailable preset registered")
	}
	if _, err := service.Call(context.Background(), Scope{"owner", "session"}, "read_tasks", json.RawMessage(`{}`)); !errors.Is(err, ErrUnavailable) || created != 0 {
		t.Fatalf("unavailable preset created peer: %d %v", created, err)
	}
}

func TestServiceBoundedDeadlineAndResultRedaction(t *testing.T) {
	preset := testPreset()
	service, err := New([]catalog.AgentMCPPresetSchema{preset}, testPolicy(), func(ctx context.Context, _ catalog.AgentMCPPresetSchema, _ Scope) (Peer, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("missing timeout")
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Call(context.Background(), Scope{"owner", "session"}, "read_tasks", json.RawMessage(`{}`)); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	result, err := safeResult(json.RawMessage(`{"resultType":"complete","content":[{"type":"resource_link","uri":"https://private/model"},{"type":"text","text":"{\"remote_job_id\":\"job-secret\",\"url\":\"https://private/x?sig=secret\",\"id\":\"task-public\"}"}],"structuredContent":{"opaque":"private","nested":[{"api_key":"private-key","asset_url":"/api/v1/tasks/task-public/assets/a"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"job-secret", "sig=secret", "private-key", "opaque", "https://", "resource_link"} {
		if strings.Contains(string(result), forbidden) {
			t.Fatalf("leaked %s", forbidden)
		}
	}
	if !strings.Contains(string(result), "task-public") || !strings.Contains(string(result), "/api/v1/tasks/") {
		t.Fatal("public facts lost")
	}
	for _, result := range []string{`{"content":[{"type":"text","text":"legacy"}]}`, `{"resultType":"input_required","content":[{"type":"text","text":"consent"}]}`, `{"resultType":"complete","isError":true,"content":[{"type":"text","text":"private upstream error"}]}`} {
		if _, err := safeResult(json.RawMessage(result)); err == nil {
			t.Fatalf("accepted noncomplete result: %s", result)
		}
	}
}
