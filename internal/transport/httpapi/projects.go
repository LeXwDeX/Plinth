package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// The project wire names are stable product contract names. The Catalog
// publishes the routes and limits; these members describe the objects those
// routes read and write.
const (
	projectFieldID          = "id"
	projectFieldName        = "name"
	projectFieldCreatedAt   = "created_at"
	projectFieldUpdatedAt   = "updated_at"
	projectFieldProjectID   = "project_id"
	projectFieldKind        = "kind"
	projectFieldDescription = "description"
	projectFieldImages      = "images"
	projectFieldModels      = "models"
	projectFieldEnabled     = "enabled"
	projectFieldText        = "text"
	projectFieldPalette     = "palette"
	projectFieldReferences  = "references"
	projectFieldBrief       = "brief"
	projectFieldTexts       = "texts"
	projectFieldSource      = "source"
	projectFieldProjects    = "projects"
	projectFieldAssets      = "assets"
)

func (s *Server) projectMessages() catalog.ProjectMessagesSchema {
	if s.application.Project == nil {
		return catalog.ProjectMessagesSchema{}
	}
	return s.application.Project.Messages
}

func (s *Server) projectUnknownMessage() string {
	if message := s.projectMessages().UnknownProject; message != "" {
		return message
	}
	return s.application.Chat.Presentation.Messages.InvalidParameters
}

func projectRender(template string, maximum int, projectName string) string {
	return strings.NewReplacer(
		"{maximum}", strconv.Itoa(maximum),
		"{project}", projectName,
	).Replace(template)
}

func (s *Server) projectServiceOrError(writer http.ResponseWriter) bool {
	if s.projects != nil {
		return true
	}
	s.writeChatInternalError(writer, errors.New("httpapi: project service unavailable"))
	return false
}

func (s *Server) decodeProjectBody(writer http.ResponseWriter, request *http.Request) (map[string]json.RawMessage, bool) {
	request.Body = http.MaxBytesReader(writer, request.Body, s.application.Runtime.HTTP.JSONBodyMaxBytes)
	decoder := json.NewDecoder(request.Body)
	var body map[string]json.RawMessage
	if err := decoder.Decode(&body); err != nil || body == nil {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return nil, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return nil, false
	}
	return body, true
}

func projectAllowedFields(body map[string]json.RawMessage, allowed ...string) bool {
	allowedSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = true
	}
	for name := range body {
		if !allowedSet[name] {
			return false
		}
	}
	return true
}

func projectBodyString(raw json.RawMessage, target *string) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	return json.Unmarshal(raw, target) == nil
}

func projectBodyStrings(raw json.RawMessage, target *[]string) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	return json.Unmarshal(raw, target) == nil && *target != nil
}

func projectBodyReferences(raw json.RawMessage, target *[]project.Reference) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	return json.Unmarshal(raw, target) == nil && *target != nil
}

func projectWire(value project.Project) map[string]any {
	return map[string]any{
		projectFieldID:        value.ID,
		projectFieldName:      value.Name,
		projectFieldCreatedAt: value.CreatedAt,
		projectFieldUpdatedAt: value.UpdatedAt,
	}
}

func projectAssetWire(value project.Asset) map[string]any {
	return map[string]any{
		projectFieldID:          value.ID,
		projectFieldProjectID:   value.ProjectID,
		projectFieldKind:        value.Kind,
		projectFieldName:        value.Name,
		projectFieldDescription: value.Description,
		projectFieldImages:      append([]project.Reference(nil), value.Images...),
		projectFieldModels:      append([]project.Reference(nil), value.Models...),
		projectFieldCreatedAt:   value.CreatedAt,
		projectFieldUpdatedAt:   value.UpdatedAt,
	}
}

func projectStyleGuideWire(value project.StyleGuide) map[string]any {
	return map[string]any{
		projectFieldProjectID:  value.ProjectID,
		projectFieldEnabled:    value.Enabled,
		projectFieldText:       value.Text,
		projectFieldPalette:    append([]string(nil), value.Palette...),
		projectFieldReferences: append([]project.Reference(nil), value.References...),
		projectFieldUpdatedAt:  value.UpdatedAt,
	}
}

