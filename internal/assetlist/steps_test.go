package assetlist_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
)

// stepRunStore is the run port the extra-step write needs: one item's block,
// its list's run and the resume write. Everything else stays unimplemented.
type stepRunStore struct {
	assetlist.RunStore
	runs   map[string]assetlist.Run
	items  map[string]assetlist.Item
	blocks []string
}

func newStepRunStore() *stepRunStore {
	return &stepRunStore{runs: map[string]assetlist.Run{}, items: map[string]assetlist.Item{}}
}

func (store *stepRunStore) GetRunByListID(_ context.Context, ownerID, listID string) (assetlist.Run, error) {
	run, found := store.runs[listID]
	if !found || run.OwnerID != ownerID {
		return assetlist.Run{}, assetlist.ErrNotFound
	}
	return run, nil
}

func (store *stepRunStore) SetItemBlock(
	_ context.Context,
	ownerID, listID, itemID, block string,
	expectedVersion int64,
	at time.Time,
) (assetlist.Item, error) {
	item, found := store.items[itemID]
	if !found || item.OwnerID != ownerID || item.ListID != listID {
		return assetlist.Item{}, assetlist.ErrNotFound
	}
	if expectedVersion != assetlist.VersionUnchecked && item.Version != expectedVersion {
		return assetlist.Item{}, assetlist.ErrConflict
	}
	store.blocks = append(store.blocks, block)
	item.Block = block
	item.Version++
	item.UpdatedAt = at
	store.items[itemID] = item
	return item, nil
}

func (store *stepRunStore) ItemsByList(_ context.Context, ownerID, listID string) ([]assetlist.Item, error) {
	items := make([]assetlist.Item, 0, len(store.items))
	for _, item := range store.items {
		if item.OwnerID == ownerID && item.ListID == listID {
			items = append(items, item)
		}
	}
	return items, nil
}

func (store *stepRunStore) ItemBySession(_ context.Context, ownerID, sessionID string) (assetlist.Item, bool, error) {
	for _, item := range store.items {
		if item.OwnerID == ownerID && item.SessionID == sessionID && sessionID != "" {
			return item, true, nil
		}
	}
	return assetlist.Item{}, false, nil
}

func (store *stepRunStore) ItemByID(_ context.Context, ownerID, listID, itemID string) (assetlist.Item, error) {
	item, found := store.items[itemID]
	if !found || item.OwnerID != ownerID || item.ListID != listID {
		return assetlist.Item{}, assetlist.ErrNotFound
	}
	return item, nil
}

func (store *stepRunStore) UpdateRunStatus(
	_ context.Context,
	ownerID, listID, from, to, pauseReason string,
	at time.Time,
) (assetlist.Run, error) {
	run, found := store.runs[listID]
	if !found || run.OwnerID != ownerID || run.Status != from {
		return assetlist.Run{}, assetlist.ErrConflict
	}
	run.Status, run.PauseReason, run.UpdatedAt = to, pauseReason, at
	store.runs[listID] = run
	return run, nil
}

// stepStore records the extra-step writes and answers the new standing.
type stepStore struct {
	targets  []int
	sessions []string
	err      error
}

func (store *stepStore) set(_ context.Context, _, sessionID string, target int) (assetlist.StepChange, error) {
	store.sessions = append(store.sessions, sessionID)
	store.targets = append(store.targets, target)
	if store.err != nil {
		return assetlist.StepChange{}, store.err
	}
	return assetlist.StepChange{
		Previous: assetlist.StepState{Mode: "cap", Used: 1, Cap: 1},
		Current:  assetlist.StepState{Mode: "cap", Used: 1, Cap: 1, Extra: target},
	}, nil
}

