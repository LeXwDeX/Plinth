package assetlistrunner_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/assetlistrunner"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

const (
	testOwner   = "owner-a"
	testProject = "prj_1"
	testList    = "alist_1"
	testRunID   = "run_1"

	turnQueued = "queued"
	turnDone   = "done"
	turnError  = "error"
)

var testNow = time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)

// ---------------------------------------------------------------- fake runs

type recordedTurn struct {
	itemID             string
	assistantMessageID string
	round              int
	workRound          int
}

type fakeRuns struct {
	runs        []assetlist.Run
	items       []assetlist.Item
	claims      int
	finishes    []assetlist.FinishItemInput
	records     []recordedTurn
	transitions []string
}

func (runs *fakeRuns) RunsByStatus(_ context.Context, status string) ([]assetlist.Run, error) {
	var found []assetlist.Run
	for _, run := range runs.runs {
		if run.Status == status {
			found = append(found, run)
		}
	}
	return found, nil
}

func (runs *fakeRuns) UpdateRunStatus(
	_ context.Context, ownerID, listID, from, to, pauseReason string, at time.Time,
) (assetlist.Run, error) {
	run := runs.run(ownerID, listID)
	if run == nil {
		return assetlist.Run{}, assetlist.ErrNotFound
	}
	if run.Status == to {
		return *run, nil
	}
	if run.Status != from {
		return assetlist.Run{}, assetlist.ErrConflict
	}
	run.Status = to
	run.PauseReason = pauseReason
	run.UpdatedAt = at
	runs.transitions = append(runs.transitions, from+"->"+to+"("+pauseReason+")")
	return *run, nil
}

func (runs *fakeRuns) ClaimItem(
	_ context.Context, ownerID, listID string, at time.Time,
) (assetlist.Item, bool, error) {
	run := runs.run(ownerID, listID)
	if run == nil {
		return assetlist.Item{}, false, assetlist.ErrNotFound
	}
	if run.Status != assetlist.RunRunning {
		return assetlist.Item{}, false, nil
	}
	claimable := func(item assetlist.Item) bool {
		return item.ListID == listID &&
			(item.State == assetlist.StateTodo || item.State == assetlist.StateReturned)
	}
	index := -1
	for position := range runs.items {
		if runs.items[position].State == assetlist.StateReturned && claimable(runs.items[position]) {
			index = position
			break
		}
		if index < 0 && claimable(runs.items[position]) {
			index = position
		}
	}
	if index < 0 {
		return assetlist.Item{}, false, nil
	}
	item := &runs.items[index]
	if item.State == assetlist.StateTodo {
		item.Round++
	}
	item.State = assetlist.StateWorking
	item.WorkRound = 1
	item.Version++
	item.UpdatedAt = at
	runs.claims++
	return *item, true, nil
}

func (runs *fakeRuns) FinishItem(
	_ context.Context, input assetlist.FinishItemInput,
) (assetlist.Item, error) {
	item := runs.item(input.ItemID)
	if item == nil {
		return assetlist.Item{}, assetlist.ErrNotFound
	}
	if item.State != assetlist.StateWorking {
		return assetlist.Item{}, assetlist.ErrItemState
	}
	item.State = input.State
	item.StateReason = input.StateReason
	item.Version++
	item.UpdatedAt = input.At
	runs.finishes = append(runs.finishes, input)
	return *item, nil
}

func (runs *fakeRuns) SetItemBlock(
	_ context.Context, ownerID, listID, itemID, block string,
	expectedVersion int64, at time.Time,
) (assetlist.Item, error) {
	item := runs.item(itemID)
	if item == nil || item.OwnerID != ownerID || item.ListID != listID {
		return assetlist.Item{}, assetlist.ErrNotFound
	}
	if expectedVersion != assetlist.VersionUnchecked && item.Version != expectedVersion {
		return assetlist.Item{}, assetlist.ErrConflict
	}
	item.Block = block
	item.Version++
	item.UpdatedAt = at
	return *item, nil
}

func (runs *fakeRuns) RecordItemTurn(
	_ context.Context, ownerID, listID, itemID, assistantMessageID string,
	round, workRound int, expectedVersion int64, at time.Time,
) (assetlist.Item, error) {
	item := runs.item(itemID)
	if item == nil || item.OwnerID != ownerID || item.ListID != listID {
		return assetlist.Item{}, assetlist.ErrNotFound
	}
	if item.State != assetlist.StateWorking {
		return assetlist.Item{}, assetlist.ErrItemState
	}
	if expectedVersion != assetlist.VersionUnchecked && item.Version != expectedVersion {
		return assetlist.Item{}, assetlist.ErrConflict
	}
	item.LastTurnMessageID = assistantMessageID
	item.Round = round
	item.WorkRound = workRound
	item.Version++
	item.UpdatedAt = at
	runs.records = append(runs.records, recordedTurn{
		itemID: itemID, assistantMessageID: assistantMessageID, round: round, workRound: workRound,
	})
	return *item, nil
}

func (runs *fakeRuns) run(ownerID, listID string) *assetlist.Run {
	for index := range runs.runs {
		if runs.runs[index].OwnerID == ownerID && runs.runs[index].ListID == listID {
			return &runs.runs[index]
		}
	}
	return nil
}

func (runs *fakeRuns) item(itemID string) *assetlist.Item {
	for index := range runs.items {
		if runs.items[index].ID == itemID {
			return &runs.items[index]
		}
	}
	return nil
}

// --------------------------------------------------------------- fake lists

type fakeLists struct {
	runs *fakeRuns
}

