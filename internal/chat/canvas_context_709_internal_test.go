package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func canvasLayersSchema709(t *testing.T) catalog.ChatSchema {
	t.Helper()
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := loaded.Application().Chat
	if !schema.CanvasLayers.Enabled() {
		t.Fatal("the default catalog must declare chat.canvas_layers")
	}
	return schema
}

// boardOf709 is a canvas Port keyed by owner and session: another owner's
// session has no board, as the owner-scoped repository read answers.
type boardOf709 struct {
	owner, session string
	board          CanvasBoard
	err            error
	reads          int
}

func (source *boardOf709) Board(_ context.Context, ownerID, sessionID string) (CanvasBoard, bool, error) {
	source.reads++
	if source.err != nil {
		return CanvasBoard{}, false, source.err
	}
	if ownerID != source.owner || sessionID != source.session {
		return CanvasBoard{}, false, nil
	}
	return source.board, true, nil
}

// pictures709 verifies only the owner's platform input assets.
type pictures709 struct{ owner string }

func (verifier pictures709) VerifyAttachment(_ context.Context, ownerID, _ string, ref AttachmentRef) (Attachment, error) {
	if ownerID != verifier.owner || !strings.HasPrefix(ref.URL, "/api/assets/") {
		return Attachment{}, ErrAttachmentRejected
	}
	id := strings.TrimPrefix(ref.URL, "/api/assets/")
	return Attachment{Kind: AttachmentKindImage, Role: AttachmentRoleUpload, AssetID: id, URL: ref.URL}, nil
}

func boardService709(t *testing.T, source CanvasSource, owner string) *Service {
	schema := canvasLayersSchema709(t)
	return &Service{canvasLayers: schema.CanvasLayers, canvas: source, attachments: pictures709{owner: owner}}
}

func TestFreezeCanvasContextVerifiesEveryPictureLayer709(t *testing.T) {
	source := &boardOf709{owner: "u1", session: "s1", board: CanvasBoard{ProjectID: "canvas_1", Revision: 3, Width: 800, Height: 600,
		Layers: []CanvasBoardLayer{
			{ID: "bottom", Type: "image", ZIndex: 0, Visible: true, ImageURL: "/api/assets/in_low"},
			{ID: "foreign", Type: "image", ZIndex: 5, Visible: true, ImageURL: "https://example.invalid/a.png"},
			{ID: "data", Type: "image", ZIndex: 4, ImageURL: "data:image/png;base64,AAAA"},
			{ID: "note", Type: "text", ZIndex: 3, Text: strings.Repeat("字", 500)},
			{ID: "top", Type: "image", ZIndex: 6, Visible: true, Locked: true, ImageURL: "/api/assets/in_top"},
		}}}
	service := boardService709(t, source, "u1")
	options := TurnOptions{}
	service.freezeCanvasContext(context.Background(), StartTurn{OwnerID: "u1", SessionID: "s1"}, &options)
	board := options.CanvasContext
	if board == nil || board.Total != 5 || len(board.Layers) != 5 || board.Width != 800 || board.Revision != 3 {
		t.Fatalf("frozen board = %+v", board)
	}
	order := []string{}
	for _, layer := range board.Layers {
		order = append(order, layer.ID)
	}
	if strings.Join(order, ",") != "top,foreign,data,note,bottom" {
		t.Fatalf("layers are listed top first: %v", order)
	}
	if top := board.Layers[0]; top.Handle != "@canvas1" || top.Picture == nil || top.Picture.AssetID != "in_top" || !top.Locked {
		t.Fatalf("top = %+v", top)
	}
	for _, index := range []int{1, 2} {
		if layer := board.Layers[index]; layer.Handle != "" || layer.Picture != nil || !layer.Unreadable {
			t.Fatalf("an external or data address gets no handle: %+v", layer)
		}
	}
	if note := board.Layers[3]; len([]rune(note.Text)) > service.canvasLayers.TextCharacters+1 {
		t.Fatalf("the layer text is bounded: %d runes", len([]rune(note.Text)))
	}
	if bottom := board.Layers[4]; bottom.Handle != "@canvas2" {
		t.Fatalf("bottom = %+v", bottom)
	}
	if layer := board.LayerByHandle("@CANVAS2"); layer == nil || layer.ID != "bottom" {
		t.Fatalf("handles resolve without regard to case: %+v", layer)
	}
}

