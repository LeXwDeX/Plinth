package agenttools

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// ToolListCanvasLayers lists the workspace board's layers as a last-resort
// source of material (#709). It is read only: it lists the board snapshot
// frozen with the turn and never reads the mutable board, sets an input
// source, returns a flattened picture or changes anything.
const ToolListCanvasLayers = "list_canvas_layers"

// listCanvasLayers answers list_canvas_layers from the turn's board snapshot.
// The server closes the fallback itself instead of leaving it to the prompt:
// a canvas turn already carries the board, a turn with attached or selected
// pictures has material, and one reply looks at the board once.
func (e *Executor) listCanvasLayers(_ context.Context, command chat.ToolCommand) chat.ToolResult {
	schema := e.canvasLayers
	if !schema.Enabled() {
		return chat.ToolResult{Error: "canvas layers unavailable", NoSubmission: true}
	}
	refuse := func(text string) chat.ToolResult {
		return chat.ToolResult{AgentContent: e.texts.Text(command.Options.Locale, text), NoSubmission: true}
	}
	if command.Options.InputSource != "" {
		return refuse(schema.CanvasTurnMessage)
	}
	if chat.CanvasTurnHasMaterial(command, e.historyLookup.UserRole) {
		return refuse(schema.TurnMaterialMessage)
	}
	for _, call := range command.EarlierCalls {
		if call.Name == command.Call.Name {
			return refuse(schema.RepeatedCallMessage)
		}
	}
	board := command.Options.CanvasContext
	if board == nil {
		return chat.ToolResult{Error: "canvas layers unavailable", NoSubmission: true}
	}
	if board.Total == 0 || len(board.Layers) == 0 {
		return chat.ToolResult{AgentContent: e.texts.Text(command.Options.Locale, schema.EmptyMessage), NoSubmission: true,
			Summary: canvasSummary(schema.SummaryTemplate, 0, 0)}
	}
	history := command.ReferenceHistory()
	layers := make([]map[string]any, 0, len(board.Layers))
	pictures := 0
	for _, layer := range board.Layers {
		item := map[string]any{
			"id": layer.ID, "type": layer.Type,
			"x": layer.X, "y": layer.Y, "width": layer.Width, "height": layer.Height,
			"z_index": layer.ZIndex, "visible": layer.Visible, "locked": layer.Locked,
		}
		if layer.Name != "" {
			item["name"] = layer.Name
		}
		if layer.Text != "" {
			item["text"] = layer.Text
		}
		if layer.ShapeType != "" {
			item["shape_type"] = layer.ShapeType
		}
		switch {
		case layer.Picture != nil && layer.Handle != "":
			pictures++
			item["handle"] = layer.Handle
			item["source"] = layer.Picture.Role
			// The same picture may already be a handle of the conversation.
			if same := sessionHandleOf(history, *layer.Picture); same != "" {
				item["same_as"] = same
			}
		case layer.Unreadable:
			item["note"] = e.texts.Text(command.Options.Locale, schema.UnreadableNote)
		}
		layers = append(layers, item)
	}
	payload := map[string]any{
		"layers": layers,
		"total":  board.Total,
		"note":   e.texts.Text(command.Options.Locale, schema.ResultNote),
	}
	if board.Width > 0 && board.Height > 0 {
		payload["board"] = map[string]int{"width": board.Width, "height": board.Height}
	}
	if board.Revision > 0 {
		payload["revision"] = board.Revision
	}
	if board.Total > len(board.Layers) {
		payload["truncated"] = strings.NewReplacer("{shown}", strconv.Itoa(len(board.Layers)),
			"{total}", strconv.Itoa(board.Total)).Replace(e.texts.Text(command.Options.Locale, schema.TruncatedTemplate))
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return chat.ToolResult{Error: "canvas layers unavailable", NoSubmission: true}
	}
	return chat.ToolResult{AgentContent: string(encoded), NoSubmission: true,
		Summary: canvasSummary(e.texts.Text(command.Options.Locale, schema.SummaryTemplate), board.Total, pictures)}
}

// sessionHandleOf is the newest conversation handle of the same picture, or "".
func sessionHandleOf(history []chat.Message, picture chat.Attachment) string {
	for index := len(history) - 1; index >= 0; index-- {
		for _, attachment := range history[index].Attachments {
			if attachment.Handle != "" && chat.SameObject(attachment, picture) {
				return attachment.Handle
			}
		}
	}
	return ""
}

// canvasSummary is the one-line summary kept with the tool call.
func canvasSummary(template string, layers, pictures int) json.RawMessage {
	line := strings.NewReplacer("{layers}", strconv.Itoa(layers), "{pictures}", strconv.Itoa(pictures)).Replace(template)
	encoded, err := json.Marshal(map[string]any{"line": line, "layers": layers, "pictures": pictures})
	if err != nil {
		return nil
	}
	return encoded
}