func (lists *fakeLists) Get(_ context.Context, ownerID, projectID, listID string) (assetlist.List, error) {
	run := lists.runs.run(ownerID, listID)
	if run == nil || run.ProjectID != projectID {
		return assetlist.List{}, assetlist.ErrNotFound
	}
	list := assetlist.List{ID: listID, OwnerID: ownerID, ProjectID: projectID, Title: "List"}
	for _, item := range lists.runs.items {
		if item.ListID == listID {
			list.Items = append(list.Items, item)
		}
	}
	return list, nil
}

// ---------------------------------------------------------------- fake chat

type fakeChat struct {
	runs          *fakeRuns
	now           time.Time
	sessions      map[string]chat.Session
	turns         map[string][]chat.Turn
	tasks         map[string][]generation.Task
	continuations []string
	nextID        int
}

func newFakeChat(runs *fakeRuns, now time.Time) *fakeChat {
	return &fakeChat{
		runs: runs, now: now,
		sessions: map[string]chat.Session{},
		turns:    map[string][]chat.Turn{},
		tasks:    map[string][]generation.Task{},
	}
}

func (chatService *fakeChat) CreateItemSession(
	_ context.Context, input chat.CreateItemSessionInput,
) (chat.Session, error) {
	item := chatService.runs.item(input.ItemID)
	if item == nil {
		return chat.Session{}, chat.ErrAssetListItemNotFound
	}
	if item.State != assetlist.StateTodo && item.State != assetlist.StateWorking {
		return chat.Session{}, chat.ErrAssetListItemNotStartable
	}
	if item.SessionID != "" {
		if session, found := chatService.sessions[item.SessionID]; found {
			return session, nil
		}
	}
	chatService.nextID++
	session := chat.Session{
		ID: fmt.Sprintf("ses_%d", chatService.nextID), OwnerID: input.OwnerID,
		ProjectID: input.ProjectID, Title: input.Title,
	}
	chatService.sessions[session.ID] = session
	item.SessionID = session.ID
	item.Version++
	return session, nil
}

func (chatService *fakeChat) StartItemTurn(
	_ context.Context, input chat.StartItemTurnInput,
) (chat.Message, chat.TurnOptions, error) {
	item := chatService.runs.item(input.ItemID)
	if item == nil {
		return chat.Message{}, chat.TurnOptions{}, chat.ErrAssetListItemNotFound
	}
	if item.State != assetlist.StateWorking {
		return chat.Message{}, chat.TurnOptions{}, chat.ErrAssetListItemNotWorking
	}
	if item.SessionID != input.SessionID {
		return chat.Message{}, chat.TurnOptions{}, chat.ErrAssetListItemSessionMismatch
	}
	if _, found := chatService.sessions[input.SessionID]; !found {
		return chat.Message{}, chat.TurnOptions{}, chat.ErrSessionNotFound
	}
	if turn, found := chatService.latest(input.SessionID); found && !terminal(turn.State) {
		return chat.Message{}, chat.TurnOptions{}, chat.ErrTurnActive
	}
	chatService.nextID++
	options := chat.TurnOptions{ProjectID: input.ProjectID, AssetListItemID: item.ID}
	turn := chat.Turn{
		OwnerID: input.OwnerID, SessionID: input.SessionID,
		AssistantMessageID: fmt.Sprintf("msg_%d", chatService.nextID),
		Options:            options, State: turnQueued, CreatedAt: chatService.now,
	}
	chatService.turns[input.SessionID] = append(chatService.turns[input.SessionID], turn)
	chatService.continuations = append(chatService.continuations, input.Continuation)
	return chat.Message{ID: turn.AssistantMessageID, SessionID: input.SessionID, Role: "assistant"}, options, nil
}

func (chatService *fakeChat) LatestTurn(
	_ context.Context, ownerID, sessionID string,
) (chat.Turn, bool, error) {
	turn, found := chatService.latest(sessionID)
	if !found || turn.OwnerID != ownerID {
		return chat.Turn{}, false, nil
	}
	return turn, true, nil
}

func (chatService *fakeChat) TurnStateTerminal(state string) bool {
	return state == turnDone || state == turnError
}

func (chatService *fakeChat) TurnEndedInError(turn chat.Turn) bool {
	return turn.EndReason == turnError
}

func (chatService *fakeChat) Tasks(
	_ context.Context, ownerID, sessionID string,
) ([]generation.Task, error) {
	if session, found := chatService.sessions[sessionID]; found && session.OwnerID != ownerID {
		return nil, nil
	}
	return append([]generation.Task(nil), chatService.tasks[sessionID]...), nil
}

func (chatService *fakeChat) latest(sessionID string) (chat.Turn, bool) {
	turns := chatService.turns[sessionID]
	if len(turns) == 0 {
		return chat.Turn{}, false
	}
	return turns[len(turns)-1], true
}

// finishTurn ends the session's newest turn.
func (chatService *fakeChat) finishTurn(sessionID, state string) {
	turns := chatService.turns[sessionID]
	if len(turns) == 0 {
		panic("finishTurn without a turn")
	}
	turns[len(turns)-1].State = state
	if state == turnError {
		turns[len(turns)-1].EndReason = turnError
	}
}

// failTask adds a failed Task created during the session's newest turn.
func (chatService *fakeChat) failTask(
	sessionID string, class generation.FailureClass, retryable, ambiguous bool,
) {
	turn, found := chatService.latest(sessionID)
	if !found {
		panic("failTask without a turn")
	}
	chatService.nextID++
	chatService.tasks[sessionID] = append(chatService.tasks[sessionID], generation.Task{
		ID:    generation.TaskID(fmt.Sprintf("task_%d", chatService.nextID)),
		State: generation.TaskFailed, CreatedAt: turn.CreatedAt,
		Failure: &generation.Failure{Class: class, Retryable: retryable, Ambiguous: ambiguous},
	})
}

