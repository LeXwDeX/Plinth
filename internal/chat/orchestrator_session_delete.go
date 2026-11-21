package chat

import (
	"context"
	"errors"
	"time"
)

// sessionQuiesceTimeout bounds how long deleting a session waits for its
// running turn to leave. A turn normally ends within milliseconds of its
// context being canceled; the bound only keeps a stuck call from holding the
// deletion, and the turn then ends by itself once it notices the session is
// gone (turnAbandoned).
const sessionQuiesceTimeout = 10 * time.Second

// QuiesceSession implements SessionQuiescer: it interrupts the running turn of
// the session and waits for it to leave, and drops the registration of a turn
// parked for consent. Public Tasks the turn already submitted are not
// touched; they belong to the generation kernel and carry on without their
// session.
func (orchestrator *Orchestrator) QuiesceSession(ctx context.Context, ownerID, sessionID string) {
	orchestrator.popConsentWait(ownerID, sessionID)
	key := orchestrator.turnKey(ownerID, sessionID)
	orchestrator.activeMu.Lock()
	cancel, done, assistantID := orchestrator.activeCancel[key], orchestrator.activeDone[key], orchestrator.activeAssistant[key]
	orchestrator.activeMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	deadline := time.Now().Add(sessionQuiesceTimeout)
	timer := time.NewTimer(sessionQuiesceTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		orchestrator.logger.Warn("删除会话时等待轮次退出超时，轮次将在发现会话已删除后自行结束",
			"session_id", sessionID)
		return
	case <-ctx.Done():
		return
	}
	// The worker forgets a turn right after Process returns, and a resume that
	// enqueued the turn before then would be dropped as a duplicate of it.
	for orchestrator.queuedMarked(assistantID) && time.Now().Before(deadline) && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
}

// queuedMarked reports whether a worker still holds the turn: it was enqueued
// and has not been unmarked since.
func (orchestrator *Orchestrator) queuedMarked(assistantMessageID string) bool {
	orchestrator.queueMu.Lock()
	defer orchestrator.queueMu.Unlock()
	_, marked := orchestrator.queued[assistantMessageID]
	return marked
}

// ResumeSession implements SessionQuiescer: the deletion did not happen, so
// the turns it interrupted go on from their persisted state, the way
// RecoverTurns resumes them after a restart. A turn that is parked on its
// Tasks was never interrupted and is woken by the waiting scanner as usual.
func (orchestrator *Orchestrator) ResumeSession(ctx context.Context, ownerID, sessionID string) {
	turns, err := orchestrator.service.repository.ListSessionTurns(ctx, ownerID, sessionID,
		[]string{orchestrator.service.turnQueued, orchestrator.service.turnRunning})
	if err != nil {
		orchestrator.logger.Warn("删除会话失败后无法读取会话轮次，等待重启恢复",
			"session_id", sessionID, "error", err)
		return
	}
	for _, turn := range turns {
		orchestrator.resumeTurn(ctx, turn)
	}
}

// resumeEnqueueAttempts bounds how long ResumeSession waits for a worker to
// release the turn it just ran. The turn that noticed the deletion mark and
// returned may stay in the queue's duplicate guard for a moment; that is not a
// duplicate to drop, so the wake path is used and retried instead.
const (
	resumeEnqueueAttempts = 100
	resumeEnqueueInterval = 2 * time.Millisecond
)

// resumeTurn puts one interrupted turn back in the queue. A full queue would
// otherwise leave a running row waiting for the next restart, because the
// waiting scanner only looks at waiting_task and queued turns (#430); the
// running row is returned to the queued state the scanner owns and picks up.
func (orchestrator *Orchestrator) resumeTurn(ctx context.Context, turn Turn) {
	command := ProcessTurn{OwnerID: turn.OwnerID, SessionID: turn.SessionID,
		AssistantMessageID: turn.AssistantMessageID, Options: turn.Options}
	for attempt := 0; attempt < resumeEnqueueAttempts; attempt++ {
		err := orchestrator.enqueueForWake(command)
		if err == nil {
			return
		}
		if errors.Is(err, ErrWakeNotReleased) {
			time.Sleep(resumeEnqueueInterval)
			continue
		}
		orchestrator.returnResumedTurnToScanner(ctx, turn, err)
		return
	}
	orchestrator.logger.Warn("删除会话失败后轮次仍由原 worker 持有，未重复入队",
		"session_id", turn.SessionID, "assistant_message_id", turn.AssistantMessageID)
}

