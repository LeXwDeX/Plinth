package chat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

const (
	itemSessionID  = "session-item"
	plainSessionID = "session-plain"
)

// itemSessionRuns answers the item-session lookup; the embedded nil interface
// keeps the rest of assetlist.RunStore out of the fixture.
type itemSessionRuns struct {
	assetlist.RunStore
	items map[string]assetlist.Item
	err   error
}

func (r *itemSessionRuns) ItemBySession(_ context.Context, ownerID, sessionID string) (assetlist.Item, bool, error) {
	if r.err != nil {
		return assetlist.Item{}, false, r.err
	}
	item, found := r.items[scopedKey(ownerID, sessionID)]
	return item, found, nil
}

func itemSessionRunsWithItem() *itemSessionRuns {
	return &itemSessionRuns{items: map[string]assetlist.Item{
		scopedKey("owner-a", itemSessionID): {ID: "aitem_1", SessionID: itemSessionID},
	}}
}

// newItemSessionService builds the chat service over one item sub-session
// (stored manual) and one ordinary session (stored auto).
func newItemSessionService(t *testing.T, runs assetlist.RunStore,
	stepsEnabled bool,
) (*chat.Service, *memoryChatRepository) {
	t.Helper()
	repository := newMemoryChatRepository()
	repository.sessions[itemSessionID] = chat.Session{
		ID: itemSessionID, OwnerID: "owner-a", ApprovalMode: "manual",
	}
	repository.sessions[plainSessionID] = chat.Session{
		ID: plainSessionID, OwnerID: "owner-a", ApprovalMode: "auto",
	}
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
	service := newChatService(t, repository)
	service.SetAssetLists(chat.AssetListPolicy{
		Service: listService, Schema: schema, StepsEnabled: stepsEnabled,
	})
	return service, repository
}

// An item sub-session derives the internal steps mode; an ordinary session
// keeps its stored mode even while the list feature is assembled.
func TestSessionApprovalModeDerivesStepsForAnItemSession(t *testing.T) {
	service, _ := newItemSessionService(t, itemSessionRunsWithItem(), true)
	ctx := context.Background()
	if mode, err := service.SessionApprovalMode(ctx, "owner-a", itemSessionID); err != nil || mode != approvalpolicy.ModeSteps {
		t.Fatalf("item session mode = %q err=%v; want steps", mode, err)
	}
	if mode, err := service.SessionApprovalMode(ctx, "owner-a", plainSessionID); err != nil || mode != approvalpolicy.ModeAuto {
		t.Fatalf("ordinary session mode = %q err=%v; want auto", mode, err)
	}
	if _, err := service.SessionApprovalMode(ctx, "owner-a", "session-missing"); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("unknown session = %v; want ErrSessionNotFound", err)
	}
}

// Without application.step_policy an item sub-session keeps its stored mode:
// the derivation can only make a session stricter, never approve by itself.
func TestSessionApprovalModeFallsBackWhenStepsAreNotDeclared(t *testing.T) {
	service, _ := newItemSessionService(t, itemSessionRunsWithItem(), false)
	if mode, err := service.SessionApprovalMode(context.Background(), "owner-a", itemSessionID); err != nil || mode != approvalpolicy.ModeManual {
		t.Fatalf("item session without steps = %q err=%v; want manual", mode, err)
	}
}

// A failed ownership read is an error: no mode is guessed, not even the
// stored one.
func TestSessionApprovalModeFailsWhenTheItemLookupFails(t *testing.T) {
	runs := itemSessionRunsWithItem()
	runs.err = errors.New("database is locked")
	service, _ := newItemSessionService(t, runs, true)
	if mode, err := service.SessionApprovalMode(context.Background(), "owner-a", plainSessionID); !errors.Is(err, runs.err) || mode != "" {
		t.Fatalf("mode = %q err=%v; want the lookup failure", mode, err)
	}
}

// Only an item sub-session names a steps entry. An empty entry makes
// ValidStepEntry reject it, so a counted 3D step falls back to a person.
func TestSessionStepEntryNamesTheListOnlyForItemSessions(t *testing.T) {
	runs := itemSessionRunsWithItem()
	service, _ := newItemSessionService(t, runs, true)
	ctx := context.Background()
	entry, err := service.SessionStepEntry(ctx, "owner-a", itemSessionID)
	if err != nil || entry != approvalpolicy.StepEntryList {
		t.Fatalf("item entry = %q err=%v; want %q", entry, err, approvalpolicy.StepEntryList)
	}
	if entry, err := service.SessionStepEntry(ctx, "owner-a", plainSessionID); err != nil || entry != "" ||
		approvalpolicy.ValidStepEntry(entry) {
		t.Fatalf("ordinary entry = %q err=%v; want empty and invalid", entry, err)
	}
	runs.err = errors.New("database is locked")
	if entry, err := service.SessionStepEntry(ctx, "owner-a", plainSessionID); !errors.Is(err, runs.err) || entry != "" {
		t.Fatalf("read failure = %q, %v; want the error", entry, err)
	}
}

// A person cannot flip an item sub-session's mode: the list workflow owns it.
func TestSetApprovalModeRefusesAnItemSession(t *testing.T) {
	service, repository := newItemSessionService(t, itemSessionRunsWithItem(), true)
	ctx := context.Background()
	if _, _, err := service.SetApprovalMode(ctx, "owner-a", itemSessionID, "auto"); !errors.Is(err, chat.ErrItemSessionApprovalMode) {
		t.Fatalf("item session write = %v; want ErrItemSessionApprovalMode", err)
	}
	if session, found, err := repository.Session(ctx, "owner-a", itemSessionID); err != nil || !found || session.ApprovalMode != "manual" {
		t.Fatalf("item session after refusal = %+v, %v, %v", session, found, err)
	}
	// An ordinary session keeps the existing behavior.
	updated, previous, err := service.SetApprovalMode(ctx, "owner-a", plainSessionID, "manual")
	if err != nil || updated.ApprovalMode != "manual" || previous != "auto" {
		t.Fatalf("ordinary write = %+v previous=%q err=%v", updated, previous, err)
	}
}
