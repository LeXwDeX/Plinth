package assetlist_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
)

// controlRunStore is an in-memory run port for the control tests. It applies
// the same guards as the SQLite adapter (status, updated_at CAS and item
// version CAS) so the service tests observe real conflicts. The embedded nil
// interface keeps every other port method out of the fixture.
type controlRunStore struct {
	assetlist.RunStore
	runs      map[string]assetlist.Run
	items     map[string]assetlist.Item
	summaries map[string]assetlist.RunSummary

	transitions  []controlTransition
	skips        []controlSkip
	summariesErr error
}

type controlTransition struct {
	ownerID, listID, from, to, pauseReason string
	expected                               time.Time
	at                                     time.Time
}

type controlSkip struct {
	ownerID, listID, itemID, reason string
	expectedVersion                 int64
	at                              time.Time
}

func newControlRunStore() *controlRunStore {
	return &controlRunStore{
		runs:      map[string]assetlist.Run{},
		items:     map[string]assetlist.Item{},
		summaries: map[string]assetlist.RunSummary{},
	}
}

func controlRunKey(ownerID, listID string) string { return ownerID + "\x00" + listID }

func controlItemKey(ownerID, listID, itemID string) string {
	return ownerID + "\x00" + listID + "\x00" + itemID
}

func (store *controlRunStore) GetRunByList(
	_ context.Context,
	ownerID, projectID, listID string,
) (assetlist.Run, error) {
	run, found := store.runs[controlRunKey(ownerID, listID)]
	if !found || run.ProjectID != projectID {
		return assetlist.Run{}, assetlist.ErrNotFound
	}
	return run, nil
}

func (store *controlRunStore) TransitionRun(
	_ context.Context,
	ownerID, listID, from, to, pauseReason string,
	expectedUpdatedAt, at time.Time,
) (assetlist.Run, error) {
	store.transitions = append(store.transitions, controlTransition{
		ownerID: ownerID, listID: listID, from: from, to: to,
		pauseReason: pauseReason, expected: expectedUpdatedAt, at: at,
	})
	key := controlRunKey(ownerID, listID)
	run, found := store.runs[key]
	if !found {
		return assetlist.Run{}, assetlist.ErrNotFound
	}
	if run.Status == to {
		if to != assetlist.RunPaused || run.PauseReason == pauseReason {
			return run, nil
		}
		if run.PauseReason != assetlist.PausePresence || pauseReason != assetlist.PauseUser {
			return assetlist.Run{}, assetlist.ErrConflict
		}
	} else if run.Status != from {
		return assetlist.Run{}, assetlist.ErrConflict
	}
	if !run.UpdatedAt.Equal(expectedUpdatedAt) {
		return assetlist.Run{}, assetlist.ErrConflict
	}
	run.Status = to
	run.PauseReason = pauseReason
	run.UpdatedAt = at
	store.runs[key] = run
	return run, nil
}

func (store *controlRunStore) SkipItem(
	_ context.Context,
	ownerID, listID, itemID, reason string,
	expectedVersion int64,
	at time.Time,
) (assetlist.Item, error) {
	store.skips = append(store.skips, controlSkip{
		ownerID: ownerID, listID: listID, itemID: itemID, reason: reason,
		expectedVersion: expectedVersion, at: at,
	})
	key := controlItemKey(ownerID, listID, itemID)
	item, found := store.items[key]
	if !found {
		return assetlist.Item{}, assetlist.ErrNotFound
	}
	if item.State == assetlist.StateSkipped {
		return item, nil
	}
	if item.State != assetlist.StateTodo && item.State != assetlist.StateWorking {
		return assetlist.Item{}, assetlist.ErrItemState
	}
	if expectedVersion != assetlist.VersionUnchecked && item.Version != expectedVersion {
		return assetlist.Item{}, assetlist.ErrConflict
	}
	item.State = assetlist.StateSkipped
	item.StateReason = reason
	item.Version++
	item.UpdatedAt = at
	store.items[key] = item
	return item, nil
}

func (store *controlRunStore) RunSummaries(
	_ context.Context,
	_, _ string,
) (map[string]assetlist.RunSummary, error) {
	if store.summariesErr != nil {
		return nil, store.summariesErr
	}
	result := make(map[string]assetlist.RunSummary, len(store.summaries))
	for listID, summary := range store.summaries {
		result[listID] = summary
	}
	return result, nil
}

