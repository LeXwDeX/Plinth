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

const anchorTestUser = "configured-user"

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

// anchorChoiceStore is the session's choice card list as the confirmation gate
// reads it. Only ListChoices is exercised; the other store methods are never
// called by the anchor write.
type anchorChoiceStore struct {
	chat.ChoiceStore
	choices []chat.Choice
}

func (store *anchorChoiceStore) ListChoices(context.Context, string, string, int) ([]chat.Choice, error) {
	return store.choices, nil
}

// accept records that the person accepted a proposal on the card that carried
// answerMessage.
func (store *anchorChoiceStore) accept(proposal, answerMessage string) {
	store.card(proposal, answerMessage, "accept", chat.ChoiceAnswered)
}

// decline records that the person declined the proposal.
func (store *anchorChoiceStore) decline(proposal, answerMessage string) {
	store.card(proposal, answerMessage, "decline", chat.ChoiceAnswered)
}

// pending records an unanswered card with the proposal as its accept option.
func (store *anchorChoiceStore) pending(proposal string) {
	store.card(proposal, "", "accept", chat.ChoicePending)
}

func (store *anchorChoiceStore) card(proposal, answerMessage, answerOption string, state chat.ChoiceState) {
	store.choices = []chat.Choice{{
		ID: "choice-anchor", OwnerID: "owner-a", SessionID: "session-a",
		ToolName: "set_visual_anchor", Kind: "visual_anchor",
		State: state, AnswerOptionID: answerOption, AnswerSource: chat.ChoiceAnsweredOnCard,
		AnswerMessageID: answerMessage,
		Options: []chat.ChoiceOption{
			{ID: "accept", Label: "设为视觉锚", Arguments: json.RawMessage(proposal)},
			{ID: "decline", Label: "不用了", Arguments: json.RawMessage(`{"action":"decline"}`)},
		},
	}}
}

type anchorRig struct {
	service    *chat.Service
	repository *memoryChatRepository
	choices    *anchorChoiceStore
	session    chat.Session
}

func newAnchorRig(t *testing.T) *anchorRig {
	t.Helper()
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Attachments = styleLockVerifier{
			owned: map[string]bool{"asset-a": true, "asset-b": true},
			urls: map[string]chat.Attachment{
				"/api/v1/input-assets/asset-b": {Kind: chat.AttachmentKindImage,
					Role: chat.AttachmentRoleUpload, AssetID: "asset-b"},
				"/api/v1/tasks/task-1/assets/gen-1": {Kind: chat.AttachmentKindImage,
					Role: chat.AttachmentRoleGenerated, AssetID: "gen-1", TaskID: "task-1",
					URL: "/api/v1/tasks/task-1/assets/gen-1"},
			},
		}
		config.VisualAnchors = anchorSchema()
		config.ChoiceKinds = []catalog.ChatChoiceKindSchema{visualAnchorChoiceKind()}
		config.Handles = chat.HandlePolicy{
			Prefixes: map[string]string{chat.AttachmentKindImage: "@img"}, SelectionAlias: "@selected",
		}
	})
	store := &anchorChoiceStore{}
	service.SetChoices(store, 20)
	session, err := service.CreateSession(context.Background(), "owner-a", "Owner", "anchors")
	if err != nil {
		t.Fatal(err)
	}
	return &anchorRig{service: service, repository: repository, choices: store, session: session}
}

func (rig *anchorRig) write(arguments string) chat.ToolResult {
	return rig.writeForUser(arguments, "user-1")
}

func (rig *anchorRig) writeForUser(arguments, userMessage string) chat.ToolResult {
	command := chat.ToolCommand{
		OwnerID: "owner-a", SessionID: rig.session.ID, AssistantMessageID: "assistant",
		Call: agents.ToolCall{ID: "call-1", Name: "set_visual_anchor", Arguments: json.RawMessage(arguments)},
		History: []chat.Message{
			{ID: "user-1", Role: anchorTestUser, Content: "确认"},
		},
	}
	if userMessage != "user-1" {
		command.History = []chat.Message{{ID: userMessage, Role: anchorTestUser, Content: "确认"}}
	}
	result, err := rig.service.WriteVisualAnchor(context.Background(), command)
	if err != nil {
		panic(err)
	}
	return result
}

