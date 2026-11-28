package app

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/canvas"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// canvasBoardReader is the read-only part of the canvas repository the board
// Port needs. It never creates the session's board.
type canvasBoardReader interface {
	GetProjectBySession(ctx context.Context, ownerID, sessionID string) (canvas.Project, bool, error)
}

// canvasBoardSource adapts the canvas repository to chat.CanvasSource (#709).
// chat does not import canvas (canvas depends on chat), so the conversion of a
// stored project into the chat board facts lives here. The node data is the
// browser's own JSON and is not trusted: only plain strings are kept and
// chat verifies every picture before it gets a handle; the Agent never reads
// the saved address.
type canvasBoardSource struct {
	repository canvasBoardReader
	// centerX and centerY are canvas.frontend.artboard: the world centre of
	// the canvas frame, so the frame's corner is the centre minus half its size.
	centerX, centerY float64
}

func (source canvasBoardSource) Board(ctx context.Context, ownerID, sessionID string) (chat.CanvasBoard, bool, error) {
	project, found, err := source.repository.GetProjectBySession(ctx, ownerID, sessionID)
	if err != nil || !found {
		return chat.CanvasBoard{}, false, err
	}
	board := chat.CanvasBoard{ProjectID: project.ID, Revision: project.Revision}
	if project.Frame != nil {
		board.Width, board.Height = project.Frame.Width, project.Frame.Height
		board.FrameX = source.centerX - float64(board.Width)/2
		board.FrameY = source.centerY - float64(board.Height)/2
	}
	for _, node := range project.Nodes {
		layer := chat.CanvasBoardLayer{
			ID: node.ID, Type: node.Type, X: node.X, Y: node.Y, Width: node.Width, Height: node.Height,
			ZIndex: node.ZIndex, Visible: node.Visible, Locked: node.Locked, CreatedAt: node.CreatedAt,
		}
		var data map[string]any
		if json.Unmarshal([]byte(node.Data), &data) == nil {
			text := func(key string) string {
				value, _ := data[key].(string)
				return value
			}
			layer.Name, layer.Text, layer.ShapeType = text("name"), text("text"), text("shapeType")
			layer.TaskID, layer.CaptureID = text("taskId"), text("captureId")
			// The address goes to the verifier as the browser saved it, like a
			// selection claim; only the verified platform URL is kept.
			layer.ImageURL = strings.TrimSpace(text("imageUrl"))
			layer.Transformed = nodeTransformed(data)
		}
		board.Layers = append(board.Layers, layer)
	}
	return board, true, nil
}

// nodeTransformed reads the browser's crop, rotation and opacity: true when
// any of them changes the picture, false when the data has one of them at its
// neutral value, nil when it has none (the browser omits neutral values).
func nodeTransformed(data map[string]any) *bool {
	known, changed := false, false
	if rotation, ok := data["rotation"].(float64); ok {
		known, changed = true, changed || rotation != 0
	}
	if opacity, ok := data["opacity"].(float64); ok {
		known, changed = true, changed || opacity != 1
	}
	if crop, ok := data["crop"].(map[string]any); ok && len(crop) > 0 {
		known, changed = true, true
	}
	if !known {
		return nil
	}
	return &changed
}
