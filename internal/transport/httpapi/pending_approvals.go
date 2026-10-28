package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/pendingapproval"
)

// PendingApprovals is the service for Agent approvals that outlive their turn.
type PendingApprovals interface {
	List(ctx context.Context, owner, session string) ([]pendingapproval.Item, error)
	DetachedSession(ctx context.Context, owner, requestID string) (session string, ok bool, err error)
	Decide(ctx context.Context, principal executiongrant.Principal, requestID string,
		decision executiongrant.Decision) (pendingapproval.Outcome, bool, error)
}

// listPendingApprovals projects the caller's own session's pending approvals.
// A foreign or missing session is the same 404 as everywhere else.
func (s *Server) listPendingApprovals(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	locale := requestLocale(request)
	items, err := s.pendingApprovals.List(catalog.WithLocale(request.Context(), locale), string(owner), request.PathValue("sessionID"))
	if err != nil {
		if errors.Is(err, pendingapproval.ErrSessionNotFound) {
			s.writeMessageProblem(writer, http.StatusNotFound, "not_found", "error.task_not_found")
			return
		}
		s.writeChatInternalError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	for index := range items {
		// Model, provider, quote and extra-credit labels are offering copy;
		// the prompt and inputs are the person's own and stay as written.
		item := &items[index]
		item.Model = s.textIn(locale, item.Model)
		item.Provider = s.textIn(locale, item.Provider)
		item.ExtraLabel = s.textIn(locale, item.ExtraLabel)
		item.ParameterAdjustments = s.parameterAdjustmentsIn(locale, item.ParameterAdjustments)
		if item.ExtraItems != nil {
			localizedItems := make([]string, len(item.ExtraItems))
			for position, extra := range item.ExtraItems {
				localizedItems[position] = s.textIn(locale, extra)
			}
			item.ExtraItems = localizedItems
		}
		item.NativeQuote = s.quoteIn(locale, generation.Offering{}, item.NativeQuote)
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": items})
}

// pendingOutcomeWire is the response of a decision on a pending approval. The
// task and the message are present once the approval was submitted and posted.
func pendingOutcomeWire(outcome pendingapproval.Outcome) map[string]any {
	response := map[string]any{"request_id": outcome.RequestID, "status": outcome.Status}
	if outcome.TaskID != "" {
		response["task_id"] = outcome.TaskID
	}
	if outcome.MessageID != "" {
		response["message_id"] = outcome.MessageID
	}
	return response
}
