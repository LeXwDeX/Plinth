package chat

import (
	"context"
	"errors"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

const waitingScanPageSize = 64
const waitingScanInterval = 250 * time.Millisecond

// ScanWaitingTasks performs one bounded-page sweep. The database retains
// waiting/queued work when the in-memory queue is full or a Task read fails.
func (orchestrator *Orchestrator) ScanWaitingTasks(ctx context.Context) error {
	var cursor TurnPageCursor
	for {
		page, err := orchestrator.service.ListWakeCandidates(ctx, cursor, waitingScanPageSize)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		for _, turn := range page {
			if err := orchestrator.scanTurn(ctx, turn); err != nil {
				orchestrator.logger.Warn("扫描等待任务失败，保留持久轮次供重试",
					"assistant_message_id", turn.AssistantMessageID, "error", err)
			}
		}
		last := page[len(page)-1]
		cursor = TurnPageCursor{CreatedAt: last.CreatedAt, AssistantMessageID: last.AssistantMessageID}
		if len(page) < waitingScanPageSize {
			return nil
		}
	}
}

func (orchestrator *Orchestrator) scanTurn(ctx context.Context, turn Turn) error {
	command := ProcessTurn{OwnerID: turn.OwnerID, SessionID: turn.SessionID,
		AssistantMessageID: turn.AssistantMessageID, Options: turn.Options}
	if turn.State == orchestrator.service.TurnQueuedState() {
		if err := orchestrator.Enqueue(command); err != nil && !errors.Is(err, ErrOrchestratorQueueFull) {
			return err
		}
		return nil
	}
	if turn.State != orchestrator.service.TurnWaitingTaskState() {
		return nil
	}
	ready := turn.StopRequested
	if !ready {
		var err error
		ready, err = orchestrator.waitingReady(ctx, &turn)
		if err != nil {
			return err
		}
	}
	if !ready {
		return nil
	}
	_, err := orchestrator.service.SaveTurnCheckpoint(ctx, turn,
		orchestrator.service.TurnQueuedState(), turn.Checkpoint, true)
	if errors.Is(err, ErrTurnCheckpointConflict) {
		return nil
	} // another scanner won
	if err != nil {
		return err
	}
	if err := orchestrator.Enqueue(command); err != nil && !errors.Is(err, ErrOrchestratorQueueFull) {
		return err
	}
	return nil
}

func (orchestrator *Orchestrator) runWaitingScanner(ctx context.Context) {
	ticker := time.NewTicker(waitingScanInterval)
	defer ticker.Stop()
	// Running turns are read on the slower consent-wait cadence: they carry
	// whole checkpoints, and a stranded turn is rare (#516).
	// The first pass waits one interval, after the restart's RecoverTurns.
	reclaimEvery := orchestrator.consentWaitTimeout()
	nextReclaim := time.Now().Add(reclaimEvery)
	for {
		if err := orchestrator.ScanWaitingTasks(ctx); err != nil && ctx.Err() == nil {
			orchestrator.logger.Warn("等待任务扫描失败", "error", err)
		}
		if now := time.Now(); !now.Before(nextReclaim) {
			nextReclaim = now.Add(reclaimEvery)
			if err := orchestrator.ReclaimStrandedTurns(ctx); err != nil && ctx.Err() == nil {
				orchestrator.logger.Warn("回收无人持有的运行中回合失败", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ReclaimStrandedTurns enqueues again the running turns nothing in this
// process holds (#516). A turn is running from the moment a worker marks it
// until Process ends it, and Process can return before its ending is
// installed (a failed read of the session's messages or of the turn) or fail
// to write that ending; such a turn would wait for the next restart's
// RecoverTurns. A turn is held while its command is queued or being processed
// (the queued set) and while it is suspended on a consent wait or an
// unconfirmed paid call (consentWaits). Only a turn found unheld on two
// passes in a row is enqueued, which leaves out the moment a timer pops a wait
// and wakes the turn itself; the queued mark is taken only then, and the wait
// is checked again under it, so a Process that ended meanwhile has registered
// its wait first. The worker reads the turn again before dispatching it, the
// session checks of the restart recovery apply (a session being deleted is
// skipped, one that is gone has its turn discarded, #384/#430), and the
// dispatch is the restart's: Process resumes from the stored checkpoint.
func (orchestrator *Orchestrator) ReclaimStrandedTurns(ctx context.Context) error {
	turns, err := orchestrator.service.RunningTurns(ctx)
	if err != nil {
		return err
	}
	orchestrator.strandedMu.Lock()
	defer orchestrator.strandedMu.Unlock()
	previous, unheld := orchestrator.stranded, map[string]struct{}{}
	defer func() { orchestrator.stranded = unheld }()
	for _, turn := range turns {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		id := turn.AssistantMessageID
		if orchestrator.service.sessionDeleting(turn.OwnerID, turn.SessionID) {
			continue
		}
		if orchestrator.isQueued(id) || orchestrator.consentWaiting(turn.OwnerID, turn.SessionID) {
			continue
		}
		if _, seen := previous[id]; !seen {
			unheld[id] = struct{}{}
			continue
		}
		if !orchestrator.markQueued(id) {
			continue
		}
		if orchestrator.consentWaiting(turn.OwnerID, turn.SessionID) {
			orchestrator.unmarkQueued(id)
			continue
		}
		command := ProcessTurn{OwnerID: turn.OwnerID, SessionID: turn.SessionID,
			AssistantMessageID: id, Options: turn.Options}
		if orchestrator.sessionGone(ctx, turn.OwnerID, turn.SessionID) {
			orchestrator.unmarkQueued(id)
			orchestrator.discardOrphanTurn(ctx, command)
			continue
		}
		select {
		case orchestrator.queue <- command:
			orchestrator.logger.Warn("运行中回合无人持有，已重新入队",
				"session_id", turn.SessionID, "assistant_message_id", id)
		default:
			// The queue is full: try again on the next pass.
			orchestrator.unmarkQueued(id)
			unheld[id] = struct{}{}
		}
	}
	return nil
}

// waitingReady reports whether a waiting turn can resume: every task in
// flight is terminal or past its own deadline, or a submission queued for a
// retry is due (the retry then runs next to the tasks still in flight, which
// simply wait again). Entries are those of the checkpoint's Waiting list; the
// single-call format of older checkpoints reads as a list of one.
func (orchestrator *Orchestrator) waitingReady(ctx context.Context, turn *Turn) (bool, error) {
	entries := turn.Checkpoint.WaitingCalls()
	if len(entries) == 0 || orchestrator.taskGet == nil {
		return false, ErrTurnCheckpointConflict
	}
	now := time.Now()
	allReady, queuedDue, tasks := true, false, 0
	var firstErr error
	repaired := false
	for index := range entries {
		entry := &entries[index]
		if entry.CallID == "" {
			return false, ErrTurnCheckpointConflict
		}
		if entry.TaskID == "" {
			// A submission queued for a retry holds no Task yet.
			if entry.RetryAt.IsZero() && entry.Deadline.IsZero() {
				return false, ErrTurnCheckpointConflict
			}
			if !now.Before(entry.RetryAt) || !entry.Deadline.IsZero() && !now.Before(entry.Deadline) {
				queuedDue = true
			}
			continue
		}
		tasks++
		task, err := orchestrator.taskGet(ctx, turn.OwnerID, entry.TaskID)
		if err == nil && (task.OwnerID != generation.OwnerID(turn.OwnerID) || task.SessionID != turn.SessionID ||
			task.ID != generation.TaskID(entry.TaskID)) {
			return false, ErrTurnCheckpointConflict
		}
		if entry.Deadline.IsZero() {
			// Repair checkpoints written by an older failing observer. Anchor
			// the deadline to the durable wait write so repeated scans cannot
			// extend it while a Task is unreadable.
			timeout := time.Duration(orchestrator.schema.Agent.RequestTimeoutSeconds) * time.Second
			if err == nil && task.Offering.Timeout > 0 {
				timeout = task.Offering.Timeout + task.Offering.UpstreamQueueTimeout
			}
			entry.Deadline = turn.UpdatedAt.Add(timeout)
			repaired = true
		}
		pastDeadline := !now.Before(entry.Deadline)
		switch {
		case err != nil:
			// An unreadable or deleted Task must not suppress the durable wait
			// deadline. Resume the same call so it can report a bounded failure.
			if !pastDeadline {
				firstErr = errors.Join(firstErr, err)
				allReady = false
			}
		case task.State == generation.TaskSucceeded || task.State == generation.TaskFailed ||
			task.State == generation.TaskCanceled || pastDeadline:
		default:
			allReady = false
		}
	}
	if repaired {
		turn.Checkpoint.SetWaiting(entries)
		version, saveErr := orchestrator.service.SaveTurnCheckpoint(ctx, *turn, turn.State, turn.Checkpoint, false)
		if errors.Is(saveErr, ErrTurnCheckpointConflict) {
			return false, nil
		}
		if saveErr != nil {
			return false, saveErr
		}
		turn.CheckpointVersion = version
	}
	if queuedDue || allReady && tasks > 0 {
		return true, nil
	}
	return false, firstErr
}

// consentWaiting reports whether the session's turn is suspended on a wait of
// this process (a consent decision or an unconfirmed paid call).
func (orchestrator *Orchestrator) consentWaiting(ownerID, sessionID string) bool {
	orchestrator.suspendMu.Lock()
	defer orchestrator.suspendMu.Unlock()
	_, waiting := orchestrator.consentWaits[orchestrator.turnKey(ownerID, sessionID)]
	return waiting
}
