// Package assetlistrunner drives a confirmed production list (#328): it
// claims todo items, opens each item's sub-session and turns, continues a
// round while the item stays working, records failures and completes the run.
//
// The runner is an observer of the run/item state, not a controller. Pause,
// resume, stop and skip are written by the control service; the kernel's
// submission gate refuses new paid steps while a run is paused; this package
// only reacts to that stored state on its next tick. It never creates tasks
// itself and its turn start is the same server-side entry the application
// calls.
//
// One application instance owns one SQLite database (the existing
// single-instance assumption), so the loop takes no distributed lease. It runs
// one goroutine and no goroutine per item; MaxParallelItems bounds how many
// items may have work in flight at once.
package assetlistrunner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// Defaults apply when application.asset_list.execution omits a value. The
// application starts the runner only for an enabled execution section, which
// the Catalog validation fills; these keep a bare test configuration sane.
const (
	defaultPollInterval = 2 * time.Second
	defaultGrace        = 120 * time.Second
	defaultMaxParallel  = 1
	defaultMaxRounds    = 3

	// roundLimitReason is the item state reason of a working item that used
	// all its rounds (#328). It is not one of the five upstream failure
	// categories: the person may continue the item manually.
	roundLimitReason = "round_limit"

	// defaultContinuation is only used when a caller builds the runner without
	// the Catalog copy. The declared message always carries the four
	// placeholders.
	defaultContinuation = "继续制作清单项「{name}」（{description}），路线 {route}，这是第 {round} 轮。"
)

// Runs is the run store port. It is the narrow subset of assetlist.RunStore
// the loop needs; the SQLite adapter implements it directly.
type Runs interface {
	RunsByStatus(ctx context.Context, status string) ([]assetlist.Run, error)
	UpdateRunStatus(ctx context.Context, ownerID, listID, from, to, pauseReason string, at time.Time) (assetlist.Run, error)
	ClaimItem(ctx context.Context, ownerID, listID string, at time.Time) (assetlist.Item, bool, error)
	FinishItem(ctx context.Context, input assetlist.FinishItemInput) (assetlist.Item, error)
	SetItemBlock(ctx context.Context, ownerID, listID, itemID, block string, expectedVersion int64, at time.Time) (assetlist.Item, error)
	RecordItemTurn(ctx context.Context, ownerID, listID, itemID, assistantMessageID string, round int, expectedVersion int64, at time.Time) (assetlist.Item, error)
}

// Lists reads one list with its items. assetlist.Service implements it.
type Lists interface {
	Get(ctx context.Context, ownerID, projectID, listID string) (assetlist.List, error)
}

// Chat is the server-side item-turn entry of the chat service. Tasks is not
// part of that service: the application wires chat.AssetListTaskSource from
// generation.Module.List as Config.Tasks.
type Chat interface {
	CreateItemSession(ctx context.Context, input chat.CreateItemSessionInput) (chat.Session, error)
	StartItemTurn(ctx context.Context, input chat.StartItemTurnInput) (chat.Message, chat.TurnOptions, error)
	LatestTurn(ctx context.Context, ownerID, sessionID string) (chat.Turn, bool, error)
	// TurnStateTerminal reports whether a turn state is done or error.
	TurnStateTerminal(state string) bool
	// TurnEndedInError reports whether the turn recorded the Schema's error
	// reason.
	TurnEndedInError(turn chat.Turn) bool
}

// Scheduler hands a started turn to the chat orchestrator. A full queue or a
// rejected enqueue is retried on the next tick through the unrecorded turn
// reconciliation; chat.Orchestrator implements it.
type Scheduler interface {
	Schedule(ctx context.Context, turn chat.ProcessTurn) error
}

// Presence reports a project's unexpired heartbeat. The runner treats a nil
// port as no presence gate (a deployment without presence routes).
type Presence interface {
	Active(ctx context.Context, ownerID, projectID string, since time.Time) (bool, error)
}

// StepCaps reports whether the newest turn of an item's sub-session was
// refused a paid step because the asset used all its 3D steps (#328 §2.5).
// The runner asks before it continues or fails an item whose turn ended, so a
// step-cap refusal blocks the item instead of being read as an internal
// failure. A nil port means the deployment has no step store to observe.
type StepCaps interface {
	StepCapReached(ctx context.Context, ownerID, sessionID string) (bool, error)
}

