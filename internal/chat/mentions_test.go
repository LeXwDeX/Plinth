package chat_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

func startMentionTurn(t *testing.T, held map[string]bool, turn chat.StartTurn) (string, []chat.Attachment, []string) {
	t.Helper()
	verifier := &recordingVerifier{held: map[string]map[string]bool{}}
	service, _, _ := newHandleService(t, verifier)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	verifier.held["owner-a/"+session.ID] = held
	turn.OwnerID, turn.SessionID = "owner-a", session.ID
	var dropped []string
	turn.OnMentionsDropped = func(tokens []string) { dropped = append(dropped, tokens...) }
	if _, err := service.StartTurn(ctx, turn); err != nil {
		t.Fatal(err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	return messages[0].Content, messages[0].Attachments, dropped
}

func TestMentionsAreRewrittenToHandlesInTheServersOrder(t *testing.T) {
	held := map[string]bool{"task-1": true, "asset-1": true, "task-2": true, "asset-2": true}
	content, attachments, dropped := startMentionTurn(t, held, chat.StartTurn{
		Content:   "style of @版本1 and @图层2, keep @上传的图2 and @版本1 again",
		ImageURLs: []string{"data:image/png;base64,AA==", "data:image/png;base64,QQ=="},
		Mentions: []chat.MentionRef{
			{Token: "@版本1", Ref: chat.AttachmentRef{Kind: chat.AttachmentKindImage, TaskID: "task-1", AssetID: "asset-1"}},
			{Token: "@图层2", Ref: chat.AttachmentRef{Kind: chat.AttachmentKindImage, TaskID: "task-2", AssetID: "asset-2"}},
			{Token: "@上传的图2", Ref: chat.AttachmentRef{ImageSlot: 2}},
			// A repeated token is one mention.
			{Token: "@版本1", Ref: chat.AttachmentRef{Kind: chat.AttachmentKindImage, TaskID: "task-2", AssetID: "asset-2"}},
		},
	})
	// Message images take @img1 and @img2; the mentioned pictures follow in the order of the mentions.
	want := "style of @img3 and @img4, keep @img2 and @img3 again"
	if content != want {
		t.Fatalf("content = %q; want %q", content, want)
	}
	if len(attachments) != 4 || attachments[2].TaskID != "task-1" || attachments[3].TaskID != "task-2" {
		t.Fatalf("attachments = %+v", attachments)
	}
	if len(dropped) != 0 {
		t.Fatalf("dropped = %v", dropped)
	}
}

func TestMentionOfAForeignObjectStaysPlainText(t *testing.T) {
	content, attachments, dropped := startMentionTurn(t, map[string]bool{"task-1": true, "asset-1": true}, chat.StartTurn{
		Content: "use @他人的图 and @版本1",
		Mentions: []chat.MentionRef{
			{Token: "@他人的图", Ref: chat.AttachmentRef{Kind: chat.AttachmentKindImage, TaskID: "foreign", AssetID: "foreign-asset"}},
			{Token: "@版本1", Ref: chat.AttachmentRef{Kind: chat.AttachmentKindImage, TaskID: "task-1", AssetID: "asset-1"}},
		},
	})
	if content != "use @他人的图 and @img1" {
		t.Fatalf("content = %q", content)
	}
	if !reflect.DeepEqual(dropped, []string{"@他人的图"}) {
		t.Fatalf("dropped = %v", dropped)
	}
	for _, attachment := range attachments {
		if attachment.TaskID == "foreign" || attachment.AssetID == "foreign-asset" {
			t.Fatalf("foreign ID persisted: %+v", attachment)
		}
	}
}

func TestMentionNotWrittenInTheTextOrInsideAWordIsDropped(t *testing.T) {
	held := map[string]bool{"task-1": true, "asset-1": true}
	ref := chat.AttachmentRef{Kind: chat.AttachmentKindImage, TaskID: "task-1", AssetID: "asset-1"}
	content, attachments, dropped := startMentionTurn(t, held, chat.StartTurn{
		Content:  "see @版本10 and a@版本1",
		Mentions: []chat.MentionRef{{Token: "@版本1", Ref: ref}, {Token: "@没写", Ref: ref}},
	})
	if content != "see @版本10 and a@版本1" || len(attachments) != 0 {
		t.Fatalf("content = %q, attachments = %+v", content, attachments)
	}
	if len(dropped) != 2 {
		t.Fatalf("dropped = %v", dropped)
	}
}

func TestMentionOfAHistoryHandlePassesThroughUnchanged(t *testing.T) {
	verifier := &recordingVerifier{held: map[string]map[string]bool{}}
	service, _, _ := newHandleService(t, verifier)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	verifier.held["owner-a/"+session.ID] = map[string]bool{"task-1": true, "asset-1": true}
	first, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "one",
		ImageURLs:   []string{"/api/v1/tasks/task-1/assets/asset-1"},
		Attachments: []chat.AttachmentRef{{ImageSlot: 1, TaskID: "task-1", AssetID: "asset-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.FinishTurn(ctx, first.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := service.FinishStream(ctx, session.ID, first.ID, false); err != nil {
		t.Fatal(err)
	}
	// The same object by its task and asset is the session's own @img1, not a new picture.
	if _, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "change @img1 and @版本1",
		Mentions: []chat.MentionRef{
			{Token: "@img1"},
			{Token: "@版本1", Ref: chat.AttachmentRef{Kind: chat.AttachmentKindImage, TaskID: "task-1", AssetID: "asset-1"}},
		}}); err != nil {
		t.Fatal(err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	var second chat.Message
	for _, message := range messages {
		if strings.HasPrefix(message.Content, "change") {
			second = message
		}
	}
	if second.Content != "change @img1 and @img1" || len(second.Attachments) != 0 {
		t.Fatalf("content = %q, attachments = %+v", second.Content, second.Attachments)
	}
}

func TestMentionMayBeFollowedByCJKButNotByAnASCIIWordCharacter(t *testing.T) {
	held := map[string]bool{"task-1": true, "asset-1": true}
	ref := chat.AttachmentRef{Kind: chat.AttachmentKindImage, TaskID: "task-1", AssetID: "asset-1"}
	content, attachments, dropped := startMentionTurn(t, held, chat.StartTurn{
		Content:  "@图层2改成红色",
		Mentions: []chat.MentionRef{{Token: "@图层2", Ref: ref}},
	})
	if content != "@img1改成红色" || len(attachments) != 1 || len(dropped) != 0 {
		t.Fatalf("content = %q, attachments = %+v, dropped = %v", content, attachments, dropped)
	}
	content, attachments, dropped = startMentionTurn(t, held, chat.StartTurn{
		Content:  "@img1x",
		Mentions: []chat.MentionRef{{Token: "@img1", Ref: ref}},
	})
	if content != "@img1x" || len(attachments) != 0 || len(dropped) != 1 {
		t.Fatalf("content = %q, attachments = %+v, dropped = %v", content, attachments, dropped)
	}
}

func TestTwoMessageImagesOfOneNameGetTwoHandlesAndAStaleSlotStaysPlainText(t *testing.T) {
	held := map[string]bool{"task-1": true, "asset-1": true}
	content, attachments, dropped := startMentionTurn(t, held, chat.StartTurn{
		Content: "mix @photo.png with @photo.png_2, drop @gone and keep @版本1",
		// Two identical pictures: the slots, not the bytes, tell them apart.
		ImageURLs: []string{"data:image/png;base64,AA==", "data:image/png;base64,AA=="},
		Mentions: []chat.MentionRef{
			{Token: "@photo.png", Ref: chat.AttachmentRef{ImageSlot: 1}},
			{Token: "@photo.png_2", Ref: chat.AttachmentRef{ImageSlot: 2}},
			// The attachment this named was removed before sending: the slot no longer exists.
			{Token: "@gone", Ref: chat.AttachmentRef{ImageSlot: 3}},
			{Token: "@版本1", Ref: chat.AttachmentRef{Kind: chat.AttachmentKindImage, TaskID: "task-1", AssetID: "asset-1"}},
		},
	})
	if want := "mix @img1 with @img2, drop @gone and keep @img3"; content != want {
		t.Fatalf("content = %q; want %q", content, want)
	}
	if len(attachments) != 3 || attachments[2].TaskID != "task-1" {
		t.Fatalf("attachments = %+v", attachments)
	}
	if !reflect.DeepEqual(dropped, []string{"@gone"}) {
		t.Fatalf("dropped = %v", dropped)
	}
}