func projectConventionsWire(value project.Conventions) map[string]any {
	brief := value.Brief
	if len(brief) == 0 {
		brief = []byte("{}")
	}
	return map[string]any{
		projectFieldProjectID: value.ProjectID,
		projectFieldBrief:     json.RawMessage(brief),
		projectFieldTexts:     value.Texts,
		projectFieldUpdatedAt: value.UpdatedAt,
	}
}

// projectErrorScope selects the bound and copy one handler reports. Project
// and asset operations share the same sentinels but have different maxima.
type projectErrorScope struct {
	asset            bool
	nameMaximum      int
	limitMessage     string
	limitMaximum     int
	referenceMaximum int
}

func (s *Server) projectScopeProject() projectErrorScope {
	if s.application.Project == nil {
		return projectErrorScope{limitMessage: s.application.Chat.Presentation.Messages.InvalidParameters}
	}
	return projectErrorScope{
		nameMaximum:      s.application.Project.Limits.MaximumNameCharacters,
		limitMessage:     s.application.Project.Messages.ProjectLimit,
		limitMaximum:     s.application.Project.Limits.MaximumProjectsPerOwner,
		referenceMaximum: s.application.Project.StyleGuide.MaximumReferences,
	}
}

func (s *Server) projectScopeAsset() projectErrorScope {
	if s.application.Project == nil {
		return projectErrorScope{asset: true, limitMessage: s.application.Chat.Presentation.Messages.InvalidParameters}
	}
	return projectErrorScope{
		asset:            true,
		nameMaximum:      s.application.Project.Limits.MaximumAssetNameCharacters,
		limitMessage:     s.application.Project.Messages.AssetLimit,
		limitMaximum:     s.application.Project.Limits.MaximumAssetsPerProject,
		referenceMaximum: s.application.Project.Limits.MaximumAssetImages,
	}
}

func (s *Server) projectScopeStyleGuide() projectErrorScope {
	if s.application.Project == nil {
		return projectErrorScope{limitMessage: s.application.Chat.Presentation.Messages.InvalidParameters}
	}
	return projectErrorScope{
		limitMessage:     s.application.Project.Messages.PaletteColorInvalid,
		limitMaximum:     s.application.Project.StyleGuide.MaximumPaletteColors,
		referenceMaximum: s.application.Project.StyleGuide.MaximumReferences,
	}
}

func (s *Server) writeProjectError(writer http.ResponseWriter, err error, scope projectErrorScope) {
	noteCause(writer, err)
	messages := s.projectMessages()
	status := http.StatusBadRequest
	message := ""
	switch {
	case errors.Is(err, project.ErrNotFound):
		status = http.StatusNotFound
		if scope.asset {
			message = messages.AssetNotFound
		} else {
			message = messages.UnknownProject
		}
	case errors.Is(err, project.ErrNameRequired):
		if scope.asset && messages.AssetNameRequired != "" {
			message = messages.AssetNameRequired
		} else {
			message = messages.NameRequired
		}
	case errors.Is(err, project.ErrNameTooLong):
		template := messages.NameTooLong
		if scope.asset && messages.AssetNameTooLong != "" {
			template = messages.AssetNameTooLong
		}
		message = projectRender(template, scope.nameMaximum, "")
	case errors.Is(err, project.ErrLimit):
		message = projectRender(scope.limitMessage, scope.limitMaximum, "")
	case errors.Is(err, project.ErrStyleTextTooLong):
		message = projectRender(messages.StyleTextTooLong, s.application.Project.StyleGuide.MaximumTextCharacters, "")
	case errors.Is(err, project.ErrPaletteInvalid):
		message = messages.PaletteColorInvalid
	case errors.Is(err, project.ErrReferenceLimit):
		message = projectRender(messages.ReferenceLimit, scope.referenceMaximum, "")
	case errors.Is(err, project.ErrReferenceInvalid):
		message = messages.ReferenceInvalid
	case errors.Is(err, project.ErrInvalid):
		message = s.application.Chat.Presentation.Messages.InvalidParameters
	default:
		s.writeChatInternalError(writer, err)
		return
	}
	if message == "" {
		message = s.application.Chat.Presentation.Messages.InvalidParameters
	}
	s.writeChatError(writer, status, message)
}