// Config is the runner's ports and Schema values. Execution nil keeps the
// defaults above; the application only starts the runner when execution is
// declared and enabled.
type Config struct {
	Runs      Runs
	Lists     Lists
	Chat      Chat
	Tasks     chat.AssetListTaskSource
	Scheduler Scheduler
	Presence  Presence
	StepCaps  StepCaps
	Schema    catalog.AssetListSchema
	Clock     func() time.Time
	Logger    *slog.Logger
}

// failureStreak remembers the last item that failed with a continuable
// category, so two items in a row with the same cause pause the run instead of
// burning through the whole list.
type failureStreak struct {
	category string
	itemID   string
	at       time.Time
}

// deferredTurn is a turn whose enqueue failed. The orchestrator may finalize
// the turn as an error while rejecting it; remembering the assistant message
// keeps the next tick from mistaking that local failure for an internal item
// failure.
type deferredTurn struct {
	assistantMessageID string
}

// Runner is the single scheduling loop of one application instance.
type Runner struct {
	runs         Runs
	lists        Lists
	chat         Chat
	tasks        chat.AssetListTaskSource
	scheduler    Scheduler
	presence     Presence
	stepCaps     StepCaps
	interval     time.Duration
	maxParallel  int
	maxRounds    int
	grace        time.Duration
	continuation string
	clock        func() time.Time
	logger       *slog.Logger
	streaks      map[string]failureStreak
	deferred     map[string]deferredTurn
}

// New builds the loop. Callers must not run two instances over one database.
func New(config Config) *Runner {
	runner := &Runner{
		runs:         config.Runs,
		lists:        config.Lists,
		chat:         config.Chat,
		tasks:        config.Tasks,
		scheduler:    config.Scheduler,
		presence:     config.Presence,
		stepCaps:     config.StepCaps,
		interval:     defaultPollInterval,
		maxParallel:  defaultMaxParallel,
		maxRounds:    defaultMaxRounds,
		grace:        defaultGrace,
		continuation: defaultContinuation,
		clock:        config.Clock,
		logger:       config.Logger,
		streaks:      map[string]failureStreak{},
		deferred:     map[string]deferredTurn{},
	}
	if runner.clock == nil {
		runner.clock = time.Now
	}
	if runner.logger == nil {
		runner.logger = slog.Default()
	}
	if execution := config.Schema.Execution; execution != nil {
		if execution.PollIntervalMilliseconds > 0 {
			runner.interval = time.Duration(execution.PollIntervalMilliseconds) * time.Millisecond
		}
		if execution.MaxParallelItems > 0 {
			runner.maxParallel = execution.MaxParallelItems
		}
		if execution.MaxRoundsPerItem > 0 {
			runner.maxRounds = execution.MaxRoundsPerItem
		}
		if execution.Presence.GraceSeconds > 0 {
			runner.grace = time.Duration(execution.Presence.GraceSeconds) * time.Second
		}
		if message := strings.TrimSpace(execution.Continuation.Message); message != "" {
			runner.continuation = message
		}
	}
	return runner
}