func (rig *anchorRig) anchors(t *testing.T) []chat.VisualAnchor {
	t.Helper()
	anchors, err := rig.service.VisualAnchors(context.Background(), "owner-a", rig.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	return anchors
}

func TestVisualAnchorsCreateUpdateDeleteAndDecline(t *testing.T) {
	rig := newAnchorRig(t)
	create := `{"action":"create","name":"hero","kind":"identity","description":"red cloak","references":["asset-a","asset-a"]}`
	rig.choices.accept(create, "user-1")
	if result := rig.write(create); result.Error != "" {
		t.Fatalf("create refused: %+v", result)
	}
	anchors := rig.anchors(t)
	if len(anchors) != 1 || anchors[0].Name != "hero" || anchors[0].Kind != "identity" ||
		anchors[0].Origin != chat.VisualAnchorOriginAgent || len(anchors[0].References) != 1 {
		t.Fatalf("stored anchors = %+v", anchors)
	}
	// An update changes only what it names.
	update := `{"action":"update","name":"hero","description":"blue cloak"}`
	rig.choices.accept(update, "user-1")
	rig.write(update)
	anchors = rig.anchors(t)
	if anchors[0].Description != "blue cloak" || len(anchors[0].References) != 1 || anchors[0].Kind != "identity" {
		t.Fatalf("updated anchor = %+v", anchors[0])
	}
	// Decline writes nothing.
	rig.write(`{"action":"decline","name":"hero"}`)
	if anchors = rig.anchors(t); len(anchors) != 1 {
		t.Fatalf("decline changed anchors: %+v", anchors)
	}
	drop := `{"action":"delete","name":"hero"}`
	rig.choices.accept(drop, "user-1")
	rig.write(drop)
	if anchors = rig.anchors(t); len(anchors) != 0 {
		t.Fatalf("delete left anchors: %+v", anchors)
	}
}

// #250 review: an Agent call that no answered confirmation card names must not
// write, update or delete an anchor. The gate binds the exact proposal to the
// user message that answered the card.
func TestVisualAnchorWritesRequireAnAnsweredConfirmationCard(t *testing.T) {
	create := `{"action":"create","name":"hero","kind":"identity","references":["asset-a"]}`
	for name, test := range map[string]struct {
		prepare func(store *anchorChoiceStore)
		user    string
	}{
		"no card":               {func(*anchorChoiceStore) {}, "user-1"},
		"card still pending":    {func(store *anchorChoiceStore) { store.pending(create) }, "user-1"},
		"card answered decline": {func(store *anchorChoiceStore) { store.decline(create, "user-1") }, "user-1"},
		"card answered another proposal": {
			func(store *anchorChoiceStore) {
				store.accept(`{"action":"create","name":"villain","kind":"identity"}`, "user-1")
			}, "user-1"},
		"card answered in a previous turn": {
			func(store *anchorChoiceStore) { store.accept(create, "user-0") }, "user-1"},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newAnchorRig(t)
			test.prepare(rig.choices)
			result := rig.writeForUser(create, test.user)
			if result.Error == "" || !result.NoSubmission {
				t.Fatalf("an unconfirmed write was accepted: %+v", result)
			}
			if anchors := rig.anchors(t); len(anchors) != 0 {
				t.Fatalf("an unconfirmed write changed anchors: %+v", anchors)
			}
		})
	}
	// The exact accepted proposal is written once; a second create of the same
	// name is refused by the anchor rules, not by the gate.
	rig := newAnchorRig(t)
	rig.choices.accept(create, "user-1")
	if result := rig.write(create); result.Error != "" {
		t.Fatalf("the confirmed write was refused: %+v", result)
	}
	if anchors := rig.anchors(t); len(anchors) != 1 || anchors[0].Name != "hero" {
		t.Fatalf("confirmed write = %+v", anchors)
	}
}

