package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/canvas"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/chat/agenttools"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/generation"

	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
)

// noTasks709 holds no Task: the board fixture uses uploaded pictures only.
type noTasks709 struct{}

func (noTasks709) Get(context.Context, generation.OwnerID, generation.TaskID) (generation.Task, error) {
	return generation.Task{}, errors.New("no task")
}

func (noTasks709) OpenAsset(context.Context, generation.OwnerID, generation.TaskID, generation.AssetID) (generation.AssetContent, error) {
	return generation.AssetContent{}, errors.New("no task")
}

type noLegacy709 struct{}

func (noLegacy709) Open(context.Context, string) (io.ReadCloser, int64, error) {
	return nil, 0, errors.New("no legacy picture")
}

type noGenerate709 struct{}

func (noGenerate709) Execute(context.Context, chat.ToolCommand, func(chat.ToolObservation) error) (chat.ToolResult, error) {
	return chat.ToolResult{}, errors.New("no generation in the board fixture")
}

// boardFixture709 wires the sqlite canvas and chat repositories, the real
// input asset store and attachment resolver and the board Port as the
// application does, with one clock for chat and canvas.
type boardFixture709 struct {
	application catalog.ApplicationSchema
	chat        *chat.Service
	canvas      *canvas.Service
	inputs      *inputassets.Service
	now         time.Time
}

