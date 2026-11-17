package httpapi

import (
	"errors"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/accountsettings"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// accountStepSetting is one entry's 3D step setting on the wire.
type accountStepSetting struct {
	Mode string `json:"mode"`
	Cap  int    `json:"cap"`
}

type accountStepCapRange struct {
	Minimum int `json:"minimum"`
	Maximum int `json:"maximum"`
}

// accountStepPolicyResponse is the read and the answer of a write. Version is 0
// while the person never saved a setting; the other members then show the
// Schema defaults.
type accountStepPolicyResponse struct {
	Version  int64                `json:"version"`
	List     accountStepSetting   `json:"list"`
	MCP      accountStepSetting   `json:"mcp"`
	Modes    []catalog.ValueLabel `json:"modes"`
	CapRange accountStepCapRange  `json:"cap_range"`
}

// registerAccountStepPolicyContract serves the person's 3D step settings
// (#330). Both methods are registered only when the Catalog declares
// application.step_policy and its route.
func (s *Server) registerAccountStepPolicyContract() {
	route := s.application.Runtime.HTTP.Routes.AccountStepPolicy
	if route == "" || s.accountSettings == nil || s.application.StepPolicy == nil {
		return
	}
	s.mux.HandleFunc("GET "+route, s.withPrincipal(s.getAccountStepPolicy))
	s.mux.HandleFunc("PUT "+route, s.withPrincipal(s.putAccountStepPolicy))
}

func (s *Server) accountStepPolicyWire(request *http.Request, settings accountsettings.Settings) accountStepPolicyResponse {
	minimum, maximum := s.accountSettings.CapRange()
	locale := requestLocale(request)
	modes := make([]catalog.ValueLabel, 0, len(s.application.StepPolicy.Modes))
	for _, mode := range s.application.StepPolicy.Modes {
		modes = append(modes, catalog.ValueLabel{Value: mode.Value, Label: s.textIn(locale, mode.Label)})
	}
	return accountStepPolicyResponse{
		Version:  settings.Version,
		List:     accountStepSetting{Mode: settings.List.Mode, Cap: settings.List.Cap},
		MCP:      accountStepSetting{Mode: settings.MCP.Mode, Cap: settings.MCP.Cap},
		Modes:    modes,
		CapRange: accountStepCapRange{Minimum: minimum, Maximum: maximum},
	}
}

func (s *Server) getAccountStepPolicy(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	if !principal.Can(access.PermissionTasksReadOwn) {
		s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", "error.forbidden")
		return
	}
	settings, err := s.accountSettings.Get(request.Context(), principal.ID())
	if err != nil {
		s.writeAccountStepPolicyError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.accountStepPolicyWire(request, settings))
}

// putAccountStepPolicy saves both settings. The cap and the unlimited modes
// remove or move a guard on paid 3D steps, so the write needs the same real
// browser session as a payment decision: a Bearer token or development
// credential, such as an external MCP client holds, cannot raise its own cap.
// The change is audited; a request that holds the values already in force is
// not a change and writes no audit entry.
func (s *Server) putAccountStepPolicy(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	owner := generation.OwnerID(principal.ID())
	if _, human := s.taskHumanPrincipal(request, owner); !human {
		s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", "error.forbidden")
		return
	}
	// Every member is required: a missing one would otherwise be saved as its
	// zero value.
	var body struct {
		ExpectedVersion *int64 `json:"expected_version"`
		List            *struct {
			Mode *string `json:"mode"`
			Cap  *int    `json:"cap"`
		} `json:"list"`
		MCP *struct {
			Mode *string `json:"mode"`
			Cap  *int    `json:"cap"`
		} `json:"mcp"`
	}
	if err := s.decodeJSON(writer, request, &body); err != nil {
		return
	}
	if body.ExpectedVersion == nil || body.List == nil || body.MCP == nil ||
		body.List.Mode == nil || body.List.Cap == nil || body.MCP.Mode == nil || body.MCP.Cap == nil {
		s.writeMessageProblem(writer, http.StatusBadRequest, "invalid_input", "error.invalid_input")
		return
	}
	change, err := s.accountSettings.Put(request.Context(), principal.ID(), *body.ExpectedVersion,
		approvalpolicy.StepSetting{Mode: *body.List.Mode, Cap: *body.List.Cap},
		approvalpolicy.StepSetting{Mode: *body.MCP.Mode, Cap: *body.MCP.Cap})
	if err != nil {
		s.writeAccountStepPolicyError(writer, err)
		return
	}
	if change.Changed {
		audit := s.application.StepPolicy.Audit
		s.recordProductAudit(request, administration.AuditEvent{
			Action:       audit.UpdateAction,
			ActorID:      principal.ID(),
			ActorName:    principal.Claims.Name,
			ResourceType: audit.ResourceType,
			ResourceID:   principal.ID(),
			Detail: map[string]any{
				"from": accountStepDetail(change.Previous),
				"to":   accountStepDetail(change.Settings),
			},
		})
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.accountStepPolicyWire(request, change.Settings))
}

// accountStepDetail is the audit form of the settings: the modes and caps, not
// the version.
func accountStepDetail(settings accountsettings.Settings) map[string]any {
	return map[string]any{
		"list": map[string]any{"mode": settings.List.Mode, "cap": settings.List.Cap},
		"mcp":  map[string]any{"mode": settings.MCP.Mode, "cap": settings.MCP.Cap},
	}
}

func (s *Server) writeAccountStepPolicyError(writer http.ResponseWriter, err error) {
	noteCause(writer, err)
	switch {
	case errors.Is(err, accountsettings.ErrInvalid):
		s.writeMessageProblem(writer, http.StatusBadRequest, "invalid_input", "error.invalid_input")
	case errors.Is(err, accountsettings.ErrConflict):
		s.writeMessageProblem(writer, http.StatusConflict, "conflict", "error.conflict")
	default:
		s.writeMessageProblem(writer, http.StatusInternalServerError, "internal", "error.internal")
	}
}
