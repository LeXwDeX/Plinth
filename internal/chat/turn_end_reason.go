package chat

import (
	"context"
	"fmt"
	"strings"
)

// TurnEndReason is why a chat turn ended (#328). It is an internal kind: the
// string stored with the turn comes from the Schema
// (application.chat.state.turn_end_reasons), never from code.
type TurnEndReason int

const (
	// TurnEndCompleted is a normal end.
	TurnEndCompleted TurnEndReason = iota
	// TurnEndToolLimit is a reply that spent its tool budget and ended with the
	// tool-less summary round (#275).
	TurnEndToolLimit
	// TurnEndAwaitingApproval is a turn that left an approval card (a paid
	// step or a production plan) for the person to decide.
	TurnEndAwaitingApproval
	// TurnEndStopped is a turn whose stop the person asked for and the server
	// finalized.
	TurnEndStopped
	// TurnEndBackground is a turn that ended while its Tasks keep running (#137).
	TurnEndBackground
	// TurnEndError is a turn that finished as failed.
	TurnEndError
)

// turnEndReasonText is the Schema string stored for the kind. It is empty when
// the Schema declares no end reasons, and ok is false for a kind that does not
// exist.
func (service *Service) turnEndReasonText(reason TurnEndReason) (text string, ok bool) {
	reasons := service.turnEndReasons
	switch reason {
	case TurnEndCompleted:
		return reasons.Completed, true
	case TurnEndToolLimit:
		return reasons.ToolLimit, true
	case TurnEndAwaitingApproval:
		return reasons.AwaitingApproval, true
	case TurnEndStopped:
		return reasons.Stopped, true
	case TurnEndBackground:
		return reasons.Background, true
	case TurnEndError:
		return reasons.Error, true
	}
	return "", false
}

// RecordsTurnEndReasons reports whether the Schema declares the end reasons, so
// a caller that would have to read something to tell them apart can skip it
// when nothing is stored.
func (service *Service) RecordsTurnEndReasons() bool {
	return service.turnEndReasons.Declared()
}

// TurnEndedInError reports whether a turn ended with the Schema's error end
// reason (#328). Without declared reasons nothing is stored, so the answer is
// false: the asset-list runner treats only a recorded failure as one.
func (service *Service) TurnEndedInError(turn Turn) bool {
	reason := strings.TrimSpace(service.turnEndReasons.Error)
	return reason != "" && turn.EndReason == reason
}

// proposedPendingPlan reports whether the turn proposed a production plan that
// still waits for the person: the session's unexpired undecided plans are read
// once, and the plan's proposing assistant message names the turn (#328).
// Without production plans there is nothing to read.
func (service *Service) proposedPendingPlan(
	ctx context.Context,
	ownerID string,
	sessionID string,
	assistantMessageID string,
) (bool, error) {
	if service.productionPlans == nil || ownerID == "" || sessionID == "" {
		return false, nil
	}
	pending, err := service.productionPlans.Store.PendingBudgets(ctx, ownerID, sessionID)
	if err != nil {
		return false, err
	}
	for _, production := range pending {
		if production.MessageID == assistantMessageID {
			return true, nil
		}
	}
	return false, nil
}

// FinishTurnWithReason ends the turn and stores why. The turn state is the
// Schema's error state for TurnEndError and its done state for every other
// reason, exactly as FinishTurn maps its failed flag; the reason is the
// Schema's string, empty when the Schema declares none (#328).
func (service *Service) FinishTurnWithReason(
	ctx context.Context,
	assistantMessageID string,
	reason TurnEndReason,
) error {
	text, ok := service.turnEndReasonText(reason)
	if !ok {
		return fmt.Errorf("chat: unknown turn end reason %d", reason)
	}
	state := service.turnDone
	if reason == TurnEndError {
		state = service.turnError
	}
	return service.repository.UpdateTurnState(
		ctx,
		assistantMessageID,
		state,
		text,
		service.clock(),
	)
}
