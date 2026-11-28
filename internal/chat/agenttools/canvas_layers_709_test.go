package agenttools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func canvasLayersExecutor(t *testing.T) *Executor {
	t.Helper()
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := loaded.Application()
	if !application.Chat.CanvasLayers.Enabled() {
		t.Fatal("the default catalog must declare chat.canvas_layers")
	}
	return &Executor{canvasLayers: application.Chat.CanvasLayers, context: application.Chat.Context,
		historyLookup: chat.NewHistoryLookupPolicy(application.Chat)}
}

func canvasCommand(board *chat.CanvasContext) chat.ToolCommand {
	return chat.ToolCommand{
		Call:    agents.ToolCall{ID: "call-1", Name: ToolViewCanvas},
		History: []chat.Message{{Role: "user", Content: "直接开始干活吧。"}},
		Options: chat.TurnOptions{CanvasContext: board},
	}
}

func TestViewCanvasSaysTheBoardIsEmpty709(t *testing.T) {
	executor := canvasLayersExecutor(t)
	for _, board := range []*chat.CanvasContext{{}, {Total: 0, Layers: nil}} {
		result := executor.viewCanvas(context.Background(), canvasCommand(board))
		if !result.NoSubmission || result.Error != "" || result.AgentContent != executor.canvasLayers.EmptyMessage {
			t.Fatalf("empty board result = %+v", result)
		}
		if !strings.Contains(result.AgentContent, "画板上没有内容") {
			t.Fatalf("the empty answer must say the board is empty: %q", result.AgentContent)
		}
	}
}

func TestViewCanvasListsLayersAndHandles709(t *testing.T) {
	executor := canvasLayersExecutor(t)
	inFrame, transformed := false, true
	board := &chat.CanvasContext{ProjectID: "canvas_1", Revision: 7, Width: 1024, Height: 1024, Total: 4, Layers: []chat.CanvasLayerContext{
		{ID: "n1", Type: "image", X: 10, Y: 20, Width: 512, Height: 512, ZIndex: 3, Visible: true, Handle: "@canvas1",
			AddedAfterLastTurn: true, InFrame: &inFrame, Transformed: &transformed,
			Picture: &chat.Attachment{Kind: "image", Role: "upload", AssetID: "in_1", URL: "/api/assets/in_1"}},
		{ID: "n2", Type: "text", ZIndex: 2, Visible: false, Locked: true, Text: "红色披风"},
		{ID: "n3", Type: "shape", ZIndex: 1, Visible: true, ShapeType: "arrow"},
		{ID: "n4", Type: "image", ZIndex: 0, Visible: true, Unreadable: true},
	}}
	command := canvasCommand(board)
	command.History = append([]chat.Message{{Role: "assistant", Attachments: []chat.Attachment{
		{Handle: "@img2", Kind: "image", Role: "upload", AssetID: "in_1"}}}}, command.History...)
	result := executor.viewCanvas(context.Background(), command)
	if !result.NoSubmission || result.Error != "" || len(result.Pictures) != 0 || len(result.ImageURLs) != 0 {
		t.Fatalf("a read-only listing shows no picture and submits nothing: %+v", result)
	}
	if strings.Contains(result.AgentContent, "/api/assets/") {
		t.Fatalf("the listing must not carry a picture address: %s", result.AgentContent)
	}
	var payload struct {
		Layers []map[string]any `json:"layers"`
		Total  int              `json:"total"`
		Note   string           `json:"note"`
		Board  map[string]int   `json:"board"`
	}
	if err := json.Unmarshal([]byte(result.AgentContent), &payload); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &raw); err != nil {
		t.Fatal(err)
	}
	// The flattened preview is step 2: the key is there, null, with its reason.
	if preview, present := raw["preview"]; !present || preview != nil ||
		raw["preview_reason"] != executor.canvasLayers.PreviewReason || raw["preview_note"] != executor.canvasLayers.PreviewNote {
		t.Fatalf("preview keys = %v %v %v", raw["preview"], raw["preview_reason"], raw["preview_note"])
	}
	// Several layers and annotations: the card must offer to go on with the board.
	if raw["continue_on_canvas_label"] != executor.canvasLayers.ContinueOnCanvasLabel {
		t.Fatalf("continue label = %v", raw["continue_on_canvas_label"])
	}
	if payload.Total != 4 || len(payload.Layers) != 4 || payload.Note == "" || payload.Board["width"] != 1024 {
		t.Fatalf("payload = %+v", payload)
	}
	first := payload.Layers[0]
	if first["handle"] != "@canvas1" || first["same_as"] != "@img2" || first["source"] != "upload" ||
		first["in_conversation"] != true || first["added_after_last_turn"] != true ||
		first["in_frame"] != false || first["transformed"] != true {
		t.Fatalf("picture layer = %v", first)
	}
	if text := payload.Layers[1]; text["text"] != "红色披风" || text["visible"] != false || text["locked"] != true {
		t.Fatalf("text layer = %v", text)
	}
	if shape := payload.Layers[2]; shape["shape_type"] != "arrow" || shape["handle"] != nil ||
		shape["added_after_last_turn"] != false {
		t.Fatalf("shape layer = %v", shape)
	}
	// Without data the frame and transform keys are left out, not guessed.
	if _, present := payload.Layers[2]["in_frame"]; present {
		t.Fatalf("shape layer = %v", payload.Layers[2])
	}
	if _, present := payload.Layers[2]["transformed"]; present {
		t.Fatalf("shape layer = %v", payload.Layers[2])
	}
	if unreadable := payload.Layers[3]; unreadable["handle"] != nil || unreadable["note"] != executor.canvasLayers.UnreadableNote {
		t.Fatalf("an unverified picture is listed without a handle: %v", unreadable)
	}
	var summary map[string]any
	if json.Unmarshal(result.Summary, &summary) != nil || summary["layers"] != float64(4) || summary["pictures"] != float64(1) {
		t.Fatalf("summary = %s", result.Summary)
	}
}

