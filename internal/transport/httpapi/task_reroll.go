package httpapi

import (
	"errors"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/taskreroll"
)

// rerollTask starts a new task from the frozen input of a succeeded one,
// changing only the Schema-declared seeds (#242). It is a paid step, so only a
// real browser session of the owner may start it, it needs the client's
// Idempotency-Key, and it runs the ordinary prepare, grant and submit path:
// the session approval mode or budget decides whether a person must confirm.
// Repeating the same key never creates a second task.
func (s *Server) rerollTask(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	principal, ok := s.taskHumanPrincipal(request, owner)
	if !ok {
		s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", "error.forbidden")
		return
	}
	messages := s.application.Chat.Reroll.Messages
	idempotencyKey := request.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		writeProblem(writer, http.StatusBadRequest, "idempotency_key_required", messages.IdempotencyRequired)
		return
	}
	result, err := s.reroll.Reroll(request.Context(), principal, request.PathValue("taskID"), idempotencyKey)
	if err != nil {
		s.writeTaskRerollError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	if result.Waiting != nil {
		// A person still has to decide: the client finishes with the ordinary
		// grant decision and task submit, using the key given here.
		writeJSON(writer, http.StatusAccepted, map[string]any{
			"status":            "awaiting_decision",
			"prepared_input_id": result.Waiting.PreparedInputID,
			"request_id":        result.Waiting.RequestID,
			"idempotency_key":   result.Waiting.IdempotencyKey,
		})
		return
	}
	writer.Header().Set("Location", s.taskItemRoute.Render(result.TaskID))
	writeJSON(writer, http.StatusAccepted, map[string]any{
		"status":   "submitted",
		"task_id":  result.TaskID,
		"replayed": result.Replayed,
	})
}

func (s *Server) writeTaskRerollError(writer http.ResponseWriter, err error) {
	noteCause(writer, err)
	messages := s.application.Chat.Reroll.Messages
	switch {
	case errors.Is(err, taskreroll.ErrNotFound):
		s.writeMessageProblem(writer, http.StatusNotFound, "not_found", "error.task_not_found")
	case errors.Is(err, taskreroll.ErrNotSucceeded):
		writeProblem(writer, http.StatusConflict, "task_not_rerollable", messages.NotSucceeded)
	case errors.Is(err, taskreroll.ErrNoFrozenInput):
		writeProblem(writer, http.StatusConflict, "task_no_frozen_input", messages.NoFrozenInput)
	case errors.Is(err, taskreroll.ErrInProgress):
		writeProblem(writer, http.StatusConflict, "reroll_in_progress", messages.InProgress)
	case errors.Is(err, taskreroll.ErrDeclined):
		writeProblem(writer, http.StatusConflict, "reroll_declined", messages.Declined)
	case errors.Is(err, taskreroll.ErrUnavailable):
		writeProblem(writer, http.StatusConflict, "reroll_unavailable", messages.Unavailable)
	case errors.Is(err, taskreroll.ErrInvalid):
		s.writeMessageProblem(writer, http.StatusBadRequest, "invalid_input", "error.invalid_input")
	default:
		var domain *generation.Error
		if errors.As(err, &domain) {
			s.writeDomainError(writer, err)
			return
		}
		s.writeTaskGrantError(writer, err)
	}
}
