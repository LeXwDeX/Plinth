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
}

func (source canvasBoardSource) Board(ctx context.Context, ownerID, sessionID string) (chat.CanvasBoard, bool, error) {
	project, found, err := source.repository.GetProjectBySession(ctx, ownerID, sessionID)
	if err != nil || !found {
		return chat.CanvasBoard{}, false, err
	}
	board := chat.CanvasBoard{ProjectID: project.ID, Revision: project.Revision}
	if project.Frame != nil {
		board.Width, board.Height = project.Frame.Width, project.Frame.Height
	}
	for _, node := range project.Nodes {
		layer := chat.CanvasBoardLayer{
			ID: node.ID, Type: node.Type, X: node.X, Y: node.Y, Width: node.Width, Height: node.Height,
			ZIndex: node.ZIndex, Visible: node.Visible, Locked: node.Locked,
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
		}
		board.Layers = append(board.Layers, layer)
	}
	return board, true, nil
}
