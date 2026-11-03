package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/threeview"
)

// ThreeViews is the three-view group service.
type ThreeViews interface {
	List(ctx context.Context, owner, session string) ([]threeview.Group, error)
	Advance(ctx context.Context, principal executiongrant.Principal, session, runID, viewKey string,
		action threeview.Action, attempt int) (threeview.Group, error)
	Replace(ctx context.Context, principal executiongrant.Principal, session, runID, viewKey, assetID string) (threeview.Group, error)
}

// listThreeViews projects the groups of the caller's own session. A foreign or
// missing session is the same 404 as everywhere else.
func (s *Server) listThreeViews(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	groups, err := s.threeViews.List(request.Context(), string(owner), request.PathValue("sessionID"))
	if err != nil {
		s.writeThreeViewError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.localizedSchemaData(request, map[string]any{"items": groups}))
}

// advanceThreeView starts, retries or replaces one view of a group. Only the
// person at the browser can spend credits, so a Bearer or development identity
// is refused; the session approval mode still decides whether the step needs a
// separate approval. Replacing puts an input asset the person uploaded in
// place of the view (#65) and spends nothing.
func (s *Server) advanceThreeView(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	var body struct {
		Action  threeview.Action `json:"action"`
		Attempt int              `json:"attempt"`
		AssetID string           `json:"asset_id"`
	}
	if err := s.decodeJSON(writer, request, &body); err != nil {
		return
	}
	principal, ok := s.taskHumanPrincipal(request, owner)
	if !ok {
		s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", "error.forbidden")
		return
	}
	var group threeview.Group
	var err error
	if body.Action == threeview.Replace {
		group, err = s.threeViews.Replace(request.Context(), principal, request.PathValue("sessionID"),
			request.PathValue("runID"), request.PathValue("viewKey"), body.AssetID)
	} else {
		group, err = s.threeViews.Advance(request.Context(), principal, request.PathValue("sessionID"),
			request.PathValue("runID"), request.PathValue("viewKey"), body.Action, body.Attempt)
	}
	if err != nil {
		s.writeThreeViewError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.localizedSchemaData(request, map[string]any{"group": group}))
}

func (s *Server) writeThreeViewError(writer http.ResponseWriter, err error) {
	noteCause(writer, err)
	var failure *generation.Error
	switch {
	case errors.Is(err, threeview.ErrNotFound):
		s.writeMessageProblem(writer, http.StatusNotFound, "not_found", "error.task_not_found")
	case errors.Is(err, threeview.ErrConflict):
		s.writeMessageProblem(writer, http.StatusConflict, "three_view_conflict", "error.three_view_conflict")
	case errors.Is(err, threeview.ErrInvalid):
		s.writeMessageProblem(writer, http.StatusBadRequest, "invalid_input", "error.invalid_input")
	case errors.As(err, &failure):
		s.writeDomainError(writer, err)
	default:
		s.writeChatInternalError(writer, err)
	}
}
