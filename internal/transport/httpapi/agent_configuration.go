package httpapi

import (
	"encoding/json"
	"errors"
	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"io"
	"net/http"
	"slices"
)

func (s *Server) registerAgentConfigurationContracts() {
	if s.agentConfiguration == nil {
		return
	}
	r := s.application.AgentConfiguration.Routes
	s.mux.HandleFunc("GET "+r.Admin, s.manageAgentConfiguration)
	for _, path := range []string{r.Validate, r.Publish, r.Restore, r.Reset} {
		s.mux.HandleFunc("POST "+path, s.manageAgentConfiguration)
	}
	s.mux.HandleFunc("GET "+r.Effective, s.effectiveAgentConfiguration)
	creation := s.application.Chat.Skills.Creation.Route
	if creation != "" {
		s.mux.HandleFunc("GET "+creation, s.personalSkills)
		s.mux.HandleFunc("POST "+creation, s.personalSkills)
	}
	if item := s.application.Chat.Skills.Creation.ItemRoute; item != "" {
		s.mux.HandleFunc("DELETE "+item, s.deletePersonalSkill)
	}
}
func (s *Server) manageAgentConfiguration(w http.ResponseWriter, r *http.Request) {
	p, err := s.identity.Identify(r)
	if err != nil {
		writeProblem(w, 401, "unauthenticated", s.application.Authentication.Messages.Unauthenticated)
		return
	}
	if !p.Can(access.Permission(s.application.AgentConfiguration.Permission)) {
		writeProblem(w, 403, "forbidden", s.application.Authentication.Messages.Forbidden)
		return
	}
	if r.Method == "GET" {
		s.writeManagedAgentConfiguration(w, r)
		return
	}
	var body struct {
		ExpectedRevision string                           `json:"expected_revision"`
		TargetRevision   string                           `json:"target_revision"`
		Configuration    agentconfiguration.Configuration `json:"configuration"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeProblem(w, 400, "invalid_configuration", s.application.AgentConfiguration.UI["invalid_configuration"])
		return
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		writeProblem(w, 400, "invalid_configuration", s.application.AgentConfiguration.UI["invalid_configuration"])
		return
	}
	routes := s.application.AgentConfiguration.Routes
	if r.URL.Path == routes.Validate {
		if err := s.agentConfiguration.Validate(body.Configuration); err != nil {
			writeProblem(w, 400, "invalid_configuration", err.Error())
			return
		}
		prompts := map[string]string{}
		for _, domain := range []string{"image", "model-3d"} {
			prompts[domain] = body.Configuration.SystemPrompt
			if text := body.Configuration.DomainContext[domain]; text != "" {
				prompts[domain] += "\n\n" + text
			}
		}
		writeJSON(w, 200, map[string]any{"valid": true, "effective_prompts": prompts})
		return
	}
	var changed agentconfiguration.Snapshot
	switch r.URL.Path {
	case routes.Publish:
		changed, err = s.agentConfiguration.Publish(r.Context(), body.ExpectedRevision, p.ID(), "publish", body.Configuration)
	case routes.Restore:
		changed, err = s.agentConfiguration.Restore(r.Context(), body.ExpectedRevision, body.TargetRevision, p.ID())
	case routes.Reset:
		changed, err = s.agentConfiguration.Restore(r.Context(), body.ExpectedRevision, "0", p.ID())
	}
	if err != nil {
		status := 400
		if errors.Is(err, agentconfiguration.ErrConflict) {
			status = 409
		}
		if errors.Is(err, agentconfiguration.ErrNotFound) {
			status = 404
		}
		writeProblem(w, status, "configuration_update_failed", err.Error())
		return
	}
	s.recordProductAudit(r, administration.AuditEvent{Action: s.application.AgentConfiguration.AuditAction, ActorID: p.ID(), ActorName: p.Claims.Name, ResourceType: "agent_configuration", ResourceID: changed.Revision, Detail: map[string]any{"revision": changed.Revision, "previous_revision": body.ExpectedRevision}})
	s.writeManagedAgentConfiguration(w, r)
}
func (s *Server) writeManagedAgentConfiguration(w http.ResponseWriter, r *http.Request) {
	result, err := s.agentConfiguration.Management(r.Context())
	if err != nil {
		writeProblem(w, 500, "configuration_unavailable", s.application.Authentication.Messages.InternalError, err)
		return
	}

	locale := requestLocale(r)
	for i := range result.MCPPresets {
		result.MCPPresets[i].Available = s.mcp != nil && s.mcp.PresetAvailable(result.MCPPresets[i].ID)
		// Preset names and tool descriptions are Schema copy shown to the
		// administrator; the published configuration itself stays as written.
		result.MCPPresets[i].Name = s.textIn(locale, result.MCPPresets[i].Name)
		result.MCPPresets[i].Description = s.textIn(locale, result.MCPPresets[i].Description)
	}
	for i := range result.Tools {
		result.Tools[i].Description = s.textIn(locale, result.Tools[i].Description)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, result)
}
func (s *Server) effectiveAgentConfiguration(w http.ResponseWriter, r *http.Request) {
	p, err := s.identity.Identify(r)
	if err != nil {
		writeProblem(w, 401, "unauthenticated", s.application.Authentication.Messages.Unauthenticated)
		return
	}
	session := r.URL.Query().Get("session_id")
	if session != "" && (s.ownsAgentSession == nil || !s.ownsAgentSession(r.Context(), p.ID(), session)) {
		writeProblem(w, 404, "session_not_found", s.application.AgentConfiguration.UI["session_not_found"])
		return
	}
	snap, err := s.agentConfiguration.SnapshotForOwner(r.Context(), p.ID())
	if err != nil {
		writeProblem(w, 500, "configuration_unavailable", s.application.AgentConfiguration.UI["load_failed"], err)
		return
	}
	domain := r.URL.Query().Get("creative_domain")
	if domain == "" {
		domain = "image"
	}
	if domain != "image" && domain != "model-3d" {
		writeProblem(w, 400, "invalid_domain", s.application.AgentConfiguration.UI["invalid_domain"])
		return
	}

	tools := []map[string]string{}
	for _, tool := range s.agentConfiguration.ToolSummaries() {
		if slices.Contains(snap.Configuration.EnabledToolNames, tool.Name) && slices.Contains(tool.Domains, domain) {
			tools = append(tools, map[string]string{"name": tool.Name, "description": s.textIn(requestLocale(r), tool.Description)})
		}
	}
	for _, preset := range s.application.AgentConfiguration.MCPPresets {
		if s.mcp != nil && s.mcp.PresetAvailable(preset.ID) && slices.Contains(snap.Configuration.EnabledMCPPresetIDs, preset.ID) {
			for _, tool := range preset.Tools {
				if slices.Contains(tool.Execution.Domains, domain) {
					tools = append(tools, map[string]string{"name": tool.Name, "description": s.textIn(requestLocale(r), tool.Description)})
				}
			}
		}
	}
	compatible := false
	model := r.URL.Query().Get("model_id")
	offerings := []generation.Offering{}
	if s.generation != nil {
		offerings = s.generation.Offerings("")
	}
	for _, offering := range offerings {
		if model != "" && string(offering.ModelID) != model {
			continue
		}
		if !s.generation.OfferingAvailable(offering.ID) || !slices.Contains(offering.Features, s.application.Chat.Skills.Feature) {
			continue
		}
		for _, tool := range s.application.Chat.Tools {
			if slices.Contains(tool.Execution.Domains, domain) && slices.Contains(tool.Execution.Capabilities, offering.Capability) && slices.Contains(snap.Configuration.EnabledToolNames, tool.Name) {
				compatible = true
			}
		}

	}
	skills := []map[string]any{}
	for _, skill := range snap.Configuration.Skills {
		if skill.Tool != "" {
			// A tool-binding Skill is available when its tool is enabled for this domain.
			if !slices.Contains(snap.Configuration.EnabledToolNames, skill.Tool) || !s.toolServesDomain(skill.Tool, domain) {
				continue
			}
			skills = append(skills, map[string]any{"id": skill.ID, "name": skill.Name, "name_zh": s.systemSkillText(r, skill, skill.NameZH), "category": skill.Category, "thumbnail_path": skill.ThumbnailPath, "recommended_size": skill.RecommendedSize, "tags": skill.Tags})
			continue
		}
		if !compatible {
			continue
		}
		skills = append(skills, map[string]any{"id": skill.ID, "name": skill.Name, "name_zh": skill.NameZH, "category": skill.Category, "thumbnail_path": skill.ThumbnailPath, "recommended_size": skill.RecommendedSize, "tags": skill.Tags})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"revision": snap.Revision, "tools": tools, "skills": skills, "model": map[string]string{"label": s.textIn(requestLocale(r), s.application.AgentConfiguration.ModelLabel)}, "parameter_source": s.textIn(requestLocale(r), s.application.AgentConfiguration.ParameterSource)})
}

// Keep SkillCatalog wire fields driven by Catalog while reading the latest content.
func (s *Server) effectiveSkillCatalog(r *http.Request, owner string) (catalog.SkillCatalogSchema, error) {
	if s.agentConfiguration == nil {
		return s.skillCatalog, nil
	}
	snapshot, err := s.agentConfiguration.SnapshotForOwner(r.Context(), owner)
	return catalog.SkillCatalogSchema{Revision: snapshot.Revision, Skills: snapshot.Configuration.Skills}, err
}

// toolServesDomain reports whether the Schema declares the tool for the domain.
func (s *Server) toolServesDomain(name, domain string) bool {
	for _, tool := range s.application.Chat.AgentTools {
		if tool.Name == name {
			return slices.Contains(tool.Execution.Domains, domain)
		}
	}
	for _, tool := range s.application.Chat.Tools {
		if tool.Name == name {
			return slices.Contains(tool.Execution.Domains, domain)
		}
	}
	return false
}

// systemSkillText is the request-locale copy of a system Skill's Schema text;
// personal and published Skills are person content and stay as written.
func (s *Server) systemSkillText(r *http.Request, skill catalog.SkillSchema, text string) string {
	if skill.Tool == "" {
		return text
	}
	return s.textIn(requestLocale(r), text)
}