// #330 step 5C: the write is the item's asset standing, clears the item's
// step-cap block and resumes a run paused at the cap; a user pause stays.
func TestSetItemStepsClearsTheCapBlockAndResumesTheRun(t *testing.T) {
	ctx := context.Background()
	runs := newStepRunStore()
	steps := &stepStore{}
	service, _, clock := newService(t, func(config *assetlist.Config) {
		config.Runs = runs
		config.Steps = &assetlist.StepsConfig{Maximum: 5, TotalMaximum: 20, Set: steps.set}
	})
	list, err := service.Propose(ctx, proposal())
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("propose = %+v %v", list, err)
	}
	itemID := list.Items[0].ID
	runs.items[itemID] = assetlist.Item{
		ID: itemID, ListID: list.ID, OwnerID: "owner-a", State: assetlist.StateWorking,
		SessionID: "ses_item", Block: assetlist.BlockStepCap, Version: 0,
	}
	runs.runs[list.ID] = assetlist.Run{
		ID: "run_1", OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Status: assetlist.RunPaused, PauseReason: assetlist.PauseStepCap,
	}
	item, change, err := service.SetItemSteps(ctx, "owner-a", "prj_1", list.ID, itemID, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps.targets) != 1 || steps.targets[0] != 3 || steps.sessions[0] != "ses_item" {
		t.Fatalf("step write = %+v sessions=%v", steps.targets, steps.sessions)
	}
	if change.Current.Extra != 3 || change.Previous.Extra != 0 {
		t.Fatalf("change = %+v", change)
	}
	if item.Block != assetlist.BlockNone || item.Version != 1 || !item.UpdatedAt.Equal(clock.Now()) {
		t.Fatalf("item = %+v", item)
	}
	if stored := runs.items[itemID]; stored.Block != assetlist.BlockNone || stored.Version != 1 {
		t.Fatalf("stored item = %+v", stored)
	}
	if run := runs.runs[list.ID]; run.Status != assetlist.RunRunning || run.PauseReason != "" {
		t.Fatalf("run = %+v", run)
	}
}

func TestSetItemStepsKeepsAUserPauseAndValidatesTheRequest(t *testing.T) {
	ctx := context.Background()
	runs := newStepRunStore()
	steps := &stepStore{}
	service, _, _ := newService(t, func(config *assetlist.Config) {
		config.Runs = runs
		config.Steps = &assetlist.StepsConfig{Maximum: 5, TotalMaximum: 20, Set: steps.set}
	})
	list, err := service.Propose(ctx, proposal())
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("propose = %+v %v", list, err)
	}
	itemID := list.Items[0].ID
	runs.items[itemID] = assetlist.Item{
		ID: itemID, ListID: list.ID, OwnerID: "owner-a", State: assetlist.StateWorking,
		SessionID: "ses_item", Version: 0,
	}
	runs.runs[list.ID] = assetlist.Run{
		ID: "run_1", OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Status: assetlist.RunPaused, PauseReason: assetlist.PauseUser,
	}
	if _, _, err := service.SetItemSteps(ctx, "owner-a", "prj_1", list.ID, itemID, 2, 0); err != nil {
		t.Fatal(err)
	}
	if run := runs.runs[list.ID]; run.Status != assetlist.RunPaused || run.PauseReason != assetlist.PauseUser {
		t.Fatalf("a user pause was resumed: %+v", run)
	}

	// A stale item version, an unknown item and a missing session are refused
	// before the step store is called.
	if _, _, err := service.SetItemSteps(ctx, "owner-a", "prj_1", list.ID, itemID, 3, 0); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("stale version: %v", err)
	}
	if _, _, err := service.SetItemSteps(ctx, "owner-a", "prj_1", list.ID, "missing", 3, 0); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("unknown item: %v", err)
	}
	runs.items[itemID] = assetlist.Item{
		ID: itemID, ListID: list.ID, OwnerID: "owner-a", State: assetlist.StateTodo, Version: 0,
	}
	if _, _, err := service.SetItemSteps(ctx, "owner-a", "prj_1", list.ID, itemID, 0, 0); !errors.Is(err, assetlist.ErrStepUnavailable) {
		t.Fatalf("no session: %v", err)
	}
	// The declared total bound is checked before the port.
	if _, _, err := service.SetItemSteps(ctx, "owner-a", "prj_1", list.ID, itemID, 21, 0); !errors.Is(err, assetlist.ErrStepExtraLimit) {
		t.Fatalf("total bound: %v", err)
	}
	var limit *assetlist.StepExtraLimitError
	if _, _, err := service.SetItemSteps(ctx, "owner-a", "prj_1", list.ID, itemID, 21, 0); !errors.As(err, &limit) ||
		limit.Maximum != 20 || limit.Requested != 21 {
		t.Fatalf("typed limit: %v", err)
	}
	if len(steps.targets) != 1 {
		t.Fatalf("refusals wrote extra steps: %v", steps.targets)
	}

	// A refusal from the grant layer keeps its error and writes no block.
	runs.items[itemID] = assetlist.Item{
		ID: itemID, ListID: list.ID, OwnerID: "owner-a", State: assetlist.StateWorking,
		SessionID: "ses_item", Version: 0,
	}
	steps.err = &assetlist.StepExtraSingleLimitError{Maximum: 5}
	if _, _, err := service.SetItemSteps(ctx, "owner-a", "prj_1", list.ID, itemID, 4, 0); !errors.Is(err, assetlist.ErrStepExtraSingleLimit) {
		t.Fatalf("single bound: %v", err)
	}
	if runs.items[itemID].Block != assetlist.BlockNone || runs.items[itemID].Version != 0 {
		t.Fatalf("a refused write touched the item: %+v", runs.items[itemID])
	}
}

