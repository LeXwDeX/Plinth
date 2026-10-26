package chat

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func canvasPromptOrchestrator(t *testing.T) (*Orchestrator, catalog.ChatSchema) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application().Chat
	return &Orchestrator{schema: schema}, schema
}

// #272: on a canvas turn the Agent reads the snapshot as the layout the person
// arranged (its size and layer count) and is told to compose from it instead
// of adding the layers' own pictures; the words are the Schema's.
func TestCanvasTurnSelectionNamesTheArtboardLayout(t *testing.T) {
	orchestrator, schema := canvasPromptOrchestrator(t)
	context := schema.Context
	snapshot := Attachment{Handle: "@img5", Kind: AttachmentKindImage, Role: AttachmentRoleCanvas, AssetID: "snapshot",
		URL: "/api/v1/input-assets/snapshot", Canvas: &CanvasLayout{Width: 2048, Height: 2048, Layers: 2}}
	canvas := TurnOptions{InputSource: context.CanvasInputSource, Selection: []Attachment{snapshot}}

	prompt := orchestrator.selectionPrompt(canvas)
	layout := strings.NewReplacer("{width}", "2048", "{height}", "2048",
		"{layers}", strings.ReplaceAll(context.CanvasLayersTemplate, "{layers}", "2")).Replace(context.CanvasLayoutTemplate)
	for _, want := range []string{"@img5", layout, strings.ReplaceAll(context.SelectionCanvasTemplate, "{layout}", layout), context.SelectionCanvasNote} {
		if want == "" || !strings.Contains(prompt, want) {
			t.Fatalf("canvas selection lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Count(prompt, context.SelectionCanvasNote) != 1 {
		t.Fatalf("canvas note repeated:\n%s", prompt)
	}

	// An unknown layer count leaves the count out instead of printing 0.
	unknown := snapshot
	unknown.Canvas = &CanvasLayout{Width: 1536, Height: 1024}
	prompt = orchestrator.selectionPrompt(TurnOptions{InputSource: context.CanvasInputSource, Selection: []Attachment{unknown}})
	bare := strings.NewReplacer("{width}", "1536", "{height}", "1024", "{layers}", "").Replace(context.CanvasLayoutTemplate)
	if !strings.Contains(prompt, bare) || strings.Contains(prompt, strings.ReplaceAll(context.CanvasLayersTemplate, "{layers}", "0")) {
		t.Fatalf("unknown layer count:\n%s", prompt)
	}

	// The same object outside a canvas turn is not described as this turn's
	// source, and a plain upload never gets the canvas words.
	for name, options := range map[string]TurnOptions{
		"not a canvas turn": {Selection: []Attachment{snapshot}},
		"plain upload":      {InputSource: context.CanvasInputSource, Selection: []Attachment{{Handle: "@img5", Kind: AttachmentKindImage, Role: AttachmentRoleUpload, AssetID: "a"}}},
	} {
		prompt := orchestrator.selectionPrompt(options)
		if !strings.Contains(prompt, "@img5") || strings.Contains(prompt, context.SelectionCanvasNote) {
			t.Fatalf("%s:\n%s", name, prompt)
		}
	}
}

// The snapshot keeps its meaning in the history: later turns read it as the
// layout that was sent, not as a selected picture of an unnamed task.
func TestHistoryLabelsTheArtboardSnapshot(t *testing.T) {
	orchestrator, schema := canvasPromptOrchestrator(t)
	context := schema.Context
	message := Message{Attachments: []Attachment{{Handle: "@img5", Kind: AttachmentKindImage, Role: AttachmentRoleCanvas,
		AssetID: "snapshot", Canvas: &CanvasLayout{Width: 2048, Height: 2048, Layers: 2}}}}
	label := orchestrator.unpicturedLabels(message)
	layout := canvasLayoutText(context, message.Attachments[0].Canvas)
	want := strings.NewReplacer("{handle}", "@img5", "{layout}", layout).Replace(context.UnpicturedAttachmentTemplates["image.canvas"])
	if label != want || !strings.Contains(label, "2048") {
		t.Fatalf("label = %q; want %q", label, want)
	}
	upload := Message{Attachments: []Attachment{{Handle: "@img6", Kind: AttachmentKindImage, Role: AttachmentRoleUpload}}}
	if got := orchestrator.unpicturedLabels(upload); got != strings.NewReplacer("{handle}", "@img6", "{task_id}", "").Replace(context.UnpicturedAttachmentTemplates["image"]) {
		t.Fatalf("upload label = %q", got)
	}
}
