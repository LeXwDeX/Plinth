package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #103: a read-only review tool's compact result is projected on its call as a
// JSON object, so the conversation can show one line without fetching the
// report again. A Catalog that does not name the member keeps the old shape.
func TestChatMessageWireCarriesTheToolSummary(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	server := &Server{application: application}
	fields := application.Chat.API.ResponseFields
	if fields.ToolSummary == "" {
		t.Fatal("the shipped Schema must name the tool summary member")
	}
	summary := json.RawMessage(`{"tool":"inspect","model":"model3","triangles":19123,"has_uv":true}`)
	wire := server.chatMessageWire("", chat.Message{ID: "m1", Role: "assistant", ToolCalls: []chat.ToolCall{
		{ID: "call-1", Name: "inspect_model", Summary: summary},
		{ID: "call-2", Name: "render_model_views"},
	}})
	var calls []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(wire[fields.ToolCalls].(string)), &calls); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("tool calls = %s", wire[fields.ToolCalls])
	}
	var got map[string]any
	if err := json.Unmarshal(calls[0][fields.ToolSummary], &got); err != nil {
		t.Fatalf("tool summary = %s: %v", calls[0][fields.ToolSummary], err)
	}
	if got["tool"] != "inspect" || got["triangles"] != float64(19123) || got["has_uv"] != true {
		t.Fatalf("tool summary = %v", got)
	}
	if _, present := calls[1][fields.ToolSummary]; present {
		t.Fatal("a call without a summary must not carry the member")
	}

	// An old Catalog that does not declare the member projects no summary.
	server.application.Chat.API.ResponseFields.ToolSummary = ""
	wire = server.chatMessageWire("", chat.Message{ID: "m2", Role: "assistant", ToolCalls: []chat.ToolCall{
		{ID: "call-1", Name: "inspect_model", Summary: summary},
	}})
	calls = nil
	if err := json.Unmarshal([]byte(wire[fields.ToolCalls].(string)), &calls); err != nil {
		t.Fatal(err)
	}
	if _, present := calls[0]["summary"]; present {
		t.Fatal("an undeclared tool summary member must not be projected")
	}
}
