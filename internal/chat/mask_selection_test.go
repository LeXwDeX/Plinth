package chat_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// maskVerifier reads the pixel facts of an owned mask, like the production
// reference resolver.
type maskVerifier struct {
	measuringVerifier
	masks map[string]chat.SelectionMask
	fails map[string]bool
}

func (v *maskVerifier) MeasureMask(_ context.Context, owner, assetID string) (chat.SelectionMask, error) {
	if owner != "owner-a" || v.fails[assetID] {
		return chat.SelectionMask{}, errors.New("unusable mask")
	}
	mask, found := v.masks[assetID]
	if !found {
		return chat.SelectionMask{}, errors.New("unknown mask")
	}
	return mask, nil
}

func maskTestService(t *testing.T) (*chat.Service, *memoryChatRepository, *maskVerifier, catalog.ChatSchema) {
	t.Helper()
	policy, schema := handlePolicyForTest(t)
	verifier := &maskVerifier{
		measuringVerifier: measuringVerifier{
			recordingVerifier: recordingVerifier{held: map[string]map[string]bool{}},
			sizes:             map[string][2]int{"snapshot": {2048, 1536}},
		},
		masks: map[string]chat.SelectionMask{
			"mask-good":  {Width: 2048, Height: 1536, BBox: [4]int{100, 200, 700, 900}, Coverage: 0.25},
			"mask-small": {Width: 1024, Height: 1024, BBox: [4]int{0, 0, 9, 9}, Coverage: 0.1},
			"mask-empty": {Width: 2048, Height: 1536},
		},
		fails: map[string]bool{"mask-unreadable": true},
	}
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Handles = policy
		config.Attachments = verifier
		config.CanvasInputSource = schema.Context.CanvasInputSource
	})
	return service, repository, verifier, schema
}

// #244: the mask is frozen with the turn after the service measured it, and
// the target snapshot stays the canvas snapshot.
func TestSelectionMaskIsFrozenWithTheCanvasTurn(t *testing.T) {
	service, _, verifier, schema := maskTestService(t)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	verifier.held["owner-a/"+session.ID] = map[string]bool{"snapshot": true}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID: "owner-a", SessionID: session.ID, Content: "把这里改成蓝色",
		Options: chat.TurnOptions{InputSource: schema.Context.CanvasInputSource},
		Selection: []chat.AttachmentRef{{Kind: chat.AttachmentKindImage, AssetID: "snapshot",
			URL: "/api/v1/input-assets/snapshot"}},
		SelectionMask: &chat.SelectionMaskRef{MaskAssetID: "mask-good"},
	})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := &chat.SelectionMask{MaskAssetID: "mask-good", Width: 2048, Height: 1536,
		BBox: [4]int{100, 200, 700, 900}, Coverage: 0.25}
	if !reflect.DeepEqual(turn.Options.SelectionMask, want) {
		t.Fatalf("frozen mask = %+v, want %+v", turn.Options.SelectionMask, want)
	}
	if len(turn.Options.Selection) != 1 || turn.Options.Selection[0].Role != chat.AttachmentRoleCanvas {
		t.Fatalf("selection = %+v", turn.Options.Selection)
	}
}

// #244: a mask that does not hold refuses the turn instead of being dropped
// like an attachment claim. No message or turn is stored.
func TestSelectionMaskFailuresRefuseTheTurn(t *testing.T) {
	service, repository, verifier, schema := maskTestService(t)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	verifier.held["owner-a/"+session.ID] = map[string]bool{"snapshot": true}
	canvas := chat.TurnOptions{InputSource: schema.Context.CanvasInputSource}
	selection := []chat.AttachmentRef{{Kind: chat.AttachmentKindImage, AssetID: "snapshot",
		URL: "/api/v1/input-assets/snapshot"}}
	start := func(options chat.TurnOptions, selection []chat.AttachmentRef, assetID string) error {
		_, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID,
			Content: "改这里", Options: options, Selection: selection,
			SelectionMask: &chat.SelectionMaskRef{MaskAssetID: assetID}})
		return err
	}
	for name, test := range map[string]struct {
		options   chat.TurnOptions
		selection []chat.AttachmentRef
		assetID   string
		want      error
	}{
		"no canvas source": {chat.TurnOptions{}, selection, "mask-good", chat.ErrSelectionMaskWithoutCanvas},
		"no snapshot":      {canvas, nil, "mask-good", chat.ErrCanvasSourceMissing},
		"size mismatch":    {canvas, selection, "mask-small", chat.ErrSelectionMaskSizeMismatch},
		"unreadable":       {canvas, selection, "mask-unreadable", chat.ErrSelectionMaskInvalid},
		"unknown":          {canvas, selection, "mask-unknown", chat.ErrSelectionMaskInvalid},
		"nothing selected": {canvas, selection, "mask-empty", chat.ErrSelectionMaskInvalid},
		"empty asset id":   {canvas, selection, "", chat.ErrSelectionMaskInvalid},
		"mask without snapshot": {canvas, []chat.AttachmentRef{{Kind: chat.AttachmentKindImage, AssetID: "unheld",
			URL: "/api/v1/input-assets/unheld"}}, "mask-good", chat.ErrCanvasSourceMissing},
	} {
		if err := start(test.options, test.selection, test.assetID); !errors.Is(err, test.want) {
			t.Fatalf("%s: err = %v, want %v", name, err, test.want)
		}
		repository.mu.Lock()
		stored := len(repository.messages[session.ID])
		repository.mu.Unlock()
		if stored != 0 {
			t.Fatalf("%s: refused turn stored %d messages", name, stored)
		}
	}
}

// #244: without a mask the turn is unchanged.
func TestTurnWithoutSelectionMaskIsUnchanged(t *testing.T) {
	service, _, verifier, schema := maskTestService(t)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	verifier.held["owner-a/"+session.ID] = map[string]bool{"snapshot": true}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID,
		Content: "按画布改", Options: chat.TurnOptions{InputSource: schema.Context.CanvasInputSource},
		Selection: []chat.AttachmentRef{{Kind: chat.AttachmentKindImage, AssetID: "snapshot",
			URL: "/api/v1/input-assets/snapshot"}}})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Options.SelectionMask != nil {
		t.Fatalf("mask = %+v", turn.Options.SelectionMask)
	}
}
