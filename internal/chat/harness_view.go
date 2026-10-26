package chat

import (
	"context"
	"github.com/LeXwDeX/Plinth/internal/harness"
)

type HarnessTurnView struct {
	AssistantMessageID string            `json:"assistant_message_id"`
	Selection          harness.Selection `json:"selection"`
}

// CurrentHarnessTurn exposes the already frozen choice of an active turn. It
// never re-resolves it against today's Catalog or changes its execution input.
func (s *Service) CurrentHarnessTurn(ctx context.Context, owner, session string) (*HarnessTurnView, error) {
	turns, err := s.repository.ListSessionTurns(ctx, owner, session, []string{s.turnQueued, s.turnRunning, s.turnWaitingTask})
	if err != nil {
		return nil, err
	}
	var current *Turn
	for i := range turns {
		t := &turns[i]
		if t.Options.Harness == nil {
			continue
		}
		if current == nil || t.UpdatedAt.After(current.UpdatedAt) {
			current = t
		}
	}
	if current == nil {
		return nil, nil
	}
	selected := current.Options.Harness.Selection
	if selected.SessionID != session {
		return nil, ErrTurnCheckpointConflict
	}
	return &HarnessTurnView{AssistantMessageID: current.AssistantMessageID, Selection: selected}, nil
}