func newBoardFixture709(t *testing.T) *boardFixture709 {
	t.Helper()
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	fixture := &boardFixture709{application: loaded.Application(),
		now: time.Date(2026, 11, 28, 9, 0, 0, 0, time.UTC)}
	application := fixture.application
	root := t.TempDir()
	repository, err := sqlite.Open(filepath.Join(root, "board.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	blobs, err := local.New(filepath.Join(root, "cas"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blobs.Close() })
	fixture.inputs, err = inputassets.New(repository, blobs)
	if err != nil {
		t.Fatal(err)
	}
	routes := application.Runtime.HTTP
	resolver, err := references.New(references.Config{
		Inputs: fixture.inputs, Tasks: noTasks709{}, Legacy: noLegacy709{},
		InputAssetRoutes:                   []string{routes.Routes.AssetItem, routes.LegacyRoutes.AssetItem},
		TaskAssetRoute:                     routes.Routes.TaskAsset,
		GeneratedImageRoute:                routes.LegacyRoutes.GeneratedImage,
		UploadMaxBytes:                     application.Runtime.InputAssets.UploadMaxBytes,
		GeneratedReferenceMaxBytes:         application.Runtime.InputAssets.GeneratedReferenceMaxBytes,
		GeneratedReferenceTooLargeTemplate: application.Runtime.InputAssets.Messages.GeneratedReferenceTooLargeTemplate,
		DefaultMediaType:                   application.Runtime.InputAssets.DefaultMediaType,
		DefaultAssetType:                   application.Runtime.InputAssets.DefaultAssetType,
		ReferenceFilename:                  application.Chat.Context.ReferenceFilename,
		LegacyGeneratedImage:               application.Runtime.AssetDelivery.LegacyGeneratedImage,
	})
	if err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return fixture.now }
	schema := application.Chat
	fixture.chat, err = chat.NewService(chat.ServiceConfig{
		Handles:     chat.NewHandlePolicy(schema.Context),
		Attachments: resolver,
		Repository:  repository, Clock: clock,
		DefaultTitle: schema.Defaults.Title, UserRole: schema.Roles.User, AssistantRole: schema.Roles.Assistant,
		PendingContent: schema.State.PendingContent, StreamActive: schema.State.Active, StreamDone: schema.State.Done,
		StreamError: schema.State.Error, TurnQueued: schema.State.TurnQueued, TurnRunning: schema.State.TurnRunning,
		TurnWaitingTask: schema.State.TurnWaitingTask, TurnDone: schema.State.TurnDone, TurnError: schema.State.TurnError,
		TurnEndReasons: schema.State.TurnEndReasons,
		PlaceholderHealing: chat.PlaceholderHealingPolicy{
			StaleAfter: time.Minute, PromptArgument: schema.Context.ToolPromptArgument, PromptPreviewCharacters: 10,
			InterruptedMessage:        schema.Presentation.Messages.Interrupted,
			InterruptedPromptTemplate: schema.Presentation.Messages.InterruptedPrompt,
		},
		StreamCleanupTTL: time.Minute, SubscriberBufferSize: 8,
		EventFields:  chat.EventFieldPolicy{Type: schema.Stream.EventFields.Type, MessageID: schema.Stream.EventFields.MessageID},
		EventTypes:   chat.EventTypePolicy{MessageCreated: schema.Stream.EventTypes.MessageCreated},
		CanvasLayers: schema.CanvasLayers,
		Canvas: canvasBoardSource{repository: repository,
			centerX: application.Canvas.Frontend.Artboard.CenterX,
			centerY: application.Canvas.Frontend.Artboard.CenterY},
	})
	if err != nil {
		t.Fatal(err)
	}
	defaults := application.Canvas.Defaults
	fixture.canvas, err = canvas.New(canvas.Config{
		Repository: repository, Sessions: fixture.chat, Clock: clock,
		DefaultName: defaults.ProjectName, DefaultViewport: defaults.Viewport, DefaultBGStyle: defaults.BGStyle,
		ProjectIDPrefix: defaults.ProjectIDPrefix, MaximumBitmapNodes: application.Canvas.Limits.MaximumBitmapNodes,
		MaximumPlacedResults:             application.Canvas.Limits.MaximumPlacedResults,
		MaximumPlacedResultKeyCharacters: application.Canvas.Limits.MaximumPlacedResultKeyCharacters,
		MinimumFrameDimension:            application.Canvas.Limits.MinimumDimension,
		MaximumFrameDimension:            application.Canvas.Limits.MaximumDimension,
		CoordinateVersion:                defaults.CoordinateVersion,
		NodeDefaults: canvas.Node{X: defaults.NodeX, Y: defaults.NodeY, Width: defaults.NodeWidth, Height: defaults.NodeHeight,
			ZIndex: defaults.NodeZIndex, Data: defaults.NodeData, Visible: defaults.NodeVisible, Locked: defaults.NodeLocked},
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (fixture *boardFixture709) upload(t *testing.T, owner string) string {
	t.Helper()
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	asset, err := fixture.inputs.Upload(context.Background(), generation.OwnerID(owner), &picture, "image/png", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(fixture.application.Runtime.HTTP.Routes.AssetItem, "{assetID}", string(asset.ID))
}

type node709 struct {
	id, kind string
	x        float64
	z        int
	data     map[string]any
}

// save replaces the board's nodes as the browser's save does.
func (fixture *boardFixture709) save(t *testing.T, owner, projectID string, frame *canvas.Frame, nodes ...node709) {
	t.Helper()
	project, err := fixture.canvas.GetProject(context.Background(), owner, projectID)
	if err != nil {
		t.Fatal(err)
	}
	inputs := make([]canvas.NodeInput, 0, len(nodes))
	for _, node := range nodes {
		encoded, err := json.Marshal(node.data)
		if err != nil {
			t.Fatal(err)
		}
		data, x, y, size, z, visible := string(encoded), node.x, 0.0, 100.0, node.z, true
		inputs = append(inputs, canvas.NodeInput{ID: node.id, Type: node.kind, X: &x, Y: &y, Width: &size, Height: &size,
			ZIndex: &z, Data: &data, Visible: &visible})
	}
	if _, err := fixture.canvas.ReplaceNodes(context.Background(), owner, projectID, project.Revision, inputs, nil, frame); err != nil {
		t.Fatal(err)
	}
}

// turn starts a plain (not canvas) turn and returns its stored options and
// the tool command the orchestrator would build for view_canvas.
func (fixture *boardFixture709) turn(t *testing.T, owner, sessionID, content string) (chat.Turn, chat.ToolCommand) {
	t.Helper()
	ctx := context.Background()
	assistant, err := fixture.chat.StartTurn(ctx, chat.StartTurn{OwnerID: owner, SessionID: sessionID, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := fixture.chat.GetTurn(ctx, owner, sessionID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	history, err := fixture.chat.ListMessages(ctx, owner, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.chat.FinishTurn(ctx, assistant.ID, false); err != nil {
		t.Fatal(err)
	}
	return turn, chat.ToolCommand{OwnerID: owner, SessionID: sessionID, AssistantMessageID: assistant.ID,
		Call: agents.ToolCall{ID: "call-board", Name: agenttools.ToolViewCanvas}, History: history, Options: turn.Options}
}

// #709 end to end: a board saved through the canvas service is frozen with
// the turn by the board Port and the real attachment resolver, view_canvas
// reads the snapshot, and its @canvasN handle resolves to the verified
// platform picture; a later layer is the only one added after the last turn.
func TestBoardSnapshotFreezesVerifiesAndResolvesThroughSQLite709(t *testing.T) {
	fixture := newBoardFixture709(t)
	ctx := context.Background()
	board := fixture.application.Chat.CanvasLayers
	session, err := fixture.chat.CreateSession(ctx, "owner-a", "Owner A", "board")
	if err != nil {
		t.Fatal(err)
	}
	project, _, err := fixture.canvas.GetOrCreateProjectForSession(ctx, "owner-a", session.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	mine, foreign := fixture.upload(t, "owner-a"), fixture.upload(t, "owner-b")
	frame := &canvas.Frame{Width: 1024, Height: 1024, CoordinateVersion: fixture.application.Canvas.Defaults.CoordinateVersion}
	fixture.save(t, "owner-a", project.ID, frame,
		node709{id: "sketch", kind: "image", z: 2, data: map[string]any{"imageUrl": mine, "name": "sketch", "rotation": 15.0}},
		node709{id: "note", kind: "text", z: 3, data: map[string]any{"text": "披风改成红色"}},
		node709{id: "stolen", kind: "image", z: 1, data: map[string]any{"imageUrl": foreign}},
	)

	fixture.now = fixture.now.Add(time.Minute)
	turn, command := fixture.turn(t, "owner-a", session.ID, "开始干活")
	frozen := turn.Options.CanvasContext
	if frozen == nil || frozen.ProjectID != project.ID || frozen.Total != 3 || frozen.OffConversation != 3 {
		t.Fatalf("frozen board = %+v", frozen)
	}
	layers := map[string]chat.CanvasLayerContext{}
	for _, layer := range frozen.Layers {
		layers[layer.ID] = layer
	}
	sketch := layers["sketch"]
	if sketch.Handle != board.HandlePrefix+"1" || sketch.Picture == nil || sketch.Picture.URL != mine ||
		!sketch.AddedAfterLastTurn || sketch.Transformed == nil || !*sketch.Transformed ||
		sketch.InFrame == nil || !*sketch.InFrame {
		t.Fatalf("own picture layer = %+v", sketch)
	}
	if stolen := layers["stolen"]; !stolen.Unreadable || stolen.Handle != "" || stolen.Picture != nil {
		t.Fatalf("another owner's picture stays unreadable: %+v", stolen)
	}

	executor, err := agenttools.New(agenttools.Config{Params: agentparams.NewStore(), Generate: noGenerate709{},
		Context: fixture.application.Chat.Context, CanvasLayers: board,
		HistoryLookup: chat.NewHistoryLookupPolicy(fixture.application.Chat)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(ctx, command, nil)
	if err != nil || result.Error != "" {
		t.Fatalf("view_canvas = %+v %v", result, err)
	}
	if !strings.Contains(result.AgentContent, `"handle":"`+sketch.Handle+`"`) ||
		strings.Contains(result.AgentContent, mine) || !strings.Contains(result.AgentContent, `"preview":null`) {
		t.Fatalf("view_canvas listing = %s", result.AgentContent)
	}
	resolved, matched, err := chat.ResolveCanvasHandle(board, turn.Options, sketch.Handle)
	if err != nil || !matched || resolved.URL != mine {
		t.Fatalf("handle resolves to %+v matched=%v err=%v", resolved, matched, err)
	}

	// A second picture put on the board after the last message is the only
	// layer added since.
	fixture.now = fixture.now.Add(time.Minute)
	// The fresh layer lies far outside the canvas frame.
	fixture.save(t, "owner-a", project.ID, frame,
		node709{id: "sketch", kind: "image", z: 2, data: map[string]any{"imageUrl": mine, "name": "sketch"}},
		node709{id: "note", kind: "text", z: 3, data: map[string]any{"text": "披风改成红色"}},
		node709{id: "fresh", kind: "image", z: 4, x: 5000, data: map[string]any{"imageUrl": fixture.upload(t, "owner-a")}},
	)
	fixture.now = fixture.now.Add(time.Minute)
	turn, _ = fixture.turn(t, "owner-a", session.ID, "继续")
	for _, layer := range turn.Options.CanvasContext.Layers {
		if want := layer.ID == "fresh"; layer.AddedAfterLastTurn != want {
			t.Fatalf("layer %s added after the last turn = %v", layer.ID, layer.AddedAfterLastTurn)
		}
		if layer.ID == "fresh" && (layer.InFrame == nil || *layer.InFrame) {
			t.Fatalf("a layer far outside the frame: %+v", layer)
		}
		if layer.ID == "sketch" && layer.Transformed != nil {
			t.Fatalf("no rotation, opacity or crop saved: %+v", layer)
		}
	}
}
