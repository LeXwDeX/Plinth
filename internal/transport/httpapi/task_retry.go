package httpapi

import (
	"errors"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/taskretry"
)

// retryTask starts a new task from the frozen input of a failed one. It is a
// paid step, so only a real browser session of the owner may start it, and it
// runs the ordinary prepare, grant and submit path: the session approval mode
// decides whether a person must confirm. Repeating the request never creates a
// second task, because the idempotency key derives from the failed task.
func (s *Server) retryTask(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	principal, ok := s.taskHumanPrincipal(request, owner)
	if !ok {
		s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", "error.forbidden")
		return
	}
	result, err := s.retry.Retry(request.Context(), principal, request.PathValue("taskID"))
	if err != nil {
		s.writeTaskRetryError(writer, err)
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

func (s *Server) writeTaskRetryError(writer http.ResponseWriter, err error) {
	noteCause(writer, err)
	switch {
	case errors.Is(err, taskretry.ErrNotFound):
		s.writeMessageProblem(writer, http.StatusNotFound, "not_found", "error.task_not_found")
	case errors.Is(err, taskretry.ErrNotRetryable):
		s.writeMessageProblem(writer, http.StatusConflict, "task_not_retryable", "error.task_not_retryable")
	case errors.Is(err, taskretry.ErrInProgress):
		s.writeMessageProblem(writer, http.StatusConflict, "retry_in_progress", "error.retry_in_progress")
	case errors.Is(err, taskretry.ErrDeclined):
		s.writeMessageProblem(writer, http.StatusConflict, "retry_declined", "error.retry_declined")
	case errors.Is(err, taskretry.ErrInvalid):
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