func TestVisualAnchorsRejectBoundsForeignReferencesAndUserWrites(t *testing.T) {
	rig := newAnchorRig(t)
	foreign := `{"action":"create","name":"hero","kind":"identity","references":["asset-foreign"]}`
	rig.choices.accept(foreign, "user-1")
	if result := rig.write(foreign); result.Error == "" {
		t.Fatal("a foreign reference was accepted")
	}
	overlong := `{"action":"create","name":"` + strings.Repeat("长", 13) + `","kind":"identity"}`
	rig.choices.accept(overlong, "user-1")
	if result := rig.write(overlong); result.Error == "" {
		t.Fatal("an overlong name was accepted")
	}
	unknownKind := `{"action":"create","name":"hero","kind":"costume"}`
	rig.choices.accept(unknownKind, "user-1")
	if result := rig.write(unknownKind); result.Error == "" {
		t.Fatal("an unknown kind was accepted")
	}
	first := `{"action":"create","name":"one","kind":"identity"}`
	second := `{"action":"create","name":"two","kind":"style"}`
	third := `{"action":"create","name":"three","kind":"identity"}`
	for _, arguments := range []string{first, second} {
		rig.choices.accept(arguments, "user-1")
		if result := rig.write(arguments); result.Error != "" {
			t.Fatalf("create %s refused: %+v", arguments, result)
		}
	}
	rig.choices.accept(third, "user-1")
	if result := rig.write(third); result.Error == "" {
		t.Fatal("the anchor limit was not enforced")
	}
	// A user-set anchor is read-only to the Agent, and no tool path touches
	// the person's style lock.
	sessionValue, _, err := rig.repository.Session(context.Background(), "owner-a", rig.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	anchors := sessionValue.VisualAnchors
	anchors[0].Origin = chat.VisualAnchorOriginUser
	if _, err := rig.repository.SetVisualAnchors(context.Background(), "owner-a", rig.session.ID, anchors); err != nil {
		t.Fatal(err)
	}
	change := `{"action":"update","name":"one","description":"changed"}`
	rig.choices.accept(change, "user-1")
	if result := rig.write(change); result.Error == "" {
		t.Fatal("a user anchor was changed by the Agent")
	}
	remove := `{"action":"delete","name":"one"}`
	rig.choices.accept(remove, "user-1")
	if result := rig.write(remove); result.Error == "" {
		t.Fatal("a user anchor was deleted by the Agent")
	}
	if _, err := rig.service.StyleLock(context.Background(), "owner-a", rig.session.ID); err != nil {
		t.Fatalf("reading the style lock failed: %v", err)
	}
}

// #250 review: the Agent sees handles and URLs, never input-asset IDs. The
// write resolves what the Agent can name into the stored reference form.
func TestVisualAnchorReferencesAcceptHandlesAndPlatformURLs(t *testing.T) {
	t.Run("handle", func(t *testing.T) {
		rig := newAnchorRig(t)
		proposal := `{"action":"create","name":"hero","kind":"identity","references":["@img1"]}`
		rig.choices.accept(proposal, "user-1")
		command := chat.ToolCommand{
			OwnerID: "owner-a", SessionID: rig.session.ID, AssistantMessageID: "assistant",
			Call: agents.ToolCall{ID: "call-1", Name: "set_visual_anchor", Arguments: json.RawMessage(proposal)},
			History: []chat.Message{{
				ID: "user-1", Role: anchorTestUser, Content: "use this",
				ImageURLs: []string{"/api/v1/input-assets/asset-a"},
				Attachments: []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage,
					Role: chat.AttachmentRoleUpload, AssetID: "asset-a", ImageSlot: 1}},
			}},
		}
		result, err := rig.service.WriteVisualAnchor(context.Background(), command)
		if err != nil || result.Error != "" {
			t.Fatalf("handle write = %+v, %v", result, err)
		}
		anchors := rig.anchors(t)
		if len(anchors) != 1 || len(anchors[0].References) != 1 || anchors[0].References[0] != "asset-a" {
			t.Fatalf("handle references = %+v", anchors)
		}
	})

	t.Run("input asset URL", func(t *testing.T) {
		rig := newAnchorRig(t)
		proposal := `{"action":"create","name":"hero","kind":"identity","references":["/api/v1/input-assets/asset-b"]}`
		rig.choices.accept(proposal, "user-1")
		if result := rig.write(proposal); result.Error != "" {
			t.Fatalf("input URL write = %+v", result)
		}
		anchors := rig.anchors(t)
		if len(anchors[0].References) != 1 || anchors[0].References[0] != "asset-b" {
			t.Fatalf("input URL references = %+v", anchors[0])
		}
	})

	t.Run("generated Task asset URL", func(t *testing.T) {
		rig := newAnchorRig(t)
		url := "/api/v1/tasks/task-1/assets/gen-1"
		proposal := `{"action":"create","name":"hero","kind":"identity","references":["` + url + `"]}`
		rig.choices.accept(proposal, "user-1")
		if result := rig.write(proposal); result.Error != "" {
			t.Fatalf("Task asset URL write = %+v", result)
		}
		anchors := rig.anchors(t)
		if len(anchors[0].References) != 1 || anchors[0].References[0] != url {
			t.Fatalf("Task asset references = %+v", anchors[0])
		}
	})

	t.Run("selection alias", func(t *testing.T) {
		rig := newAnchorRig(t)
		proposal := `{"action":"create","name":"hero","kind":"identity","references":["@selected"]}`
		rig.choices.accept(proposal, "user-1")
		command := chat.ToolCommand{
			OwnerID: "owner-a", SessionID: rig.session.ID, AssistantMessageID: "assistant",
			Call:    agents.ToolCall{ID: "call-1", Name: "set_visual_anchor", Arguments: json.RawMessage(proposal)},
			History: []chat.Message{{ID: "user-1", Role: anchorTestUser, Content: "use these"}},
			Options: chat.TurnOptions{Selection: []chat.Attachment{
				{Handle: "@img1", Kind: chat.AttachmentKindImage, AssetID: "asset-a", URL: "/api/v1/input-assets/asset-a"},
				{Handle: "@img2", Kind: chat.AttachmentKindImage, AssetID: "asset-b", URL: "/api/v1/input-assets/asset-b"},
			}},
		}
		result, err := rig.service.WriteVisualAnchor(context.Background(), command)
		if err != nil || result.Error != "" {
			t.Fatalf("selection alias write = %+v, %v", result, err)
		}
		anchors := rig.anchors(t)
		if len(anchors[0].References) != 2 || anchors[0].References[0] != "asset-a" || anchors[0].References[1] != "asset-b" {
			t.Fatalf("selection alias references = %+v", anchors[0])
		}
	})
}

