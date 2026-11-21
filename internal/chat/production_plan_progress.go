package chat

import (
	"context"
	"time"

	"github.com/LeXwDeX/Plinth/internal/budget"
)

// The production plan projection (#410). The browser used to pair every plan
// step with the session's Tasks itself; the server now reads the pairing from
// the same facts and the card only renders it. The rule lives in
// budget.PlanStepStatuses (capability, the pinned Offering, one Task per step,
// a Task that started before the plan was decided never counts); this file only
// supplies the facts of a session and its turn.

// PlanTaskFacts is what the projection reads about one session: the Tasks a
// plan step may pair with, and whether the session's turn has not ended yet.
type PlanTaskFacts struct {
	Tasks []budget.StepTask
	// TurnRunning says a step without a Task may still get one.
	TurnRunning bool
}

// ProductionPlanProgress pairs the plan of each given session with that
// session's Tasks and turn state, in one store round trip (#410). summaries
// maps a session ID to its current plan; a session without a plan, or one the
// store cannot read, is absent from the result and the browser falls back to
// its own pairing. A read error is returned: a plan card that cannot be
// projected must not silently claim a step ran.
func (service *Service) ProductionPlanProgress(
	ctx context.Context,
	ownerID string,
	summaries map[string]budget.Summary,
) (map[string][]budget.StepStatus, error) {
	progress := make(map[string][]budget.StepStatus, len(summaries))
	if len(summaries) == 0 {
		return progress, nil
	}
	sessionIDs := make([]string, 0, len(summaries))
	for sessionID := range summaries {
		sessionIDs = append(sessionIDs, sessionID)
	}
	facts, err := service.repository.PlanTaskFacts(ctx, ownerID, sessionIDs, service.activeTurnStates())
	if err != nil {
		return nil, err
	}
	for sessionID, summary := range summaries {
		fact := facts[sessionID]
		var decidedAt time.Time
		if summary.DecidedAt != nil {
			decidedAt = *summary.DecidedAt
		}
		progress[sessionID] = budget.PlanStepStatuses(budget.StepStatusInput{
			Plan:        summary.Plan,
			PlanState:   summary.State,
			DecidedAt:   decidedAt,
			TurnRunning: fact.TurnRunning,
			Tasks:       budget.EligibleStepTasks(fact.Tasks, decidedAt),
		})
	}
	return progress, nil
}

// activeTurnStates are the turn states that have not ended: a session whose
// turn is in one of them still runs, so a step without a Task may still get
// one. They are the Catalog's own states (chat.state), never a second copy.
func (service *Service) activeTurnStates() []string {
	return []string{service.turnQueued, service.turnRunning, service.turnWaitingTask}
}
