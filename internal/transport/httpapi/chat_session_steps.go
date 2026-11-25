package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// sessionStepsWire is the 3D step standing of one asset session (#330 §6/5C).
// The asset is the chat session: an external MCP asset (#332) and a list
// item's sub-session share the storage, and the cap and the person's grant are
// reported only in the cap mode, where they mean something.
type sessionStepsWire struct {
	Mode  string `json:"mode"`
	Used  int    `json:"used"`
	Cap   int    `json:"cap,omitempty"`
	Extra int    `json:"extra,omitempty"`
}

func sessionStepsOf(standing executiongrant.StepSummary) sessionStepsWire {
	wire := sessionStepsWire{Mode: standing.Mode, Used: standing.Used}
	if standing.Mode == approvalpolicy.StepModeCap {
		wire.Cap = standing.Cap
		wire.Extra = standing.Extra
	}
	return wire
}

// registerChatSessionStepsContract serves one asset session's "give it a few
// more steps" (#330 §6): the read supplies the standing the dialog needs, and
// the write raises the asset's grant. Both routes need the Catalog's route key
// and the step policy; the list-item route stays the entry for list items.
func (s *Server) registerChatSessionStepsContract() {
	route := s.application.Runtime.HTTP.Routes.ChatSessionSteps
	if route == "" || s.application.StepPolicy == nil || s.executionGrants == nil {
		return
	}
	s.mux.HandleFunc("GET "+route, s.withPrincipal(s.getChatSessionSteps))
	s.mux.HandleFunc("PUT "+route, s.withPrincipal(s.putChatSessionSteps))
}

// getChatSessionSteps reads the session's live standing. A session that is not
// an asset of the owner (a plain session, another owner's session or a Catalog
// without a step entry) has no standing: 204 with no body, the same answer for
// every non-asset. The studio reads this for every open session, so a plain
// chat is an ordinary empty answer rather than a failed request (#616); the
// write below still refuses a non-asset with 404.
func (s *Server) getChatSessionSteps(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	if !principal.Can(access.PermissionTasksReadOwn) {
		s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", "error.forbidden")
		return
	}
	sessionID := request.PathValue("sessionID")
	standing, err := s.executionGrants.StepStatus(request.Context(), principal.ID(), sessionID)
	if errors.Is(err, executiongrant.ErrExtraStepsUnavailable) || errors.Is(err, executiongrant.ErrInvalid) {
		writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		s.writeChatSessionStepsError(writer, err, 0)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, sessionStepsOf(standing))
}

// putChatSessionSteps is the person's "give it a few more steps" for one asset
// session (#330 §6). The body is the asset's whole grant, not an increment, so
// a retried answer cannot add twice; the write is audited with its before and
// after. Only a real browser session may raise a cap: a Bearer token, a
// development identity or a personal access key is 403, and another owner's
// session is 404, never 403.
func (s *Server) putChatSessionSteps(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	owner := generation.OwnerID(principal.ID())
	if _, human := s.taskHumanPrincipal(request, owner); !human {
		s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", "error.forbidden")
		return
	}
	var body struct {
		ExtraSteps *int `json:"extra_steps"`
	}
	if err := s.decodeJSON(writer, request, &body); err != nil {
		return
	}
	if body.ExtraSteps == nil || *body.ExtraSteps < 0 {
		s.writeMessageProblem(writer, http.StatusBadRequest, "invalid_input", "error.invalid_input")
		return
	}
	// A target above the asset's total bound is a refused grant, not a missing
	// asset; the service treats it as an invalid argument, so the bound is
	// checked here where the Schema's copy is in hand.
	if total := s.stepExtraTotalMaximum(); total > 0 && *body.ExtraSteps > total {
		detail := s.stepPolicyMessage(func(messages catalog.StepPolicyMessagesSchema) string {
			return messages.ExtraStepsLimit
		}, map[string]string{
			"maximum": strconv.Itoa(total),
			"extra":   strconv.Itoa(*body.ExtraSteps),
		})
		writeProblem(writer, http.StatusConflict, "step_extra_limit", detail)
		return
	}
	sessionID := request.PathValue("sessionID")
	change, err := s.executionGrants.SetExtraSteps(request.Context(), principal.ID(), sessionID, *body.ExtraSteps)
	if err != nil {
		s.writeChatSessionStepsError(writer, err, *body.ExtraSteps)
		return
	}
	if audit := s.application.StepPolicy.Audit; change.Previous.Extra != change.Current.Extra {
		s.recordProductAudit(request, administration.AuditEvent{
			Action:       audit.ExtraStepsAction,
			ActorID:      principal.ID(),
			ActorName:    principal.Claims.Name,
			ResourceType: audit.ExtraStepsResourceType,
			ResourceID:   sessionID,
			Detail: map[string]any{
				"from": map[string]any{"extra_steps": change.Previous.Extra},
				"to":   map[string]any{"extra_steps": change.Current.Extra},
			},
		})
	}
	// A session that is a list item's sub-session also owns the item's block:
	// the raised grant releases the item and a run paused at the cap, exactly
	// like the list-item route. A session that is no item is a no-op.
	if s.assetLists != nil {
		if err := s.assetLists.ClearStepCapForSession(request.Context(), principal.ID(), sessionID); err != nil {
			s.writeChatSessionStepsError(writer, err, *body.ExtraSteps)
			return
		}
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, sessionStepsOf(change.Current))
}

// writeChatSessionStepsError maps the grant service's refusals. An unavailable
// asset (a plain or foreign session) is 404 so the caller cannot tell the two
// apart; a bound is 409 with the step policy's own copy.
func (s *Server) writeChatSessionStepsError(writer http.ResponseWriter, err error, requested int) {
	switch {
	case errors.Is(err, executiongrant.ErrExtraStepsUnavailable), errors.Is(err, executiongrant.ErrInvalid):
		writeProblem(writer, http.StatusNotFound, "not_found", s.message("error.not_found"), err)
	case errors.Is(err, executiongrant.ErrExtraStepsLimit):
		detail := s.stepPolicyMessage(func(messages catalog.StepPolicyMessagesSchema) string {
			return messages.ExtraStepsLimit
		}, map[string]string{
			"maximum": strconv.Itoa(s.stepExtraTotalMaximum()),
			"extra":   strconv.Itoa(requested),
		})
		writeProblem(writer, http.StatusConflict, "step_extra_limit", detail, err)
	case errors.Is(err, executiongrant.ErrExtraStepsSingleLimit):
		detail := s.stepPolicyMessage(func(messages catalog.StepPolicyMessagesSchema) string {
			return messages.ExtraStepsSingleLimit
		}, map[string]string{"maximum": strconv.Itoa(s.stepExtraMaximum())})
		writeProblem(writer, http.StatusConflict, "step_extra_single_limit", detail, err)
	case errors.Is(err, executiongrant.ErrConflict):
		writeProblem(writer, http.StatusConflict, "conflict", s.message("error.conflict"), err)
	default:
		writeProblem(writer, http.StatusInternalServerError, "internal_error", s.message("error.internal"), err)
	}
}

func (s *Server) stepExtraMaximum() int {
	if s.application.StepPolicy == nil {
		return 0
	}
	return s.application.StepPolicy.ExtraSteps.Maximum
}

func (s *Server) stepExtraTotalMaximum() int {
	if s.application.StepPolicy == nil {
		return 0
	}
	return s.application.StepPolicy.ExtraSteps.TotalMaximum
}
