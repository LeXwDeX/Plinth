package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/skilldraft"
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
		if errors.Is(err, agentconfiguration.ErrInvalidPersonalSkill) {
			s.writeInvalidPersonalSkill(w, err)
			return
		}
		writeProblem(w, status, "personal_skill_create_failed", s.application.Content.Messages["ui.workspace.skills.save_failed"], err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"skill": skill})
}

func (s *Server) updatePersonalSkill(w http.ResponseWriter, r *http.Request) {
	principal, err := s.identity.Identify(r)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", s.application.Authentication.Messages.Unauthenticated)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
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
	skill, err := s.agentConfiguration.UpdatePersonalSkill(r.Context(), principal.ID(), r.PathValue("skillID"), input)
	s.writePersonalSkillChange(w, principal.ID(), skill, err)
}

// patchPersonalSkill changes only the members the body carries (#548).
func (s *Server) patchPersonalSkill(w http.ResponseWriter, r *http.Request) {
	principal, err := s.identity.Identify(r)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", s.application.Authentication.Messages.Unauthenticated)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var input agentconfiguration.PatchPersonalSkillInput
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
	skill, err := s.agentConfiguration.PatchPersonalSkill(r.Context(), principal.ID(), r.PathValue("skillID"), input)
	s.writePersonalSkillChange(w, principal.ID(), skill, err)
}

// personalSkillFieldMessages are the Schema messages of a field the
// validator names (#548); any other invalid content keeps the general one.
var personalSkillFieldMessages = map[string]string{
	agentconfiguration.PersonalSkillFieldName:        "ui.workspace.skills.name_invalid",
	agentconfiguration.PersonalSkillFieldPrompt:      "ui.workspace.skills.body_invalid",
	agentconfiguration.PersonalSkillFieldSlug:        "ui.workspace.skills.slug_invalid",
	agentconfiguration.PersonalSkillFieldDescription: "ui.workspace.skills.description_invalid",
}

// writeInvalidPersonalSkill answers 400 with the field the validator named:
// the code is invalid_personal_skill_<field> so a client can show the error
// next to that field.
func (s *Server) writeInvalidPersonalSkill(w http.ResponseWriter, err error) {
	field := agentconfiguration.InvalidPersonalSkillField(err)
	if key, named := personalSkillFieldMessages[field]; named {
		writeProblem(w, http.StatusBadRequest, "invalid_personal_skill_"+field, s.application.Content.Messages[key], err)
		return
	}
	writeProblem(w, http.StatusBadRequest, "invalid_personal_skill", s.application.Content.Messages["ui.workspace.skills.content_invalid"], err)
}

func (s *Server) writePersonalSkillChange(w http.ResponseWriter, owner string, skill catalog.SkillSchema, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"skill": skill})
	case errors.Is(err, agentconfiguration.ErrInvalidPersonalSkill):
		s.writeInvalidPersonalSkill(w, err)
	case errors.Is(err, agentconfiguration.ErrPersonalSkillNotFound):
		writeProblem(w, http.StatusNotFound, "personal_skill_not_found", s.application.Content.Messages["ui.workspace.skills.not_found"])
	default:
		s.logger.Error("更新个人 Skill 失败", "owner_id", owner, "error", err)
		writeProblem(w, http.StatusInternalServerError, "personal_skill_update_failed", s.application.Content.Messages["ui.workspace.skills.save_failed"])
	}
}

// draftPersonalSkill drafts a Skill from a name and a requirement with one
// chat model call (#548). Nothing is saved; the person reviews the draft and
// saves it through the creation route. A failure is one readable message. The
// optional session_id names the owner's session the draft was opened from: the
// call runs on that session's chat model (#619), and a used-up quota on it is
// the Schema's own answer, so the person can switch the model and retry.
func (s *Server) draftPersonalSkill(w http.ResponseWriter, r *http.Request) {
	principal, err := s.identity.Identify(r)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", s.application.Authentication.Messages.Unauthenticated)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var input struct {
		Name        string `json:"name"`
		Requirement string `json:"requirement"`
		SessionID   string `json:"session_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeProblem(w, http.StatusBadRequest, "invalid_skill_draft", s.application.Content.Messages["ui.workspace.skills.draft_invalid"])
		return
	}
	taken, err := s.agentConfiguration.TakenSkillSlugs(r.Context(), principal.ID())
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "skill_draft_failed", s.application.Content.Messages["ui.workspace.skills.draft_failed"], err)
		return
	}
	locale := requestLocale(r)
	authoring := s.application.Chat.Skills.Authoring
	authoring.SystemPrompt = s.textIn(locale, authoring.SystemPrompt)
	authoring.UserTemplate = s.textIn(locale, authoring.UserTemplate)
	draft, err := s.skillDrafter.Draft(r.Context(), skilldraft.Request{
		Name: input.Name, Requirement: input.Requirement, Authoring: authoring, Taken: taken,
		OwnerID: principal.ID(), SessionID: input.SessionID,
	})
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"draft": draft})
	case errors.Is(err, skilldraft.ErrInvalidRequest):
		writeProblem(w, http.StatusBadRequest, "invalid_skill_draft", s.application.Content.Messages["ui.workspace.skills.draft_invalid"], err)
	case errors.Is(err, agents.ErrNotConfigured):
		writeProblem(w, http.StatusServiceUnavailable, "agent_not_configured", s.application.Chat.Presentation.Messages.AgentNotConfigured, err)
	case errors.Is(err, chat.ErrSessionNotFound):
		writeProblem(w, http.StatusNotFound, "session_not_found", s.application.AgentConfiguration.UI["session_not_found"], err)
	case errors.Is(err, agents.ErrQuotaExhausted):
		s.logger.Warn("起草个人 Skill 失败：对话模型额度已用完", "owner_id", principal.ID(), "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "skill_draft_quota_exhausted", s.application.Content.Messages["ui.workspace.skills.draft_quota_exhausted"], err)
	default:
		s.logger.Warn("起草个人 Skill 失败", "owner_id", principal.ID(), "error", err)
		writeProblem(w, http.StatusBadGateway, "skill_draft_failed", s.application.Content.Messages["ui.workspace.skills.draft_failed"], err)
	}
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
