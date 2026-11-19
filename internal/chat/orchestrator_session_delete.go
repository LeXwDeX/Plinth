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
	cancel, done := orchestrator.activeCancel[key], orchestrator.activeDone[key]
	orchestrator.activeMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	timer := time.NewTimer(sessionQuiesceTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		orchestrator.logger.Warn("删除会话时等待轮次退出超时，轮次将在发现会话已删除后自行结束",
			"session_id", sessionID)
	case <-ctx.Done():
	}
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
		if err := orchestrator.Enqueue(ProcessTurn{OwnerID: turn.OwnerID, SessionID: turn.SessionID,
			AssistantMessageID: turn.AssistantMessageID, Options: turn.Options}); err != nil {
			orchestrator.logger.Warn("删除会话失败后轮次入队暂失败，等待扫描重试",
				"session_id", sessionID, "assistant_message_id", turn.AssistantMessageID, "error", err)
		}
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
