package chat

import (
	"encoding/json"
	"testing"
)

// #103: a read-only tool's compact result is stored on the call being built
// and on the checkpointed copy of the message. The durable copy fills an
// in-memory call only when it has no summary yet.
func TestToolSummaryIsRecordedAndMerged(t *testing.T) {
	orchestrator := &Orchestrator{}
	message := &Message{ToolCalls: []ToolCall{{ID: "call-1", Name: "inspect_model"}}}
	checkpoint := &Message{ToolCalls: []ToolCall{{ID: "call-1", Name: "inspect_model"}}}
	summary := json.RawMessage(`{"tool":"inspect","triangles":19123}`)
	orchestrator.recordToolSummary(message, checkpoint, "call-1", summary)
	if string(message.ToolCalls[0].Summary) != string(summary) ||
		string(checkpoint.ToolCalls[0].Summary) != string(summary) {
		t.Fatalf("recorded summary = %s / %s", message.ToolCalls[0].Summary, checkpoint.ToolCalls[0].Summary)
	}
	// An empty summary or an unknown call never clears an existing one.
	orchestrator.recordToolSummary(message, checkpoint, "call-1", nil)
	orchestrator.recordToolSummary(message, checkpoint, "missing", json.RawMessage(`{"tool":"render"}`))
	if string(message.ToolCalls[0].Summary) != string(summary) {
		t.Fatalf("summary after empty records = %s", message.ToolCalls[0].Summary)
	}

	// The persisted call fills an in-memory call that has no summary ...
	stored := ToolCall{ID: "call-1", Summary: summary}
	target := ToolCall{ID: "call-1", TaskID: "task-a"}
	mergeStoredToolCall(&target, stored)
	if string(target.Summary) != string(summary) || target.TaskID != "task-a" {
		t.Fatalf("merged call = %#v", target)
	}
	// ... and never overwrites the one the in-memory call already holds.
	kept := json.RawMessage(`{"tool":"compare"}`)
	target = ToolCall{ID: "call-1", Summary: kept}
	mergeStoredToolCall(&target, stored)
	if string(target.Summary) != string(kept) {
		t.Fatalf("overwritten summary = %s", target.Summary)
	}
}
