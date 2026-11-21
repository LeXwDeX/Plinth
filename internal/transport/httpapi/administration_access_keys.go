package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

type administrationAccessKeyHandler func(http.ResponseWriter, *http.Request, access.Principal)

// registerAdministrationAccessKeyContracts serves the administrator's view of
// every person's access keys (#332): the paginated list with usage, the
// revoke, and the service's overall switch. The routes are registered only
// when the Catalog declares the view and the service is assembled.
func (s *Server) registerAdministrationAccessKeyContracts() {
	routes := s.application.Runtime.HTTP.LegacyRoutes
	if routes.AdminAccessKeys == "" || routes.AdminAccessKey == "" || routes.AdminAccessKeysSwitch == "" ||
		s.administrationAccessKeys == nil || !s.accessKeyAdministrationDeclared() {
		return
	}
	s.mux.HandleFunc("GET "+routes.AdminAccessKeys, s.withAdministrationAccessKeyPrincipal(s.listAdministrationAccessKeys))
	s.mux.HandleFunc("DELETE "+routes.AdminAccessKey, s.withAdministrationAccessKeyPrincipal(s.revokeAdministrationAccessKey))
	s.mux.HandleFunc("PUT "+routes.AdminAccessKeysSwitch, s.withAdministrationAccessKeyPrincipal(s.setAdministrationAccessKeysSwitch))
}

func (s *Server) accessKeyAdministrationDeclared() bool {
	return strings.TrimSpace(s.application.Administration.AccessKeys.Permission) != ""
}

func (s *Server) withAdministrationAccessKeyPrincipal(next administrationAccessKeyHandler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		principal, err := s.identity.Identify(request)
		if err != nil {
			s.writeAdministrationAccessKeyError(writer, http.StatusUnauthorized,
				s.application.Authentication.Messages.Unauthenticated)
			return
		}
		permission := access.Permission(s.application.Administration.AccessKeys.Permission)
		if !principal.Can(permission) {
			s.writeAdministrationAccessKeyError(writer, http.StatusForbidden,
				s.application.Authentication.Messages.Forbidden)
			return
		}
		next(writer, request, principal)
	}
}

func (s *Server) listAdministrationAccessKeys(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	schema := s.application.Administration.AccessKeys
	values := request.URL.Query()
	search := strings.TrimSpace(values.Get(schema.API.Query.Search))
	if utf8.RuneCountInString(search) > schema.Limits.SearchMaxLength {
		s.writeAdministrationAccessKeyError(writer, http.StatusBadRequest, schema.Messages.InvalidRequest)
		return
	}
	page, err := s.administrationAccessKeys.List(
		request.Context(),
		s.administrationActor(request, principal),
		administration.AccessKeyQuery{
			Page:     administrationAuditLogInteger(values.Get(schema.API.Query.Page), 1),
			PageSize: administrationAuditLogInteger(values.Get(schema.API.Query.PageSize), schema.Limits.DefaultPageSize),
			Search:   search,
			OwnerID:  strings.TrimSpace(values.Get(schema.API.Query.OwnerID)),
		},
	)
	if err != nil {
		s.logger.Error("list administration access keys", "error", err)
		s.writeAdministrationAccessKeyError(writer, http.StatusInternalServerError, schema.Messages.InternalError)
		return
	}
	enabled, err := s.administrationAccessKeys.Enabled(request.Context())
	if err != nil {
		s.logger.Error("read administration access key switch", "error", err)
		s.writeAdministrationAccessKeyError(writer, http.StatusInternalServerError, schema.Messages.InternalError)
		return
	}
	now := time.Now()
	fields := schema.API.Fields
	records := make([]map[string]any, 0, len(page.Records))
	for _, record := range page.Records {
		records = append(records, administrationAccessKeyWire(fields, record, now))
	}
	s.writeAdministrationAccessKeyJSON(writer, http.StatusOK, map[string]any{
		fields.Data:     records,
		fields.Total:    page.Total,
		fields.Page:     page.Page,
		fields.PageSize: page.PageSize,
		fields.Enabled:  enabled,
	})
}

func administrationAccessKeyWire(
	fields catalog.AdministrationAccessKeyFieldSchema,
	record administration.AccessKeyRecord,
	now time.Time,
) map[string]any {
	optional := func(value time.Time) any {
		if value.IsZero() {
			return nil
		}
		return value.UTC()
	}
	return map[string]any{
		fields.ID:            record.ID,
		fields.OwnerID:       record.OwnerID,
		fields.OwnerUsername: record.OwnerUsername,
		fields.OwnerName:     record.OwnerName,
		fields.Name:          record.Name,
		fields.Prefix:        record.Prefix,
		fields.Scopes:        append([]string{}, record.Scopes...),
		fields.CreatedAt:     record.CreatedAt.UTC(),
		fields.LastUsedAt:    optional(record.LastUsedAt),
		fields.RequestCount:  record.RequestCount,
		fields.ExpiresAt:     optional(record.ExpiresAt),
		fields.RevokedAt:     optional(record.RevokedAt),
		fields.Active:        record.Active(now),
	}
}

func (s *Server) revokeAdministrationAccessKey(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	schema := s.application.Administration.AccessKeys
	_, _, err := s.administrationAccessKeys.Revoke(
		request.Context(),
		s.administrationActor(request, principal),
		request.PathValue(schema.API.PathParameters.KeyID),
	)
	switch {
	case err == nil:
	case errors.Is(err, administration.ErrAccessKeyNotFound):
		s.writeAdministrationAccessKeyError(writer, http.StatusNotFound, schema.Messages.KeyNotFound)
		return
	default:
		s.logger.Error("revoke administration access key", "error", err)
		s.writeAdministrationAccessKeyError(writer, http.StatusInternalServerError, schema.Messages.InternalError)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) setAdministrationAccessKeysSwitch(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	schema := s.application.Administration.AccessKeys
	raw, err := s.decodeAdministrationUserObject(request)
	if err != nil {
		s.writeAdministrationAccessKeyError(writer, http.StatusBadRequest, schema.Messages.InvalidRequest)
		return
	}
	enabled, exists, valid := administrationOptionalBool(raw, schema.API.Fields.Enabled)
	if !exists || !valid {
		s.writeAdministrationAccessKeyError(writer, http.StatusBadRequest, schema.Messages.InvalidRequest)
		return
	}
	state, err := s.administrationAccessKeys.SetEnabled(
		request.Context(),
		s.administrationActor(request, principal),
		enabled,
	)
	if err != nil {
		s.logger.Error("set administration access key switch", "error", err)
		s.writeAdministrationAccessKeyError(writer, http.StatusInternalServerError, schema.Messages.InternalError)
		return
	}
	s.writeAdministrationAccessKeyJSON(writer, http.StatusOK, map[string]any{schema.API.Fields.Enabled: state})
}

func (s *Server) writeAdministrationAccessKeyError(writer http.ResponseWriter, status int, message string) {
	s.writeAdministrationAccessKeyJSON(writer, status, map[string]any{
		s.application.Administration.AccessKeys.API.Fields.Error: message,
	})
}

func (s *Server) writeAdministrationAccessKeyJSON(writer http.ResponseWriter, status int, body map[string]any) {
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, status, body)
}