func (s *Server) auditProject(request *http.Request, principal access.Principal, action, resourceType, resourceID string) {
	if action == "" || s.administrationAudit == nil {
		return
	}
	s.recordProductAudit(request, administration.AuditEvent{
		Action:       action,
		ActorID:      principal.ID(),
		ActorName:    principal.Claims.Name,
		ResourceType: resourceType,
		ResourceID:   resourceID,
	})
}

func (s *Server) projectAudit() catalog.ProjectAuditSchema {
	if s.application.Project == nil {
		return catalog.ProjectAuditSchema{}
	}
	return s.application.Project.Audit
}

func (s *Server) listProjects(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	if !s.projectServiceOrError(writer) {
		return
	}
	values, err := s.projects.List(request.Context(), principal.ID())
	if err != nil {
		s.writeProjectError(writer, err, s.projectScopeProject())
		return
	}
	items := make([]map[string]any, 0, len(values))
	for _, value := range values {
		items = append(items, projectWire(value))
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, map[string]any{projectFieldProjects: items})
}

func (s *Server) createProject(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	if !s.projectServiceOrError(writer) {
		return
	}
	body, ok := s.decodeProjectBody(writer, request)
	if !ok || !projectAllowedFields(body, projectFieldName) {
		if ok {
			s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		}
		return
	}
	var name string
	if !projectBodyString(body[projectFieldName], &name) {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	value, err := s.projects.Create(request.Context(), principal.ID(), name)
	if err != nil {
		s.writeProjectError(writer, err, s.projectScopeProject())
		return
	}
	audit := s.projectAudit()
	s.auditProject(request, principal, audit.CreateAction, audit.ProjectResourceType, value.ID)
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusCreated, projectWire(value))
}

func (s *Server) getProject(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	if !s.projectServiceOrError(writer) {
		return
	}
	value, err := s.projects.Get(request.Context(), principal.ID(), request.PathValue("projectID"))
	if err != nil {
		s.writeProjectError(writer, err, s.projectScopeProject())
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, projectWire(value))
}

func (s *Server) updateProject(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	if !s.projectServiceOrError(writer) {
		return
	}
	body, ok := s.decodeProjectBody(writer, request)
	if !ok || !projectAllowedFields(body, projectFieldName) {
		if ok {
			s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		}
		return
	}
	var name string
	if !projectBodyString(body[projectFieldName], &name) {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	value, err := s.projects.Rename(request.Context(), principal.ID(), request.PathValue("projectID"), name)
	if err != nil {
		s.writeProjectError(writer, err, s.projectScopeProject())
		return
	}
	audit := s.projectAudit()
	s.auditProject(request, principal, audit.UpdateAction, audit.ProjectResourceType, value.ID)
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, projectWire(value))
}

func (s *Server) deleteProject(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	if !s.projectServiceOrError(writer) {
		return
	}
	projectID := request.PathValue("projectID")
	if err := s.projects.Delete(request.Context(), principal.ID(), projectID); err != nil {
		s.writeProjectError(writer, err, s.projectScopeProject())
		return
	}
	audit := s.projectAudit()
	s.auditProject(request, principal, audit.DeleteAction, audit.ProjectResourceType, projectID)
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) getProjectStyleGuide(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	if !s.projectServiceOrError(writer) {
		return
	}
	value, err := s.projects.StyleGuide(request.Context(), principal.ID(), request.PathValue("projectID"))
	if err != nil {
		s.writeProjectError(writer, err, s.projectScopeProject())
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, projectStyleGuideWire(value))
}

