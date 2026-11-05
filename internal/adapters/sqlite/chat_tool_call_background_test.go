package sqlite

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #137: a call whose Task kept running after the person stopped waiting keeps
// that mark in the stored message, so a later turn can list the Task for the
// Agent; calls without it store nothing new and older rows read as unmarked.
func TestToolCallBackgroundMarkSurvivesStorage(t *testing.T) {
	encoded, err := marshalToolCalls([]chat.ToolCall{
		{ID: "waited", Name: "refine_image", TaskID: "task-1", Background: true},
		{ID: "plain", Name: "refine_image", TaskID: "task-2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(encoded, `"background"`) != 1 {
		t.Fatalf("encoded = %s", encoded)
	}
	var decoded []chat.ToolCall
	if err := unmarshalToolCalls(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 2 || !decoded[0].Background || decoded[1].Background || decoded[0].TaskID != "task-1" {
		t.Fatalf("decoded = %+v", decoded)
	}
	var legacy []chat.ToolCall
	if err := unmarshalToolCalls(`[{"id":"old","name":"refine_image","arguments":"{}","task_id":"task-3"}]`, &legacy); err != nil {
		t.Fatal(err)
	}
	if len(legacy) != 1 || legacy[0].Background {
		t.Fatalf("legacy = %+v", legacy)
	}
}