// Run advances the loop until the context ends. A tick failure is logged and
// the next tick retries; it never stops the loop.
func (runner *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(runner.interval)
	defer ticker.Stop()
	for {
		if err := runner.Advance(ctx); err != nil && ctx.Err() == nil {
			runner.logger.Warn("asset list runner tick failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Advance runs one tick. It resumes presence pauses first, then advances every
// running list: presence gate, working items, claims and completion. One
// run's failure does not stop the other runs.
func (runner *Runner) Advance(ctx context.Context) error {
	now := runner.clock()
	var errs []error
	if err := runner.resumePresencePauses(ctx, now); err != nil {
		errs = append(errs, err)
	}
	running, err := runner.runs.RunsByStatus(ctx, assetlist.RunRunning)
	if err != nil {
		return errors.Join(append(errs, fmt.Errorf("read running asset list runs: %w", err))...)
	}
	for _, run := range running {
		if err := runner.advanceRun(ctx, run, now); err != nil {
			errs = append(errs, fmt.Errorf("asset list run %s: %w", run.ID, err))
		}
	}
	return errors.Join(errs...)
}

// resumePresencePauses brings back only presence pauses: user stays sticky,
// step_cap needs the blocking item resolved and failure waits for the person.
func (runner *Runner) resumePresencePauses(ctx context.Context, now time.Time) error {
	paused, err := runner.runs.RunsByStatus(ctx, assetlist.RunPaused)
	if err != nil {
		return fmt.Errorf("read paused asset list runs: %w", err)
	}
	var errs []error
	for _, run := range paused {
		if run.PauseReason != assetlist.PausePresence {
			continue
		}
		present, err := runner.present(ctx, run, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("read asset list presence for run %s: %w", run.ID, err))
			continue
		}
		if !present {
			continue
		}
		if _, err := runner.runs.UpdateRunStatus(ctx, run.OwnerID, run.ListID,
			assetlist.RunPaused, assetlist.RunRunning, "", now); err != nil &&
			!errors.Is(err, assetlist.ErrConflict) {
			errs = append(errs, fmt.Errorf("resume asset list run %s: %w", run.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (runner *Runner) advanceRun(ctx context.Context, run assetlist.Run, now time.Time) error {
	present, err := runner.present(ctx, run, now)
	if err != nil {
		return err
	}
	if !present {
		return runner.pauseRun(ctx, run, assetlist.PausePresence, now)
	}
	list, err := runner.lists.Get(ctx, run.OwnerID, run.ProjectID, run.ListID)
	if errors.Is(err, assetlist.ErrNotFound) {
		// The list (and with it the run) is gone.
		return nil
	}
	if err != nil {
		return err
	}
	runner.forgetStaleStreak(run.OwnerID, list.Items)
	runner.forgetDeferredTurns(list.Items)
	for _, item := range list.Items {
		if item.State != assetlist.StateWorking {
			continue
		}
		paused, err := runner.advanceItem(ctx, run, item, now)
		if err != nil {
			return err
		}
		if paused {
			// The run is no longer running; do not open another item's turn
			// from the same tick.
			return nil
		}
	}
	// Re-read after the item transitions so the parallel-slot count reflects
	// finished items and a completion report that landed during this tick.
	current, err := runner.lists.Get(ctx, run.OwnerID, run.ProjectID, run.ListID)
	if errors.Is(err, assetlist.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	slots := workingSlots(current.Items)
	claimed := false
	for slots < runner.maxParallel {
		item, ok, err := runner.runs.ClaimItem(ctx, run.OwnerID, run.ListID, now)
		if err != nil {
			if errors.Is(err, assetlist.ErrConflict) {
				break
			}
			return err
		}
		if !ok {
			break
		}
		claimed = true
		slots++
		if err := runner.startItem(ctx, run, item, now); err != nil {
			return err
		}
	}
	if claimed {
		return nil
	}
	return runner.completeIfDone(ctx, run, current.Items, now)
}

// advanceItem moves one working item: it waits for an active turn, records a
// finished one, classifies a failure, continues the next round or reaches the
// round limit. Errors that mean "another writer moved the item" are not
// failures of the tick; they end this item's work until the next read.
func (runner *Runner) advanceItem(ctx context.Context, run assetlist.Run, item assetlist.Item, now time.Time) (bool, error) {
	if item.Block != assetlist.BlockNone {
		// awaiting_approval waits for the person; step_cap is handled by the
		// gate that pauses the run.
		return false, nil
	}
	if !item.NextActionAt.IsZero() && item.NextActionAt.After(now) {
		return false, nil
	}
	sessionID := item.SessionID
	if sessionID == "" {
		session, err := runner.ensureSession(ctx, run, item)
		if err != nil || session.ID == "" {
			return false, err
		}
		sessionID = session.ID
	}
	turn, found, err := runner.chat.LatestTurn(ctx, run.OwnerID, sessionID)
	if err != nil {
		return false, err
	}
	if !found && item.SessionID != "" {
		// The bound session was deleted; CreateItemSession builds a new one and
		// rebinds the item.
		session, err := runner.ensureSession(ctx, run, item)
		if err != nil || session.ID == "" {
			return false, err
		}
		sessionID = session.ID
		turn, found, err = runner.chat.LatestTurn(ctx, run.OwnerID, sessionID)
		if err != nil {
			return false, err
		}
	}
	if found && !runner.chat.TurnStateTerminal(turn.State) {
		if item.LastTurnMessageID == turn.AssistantMessageID {
			return false, nil
		}
		// A turn row exists but was never recorded: the process stopped
		// between starting and scheduling it, or the queue was full. Scheduling
		// is idempotent by assistant message id; the round is recorded only
		// after the schedule succeeds.
		return false, runner.scheduleTurn(ctx, run, item, sessionID, turn, now)
	}
	if found {
		if deferred, ok := runner.deferred[item.ID]; ok &&
			deferred.assistantMessageID == turn.AssistantMessageID {
			// The enqueue failed and the orchestrator finalized the turn before
			// it ran. Open a fresh turn at the same round instead of failing
			// the item for a local scheduling failure.
			delete(runner.deferred, item.ID)
			round := item.Round
			if item.LastTurnMessageID != "" {
				round++
			}
			return false, runner.openTurn(ctx, run, item, sessionID, round, now)
		}
		if item.LastTurnMessageID != turn.AssistantMessageID {
			round := item.Round
			if item.LastTurnMessageID != "" {
				round++
			}
			if err := runner.recordTurn(ctx, run, item, turn.AssistantMessageID, round, now); err != nil {
				return false, err
			}
			item.Round = round
			item.LastTurnMessageID = turn.AssistantMessageID
		}
		// A turn refused at the owner's 3D step cap ended while the item still
		// works: block the item and pause the run instead of continuing the
		// round or classifying the refusal as a failure (#328 §2.5).
		capped, err := runner.stepCapReached(ctx, run, sessionID)
		if err != nil {
			return false, err
		}
		if capped {
			if err := runner.blockStepCap(ctx, run, item, now); err != nil {
				return false, err
			}
			// The run is paused now; stop the tick before it advances another
			// working item.
			return true, nil
		}
		report, failed, err := runner.failureReport(ctx, run.OwnerID, sessionID, turn)
		if err != nil {
			return false, err
		}
		if failed {
			return runner.failItem(ctx, run, item, report, now)
		}
		if item.Round >= runner.maxRounds {
			return false, runner.finishItem(ctx, run, item, assetlist.StateFailed, roundLimitReason, now)
		}
		return false, runner.openTurn(ctx, run, item, sessionID, item.Round+1, now)
	}
	// A claimed item with no turn yet: claim already counted round one.
	return false, runner.openTurn(ctx, run, item, sessionID, item.Round, now)
}

// startItem opens the first turn of a just-claimed item. ClaimItem already
// counted its round.
func (runner *Runner) startItem(ctx context.Context, run assetlist.Run, item assetlist.Item, now time.Time) error {
	sessionID := item.SessionID
	if sessionID == "" {
		session, err := runner.ensureSession(ctx, run, item)
		if err != nil {
			return err
		}
		if session.ID == "" {
			return nil
		}
		sessionID = session.ID
	}
	return runner.openTurn(ctx, run, item, sessionID, item.Round, now)
}

// ensureSession creates or reuses the item's sub-session. An item that is no
// longer startable resolves to an empty session: the next read sees its real
// state.
func (runner *Runner) ensureSession(ctx context.Context, run assetlist.Run, item assetlist.Item) (chat.Session, error) {
	session, err := runner.chat.CreateItemSession(ctx, chat.CreateItemSessionInput{
		OwnerID:   run.OwnerID,
		ProjectID: run.ProjectID,
		ListID:    run.ListID,
		ItemID:    item.ID,
	})
	switch {
	case errors.Is(err, chat.ErrAssetListItemNotStartable),
		errors.Is(err, chat.ErrAssetListItemNotFound):
		return chat.Session{}, nil
	case err != nil:
		return chat.Session{}, err
	}
	return session, nil
}

// openTurn starts one server-side item turn and schedules it. A turn-active
// or item-state refusal is left for the next tick; a schedule failure leaves
// the turn unrecorded so the next tick re-schedules the same assistant
// message.
func (runner *Runner) openTurn(
	ctx context.Context,
	run assetlist.Run,
	item assetlist.Item,
	sessionID string,
	round int,
	now time.Time,
) error {
	if round < 1 {
		// A manual "continue" resets round to zero before the item is reopened.
		round = 1
	}
	assistant, options, err := runner.chat.StartItemTurn(ctx, chat.StartItemTurnInput{
		OwnerID:      run.OwnerID,
		SessionID:    sessionID,
		ProjectID:    run.ProjectID,
		ListID:       run.ListID,
		ItemID:       item.ID,
		Continuation: runner.continuationText(item, round),
	})
	if err != nil {
		if transientTurnError(err) {
			runner.logger.Warn("asset list runner: item turn not started yet",
				"run_id", run.ID, "item_id", item.ID, "error", err)
			return nil
		}
		return err
	}
	command := chat.ProcessTurn{
		OwnerID:            run.OwnerID,
		SessionID:          sessionID,
		AssistantMessageID: assistant.ID,
		Options:            options,
	}
	if err := runner.scheduler.Schedule(ctx, command); err != nil {
		runner.deferred[item.ID] = deferredTurn{assistantMessageID: assistant.ID}
		runner.logger.Warn("asset list runner: item turn schedule deferred",
			"run_id", run.ID, "item_id", item.ID,
			"assistant_message_id", assistant.ID, "error", err)
		return nil
	}
	delete(runner.deferred, item.ID)
	return runner.recordTurn(ctx, run, item, assistant.ID, round, now)
}

// scheduleTurn re-schedules an observed turn whose round was never recorded.
func (runner *Runner) scheduleTurn(
	ctx context.Context,
	run assetlist.Run,
	item assetlist.Item,
	sessionID string,
	turn chat.Turn,
	now time.Time,
) error {
	command := chat.ProcessTurn{
		OwnerID:            run.OwnerID,
		SessionID:          sessionID,
		AssistantMessageID: turn.AssistantMessageID,
		Options:            turn.Options,
	}
	if err := runner.scheduler.Schedule(ctx, command); err != nil {
		runner.deferred[item.ID] = deferredTurn{assistantMessageID: turn.AssistantMessageID}
		runner.logger.Warn("asset list runner: item turn schedule deferred",
			"run_id", run.ID, "item_id", item.ID,
			"assistant_message_id", turn.AssistantMessageID, "error", err)
		return nil
	}
	delete(runner.deferred, item.ID)
	round := item.Round
	if item.LastTurnMessageID != "" {
		round++
	}
	return runner.recordTurn(ctx, run, item, turn.AssistantMessageID, round, now)
}

// recordTurn stores the round of a scheduled turn. An item that a completion
// report or a control action moved meanwhile keeps its turn: an in-flight turn
// is allowed to finish.
func (runner *Runner) recordTurn(
	ctx context.Context,
	run assetlist.Run,
	item assetlist.Item,
	assistantMessageID string,
	round int,
	now time.Time,
) error {
	_, err := runner.runs.RecordItemTurn(ctx, run.OwnerID, run.ListID, item.ID,
		assistantMessageID, round, assetlist.VersionUnchecked, now)
	if errors.Is(err, assetlist.ErrItemState) || errors.Is(err, assetlist.ErrConflict) ||
		errors.Is(err, assetlist.ErrNotFound) {
		return nil
	}
	return err
}

// failureReport classifies the latest turn. A failed Task created during the
// turn supplies the category; a success later in the same turn means the Agent
// recovered, so the turn continues. A turn that ended in error without a
// failed Task is an internal failure.
func (runner *Runner) failureReport(
	ctx context.Context,
	ownerID, sessionID string,
	turn chat.Turn,
) (generation.FailureReport, bool, error) {
	failed, found, err := runner.failedTaskInTurn(ctx, ownerID, sessionID, turn)
	if err != nil {
		return generation.FailureReport{}, false, err
	}
	if found {
		if failed.Failure != nil {
			return generation.ClassifyFailure(failed.Failure.Class,
				failed.Failure.Retryable, failed.Failure.Ambiguous), true, nil
		}
		return generation.ClassifyFailure("", false, false), true, nil
	}
	if runner.chat.TurnEndedInError(turn) {
		return generation.ClassifyFailure("", false, false), true, nil
	}
	return generation.FailureReport{}, false, nil
}

// failedTaskInTurn returns the newest failed Task created by this turn, unless
// a later successful Task in the same turn shows the Agent recovered.
func (runner *Runner) failedTaskInTurn(
	ctx context.Context,
	ownerID, sessionID string,
	turn chat.Turn,
) (generation.Task, bool, error) {
	if runner.tasks == nil {
		return generation.Task{}, false, nil
	}
	tasks, err := runner.tasks(ctx, ownerID, sessionID)
	if err != nil {
		return generation.Task{}, false, err
	}
	var failed, succeeded generation.Task
	var hasFailed, hasSucceeded bool
	for _, task := range tasks {
		if task.CreatedAt.Before(turn.CreatedAt) {
			continue
		}
		switch task.State {
		case generation.TaskFailed:
			if !hasFailed || newerTask(task, failed) {
				failed, hasFailed = task, true
			}
		case generation.TaskSucceeded:
			if !hasSucceeded || newerTask(task, succeeded) {
				succeeded, hasSucceeded = task, true
			}
		}
	}
	if !hasFailed {
		return generation.Task{}, false, nil
	}
	if hasSucceeded && newerTask(succeeded, failed) {
		return generation.Task{}, false, nil
	}
	return failed, true, nil
}

func newerTask(candidate, current generation.Task) bool {
	if candidate.CreatedAt.Equal(current.CreatedAt) {
		return candidate.ID > current.ID
	}
	return candidate.CreatedAt.After(current.CreatedAt)
}

// failItem records the failure and decides whether the run can continue. A
// refused/internal failure or an operation whose outcome the person must
// decide pauses the run. Input/parameter failures continue with the next item,
// but two items in a row with the same cause pause the run. Capacity failures
// never count towards that streak.
func (runner *Runner) failItem(
	ctx context.Context,
	run assetlist.Run,
	item assetlist.Item,
	report generation.FailureReport,
	now time.Time,
) (bool, error) {
	if err := runner.finishItem(ctx, run, item, assetlist.StateFailed,
		string(report.Category), now); err != nil {
		return false, err
	}
	category := string(report.Category)
	if report.UserDecisionRequired ||
		report.Category == generation.CategoryUpstreamRefused ||
		report.Category == generation.CategoryInternal {
		delete(runner.streaks, run.OwnerID)
		return true, runner.pauseRun(ctx, run, assetlist.PauseFailure, now)
	}
	if report.Category == generation.CategoryUpstreamCapacity {
		delete(runner.streaks, run.OwnerID)
		return false, nil
	}
	if streak, ok := runner.streaks[run.OwnerID]; ok &&
		streak.category == category && streak.itemID != item.ID {
		delete(runner.streaks, run.OwnerID)
		return true, runner.pauseRun(ctx, run, assetlist.PauseFailure, now)
	}
	runner.streaks[run.OwnerID] = failureStreak{
		category: category, itemID: item.ID, at: now,
	}
	return false, nil
}

func (runner *Runner) finishItem(
	ctx context.Context,
	run assetlist.Run,
	item assetlist.Item,
	state, reason string,
	now time.Time,
) error {
	_, err := runner.runs.FinishItem(ctx, assetlist.FinishItemInput{
		OwnerID:     run.OwnerID,
		ListID:      run.ListID,
		ItemID:      item.ID,
		State:       state,
		StateReason: reason,
		At:          now,
	})
	if errors.Is(err, assetlist.ErrItemState) || errors.Is(err, assetlist.ErrConflict) ||
		errors.Is(err, assetlist.ErrNotFound) {
		return nil
	}
	return err
}

func (runner *Runner) pauseRun(ctx context.Context, run assetlist.Run, reason string, now time.Time) error {
	_, err := runner.runs.UpdateRunStatus(ctx, run.OwnerID, run.ListID,
		assetlist.RunRunning, assetlist.RunPaused, reason, now)
	if errors.Is(err, assetlist.ErrConflict) || errors.Is(err, assetlist.ErrNotFound) {
		return nil
	}
	return err
}

// stepCapReached asks the step store whether the item's newest turn was
// refused at the cap. Without a store there is nothing to observe.
func (runner *Runner) stepCapReached(ctx context.Context, run assetlist.Run, sessionID string) (bool, error) {
	if runner.stepCaps == nil {
		return false, nil
	}
	return runner.stepCaps.StepCapReached(ctx, run.OwnerID, sessionID)
}

// blockStepCap marks the item on its second axis and pauses the run. The item
// stays working: the person resolves the cap (or the item) and the control
// service clears the block before the runner advances it again.
func (runner *Runner) blockStepCap(ctx context.Context, run assetlist.Run, item assetlist.Item, now time.Time) error {
	_, err := runner.runs.SetItemBlock(ctx, run.OwnerID, run.ListID, item.ID,
		assetlist.BlockStepCap, assetlist.VersionUnchecked, now)
	if err != nil && !errors.Is(err, assetlist.ErrConflict) &&
		!errors.Is(err, assetlist.ErrItemState) && !errors.Is(err, assetlist.ErrNotFound) {
		return err
	}
	runner.logger.Info("asset list item blocked at the step cap",
		"run_id", run.ID, "item_id", item.ID)
	return runner.pauseRun(ctx, run, assetlist.PauseStepCap, now)
}

// completeIfDone finishes a running list when nothing is left to make. A
// review or returned item blocks completion: the list waits for #329 to
// resolve it, and the run keeps its per-owner running slot.
func (runner *Runner) completeIfDone(
	ctx context.Context,
	run assetlist.Run,
	items []assetlist.Item,
	now time.Time,
) error {
	for _, item := range items {
		switch item.State {
		case assetlist.StateTodo, assetlist.StateWorking,
			assetlist.StateReview, assetlist.StateReturned:
			return nil
		}
	}
	_, err := runner.runs.UpdateRunStatus(ctx, run.OwnerID, run.ListID,
		assetlist.RunRunning, assetlist.RunCompleted, "", now)
	if errors.Is(err, assetlist.ErrConflict) || errors.Is(err, assetlist.ErrNotFound) {
		return nil
	}
	return err
}

func (runner *Runner) present(ctx context.Context, run assetlist.Run, now time.Time) (bool, error) {
	if runner.presence == nil {
		return true, nil
	}
	return runner.presence.Active(ctx, run.OwnerID, run.ProjectID, now.Add(-runner.grace))
}

// forgetStaleStreak drops a remembered failure when a reviewed item was
// updated after it: a success broke the consecutive-failure sequence.
func (runner *Runner) forgetStaleStreak(ownerID string, items []assetlist.Item) {
	streak, ok := runner.streaks[ownerID]
	if !ok {
		return
	}
	for _, item := range items {
		if (item.State == assetlist.StateReview || item.State == assetlist.StateAccepted) &&
			!item.UpdatedAt.Before(streak.at) {
			delete(runner.streaks, ownerID)
			return
		}
	}
}

// forgetDeferredTurns drops schedule retries of items that left the working
// state: nothing will observe their turn again.
func (runner *Runner) forgetDeferredTurns(items []assetlist.Item) {
	if len(runner.deferred) == 0 {
		return
	}
	working := make(map[string]bool, len(items))
	for _, item := range items {
		if item.State == assetlist.StateWorking {
			working[item.ID] = true
		}
	}
	for itemID := range runner.deferred {
		if !working[itemID] {
			delete(runner.deferred, itemID)
		}
	}
}

func workingSlots(items []assetlist.Item) int {
	slots := 0
	for _, item := range items {
		// An item waiting for an approval card releases its parallel slot.
		if item.State == assetlist.StateWorking && item.Block != assetlist.BlockAwaitingApproval {
			slots++
		}
	}
	return slots
}

// continuationText fills the Catalog copy. The route placeholder uses the
// declared route id and falls back to the capability chain when an item has
// only a route snapshot.
func (runner *Runner) continuationText(item assetlist.Item, round int) string {
	route := item.RouteID
	if route == "" {
		steps := make([]string, 0, len(item.Route))
		for _, step := range item.Route {
			steps = append(steps, step.Capability)
		}
		route = strings.Join(steps, " → ")
	}
	return strings.NewReplacer(
		"{name}", item.Name,
		"{description}", item.Description,
		"{route}", route,
		"{round}", strconv.Itoa(round),
	).Replace(runner.continuation)
}

func transientTurnError(err error) bool {
	return errors.Is(err, chat.ErrTurnActive) ||
		errors.Is(err, chat.ErrAssetListItemNotWorking) ||
		errors.Is(err, chat.ErrAssetListItemNotFound) ||
		errors.Is(err, chat.ErrAssetListItemSessionMismatch) ||
		errors.Is(err, chat.ErrAssetListItemProjectMismatch) ||
		errors.Is(err, chat.ErrSessionNotFound)
}
