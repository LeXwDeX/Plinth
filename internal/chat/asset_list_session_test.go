package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

const (
	itemSessionOwnerID   = "owner-a"
	itemSessionProjectID = "project-a"
	itemSessionListID    = "alist_session"
	itemSessionItemID    = "aitem_session"
	itemSessionSourceID  = "session-source"
)

// itemCreateRuns answers the item and bind calls of the item-session tests;
// the embedded nil interface keeps the rest of assetlist.RunStore out.
type itemCreateRuns struct {
	assetlist.RunStore
	items    map[string]assetlist.Item
	setCalls []itemSessionSet
	bindErr  error
}

type itemSessionSet struct {
	ownerID         string
	listID          string
	itemID          string
	sessionID       string
	expectedVersion int64
}

func itemCreateRunsKey(ownerID, listID, itemID string) string {
	return ownerID + "/" + listID + "/" + itemID
}

func (runs *itemCreateRuns) ItemByID(_ context.Context, ownerID, listID, itemID string) (assetlist.Item, error) {
	item, found := runs.items[itemCreateRunsKey(ownerID, listID, itemID)]
	if !found {
		return assetlist.Item{}, assetlist.ErrNotFound
	}
	return item, nil
}

func (runs *itemCreateRuns) SetItemSession(
	_ context.Context,
	ownerID, listID, itemID, sessionID string,
	expectedVersion int64,
	_ time.Time,
) (assetlist.Item, error) {
	runs.setCalls = append(runs.setCalls, itemSessionSet{
		ownerID: ownerID, listID: listID, itemID: itemID,
		sessionID: sessionID, expectedVersion: expectedVersion,
	})
	if runs.bindErr != nil {
		return assetlist.Item{}, runs.bindErr
	}
	key := itemCreateRunsKey(ownerID, listID, itemID)
	item := runs.items[key]
	item.SessionID = sessionID
	item.Version++
	runs.items[key] = item
	return item, nil
}

// RunSummaries keeps list reads undecorated in this fixture.
func (runs *itemCreateRuns) RunSummaries(_ context.Context, _, _ string) (map[string]assetlist.RunSummary, error) {
	return nil, nil
}

func itemSessionItem(state string) assetlist.Item {
	return assetlist.Item{
		ID: itemSessionItemID, ListID: itemSessionListID, OwnerID: itemSessionOwnerID,
		Name: "Knight", State: state, Version: 3,
	}
}

func itemSessionList() assetlist.List {
	return assetlist.List{
		ID: itemSessionListID, OwnerID: itemSessionOwnerID, ProjectID: itemSessionProjectID,
		Title: "骑士清单", Status: assetlist.StatusConfirmed,
		SourceSessionID: itemSessionSourceID,
		Brief:           json.RawMessage(`{"purpose":"render"}`),
	}
}

// newItemSessionFixture builds the chat service over one seeded list and item.
// The proposal session carries both a frozen turn choice (the 3D model) and a
// session-level fallback (the 2D model).
func newItemSessionFixture(
	t *testing.T,
	item assetlist.Item,
) (*chat.Service, *memoryChatRepository, *itemCreateRuns) {
	t.Helper()
	repository := newMemoryChatRepository()
	repository.sessions[itemSessionSourceID] = chat.Session{
		ID: itemSessionSourceID, OwnerID: itemSessionOwnerID, ProjectID: itemSessionProjectID,
		ExplicitModelImage: "session-model-2d",
	}
	repository.turns["message-source"] = chat.Turn{
		OwnerID: itemSessionOwnerID, SessionID: itemSessionSourceID,
		AssistantMessageID: "message-source", State: "configured-turn-done",
		CreatedAt: time.Unix(10, 0).UTC(),
		Options:   chat.TurnOptions{ExplicitModel3D: "frozen-model-3d"},
	}
	runs := &itemCreateRuns{items: map[string]assetlist.Item{
		itemCreateRunsKey(itemSessionOwnerID, itemSessionListID, item.ID): item,
	}}
	lists := newAssetListRepo()
	lists.lists = append(lists.lists, itemSessionList())
	schema := defaultAssetListSchema(t)
	listService, err := assetlist.New(assetlist.Config{
		Repository:      lists,
		Runs:            runs,
		Schema:          schema,
		ProjectMaxItems: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Projects = testProjectSource{projects: map[string]bool{
			itemSessionOwnerID + "/" + itemSessionProjectID: true,
		}}
	})
	service.SetAssetLists(chat.AssetListPolicy{
		Service: listService, Schema: schema, StepsEnabled: true,
	})
	return service, repository, runs
}

func itemSessionInput() chat.CreateItemSessionInput {
	return chat.CreateItemSessionInput{
		OwnerID: itemSessionOwnerID, ProjectID: itemSessionProjectID,
		ListID: itemSessionListID, ItemID: itemSessionItemID,
	}
}

