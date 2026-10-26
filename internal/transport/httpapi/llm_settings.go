package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/llmsettings"
)

// Administrator LLM settings (#322 Scope 2). Same rules as the Agent
// configuration: the Schema permission guards every route, every change and
// every connection test is audited, and every change names the revision it
// was based on. Responses carry whether each connection variable is set,
// never a value; a test answers with a classified outcome only.

func (s *Server) registerLLMSettingsContracts() {
	if s.llmSettings == nil || s.application.LLMSettings == nil {
		return
	}
	routes := s.application.LLMSettings.Routes
	s.mux.HandleFunc("GET "+routes.Admin, s.manageLLMSettings)
	for _, path := range []string{routes.Publish, routes.Restore, routes.Reset, routes.Test} {
		s.mux.HandleFunc("POST "+path, s.manageLLMSettings)
	}
}

// llmSettingsText is Schema copy; writeProblem sends it in the request's
// locale.
func (s *Server) llmSettingsText(_ *http.Request, key string) string {
	return s.application.LLMSettings.UI[key]
}

func (s *Server) manageLLMSettings(w http.ResponseWriter, r *http.Request) {
	principal, err := s.identity.Identify(r)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", s.application.Authentication.Messages.Unauthenticated)
		return
	}
	if !principal.Can(access.Permission(s.application.LLMSettings.Permission)) {
		writeProblem(w, http.StatusForbidden, "forbidden", s.application.Authentication.Messages.Forbidden)
		return
	}
	if r.Method == http.MethodGet {
		s.writeLLMSettings(w, r)
		return
	}
	var body struct {
		ExpectedRevision string               `json:"expected_revision"`
		TargetRevision   string               `json:"target_revision"`
		Settings         llmsettings.Settings `json:"settings"`
		ConnectionID     string               `json:"connection_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeProblem(w, http.StatusBadRequest, "invalid_settings", s.llmSettingsText(r, "invalid_settings"))
		return
	}
	routes := s.application.LLMSettings.Routes
	if r.URL.Path == routes.Test {
		check, err := s.llmSettings.Test(r.Context(), body.ConnectionID, principal.ID())
		if errors.Is(err, llmsettings.ErrUnknownConnection) {
			writeProblem(w, http.StatusNotFound, "connection_not_found", s.llmSettingsText(r, "test_failed"))
			return
		}
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "connection_test_failed", s.llmSettingsText(r, "test_failed"), err)
			return
		}
		s.recordProductAudit(r, administration.AuditEvent{Action: s.application.LLMSettings.TestAuditAction, ActorID: principal.ID(),
			ActorName: principal.Claims.Name, ResourceType: "llm_connection", ResourceID: check.ConnectionID,
			Detail: map[string]any{"model": check.Model, "outcome": check.Outcome, "reachable": check.Reachable, "vision": check.Vision}})
		s.writeLLMSettings(w, r)
		return
	}
	var changed llmsettings.Version
	switch r.URL.Path {
	case routes.Publish:
		changed, err = s.llmSettings.Publish(r.Context(), body.ExpectedRevision, principal.ID(), llmsettings.SourcePublish, body.Settings)
	case routes.Restore:
		changed, err = s.llmSettings.Restore(r.Context(), body.ExpectedRevision, body.TargetRevision, principal.ID())
	case routes.Reset:
		changed, err = s.llmSettings.Restore(r.Context(), body.ExpectedRevision, "0", principal.ID())
	}
	switch {
	case errors.Is(err, llmsettings.ErrConflict):
		writeProblem(w, http.StatusConflict, "settings_conflict", s.llmSettingsText(r, "conflict"), err)
		return
	case errors.Is(err, llmsettings.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "settings_version_not_found", s.llmSettingsText(r, "save_failed"), err)
		return
	case errors.Is(err, llmsettings.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid_settings", s.llmSettingsText(r, "invalid_settings"), err)
		return
	case err != nil:
		writeProblem(w, http.StatusInternalServerError, "settings_update_failed", s.llmSettingsText(r, "save_failed"), err)
		return
	}
	s.recordProductAudit(r, administration.AuditEvent{Action: s.application.LLMSettings.AuditAction, ActorID: principal.ID(),
		ActorName: principal.Claims.Name, ResourceType: "llm_settings", ResourceID: changed.Revision,
		Detail: map[string]any{"revision": changed.Revision, "previous_revision": body.ExpectedRevision, "source": changed.Source,
			"default_model": changed.Settings.DefaultModel}})
	s.writeLLMSettings(w, r)
}

func (s *Server) writeLLMSettings(w http.ResponseWriter, r *http.Request) {
	management, err := s.llmSettings.Management(r.Context())
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "settings_unavailable", s.llmSettingsText(r, "load_failed"), err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, management)
}