// A deployment without the step ports keeps the refusals of the historical
// wire: no write is possible and the run stays as it was.
func TestSetItemStepsWithoutPortsIsUnavailable(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newService(t, nil)
	list, err := service.Propose(ctx, proposal())
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("propose = %+v %v", list, err)
	}
	if _, _, err := service.SetItemSteps(ctx, "owner-a", "prj_1", list.ID, list.Items[0].ID, 1, 0); !errors.Is(err, assetlist.ErrRunUnavailable) {
		t.Fatalf("no ports: %v", err)
	}
}

// #330 §6: the session-keyed grant release resolves the item by its session,
// clears the step-cap block and resumes the paused run; a plain session is a
// no-op.
func TestClearStepCapForSessionReleasesTheItemsRun(t *testing.T) {
	ctx := context.Background()
	runs := newStepRunStore()
	service, _, clock := newService(t, func(config *assetlist.Config) {
		config.Runs = runs
	})
	list, err := service.Propose(ctx, proposal())
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("propose = %+v %v", list, err)
	}
	itemID := list.Items[0].ID
	runs.items[itemID] = assetlist.Item{
		ID: itemID, ListID: list.ID, OwnerID: "owner-a", State: assetlist.StateWorking,
		SessionID: "ses_item", Block: assetlist.BlockStepCap, Version: 0,
	}
	runs.runs[list.ID] = assetlist.Run{
		ID: "run_1", OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Status: assetlist.RunPaused, PauseReason: assetlist.PauseStepCap,
	}
	if err := service.ClearStepCapForSession(ctx, "owner-a", "ses_item"); err != nil {
		t.Fatal(err)
	}
	if stored := runs.items[itemID]; stored.Block != assetlist.BlockNone || !stored.UpdatedAt.Equal(clock.Now()) {
		t.Fatalf("item = %+v; want the block cleared", stored)
	}
	if run := runs.runs[list.ID]; run.Status != assetlist.RunRunning || run.PauseReason != "" {
		t.Fatalf("run = %+v; want running", run)
	}

	// A plain session and an item without the block change nothing.
	before := runs.items[itemID]
	if err := service.ClearStepCapForSession(ctx, "owner-a", "ses_other"); err != nil {
		t.Fatal(err)
	}
	if stored := runs.items[itemID]; stored.Version != before.Version {
		t.Fatalf("a plain session touched the item: %+v", stored)
	}
}
