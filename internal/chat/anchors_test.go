package chat_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func anchorSchema() catalog.ChatVisualAnchorsSchema {
	return catalog.ChatVisualAnchorsSchema{
		MaximumAnchors: 2, MaximumReferences: 2,
		MaximumNameCharacters: 12, MaximumDescriptionCharacters: 20,
		Kinds: []catalog.ChatVisualAnchorKindSchema{
			{ID: "identity", Label: "身份锚", ReferencePurposeTemplate: "保留「{name}」"},
			{ID: "style", Label: "风格锚", ReferencePurposeTemplate: "沿用「{name}」"},
		},
		InvalidTemplate: "视觉锚无效：{reason}",
		SavedMessage:    "saved", RemovedMessage: "removed", DeclinedMessage: "declined",
	}
}

func newAnchorService(t *testing.T) (*chat.Service, *memoryChatRepository) {
	t.Helper()
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Attachments = styleLockVerifier{owned: map[string]bool{"asset-a": true, "asset-b": true}}
		config.VisualAnchors = anchorSchema()
	})
	return service, repository
}

func anchorCommand(t *testing.T, arguments string) chat.ToolCommand {
	t.Helper()
	return chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session", AssistantMessageID: "assistant",
		Call: agents.ToolCall{ID: "call-1", Name: "set_visual_anchor", Arguments: json.RawMessage(arguments)},
	}
}