func (chatService *fakeChat) succeedTask(sessionID string) {
	turn, found := chatService.latest(sessionID)
	if !found {
		panic("succeedTask without a turn")
	}
	chatService.nextID++
	chatService.tasks[sessionID] = append(chatService.tasks[sessionID], generation.Task{
		ID:    generation.TaskID(fmt.Sprintf("task_%d", chatService.nextID)),
		State: generation.TaskSucceeded, CreatedAt: turn.CreatedAt.Add(time.Second),
	})
}

func (chatService *fakeChat) seedTerminalTurn(sessionID, state string) {
	chatService.nextID++
	chatService.turns[sessionID] = append(chatService.turns[sessionID], chat.Turn{
		OwnerID: testOwner, SessionID: sessionID,
		AssistantMessageID: fmt.Sprintf("msg_seed_%d", chatService.nextID),
		State:              state, CreatedAt: chatService.now,
	})
}

func terminal(state string) bool { return state == turnDone || state == turnError }

// ------------------------------------------------------------ fake scheduler

type fakeScheduler struct {
	calls    []chat.ProcessTurn
	failures int
}

func (scheduler *fakeScheduler) Schedule(_ context.Context, turn chat.ProcessTurn) error {
	scheduler.calls = append(scheduler.calls, turn)
	if scheduler.failures > 0 {
		scheduler.failures--
		return errors.New("chat orchestrator: queue full")
	}
	return nil
}

// -------------------------------------------------------------- fake presence

type fakePresence struct {
	states map[string]bool
	err    error
}

func (presence *fakePresence) Active(
	_ context.Context, ownerID, projectID string, _ time.Time,
) (bool, error) {
	if presence.err != nil {
		return false, presence.err
	}
	state, found := presence.states[ownerID+"|"+projectID]
	if !found {
		return true, nil
	}
	return state, nil
}

// ------------------------------------------------------------- fake step caps

type fakeStepCaps struct {
	reached bool
	err     error
	calls   int
}

func (caps *fakeStepCaps) StepCapReached(_ context.Context, _, _ string) (bool, error) {
	caps.calls++
	if caps.err != nil {
		return false, caps.err
	}
	return caps.reached, nil
}

// ------------------------------------------------------------------- fixture

type fixture struct {
	now       time.Time
	runs      *fakeRuns
	lists     *fakeLists
	chat      *fakeChat
	scheduler *fakeScheduler
	presence  *fakePresence
	stepCaps  *fakeStepCaps
	runner    *assetlistrunner.Runner
}

func newFixture(t *testing.T, maxParallel, maxRounds int) *fixture {
	t.Helper()
	fixture := &fixture{
		now:       testNow,
		runs:      &fakeRuns{},
		scheduler: &fakeScheduler{},
		presence:  &fakePresence{states: map[string]bool{}},
		stepCaps:  &fakeStepCaps{},
	}
	fixture.lists = &fakeLists{runs: fixture.runs}
	fixture.chat = newFakeChat(fixture.runs, fixture.now)
	fixture.runner = assetlistrunner.New(assetlistrunner.Config{
		Runs:      fixture.runs,
		Lists:     fixture.lists,
		Chat:      fixture.chat,
		Tasks:     fixture.chat.Tasks,
		Scheduler: fixture.scheduler,
		Presence:  fixture.presence,
		StepCaps:  fixture.stepCaps,
		Schema: catalog.AssetListSchema{Execution: &catalog.AssetListExecutionSchema{
			Enabled:                  true,
			PollIntervalMilliseconds: 2000,
			MaxParallelItems:         maxParallel,
			MaxRoundsPerItem:         maxRounds,
			Presence: catalog.AssetListPresenceSchema{
				HeartbeatSeconds: 15, GraceSeconds: 120,
			},
			Continuation: catalog.AssetListContinuationSchema{
				Message: "第 {round} 轮：{name} / {description} / {route}",
			},
		}},
		Clock:  func() time.Time { return fixture.now },
		Logger: slog.New(slog.DiscardHandler),
	})
	return fixture
}

func newItem(id, name string) assetlist.Item {
	return assetlist.Item{
		ID: id, ListID: testList, OwnerID: testOwner, Kind: "character",
		Name: name, Description: "描述 " + name, RouteID: "text-to-3d",
		Route: []assetlist.RouteStep{{Capability: "text-to-3d", Available: true}},
		State: assetlist.StateTodo, Version: 1, CreatedAt: testNow, UpdatedAt: testNow,
	}
}

func (f *fixture) seedRun(status string, items ...assetlist.Item) {
	f.runs.runs = append(f.runs.runs, assetlist.Run{
		ID: testRunID, OwnerID: testOwner, ProjectID: testProject, ListID: testList,
		Status: status, CreatedAt: f.now, UpdatedAt: f.now,
	})
	f.runs.items = append(f.runs.items, items...)
}

func (f *fixture) advance(t *testing.T) {
	t.Helper()
	if err := f.runner.Advance(context.Background()); err != nil {
		t.Fatalf("Advance: %v", err)
	}
}

func (f *fixture) item(t *testing.T, itemID string) assetlist.Item {
	t.Helper()
	item := f.runs.item(itemID)
	if item == nil {
		t.Fatalf("item %s missing", itemID)
	}
	return *item
}

func (f *fixture) runState(t *testing.T) assetlist.Run {
	t.Helper()
	run := f.runs.run(testOwner, testList)
	if run == nil {
		t.Fatal("run missing")
	}
	return *run
}

// --------------------------------------------------------------------- tests

