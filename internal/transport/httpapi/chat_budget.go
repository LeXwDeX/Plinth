package httpapi

import (
	"context"
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
		s.completeFinishedPlan(request.Context(), string(owner), outcome.BudgetID)
		if turnMessageID := s.continueApprovedPlan(request, string(owner), sessionID, outcome.BudgetID); turnMessageID != "" {
			result["turn_message_id"] = turnMessageID
		}
	}
	writeJSON(writer, http.StatusOK, result)
}

// continueChatSessionBudget starts the turn that runs the session's plan again
// after the person left it unfinished (#410): the plan is still active, its
// turn has ended and required steps are left, so the plan card offers
// 「继续」. Only the person's own browser session may start it: the plan is
// read owner-scoped, and a turn of the session that still runs is refused with
// 409 rather than opening a second one. The decision to spend is the one
// already made when the plan was approved; this route starts no new plan.
func (s *Server) continueChatSessionBudget(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	principal, ok := s.taskHumanPrincipal(request, owner)
	if !ok {
		s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", "error.forbidden")
		return
	}
	sessionID := request.PathValue("sessionID")
	budgetID := request.PathValue("budgetID")
	if _, found, err := s.chat.Session(request.Context(), principal.OwnerID, sessionID); err != nil {
		s.writeChatInternalError(writer, err)
		return
	} else if !found {
		s.writeChatError(writer, http.StatusNotFound, s.application.Chat.Presentation.Messages.SessionNotFound)
		return
	}
	assistant, options, err := s.chat.StartPlanContinuation(request.Context(), principal.OwnerID, sessionID, budgetID, requestLocale(request))
	if err != nil {
		switch {
		case errors.Is(err, chat.ErrTurnActive):
			s.writeChatError(writer, http.StatusConflict, s.application.Chat.Presentation.Messages.TurnActive)
		case errors.Is(err, chat.ErrPlanNotActive), errors.Is(err, chat.ErrSessionNotFound):
			// A historical Catalog declares no continuation text; the generic
			// invalid-parameter text says the request cannot apply.
			message := s.application.Chat.Presentation.Messages.BudgetPlanInactive
			if message == "" {
				message = s.application.Chat.Presentation.Messages.InvalidParameters
			}
			s.writeChatError(writer, http.StatusConflict, message)
		case errors.Is(err, chat.ErrProductionPlanUnavailable):
			s.writeMessageProblem(writer, http.StatusServiceUnavailable, "unavailable", "error.unavailable")
		default:
			s.writeChatInternalError(writer, err)
		}
		return
	}
	// #410: the plan's own turn is about to start, so a plan whose steps are all
	// done already (its last step paid without a reservation, say) is collected
	// now instead of waiting for a settlement that may never touch it.
	s.completeFinishedPlan(request.Context(), principal.OwnerID, budgetID)
	if s.chatScheduler != nil {
		// The turn is stored as queued; a failed schedule leaves it for the
		// turn recovery that picks up queued turns.
		if err := s.chatScheduler.Schedule(request.Context(), chat.ProcessTurn{
			OwnerID: principal.OwnerID, SessionID: sessionID, AssistantMessageID: assistant.ID, Options: options,
		}); err != nil && s.logger != nil {
			s.logger.ErrorContext(request.Context(), "提交制作计划续跑轮次失败",
				"session_id", sessionID, "assistant_message_id", assistant.ID, "error", err)
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"turn_message_id": assistant.ID})
}

// completeFinishedPlan marks the plan completed when all of its required steps
// already have a succeeded Task (#410). It runs where a plan's turn starts — a
// decision and a continuation — so a plan that finished without a reserving step
// still reads completed; a failure is logged and changes nothing, because the
// plan is already decided and the sweep's bounded scope will not collect it.
func (s *Server) completeFinishedPlan(ctx context.Context, ownerID, budgetID string) {
	if s.executionGrants == nil || ownerID == "" || budgetID == "" {
		return
	}
	if _, err := s.executionGrants.CompleteFinishedBudget(ctx, ownerID, budgetID); err != nil && s.logger != nil {
		s.logger.WarnContext(ctx, "收尾已完成的制作计划失败",
			"owner_id", ownerID, "budget_id", budgetID, "error", err)
	}
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