// returnResumedTurnToScanner handles an enqueue that failed on a full queue. A
// queued row is already visible to the waiting scanner and keeps its state; a
// running row would only be recovered by a restart, so it is put back in the
// queued state whose next scan re-enqueues it.
func (orchestrator *Orchestrator) returnResumedTurnToScanner(ctx context.Context, turn Turn, cause error) {
	if turn.State != orchestrator.service.TurnRunningState() {
		orchestrator.logger.Warn("删除会话失败后轮次入队暂失败，保留 queued 状态等待扫描器重试",
			"session_id", turn.SessionID, "assistant_message_id", turn.AssistantMessageID, "error", cause)
		return
	}
	_, err := orchestrator.service.SaveTurnCheckpoint(ctx, turn,
		orchestrator.service.TurnQueuedState(), turn.Checkpoint, true)
	switch {
	case err == nil:
		orchestrator.logger.Info("删除会话失败后队列已满，轮次改回 queued 等待扫描器重新入队",
			"session_id", turn.SessionID, "assistant_message_id", turn.AssistantMessageID)
	case errors.Is(err, ErrTurnCheckpointConflict):
		// Another writer owns the turn now (for instance the worker that was
		// still winding down); it is not ours to move back into the queue.
	default:
		orchestrator.logger.Warn("删除会话失败后无法把轮次改回 queued，等待重启恢复",
			"session_id", turn.SessionID, "assistant_message_id", turn.AssistantMessageID, "error", err)
	}
}

// turnAbandoned reports whether the turn's session or its assistant message
// is being deleted or already gone: the turn has nothing left to write and
// nothing to report. Process asks it where a failure would otherwise be
// logged or recorded as the turn's own.
func (orchestrator *Orchestrator) turnAbandoned(ctx context.Context, command ProcessTurn) bool {
	if orchestrator.service.sessionDeleting(command.OwnerID, command.SessionID) {
		return true
	}
	// The turn row goes with its session and with its assistant message (both
	// foreign keys cascade), so a missing row means either was deleted.
	_, err := orchestrator.service.GetTurn(context.WithoutCancel(ctx),
		command.OwnerID, command.SessionID, command.AssistantMessageID)
	return errors.Is(err, ErrMessageNotFound)
}

// discardOrphanTurn ends the turn of a session that is gone, so that no
// recovery or scan offers it again. Normally the turn row is deleted with its
// session; one left behind (a database that once ran without foreign key
// enforcement) is finished as stopped.
func (orchestrator *Orchestrator) discardOrphanTurn(ctx context.Context, command ProcessTurn) {
	orchestrator.logger.Info("跳过已删除会话的轮次",
		"session_id", command.SessionID, "assistant_message_id", command.AssistantMessageID)
	if err := orchestrator.service.FinishTurnWithReason(
		context.WithoutCancel(ctx), command.AssistantMessageID, TurnEndStopped,
	); err != nil && !errors.Is(err, ErrMessageNotFound) {
		orchestrator.logger.Warn("无法结束已删除会话的轮次",
			"session_id", command.SessionID, "assistant_message_id", command.AssistantMessageID, "error", err)
	}
}

// sessionGone reports whether the owner no longer has the session.
func (orchestrator *Orchestrator) sessionGone(ctx context.Context, ownerID, sessionID string) bool {
	owned, err := orchestrator.service.OwnsSession(ctx, ownerID, sessionID)
	return err == nil && !owned
}