func newControlService(t *testing.T, store *controlRunStore) (*assetlist.Service, *testClock) {
	t.Helper()
	service, _, clock := newService(t, func(config *assetlist.Config) {
		config.Runs = store
	})
	return service, clock
}

func storedControlRun(ownerID, listID, status, pauseReason string, updated time.Time) assetlist.Run {
	return assetlist.Run{
		ID: "run_1", OwnerID: ownerID, ProjectID: "prj_1", ListID: listID,
		Status: status, PauseReason: pauseReason, CreatedAt: updated, UpdatedAt: updated,
	}
}

func runVersion(run assetlist.Run) string {
	return run.UpdatedAt.UTC().Format(time.RFC3339Nano)
}

// The four person actions map to the documented transitions, repeating the
// target status is idempotent, and terminal states refuse new actions.
func TestControlRunAppliesPersonActions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	created := time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC)
	store := newControlRunStore()
	store.runs[controlRunKey("owner-a", "alist_1")] = storedControlRun(
		"owner-a", "alist_1", assetlist.RunIdle, "", created)
	service, clock := newControlService(t, store)

	started, err := service.ControlRun(ctx, " owner-a ", " prj_1 ", " alist_1 ",
		assetlist.RunActionStart, " "+runVersion(store.runs[controlRunKey("owner-a", "alist_1")])+" ", "")
	if err != nil || started.Status != assetlist.RunRunning {
		t.Fatalf("start = %+v, %v", started, err)
	}
	last := store.transitions[len(store.transitions)-1]
	if last.ownerID != "owner-a" || last.listID != "alist_1" ||
		last.from != assetlist.RunIdle || last.to != assetlist.RunRunning ||
		last.pauseReason != assetlist.PauseNone || !last.expected.Equal(created) {
		t.Fatalf("start transition = %+v", last)
	}

	clock.Advance(time.Second)
	if _, err := service.ControlRun(ctx, "owner-a", "prj_1", "alist_1",
		assetlist.RunActionPause, runVersion(started), ""); err != nil {
		t.Fatal(err)
	}
	paused := store.runs[controlRunKey("owner-a", "alist_1")]
	if paused.Status != assetlist.RunPaused || paused.PauseReason != assetlist.PauseUser {
		t.Fatalf("paused = %+v", paused)
	}
	// A double click repeats the target status and writes nothing.
	repeated, err := service.ControlRun(ctx, "owner-a", "prj_1", "alist_1",
		assetlist.RunActionPause, runVersion(paused), "")
	if err != nil || repeated.Status != assetlist.RunPaused || repeated.PauseReason != assetlist.PauseUser {
		t.Fatalf("repeated pause = %+v, %v", repeated, err)
	}

	clock.Advance(time.Second)
	resumed, err := service.ControlRun(ctx, "owner-a", "prj_1", "alist_1",
		assetlist.RunActionResume, runVersion(repeated), "")
	if err != nil || resumed.Status != assetlist.RunRunning || resumed.PauseReason != "" {
		t.Fatalf("resume = %+v, %v", resumed, err)
	}
	if idempotent, err := service.ControlRun(ctx, "owner-a", "prj_1", "alist_1",
		assetlist.RunActionStart, runVersion(resumed), ""); err != nil || idempotent.Status != assetlist.RunRunning {
		t.Fatalf("start while running = %+v, %v", idempotent, err)
	}

	clock.Advance(time.Second)
	stopped, err := service.ControlRun(ctx, "owner-a", "prj_1", "alist_1",
		assetlist.RunActionStop, runVersion(resumed), "")
	if err != nil || stopped.Status != assetlist.RunStopped {
		t.Fatalf("stop = %+v, %v", stopped, err)
	}
	if again, err := service.ControlRun(ctx, "owner-a", "prj_1", "alist_1",
		assetlist.RunActionStop, runVersion(stopped), ""); err != nil || again.Status != assetlist.RunStopped {
		t.Fatalf("repeated stop = %+v, %v", again, err)
	}
	if _, err := service.ControlRun(ctx, "owner-a", "prj_1", "alist_1",
		assetlist.RunActionResume, runVersion(stopped), ""); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("resume stopped = %v; want ErrConflict", err)
	}
	if _, err := service.ControlRun(ctx, "owner-a", "prj_1", "alist_1",
		assetlist.RunActionStart, runVersion(stopped), ""); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("start stopped = %v; want ErrConflict", err)
	}
}