func TestViewCanvasStatesTruncation709(t *testing.T) {
	executor := canvasLayersExecutor(t)
	board := &chat.CanvasContext{Total: 80, Layers: []chat.CanvasLayerContext{{ID: "n1", Type: "pencil", Visible: true}}}
	result := executor.viewCanvas(context.Background(), canvasCommand(board))
	var payload struct {
		Truncated string `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(result.AgentContent), &payload); err != nil || !strings.Contains(payload.Truncated, "80") ||
		!strings.Contains(payload.Truncated, "1") {
		t.Fatalf("truncation line = %q (%v)", payload.Truncated, err)
	}
}

// The fallback is closed by the server, not by the prompt (#709).
func TestViewCanvasRefusesWhenTheTurnHasMaterial709(t *testing.T) {
	executor := canvasLayersExecutor(t)
	board := &chat.CanvasContext{Total: 1, Layers: []chat.CanvasLayerContext{{ID: "n1", Type: "image", Handle: "@canvas1",
		Picture: &chat.Attachment{Kind: "image", URL: "/api/assets/in_1"}}}}
	schema := executor.canvasLayers

	canvasTurn := canvasCommand(board)
	canvasTurn.Options.InputSource = "canvas"
	if got := executor.viewCanvas(context.Background(), canvasTurn).AgentContent; got != schema.CanvasTurnMessage {
		t.Fatalf("canvas turn = %q", got)
	}
	selected := canvasCommand(board)
	selected.Options.Selection = []chat.Attachment{{Handle: "@img1", Kind: "image"}}
	if got := executor.viewCanvas(context.Background(), selected).AgentContent; got != schema.TurnMaterialMessage {
		t.Fatalf("selection = %q", got)
	}
	attached := canvasCommand(board)
	attached.History[0].ImageURLs = []string{"/api/assets/in_2"}
	if got := executor.viewCanvas(context.Background(), attached).AgentContent; got != schema.TurnMaterialMessage {
		t.Fatalf("attached picture = %q", got)
	}
	repeated := canvasCommand(board)
	repeated.EarlierCalls = []chat.ToolCall{{ID: "call-0", Name: ToolViewCanvas}}
	if got := executor.viewCanvas(context.Background(), repeated).AgentContent; got != schema.RepeatedCallMessage {
		t.Fatalf("second call of a reply = %q", got)
	}
	if result := executor.viewCanvas(context.Background(), canvasCommand(nil)); result.Error == "" {
		t.Fatalf("a turn without a frozen board snapshot cannot list it: %+v", result)
	}
}

func TestViewCanvasIsDispatched709(t *testing.T) {
	executor := canvasLayersExecutor(t)
	result, err := executor.execute(context.Background(), canvasCommand(&chat.CanvasContext{}), nil)
	if err != nil || result.AgentContent != executor.canvasLayers.EmptyMessage {
		t.Fatalf("dispatch = %+v %v", result, err)
	}
}

// create_three_view takes an @canvasN handle as its source picture (#709).
func TestThreeViewTakesABoardHandle709(t *testing.T) {
	executor := canvasLayersExecutor(t)
	command := canvasCommand(&chat.CanvasContext{Total: 1, Layers: []chat.CanvasLayerContext{{ID: "n1", Type: "image",
		Handle: "@canvas1", Picture: &chat.Attachment{Kind: "image", URL: "/api/assets/in_1"}}}})
	urls, refusal := executor.threeViewImages(command, nil, []string{"@canvas1"})
	if refusal != "" || len(urls) != 1 || urls[0] != "/api/assets/in_1" {
		t.Fatalf("urls=%v refusal=%q", urls, refusal)
	}
	if _, refusal := executor.threeViewImages(command, nil, []string{"@canvas9"}); !strings.Contains(refusal, "@canvas9") {
		t.Fatalf("an unknown board handle must be refused by name: %q", refusal)
	}
}

func TestViewCanvasOneLonePictureNeedsNoBoardOption709(t *testing.T) {
	executor := canvasLayersExecutor(t)
	command := canvasCommand(&chat.CanvasContext{Total: 1, OffConversation: 1, Layers: []chat.CanvasLayerContext{
		{ID: "n1", Type: "image", Visible: true, Handle: "@canvas1",
			Picture: &chat.Attachment{Kind: "image", Role: "upload", AssetID: "in_1", URL: "/api/assets/in_1"}}}})
	result := executor.viewCanvas(context.Background(), command)
	var raw map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &raw); err != nil {
		t.Fatal(err)
	}
	if _, present := raw["continue_on_canvas_label"]; present {
		t.Fatalf("one picture and no annotation: %v", raw)
	}
	if layer := raw["layers"].([]any)[0].(map[string]any); layer["in_conversation"] != false {
		t.Fatalf("layer = %v", layer)
	}
}
