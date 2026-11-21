package chat

import (
	"context"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

// turnOutcome is what Process knows when a turn ends, and what the end reason
// is told from (#328).
type turnOutcome struct {
	// failed is a turn that finishes with the Schema's error state.
	failed bool
	// background is a turn that ended with its Tasks left running (#137).
	background bool
	// stopped is a turn whose stop the server finalized.
	stopped bool
	// toolLimit is a reply whose tool budget ran out: the tool-less summary
	// round ran (#275).
	toolLimit bool
}

// turnEndReason tells why a finished turn ended, by one fixed priority: failed,
// background, stopped, awaiting approval, tool limit, completed. A turn that
// failed stays an error however it also looks; a stop or a background end is
// what the person chose; a turn that left an approval card is waiting for the
// person; a spent tool budget only says the reply was cut short.
func (orchestrator *Orchestrator) turnEndReason(
	ctx context.Context,
	command ProcessTurn,
	assistant Message,
	outcome turnOutcome,
) TurnEndReason {
	switch {
	case outcome.failed:
		return TurnEndError
	case outcome.background:
		return TurnEndBackground
	case outcome.stopped:
		return TurnEndStopped
	}
	// Without declared reasons nothing is stored, so the cards are not read.
	if !orchestrator.service.RecordsTurnEndReasons() {
		return TurnEndCompleted
	}
	if orchestrator.turnLeftApprovalCard(ctx, command, assistant) {
		return TurnEndAwaitingApproval
	}
	if outcome.toolLimit {
		return TurnEndToolLimit
	}
	return TurnEndCompleted
}

// turnLeftApprovalCard reports whether the turn handed an approval card to the
// person: a generation call whose card was detached from the turn (its
// approval waits for the person from then on), or a production plan this turn
// proposed that is still undecided. Both come from persisted state, so a turn
// that was parked and woken again, or restarted, answers the same way as one
// that ran straight through. The calls are read in the turn's own registry
// (#453), so a generation tool the Schema alone does not declare is still
// recognized. The reason is bookkeeping: a read that fails is logged and counts
// as no card, and never fails the turn.
func (orchestrator *Orchestrator) turnLeftApprovalCard(
	ctx context.Context,
	command ProcessTurn,
	assistant Message,
) bool {
	if reader, ok := orchestrator.tools.(interface {
		DetachedOperationForCall(context.Context, ToolCommand) (bool, error)
	}); ok {
		registry := orchestrator.turnRegistry(ctx)
		for _, call := range assistant.ToolCalls {
			tool, known := registry.byName[call.Name]
			if !known || tool.generation == nil {
				continue
			}
			detached, err := reader.DetachedOperationForCall(ctx, ToolCommand{
				OwnerID: command.OwnerID, SessionID: command.SessionID, AssistantMessageID: command.AssistantMessageID,
				Configuration:        configurationFromContext(ctx),
				Call:                 agents.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments},
				BoundTaskID:          call.TaskID,
				FrozenIdempotencyKey: call.IdempotencyKey,
				Options:              command.Options})
			if err != nil {
				orchestrator.logger.Warn(
					"读取轮次的批准卡失败，结束原因不记为等待批准",
					"session_id", command.SessionID,
					"assistant_message_id", command.AssistantMessageID,
					"call_id", call.ID,
					"error", err,
				)
				continue
			}
			if detached {
				return true
			}
		}
	}
	proposed, err := orchestrator.service.proposedPendingPlan(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil {
		orchestrator.logger.Warn(
			"读取轮次提出的制作计划失败，结束原因不记为等待批准",
			"session_id", command.SessionID,
			"assistant_message_id", command.AssistantMessageID,
			"error", err,
		)
		return false
	}
	return proposed
}