func TestFreezeCanvasContextStaysWithinTheOwnerAndLimit709(t *testing.T) {
	layers := make([]CanvasBoardLayer, 0, 60)
	for index := 0; index < 60; index++ {
		layers = append(layers, CanvasBoardLayer{ID: "n", Type: "pencil", ZIndex: index})
	}
	source := &boardOf709{owner: "u1", session: "s1", board: CanvasBoard{Layers: layers}}
	service := boardService709(t, source, "u1")

	options := TurnOptions{}
	service.freezeCanvasContext(context.Background(), StartTurn{OwnerID: "u1", SessionID: "s1"}, &options)
	if options.CanvasContext.Total != 60 || len(options.CanvasContext.Layers) != service.canvasLayers.LayerLimit {
		t.Fatalf("limit: total=%d listed=%d", options.CanvasContext.Total, len(options.CanvasContext.Layers))
	}

	other := TurnOptions{}
	service.freezeCanvasContext(context.Background(), StartTurn{OwnerID: "u2", SessionID: "s1"}, &other)
	if other.CanvasContext == nil || other.CanvasContext.Total != 0 || len(other.CanvasContext.Layers) != 0 {
		t.Fatalf("another owner reads an empty board: %+v", other.CanvasContext)
	}

	canvasTurn := TurnOptions{InputSource: "canvas"}
	reads := source.reads
	service.freezeCanvasContext(context.Background(), StartTurn{OwnerID: "u1", SessionID: "s1"}, &canvasTurn)
	if canvasTurn.CanvasContext != nil || source.reads != reads {
		t.Fatal("a canvas turn freezes no board snapshot and reads no board")
	}

	failing := boardService709(t, &boardOf709{err: errors.New("down")}, "u1")
	broken := TurnOptions{}
	failing.freezeCanvasContext(context.Background(), StartTurn{OwnerID: "u1", SessionID: "s1"}, &broken)
	if broken.CanvasContext != nil {
		t.Fatal("a failed board read leaves the turn without a snapshot")
	}
}

func TestResolveCanvasHandleUsesOnlyTheFrozenSnapshot709(t *testing.T) {
	schema := canvasLayersSchema709(t).CanvasLayers
	options := TurnOptions{CanvasContext: &CanvasContext{Total: 2, Layers: []CanvasLayerContext{
		{ID: "a", Type: "image", Handle: "@canvas1", Picture: &Attachment{Kind: "image", AssetID: "in_1", URL: "/api/assets/in_1"}},
		{ID: "b", Type: "image", Unreadable: true},
	}}}
	picture, matched, err := ResolveCanvasHandle(schema, options, "@canvas1")
	if !matched || err != nil || picture.URL != "/api/assets/in_1" || picture.Attachment.Handle != "@canvas1" {
		t.Fatalf("resolved = %+v matched=%v err=%v", picture, matched, err)
	}
	if _, matched, err := ResolveCanvasHandle(schema, options, "@img1"); matched || err != nil {
		t.Fatal("an attachment handle is not a board handle")
	}
	_, matched, err = ResolveCanvasHandle(schema, options, "@canvas2")
	var refusal *CanvasHandleError
	if !matched || !errors.As(err, &refusal) || !strings.Contains(refusal.Message, "@canvas2") ||
		!strings.Contains(refusal.Message, schema.ToolName) {
		t.Fatalf("an unreadable layer is refused by name: %v", err)
	}
	if _, matched, err := ResolveCanvasHandle(schema, TurnOptions{}, "@canvas1"); !matched || err == nil {
		t.Fatal("without a frozen snapshot a board handle is refused, never guessed")
	}
}

func TestCanvasPromptLineOnlyOutsideACanvasTurn709(t *testing.T) {
	schema := canvasLayersSchema709(t)
	orchestrator := &Orchestrator{schema: schema}
	board := &CanvasContext{Total: 5, OffConversation: 3}
	line := orchestrator.canvasPrompt(context.Background(), TurnOptions{CanvasContext: board})
	if !strings.Contains(line, "3") || strings.Contains(line, "5") || !strings.Contains(line, schema.CanvasLayers.ToolName) {
		t.Fatalf("the board line counts only the layers the conversation lacks: %q", line)
	}
	for name, options := range map[string]TurnOptions{
		"canvas turn":             {CanvasContext: board, InputSource: "canvas"},
		"empty board":             {CanvasContext: &CanvasContext{}},
		"all in the conversation": {CanvasContext: &CanvasContext{Total: 2}},
		"no snapshot":             {},
	} {
		if line := orchestrator.canvasPrompt(context.Background(), options); line != "" {
			t.Fatalf("%s adds no board line: %q", name, line)
		}
	}
}

