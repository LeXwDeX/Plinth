package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// selectionVisionOrchestrator is the default catalog's chat with vision on and
// an inliner that names each URL, so a test can find a picture by its source.
func selectionVisionOrchestrator(t *testing.T) (*Orchestrator, catalog.ChatSchema) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application().Chat
	schema.Agent.Vision = true
	registry, err := compileToolRegistry(schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	orchestrator := &Orchestrator{
		schema:     schema,
		model:      "test-model",
		registry:   registry,
		agentScope: agentscope.FromSchema(schema.AgentScope),
		visionInline: func(_ context.Context, _ generation.OwnerID, url string) (string, bool) {
			return "data:image/png;base64," + strings.ReplaceAll(url, "/", "_"), true
		},
	}
	return orchestrator, schema
}

// selectionVisionHistory is the #679 conversation: an effect layer @img4 and
// the work badge @img5 were generated earlier; the newest user turn sends the
// board raster @img6 as its selection only (a stage action, not a canvas
// turn), so the message carries no picture slot for it.
func selectionVisionHistory(schema catalog.ChatSchema) ([]Message, Attachment) {
	selected := Attachment{Handle: "@img6", Kind: AttachmentKindImage, Role: AttachmentRoleUpload,
		AssetID: "board-raster", URL: "/api/v1/input-assets/board-raster"}
	return []Message{
		{ID: "u1", Role: schema.Roles.User, Content: "做一个能量弧特效"},
		{ID: "a1", Role: schema.Roles.Assistant, Content: "好了", ImageURLs: []string{"/effect"},
			Attachments: []Attachment{{Handle: "@img4", Kind: AttachmentKindImage, Role: AttachmentRoleGenerated,
				ImageSlot: 1, TaskID: "task-effect", AssetID: "effect"}}},
		{ID: "u2", Role: schema.Roles.User, Content: "再做一个工作证"},
		{ID: "a2", Role: schema.Roles.Assistant, Content: "好了", ImageURLs: []string{"/badge"},
			Attachments: []Attachment{{Handle: "@img5", Kind: AttachmentKindImage, Role: AttachmentRoleGenerated,
				ImageSlot: 1, TaskID: "task-badge", AssetID: "badge"}}},
		{ID: "u3", Role: schema.Roles.User, Content: "把画板里的图转成 3D 模型",
			Attachments: []Attachment{selected}},
	}, selected
}

// #679: a selected picture without a picture slot reaches the model as its
// own picture, right behind a handle label, after the newest user message and
// before the generated results' intro. Its dangling text label is gone, and
// nothing names the board layout or size.
func TestSelectedPictureWithoutSlotIsShownBesideItsHandle(t *testing.T) {
	orchestrator, schema := selectionVisionOrchestrator(t)
	history, selected := selectionVisionHistory(schema)
	options := TurnOptions{CreativeDomain: "image", Selection: []Attachment{selected}}
	request := orchestrator.agentRequest(context.Background(), history, options, "owner", "")
	last := request.Messages[len(request.Messages)-1]

	selectedURL := "data:image/png;base64,_api_v1_input-assets_board-raster"
	label := strings.ReplaceAll(schema.Context.Vision.SelectionLabelTemplate, "{handle}", "@img6")
	pictureAt, labelAt, introAt, selectionIntroAt := -1, -1, -1, -1
	for index, part := range last.Parts {
		switch {
		case part.URL == selectedURL:
			pictureAt = index
			if part.Detail != "high" {
				t.Fatalf("selected picture detail = %q", part.Detail)
			}
		case part.Text == label:
			labelAt = index
		case part.Text == schema.Context.GeneratedVisionIntro:
			introAt = index
		case part.Text == schema.Context.Vision.SelectionIntro:
			selectionIntroAt = index
		}
	}
	if pictureAt < 0 || labelAt != pictureAt+1 {
		t.Fatalf("selected picture %d / label %d missing or apart: %+v", pictureAt, labelAt, last.Parts)
	}
	if selectionIntroAt < 0 || selectionIntroAt > pictureAt {
		t.Fatalf("selection intro at %d, picture at %d", selectionIntroAt, pictureAt)
	}
	if introAt < 0 || introAt < labelAt {
		t.Fatalf("generated intro at %d must follow the selected picture's label at %d", introAt, labelAt)
	}
	text := partsText(last)
	unpictured := strings.NewReplacer("{handle}", "@img6", "{task_id}", "").
		Replace(schema.Context.UnpicturedAttachmentTemplates[AttachmentKindImage])
	if strings.Contains(text, unpictured) {
		t.Fatalf("the shown selection kept its text-only label too: %q", text)
	}
	if strings.Contains(text, "画板版面") || strings.Contains(request.Messages[0].Content, schema.Context.SelectionCanvasNote) {
		t.Fatalf("a non-canvas turn named the board layout")
	}
}

// The selected object already shown as a recent result is not shown twice,
// and a selected picture outranks low-detail history when places run out.
func TestSelectedPictureDeduplicatesAndOutranksHistory(t *testing.T) {
	orchestrator, schema := selectionVisionOrchestrator(t)
	history, _ := selectionVisionHistory(schema)
	// The person selects @img5 again under a new selection record: same asset.
	again := Attachment{Handle: "@img5", Kind: AttachmentKindImage, Role: AttachmentRoleGenerated,
		AssetID: "badge", TaskID: "task-badge", URL: "/badge"}
	plan := planForTest(t, orchestrator, history, TurnOptions{Selection: []Attachment{again}})
	if len(plan.Selected) != 0 {
		t.Fatalf("selected picture repeated the recent result: %+v", plan.Selected)
	}
	shown := 0
	for _, pick := range plan.Generated {
		if pick.URL == "data:image/png;base64,_badge" {
			shown++
		}
	}
	if shown != 1 {
		t.Fatalf("the recent result must stay shown once, got %d", shown)
	}

	// Two places in all: the selection role comes before history.
	vision := *schema.Context.Vision
	vision.MaximumImages = 1
	orchestrator.schema.Context.Vision = &vision
	_, selected := selectionVisionHistory(schema)
	plan = planForTest(t, orchestrator, []Message{
		{ID: "u1", Role: schema.Roles.User, Content: "old", ImageURLs: []string{"/old"}},
		{ID: "a1", Role: schema.Roles.Assistant, Content: "ok"},
		{ID: "u2", Role: schema.Roles.User, Content: "转 3D", Attachments: []Attachment{selected}},
	}, TurnOptions{Selection: []Attachment{selected}})
	if len(plan.Selected) != 1 || len(plan.Picks) != 0 {
		t.Fatalf("selected %d / history picks %d", len(plan.Selected), len(plan.Picks))
	}
}

func partsText(message agents.Message) string {
	var builder strings.Builder
	builder.WriteString(message.Content)
	for _, part := range message.Parts {
		builder.WriteString(part.Text)
	}
	return builder.String()
}