func TestVisualAnchorWritesNeverTouchThePersonStyleLock(t *testing.T) {
	rig := newAnchorRig(t)
	ctx := context.Background()
	lock := chat.StyleLock{Enabled: true, Text: "ink wash", References: []string{"asset-a"}}
	if found, err := rig.repository.SetStyleLock(ctx, "owner-a", rig.session.ID, lock); err != nil || !found {
		t.Fatalf("set style lock = %v, %v", found, err)
	}
	create := `{"action":"create","name":"hero","kind":"identity","references":["asset-b"]}`
	update := `{"action":"update","name":"hero","description":"blue"}`
	remove := `{"action":"delete","name":"hero"}`
	for _, arguments := range []string{create, update, remove} {
		rig.choices.accept(arguments, "user-1")
		rig.write(arguments)
	}
	stored, found, err := rig.repository.StyleLock(ctx, "owner-a", rig.session.ID)
	if err != nil || !found || !stored.Enabled || stored.Text != "ink wash" ||
		len(stored.References) != 1 || stored.References[0] != "asset-a" {
		t.Fatalf("the person's style lock changed: %+v, %v", stored, err)
	}
}

func TestStartTurnFreezesVisualAnchors(t *testing.T) {
	rig := newAnchorRig(t)
	ctx := context.Background()
	if _, err := rig.repository.SetVisualAnchors(ctx, "owner-a", rig.session.ID, []chat.VisualAnchor{{
		ID: "anchor-1", Name: "hero", Kind: "identity", References: []string{"asset-a"}, Origin: "agent",
	}}); err != nil {
		t.Fatal(err)
	}
	assistant, err := rig.service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: rig.session.ID, Content: "draw her"})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := rig.service.GetTurn(ctx, "owner-a", rig.session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turn.Options.VisualAnchors) != 1 || turn.Options.VisualAnchors[0].Name != "hero" {
		t.Fatalf("frozen anchors = %+v", turn.Options.VisualAnchors)
	}
	if _, err := rig.repository.SetVisualAnchors(ctx, "owner-a", rig.session.ID, nil); err != nil {
		t.Fatal(err)
	}
	turn, _ = rig.service.GetTurn(ctx, "owner-a", rig.session.ID, assistant.ID)
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