// A manual pause makes an automatic presence pause sticky.
func TestControlRunUpgradesPresencePauseToUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	created := time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC)
	store := newControlRunStore()
	store.runs[controlRunKey("owner-a", "alist_1")] = storedControlRun(
		"owner-a", "alist_1", assetlist.RunPaused, assetlist.PausePresence, created)
	service, _ := newControlService(t, store)

	stored, err := service.ControlRun(ctx, "owner-a", "prj_1", "alist_1",
		assetlist.RunActionPause, runVersion(store.runs[controlRunKey("owner-a", "alist_1")]), "")
	if err != nil || stored.Status != assetlist.RunPaused || stored.PauseReason != assetlist.PauseUser {
		t.Fatalf("upgraded = %+v, %v", stored, err)
	}
	// A step cap pause keeps its reason: a plain pause does not resolve the cap.
	store.runs[controlRunKey("owner-a", "alist_2")] = storedControlRun(
		"owner-a", "alist_2", assetlist.RunPaused, assetlist.PauseStepCap, created)
	capped, err := service.ControlRun(ctx, "owner-a", "prj_1", "alist_2",
		assetlist.RunActionPause, runVersion(store.runs[controlRunKey("owner-a", "alist_2")]), "")
	if err != nil || capped.PauseReason != assetlist.PauseStepCap {
		t.Fatalf("step cap pause = %+v, %v", capped, err)
	}
}

// The updated_at token is the CAS guard: a stale token and a token for another
// owner or project never writes.
func TestControlRunRejectsStaleTokensAndForeignRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	created := time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC)
	store := newControlRunStore()
	store.runs[controlRunKey("owner-a", "alist_1")] = storedControlRun(
		"owner-a", "alist_1", assetlist.RunRunning, "", created)
	service, _ := newControlService(t, store)
	stale := created.Add(-time.Hour).Format(time.RFC3339Nano)

	if _, err := service.ControlRun(ctx, "owner-a", "prj_1", "alist_1",
		assetlist.RunActionPause, stale, ""); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("stale token = %v; want ErrConflict", err)
	}
	if _, err := service.ControlRun(ctx, "owner-b", "prj_1", "alist_1",
		assetlist.RunActionPause, runVersion(store.runs[controlRunKey("owner-a", "alist_1")]), ""); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("foreign owner = %v; want ErrNotFound", err)
	}
	if _, err := service.ControlRun(ctx, "owner-a", "prj_2", "alist_1",
		assetlist.RunActionPause, runVersion(store.runs[controlRunKey("owner-a", "alist_1")]), ""); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("foreign project = %v; want ErrNotFound", err)
	}
	if _, err := service.ControlRun(ctx, "owner-a", "prj_1", "alist_missing",
		assetlist.RunActionPause, stale, ""); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("missing run = %v; want ErrNotFound", err)
	}
}

func TestControlRunValidatesActionAndToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newControlRunStore()
	service, _ := newControlService(t, store)
	token := time.Now().UTC().Format(time.RFC3339Nano)

	if _, err := service.ControlRun(ctx, "owner-a", "prj_1", "alist_1", "bogus", token, ""); !errors.Is(err, assetlist.ErrInvalid) {
		t.Fatalf("unknown action = %v; want ErrInvalid", err)
	}
	if _, err := service.ControlRun(ctx, "owner-a", "prj_1", "alist_1", assetlist.RunActionStart, "not-a-time", ""); !errors.Is(err, assetlist.ErrInvalid) {
		t.Fatalf("bad token = %v; want ErrInvalid", err)
	}
	if _, err := service.ControlRun(ctx, "", "prj_1", "alist_1", assetlist.RunActionStart, token, ""); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("empty owner = %v; want ErrNotFound", err)
	}

	withoutStore, _, _ := newService(t, nil)
	if _, err := withoutStore.ControlRun(ctx, "owner-a", "prj_1", "alist_1",
		assetlist.RunActionStart, token, ""); !errors.Is(err, assetlist.ErrRunUnavailable) {
		t.Fatalf("no run store = %v; want ErrRunUnavailable", err)
	}
}

