package chat_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// measuringVerifier is the recording verifier that also reads the pixel size
// of an owned input asset, like the production reference resolver.
type measuringVerifier struct {
	recordingVerifier
	sizes    map[string][2]int
	measured []string
}

func (v *measuringVerifier) ImageDimensions(_ context.Context, owner, assetID string) (int, int, error) {
	v.measured = append(v.measured, owner+"/"+assetID)
	size, found := v.sizes[assetID]
	if !found {
		return 0, 0, errors.New("unreadable")
	}
	return size[0], size[1], nil
}

// #272: the artboard snapshot of a canvas turn is not a plain upload. The
// service gives it the canvas role and its layout: the pixel size it measured
// from the uploaded picture, and the layer count the client claimed when it is
// within the Schema bound. Nothing about it is taken on the client's word
// alone, and outside a canvas turn the same picture stays an upload.
func TestCanvasSnapshotGetsTheCanvasRoleAndItsMeasuredLayout(t *testing.T) {
	policy, schema := handlePolicyForTest(t)
	verifier := &measuringVerifier{recordingVerifier: recordingVerifier{held: map[string]map[string]bool{}},
		sizes: map[string][2]int{"snapshot": {2048, 1536}, "generated-asset": {1024, 1024}, "plain": {512, 512}}}
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Handles = policy
		config.Attachments = verifier
		config.CanvasInputSource = schema.Context.CanvasInputSource
	})
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	verifier.held["owner-a/"+session.ID] = map[string]bool{"snapshot": true, "unmeasured": true,
		"generated-task": true, "generated-asset": true, "plain": true}
	canvas := chat.TurnOptions{InputSource: schema.Context.CanvasInputSource}
	snapshot := func(asset string, layers int) []chat.AttachmentRef {
		return []chat.AttachmentRef{{Kind: chat.AttachmentKindImage, AssetID: asset,
			URL: "/api/v1/input-assets/" + asset, CanvasLayers: layers}}
	}
	start := func(options chat.TurnOptions, selection []chat.AttachmentRef) (chat.Turn, chat.Message) {
		t.Helper()
		assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID,
			Content: "按摆放合并", Options: options, Selection: selection})
		if err != nil {
			t.Fatal(err)
		}
		turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
		if err != nil {
			t.Fatal(err)
		}
		repository.mu.Lock()
		messages := repository.messages[session.ID]
		repository.mu.Unlock()
		return turn, messages[len(messages)-2]
	}

	turn, user := start(canvas, snapshot("snapshot", 2))
	want := &chat.CanvasLayout{Width: 2048, Height: 1536, Layers: 2}
	if len(turn.Options.Selection) != 1 || turn.Options.Selection[0].Role != chat.AttachmentRoleCanvas ||
		!reflect.DeepEqual(turn.Options.Selection[0].Canvas, want) {
		t.Fatalf("frozen selection = %+v", turn.Options.Selection)
	}
	if len(user.Attachments) != 1 || user.Attachments[0].Role != chat.AttachmentRoleCanvas ||
		!reflect.DeepEqual(user.Attachments[0].Canvas, want) || user.Attachments[0].Handle == "" {
		t.Fatalf("user attachments = %+v", user.Attachments)
	}
	if !reflect.DeepEqual(verifier.measured, []string{"owner-a/snapshot"}) {
		t.Fatalf("measured = %v", verifier.measured)
	}

	// A layer count outside the Schema bound is not kept; the role and the
	// measured size are.
	for _, layers := range []int{0, -3, policy.MaximumCanvasLayers + 1} {
		turn, _ = start(canvas, snapshot("snapshot", layers))
		if got := turn.Options.Selection[0]; got.Role != chat.AttachmentRoleCanvas ||
			!reflect.DeepEqual(got.Canvas, &chat.CanvasLayout{Width: 2048, Height: 1536}) {
			t.Fatalf("layers %d: selection = %+v", layers, got)
		}
	}

	// Outside a canvas turn the same picture is a plain upload, unmeasured.
	verifier.measured = nil
	turn, _ = start(chat.TurnOptions{}, snapshot("plain", 2))
	if got := turn.Options.Selection[0]; got.Role != chat.AttachmentRoleUpload || got.Canvas != nil || len(verifier.measured) != 0 {
		t.Fatalf("plain selection = %+v measured %v", got, verifier.measured)
	}

	// A picture whose size cannot be read stays an upload; the turn still runs
	// from it as #266 requires.
	turn, _ = start(canvas, snapshot("unmeasured", 2))
	if got := turn.Options.Selection[0]; got.Role != chat.AttachmentRoleUpload || got.Canvas != nil {
		t.Fatalf("unmeasured snapshot = %+v", got)
	}

	// A generated picture is never relabelled as the artboard.
	generated := []chat.AttachmentRef{{Kind: chat.AttachmentKindImage, TaskID: "generated-task", AssetID: "generated-asset",
		URL: "/api/v1/tasks/generated-task/assets/generated-asset", CanvasLayers: 2}}
	turn, _ = start(canvas, generated)
	if got := turn.Options.Selection[0]; got.Role != chat.AttachmentRoleGenerated || got.Canvas != nil {
		t.Fatalf("generated selection = %+v", got)
	}
}