func TestAdvanceClaimsOneItemCreatesItsSessionAndSchedulesTheFirstTurn(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	fixture.seedRun(assetlist.RunRunning, newItem("aitem_1", "角色"), newItem("aitem_2", "武器"))

	fixture.advance(t)

	first := fixture.item(t, "aitem_1")
	second := fixture.item(t, "aitem_2")
	if fixture.runs.claims != 1 {
		t.Fatalf("claims = %d; want 1", fixture.runs.claims)
	}
	if first.State != assetlist.StateWorking || first.Round != 1 || first.SessionID == "" {
		t.Fatalf("first item = %+v; want working round 1 with a session", first)
	}
	if second.State != assetlist.StateTodo || second.Round != 0 {
		t.Fatalf("second item = %+v; want untouched todo", second)
	}
	if len(fixture.chat.turns[first.SessionID]) != 1 {
		t.Fatalf("turns = %+v; want one started turn", fixture.chat.turns[first.SessionID])
	}
	if len(fixture.scheduler.calls) != 1 {
		t.Fatalf("schedule calls = %d; want 1", len(fixture.scheduler.calls))
	}
	command := fixture.scheduler.calls[0]
	if command.AssistantMessageID != fixture.chat.turns[first.SessionID][0].AssistantMessageID {
		t.Fatalf("scheduled %q; want the started turn", command.AssistantMessageID)
	}
	if first.LastTurnMessageID != command.AssistantMessageID || first.Round != 1 {
		t.Fatalf("recorded turn = %q round %d; want %q round 1",
			first.LastTurnMessageID, first.Round, command.AssistantMessageID)
	}
	if len(fixture.chat.continuations) != 1 ||
		!strings.Contains(fixture.chat.continuations[0], "角色") ||
		!strings.Contains(fixture.chat.continuations[0], "描述 角色") ||
		!strings.Contains(fixture.chat.continuations[0], "text-to-3d") ||
		!strings.Contains(fixture.chat.continuations[0], "第 1 轮") {
		t.Fatalf("continuation = %q; want the four placeholders filled",
			fixture.chat.continuations)
	}
}

func TestAdvanceClaimsAReturnedItemWithItsReworkReason(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	item := newItem("aitem_1", "角色")
	item.State = assetlist.StateReturned
	item.Round = 3
	item.ReturnCount = 1
	item.SessionID = "ses_1"
	item.ReturnReason = "风格不对、贴图有问题"
	item.ReturnNote = "把脸修好"
	fixture.chat.sessions["ses_1"] = chat.Session{
		ID: "ses_1", OwnerID: testOwner, ProjectID: testProject,
	}
	fixture.seedRun(assetlist.RunRunning, item)

	fixture.advance(t)

	claimed := fixture.item(t, "aitem_1")
	if claimed.State != assetlist.StateWorking || claimed.Round != 3 || claimed.WorkRound != 1 ||
		fixture.runs.claims != 1 {
		t.Fatalf("returned item = %+v, claims = %d; want working round 3 work round 1",
			claimed, fixture.runs.claims)
	}
	if len(fixture.chat.continuations) != 1 {
		t.Fatalf("continuations = %q; want one rework turn", fixture.chat.continuations)
	}
	// {round} in the rework copy is the rework ordinal (return_count), not the
	// item's whole-life Agent round.
	continuation := fixture.chat.continuations[0]
	for _, want := range []string{"风格不对", "把脸修好", "第 1 轮重做", "角色"} {
		if !strings.Contains(continuation, want) {
			t.Fatalf("continuation = %q; want %q", continuation, want)
		}
	}
}

// A returned item gets its own allowance of rounds: its first make already
// used the item-wide round count, so the gate must read the work session's
// round, not the item's.
func TestAdvanceGivesAReturnedItemAFullRoundAllowance(t *testing.T) {
	fixture := newFixture(t, 1, 2)
	item := newItem("aitem_1", "角色")
	item.State = assetlist.StateReturned
	// The first make used both rounds; the return itself bumped round to 3.
	item.Round = 3
	item.ReturnCount = 1
	item.SessionID = "ses_1"
	item.ReturnReason = "风格不对"
	fixture.chat.sessions["ses_1"] = chat.Session{
		ID: "ses_1", OwnerID: testOwner, ProjectID: testProject,
	}
	fixture.seedRun(assetlist.RunRunning, item)

	fixture.advance(t)
	first := fixture.item(t, "aitem_1")
	if first.WorkRound != 1 || first.State != assetlist.StateWorking {
		t.Fatalf("rework turned = %+v; want one started round", first)
	}
	fixture.chat.finishTurn(first.SessionID, turnDone)
	if len(fixture.scheduler.calls) != 1 {
		t.Fatalf("schedules = %d; want the first rework round", len(fixture.scheduler.calls))
	}

	// The first rework turn ended without a report: the item gets the second
	// (and last) round of this work session, not round_limit.
	fixture.advance(t)
	second := fixture.item(t, "aitem_1")
	if second.State != assetlist.StateWorking || second.WorkRound != 2 || second.Round != 4 {
		t.Fatalf("second rework round = %+v; want working work round 2 (item round 4)", second)
	}
	if len(fixture.scheduler.calls) != 2 {
		t.Fatalf("schedules = %d; want the second rework round", len(fixture.scheduler.calls))
	}
	fixture.chat.finishTurn(second.SessionID, turnDone)

	fixture.advance(t)
	final := fixture.item(t, "aitem_1")
	if final.State != assetlist.StateFailed || final.StateReason != "round_limit" {
		t.Fatalf("item = %s/%s; want failed/round_limit after the rework allowance", final.State, final.StateReason)
	}
}

