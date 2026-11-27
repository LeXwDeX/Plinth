package generationtool

import (
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #644: the board is a turn's input only on a canvas turn (以画板为主). A turn
// without the declared input source works on the conversation: even with an
// artboard snapshot in its selection, the snapshot is not bound as a
// reference, it gives no canvas size or layout, and the single-layer version
// stand-in (#631) does not apply. The same selection on a canvas turn keeps
// every canvas-turn rule unchanged.
func TestBoardTakesNoPartOutsideACanvasTurn(t *testing.T) {
	executor, schema := handleExecutor(t)
	refine := schema.Tools[0]
	if refine.CanvasSource != canvasSourceBind {
		t.Fatalf("refine canvas source = %q", refine.CanvasSource)
	}
	layout := &chat.CanvasLayout{Width: 4096, Height: 4096, Layers: 1}
	selection := []chat.Attachment{{Handle: "@img5", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleCanvas,
		URL: "board-snapshot", Canvas: layout}}
	history := historyWithHandles(schema)

	conversation := chat.ToolCommand{History: history, Tool: refine, Options: chat.TurnOptions{Selection: selection}}
	if executor.canvasTurn(conversation) {
		t.Fatal("a turn without the canvas input source is a canvas turn")
	}
	if got := executor.canvasLayout(conversation); got != nil {
		t.Fatalf("the board's layout reached a turn that is not a canvas turn: %+v", got)
	}
	if snapshot, picture := executor.boardPicture(conversation); snapshot != "" || picture != "" {
		t.Fatalf("board stand-in outside a canvas turn: %q %q", snapshot, picture)
	}
	if got, err := executor.selectReferences(conversation, nil); err != nil || len(got) != 0 {
		t.Fatalf("the board snapshot was bound outside a canvas turn: %v %v", got, err)
	}
	if leading := executor.turnLeadingReferences(conversation, nil); len(leading) != 0 {
		t.Fatalf("the board snapshot leads the references outside a canvas turn: %v", leading)
	}

	board := conversation
	board.Options.InputSource = schema.Context.CanvasInputSource
	if !executor.canvasTurn(board) {
		t.Fatal("the declared canvas input source is not a canvas turn")
	}
	if got := executor.canvasLayout(board); !reflect.DeepEqual(got, layout) {
		t.Fatalf("canvas turn layout = %+v, want %+v", got, layout)
	}
	if got, err := executor.selectReferences(board, nil); err != nil || !reflect.DeepEqual(got, []string{"board-snapshot"}) {
		t.Fatalf("canvas turn references = %v, %v", got, err)
	}
}