// The first call creates the project session with the list's brief, a stored
// manual mode, the proposal session's frozen models and the composed title,
// then binds it to the item with the version it read.
func TestCreateItemSessionCreatesAndBindsTheSession(t *testing.T) {
	t.Parallel()
	service, repository, runs := newItemSessionFixture(t, itemSessionItem(assetlist.StateTodo))
	session, err := service.CreateItemSession(context.Background(), itemSessionInput())
	if err != nil {
		t.Fatal(err)
	}
	if session.ProjectID != itemSessionProjectID || session.ApprovalMode != "manual" ||
		session.Title != "骑士清单 · Knight" {
		t.Fatalf("session = %+v", session)
	}
	if session.Brief.Text("purpose") != "render" {
		t.Fatalf("brief = %+v", session.Brief)
	}
	if session.ExplicitModel3D != "frozen-model-3d" || session.ExplicitModelImage != "session-model-2d" {
		t.Fatalf("models = %q/%q", session.ExplicitModel3D, session.ExplicitModelImage)
	}
	stored, found, err := repository.Session(context.Background(), itemSessionOwnerID, session.ID)
	if err != nil || !found {
		t.Fatalf("stored session = %+v, %v, %v", stored, found, err)
	}
	if stored.ProjectID != itemSessionProjectID || stored.ApprovalMode != "manual" ||
		stored.ExplicitModel3D != "frozen-model-3d" || stored.ExplicitModelImage != "session-model-2d" {
		t.Fatalf("stored session = %+v", stored)
	}
	if len(runs.setCalls) != 1 {
		t.Fatalf("bind calls = %+v", runs.setCalls)
	}
	bind := runs.setCalls[0]
	if bind.sessionID != session.ID || bind.itemID != itemSessionItemID ||
		bind.listID != itemSessionListID || bind.expectedVersion != 3 {
		t.Fatalf("bind = %+v", bind)
	}
	if runs.items[itemCreateRunsKey(itemSessionOwnerID, itemSessionListID, itemSessionItemID)].SessionID != session.ID {
		t.Fatal("item was not bound to the created session")
	}
}

// A caller-supplied title wins over the composed one.
func TestCreateItemSessionKeepsTheRequestedTitle(t *testing.T) {
	t.Parallel()
	service, _, _ := newItemSessionFixture(t, itemSessionItem(assetlist.StateWorking))
	input := itemSessionInput()
	input.Title = "  自定义  "
	session, err := service.CreateItemSession(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if session.Title != "自定义" {
		t.Fatalf("title = %q", session.Title)
	}
}

// todo and working items may get their session; every later state is refused.
func TestCreateItemSessionRefusesFinishedItems(t *testing.T) {
	t.Parallel()
	service, _, _ := newItemSessionFixture(t, itemSessionItem(assetlist.StateReview))
	if _, err := service.CreateItemSession(context.Background(), itemSessionInput()); !errors.Is(err, chat.ErrAssetListItemNotStartable) {
		t.Fatalf("review item = %v; want ErrAssetListItemNotStartable", err)
	}
}

// The item, list and owner scope is enforced before anything is stored.
func TestCreateItemSessionRefusesForeignScopes(t *testing.T) {
	t.Parallel()
	service, _, _ := newItemSessionFixture(t, itemSessionItem(assetlist.StateTodo))
	ctx := context.Background()
	foreign := itemSessionInput()
	foreign.OwnerID = "owner-b"
	if _, err := service.CreateItemSession(ctx, foreign); !errors.Is(err, chat.ErrAssetListItemNotFound) {
		t.Fatalf("foreign owner = %v; want ErrAssetListItemNotFound", err)
	}
	missing := itemSessionInput()
	missing.ItemID = "aitem_missing"
	if _, err := service.CreateItemSession(ctx, missing); !errors.Is(err, chat.ErrAssetListItemNotFound) {
		t.Fatalf("missing item = %v; want ErrAssetListItemNotFound", err)
	}
	wrongProject := itemSessionInput()
	wrongProject.ProjectID = "project-b"
	if _, err := service.CreateItemSession(ctx, wrongProject); !errors.Is(err, chat.ErrAssetListItemNotFound) {
		t.Fatalf("wrong project = %v; want ErrAssetListItemNotFound", err)
	}
	blank := itemSessionInput()
	blank.ListID = " "
	if _, err := service.CreateItemSession(ctx, blank); !errors.Is(err, chat.ErrAssetListItemNotFound) {
		t.Fatalf("blank list = %v; want ErrAssetListItemNotFound", err)
	}
	plain := newChatService(t, newMemoryChatRepository())
	if _, err := plain.CreateItemSession(ctx, itemSessionInput()); !errors.Is(err, chat.ErrAssetListTurnUnavailable) {
		t.Fatalf("no asset lists = %v; want ErrAssetListTurnUnavailable", err)
	}
}

// An existing bound session is returned untouched, so retries do not create a
// second session; a dangling binding is replaced and bound again.
func TestCreateItemSessionIsIdempotentAndReplacesADanglingBinding(t *testing.T) {
	t.Parallel()
	item := itemSessionItem(assetlist.StateWorking)
	service, repository, runs := newItemSessionFixture(t, item)
	repository.sessions["session-bound"] = chat.Session{
		ID: "session-bound", OwnerID: itemSessionOwnerID, ProjectID: itemSessionProjectID,
	}
	boundItem := item
	boundItem.SessionID = "session-bound"
	runs.items[itemCreateRunsKey(itemSessionOwnerID, itemSessionListID, itemSessionItemID)] = boundItem
	session, err := service.CreateItemSession(context.Background(), itemSessionInput())
	if err != nil || session.ID != "session-bound" {
		t.Fatalf("bound session = %+v, %v", session, err)
	}
	if len(runs.setCalls) != 0 {
		t.Fatalf("a bound item was written again: %+v", runs.setCalls)
	}

	dangling := item
	dangling.SessionID = "session-deleted"
	runs.items[itemCreateRunsKey(itemSessionOwnerID, itemSessionListID, itemSessionItemID)] = dangling
	replacement, err := service.CreateItemSession(context.Background(), itemSessionInput())
	if err != nil {
		t.Fatal(err)
	}
	if replacement.ID == "session-deleted" || len(runs.setCalls) != 1 ||
		runs.setCalls[0].sessionID != replacement.ID {
		t.Fatalf("replacement = %+v, binds = %+v", replacement, runs.setCalls)
	}
}

// A session bound in storage to another project is refused.
func TestCreateItemSessionRefusesASessionOutsideTheProject(t *testing.T) {
	t.Parallel()
	item := itemSessionItem(assetlist.StateWorking)
	service, repository, runs := newItemSessionFixture(t, item)
	repository.sessions["session-elsewhere"] = chat.Session{
		ID: "session-elsewhere", OwnerID: itemSessionOwnerID, ProjectID: "project-b",
	}
	item.SessionID = "session-elsewhere"
	runs.items[itemCreateRunsKey(itemSessionOwnerID, itemSessionListID, itemSessionItemID)] = item
	if _, err := service.CreateItemSession(context.Background(), itemSessionInput()); !errors.Is(err, chat.ErrAssetListItemProjectMismatch) {
		t.Fatalf("foreign project session = %v; want ErrAssetListItemProjectMismatch", err)
	}
}

// A lost bind CAS deletes the session this call created, so an unbound
// duplicate never survives.
func TestCreateItemSessionCleansUpAfterALostBind(t *testing.T) {
	t.Parallel()
	service, repository, runs := newItemSessionFixture(t, itemSessionItem(assetlist.StateTodo))
	runs.bindErr = assetlist.ErrConflict
	if _, err := service.CreateItemSession(context.Background(), itemSessionInput()); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("bind conflict = %v", err)
	}
	if len(runs.setCalls) != 1 {
		t.Fatalf("bind calls = %+v", runs.setCalls)
	}
	if _, found, err := repository.Session(context.Background(), itemSessionOwnerID, runs.setCalls[0].sessionID); err != nil || found {
		t.Fatalf("unbound session survived: found=%v err=%v", found, err)
	}
}

