package assetlistrunner_test

import (
	"context"
	"encoding/json"
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

func (runs *fakeRuns) SetItemStepCapTurn(
	_ context.Context, ownerID, listID, itemID, assistantMessageID string,
	expectedVersion int64, at time.Time,
) (assetlist.Item, error) {
	item := runs.item(itemID)
	if item == nil || item.OwnerID != ownerID || item.ListID != listID {
		return assetlist.Item{}, assetlist.ErrNotFound
	}
	if expectedVersion != assetlist.VersionUnchecked && item.Version != expectedVersion {
		return assetlist.Item{}, assetlist.ErrConflict
	}
	item.StepCapTurnMessageID = assistantMessageID
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
	// sourceSessionID is the session the list was proposed in (#329f), where
	// the list-end summary is posted. Empty leaves the summary nowhere to go.
	sourceSessionID string
}

func (lists *fakeLists) Get(_ context.Context, ownerID, projectID, listID string) (assetlist.List, error) {
	run := lists.runs.run(ownerID, listID)
	if run == nil || run.ProjectID != projectID {
		return assetlist.List{}, assetlist.ErrNotFound
	}
	list := assetlist.List{
		ID: listID, OwnerID: ownerID, ProjectID: projectID, Title: "List",
		SourceSessionID: lists.sourceSessionID,
	}
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
	messages      map[string]chat.Message
	continuations []string
	nextID        int
}

func newFakeChat(runs *fakeRuns, now time.Time) *fakeChat {
	return &fakeChat{
		runs: runs, now: now,
		sessions: map[string]chat.Session{},
		turns:    map[string][]chat.Turn{},
		tasks:    map[string][]generation.Task{},
		messages: map[string]chat.Message{},
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
	_ context.Context, ownerID, sessionID string, _ generation.TaskID,
) ([]generation.Task, error) {
	if session, found := chatService.sessions[sessionID]; found && session.OwnerID != ownerID {
		return nil, nil
	}
	return append([]generation.Task(nil), chatService.tasks[sessionID]...), nil
}

// SettleReportedItem moves a working item whose stored result names a succeeded
// Task into review, the way the chat service does once the delivery is stable
// (#459). Items without a stored report are left to the ordinary turn path.
func (chatService *fakeChat) SettleReportedItem(
	_ context.Context, input chat.SettleReportedItemInput,
) (assetlist.Item, bool, error) {
	var snapshot struct {
		TaskID string `json:"task_id"`
	}
	if len(input.Item.Result) == 0 || json.Unmarshal(input.Item.Result, &snapshot) != nil || snapshot.TaskID == "" {
		return input.Item, false, nil
	}
	for _, task := range input.Tasks {
		if string(task.ID) != snapshot.TaskID {
			continue
		}
		if generation.ResourcesPending(task.Resources) {
			// Still converting: the item keeps its state.
			return input.Item, true, nil
		}
		finished, err := chatService.runs.FinishItem(context.Background(), assetlist.FinishItemInput{
			OwnerID: input.OwnerID, ListID: input.ListID, ItemID: input.Item.ID,
			State: assetlist.StateReview, Result: input.Item.Result, At: input.Now,
		})
		return finished, true, err
	}
	return input.Item, false, nil
}

// PostAssistantMessage records a platform message the way the chat service
// does: the caller chose the id, so posting it twice stores it once.
func (chatService *fakeChat) PostAssistantMessage(
	_ context.Context, ownerID string, message chat.Message,
) (chat.Message, bool, error) {
	if ownerID == "" || message.ID == "" || message.SessionID == "" {
		return chat.Message{}, false, chat.ErrMessageNotFound
	}
	if session, found := chatService.sessions[message.SessionID]; !found || session.OwnerID != ownerID {
		return chat.Message{}, false, chat.ErrSessionNotFound
	}
	if _, found := chatService.messages[message.ID]; found {
		return message, false, nil
	}
	message.Role = "assistant"
	chatService.messages[message.ID] = message
	return message, true, nil
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

// fakeStepCaps answers the runner's cap question per session: `all` caps every
// asset, and reached names individual sessions, so one item can be blocked
// while another still works (#330 §6).
type fakeStepCaps struct {
	all     bool
	reached map[string]bool
	err     error
	calls   int
}

func (caps *fakeStepCaps) StepCapReached(_ context.Context, _, sessionID string) (bool, error) {
	caps.calls++
	if caps.err != nil {
		return false, caps.err
	}
	return caps.all || caps.reached[sessionID], nil
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
	// config is the runner's assembly; a test that declares more Schema or an
	// extra port rebuilds from it with buildRunner.
	config assetlistrunner.Config
	runner *assetlistrunner.Runner
}

// buildRunner replaces the fixture's runner with one built from the same ports,
// after mutate adjusts the assembly (more Schema copy, a step port).
func (f *fixture) buildRunner(mutate func(*assetlistrunner.Config)) {
	config := f.config
	if mutate != nil {
		mutate(&config)
	}
	f.runner = assetlistrunner.New(config)
}

func newFixture(t *testing.T, maxParallel, maxRounds int) *fixture {
	t.Helper()
	fixture := &fixture{
		now:       testNow,
		runs:      &fakeRuns{},
		scheduler: &fakeScheduler{},
		presence:  &fakePresence{states: map[string]bool{}},
		stepCaps:  &fakeStepCaps{reached: map[string]bool{}},
	}
	fixture.lists = &fakeLists{runs: fixture.runs}
	fixture.chat = newFakeChat(fixture.runs, fixture.now)
	config := assetlistrunner.Config{
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
	}
	fixture.config = config
	fixture.runner = assetlistrunner.New(config)
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

// #330 §6: a capped item is blocked and releases its parallel slot; the
// scheduler keeps going and claims the next item, and the run stays running.
func TestAdvanceBlocksTheCappedItemAndClaimsTheNext(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	fixture.seedRun(assetlist.RunRunning, newItem("aitem_1", "角色"), newItem("aitem_2", "武器"))

	fixture.advance(t)
	first := fixture.item(t, "aitem_1")
	// The turn ended in error with a failed Task: without the step-cap port this
	// would fail the item as internal and pause the run for a failure.
	fixture.chat.finishTurn(first.SessionID, turnError)
	fixture.chat.failTask(first.SessionID, generation.FailureInternal, false, false)
	fixture.stepCaps.all = true

	fixture.advance(t)

	first = fixture.item(t, "aitem_1")
	if first.State != assetlist.StateWorking || first.Block != assetlist.BlockStepCap {
		t.Fatalf("item = %s/%q; want working blocked at step_cap", first.State, first.Block)
	}
	if first.StateReason != "" {
		t.Fatalf("state reason = %q; a step-cap block is not a failure", first.StateReason)
	}
	second := fixture.item(t, "aitem_2")
	if second.State != assetlist.StateWorking || second.SessionID == "" || second.Round != 1 {
		t.Fatalf("second item = %+v; want claimed and turning while the first waits at the cap", second)
	}
	if run := fixture.runState(t); run.Status != assetlist.RunRunning {
		t.Fatalf("run = %s/%s; want running because another item can still work",
			run.Status, run.PauseReason)
	}
	if len(fixture.scheduler.calls) != 2 {
		t.Fatalf("schedules = %d; want the blocked item's turn plus the next item's turn",
			len(fixture.scheduler.calls))
	}
	if fixture.stepCaps.calls != 1 {
		t.Fatalf("step-cap reads = %d; want one at the terminal turn", fixture.stepCaps.calls)
	}
}

// #330 §6: once every remaining item waits at the cap the run pauses at
// step_cap; the block itself is never a failure.
func TestAdvancePausesOnlyWhenEveryRemainingItemIsAtTheStepCap(t *testing.T) {
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
	for _, item := range []*assetlist.Item{&first, &second} {
		fixture.chat.sessions[item.SessionID] = chat.Session{
			ID: item.SessionID, OwnerID: testOwner, ProjectID: testProject,
		}
		fixture.chat.seedTerminalTurn(item.SessionID, turnDone)
		turn, found := fixture.chat.latest(item.SessionID)
		if !found {
			t.Fatal("seeded turn missing")
		}
		fixture.runs.item(item.ID).LastTurnMessageID = turn.AssistantMessageID
	}
	fixture.stepCaps.reached["ses_1"] = true

	fixture.advance(t)

	if fixture.runs.item("aitem_1").Block != assetlist.BlockStepCap {
		t.Fatalf("first block = %q; want step_cap", fixture.runs.item("aitem_1").Block)
	}
	// The second item is not capped: it opens its next round and the run keeps
	// running even though the first item is blocked.
	if fixture.runs.item("aitem_2").Block != assetlist.BlockNone {
		t.Fatalf("second block = %q; want none", fixture.runs.item("aitem_2").Block)
	}
	if run := fixture.runState(t); run.Status != assetlist.RunRunning {
		t.Fatalf("run = %s/%s; want running while the second item works",
			run.Status, run.PauseReason)
	}

	// The second item reaches the cap of its own asset: nothing can progress
	// any more, so the run pauses at the cap.
	fixture.chat.finishTurn("ses_2", turnDone)
	fixture.stepCaps.reached["ses_2"] = true
	fixture.advance(t)

	if fixture.runs.item("aitem_2").Block != assetlist.BlockStepCap {
		t.Fatalf("second block = %q; want step_cap", fixture.runs.item("aitem_2").Block)
	}
	if run := fixture.runState(t); run.Status != assetlist.RunPaused ||
		run.PauseReason != assetlist.PauseStepCap {
		t.Fatalf("run = %s/%s; want paused/step_cap when every remaining item is blocked",
			run.Status, run.PauseReason)
	}
}

// #330 §6 with two parallel slots: blocking one item leaves the other item's
// turn in flight and the run running.
func TestAdvanceKeepsRunningWhileAParallelItemStillWorks(t *testing.T) {
	fixture := newFixture(t, 2, 3)
	fixture.seedRun(assetlist.RunRunning, newItem("aitem_1", "角色"), newItem("aitem_2", "武器"))

	fixture.advance(t)
	if fixture.runs.claims != 2 {
		t.Fatalf("claims = %d; want both slots used", fixture.runs.claims)
	}
	first := fixture.item(t, "aitem_1")
	fixture.chat.finishTurn(first.SessionID, turnError)
	fixture.chat.failTask(first.SessionID, generation.FailureInternal, false, false)
	fixture.stepCaps.reached[first.SessionID] = true

	fixture.advance(t)

	if item := fixture.item(t, "aitem_1"); item.Block != assetlist.BlockStepCap {
		t.Fatalf("first item block = %q; want step_cap", item.Block)
	}
	second := fixture.item(t, "aitem_2")
	if second.State != assetlist.StateWorking || second.Block != assetlist.BlockNone {
		t.Fatalf("second item = %s/%q; want its turn left in flight", second.State, second.Block)
	}
	if run := fixture.runState(t); run.Status != assetlist.RunRunning {
		t.Fatalf("run = %s/%s; want running while the other turn is in flight",
			run.Status, run.PauseReason)
	}

	// Both blocked: now the run pauses.
	fixture.chat.finishTurn(second.SessionID, turnDone)
	fixture.stepCaps.reached[second.SessionID] = true
	fixture.advance(t)
	if run := fixture.runState(t); run.Status != assetlist.RunPaused ||
		run.PauseReason != assetlist.PauseStepCap {
		t.Fatalf("run = %s/%s; want paused/step_cap", run.Status, run.PauseReason)
	}
}

// #330 §6: the pause ends when the last blocked item is skipped; the resumed
// tick settles the list as completed instead of leaving it paused forever.
func TestAdvanceResumesAndCompletesAfterTheLastBlockedItemIsSkipped(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	blocked := newItem("aitem_1", "角色")
	blocked.State = assetlist.StateWorking
	blocked.Round = 1
	blocked.Block = assetlist.BlockStepCap
	blocked.SessionID = "ses_1"
	fixture.seedRun(assetlist.RunPaused, blocked)
	fixture.runs.runs[0].PauseReason = assetlist.PauseStepCap
	fixture.chat.sessions["ses_1"] = chat.Session{ID: "ses_1", OwnerID: testOwner, ProjectID: testProject}
	fixture.chat.seedTerminalTurn("ses_1", turnDone)

	// A paused run is not advanced while the item is blocked.
	fixture.advance(t)
	if run := fixture.runState(t); run.Status != assetlist.RunPaused {
		t.Fatalf("run = %s; want the step-cap pause kept while the item is blocked", run.Status)
	}
	if len(fixture.scheduler.calls) != 0 {
		t.Fatalf("schedules = %d; want no turn for a blocked item", len(fixture.scheduler.calls))
	}

	// The person skips the item: the skip makes it terminal, and the runner's
	// next tick resumes and completes the run.
	fixture.runs.item("aitem_1").State = assetlist.StateSkipped
	fixture.advance(t)

	if run := fixture.runState(t); run.Status != assetlist.RunCompleted {
		t.Fatalf("run = %s/%s; want completed after the last item became terminal",
			run.Status, run.PauseReason)
	}
}

// #330 step 5C: a grant of more steps clears the item's cap block and resumes
// the run; the next tick must continue from a fresh turn instead of judging
// the already-capped turn again (which would re-block or fail it).
func TestAdvanceContinuesAfterTheStepCapBlockWasResolved(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	fixture.seedRun(assetlist.RunRunning, newItem("aitem_1", "角色"))

	fixture.advance(t)
	first := fixture.item(t, "aitem_1")
	fixture.chat.finishTurn(first.SessionID, turnError)
	fixture.chat.failTask(first.SessionID, generation.FailureInternal, false, false)
	fixture.stepCaps.all = true
	fixture.advance(t)
	if item := fixture.item(t, "aitem_1"); item.Block != assetlist.BlockStepCap || item.StepCapTurnMessageID == "" {
		t.Fatalf("capped item = %+v; want the block and its turn", item)
	}
	if run := fixture.runState(t); run.Status != assetlist.RunPaused ||
		run.PauseReason != assetlist.PauseStepCap {
		t.Fatalf("run = %s/%s; want paused/step_cap with no other item to run",
			run.Status, run.PauseReason)
	}

	// The person grants more steps: the item's block is cleared (the service's
	// write, #330 step 5C) and the runner resumes the pause that nothing waits
	// for any more.
	if _, err := fixture.runs.SetItemBlock(context.Background(), testOwner, testList,
		"aitem_1", assetlist.BlockNone, assetlist.VersionUnchecked, fixture.now); err != nil {
		t.Fatal(err)
	}
	fixture.stepCaps.all = false
	fixture.advance(t)

	item := fixture.item(t, "aitem_1")
	if item.State != assetlist.StateWorking || item.Round != 2 || item.WorkRound != 2 ||
		item.Block != assetlist.BlockNone {
		t.Fatalf("continued item = %+v; want the next round of the same work session", item)
	}
	if len(fixture.scheduler.calls) != 2 {
		t.Fatalf("schedules = %d; want the fresh turn after the grant", len(fixture.scheduler.calls))
	}
	if run := fixture.runState(t); run.Status != assetlist.RunRunning {
		t.Fatalf("run = %s; want running", run.Status)
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

func TestAdvanceDoesNotCountADeferredReportItemAsASlot(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	// #459: item A reported done while one of its formats is still converting.
	// The report is stored on the item and its next check is an observation
	// interval away, so it keeps working without holding the one parallel slot:
	// otherwise item B never opens a turn (default max_parallel_items = 1).
	deferred := newItem("aitem_1", "角色")
	deferred.State = assetlist.StateWorking
	deferred.Round = 1
	deferred.SessionID = "ses_1"
	deferred.DeliveryDeferred = true
	deferred.NextActionAt = fixture.now.Add(5 * time.Minute)
	fixture.chat.sessions["ses_1"] = chat.Session{
		ID: "ses_1", OwnerID: testOwner, ProjectID: testProject,
	}
	fixture.chat.seedTerminalTurn("ses_1", turnDone)
	fixture.seedRun(assetlist.RunRunning, deferred, newItem("aitem_2", "武器"))

	fixture.advance(t)

	if second := fixture.item(t, "aitem_2"); second.State != assetlist.StateWorking {
		t.Fatalf("second item = %s; want working because the deferred item releases the slot", second.State)
	}
	if calls := len(fixture.scheduler.calls); calls != 1 {
		t.Fatalf("schedules = %d; want only the claimed item's turn", calls)
	}
	// The deferred item keeps its state and its report: only its delivery
	// decides when it enters review.
	if first := fixture.item(t, "aitem_1"); first.State != assetlist.StateWorking ||
		!first.DeliveryDeferred || first.Block != assetlist.BlockNone {
		t.Fatalf("deferred item = %+v; want working, deferred, unblocked", first)
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

// #329f: a list whose items all reached a terminal state gets one summary
// message in the session the list was proposed in. It is a platform message, not
// a turn: it opens no card and spends no step.
func TestCompletedRunPostsTheListSummaryIntoTheListSession(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	fixture.lists.sourceSessionID = "ses_list"
	fixture.chat.sessions["ses_list"] = chat.Session{
		ID: "ses_list", OwnerID: testOwner, ProjectID: testProject,
	}
	used := map[string]int{"ses_1": 3, "ses_2": 2}
	fixture.buildRunner(func(config *assetlistrunner.Config) {
		config.Schema.Messages.RunSummary = "清单「{title}」已结束：通过 {accepted} 项，打回 {returned} 项，" +
			"失败 {failed} 项，跳过 {skipped} 项；共 {steps} 步。"
		config.Schema.Messages.RunSummaryItem = "· {name}：{state}，{steps} 步"
		config.Schema.Messages.RunSummaryItemFailed = "· {name}：失败，{steps} 步（{reason}）"
		config.Schema.Messages.RunSummaryReasonInternal = "平台内部错误"
		config.Schema.ItemStates = []catalog.AssetListStateSchema{
			{ID: assetlist.StateAccepted, Label: "已验收"},
			{ID: assetlist.StateFailed, Label: "失败"},
		}
		config.Steps = func(_ context.Context, _, sessionID string) (int, error) {
			return used[sessionID], nil
		}
	})
	accepted := newItem("aitem_1", "角色")
	accepted.State = assetlist.StateAccepted
	accepted.SessionID = "ses_1"
	// The item was sent back once before it was accepted: the summary counts
	// the person's returns, not a state no item can end in.
	accepted.ReturnCount = 1
	failed := newItem("aitem_2", "道具")
	failed.State = assetlist.StateFailed
	failed.StateReason = string(generation.CategoryInternal)
	failed.SessionID = "ses_2"
	fixture.seedRun(assetlist.RunRunning, accepted, failed)

	fixture.advance(t)

	if run := fixture.runState(t); run.Status != assetlist.RunCompleted {
		t.Fatalf("run = %s; want completed", run.Status)
	}
	if len(fixture.chat.messages) != 1 {
		t.Fatalf("messages = %d; want one list summary", len(fixture.chat.messages))
	}
	for _, message := range fixture.chat.messages {
		if message.SessionID != "ses_list" || message.Role != "assistant" {
			t.Fatalf("summary = %s/%s; want an assistant message of the list's session",
				message.SessionID, message.Role)
		}
		for _, want := range []string{
			"清单「List」已结束：通过 1 项，打回 1 项，失败 1 项，跳过 0 项；共 5 步。",
			"· 角色：已验收，3 步",
			"· 道具：失败，2 步（平台内部错误）",
		} {
			if !strings.Contains(message.Content, want) {
				t.Fatalf("summary = %q; want it to contain %q", message.Content, want)
			}
		}
	}
	if len(fixture.chat.continuations) != 0 {
		t.Fatalf("continuations = %d; want the summary to open no turn",
			len(fixture.chat.continuations))
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

// #459: a working item whose done report is stored waits for its delivery to
// settle instead of burning a turn. The loop keeps the item's state, and the
// stored report moves it into review as soon as every format is terminal.
func TestAdvanceLeavesADoneReportWaitingForItsDelivery459(t *testing.T) {
	fixture := newFixture(t, 1, 3)
	item := newItem("aitem_1", "角色")
	item.State = assetlist.StateWorking
	item.Round = 1
	item.SessionID = "ses_1"
	item.Result = json.RawMessage(`{"task_id":"task-1","assets":[]}`)
	fixture.seedRun(assetlist.RunRunning, item)
	fixture.chat.sessions["ses_1"] = chat.Session{
		ID: "ses_1", OwnerID: testOwner, ProjectID: testProject,
	}
	fixture.chat.seedTerminalTurn("ses_1", turnDone)
	turn, _ := fixture.chat.latest("ses_1")
	fixture.runs.item(item.ID).LastTurnMessageID = turn.AssistantMessageID
	fixture.chat.tasks["ses_1"] = []generation.Task{{
		ID: "task-1", State: generation.TaskSucceeded, CreatedAt: turn.CreatedAt,
		Resources: map[string]generation.ResourceState{
			"converted_model|converted.fbx|": {State: generation.ResourcePending},
		},
	}}

	fixture.advance(t)

	waited := fixture.item(t, "aitem_1")
	if waited.State != assetlist.StateWorking || waited.Round != 1 {
		t.Fatalf("item = %s round %d; want it kept in progress, not advanced",
			waited.State, waited.Round)
	}
	if len(fixture.runs.finishes) != 0 {
		t.Fatalf("the item was finished while its delivery runs: %+v", fixture.runs.finishes)
	}
	if len(fixture.chat.continuations) != 0 {
		t.Fatalf("another turn was opened for the waiting item: %d", len(fixture.chat.continuations))
	}

	// Once the slot is terminal the same tick moves the item into review.
	fixture.chat.tasks["ses_1"][0].Resources["converted_model|converted.fbx|"] =
		generation.ResourceState{State: generation.ResourceReady}
	fixture.advance(t)

	reviewed := fixture.item(t, "aitem_1")
	if reviewed.State != assetlist.StateReview {
		t.Fatalf("item = %s; want review after the formats settled", reviewed.State)
	}
	if len(fixture.runs.finishes) != 1 || fixture.runs.finishes[0].State != assetlist.StateReview {
		t.Fatalf("finishes = %+v", fixture.runs.finishes)
	}
}