func TestCanvasContextSurvivesTheStoredTurn709(t *testing.T) {
	stored := TurnOptions{CanvasContext: &CanvasContext{Total: 1, Layers: []CanvasLayerContext{
		{ID: "a", Handle: "@canvas1", Picture: &Attachment{Kind: "image", URL: "/api/assets/in_1"}}}}}
	options := TurnOptions{CanvasContext: &CanvasContext{Total: 9}}
	copyFrozenProject(&options, stored)
	if options.CanvasContext == nil || options.CanvasContext.Total != 1 || options.CanvasContext.Layers[0].Picture == stored.CanvasContext.Layers[0].Picture {
		t.Fatalf("the stored snapshot replaces the request's and is copied: %+v", options.CanvasContext)
	}
}

// history709 is a repository whose only read is the session's stored messages.
type history709 struct {
	Repository
	messages []Message
}

func (repository history709) ListMessages(context.Context, string, string) ([]Message, error) {
	return repository.messages, nil
}

func TestFreezeCanvasContextMarksNewAndConversationLayers709(t *testing.T) {
	lastTurn := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	source := &boardOf709{owner: "u1", session: "s1", board: CanvasBoard{Width: 800, Height: 600, FrameX: 200, FrameY: 100,
		Layers: []CanvasBoardLayer{
			// The picture the conversation already holds as @img1: the Agent's own result, placed on the
			// board after the last message, is still not "new" (the person did not put it there).
			{ID: "old", Type: "image", ZIndex: 1, X: 200, Y: 100, Width: 100, Height: 100,
				ImageURL: "/api/assets/in_old", CreatedAt: lastTurn.Add(30 * time.Second)},
			// Put on the board after the last message, outside the frame and rotated.
			{ID: "new", Type: "image", ZIndex: 3, X: 1100, Y: 100, Width: 100, Height: 100,
				ImageURL: "/api/assets/in_new", CreatedAt: lastTurn.Add(time.Minute), Transformed: ptrBool709(true)},
			// An annotation always counts, even an old one.
			{ID: "note", Type: "text", ZIndex: 2, X: 250, Y: 150, Width: 50, Height: 20, Text: "改成红色",
				CreatedAt: lastTurn.Add(-time.Hour)},
		}}}
	service := boardService709(t, source, "u1")
	service.userRole = "user"
	service.repository = history709{messages: []Message{
		{Role: "user", CreatedAt: lastTurn.Add(-2 * time.Hour)},
		{Role: "assistant", CreatedAt: lastTurn.Add(2 * time.Hour),
			Attachments: []Attachment{{Kind: AttachmentKindImage, AssetID: "in_old", URL: "/api/assets/in_old", Handle: "@img1"}}},
		{Role: "user", CreatedAt: lastTurn},
	}}
	options := TurnOptions{}
	service.freezeCanvasContext(context.Background(), StartTurn{OwnerID: "u1", SessionID: "s1"}, &options)
	board := options.CanvasContext
	if board == nil || board.Total != 3 || board.OffConversation != 2 {
		t.Fatalf("the conversation picture is not counted: %+v", board)
	}
	layers := map[string]CanvasLayerContext{}
	for _, layer := range board.Layers {
		layers[layer.ID] = layer
	}
	if old := layers["old"]; !old.InConversation || old.AddedAfterLastTurn || old.InFrame == nil || !*old.InFrame || old.Transformed != nil {
		t.Fatalf("old layer = %+v", old)
	}
	if fresh := layers["new"]; fresh.InConversation || !fresh.AddedAfterLastTurn || fresh.InFrame == nil || *fresh.InFrame ||
		fresh.Transformed == nil || !*fresh.Transformed {
		t.Fatalf("new layer = %+v", fresh)
	}
	if note := layers["note"]; note.InConversation || note.AddedAfterLastTurn || note.InFrame == nil || !*note.InFrame {
		t.Fatalf("note layer = %+v", note)
	}

	// Only the conversation's own picture on the board: no board line.
	source.board.Layers = source.board.Layers[:1]
	options = TurnOptions{}
	service.freezeCanvasContext(context.Background(), StartTurn{OwnerID: "u1", SessionID: "s1"}, &options)
	if options.CanvasContext.OffConversation != 0 {
		t.Fatalf("off conversation = %d", options.CanvasContext.OffConversation)
	}
	orchestrator := &Orchestrator{schema: canvasLayersSchema709(t)}
	if line := orchestrator.canvasPrompt(context.Background(), options); line != "" {
		t.Fatalf("a board holding only conversation pictures adds no line: %q", line)
	}

	// A session without an earlier user message: every layer is new.
	service.repository = history709{}
	options = TurnOptions{}
	service.freezeCanvasContext(context.Background(), StartTurn{OwnerID: "u1", SessionID: "s1"}, &options)
	if !options.CanvasContext.Layers[0].AddedAfterLastTurn || options.CanvasContext.Layers[0].InFrame == nil {
		t.Fatalf("first turn layer = %+v", options.CanvasContext.Layers[0])
	}
}

func ptrBool709(value bool) *bool { return &value }