// LatestTurn returns the newest turn of the owner's session whatever its
// state, and reports none for an empty, foreign or unknown session.
func TestLatestTurnReadsTheNewestOwnedTurn(t *testing.T) {
	t.Parallel()
	repository := newMemoryChatRepository()
	repository.turns["message-1"] = chat.Turn{
		OwnerID: itemSessionOwnerID, SessionID: "session-a",
		AssistantMessageID: "message-1", State: "configured-turn-done",
		EndReason: "configured-end-completed", CreatedAt: time.Unix(10, 0).UTC(),
	}
	repository.turns["message-2"] = chat.Turn{
		OwnerID: itemSessionOwnerID, SessionID: "session-a",
		AssistantMessageID: "message-2", State: "configured-turn-running",
		CreatedAt: time.Unix(20, 0).UTC(),
	}
	repository.turns["message-3"] = chat.Turn{
		OwnerID: itemSessionOwnerID, SessionID: "session-other",
		AssistantMessageID: "message-3", State: "configured-turn-running",
		CreatedAt: time.Unix(30, 0).UTC(),
	}
	service := newChatService(t, repository)
	ctx := context.Background()

	turn, found, err := service.LatestTurn(ctx, itemSessionOwnerID, "session-a")
	if err != nil || !found || turn.AssistantMessageID != "message-2" ||
		turn.State != "configured-turn-running" || turn.EndReason != "" {
		t.Fatalf("latest turn = %+v, %v, %v", turn, found, err)
	}
	if _, found, err := service.LatestTurn(ctx, "owner-b", "session-a"); err != nil || found {
		t.Fatalf("foreign owner = %v, %v; want no turn", found, err)
	}
	if _, found, err := service.LatestTurn(ctx, itemSessionOwnerID, "session-unknown"); err != nil || found {
		t.Fatalf("unknown session = %v, %v; want no turn", found, err)
	}
	if _, found, err := service.LatestTurn(ctx, "", ""); err != nil || found {
		t.Fatalf("blank ids = %v, %v; want no turn", found, err)
	}
}
