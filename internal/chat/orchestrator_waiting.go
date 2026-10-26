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
		if turn.Checkpoint.WaitingTaskID == "" || turn.Checkpoint.WaitingCallID == "" || orchestrator.taskGet == nil {
			return ErrTurnCheckpointConflict
		}
		task, err := orchestrator.taskGet(ctx, turn.OwnerID, turn.Checkpoint.WaitingTaskID)
		if err == nil && (task.OwnerID != generation.OwnerID(turn.OwnerID) || task.SessionID != turn.SessionID ||
			task.ID != generation.TaskID(turn.Checkpoint.WaitingTaskID)) {
			return ErrTurnCheckpointConflict
		}
		if turn.Checkpoint.WaitDeadline.IsZero() {
			// Repair checkpoints written by an older failing observer. Anchor
			// the deadline to the durable wait write so repeated scans cannot
			// extend it while a Task is unreadable.
			timeout := time.Duration(orchestrator.schema.Agent.RequestTimeoutSeconds) * time.Second
			if err == nil && task.Offering.Timeout > 0 {
				timeout = task.Offering.Timeout
			}
			turn.Checkpoint.WaitDeadline = turn.UpdatedAt.Add(timeout)
			version, saveErr := orchestrator.service.SaveTurnCheckpoint(ctx, turn, turn.State, turn.Checkpoint, false)
			if errors.Is(saveErr, ErrTurnCheckpointConflict) {
				return nil
			}
			if saveErr != nil {
				return saveErr
			}
			turn.CheckpointVersion = version
		}
		if err != nil {
			// An unreadable or deleted Task must not suppress the durable wait
			// deadline. Resume the same call so it can report a bounded failure.
			if turn.Checkpoint.WaitDeadline.IsZero() || time.Now().Before(turn.Checkpoint.WaitDeadline) {
				return err
			}
			ready = true
		} else {
			ready = task.State == generation.TaskSucceeded || task.State == generation.TaskFailed ||
				task.State == generation.TaskCanceled || !turn.Checkpoint.WaitDeadline.IsZero() &&
				!time.Now().Before(turn.Checkpoint.WaitDeadline)
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
