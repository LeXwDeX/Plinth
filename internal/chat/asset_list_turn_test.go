package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/project"
)

const (
	itemTurnOwnerID   = "owner-a"
	itemTurnProjectID = "project-a"
	itemTurnListID    = "alist_1"
	itemTurnItemID    = "aitem_1"
	itemTurnSessionID = "session-item-turn"
)

// itemTurnRuns answers ItemByID for the item-turn tests; the embedded nil
// interface keeps the rest of assetlist.RunStore out of the fixture.
type itemTurnRuns struct {
	assetlist.RunStore
	items map[string]assetlist.Item
	err   error
}

func itemTurnRunsKey(ownerID, listID, itemID string) string {
	return ownerID + "/" + listID + "/" + itemID
}

func (runs *itemTurnRuns) ItemByID(_ context.Context, ownerID, listID, itemID string) (assetlist.Item, error) {
	if runs.err != nil {
		return assetlist.Item{}, runs.err
	}
	item, found := runs.items[itemTurnRunsKey(ownerID, listID, itemID)]
	if !found {
		return assetlist.Item{}, assetlist.ErrNotFound
	}
	return item, nil
}

func workingItemTurnItem() assetlist.Item {
	return assetlist.Item{
		ID: itemTurnItemID, OwnerID: itemTurnOwnerID, SessionID: itemTurnSessionID,
		State: assetlist.StateWorking, Name: "Knight",
		References: []project.Reference{{TaskID: "task-1", AssetID: "asset-1"}},
	}
}

func itemTurnRunsWith(item assetlist.Item) *itemTurnRuns {
	return &itemTurnRuns{items: map[string]assetlist.Item{
		itemTurnRunsKey(itemTurnOwnerID, itemTurnListID, item.ID): item,
	}}
}

// newItemTurnService builds the chat service over the runs fixture and one
// item sub-session in the project.
func newItemTurnService(
	t *testing.T,
	runs assetlist.RunStore,
	repository chat.Repository,
) *chat.Service {
	t.Helper()
	return newItemTurnServiceWith(t, runs, repository, nil)
}

// newItemTurnServiceWith additionally installs the attachment handle policy
// and the verifier (which may also resolve the item's references, #328).
func newItemTurnServiceWith(
	t *testing.T,
	runs assetlist.RunStore,
	repository chat.Repository,
	verifier chat.AttachmentVerifier,
) *chat.Service {
	t.Helper()
	schema := defaultAssetListSchema(t)
	listService, err := assetlist.New(assetlist.Config{
		Repository:      newAssetListRepo(),
		Schema:          schema,
		ProjectMaxItems: 20,
		Runs:            runs,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Handles = chat.HandlePolicy{Prefixes: map[string]string{chat.AttachmentKindImage: "@img"}}
		config.Attachments = verifier
	})
	service.SetAssetLists(chat.AssetListPolicy{
		Service: listService, Schema: schema, StepsEnabled: true,
	})
	return service
}

func newItemTurnRepository() *memoryChatRepository {
	repository := newMemoryChatRepository()
	repository.sessions[itemTurnSessionID] = chat.Session{
		ID: itemTurnSessionID, OwnerID: itemTurnOwnerID, ProjectID: itemTurnProjectID,
	}
	return repository
}

func itemTurnInput(continuation string) chat.StartItemTurnInput {
	return chat.StartItemTurnInput{
		OwnerID: itemTurnOwnerID, SessionID: itemTurnSessionID,
		ProjectID: itemTurnProjectID, ListID: itemTurnListID, ItemID: itemTurnItemID,
		Continuation: continuation,
	}
}

