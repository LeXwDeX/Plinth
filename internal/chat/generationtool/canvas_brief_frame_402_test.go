package generationtool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #402: the person's canvas size is their decision; the frame the brief's
// purpose recommends (concept art: 16:9) is only a suggestion. On a canvas turn
// the call the Agent makes is submitted at the canvas size whatever the brief
// says, and the note the tool result carries says the size followed the canvas
// without sending the person back to it. The code always did this; the Agent's
// words around it (selection_canvas_note, the brief wording) made it ask for a
// different canvas anyway, so the Schema text changed and this test keeps the
// behavior the text now promises.
func TestCanvasTurnIgnoresTheBriefFrameAndDoesNotAskForAnotherCanvas_402(t *testing.T) {
	_, schema := handleExecutor(t)
	executor := Executor{chat: schema}
	brief := chat.ProductionBrief{"image_use": json.RawMessage(`"concept_art"`)}
	offering := ratioOffering() // meshy/nano-banana-pro/image-to-image: concept art freezes aspect_ratio 16:9 here
	build := func(command chat.ToolCommand) (map[string]any, json.RawMessage) {
		t.Helper()
		raw, _, err := executor.buildInput(context.Background(), command, offering, map[string]json.RawMessage{}, "merge", nil, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		var input map[string]any
		if err := json.Unmarshal(raw, &input); err != nil {
			t.Fatal(err)
		}
		return input, raw
	}

	// Control: without a canvas the purpose's frame is what the step runs with,
	// so the brief really does disagree with a portrait canvas below.
	plain := canvasCommand(schema, 1536, 2048)
	plain.Options.InputSource = ""
	plain.Options.ProductionBrief = brief
	if input, _ := build(plain); input["aspect_ratio"] != "16:9" {
		t.Fatalf("the purpose's frame was not applied outside a canvas turn: aspect_ratio = %v", input["aspect_ratio"])
	}

	// A portrait canvas against the landscape purpose: the canvas wins.
	command := canvasCommand(schema, 1536, 2048)
	command.Options.ProductionBrief = brief
	input, submitted := build(command)
	if input["aspect_ratio"] != "3:4" {
		t.Fatalf("a canvas turn was submitted at %v, not at the canvas ratio 3:4", input["aspect_ratio"])
	}

	// The note says the size followed the canvas, and never asks for another one.
	note := executor.canvasSizeNote(command, offering, submitted)
	want := strings.NewReplacer("{canvas}", fmt.Sprintf("%d×%d", 1536, 2048), "{size}", "3:4").Replace(schema.Context.CanvasOutputSizeTemplate)
	if note != want {
		t.Fatalf("note = %q; want %q", note, want)
	}
	for _, asking := range []string{"调整画板", "改画板", "画板改成", "画布改"} {
		if strings.Contains(note, asking) {
			t.Fatalf("the note sends the person back to the canvas: %q", note)
		}
	}
}
