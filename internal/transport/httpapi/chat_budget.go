package httpapi

import (
	"errors"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/pendingapproval"
)

// decideChatSessionBudget applies the person's decision to one production plan
// card of the session (#103). The plan belongs to the owner's session, and the
// decision is attributed to the real browser session: a plan approves spending
// money, so neither a Bearer identity nor an owner ID in the body is enough.
func (s *Server) decideChatSessionBudget(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	principal, ok := s.taskHumanPrincipal(request, owner)
	if !ok {
		s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", "error.forbidden")
		return
	}
	if s.pendingApprovals == nil {
		s.writeMessageProblem(writer, http.StatusServiceUnavailable, "unavailable", "error.unavailable")
		return
	}
	decision, ok := s.decodeApprovalDecision(writer, request)
	if !ok {
		return
	}
	outcome, err := s.pendingApprovals.DecideBudget(request.Context(), principal,
		request.PathValue("sessionID"), request.PathValue("budgetID"), decision)
	if errors.Is(err, pendingapproval.ErrBudgetNoticeFailed) {
		// The decision is committed; only its chat notice is missing.
		noteCause(writer, err)
		err = nil
	}
	if err != nil {
		s.writeTaskGrantError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"budget_id":  outcome.BudgetID,
		"state":      outcome.State,
		"message_id": outcome.MessageID,
	})
}
