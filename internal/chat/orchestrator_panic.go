package chat

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
)

// errTurnPanicked is the cause of a turn whose processing panicked (#665).
var errTurnPanicked = errors.New("chat turn panicked")

// recoverGoroutine is deferred first thing in every background goroutine and
// timer callback of the Orchestrator (#665): a panic there is logged with its
// stack and swallowed, so a bug in one wake or scan cannot take the process
// down. It must be deferred directly: recover only works in the deferred call.
func (orchestrator *Orchestrator) recoverGoroutine(name string, attributes ...any) {
	value := recover()
	if value == nil {
		return
	}
	orchestrator.logger.Error("对话后台协程崩溃，已恢复",
		append([]any{"goroutine", name, "panic", fmt.Sprint(value), "stack", string(debug.Stack())}, attributes...)...)
}

func (orchestrator *Orchestrator) notePanicked(assistantMessageID string) {
	orchestrator.panickedMu.Lock()
	defer orchestrator.panickedMu.Unlock()
	orchestrator.panicked[assistantMessageID] = struct{}{}
}

// clearPanicked drops the mark of a turn that has ended (#665): a later
// command with its ID (a leftover consent timer, a duplicate stop) then
// finds the ended turn and does nothing, instead of ending it again and
// closing the session's next stream early.
func (orchestrator *Orchestrator) clearPanicked(assistantMessageID string) {
	orchestrator.panickedMu.Lock()
	defer orchestrator.panickedMu.Unlock()
	delete(orchestrator.panicked, assistantMessageID)
}

// panickedTurnEnded reports whether a panicked turn already has its ending:
// its row is in a terminal state, or gone with its session or message. A read
// failure is not an ending.
func (orchestrator *Orchestrator) panickedTurnEnded(ctx context.Context, command ProcessTurn) bool {
	turn, err := orchestrator.service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if errors.Is(err, ErrMessageNotFound) {
		return true
	}
	return err == nil && orchestrator.service.TurnStateTerminal(turn.State)
}

func (orchestrator *Orchestrator) turnPanicked(assistantMessageID string) bool {
	orchestrator.panickedMu.Lock()
	defer orchestrator.panickedMu.Unlock()
	_, found := orchestrator.panicked[assistantMessageID]
	return found
}

// failPanickedTurn ends a turn whose processing panicked outside the turn's
// own recovery (#665): the reply gets the Schema's error text with its error
// ID and the stream its done event, and the stream and the turn end as
// failed. Every step is best effort; what cannot be written is logged.
func (orchestrator *Orchestrator) failPanickedTurn(ctx context.Context, command ProcessTurn) {
	// It runs in a recovery itself: whatever panicked may panic here again.
	defer orchestrator.recoverGoroutine("panicked-turn-ending", "assistant_message_id", command.AssistantMessageID)
	messages, err := orchestrator.service.ListMessages(ctx, command.OwnerID, command.SessionID)
	if err == nil {
		for index := range messages {
			if messages[index].ID != command.AssistantMessageID {
				continue
			}
			err = orchestrator.fail(ctx, command.OwnerID, &messages[index], "")
			break
		}
	}
	err = errors.Join(err,
		orchestrator.service.FinishStream(ctx, command.SessionID, command.AssistantMessageID, true),
		orchestrator.service.FinishTurn(ctx, command.AssistantMessageID, true))
	if err == nil {
		// The ending is written: the turn is an ended one now.
		orchestrator.clearPanicked(command.AssistantMessageID)
	}
	if err != nil {
		orchestrator.logger.Error("崩溃轮次收尾失败",
			"session_id", command.SessionID,
			"assistant_message_id", command.AssistantMessageID,
			"error", err,
		)
	}
}

// appendDone appends the done event that closes the turn's stream, best
// effort (#665): a failure path that cannot write anything else still lets
// the client stop waiting.
func (orchestrator *Orchestrator) appendDone(ctx context.Context, sessionID string) {
	_ = orchestrator.appendEvent(context.WithoutCancel(ctx), sessionID, map[string]any{
		orchestrator.schema.Stream.EventFields.Type: orchestrator.schema.Stream.EventTypes.Done,
	})
}