// skip moves todo and working items, keeps the completed states untouched and
// leaves the item's session and bound turn intact.
func TestSkipItemTransitionsAndKeepsBoundWork(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newControlRunStore()
	service, _ := newControlService(t, store)
	list, err := service.Propose(ctx, proposal())
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("propose = %+v, %v", list, err)
	}
	itemID := list.Items[0].ID
	key := controlItemKey("owner-a", list.ID, itemID)
	store.items[key] = assetlist.Item{
		ID: itemID, ListID: list.ID, OwnerID: "owner-a", State: assetlist.StateTodo,
		SessionID: "ses_item", LastTurnMessageID: "msg_turn", Version: 0,
	}

	skipped, err := service.SkipItem(ctx, " owner-a ", " prj_1 ", list.ID, itemID, 0, "  not needed  ")
	if err != nil || skipped.State != assetlist.StateSkipped || skipped.StateReason != "not needed" {
		t.Fatalf("skip todo = %+v, %v", skipped, err)
	}
	if skipped.SessionID != "ses_item" || skipped.LastTurnMessageID != "msg_turn" {
		t.Fatalf("skip touched bound work: %+v", skipped)
	}
	if last := store.skips[len(store.skips)-1]; last.reason != "not needed" || last.expectedVersion != 0 {
		t.Fatalf("skip call = %+v", last)
	}
	// A repeated skip answers the stored row even with a stale version.
	idempotent, err := service.SkipItem(ctx, "owner-a", "prj_1", list.ID, itemID, 0, "again")
	if err != nil || idempotent.State != assetlist.StateSkipped || idempotent.StateReason != "not needed" {
		t.Fatalf("repeated skip = %+v, %v", idempotent, err)
	}

	store.items[key] = assetlist.Item{
		ID: itemID, ListID: list.ID, OwnerID: "owner-a", State: assetlist.StateWorking,
		SessionID: "ses_item", Version: 3,
	}
	if _, err := service.SkipItem(ctx, "owner-a", "prj_1", list.ID, itemID, 2, ""); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("stale item version = %v; want ErrConflict", err)
	}
	working, err := service.SkipItem(ctx, "owner-a", "prj_1", list.ID, itemID, 3, "")
	if err != nil || working.State != assetlist.StateSkipped {
		t.Fatalf("skip working = %+v, %v", working, err)
	}

	for _, state := range []string{
		assetlist.StateReview, assetlist.StateAccepted, assetlist.StateReturned, assetlist.StateFailed,
	} {
		store.items[key] = assetlist.Item{
			ID: itemID, ListID: list.ID, OwnerID: "owner-a", State: state, Version: 4,
		}
		if _, err := service.SkipItem(ctx, "owner-a", "prj_1", list.ID, itemID, 4, ""); !errors.Is(err, assetlist.ErrItemState) {
			t.Fatalf("skip %s = %v; want ErrItemState", state, err)
		}
	}
}

func TestSkipItemValidatesOwnershipAndVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newControlRunStore()
	service, _ := newControlService(t, store)
	list, err := service.Propose(ctx, proposal())
	if err != nil {
		t.Fatal(err)
	}
	itemID := list.Items[0].ID

	if _, err := service.SkipItem(ctx, "owner-b", "prj_1", list.ID, itemID, 0, ""); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("foreign owner = %v; want ErrNotFound", err)
	}
	if _, err := service.SkipItem(ctx, "owner-a", "prj_2", list.ID, itemID, 0, ""); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("foreign project = %v; want ErrNotFound", err)
	}
	if _, err := service.SkipItem(ctx, "owner-a", "prj_1", list.ID, "aitem_missing", 0, ""); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("missing item = %v; want ErrNotFound", err)
	}
	if _, err := service.SkipItem(ctx, "owner-a", "prj_1", list.ID, itemID, -2, ""); !errors.Is(err, assetlist.ErrInvalid) {
		t.Fatalf("invalid version = %v; want ErrInvalid", err)
	}

	withoutStore, _, _ := newService(t, nil)
	if _, err := withoutStore.SkipItem(ctx, "owner-a", "prj_1", list.ID, itemID, 0, ""); !errors.Is(err, assetlist.ErrRunUnavailable) {
		t.Fatalf("no run store = %v; want ErrRunUnavailable", err)
	}
}

