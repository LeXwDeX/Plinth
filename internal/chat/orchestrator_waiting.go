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
	for {
		if err := orchestrator.ScanWaitingTasks(ctx); err != nil && ctx.Err() == nil {
			orchestrator.logger.Warn("等待任务扫描失败", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
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