func TestAdvanceContinuesRoundsUntilTheRoundLimitThenFailsTheItem(t *testing.T) {
	fixture := newFixture(t, 1, 2)
	fixture.seedRun(assetlist.RunRunning, newItem("aitem_1", "角色"), newItem("aitem_2", "武器"))

	fixture.advance(t)
	first := fixture.item(t, "aitem_1")
	fixture.chat.finishTurn(first.SessionID, turnDone)

	fixture.advance(t)
	first = fixture.item(t, "aitem_1")
	if first.Round != 2 || len(fixture.scheduler.calls) != 2 {
		t.Fatalf("continuation round = %d, schedules = %d; want round 2, 2 schedules",
			first.Round, len(fixture.scheduler.calls))
	}
	if !strings.Contains(fixture.chat.continuations[1], "第 2 轮") {
		t.Fatalf("continuation = %q; want round 2", fixture.chat.continuations[1])
	}
	fixture.chat.finishTurn(first.SessionID, turnDone)

	fixture.advance(t)
	first = fixture.item(t, "aitem_1")
	if first.State != assetlist.StateFailed || first.StateReason != "round_limit" {
		t.Fatalf("round limit item = %s/%s; want failed/round_limit", first.State, first.StateReason)
	}
	second := fixture.item(t, "aitem_2")
	if second.State != assetlist.StateWorking || fixture.runs.claims != 2 {
		t.Fatalf("next item = %s, claims = %d; want working after the limit", second.State, fixture.runs.claims)
	}
	if run := fixture.runState(t); run.Status != assetlist.RunRunning {
		t.Fatalf("run = %s; want running after round_limit", run.Status)
	}
}

func TestAdvanceBlocksTheItemAndPausesTheRunAtTheStepCap(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	fixture.seedRun(assetlist.RunRunning, newItem("aitem_1", "角色"), newItem("aitem_2", "武器"))

	fixture.advance(t)
	first := fixture.item(t, "aitem_1")
	// The turn ended in error with a failed Task: without the step-cap port this
	// would fail the item as internal and pause the run for a failure.
	fixture.chat.finishTurn(first.SessionID, turnError)
	fixture.chat.failTask(first.SessionID, generation.FailureInternal, false, false)
	fixture.stepCaps.reached = true

	fixture.advance(t)

	first = fixture.item(t, "aitem_1")
	if first.State != assetlist.StateWorking || first.Block != assetlist.BlockStepCap {
		t.Fatalf("item = %s/%q; want working blocked at step_cap", first.State, first.Block)
	}
	if first.StateReason != "" {
		t.Fatalf("state reason = %q; a step-cap block is not a failure", first.StateReason)
	}
	if run := fixture.runState(t); run.Status != assetlist.RunPaused ||
		run.PauseReason != assetlist.PauseStepCap {
		t.Fatalf("run = %s/%s; want paused/step_cap", run.Status, run.PauseReason)
	}
	if len(fixture.scheduler.calls) != 1 {
		t.Fatalf("schedules = %d; want no continuation at the cap", len(fixture.scheduler.calls))
	}
	if second := fixture.item(t, "aitem_2"); second.State != assetlist.StateTodo {
		t.Fatalf("second item = %s; want todo after the pause", second.State)
	}
	if fixture.stepCaps.calls != 1 {
		t.Fatalf("step-cap reads = %d; want one at the terminal turn", fixture.stepCaps.calls)
	}
}

func TestAdvanceContinuesTheNextRoundWhenTheStepCapWasNotReached(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	fixture.seedRun(assetlist.RunRunning, newItem("aitem_1", "角色"))

	fixture.advance(t)
	first := fixture.item(t, "aitem_1")
	fixture.chat.finishTurn(first.SessionID, turnDone)

	fixture.advance(t)

	first = fixture.item(t, "aitem_1")
	if first.Round != 2 || first.Block != assetlist.BlockNone {
		t.Fatalf("item = round %d block %q; want an unblocked continuation", first.Round, first.Block)
	}
	if run := fixture.runState(t); run.Status != assetlist.RunRunning {
		t.Fatalf("run = %s; want running", run.Status)
	}
	if fixture.stepCaps.calls != 1 {
		t.Fatalf("step-cap reads = %d; want one at the terminal turn", fixture.stepCaps.calls)
	}
}

func TestAdvanceHonoursMaxParallelItems(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	fixture.seedRun(assetlist.RunRunning, newItem("aitem_1", "角色"), newItem("aitem_2", "武器"))

	fixture.advance(t)
	fixture.advance(t)

	if fixture.runs.claims != 1 {
		t.Fatalf("claims = %d; want one item in flight", fixture.runs.claims)
	}
	if second := fixture.item(t, "aitem_2"); second.State != assetlist.StateTodo {
		t.Fatalf("second item = %s; want todo while the slot is taken", second.State)
	}
}

func TestAdvanceDoesNotCountAnAwaitingApprovalItemAsASlot(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	blocked := newItem("aitem_1", "角色")
	blocked.State = assetlist.StateWorking
	blocked.Round = 1
	blocked.Block = assetlist.BlockAwaitingApproval
	fixture.seedRun(assetlist.RunRunning, blocked, newItem("aitem_2", "武器"))

	fixture.advance(t)

	if second := fixture.item(t, "aitem_2"); second.State != assetlist.StateWorking {
		t.Fatalf("second item = %s; want working because the card releases the slot", second.State)
	}
	if calls := len(fixture.scheduler.calls); calls != 1 {
		t.Fatalf("schedules = %d; want only the claimed item's turn", calls)
	}
}