// StartItemTurn records the runner's continuation as the user message, the
// assistant placeholder, and freezes the item and its references with the
// turn, including across the options JSON round trip of persistence.
func TestStartItemTurnFreezesTheItemAndItsReferences(t *testing.T) {
	repository := newItemTurnRepository()
	service := newItemTurnService(t, itemTurnRunsWith(workingItemTurnItem()), repository)
	ctx := context.Background()

	assistant, options, err := service.StartItemTurn(ctx, itemTurnInput("  继续第 1 步  "))
	if err != nil {
		t.Fatal(err)
	}
	if assistant.ID == "" || assistant.SessionID != itemTurnSessionID ||
		assistant.Role != "configured-assistant" || assistant.Content != "configured-pending" {
		t.Fatalf("assistant placeholder = %+v", assistant)
	}
	messages := repository.messages[itemTurnSessionID]
	if len(messages) != 2 || messages[0].Role != "configured-user" || messages[0].Content != "继续第 1 步" {
		t.Fatalf("stored messages = %+v", messages)
	}
	if options.AssetListItemID != itemTurnItemID || len(options.AssetListReferences) != 1 ||
		options.AssetListReferences[0].TaskID != "task-1" || options.AssetListReferences[0].AssetID != "asset-1" {
		t.Fatalf("returned options = %+v", options)
	}
	stored, err := service.GetTurn(ctx, itemTurnOwnerID, itemTurnSessionID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Options.AssetListItemID != itemTurnItemID ||
		len(stored.Options.AssetListReferences) != 1 ||
		stored.Options.AssetListReferences[0] != (project.Reference{TaskID: "task-1", AssetID: "asset-1"}) {
		t.Fatalf("stored turn options = %+v", stored.Options)
	}
	if stored.Options.ProjectID != itemTurnProjectID {
		t.Fatalf("stored project = %q; want %q", stored.Options.ProjectID, itemTurnProjectID)
	}
	encoded, err := json.Marshal(stored.Options)
	if err != nil {
		t.Fatal(err)
	}
	var decoded chat.TurnOptions
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.AssetListItemID != itemTurnItemID || len(decoded.AssetListReferences) != 1 ||
		decoded.AssetListReferences[0].AssetID != "asset-1" {
		t.Fatalf("round-tripped options = %+v", decoded)
	}
	if !strings.Contains(string(encoded), `"AssetListItemID":"`+itemTurnItemID+`"`) ||
		!strings.Contains(string(encoded), `"AssetListReferences"`) {
		t.Fatalf("options JSON misses the frozen marks: %s", encoded)
	}
}

// An item the owner does not have in the named list is refused with the
// item-specific error, never a session error.
func TestStartItemTurnRefusesAForeignItem(t *testing.T) {
	service := newItemTurnService(t, itemTurnRunsWith(workingItemTurnItem()), newItemTurnRepository())
	ctx := context.Background()
	input := itemTurnInput("go")
	input.OwnerID = "owner-b"
	if _, _, err := service.StartItemTurn(ctx, input); !errors.Is(err, chat.ErrAssetListItemNotFound) {
		t.Fatalf("foreign owner = %v; want ErrAssetListItemNotFound", err)
	}
	input = itemTurnInput("go")
	input.ItemID = "aitem_missing"
	if _, _, err := service.StartItemTurn(ctx, input); !errors.Is(err, chat.ErrAssetListItemNotFound) {
		t.Fatalf("missing item = %v; want ErrAssetListItemNotFound", err)
	}
}

// The item must be the sub-session of this very session and still working.
func TestStartItemTurnRefusesAMismatchedOrFinishedItem(t *testing.T) {
	ctx := context.Background()
	otherSession := workingItemTurnItem()
	otherSession.SessionID = "session-other"
	service := newItemTurnService(t, itemTurnRunsWith(otherSession), newItemTurnRepository())
	if _, _, err := service.StartItemTurn(ctx, itemTurnInput("go")); !errors.Is(err, chat.ErrAssetListItemSessionMismatch) {
		t.Fatalf("foreign session item = %v; want ErrAssetListItemSessionMismatch", err)
	}

	todo := workingItemTurnItem()
	todo.State = assetlist.StateTodo
	service = newItemTurnService(t, itemTurnRunsWith(todo), newItemTurnRepository())
	if _, _, err := service.StartItemTurn(ctx, itemTurnInput("go")); !errors.Is(err, chat.ErrAssetListItemNotWorking) {
		t.Fatalf("todo item = %v; want ErrAssetListItemNotWorking", err)
	}
}

// The stored session, not the runner's claim, decides the project; an
// unknown session is ErrSessionNotFound.
func TestStartItemTurnRefusesASessionOutsideTheProject(t *testing.T) {
	ctx := context.Background()
	repository := newItemTurnRepository()
	service := newItemTurnService(t, itemTurnRunsWith(workingItemTurnItem()), repository)
	input := itemTurnInput("go")
	input.ProjectID = "project-b"
	if _, _, err := service.StartItemTurn(ctx, input); !errors.Is(err, chat.ErrAssetListItemProjectMismatch) {
		t.Fatalf("foreign project = %v; want ErrAssetListItemProjectMismatch", err)
	}

	repository.sessions[itemTurnSessionID] = chat.Session{
		ID: itemTurnSessionID, OwnerID: itemTurnOwnerID, ProjectID: itemTurnProjectID,
	}
	repository.sessions["session-unknown"] = chat.Session{
		ID: "session-unknown", OwnerID: itemTurnOwnerID, ProjectID: itemTurnProjectID,
	}
	item := workingItemTurnItem()
	item.SessionID = "session-unknown"
	service = newItemTurnService(t, itemTurnRunsWith(item), repository)
	input = itemTurnInput("go")
	input.SessionID = ""
	if _, _, err := service.StartItemTurn(ctx, input); !errors.Is(err, chat.ErrAssetListItemSessionMismatch) {
		t.Fatalf("empty session = %v; want ErrAssetListItemSessionMismatch", err)
	}
	delete(repository.sessions, itemTurnSessionID)
	service = newItemTurnService(t, itemTurnRunsWith(workingItemTurnItem()), repository)
	if _, _, err := service.StartItemTurn(ctx, itemTurnInput("go")); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("unknown session = %v; want ErrSessionNotFound", err)
	}
}