// layerVerifier also verifies picture URLs, like the production resolver
// does for a Task asset route: a URL verifies only for the owner and session
// that hold its Task.
type layerVerifier struct {
	measuringVerifier
	// layers maps "owner/session" to the picture URLs that scope holds and
	// the Task each comes from ("" for an owned upload).
	layers map[string]map[string]string
}

func (v *layerVerifier) VerifyAttachment(ctx context.Context, owner, session string, ref chat.AttachmentRef) (chat.Attachment, error) {
	if ref.AssetID == "" && ref.TaskID == "" && ref.CaptureID == "" && ref.URL != "" {
		v.calls = append(v.calls, ref)
		task, held := v.layers[owner+"/"+session][ref.URL]
		if !held {
			return chat.Attachment{}, chat.ErrAttachmentRejected
		}
		role := chat.AttachmentRoleGenerated
		if task == "" {
			role = chat.AttachmentRoleUpload
		}
		return chat.Attachment{Kind: ref.Kind, Role: role, TaskID: task, URL: ref.URL}, nil
	}
	return v.measuringVerifier.VerifyAttachment(ctx, owner, session, ref)
}

// #189: the snapshot names the Tasks of the picture layers it flattens, so a
// tool that takes a source Task (the Creative Lab build) can resolve it back
// to the preview it shows. Each layer URL is verified for the owner and the
// session; uploads, other sessions' pictures and unknown URLs are left out,
// and outside a canvas turn nothing is read.
func TestCanvasSnapshotNamesTheVerifiedTasksOfItsLayers(t *testing.T) {
	policy, schema := handlePolicyForTest(t)
	verifier := &layerVerifier{measuringVerifier: measuringVerifier{
		recordingVerifier: recordingVerifier{held: map[string]map[string]bool{}},
		sizes:             map[string][2]int{"snapshot": {1024, 1024}}}}
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Handles = policy
		config.Attachments = verifier
		config.CanvasInputSource = schema.Context.CanvasInputSource
	})
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	verifier.held["owner-a/"+session.ID] = map[string]bool{"snapshot": true, "snapshot-plain": true}
	verifier.layers = map[string]map[string]string{
		"owner-a/" + session.ID: {
			"/api/v1/tasks/task-preview/assets/a1": "task-preview",
			"/api/v1/tasks/task-preview/assets/a2": "task-preview",
			"/api/v1/input-assets/photo":           "",
		},
		"owner-a/other-session": {"/api/v1/tasks/task-elsewhere/assets/a1": "task-elsewhere"},
	}
	claims := []string{
		"/api/v1/input-assets/photo",
		"/api/v1/tasks/task-preview/assets/a1",
		"/api/v1/tasks/task-preview/assets/a2",
		"/api/v1/tasks/task-elsewhere/assets/a1",
		"/api/v1/tasks/task-unknown/assets/a1",
	}
	start := func(options chat.TurnOptions, asset string) chat.Turn {
		t.Helper()
		assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID,
			Content: "做成成品", Options: options, Selection: []chat.AttachmentRef{{Kind: chat.AttachmentKindImage,
				AssetID: asset, URL: "/api/v1/input-assets/" + asset, CanvasLayers: 3, CanvasSources: claims}}})
		if err != nil {
			t.Fatal(err)
		}
		turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
		if err != nil {
			t.Fatal(err)
		}
		return turn
	}
	turn := start(chat.TurnOptions{InputSource: schema.Context.CanvasInputSource}, "snapshot")
	if got := turn.Options.Selection[0].Canvas; got == nil || !reflect.DeepEqual(got.Sources, []string{"task-preview"}) {
		t.Fatalf("canvas = %+v", got)
	}
	verifier.calls = nil
	turn = start(chat.TurnOptions{}, "snapshot-plain")
	if got := turn.Options.Selection[0]; got.Canvas != nil {
		t.Fatalf("plain selection = %+v", got)
	}
	for _, call := range verifier.calls {
		if call.AssetID == "" && call.URL != "" {
			t.Fatalf("layer read outside a canvas turn: %+v", call)
		}
	}
}