func TestAdvancePausesOnMissingPresenceAndResumesOnlyPresence(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	fixture.seedRun(assetlist.RunRunning, newItem("aitem_1", "角色"))
	fixture.presence.states[testOwner+"|"+testProject] = false

	fixture.advance(t)
	if run := fixture.runState(t); run.Status != assetlist.RunPaused || run.PauseReason != assetlist.PausePresence {
		t.Fatalf("run = %s/%s; want paused/presence", run.Status, run.PauseReason)
	}
	if fixture.runs.claims != 0 {
		t.Fatalf("claims = %d; want none without presence", fixture.runs.claims)
	}

	fixture.presence.states[testOwner+"|"+testProject] = true
	fixture.advance(t)
	if run := fixture.runState(t); run.Status != assetlist.RunRunning {
		t.Fatalf("run = %s; want running after the heartbeat", run.Status)
	}
	if fixture.runs.claims != 1 {
		t.Fatalf("claims = %d; want the resumed tick to claim", fixture.runs.claims)
	}

	fixture.presence.states[testOwner+"|"+testProject] = false
	fixture.advance(t)
	if run := fixture.runState(t); run.Status != assetlist.RunPaused || run.PauseReason != assetlist.PausePresence {
		t.Fatalf("run = %s/%s; want paused/presence again", run.Status, run.PauseReason)
	}
	fixture.presence.states[testOwner+"|"+testProject] = true
	run := fixture.runs.run(testOwner, testList)
	run.PauseReason = assetlist.PauseUser
	fixture.advance(t)
	if run := fixture.runState(t); run.Status != assetlist.RunPaused || run.PauseReason != assetlist.PauseUser {
		t.Fatalf("run = %s/%s; a user pause must stay sticky", run.Status, run.PauseReason)
	}
}

func TestAdvanceFailureMapping(t *testing.T) {
	cases := []struct {
		name       string
		class      generation.FailureClass
		retryable  bool
		ambiguous  bool
		reason     string
		wantStatus string
		wantReason string
	}{
		{"input invalid continues", generation.FailureInvalidInput, false, false,
			"input_invalid", assetlist.RunRunning, ""},
		{"upstream capacity continues", generation.FailureRateLimited, true, false,
			"upstream_capacity", assetlist.RunRunning, ""},
		{"upstream refused pauses", generation.FailureAuthentication, false, false,
			"upstream_refused", assetlist.RunPaused, assetlist.PauseFailure},
		{"internal pauses", generation.FailureInternal, false, false,
			"internal", assetlist.RunPaused, assetlist.PauseFailure},
		{"ambiguous capacity pauses", generation.FailureRateLimited, true, true,
			"upstream_capacity", assetlist.RunPaused, assetlist.PauseFailure},
		{"submission unknown pauses", generation.FailureSubmissionUnknown, false, true,
			"upstream_capacity", assetlist.RunPaused, assetlist.PauseFailure},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newFixture(t, 1, 3)
			fixture.seedRun(assetlist.RunRunning, newItem("aitem_1", "角色"), newItem("aitem_2", "武器"))
			fixture.advance(t)
			first := fixture.item(t, "aitem_1")
			fixture.chat.finishTurn(first.SessionID, turnDone)
			fixture.chat.failTask(first.SessionID, testCase.class, testCase.retryable, testCase.ambiguous)

			fixture.advance(t)

			first = fixture.item(t, "aitem_1")
			if first.State != assetlist.StateFailed || first.StateReason != testCase.reason {
				t.Fatalf("item = %s/%s; want failed/%s", first.State, first.StateReason, testCase.reason)
			}
			run := fixture.runState(t)
			if run.Status != testCase.wantStatus || run.PauseReason != testCase.wantReason {
				t.Fatalf("run = %s/%s; want %s/%s", run.Status, run.PauseReason,
					testCase.wantStatus, testCase.wantReason)
			}
			if testCase.wantStatus == assetlist.RunRunning {
				if second := fixture.item(t, "aitem_2"); second.State != assetlist.StateWorking {
					t.Fatalf("second item = %s; want the next item started", second.State)
				}
			} else if second := fixture.item(t, "aitem_2"); second.State != assetlist.StateTodo {
				t.Fatalf("second item = %s; want todo after the pause", second.State)
			}
		})
	}
}

func TestAdvancePausesAfterTwoConsecutiveSameCauseFailures(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	fixture.seedRun(assetlist.RunRunning,
		newItem("aitem_1", "角色"), newItem("aitem_2", "武器"), newItem("aitem_3", "道具"))

	fixture.advance(t)
	first := fixture.item(t, "aitem_1")
	fixture.chat.finishTurn(first.SessionID, turnDone)
	fixture.chat.failTask(first.SessionID, generation.FailureInvalidInput, false, false)
	fixture.advance(t)
	if run := fixture.runState(t); run.Status != assetlist.RunRunning {
		t.Fatalf("run = %s after one failure; want running", run.Status)
	}

	second := fixture.item(t, "aitem_2")
	fixture.chat.finishTurn(second.SessionID, turnDone)
	fixture.chat.failTask(second.SessionID, generation.FailureInvalidInput, false, false)
	fixture.advance(t)

	if run := fixture.runState(t); run.Status != assetlist.RunPaused ||
		run.PauseReason != assetlist.PauseFailure {
		t.Fatalf("run = %s/%s; want paused/failure after the same cause twice",
			run.Status, run.PauseReason)
	}
	if third := fixture.item(t, "aitem_3"); third.State != assetlist.StateTodo {
		t.Fatalf("third item = %s; want todo after the pause", third.State)
	}
}

func TestAdvanceCapacityFailureBreaksTheConsecutiveStreak(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	fixture.seedRun(assetlist.RunRunning,
		newItem("aitem_1", "角色"), newItem("aitem_2", "武器"), newItem("aitem_3", "道具"))

	fixture.advance(t)
	first := fixture.item(t, "aitem_1")
	fixture.chat.finishTurn(first.SessionID, turnDone)
	fixture.chat.failTask(first.SessionID, generation.FailureRateLimited, true, false)
	fixture.advance(t)

	second := fixture.item(t, "aitem_2")
	fixture.chat.finishTurn(second.SessionID, turnDone)
	fixture.chat.failTask(second.SessionID, generation.FailureInvalidInput, false, false)
	fixture.advance(t)

	if run := fixture.runState(t); run.Status != assetlist.RunRunning {
		t.Fatalf("run = %s after capacity then input failure; want running", run.Status)
	}
	if third := fixture.item(t, "aitem_3"); third.State != assetlist.StateWorking {
		t.Fatalf("third item = %s; want the next item started", third.State)
	}
}