// activeTurnRepository fails the turn write the way an already-running turn
// does; the other repository reads delegate.
type activeTurnRepository struct {
	chat.Repository
}

func (activeTurnRepository) StartTurn(
	context.Context, string, chat.Message, chat.Message, chat.Event, chat.Stream, chat.Turn,
) (chat.Message, chat.Event, error) {
	return chat.Message{}, chat.Event{}, chat.ErrTurnActive
}

// An active turn of the session is surfaced, not swallowed.
func TestStartItemTurnSurfacesAnActiveTurn(t *testing.T) {
	repository := newItemTurnRepository()
	service := newItemTurnService(t, itemTurnRunsWith(workingItemTurnItem()), activeTurnRepository{repository})
	if _, _, err := service.StartItemTurn(context.Background(), itemTurnInput("go")); !errors.Is(err, chat.ErrTurnActive) {
		t.Fatalf("active turn = %v; want ErrTurnActive", err)
	}
}

// A request that carries the server-only marks has them replaced before the
// turn is stored: StartTurn (the request entry) cannot forge an item turn.
func TestStartTurnClearsClientSuppliedAssetListMarks(t *testing.T) {
	repository := newItemTurnRepository()
	service := newItemTurnService(t, itemTurnRunsWith(workingItemTurnItem()), repository)
	ctx := context.Background()
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID: itemTurnOwnerID, SessionID: itemTurnSessionID, Content: "hello",
		Options: chat.TurnOptions{
			AssetListItemID:     "aitem_forged",
			AssetListReferences: []project.Reference{{TaskID: "task-forged", AssetID: "asset-forged"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := service.GetTurn(ctx, itemTurnOwnerID, itemTurnSessionID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Options.AssetListItemID != "" || len(stored.Options.AssetListReferences) != 0 {
		t.Fatalf("client marks survived = %+v", stored.Options)
	}
}

// A deployment without the asset-list workflow refuses item turns.
func TestStartItemTurnWithoutAssetLists(t *testing.T) {
	service := newChatService(t, newItemTurnRepository())
	if _, _, err := service.StartItemTurn(context.Background(), itemTurnInput("go")); !errors.Is(err, chat.ErrAssetListTurnUnavailable) {
		t.Fatalf("unavailable = %v; want ErrAssetListTurnUnavailable", err)
	}
}

// itemTurnAttachments is the reference resolver stub of the item-turn tests:
// the real resolver maps the stored references to platform attachments, while
// these tests check that StartItemTurn passes exactly the frozen references
// and that the assigned handle reaches the Agent.
type itemTurnAttachments struct {
	attachments []chat.Attachment
	references  []project.Reference
	calls       int
	err         error
}

func (refs *itemTurnAttachments) VerifyAttachment(
	context.Context, string, string, chat.AttachmentRef,
) (chat.Attachment, error) {
	return chat.Attachment{}, chat.ErrAttachmentRejected
}

func (refs *itemTurnAttachments) ItemReferenceAttachments(
	_ context.Context, _ string, references []project.Reference,
) ([]chat.Attachment, error) {
	refs.calls++
	refs.references = append([]project.Reference(nil), references...)
	if refs.err != nil {
		return nil, refs.err
	}
	return append([]chat.Attachment(nil), refs.attachments...), nil
}

// The item sub-session starts with its frozen references as handled
// attachments: the stored turn and the user message carry the same handle, so
// the Agent can name @imgN and the ordinary handle lookup returns the picture
// URL the generation reference resolver accepts.
func TestStartItemTurnInjectsTheFrozenReferencesAsHandledAttachments(t *testing.T) {
	repository := newItemTurnRepository()
	resolver := &itemTurnAttachments{attachments: []chat.Attachment{{
		Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated,
		TaskID: "task-1", AssetID: "asset-1",
		URL: "/configured/tasks/task-1/assets/asset-1",
	}}}
	service := newItemTurnServiceWith(t, itemTurnRunsWith(workingItemTurnItem()), repository, resolver)
	assistant, options, err := service.StartItemTurn(context.Background(), itemTurnInput("继续"))
	if err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 1 || len(resolver.references) != 1 ||
		resolver.references[0] != (project.Reference{TaskID: "task-1", AssetID: "asset-1"}) {
		t.Fatalf("resolver calls=%d references=%+v", resolver.calls, resolver.references)
	}
	if len(options.Selection) != 1 || options.Selection[0].Handle != "@img1" ||
		options.Selection[0].URL != "/configured/tasks/task-1/assets/asset-1" ||
		options.Selection[0].TaskID != "task-1" || options.Selection[0].AssetID != "asset-1" {
		t.Fatalf("frozen selection = %+v", options.Selection)
	}
	messages := repository.messages[itemTurnSessionID]
	if len(messages) != 2 || len(messages[0].Attachments) != 1 ||
		messages[0].Attachments[0].Handle != "@img1" || messages[0].Attachments[0].ImageSlot != 0 {
		t.Fatalf("user message attachments = %+v", messages)
	}
	stored, err := service.GetTurn(context.Background(), itemTurnOwnerID, itemTurnSessionID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(stored.Options)
	if err != nil {
		t.Fatal(err)
	}
	var decoded chat.TurnOptions
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Selection) != 1 || decoded.Selection[0].Handle != "@img1" ||
		decoded.Selection[0].URL != "/configured/tasks/task-1/assets/asset-1" {
		t.Fatalf("round-tripped selection = %+v", decoded.Selection)
	}
	policy, _ := handlePolicyForTest(t)
	resolved, err := policy.ResolveHandles(messages, decoded.Selection, []string{"@img1"}, chat.AttachmentKindImage)
	if err != nil || len(resolved) != 1 ||
		resolved[0].URL != "/configured/tasks/task-1/assets/asset-1" {
		t.Fatalf("handle resolution = %+v, %v", resolved, err)
	}
}

// StartTurn is the request entry and never reaches the item resolver: a
// request cannot attach the references of an item it does not run.
func TestStartTurnNeverInjectsItemReferences(t *testing.T) {
	repository := newItemTurnRepository()
	resolver := &itemTurnAttachments{attachments: []chat.Attachment{{
		Kind: chat.AttachmentKindImage, AssetID: "asset-forged",
	}}}
	service := newItemTurnServiceWith(t, itemTurnRunsWith(workingItemTurnItem()), repository, resolver)
	assistant, err := service.StartTurn(context.Background(), chat.StartTurn{
		OwnerID: itemTurnOwnerID, SessionID: itemTurnSessionID, Content: "hello",
		Options: chat.TurnOptions{
			AssetListItemID:     "aitem_forged",
			AssetListReferences: []project.Reference{{TaskID: "task-forged", AssetID: "asset-forged"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 0 {
		t.Fatalf("the request path called the item resolver %d times", resolver.calls)
	}
	stored, err := service.GetTurn(context.Background(), itemTurnOwnerID, itemTurnSessionID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Options.Selection) != 0 || stored.Options.AssetListItemID != "" ||
		len(stored.Options.AssetListReferences) != 0 {
		t.Fatalf("client-supplied item context survived: %+v", stored.Options)
	}
}

// A reference that cannot be mapped fails the start instead of silently
// running the item without its source pictures.
func TestStartItemTurnSurfacesAnUnresolvableReference(t *testing.T) {
	resolver := &itemTurnAttachments{err: errors.New("reference store unavailable")}
	service := newItemTurnServiceWith(t, itemTurnRunsWith(workingItemTurnItem()), newItemTurnRepository(), resolver)
	_, _, err := service.StartItemTurn(context.Background(), itemTurnInput("go"))
	if err == nil || !strings.Contains(err.Error(), "reference store unavailable") {
		t.Fatalf("error = %v", err)
	}
}
