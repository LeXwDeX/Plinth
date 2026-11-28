package generationtool

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// A board handle is an ordinary reference resolved from the frozen board
// snapshot, and the server names it on the step's reference note (#709).
func TestBoardHandleIsAnOrdinaryReferenceNamedOnTheNote709(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	executor := &Executor{chat: loaded.Application().Chat}
	argument := executor.chat.Context.ReferenceHandlesArgument
	arguments, _ := json.Marshal(map[string]any{argument: []string{"@canvas1"}})
	command := chat.ToolCommand{
		Call: agents.ToolCall{ID: "c1", Name: "refine_image", Arguments: arguments},
		Options: chat.TurnOptions{CanvasContext: &chat.CanvasContext{Total: 1, Layers: []chat.CanvasLayerContext{
			{ID: "n1", Type: "image", Handle: "@canvas1", Picture: &chat.Attachment{Kind: "image", AssetID: "in_1", URL: "/api/assets/in_1"}}}}},
	}
	urls, err := executor.resolveImageHandles(command, []string{"@canvas1"})
	if err != nil || len(urls) != 1 || urls[0] != "/api/assets/in_1" {
		t.Fatalf("urls=%v err=%v", urls, err)
	}
	if _, err := executor.resolveImageHandles(command, []string{"@canvas2"}); err == nil || !strings.Contains(err.Error(), "@canvas2") {
		t.Fatalf("an unknown board handle is refused by name: %v", err)
	}
	note := executor.canvasSourceNote(command)
	if !strings.Contains(note, "@canvas1") || !strings.Contains(note, "画板") {
		t.Fatalf("source note = %q", note)
	}
	command.Options.CanvasContext = nil
	if note := executor.canvasSourceNote(command); note != "" {
		t.Fatalf("no snapshot, no board note: %q", note)
	}
}
