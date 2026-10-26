package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
)

func (s *Server) personalSkills(w http.ResponseWriter, r *http.Request) {
	principal, err := s.identity.Identify(r)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", s.application.Authentication.Messages.Unauthenticated)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		skills, err := s.agentConfiguration.PersonalSkills(r.Context(), principal.ID())
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "personal_skills_unavailable", s.application.Authentication.Messages.InternalError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"skills": skills})
		return
	}
	var input agentconfiguration.CreatePersonalSkillInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_personal_skill", s.application.Content.Messages["ui.workspace.skills.content_invalid"])
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeProblem(w, http.StatusBadRequest, "invalid_personal_skill", s.application.Content.Messages["ui.workspace.skills.content_invalid"])
		return
	}
	skill, err := s.agentConfiguration.CreatePersonalSkill(r.Context(), principal.ID(), input)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, agentconfiguration.ErrInvalidPersonalSkill):
			status = http.StatusBadRequest
		case errors.Is(err, agentconfiguration.ErrPersonalSkillLimit):
			status = http.StatusConflict
		}
		message := s.application.Content.Messages["ui.workspace.skills.save_failed"]
		if errors.Is(err, agentconfiguration.ErrInvalidPersonalSkill) {
			message = s.application.Content.Messages["ui.workspace.skills.content_invalid"]
		}
		writeProblem(w, status, "personal_skill_create_failed", message)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"skill": skill})
}

func (s *Server) deletePersonalSkill(w http.ResponseWriter, r *http.Request) {
	principal, err := s.identity.Identify(r)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", s.application.Authentication.Messages.Unauthenticated)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	err = s.agentConfiguration.DeletePersonalSkill(r.Context(), principal.ID(), r.PathValue("skillID"))
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, agentconfiguration.ErrPersonalSkillNotFound):
		writeProblem(w, http.StatusNotFound, "personal_skill_not_found", s.application.Content.Messages["ui.workspace.skills.delete_failed"])
	default:
		s.logger.Error("删除个人 Skill 失败", "owner_id", principal.ID(), "error", err)
		writeProblem(w, http.StatusInternalServerError, "personal_skill_delete_failed", s.application.Content.Messages["ui.workspace.skills.delete_failed"])
	}
}
