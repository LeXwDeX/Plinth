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
	// categories. failed is terminal: there is no continue or retry entry for
	// the item, so the copy around it must not promise one (#508).
	roundLimitReason = "round_limit"

	// defaultContinuation is only used when a caller builds the runner without
	// the Catalog copy. The declared message always carries the four
	// placeholders.
	defaultContinuation = "继续制作清单项「{name}」（{description}），路线 {route}，这是第 {round} 轮。完成后调用 submit_asset_list_item 报告。"

	// defaultRework is the returned-item turn when the Catalog declares no
	// rework copy (#329b).
	defaultRework = "清单项「{name}」（{description}）被打回，这是第 {round} 轮重做。打回原因：{reasons}。说明：{note}。请按原因修正，新的任务成功后调用 submit_asset_list_item 重新申报。"
)

// Runs is the run store port. It is the narrow subset of assetlist.RunStore
// the loop needs; the SQLite adapter implements it directly.
type Runs interface {
	RunsByStatus(ctx context.Context, status string) ([]assetlist.Run, error)
	UpdateRunStatus(ctx context.Context, ownerID, listID, from, to, pauseReason string, at time.Time) (assetlist.Run, error)
	ClaimItem(ctx context.Context, ownerID, listID string, at time.Time) (assetlist.Item, bool, error)
	FinishItem(ctx context.Context, input assetlist.FinishItemInput) (assetlist.Item, error)
	SetItemBlock(ctx context.Context, ownerID, listID, itemID, block string, expectedVersion int64, at time.Time) (assetlist.Item, error)
	SetItemStepCapTurn(ctx context.Context, ownerID, listID, itemID, assistantMessageID string, expectedVersion int64, at time.Time) (assetlist.Item, error)
	RecordItemTurn(ctx context.Context, ownerID, listID, itemID, assistantMessageID string, round, workRound int, expectedVersion int64, at time.Time) (assetlist.Item, error)
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
	// PostAssistantMessage writes a platform message into an existing session
	// without a turn: the list-end summary (#329f), which is neither a card nor
	// a step.
	PostAssistantMessage(ctx context.Context, ownerID string, message chat.Message) (chat.Message, bool, error)
	// TurnStateTerminal reports whether a turn state is done or error.
	TurnStateTerminal(state string) bool
	// TurnEndedInError reports whether the turn recorded the Schema's error
	// reason.
	TurnEndedInError(turn chat.Turn) bool
	// TurnEndedAwaitingApproval reports whether the turn ended on an approval
	// card the person has to decide (#508).
	TurnEndedAwaitingApproval(turn chat.Turn) bool
	// SettleReportedItem moves a working item whose done report is stored into
	// review once its delivery is stable (#459). It reports false when the item
	// has no such report, and true when it finished the item or left it waiting
	// for its delivery.
	SettleReportedItem(ctx context.Context, input chat.SettleReportedItemInput) (assetlist.Item, bool, error)
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

// Approvals reports whether an item's sub-session still has an approval card
// that waits for the person (#508). The runner blocks such an item as
// awaiting_approval instead of continuing it, and clears the block once the
// card was decided or expired. A nil port means the deployment observes no
// cards, and a turn that ended on one continues like any other.
type Approvals interface {
	PendingApproval(ctx context.Context, ownerID, sessionID string) (bool, error)
}

// Backfill stores an idle run for confirmed lists that have none (#508):
// confirm creates the run best-effort, and a list confirmed while automatic
// production was off never got one. assetlist.Service implements it. A nil
// port means the deployment backfills nothing.
type Backfill interface {
	BackfillIdleRuns(ctx context.Context) (int, error)
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
	Backfill  Backfill
	Approvals Approvals
	// Steps reads how many steps one item's asset actually used (#330): the
	// list-end summary reports it per item. nil leaves the summary without a
	// per-item number rather than guessing one.
	Steps func(ctx context.Context, ownerID, sessionID string) (int, error)
	// Texts gives the list-end summary the copy of the UI locale the list's
	// session last used. Nil keeps the Schema's default-locale copy.
	Texts  *catalog.TextLocalizer
	Schema catalog.AssetListSchema
	Clock  func() time.Time
	Logger *slog.Logger
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
	backfill     Backfill
	approvals    Approvals
	steps        func(ctx context.Context, ownerID, sessionID string) (int, error)
	texts        *catalog.TextLocalizer
	interval     time.Duration
	maxParallel  int
	maxRounds    int
	grace        time.Duration
	continuation string
	// rework is the continuation of a returned item (#329b); rework falls
	// back to the default when the Catalog has no review section.
	rework string
	// schema is the declared application.asset_list: the state labels and the
	// list-end summary copy (#329f). New keeps the Config's value whole.
	schema   catalog.AssetListSchema
	clock    func() time.Time
	logger   *slog.Logger
	streaks  map[string]failureStreak
	deferred map[string]deferredTurn
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
		backfill:     config.Backfill,
		approvals:    config.Approvals,
		steps:        config.Steps,
		texts:        config.Texts,
		interval:     defaultPollInterval,
		maxParallel:  defaultMaxParallel,
		maxRounds:    defaultMaxRounds,
		grace:        defaultGrace,
		continuation: defaultContinuation,
		rework:       defaultRework,
		schema:       config.Schema,
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
	if review := config.Schema.Review; review != nil {
		if message := strings.TrimSpace(review.Messages.ReworkMessage); message != "" {
			runner.rework = message
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
	// A confirmed list without a run gets an idle one (#508), so the card
	// offers the person's start. It never starts on its own.
	if runner.backfill != nil {
		if _, err := runner.backfill.BackfillIdleRuns(ctx); err != nil {
			errs = append(errs, fmt.Errorf("backfill asset list runs: %w", err))
		}
	}
	if err := runner.resumePresencePauses(ctx, now); err != nil {
		errs = append(errs, err)
	}
	// A step-cap pause ends as soon as no item is blocked at the cap any more
	// (#330 §6): the blocked item was extended, skipped or reported. Resuming
	// before the running scan lets the same tick settle (and possibly complete)
	// the list instead of waiting one more interval.
	if err := runner.resumeStepCapPauses(ctx, now); err != nil {
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
// step_cap is handled by resumeStepCapPauses and failure waits for the person.
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

// resumeStepCapPauses ends a pause the runner set when every remaining item
// waited at the 3D step cap (#330 §6). The runner pauses only while an item is
// blocked, so a pause whose last blocked item was extended, skipped or
// reported has nothing left to wait for; it continues and the tick settles the
// list. No presence means no resume: the presence gate owns that pause reason.
func (runner *Runner) resumeStepCapPauses(ctx context.Context, now time.Time) error {
	paused, err := runner.runs.RunsByStatus(ctx, assetlist.RunPaused)
	if err != nil {
		return fmt.Errorf("read paused asset list runs: %w", err)
	}
	var errs []error
	for _, run := range paused {
		if run.PauseReason != assetlist.PauseStepCap {
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
		list, err := runner.lists.Get(ctx, run.OwnerID, run.ProjectID, run.ListID)
		if errors.Is(err, assetlist.ErrNotFound) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("read asset list %s: %w", run.ListID, err))
			continue
		}
		if stepCapBlocked(list.Items) {
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
	// A blocked item (awaiting_approval, step_cap) releases its slot, so a
	// cap-blocked item does not stop the scheduler from claiming another one
	// (#330 §6).
	current, err := runner.lists.Get(ctx, run.OwnerID, run.ProjectID, run.ListID)
	if errors.Is(err, assetlist.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	slots := workingSlots(current.Items)
	claimed := false
	stale := false
	for slots < runner.maxParallel {
		item, ok, err := runner.runs.ClaimItem(ctx, run.OwnerID, run.ListID, now)
		if err != nil {
			if errors.Is(err, assetlist.ErrConflict) {
				// Another writer moved an item between the read and the claim;
				// the next tick judges from a fresh read.
				stale = true
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
	if claimed || stale {
		return nil
	}
	return runner.settleRun(ctx, run, current.Items, now)
}

// advanceItem moves one working item: it waits for an active turn, records a
// finished one, classifies a failure, continues the next round or reaches the
// round limit. Errors that mean "another writer moved the item" are not
// failures of the tick; they end this item's work until the next read.
//
// The second result is true only when the runner paused the run (a failure
// that needs the person). A step-cap block no longer pauses here: the item is
// left blocked and the tick keeps working on the other items.
func (runner *Runner) advanceItem(ctx context.Context, run assetlist.Run, item assetlist.Item, now time.Time) (bool, error) {
	if item.Block == assetlist.BlockAwaitingApproval && runner.approvals != nil {
		// The card is the person's (#508): while it waits, the item opens no
		// turn. Once it was decided or expired the block clears and the newest
		// turn is judged again below; a card that no longer waits does not
		// block it a second time. Without the port nothing can tell the card
		// was decided, so the block stays.
		waiting, err := runner.approvalPending(ctx, run.OwnerID, item.SessionID)
		if err != nil || waiting {
			return false, err
		}
		cleared, err := runner.runs.SetItemBlock(ctx, run.OwnerID, run.ListID, item.ID,
			assetlist.BlockNone, assetlist.VersionUnchecked, now)
		if err != nil {
			if errors.Is(err, assetlist.ErrConflict) || errors.Is(err, assetlist.ErrItemState) ||
				errors.Is(err, assetlist.ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		item = cleared
	}
	if item.Block != assetlist.BlockNone {
		// step_cap waits for the person to extend, skip or report the item (and
		// awaiting_approval without the approvals port has nothing to observe);
		// neither may open another turn.
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
			round, workRound := runner.nextRounds(item)
			return false, runner.openTurn(ctx, run, item, sessionID, round, workRound, now)
		}
		if item.LastTurnMessageID != turn.AssistantMessageID {
			round, workRound := runner.nextRounds(item)
			if err := runner.recordTurn(ctx, run, item, turn.AssistantMessageID, round, workRound, now); err != nil {
				return false, err
			}
			item.Round = round
			item.WorkRound = workRound
			item.LastTurnMessageID = turn.AssistantMessageID
		}
		// A turn already handled as a step-cap block (#330 step 5C): the refusal
		// stays in that turn's grant audit, so after the person granted more
		// steps (or resumed) the runner continues from a fresh turn instead of
		// re-judging the same capped turn. Evaluating it again would either
		// re-block or misreport the refusal as a failure.
		if item.StepCapTurnMessageID != "" && item.StepCapTurnMessageID == turn.AssistantMessageID {
			round, workRound := runner.nextRounds(item)
			return false, runner.openTurn(ctx, run, item, sessionID, round, workRound, now)
		}
		// A turn refused at the owner's 3D step cap ended while the item still
		// works: block the item and keep the tick going instead of classifying
		// the refusal as a failure (#330 §6). The blocked item releases its
		// parallel slot; the run pauses only when nothing else can progress.
		capped, err := runner.stepCapReached(ctx, run, sessionID)
		if err != nil {
			return false, err
		}
		if capped {
			return false, runner.blockStepCap(ctx, run, item, turn.AssistantMessageID, now)
		}
		// A turn that ended on an approval card waits for the person (#508):
		// continuing it would only open turns that meet the same card until
		// the round limit fails the item. The block releases the slot.
		if runner.chat.TurnEndedAwaitingApproval(turn) {
			waiting, err := runner.approvalPending(ctx, run.OwnerID, sessionID)
			if err != nil {
				return false, err
			}
			if waiting {
				return false, runner.blockAwaitingApproval(ctx, run, item, now)
			}
		}
		report, failed, err := runner.failureReport(ctx, run.OwnerID, sessionID, turn)
		if err != nil {
			return false, err
		}
		if failed {
			return runner.failItem(ctx, run, item, report, now)
		}
		// A working item that already reported done waits for its delivery to
		// settle (#459): the report is stored on the item, so the loop neither
		// opens another turn nor spends a round, and the item moves into review
		// as soon as every post-success format is terminal. Only format slots
		// hold it: a background slot that maps to no format must not.
		finished, err := runner.finishReportedItem(ctx, run, item, now)
		if err != nil || finished {
			return false, err
		}
		// max_rounds_per_item bounds the turns of one work session. Reading
		// the item's whole-life round here would fail a returned item after a
		// single turn when its first make used the allowance (#329b).
		if item.WorkRound >= runner.maxRounds {
			return false, runner.finishItem(ctx, run, item, assetlist.StateFailed, roundLimitReason, now)
		}
		return false, runner.openTurn(ctx, run, item, sessionID, item.Round+1, item.WorkRound+1, now)
	}
	// A claimed item with no turn yet: claim already counted the session's first
	// round.
	return false, runner.openTurn(ctx, run, item, sessionID, max(item.Round, 1), max(item.WorkRound, 1), now)
}

// nextRounds is the round pair of a turn the server is about to record: one
// beyond the stored numbers once an earlier turn of this session was recorded,
// or the stored round itself for a turn that was started but never recorded.
func (runner *Runner) nextRounds(item assetlist.Item) (round, workRound int) {
	round = max(item.Round, 1)
	workRound = max(item.WorkRound, 1)
	if item.LastTurnMessageID != "" {
		round++
		workRound++
	}
	return round, workRound
}

// startItem opens the first turn of a just-claimed item. ClaimItem already
// counted the session's first round.
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
	return runner.openTurn(ctx, run, item, sessionID, max(item.Round, 1), max(item.WorkRound, 1), now)
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
	round, workRound int,
	now time.Time,
) error {
	if round < 1 {
		// A stored round below one (an item claimed before its first turn was
		// counted) still opens the first turn.
		round = 1
	}
	if workRound < 1 {
		workRound = 1
	}
	assistant, options, err := runner.chat.StartItemTurn(ctx, chat.StartItemTurnInput{
		OwnerID:      run.OwnerID,
		SessionID:    sessionID,
		ProjectID:    run.ProjectID,
		ListID:       run.ListID,
		ItemID:       item.ID,
		Continuation: runner.continuationText(item, workRound),
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
	return runner.recordTurn(ctx, run, item, assistant.ID, round, workRound, now)
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
	round, workRound := runner.nextRounds(item)
	return runner.recordTurn(ctx, run, item, turn.AssistantMessageID, round, workRound, now)
}

// recordTurn stores the rounds of a scheduled turn. An item that a completion
// report or a control action moved meanwhile keeps its turn: an in-flight turn
// is allowed to finish.
func (runner *Runner) recordTurn(
	ctx context.Context,
	run assetlist.Run,
	item assetlist.Item,
	assistantMessageID string,
	round, workRound int,
	now time.Time,
) error {
	_, err := runner.runs.RecordItemTurn(ctx, run.OwnerID, run.ListID, item.ID,
		assistantMessageID, round, workRound, assetlist.VersionUnchecked, now)
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
	tasks, err := runner.tasks(ctx, ownerID, sessionID, "")
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

// finishReportedItem moves a working item whose done report is stored into
// review once its delivery is stable (#459). It reports true when the item was
// such a report, whether the item was finished or is still waiting: the loop
// must not open another turn for it in either case. A waiting item's next
// scheduling instant is one post-success observation interval ahead (capped by
// the Catalog), so the check runs at that cadence rather than every tick.
func (runner *Runner) finishReportedItem(
	ctx context.Context,
	run assetlist.Run,
	item assetlist.Item,
	now time.Time,
) (bool, error) {
	if runner.chat == nil {
		return false, nil
	}
	if runner.tasks == nil {
		return false, nil
	}
	// Read the reported Task by id: an item's sub-session can create more
	// terminal Tasks than one page holds, and a report that cannot be found
	// would open another (paid) turn for the item (#473).
	tasks, err := runner.tasks(ctx, run.OwnerID, item.SessionID,
		generation.TaskID(chat.AssetListItemResultTaskID(item.Result)))
	if err != nil {
		// Without the session's Tasks the report cannot be judged: leave the
		// item to the ordinary turn path instead of guessing.
		return false, err
	}
	list, err := runner.lists.Get(ctx, run.OwnerID, run.ProjectID, run.ListID)
	if errors.Is(err, assetlist.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, handled, err := runner.chat.SettleReportedItem(ctx, chat.SettleReportedItemInput{
		OwnerID:   run.OwnerID,
		ProjectID: run.ProjectID,
		ListID:    run.ListID,
		Item:      item,
		Brief:     list.Brief,
		Tasks:     tasks,
		Now:       now,
	})
	if err != nil {
		return false, err
	}
	return handled, nil
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

// blockStepCap marks the item on its second axis and leaves the run running
// (#330 §6). The item stays working: the person resolves the cap (extends,
// skips, or lets the Agent report) and the control service clears the block
// before the runner advances it again. The capped turn is recorded first, so
// the person's grant continues from a fresh turn. A blocked item does not hold
// a parallel slot; the run pauses at the cap only when settleRun finds nothing
// else to do.
func (runner *Runner) blockStepCap(ctx context.Context, run assetlist.Run, item assetlist.Item, assistantMessageID string, now time.Time) error {
	if assistantMessageID != "" {
		if _, err := runner.runs.SetItemStepCapTurn(ctx, run.OwnerID, run.ListID, item.ID,
			assistantMessageID, assetlist.VersionUnchecked, now); err != nil &&
			!errors.Is(err, assetlist.ErrConflict) && !errors.Is(err, assetlist.ErrItemState) &&
			!errors.Is(err, assetlist.ErrNotFound) {
			return err
		}
	}
	_, err := runner.runs.SetItemBlock(ctx, run.OwnerID, run.ListID, item.ID,
		assetlist.BlockStepCap, assetlist.VersionUnchecked, now)
	if err != nil && !errors.Is(err, assetlist.ErrConflict) &&
		!errors.Is(err, assetlist.ErrItemState) && !errors.Is(err, assetlist.ErrNotFound) {
		return err
	}
	runner.logger.Info("asset list item blocked at the step cap",
		"run_id", run.ID, "item_id", item.ID)
	return nil
}

// approvalPending asks whether the sub-session still has an undecided approval
// card. Without the port there is nothing to observe.
func (runner *Runner) approvalPending(ctx context.Context, ownerID, sessionID string) (bool, error) {
	if runner.approvals == nil || sessionID == "" {
		return false, nil
	}
	return runner.approvals.PendingApproval(ctx, ownerID, sessionID)
}

// blockAwaitingApproval marks a working item as waiting for the person's
// approval card (#508). Another writer that moved the item meanwhile wins.
func (runner *Runner) blockAwaitingApproval(ctx context.Context, run assetlist.Run, item assetlist.Item, now time.Time) error {
	_, err := runner.runs.SetItemBlock(ctx, run.OwnerID, run.ListID, item.ID,
		assetlist.BlockAwaitingApproval, assetlist.VersionUnchecked, now)
	if err != nil && !errors.Is(err, assetlist.ErrConflict) &&
		!errors.Is(err, assetlist.ErrItemState) && !errors.Is(err, assetlist.ErrNotFound) {
		return err
	}
	runner.logger.Info("asset list item waits for an approval card",
		"run_id", run.ID, "item_id", item.ID)
	return nil
}

// runDrain is what a tick with no claim left can do for one list.
type runDrain int

const (
	// drainWaiting means something outside this tick will move the list: a
	// turn in flight, an approval card the person is deciding, a review the
	// person has to accept, a return to redo or an item scheduled for later.
	// The run stays running.
	drainWaiting runDrain = iota
	// drainComplete means every item reached a terminal state.
	drainComplete
	// drainStepCap means no item can advance without the person resolving a
	// 3D step cap, and at least one item is blocked at it.
	drainStepCap
)

// drainOf classifies a list with nothing claimable. An item blocked at the cap
// is not "waiting": its turn does not continue by itself. Any other unfinished
// item outranks the block, because the runner or the person can still move it
// without touching the cap.
func drainOf(items []assetlist.Item) runDrain {
	blocked := false
	pending := false
	for _, item := range items {
		switch item.State {
		case assetlist.StateTodo, assetlist.StateReturned:
			return drainWaiting
		case assetlist.StateWorking:
			pending = true
			if item.Block == assetlist.BlockStepCap {
				blocked = true
				continue
			}
			return drainWaiting
		case assetlist.StateReview:
			pending = true
		}
	}
	if !pending {
		return drainComplete
	}
	if blocked {
		return drainStepCap
	}
	return drainWaiting
}

// stepCapBlocked reports whether any item still waits at the 3D step cap.
func stepCapBlocked(items []assetlist.Item) bool {
	for _, item := range items {
		if item.State == assetlist.StateWorking && item.Block == assetlist.BlockStepCap {
			return true
		}
	}
	return false
}

// settleRun decides the run's status after a tick that claimed nothing. A
// list whose items are all terminal completes; a list whose remaining work
// sits behind step-cap blocks pauses at the cap; every other unfinished state
// keeps the run running. Review items alone keep waiting as before (#329), and
// they do not hide a cap pause: nothing automatic would happen either way.
//
// A completing list gets its summary first (#329f): the message is idempotent
// by its id, so a status write that fails is retried on the next tick without
// posting twice.
func (runner *Runner) settleRun(
	ctx context.Context,
	run assetlist.Run,
	items []assetlist.Item,
	now time.Time,
) error {
	switch drainOf(items) {
	case drainComplete:
		if err := runner.postRunSummary(ctx, run); err != nil {
			return err
		}
		_, err := runner.runs.UpdateRunStatus(ctx, run.OwnerID, run.ListID,
			assetlist.RunRunning, assetlist.RunCompleted, "", now)
		if errors.Is(err, assetlist.ErrConflict) || errors.Is(err, assetlist.ErrNotFound) {
			return nil
		}
		return err
	case drainStepCap:
		return runner.pauseRun(ctx, run, assetlist.PauseStepCap, now)
	}
	return nil
}

// runSummaryMessageID is the summary's message id. The run is unique, so the
// id makes a repeated tick post the same message once.
func runSummaryMessageID(runID string) string { return "asset_list_summary_" + runID }

// postRunSummary writes the list-end summary into the list's session (#329f).
// It is a platform message, not an Agent turn: no card, no step and no model
// call. A list without a session (or one whose session is gone) gets no
// summary, which must not stop the run from completing.
func (runner *Runner) postRunSummary(ctx context.Context, run assetlist.Run) error {
	list, err := runner.lists.Get(ctx, run.OwnerID, run.ProjectID, run.ListID)
	if errors.Is(err, assetlist.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	sessionID := strings.TrimSpace(list.SourceSessionID)
	if sessionID == "" {
		return nil
	}
	content := runner.runSummaryText(ctx, run.OwnerID,
		runner.sessionLocale(ctx, run.OwnerID, sessionID), list)
	if content == "" {
		return nil
	}
	_, _, err = runner.chat.PostAssistantMessage(ctx, run.OwnerID, chat.Message{
		ID:        runSummaryMessageID(run.ID),
		SessionID: sessionID,
		Content:   content,
	})
	if errors.Is(err, chat.ErrSessionNotFound) {
		// The session is gone; there is nobody to read the summary. Completing
		// the run must not depend on it.
		runner.logger.Info("asset list summary not posted: session is gone",
			"run_id", run.ID, "session_id", sessionID)
		return nil
	}
	return err
}

// runSummaryText renders the summary from the Catalog: the head line with the
// counts and the total steps, then one line per item in position order. The
// copy comes from the Schema in the session's locale, so the loop never holds a
// second version of the product text. An empty declared head line means there
// is nothing to post.
func (runner *Runner) runSummaryText(
	ctx context.Context,
	ownerID, locale string,
	list assetlist.List,
) string {
	messages := runner.schema.Messages
	head := runner.text(locale, messages.RunSummary)
	if strings.TrimSpace(head) == "" {
		return ""
	}
	counts := map[string]int{}
	returned := 0
	steps := 0
	lines := make([]string, 0, len(list.Items)+1)
	for _, item := range list.Items {
		counts[item.State]++
		// A returned item is accepted or failed by now: what the person sees
		// as "sent back" is the items that were returned at least once, not a
		// state the list can still be in when it ends.
		if item.ReturnCount > 0 {
			returned++
		}
		used := runner.itemSteps(ctx, ownerID, item)
		steps += used
		lines = append(lines, runner.summaryItemLine(locale, item, used))
	}
	head = strings.NewReplacer(
		"{title}", list.Title,
		"{accepted}", strconv.Itoa(counts[assetlist.StateAccepted]),
		"{returned}", strconv.Itoa(returned),
		"{failed}", strconv.Itoa(counts[assetlist.StateFailed]),
		"{skipped}", strconv.Itoa(counts[assetlist.StateSkipped]),
		"{steps}", strconv.Itoa(steps),
	).Replace(head)
	return head + "\n" + strings.Join(lines, "\n")
}

// summaryItemLine is one item's line: its name, its state's label and what the
// item's asset used. A failed item names the declared reason of its stored
// state_reason instead of only the state.
func (runner *Runner) summaryItemLine(locale string, item assetlist.Item, used int) string {
	messages := runner.schema.Messages
	if item.State == assetlist.StateFailed {
		line := runner.text(locale, messages.RunSummaryItemFailed)
		return strings.NewReplacer(
			"{name}", item.Name,
			"{steps}", strconv.Itoa(used),
			"{reason}", runner.summaryReason(locale, item.StateReason),
		).Replace(line)
	}
	line := runner.text(locale, messages.RunSummaryItem)
	return strings.NewReplacer(
		"{name}", item.Name,
		"{state}", runner.summaryStateLabel(item.State),
		"{steps}", strconv.Itoa(used),
	).Replace(line)
}

// summaryReason words a stored state_reason: one of the five upstream failure
// categories (#251) or the runner's own round limit. A reason with no declared
// copy falls back to the state's own label, never to a hardcoded sentence.
func (runner *Runner) summaryReason(locale, stateReason string) string {
	messages := runner.schema.Messages
	declared := map[string]string{
		string(generation.CategoryInputInvalid):     messages.RunSummaryReasonInputInvalid,
		string(generation.CategoryParameterInvalid): messages.RunSummaryReasonParameterInvalid,
		string(generation.CategoryUpstreamCapacity): messages.RunSummaryReasonUpstreamCapacity,
		string(generation.CategoryUpstreamRefused):  messages.RunSummaryReasonUpstreamRefused,
		string(generation.CategoryInternal):         messages.RunSummaryReasonInternal,
		roundLimitReason:                            messages.RunSummaryReasonRoundLimit,
	}
	if reason := declared[stateReason]; reason != "" {
		return runner.text(locale, reason)
	}
	// An empty reason (a failure recorded before categories existed) still
	// reads as a failure: name the state instead of printing an empty bracket.
	if strings.TrimSpace(stateReason) == "" {
		return runner.summaryStateLabel(assetlist.StateFailed)
	}
	return stateReason
}

// summaryStateLabel is the Schema's label of one item state. An unknown state
// falls back to the stored id, which is what the state vocabulary calls it.
func (runner *Runner) summaryStateLabel(state string) string {
	for _, declared := range runner.schema.ItemStates {
		if declared.ID == state {
			return declared.Label
		}
	}
	return state
}

// itemSteps is what one item's asset actually used (#330). An item without a
// sub-session, a deployment without the step store and a failed read leave it
// at zero: the summary counts what it can prove.
func (runner *Runner) itemSteps(ctx context.Context, ownerID string, item assetlist.Item) int {
	if runner.steps == nil || item.SessionID == "" {
		return 0
	}
	used, err := runner.steps(ctx, ownerID, item.SessionID)
	if err != nil {
		runner.logger.Warn("asset list item step standing not readable for the summary",
			"item_id", item.ID, "error", err)
		return 0
	}
	return used
}

// text is one Schema string in the session's locale. Nil localizer or an
// undeclared text keep the default-locale copy.
func (runner *Runner) text(locale, text string) string {
	if runner.texts == nil {
		return text
	}
	return runner.texts.Text(locale, text)
}

// sessionLocale is the UI locale the list's session last used: its newest turn
// froze it. A session without a turn keeps the default locale, like every other
// background write.
func (runner *Runner) sessionLocale(ctx context.Context, ownerID, sessionID string) string {
	turn, found, err := runner.chat.LatestTurn(ctx, ownerID, sessionID)
	if err != nil || !found {
		return ""
	}
	return turn.Options.Locale
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

// workingSlots counts the items that hold a parallel slot. An item waiting for
// an approval card or blocked at the 3D step cap releases its slot (#328,
// #330 §6): the person can resolve it later, and the scheduler is free to
// claim another item meanwhile. An item whose done report is stored while its
// delivery is still converting releases it too (#459): the delivery tick
// advances that item on its own schedule (its next check is one observation
// interval ahead), so without this a max_parallel_items = 1 list would claim
// nothing until the delivery settled.
func workingSlots(items []assetlist.Item) int {
	slots := 0
	for _, item := range items {
		if item.State != assetlist.StateWorking {
			continue
		}
		if item.Block == assetlist.BlockAwaitingApproval || item.Block == assetlist.BlockStepCap {
			continue
		}
		if item.DeliveryDeferred {
			continue
		}
		slots++
	}
	return slots
}

// continuationText fills the Catalog copy. The route placeholder uses the
// declared route id and falls back to the capability chain when an item has
// only a route snapshot. workRound is the turn of the current work session.
func (runner *Runner) continuationText(item assetlist.Item, workRound int) string {
	route := item.RouteID
	if route == "" {
		steps := make([]string, 0, len(item.Route))
		for _, step := range item.Route {
			steps = append(steps, step.Capability)
		}
		route = strings.Join(steps, " → ")
	}
	if item.ReturnReason != "" {
		// A claimed returned item carries its return context (#329b): the
		// next turn of the same sub-session quotes it word for word. {round}
		// here is the rework ordinal (which rework this is), not the turn of
		// the rework session: return_count is that number.
		return strings.NewReplacer(
			"{name}", item.Name,
			"{description}", item.Description,
			"{route}", route,
			"{round}", strconv.Itoa(max(item.ReturnCount, 1)),
			"{reasons}", item.ReturnReason,
			"{note}", item.ReturnNote,
		).Replace(runner.rework)
	}
	return strings.NewReplacer(
		"{name}", item.Name,
		"{description}", item.Description,
		"{route}", route,
		"{round}", strconv.Itoa(max(workRound, 1)),
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
