package generationtool

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #266: on a turn whose source is the artboard snapshot, a generation tool that
// reads images uses the snapshot first whatever the Agent named or left out.
func TestCanvasTurnBindsTheSnapshotAheadOfAgentReferences(t *testing.T) {
	executor, schema := handleExecutor(t)
	var refine, background = schema.Tools[0], schema.Tools[1]
	if refine.Name != "refine_image" || refine.CanvasSource != canvasSourceBind || background.CanvasSource != canvasSourceBind {
		t.Fatalf("default catalog canvas sources: %s=%q %s=%q", refine.Name, refine.CanvasSource, background.Name, background.CanvasSource)
	}
	canvas := chat.TurnOptions{
		InputSource: schema.Context.CanvasInputSource,
		Selection:   []chat.Attachment{{Handle: "@img4", Kind: chat.AttachmentKindImage, URL: "canvas-snapshot"}},
	}
	history := []chat.Message{
		{Role: schema.Roles.User, ImageURLs: []string{"old-image"}},
		{Role: schema.Roles.Assistant},
		{Role: schema.Roles.User, Content: "make it a night scene"},
	}
	indices := schema.Context.ToolReferenceArgument
	for _, test := range []struct {
		name      string
		turnImage string
		arguments map[string]json.RawMessage
		want      []string
	}{
		{"agent named nothing", "", nil, []string{"canvas-snapshot"}},
		{"agent asked for a fresh image", "", map[string]json.RawMessage{indices: json.RawMessage(`[]`)}, []string{"canvas-snapshot"}},
		{"agent named a history picture", "", map[string]json.RawMessage{indices: json.RawMessage(`[1]`)}, []string{"canvas-snapshot", "old-image"}},
		{"agent named the snapshot itself", "", map[string]json.RawMessage{schema.Context.ReferenceHandlesArgument: json.RawMessage(`["` + schema.Context.SelectionAlias + `"]`)}, []string{"canvas-snapshot"}},
		{"the person attached a picture too", "attached", nil, []string{"canvas-snapshot", "attached"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			turn := append([]chat.Message(nil), history...)
			if test.turnImage != "" {
				turn[2].ImageURLs = []string{test.turnImage}
			}
			command := chat.ToolCommand{History: turn, Tool: refine, Options: canvas}
			got, err := executor.selectReferences(command, test.arguments)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("references = %v, %v; want %v", got, err, test.want)
			}
		})
	}
	// Without the declared source the selection keeps its old meaning: a
	// plain request stays text-to-image.
	command := chat.ToolCommand{History: history, Tool: refine, Options: chat.TurnOptions{Selection: canvas.Selection}}
	if got, err := executor.selectReferences(command, nil); err != nil || len(got) != 0 {
		t.Fatalf("an undeclared selection was bound: %v %v", got, err)
	}
	// An ignore tool is not touched by the canvas source.
	command = chat.ToolCommand{History: history, Tool: refine, Options: canvas}
	command.Tool.CanvasSource = "ignore"
	if got, err := executor.selectReferences(command, nil); err != nil || len(got) != 0 {
		t.Fatalf("ignore tool bound the snapshot: %v %v", got, err)
	}
}

func TestCanvasRefusalNamesTheToolsThatBindTheSnapshot(t *testing.T) {
	executor, schema := handleExecutor(t)
	message := executor.canvasRefusal(chat.ToolCommand{})
	var bind []string
	for _, tool := range schema.Tools {
		if tool.CanvasSource == canvasSourceBind {
			bind = append(bind, tool.Name)
		}
	}
	if len(bind) == 0 || !strings.Contains(message, strings.Join(bind, ", ")) {
		t.Fatalf("refusal %q does not list exactly %v", message, bind)
	}
	if strings.Contains(message, "{tools}") {
		t.Fatalf("placeholder left in %q", message)
	}
}
