package httpapi

import (
	"net/http"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/access"
)

// The presence wire names (#328 §4.4): the browser sends one heartbeat or a
// leave per tab; the server never echoes the tab id back.
const (
	presenceStatePresent = "present"
	presenceStateLeave   = "leave"
	presenceTabIDMax     = 128
)

type projectPresenceBody struct {
	TabID string `json:"tab_id"`
	State string `json:"state"`
}

// postProjectPresence records or removes one browser tab's heartbeat. Only a
// real browser session may heartbeat: a Bearer identity or key is refused with
// 403, an anonymous request with 401. A foreign or missing project answers 404
// exactly like the asset list routes, so the route never reveals whether an
// id exists. The store's clock comes from Config.Clock (default time.Now).
func (s *Server) postProjectPresence(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	owner, ok := s.assetListOwner(writer, request, principal)
	if !ok {
		return
	}
	var body projectPresenceBody
	if err := s.decodeJSON(writer, request, &body); err != nil {
		return
	}
	tabID := strings.TrimSpace(body.TabID)
	invalid := tabID == "" || len(tabID) > presenceTabIDMax ||
		(body.State != presenceStatePresent && body.State != presenceStateLeave)
	if invalid {
		writeProblem(writer, http.StatusBadRequest, "invalid_input",
			s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	projectID := request.PathValue("projectID")
	exists, err := s.assetLists.ProjectExists(request.Context(), owner, projectID)
	if err != nil {
		writeProblem(writer, http.StatusInternalServerError, "internal",
			s.application.Chat.Presentation.Messages.InternalError, err)
		return
	}
	if !exists {
		writeProblem(writer, http.StatusNotFound, "not_found", s.message("error.not_found"))
		return
	}
	if body.State == presenceStatePresent {
		err = s.assetListPresence.Upsert(request.Context(), owner, projectID, tabID, s.clock())
	} else {
		err = s.assetListPresence.Leave(request.Context(), owner, projectID, tabID)
	}
	if err != nil {
		writeProblem(writer, http.StatusInternalServerError, "internal",
			s.application.Chat.Presentation.Messages.InternalError, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}
