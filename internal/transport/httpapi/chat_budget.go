package httpapi

import (
	"errors"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/pendingapproval"
)

// decideChatSessionBudget applies the person's decision to one production plan
// card of the session (#103). The plan belongs to the owner's session, and the
// decision is attributed to the real browser session: a plan approves spending
// money, so neither a Bearer identity nor an owner ID in the body is enough.
// An approved plan then runs in a turn the server starts itself (#134); a
// denied plan keeps only its notice.
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
	sessionID := request.PathValue("sessionID")
	outcome, err := s.pendingApprovals.DecideBudget(request.Context(), principal,
		sessionID, request.PathValue("budgetID"), decision)
	if errors.Is(err, pendingapproval.ErrBudgetNoticeFailed) {
		// The decision is committed; only its chat notice is missing.
		noteCause(writer, err)
		err = nil
	}
	if err != nil {
		s.writeTaskGrantError(writer, err)
		return
	}
	result := map[string]any{
		"budget_id":  outcome.BudgetID,
		"state":      outcome.State,
		"message_id": outcome.MessageID,
	}
	if outcome.State == budget.StateActive {
		if turnMessageID := s.continueApprovedPlan(request, string(owner), sessionID, outcome.BudgetID); turnMessageID != "" {
			result["turn_message_id"] = turnMessageID
		}
	}
	writeJSON(writer, http.StatusOK, result)
}

// continueApprovedPlan starts and schedules the turn that runs a plan the
// person just approved (#134) and returns its assistant message ID. The
// decision is already committed, so a continuation that cannot start (a turn of
// the session is still running, or the deployment declares no continuation) is
// logged rather than failing the decision: the running turn reads the approved
// plan from its budget context, and the person can still ask the Agent to go on.
func (s *Server) continueApprovedPlan(request *http.Request, ownerID, sessionID, budgetID string) string {
	ctx := request.Context()
	assistant, options, err := s.chat.StartPlanContinuation(ctx, ownerID, sessionID, budgetID, requestLocale(request))
	if err != nil {
		if s.logger != nil {
			level := s.logger.WarnContext
			if errors.Is(err, chat.ErrTurnActive) || errors.Is(err, chat.ErrProductionPlanUnavailable) {
				level = s.logger.InfoContext
			}
			level(ctx, "批准制作计划后未开启续跑轮次", "session_id", sessionID, "budget_id", budgetID, "error", err)
		}
		return ""
	}
	if s.chatScheduler == nil {
		return assistant.ID
	}
	// The turn is stored as queued; a failed schedule leaves it for the turn
	// recovery that picks up queued turns.
	if err := s.chatScheduler.Schedule(ctx, chat.ProcessTurn{
		OwnerID: ownerID, SessionID: sessionID, AssistantMessageID: assistant.ID, Options: options,
	}); err != nil && s.logger != nil {
		s.logger.ErrorContext(ctx, "提交制作计划续跑轮次失败",
			"session_id", sessionID, "assistant_message_id", assistant.ID, "error", err)
	}
	return assistant.ID
}