func writeAnchor(t *testing.T, service *chat.Service, owner, session, arguments string) chat.ToolResult {
	t.Helper()
	command := anchorCommand(t, arguments)
	command.OwnerID, command.SessionID = owner, session
	result, err := service.WriteVisualAnchor(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestVisualAnchorsCreateUpdateDeleteAndDecline(t *testing.T) {
	service, repository := newAnchorService(t)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "anchors")
	if err != nil {
		t.Fatal(err)
	}
	if result := writeAnchor(t, service, "owner-a", session.ID,
		`{"action":"create","name":"hero","kind":"identity","description":"red cloak","references":["asset-a","asset-a"]}`); result.Error != "" {
		t.Fatalf("create refused: %+v", result)
	}
	anchors, err := service.VisualAnchors(ctx, "owner-a", session.ID)
	if err != nil || len(anchors) != 1 || anchors[0].Name != "hero" || anchors[0].Kind != "identity" ||
		anchors[0].Origin != chat.VisualAnchorOriginAgent || len(anchors[0].References) != 1 {
		t.Fatalf("stored anchors = %+v, %v", anchors, err)
	}
	// An update changes only what it names.
	writeAnchor(t, service, "owner-a", session.ID, `{"action":"update","name":"hero","description":"blue cloak"}`)
	anchors, _ = service.VisualAnchors(ctx, "owner-a", session.ID)
	if anchors[0].Description != "blue cloak" || len(anchors[0].References) != 1 || anchors[0].Kind != "identity" {
		t.Fatalf("updated anchor = %+v", anchors[0])
	}
	// Decline writes nothing.
	writeAnchor(t, service, "owner-a", session.ID, `{"action":"decline","name":"hero"}`)
	anchors, _ = service.VisualAnchors(ctx, "owner-a", session.ID)
	if len(anchors) != 1 {
		t.Fatalf("decline changed anchors: %+v", anchors)
	}
	writeAnchor(t, service, "owner-a", session.ID, `{"action":"delete","name":"hero"}`)
	anchors, _ = service.VisualAnchors(ctx, "owner-a", session.ID)
	if len(anchors) != 0 {
		t.Fatalf("delete left anchors: %+v", anchors)
	}
	_ = repository
}

func TestVisualAnchorsRejectBoundsForeignReferencesAndUserWrites(t *testing.T) {
	service, repository := newAnchorService(t)
	ctx := context.Background()
	session, _ := service.CreateSession(ctx, "owner-a", "Owner", "anchors")
	if result := writeAnchor(t, service, "owner-a", session.ID,
		`{"action":"create","name":"hero","kind":"identity","references":["asset-foreign"]}`); result.Error == "" {
		t.Fatal("a foreign reference was accepted")
	}
	if result := writeAnchor(t, service, "owner-a", session.ID,
		`{"action":"create","name":"`+strings.Repeat("长", 13)+`","kind":"identity"}`); result.Error == "" {
		t.Fatal("an overlong name was accepted")
	}
	if result := writeAnchor(t, service, "owner-a", session.ID,
		`{"action":"create","name":"hero","kind":"costume"}`); result.Error == "" {
		t.Fatal("an unknown kind was accepted")
	}
	writeAnchor(t, service, "owner-a", session.ID, `{"action":"create","name":"one","kind":"identity"}`)
	writeAnchor(t, service, "owner-a", session.ID, `{"action":"create","name":"two","kind":"style"}`)
	if result := writeAnchor(t, service, "owner-a", session.ID, `{"action":"create","name":"three","kind":"identity"}`); result.Error == "" {
		t.Fatal("the anchor limit was not enforced")
	}
	// A user-set anchor is read-only to the Agent, and no tool path touches
	// the person's style lock.
	sessionValue, _, err := repository.Session(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	anchors := sessionValue.VisualAnchors
	anchors[0].Origin = chat.VisualAnchorOriginUser
	if _, err := repository.SetVisualAnchors(ctx, "owner-a", session.ID, anchors); err != nil {
		t.Fatal(err)
	}
	if result := writeAnchor(t, service, "owner-a", session.ID, `{"action":"update","name":"one","description":"changed"}`); result.Error == "" {
		t.Fatal("a user anchor was changed by the Agent")
	}
	if result := writeAnchor(t, service, "owner-a", session.ID, `{"action":"delete","name":"one"}`); result.Error == "" {
		t.Fatal("a user anchor was deleted by the Agent")
	}
	if _, err := service.StyleLock(ctx, "owner-a", session.ID); err != nil {
		t.Fatalf("reading the style lock failed: %v", err)
	}
}

func TestVisualAnchorWritesNeverTouchThePersonStyleLock(t *testing.T) {
	service, repository := newAnchorService(t)
	ctx := context.Background()
	session, _ := service.CreateSession(ctx, "owner-a", "Owner", "anchors")
	lock := chat.StyleLock{Enabled: true, Text: "ink wash", References: []string{"asset-a"}}
	if found, err := repository.SetStyleLock(ctx, "owner-a", session.ID, lock); err != nil || !found {
		t.Fatalf("set style lock = %v, %v", found, err)
	}
	writeAnchor(t, service, "owner-a", session.ID, `{"action":"create","name":"hero","kind":"identity","references":["asset-b"]}`)
	writeAnchor(t, service, "owner-a", session.ID, `{"action":"update","name":"hero","description":"blue"}`)
	writeAnchor(t, service, "owner-a", session.ID, `{"action":"delete","name":"hero"}`)
	stored, found, err := repository.StyleLock(ctx, "owner-a", session.ID)
	if err != nil || !found || !stored.Enabled || stored.Text != "ink wash" ||
		len(stored.References) != 1 || stored.References[0] != "asset-a" {
		t.Fatalf("the person's style lock changed: %+v, %v", stored, err)
	}
}

func TestStartTurnFreezesVisualAnchors(t *testing.T) {
	service, repository := newAnchorService(t)
	ctx := context.Background()
	session, _ := service.CreateSession(ctx, "owner-a", "Owner", "anchors")
	if _, err := repository.SetVisualAnchors(ctx, "owner-a", session.ID, []chat.VisualAnchor{{
		ID: "anchor-1", Name: "hero", Kind: "identity", References: []string{"asset-a"}, Origin: "agent",
	}}); err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw her"})
	if err != nil {
		t.Fatal(err)
	}
	var frozen []chat.VisualAnchor
	turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	frozen = turn.Options.VisualAnchors
	if len(frozen) != 1 || frozen[0].Name != "hero" {
		t.Fatalf("frozen anchors = %+v", frozen)
	}
	if _, err := repository.SetVisualAnchors(ctx, "owner-a", session.ID, nil); err != nil {
		t.Fatal(err)
	}
	turn, _ = service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if len(turn.Options.VisualAnchors) != 1 {
		t.Fatalf("the frozen turn changed after the session anchor write: %+v", turn.Options.VisualAnchors)
	}
}

func TestEncodeDecodeVisualAnchorsRoundTripsAndToleratesLegacyValues(t *testing.T) {
	anchors := []chat.VisualAnchor{{ID: "a", Name: "hero", Kind: "identity", Description: "d", References: []string{"asset"}, Origin: "agent"}}
	encoded, err := chat.EncodeVisualAnchors(anchors)
	if err != nil {
		t.Fatal(err)
	}
	decoded := chat.DecodeVisualAnchors(encoded)
	if len(decoded) != 1 || decoded[0].ID != "a" || decoded[0].Name != "hero" ||
		decoded[0].Kind != "identity" || decoded[0].Description != "d" ||
		len(decoded[0].References) != 1 || decoded[0].References[0] != "asset" || decoded[0].Origin != "agent" {
		t.Fatalf("round trip = %+v", decoded)
	}
	for _, raw := range []string{"", "null", "not json", "[]"} {
		if got := chat.DecodeVisualAnchors(raw); got == nil || len(got) != 0 {
			t.Fatalf("legacy value %q decoded to %+v", raw, got)
		}
	}
	if _, err := chat.EncodeVisualAnchors(nil); err != nil {
		t.Fatalf("nil encode error = %v", err)
	}
}