func (s *Server) putProjectStyleGuide(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	if !s.projectServiceOrError(writer) {
		return
	}
	body, ok := s.decodeProjectBody(writer, request)
	if !ok {
		return
	}
	if !projectAllowedFields(body, projectFieldEnabled, projectFieldText, projectFieldPalette, projectFieldReferences) {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	var enabled bool
	var text string
	var palette []string
	var references []project.Reference
	if raw, present := body[projectFieldEnabled]; !present || string(raw) == "null" || json.Unmarshal(raw, &enabled) != nil ||
		!projectBodyString(body[projectFieldText], &text) ||
		!projectBodyStrings(body[projectFieldPalette], &palette) ||
		!projectBodyReferences(body[projectFieldReferences], &references) {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	value, err := s.projects.SetStyleGuide(request.Context(), principal.ID(), request.PathValue("projectID"), project.StyleGuide{
		Enabled: enabled, Text: text, Palette: palette, References: references,
	})
	if err != nil {
		s.writeProjectError(writer, err, s.projectScopeStyleGuide())
		return
	}
	audit := s.projectAudit()
	s.auditProject(request, principal, audit.UpdateAction, audit.ProjectResourceType, request.PathValue("projectID"))
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, projectStyleGuideWire(value))
}

func (s *Server) getProjectConventions(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	if !s.projectServiceOrError(writer) {
		return
	}
	value, err := s.projects.Conventions(request.Context(), principal.ID(), request.PathValue("projectID"))
	if err != nil {
		s.writeProjectError(writer, err, s.projectScopeProject())
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, projectConventionsWire(value))
}

func (s *Server) putProjectConventions(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	if !s.projectServiceOrError(writer) {
		return
	}
	body, ok := s.decodeProjectBody(writer, request)
	if !ok {
		return
	}
	if !projectAllowedFields(body, projectFieldBrief, projectFieldTexts) {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	var brief json.RawMessage
	var texts map[string]string
	if raw, present := body[projectFieldBrief]; present {
		brief = append(json.RawMessage(nil), raw...)
	}
	if raw, present := body[projectFieldTexts]; present {
		if string(raw) == "null" || json.Unmarshal(raw, &texts) != nil {
			s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
			return
		}
	}
	value, err := s.projects.SetConventions(request.Context(), principal.ID(), request.PathValue("projectID"), project.Conventions{
		Brief: brief, Texts: texts,
	})
	if err != nil {
		s.writeProjectError(writer, err, s.projectScopeProject())
		return
	}
	audit := s.projectAudit()
	s.auditProject(request, principal, audit.UpdateAction, audit.ProjectResourceType, request.PathValue("projectID"))
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, projectConventionsWire(value))
}

func (s *Server) listProjectAssets(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	if !s.projectServiceOrError(writer) {
		return
	}
	values, err := s.projects.ListAssets(request.Context(), principal.ID(), request.PathValue("projectID"))
	if err != nil {
		s.writeProjectError(writer, err, s.projectScopeProject())
		return
	}
	items := make([]map[string]any, 0, len(values))
	for _, value := range values {
		items = append(items, projectAssetWire(value))
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, map[string]any{projectFieldAssets: items})
}

func (s *Server) createProjectAsset(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	if !s.projectServiceOrError(writer) {
		return
	}
	body, ok := s.decodeProjectBody(writer, request)
	if !ok {
		return
	}
	if !projectAllowedFields(body, projectFieldKind, projectFieldName, projectFieldDescription, projectFieldImages, projectFieldModels) {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	var kind, name, description string
	var images, models []project.Reference
	if !projectBodyString(body[projectFieldKind], &kind) || !projectBodyString(body[projectFieldName], &name) {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	if raw, present := body[projectFieldDescription]; present && !projectBodyString(raw, &description) {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	if raw, present := body[projectFieldImages]; present && !projectBodyReferences(raw, &images) {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	if raw, present := body[projectFieldModels]; present && !projectBodyReferences(raw, &models) {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	value, err := s.projects.CreateAsset(request.Context(), principal.ID(), request.PathValue("projectID"), project.AssetInput{
		Kind: kind, Name: name, Description: description, Images: images, Models: models,
	})
	if err != nil {
		s.writeProjectError(writer, err, s.projectScopeAsset())
		return
	}
	audit := s.projectAudit()
	s.auditProject(request, principal, audit.CreateAction, audit.AssetResourceType, value.ID)
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusCreated, projectAssetWire(value))
}

func (s *Server) updateProjectAsset(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	if !s.projectServiceOrError(writer) {
		return
	}
	body, ok := s.decodeProjectBody(writer, request)
	if !ok {
		return
	}
	if !projectAllowedFields(body, projectFieldKind, projectFieldName, projectFieldDescription) {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	var update project.AssetUpdate
	if raw, present := body[projectFieldKind]; present {
		var kind string
		if !projectBodyString(raw, &kind) {
			s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
			return
		}
		update.Kind = &kind
	}
	if raw, present := body[projectFieldName]; present {
		var name string
		if !projectBodyString(raw, &name) {
			s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
			return
		}
		update.Name = &name
	}
	if raw, present := body[projectFieldDescription]; present {
		var description string
		if !projectBodyString(raw, &description) {
			s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
			return
		}
		update.Description = &description
	}
	if update.Kind == nil && update.Name == nil && update.Description == nil {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	value, err := s.projects.UpdateAsset(request.Context(), principal.ID(), request.PathValue("projectID"), request.PathValue("assetID"), update)
	if err != nil {
		s.writeProjectError(writer, err, s.projectScopeAsset())
		return
	}
	audit := s.projectAudit()
	s.auditProject(request, principal, audit.UpdateAction, audit.AssetResourceType, value.ID)
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, projectAssetWire(value))
}

func (s *Server) deleteProjectAsset(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	if !s.projectServiceOrError(writer) {
		return
	}
	projectID := request.PathValue("projectID")
	assetID := request.PathValue("assetID")
	if err := s.projects.DeleteAsset(request.Context(), principal.ID(), projectID, assetID); err != nil {
		s.writeProjectError(writer, err, s.projectScopeAsset())
		return
	}
	audit := s.projectAudit()
	s.auditProject(request, principal, audit.DeleteAction, audit.AssetResourceType, assetID)
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) promoteProjectAsset(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	if !s.projectServiceOrError(writer) {
		return
	}
	body, ok := s.decodeProjectBody(writer, request)
	if !ok {
		return
	}
	if !projectAllowedFields(body, projectFieldSource, projectFieldKind, projectFieldName, projectFieldDescription) {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	var kind, name, description string
	var source project.Reference
	if raw, present := body[projectFieldSource]; !present || string(raw) == "null" || json.Unmarshal(raw, &source) != nil {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	if !projectBodyString(body[projectFieldKind], &kind) || !projectBodyString(body[projectFieldName], &name) {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	if raw, present := body[projectFieldDescription]; present && !projectBodyString(raw, &description) {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	value, err := s.projects.Promote(request.Context(), principal.ID(), request.PathValue("projectID"), project.PromoteInput{
		Kind: kind, Name: name, Description: description, Source: source,
	})
	if err != nil {
		if errors.Is(err, project.ErrReferenceInvalid) {
			message := s.projectMessages().PromotionSourceInvalid
			if message == "" {
				message = s.application.Chat.Presentation.Messages.InvalidParameters
			}
			noteCause(writer, err)
			s.writeChatError(writer, http.StatusBadRequest, message)
			return
		}
		s.writeProjectError(writer, err, s.projectScopeAsset())
		return
	}
	audit := s.projectAudit()
	s.auditProject(request, principal, audit.PromoteAction, audit.AssetResourceType, value.ID)
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusCreated, projectAssetWire(value))
}

func (s *Server) putChatSessionProject(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	if !s.projectServiceOrError(writer) {
		return
	}
	body, ok := s.decodeProjectBody(writer, request)
	if !ok {
		return
	}
	if !projectAllowedFields(body, projectFieldProjectID) {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	var projectID string
	if !projectBodyString(body[projectFieldProjectID], &projectID) {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	session, err := s.chat.SetSessionProject(request.Context(), principal.ID(), request.PathValue("sessionID"), projectID)
	switch {
	case errors.Is(err, chat.ErrProjectNotFound):
		s.writeChatError(writer, http.StatusNotFound, s.projectUnknownMessage())
		return
	case errors.Is(err, chat.ErrSessionNotFound):
		s.writeChatError(writer, http.StatusNotFound, s.application.Chat.Presentation.Messages.SessionNotFound)
		return
	case err != nil:
		s.writeChatInternalError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatSessionWire(request.Context(), writer, session, false))
}
