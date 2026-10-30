package chat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// canvasDimensionVerifier measures every owned input asset, so the snapshot
// of a canvas turn gets its canvas role and layout (#272).
type canvasDimensionVerifier struct{ recordingVerifier }

func (v *canvasDimensionVerifier) ImageDimensions(context.Context, string, string) (int, int, error) {
	return 2048, 1152, nil
}

// canvasLabelMarker appears once in the Schema's image.canvas label.
const canvasLabelMarker = "用户发送这一轮时的画板版面"

type canvasVisionRig struct {
	t        *testing.T
	service  *chat.Service
	verifier *canvasDimensionVerifier
	schema   catalog.ChatSchema
	agent    *scriptedAgent
	inlined  []string
	inline   func(url string) (string, bool)
	session  chat.Session
	ctx      context.Context
	orch     *chat.Orchestrator
}

func newCanvasVisionRig(t *testing.T, mutate func(*catalog.ChatSchema)) *canvasVisionRig {
	t.Helper()
	rig := &canvasVisionRig{t: t, ctx: context.Background(),
		verifier: &canvasDimensionVerifier{recordingVerifier{held: map[string]map[string]bool{}}}}
	policy, schema := handlePolicyForTest(t)
	schema.Agent.Vision = true
	schema.Stream.EventFields.Type = "configured-type"
	schema.Stream.EventFields.Content = "configured-content"
	schema.Stream.EventTypes.Text = "configured-text"
	schema.Stream.EventTypes.Done = "configured-done"
	schema.Roles.System, schema.Roles.User, schema.Roles.Assistant, schema.Roles.Tool =
		"configured-system", "configured-user", "configured-assistant", "configured-tool"
	schema.State.PendingContent = "configured-pending"
	schema.Context.CanvasImageDetail = "configured-canvas-detail"
	if mutate != nil {
		mutate(&schema)
	}
	rig.schema = schema
	rig.inline = func(string) (string, bool) { return "data:image/jpeg;base64,SNAP", true }
	service := newChatServiceWith(t, newMemoryChatRepository(), func(c *chat.ServiceConfig) {
		c.Handles = policy
		c.Attachments = rig.verifier
		c.CanvasInputSource = schema.Context.CanvasInputSource
	})
	rig.service = service
	rig.agent = &scriptedAgent{responses: []agents.Completion{{Content: "ok"}, {Content: "ok"}, {Content: "ok"}}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "m", Agent: rig.agent, Tools: noToolExecutor{},
		VisionInline: func(_ context.Context, _ generation.OwnerID, url string) (string, bool) {
			rig.inlined = append(rig.inlined, url)
			return rig.inline(url)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rig.orch = orchestrator
	session, err := service.CreateSession(rig.ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	rig.session = session
	rig.verifier.held["owner-a/"+session.ID] = map[string]bool{"asset-snap": true}
	return rig
}

// turn runs one turn; a canvas turn carries the snapshot as its selection only.
func (rig *canvasVisionRig) turn(content string, canvas bool, imageURLs ...string) {
	rig.t.Helper()
	options := chat.TurnOptions{CreativeDomain: "image"}
	var selection []chat.AttachmentRef
	if canvas {
		options.InputSource = rig.schema.Context.CanvasInputSource
		selection = []chat.AttachmentRef{{Kind: chat.AttachmentKindImage, AssetID: "asset-snap",
			URL: "/api/v1/assets/asset-snap", CanvasLayers: 2}}
	}
	assistant, err := rig.service.StartTurn(rig.ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: rig.session.ID,
		Content: content, Options: options, Selection: selection, ImageURLs: imageURLs})
	if err != nil {
		rig.t.Fatal(err)
	}
	if err := rig.orch.Process(rig.ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: rig.session.ID,
		AssistantMessageID: assistant.ID, Options: options}); err != nil {
		rig.t.Fatal(err)
	}
}

// lastUser is the newest user message of the newest agent request.
func (rig *canvasVisionRig) lastUser() agents.Message {
	rig.t.Helper()
	if len(rig.agent.requests) == 0 {
		rig.t.Fatal("no agent request")
	}
	messages := rig.agent.requests[len(rig.agent.requests)-1].Messages
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == rig.schema.Roles.User {
			return messages[index]
		}
	}
	rig.t.Fatal("no user message in request")
	return agents.Message{}
}

func (rig *canvasVisionRig) imageParts(message agents.Message) []agents.ContentPart {
	var result []agents.ContentPart
	for _, part := range message.Parts {
		if part.Kind == rig.schema.Context.ImagePartKind {
			result = append(result, part)
		}
	}
	return result
}