func TestAdvanceStopsTheTickAfterAFailurePause(t *testing.T) {
	fixture := newFixture(t, 2, 3)
	first := newItem("aitem_1", "角色")
	first.State = assetlist.StateWorking
	first.Round = 1
	first.SessionID = "ses_1"
	second := newItem("aitem_2", "武器")
	second.State = assetlist.StateWorking
	second.Round = 1
	second.SessionID = "ses_2"
	fixture.seedRun(assetlist.RunRunning, first, second)
	for _, sessionID := range []string{"ses_1", "ses_2"} {
		fixture.chat.sessions[sessionID] = chat.Session{
			ID: sessionID, OwnerID: testOwner, ProjectID: testProject,
		}
		fixture.chat.seedTerminalTurn(sessionID, turnDone)
		fixture.chat.failTask(sessionID, generation.FailureInternal, false, false)
	}

	fixture.advance(t)

	if run := fixture.runState(t); run.Status != assetlist.RunPaused ||
		run.PauseReason != assetlist.PauseFailure {
		t.Fatalf("run = %s/%s; want paused/failure", run.Status, run.PauseReason)
	}
	if item := fixture.item(t, "aitem_2"); item.LastTurnMessageID != "" {
		t.Fatalf("second item recorded %q; want the tick stopped at the pause",
			item.LastTurnMessageID)
	}
	if len(fixture.scheduler.calls) != 0 {
		t.Fatalf("schedules = %d; want no turn opened after the pause", len(fixture.scheduler.calls))
	}
}

func TestAdvanceTreatsAnErrorTurnWithoutTasksAsInternalFailure(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	fixture.seedRun(assetlist.RunRunning, newItem("aitem_1", "角色"))

	fixture.advance(t)
	first := fixture.item(t, "aitem_1")
	fixture.chat.finishTurn(first.SessionID, turnError)

	fixture.advance(t)

	first = fixture.item(t, "aitem_1")
	if first.State != assetlist.StateFailed || first.StateReason != "internal" {
		t.Fatalf("item = %s/%s; want failed/internal", first.State, first.StateReason)
	}
	run := fixture.runState(t)
	if run.Status != assetlist.RunPaused || run.PauseReason != assetlist.PauseFailure {
		t.Fatalf("run = %s/%s; want paused/failure", run.Status, run.PauseReason)
	}
}

func TestAdvanceContinuesWhenAFailedTaskWasRecoveredByALaterSuccess(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	fixture.seedRun(assetlist.RunRunning, newItem("aitem_1", "角色"))

	fixture.advance(t)
	first := fixture.item(t, "aitem_1")
	fixture.chat.finishTurn(first.SessionID, turnDone)
	fixture.chat.failTask(first.SessionID, generation.FailureInternal, false, false)
	fixture.chat.succeedTask(first.SessionID)

	fixture.advance(t)

	first = fixture.item(t, "aitem_1")
	if first.State != assetlist.StateWorking || first.Round != 2 {
		t.Fatalf("item = %s round %d; want a continuation round", first.State, first.Round)
	}
	if run := fixture.runState(t); run.Status != assetlist.RunRunning {
		t.Fatalf("run = %s; want running", run.Status)
	}
}

func TestAdvanceReopensAClaimedItemThatNeverGotItsSession(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	orphan := newItem("aitem_1", "角色")
	orphan.State = assetlist.StateWorking
	orphan.Round = 1
	fixture.seedRun(assetlist.RunRunning, orphan)

	fixture.advance(t)

	item := fixture.item(t, "aitem_1")
	if item.SessionID == "" || len(fixture.chat.turns[item.SessionID]) != 1 {
		t.Fatalf("item = %+v; want a session and one turn", item)
	}
	if item.Round != 1 || item.LastTurnMessageID == "" {
		t.Fatalf("round = %d, last turn = %q; want round 1 recorded", item.Round, item.LastTurnMessageID)
	}
}

func TestAdvanceRetriesAnUnrecordedSchedule(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	fixture.seedRun(assetlist.RunRunning, newItem("aitem_1", "角色"))
	fixture.scheduler.failures = 1

	fixture.advance(t)
	first := fixture.item(t, "aitem_1")
	if len(fixture.scheduler.calls) != 1 || first.LastTurnMessageID != "" {
		t.Fatalf("schedules = %d, last turn = %q; want one failed schedule and no record",
			len(fixture.scheduler.calls), first.LastTurnMessageID)
	}

	fixture.advance(t)
	first = fixture.item(t, "aitem_1")
	if len(fixture.scheduler.calls) != 2 {
		t.Fatalf("schedules = %d; want the same turn scheduled again", len(fixture.scheduler.calls))
	}
	if fixture.scheduler.calls[1].AssistantMessageID != fixture.scheduler.calls[0].AssistantMessageID {
		t.Fatalf("rescheduled %q; want %q", fixture.scheduler.calls[1].AssistantMessageID,
			fixture.scheduler.calls[0].AssistantMessageID)
	}
	if first.LastTurnMessageID != fixture.scheduler.calls[0].AssistantMessageID || first.Round != 1 {
		t.Fatalf("recorded %q round %d; want the first turn round 1",
			first.LastTurnMessageID, first.Round)
	}
}