// The list summaries and one list's detail expose the run state and counts.
func TestListAndGetAttachRunSummary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newControlRunStore()
	service, _ := newControlService(t, store)
	list, err := service.Propose(ctx, proposal())
	if err != nil {
		t.Fatal(err)
	}
	store.summaries[list.ID] = assetlist.RunSummary{
		Status: assetlist.RunPaused, PauseReason: assetlist.PauseUser, Version: "token-1",
		ItemCounts: assetlist.ItemCounts{Todo: 1, Skipped: 2},
	}

	// The detail carries its items, so its counts are those items' states
	// (one todo item), not the separately read summary counts.
	read, err := service.Get(ctx, "owner-a", "prj_1", list.ID)
	if err != nil || read.Run == nil || read.Run.Status != assetlist.RunPaused ||
		read.Run.PauseReason != assetlist.PauseUser || read.Run.Version != "token-1" ||
		read.Run.ItemCounts != (assetlist.ItemCounts{Todo: 1}) {
		t.Fatalf("get run = %+v, %v", read.Run, err)
	}
	// The list summaries carry no items and keep the summary counts.
	lists, err := service.List(ctx, "owner-a", "prj_1")
	if err != nil || len(lists) != 1 || lists[0].Run == nil || lists[0].Run.Version != "token-1" ||
		lists[0].Run.ItemCounts != (assetlist.ItemCounts{Todo: 1, Skipped: 2}) {
		t.Fatalf("list runs = %+v, %v", lists, err)
	}

	summary := store.summaries[list.ID]
	delete(store.summaries, list.ID)
	read, err = service.Get(ctx, "owner-a", "prj_1", list.ID)
	if err != nil || read.Run != nil {
		t.Fatalf("list without run = %+v, %v", read.Run, err)
	}
	if _, err := service.RunSummaryByList(ctx, "owner-a", "prj_1", list.ID); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("missing run summary = %v; want ErrNotFound", err)
	}
	store.summaries[list.ID] = summary

	store.summariesErr = errors.New("summary read failed")
	if _, err := service.Get(ctx, "owner-a", "prj_1", list.ID); err == nil {
		t.Fatal("summary read failure must surface")
	}
	store.summariesErr = nil

	withoutStore, _, _ := newService(t, nil)
	if _, err := withoutStore.RunSummaryByList(ctx, "owner-a", "prj_1", list.ID); !errors.Is(err, assetlist.ErrRunUnavailable) {
		t.Fatalf("no run store = %v; want ErrRunUnavailable", err)
	}
}

// The detail reads its items and the run summary in two queries, so an item
// can change state between them (#390). The counts in one answer must agree
// with the items in that same answer: a summary already counting both items
// in review must not sit beside items still todo.
func TestGetCountsTheItemsItReturns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newControlRunStore()
	service, _ := newControlService(t, store)
	input := proposal()
	input.Items = append(input.Items, assetlist.ItemInput{Kind: "character", Name: "Sidekick"})
	list, err := service.Propose(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	store.summaries[list.ID] = assetlist.RunSummary{
		Status: assetlist.RunRunning, Version: "token-2",
		ItemCounts: assetlist.ItemCounts{Review: 2},
	}

	read, err := service.Get(ctx, "owner-a", "prj_1", list.ID)
	if err != nil || read.Run == nil {
		t.Fatalf("get = %+v, %v", read.Run, err)
	}
	var fromItems assetlist.ItemCounts
	for _, item := range read.Items {
		fromItems.Add(item.State, 1)
	}
	if len(read.Items) != 2 || read.Run.ItemCounts != fromItems ||
		read.Run.ItemCounts != (assetlist.ItemCounts{Todo: 2}) {
		t.Fatalf("counts %+v disagree with items %+v", read.Run.ItemCounts, read.Items)
	}
	if read.Run.Status != assetlist.RunRunning || read.Run.Version != "token-2" {
		t.Fatalf("run state = %+v; want the summary's status and version", read.Run)
	}
	if store.summaries[list.ID].ItemCounts != (assetlist.ItemCounts{Review: 2}) {
		t.Fatal("deriving the detail counts must not change the stored summary")
	}
}

// Add counts each documented state and ignores anything else, as the SQL
// summary does.
func TestItemCountsAddCountsEveryDocumentedState(t *testing.T) {
	t.Parallel()
	var counts assetlist.ItemCounts
	for _, state := range []string{
		assetlist.StateTodo, assetlist.StateWorking, assetlist.StateReview, assetlist.StateAccepted,
		assetlist.StateReturned, assetlist.StateFailed, assetlist.StateSkipped, "unknown", "",
	} {
		counts.Add(state, 2)
	}
	want := assetlist.ItemCounts{Todo: 2, Working: 2, Review: 2, Accepted: 2, Returned: 2, Failed: 2, Skipped: 2}
	if counts != want {
		t.Fatalf("counts = %+v; want %+v", counts, want)
	}
}