func (rig *canvasVisionRig) text(message agents.Message) string {
	var builder strings.Builder
	builder.WriteString(message.Content)
	for _, part := range message.Parts {
		if part.Kind == rig.schema.Context.TextPartKind {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

// #16: the artboard snapshot travels as the selection of a canvas turn, never
// in message.image_urls, and used to reach the model as a text label only.
func TestCanvasTurnShowsTheSnapshotToTheAgentAtTheCanvasDetail(t *testing.T) {
	rig := newCanvasVisionRig(t, nil)
	rig.turn("@画布 改成英文？", true)

	user := rig.lastUser()
	images := rig.imageParts(user)
	if len(images) != 1 || images[0].URL != "data:image/jpeg;base64,SNAP" ||
		images[0].Detail != "configured-canvas-detail" {
		t.Fatalf("image parts = %+v", images)
	}
	if len(rig.inlined) != 1 || rig.inlined[0] != "/api/v1/assets/asset-snap" {
		t.Fatalf("inlined = %v", rig.inlined)
	}
	// The image part is followed by the canvas label, which appears once.
	for index, part := range user.Parts {
		if part.Kind != rig.schema.Context.ImagePartKind {
			continue
		}
		next := user.Parts[index+1]
		if next.Kind != rig.schema.Context.TextPartKind || !strings.Contains(next.Text, "@img1") ||
			!strings.Contains(next.Text, "2048") {
			t.Fatalf("part after the snapshot = %+v", next)
		}
	}
	if strings.Count(rig.text(user), canvasLabelMarker) != 1 {
		t.Fatalf("canvas label must appear once: %q", rig.text(user))
	}
	if !strings.Contains(rig.text(user), "@画布 改成英文？") {
		t.Fatalf("user text lost: %q", rig.text(user))
	}
}

// The snapshot is not an upload: it must not change the upload hint or the
// history image indices.
func TestCanvasSnapshotDoesNotJoinTheUploadedImages(t *testing.T) {
	rig := newCanvasVisionRig(t, nil)
	rig.turn("@画布 改成英文？", true)
	if strings.Contains(rig.text(rig.lastUser()), "本轮用户上传了") {
		t.Fatalf("snapshot counted as an upload: %q", rig.text(rig.lastUser()))
	}
}

// An oversize snapshot is reduced by the inliner itself (the #292 derivative);
// the turn only asks for the data URI.
func TestCanvasSnapshotGoesThroughTheInliner(t *testing.T) {
	rig := newCanvasVisionRig(t, nil)
	rig.inline = func(string) (string, bool) { return "data:image/jpeg;base64,DERIVATIVE", true }
	rig.turn("@画布 合并", true)
	images := rig.imageParts(rig.lastUser())
	if len(images) != 1 || images[0].URL != "data:image/jpeg;base64,DERIVATIVE" || len(rig.inlined) != 1 {
		t.Fatalf("images = %+v inlined = %v", images, rig.inlined)
	}
}

func TestCanvasSnapshotStaysTextOnlyWhenVisionIsOff(t *testing.T) {
	rig := newCanvasVisionRig(t, func(schema *catalog.ChatSchema) { schema.Agent.Vision = false })
	rig.turn("@画布 改成英文？", true)
	user := rig.lastUser()
	if len(rig.imageParts(user)) != 0 || len(rig.inlined) != 0 {
		t.Fatalf("vision off rendered a picture: %+v inlined %v", user.Parts, rig.inlined)
	}
	if !strings.Contains(rig.text(user), canvasLabelMarker) {
		t.Fatalf("canvas label missing: %q", rig.text(user))
	}
}

func TestCanvasSnapshotStaysTextOnlyWhenInliningFails(t *testing.T) {
	rig := newCanvasVisionRig(t, nil)
	rig.inline = func(string) (string, bool) { return "", false }
	rig.turn("@画布 改成英文？", true)
	user := rig.lastUser()
	if len(rig.imageParts(user)) != 0 || len(rig.inlined) != 1 {
		t.Fatalf("failed inlining rendered a picture: %+v inlined %v", user.Parts, rig.inlined)
	}
	if strings.Count(rig.text(user), canvasLabelMarker) != 1 {
		t.Fatalf("canvas label must stay once: %q", rig.text(user))
	}
}

// The snapshot is the primary input of its turn: it takes a place in the
// vision image limit before the history pictures do.
func TestCanvasSnapshotCountsAgainstTheVisionLimitAndComesFirst(t *testing.T) {
	rig := newCanvasVisionRig(t, func(schema *catalog.ChatSchema) {
		schema.Context.MaximumVisionImages = 2
		schema.Context.RecentImageLimit = 2
	})
	rig.turn("看图", false, "data:image/png;base64,AA", "data:image/png;base64,BB")
	rig.turn("@画布 改成英文？", true)

	var all []agents.ContentPart
	for _, message := range rig.agent.requests[len(rig.agent.requests)-1].Messages {
		all = append(all, rig.imageParts(message)...)
	}
	if len(all) != 2 {
		t.Fatalf("image parts in the request = %d (limit 2): %+v", len(all), all)
	}
	snapshots := rig.imageParts(rig.lastUser())
	if len(snapshots) != 1 || snapshots[0].Detail != "configured-canvas-detail" {
		t.Fatalf("snapshot dropped by the limit: %+v", snapshots)
	}
}

// Only the current turn's snapshot is shown; an older one stays a label.
func TestOlderCanvasSnapshotsAreNotShownAgain(t *testing.T) {
	rig := newCanvasVisionRig(t, nil)
	rig.turn("@画布 第一轮", true)
	rig.turn("没有画板的下一轮", false)
	messages := rig.agent.requests[len(rig.agent.requests)-1].Messages
	for _, message := range messages {
		if len(rig.imageParts(message)) != 0 {
			t.Fatalf("older snapshot shown again: %+v", message.Parts)
		}
	}
	if len(rig.inlined) != 1 {
		t.Fatalf("inlined = %v; only the first turn's snapshot should be fetched", rig.inlined)
	}
}

// A turn that is not a canvas turn keeps today's request.
func TestNonCanvasTurnHasNoSnapshotPart(t *testing.T) {
	rig := newCanvasVisionRig(t, nil)
	rig.turn("你好", false)
	if len(rig.imageParts(rig.lastUser())) != 0 || len(rig.inlined) != 0 {
		t.Fatalf("plain turn rendered a picture: %+v", rig.lastUser().Parts)
	}
}