func TestAdvanceRetriesAFinalizedScheduleFailureInsteadOfFailingTheItem(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	fixture.seedRun(assetlist.RunRunning, newItem("aitem_1", "角色"))
	fixture.scheduler.failures = 1

	fixture.advance(t)
	first := fixture.item(t, "aitem_1")
	// chat.Orchestrator.Schedule finalizes a rejected turn before returning.
	fixture.chat.finishTurn(first.SessionID, turnError)

	fixture.advance(t)

	first = fixture.item(t, "aitem_1")
	if first.State != assetlist.StateWorking || first.Round != 1 || first.LastTurnMessageID == "" {
		t.Fatalf("item = %s round %d last %q; want the first round retried",
			first.State, first.Round, first.LastTurnMessageID)
	}
	if len(fixture.scheduler.calls) != 2 {
		t.Fatalf("schedules = %d; want the rejected turn retried", len(fixture.scheduler.calls))
	}
	if run := fixture.runState(t); run.Status != assetlist.RunRunning {
		t.Fatalf("run = %s; a local queue failure must not pause the list", run.Status)
	}
}

func TestAdvanceReconcilesATerminalTurnThatWasNeverRecorded(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	orphan := newItem("aitem_1", "角色")
	orphan.State = assetlist.StateWorking
	orphan.Round = 1
	orphan.SessionID = "ses_seeded"
	fixture.seedRun(assetlist.RunRunning, orphan)
	fixture.chat.sessions["ses_seeded"] = chat.Session{
		ID: "ses_seeded", OwnerID: testOwner, ProjectID: testProject,
	}
	fixture.chat.seedTerminalTurn("ses_seeded", turnDone)

	fixture.advance(t)

	item := fixture.item(t, "aitem_1")
	if item.Round != 2 {
		t.Fatalf("round = %d; want the terminal turn recorded as round 1 and a continuation", item.Round)
	}
	if len(fixture.scheduler.calls) != 1 {
		t.Fatalf("schedules = %d; want one continuation turn", len(fixture.scheduler.calls))
	}
	if item.LastTurnMessageID != fixture.scheduler.calls[0].AssistantMessageID {
		t.Fatalf("last turn = %q; want the new continuation", item.LastTurnMessageID)
	}
	if !strings.Contains(fixture.chat.continuations[len(fixture.chat.continuations)-1], "第 2 轮") {
		t.Fatalf("continuation = %q; want round 2", fixture.chat.continuations)
	}
}

func TestAdvanceDoesNotRescheduleARecordedActiveTurn(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	fixture.seedRun(assetlist.RunRunning, newItem("aitem_1", "角色"))

	fixture.advance(t)
	fixture.advance(t)

	if len(fixture.scheduler.calls) != 1 {
		t.Fatalf("schedules = %d; want the recorded active turn left alone", len(fixture.scheduler.calls))
	}
}

func TestAdvanceRespectsNextActionAt(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	waiting := newItem("aitem_1", "角色")
	waiting.State = assetlist.StateWorking
	waiting.Round = 1
	waiting.NextActionAt = fixture.now.Add(time.Minute)
	fixture.seedRun(assetlist.RunRunning, waiting)

	fixture.advance(t)
	if item := fixture.item(t, "aitem_1"); item.SessionID != "" {
		t.Fatalf("session = %q; want the item left alone until next_action_at", item.SessionID)
	}

	fixture.now = fixture.now.Add(2 * time.Minute)
	fixture.advance(t)
	if item := fixture.item(t, "aitem_1"); item.SessionID == "" {
		t.Fatal("item without a session after next_action_at passed")
	}
}

func TestAdvanceCompletesWhenNothingIsLeftToMake(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	accepted := newItem("aitem_1", "角色")
	accepted.State = assetlist.StateAccepted
	failed := newItem("aitem_2", "武器")
	failed.State = assetlist.StateFailed
	skipped := newItem("aitem_3", "道具")
	skipped.State = assetlist.StateSkipped
	fixture.seedRun(assetlist.RunRunning, accepted, failed, skipped)

	fixture.advance(t)

	if run := fixture.runState(t); run.Status != assetlist.RunCompleted {
		t.Fatalf("run = %s; want completed", run.Status)
	}
}

func TestAdvanceKeepsARunWithOnlyReviewItemsRunning(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	review := newItem("aitem_1", "角色")
	review.State = assetlist.StateReview
	fixture.seedRun(assetlist.RunRunning, review)

	fixture.advance(t)

	if run := fixture.runState(t); run.Status != assetlist.RunRunning {
		t.Fatalf("run = %s; want running while an item awaits review", run.Status)
	}
}

func TestAdvanceLeavesANonRunningRunAlone(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	fixture.seedRun(assetlist.RunIdle, newItem("aitem_1", "角色"))
	fixture.seedRun(assetlist.RunPaused, newItem("aitem_2", "武器"))
	fixture.runs.runs[1].PauseReason = assetlist.PauseUser

	fixture.advance(t)

	if fixture.runs.claims != 0 || len(fixture.scheduler.calls) != 0 {
		t.Fatalf("claims = %d, schedules = %d; want idle/paused runs untouched",
			fixture.runs.claims, len(fixture.scheduler.calls))
	}
	if run := fixture.runState(t); run.Status != assetlist.RunIdle {
		t.Fatalf("run = %s; want idle", run.Status)
	}
}

func TestAdvanceKeepsTheRoundWhenTheTurnIsActive(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	fixture.seedRun(assetlist.RunRunning, newItem("aitem_1", "角色"))

	fixture.advance(t)
	first := fixture.item(t, "aitem_1")

	fixture.advance(t)

	first = fixture.item(t, "aitem_1")
	if first.Round != 1 || first.State != assetlist.StateWorking {
		t.Fatalf("item = %s round %d; want working round 1 while the turn runs",
			first.State, first.Round)
	}
	if len(fixture.chat.continuations) != 1 {
		t.Fatalf("continuations = %d; want no turn opened ahead of the active one",
			len(fixture.chat.continuations))
	}
}
