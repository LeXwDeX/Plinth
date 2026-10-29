package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

func (s *Server) createProcessingPlan(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	var command processing.CreateCommand
	if err := s.decodeJSON(writer, request, &command); err != nil {
		return
	}
	command.OwnerID = owner
	command.IdempotencyKey = strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	owned, err := s.chat.OwnsSession(request.Context(), string(owner), command.SessionID)
	if err != nil {
		s.writeChatInternalError(writer, err)
		return
	}
	if !owned {
		s.writeMessageProblem(writer, http.StatusNotFound, "not_found", "error.task_not_found")
		return
	}
	plan, err := s.processing.Create(request.Context(), command)
	if err != nil {
		s.writeProcessingError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, plan)
}

func (s *Server) listProcessingPlans(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	sessionID := strings.TrimSpace(request.URL.Query().Get("session_id"))
	owned, err := s.chat.OwnsSession(request.Context(), string(owner), sessionID)
	if err != nil {
		s.writeChatInternalError(writer, err)
		return
	}
	if !owned {
		s.writeMessageProblem(writer, http.StatusNotFound, "not_found", "error.task_not_found")
		return
	}
	plans, err := s.processing.List(request.Context(), owner, sessionID)
	if err != nil {
		s.writeChatInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"plans": plans})
}

func (s *Server) getProcessingPlan(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	plan, err := s.processing.Get(request.Context(), owner, request.PathValue("planID"))
	if err != nil {
		s.writeProcessingError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, plan)
}

func (s *Server) retryProcessingPlan(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	var command processing.RetryCommand
	if err := s.decodeJSON(writer, request, &command); err != nil {
		return
	}
	command.OwnerID = owner
	command.PlanID = request.PathValue("planID")
	plan, err := s.processing.Retry(request.Context(), command)
	if err != nil {
		s.writeProcessingError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, plan)
}

// resumeProcessingPlan requires the revisions displayed when the user clicked
// Resume; a delayed request cannot authorize another paid attempt.
func (s *Server) resumeProcessingPlan(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	var command processing.ResumeCommand
	if err := s.decodeJSON(writer, request, &command); err != nil {
		return
	}
	command.OwnerID = owner
	command.PlanID = request.PathValue("planID")
	plan, err := s.processing.Resume(request.Context(), command)
	if err != nil {
		s.writeProcessingError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, plan)
}

func (s *Server) reconcileProcessingPlan(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	var command processing.ReconcileCommand
	if err := s.decodeJSON(writer, request, &command); err != nil {
		return
	}
	command.OwnerID = owner
	command.PlanID = request.PathValue("planID")
	plan, err := s.processing.Reconcile(request.Context(), command)
	if err != nil {
		s.writeProcessingError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, plan)
}

func (s *Server) writeProcessingError(writer http.ResponseWriter, err error) {
	noteCause(writer, err)
	switch {
	case errors.Is(err, processing.ErrReconcileFrozenChanged):
		s.writeMessageProblem(writer, http.StatusConflict, "processing_reconcile_frozen_changed", "ui.workspace.processing.plan_reconcile_frozen_changed")
	case errors.Is(err, processing.ErrReconcileUndetermined):
		s.writeMessageProblem(writer, http.StatusConflict, "processing_reconcile_undetermined", "ui.workspace.processing.plan_reconcile_undetermined")
	case errors.Is(err, processing.ErrMissing):
		s.writeMessageProblem(writer, http.StatusNotFound, "not_found", "error.task_not_found")
	case errors.Is(err, processing.ErrInvalid):
		// A plan refused for a reason the Schema words (a Remesh budget over a
		// later step's source limit, #121) shows that reason; every other
		// invalid plan keeps the generic message.
		if reason := userFacingReason(writer, http.StatusBadRequest, err); reason != "" {
			writeReasonProblem(writer, http.StatusBadRequest, "invalid_input", reason)
			return
		}
		s.writeMessageProblem(writer, http.StatusBadRequest, "invalid_input", "error.processing_invalid")
	case errors.Is(err, processing.ErrConflict):
		s.writeMessageProblem(writer, http.StatusConflict, "conflict", "error.processing_conflict")
	default:
		s.writeChatInternalError(writer, err)
	}
}
